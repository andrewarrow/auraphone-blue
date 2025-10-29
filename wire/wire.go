package wire

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/util"
	"github.com/user/auraphone-blue/wire/att"
	"github.com/user/auraphone-blue/wire/debug"
	"github.com/user/auraphone-blue/wire/gatt"
	"github.com/user/auraphone-blue/wire/l2cap"
)

// Real BLE behavior: advertising data and GATT tables are stored per-device
// and discovered via filesystem scanning (simulates over-the-air discovery)
// No global registries - each device reads/writes its own files

// Wire handles Unix domain socket communication with BLE realism
// Single socket per device at {dataDir}/sockets/auraphone-{hardwareUUID}.sock
type Wire struct {
	hardwareUUID string
	socketPath   string
	listener     net.Listener
	connections  map[string]*Connection // peer UUID -> single connection
	mu           sync.RWMutex

	// Message handler for incoming GATT messages
	gattHandler func(peerUUID string, msg *GATTMessage)
	handlerMu   sync.RWMutex

	// Connection callbacks
	connectCallback    func(peerUUID string, role ConnectionRole)
	disconnectCallback func(peerUUID string)
	callbackMu         sync.RWMutex

	// Stop channels
	stopListening chan struct{}
	stopReading   map[string]chan struct{}
	stopMu        sync.RWMutex

	// Audit logging
	connectionEventLog  *ConnectionEventLogger
	socketHealthMonitor *SocketHealthMonitor

	// Debug logging (binary protocol packets)
	debugLogger *debug.DebugLogger

	// GATT attribute database (server-side)
	attributeDB *gatt.AttributeDatabase
	dbMu        sync.RWMutex
}

// NewWire creates a new Wire instance
func NewWire(hardwareUUID string) *Wire {
	socketDir := util.GetSocketDir()
	w := &Wire{
		hardwareUUID: hardwareUUID,
		socketPath:   filepath.Join(socketDir, fmt.Sprintf("auraphone-%s.sock", hardwareUUID)),
		connections:  make(map[string]*Connection),
		stopReading:  make(map[string]chan struct{}),
	}

	// Initialize audit loggers
	w.connectionEventLog = NewConnectionEventLogger(hardwareUUID, true)
	w.socketHealthMonitor = NewSocketHealthMonitor(hardwareUUID)

	// Initialize debug logger (enabled by default, check env var or can be disabled)
	debugEnabled := os.Getenv("WIRE_DEBUG") != "0" // Enabled unless explicitly disabled
	w.debugLogger = debug.NewDebugLogger(hardwareUUID, debugEnabled)

	// Initialize GATT attribute database
	w.attributeDB = gatt.NewAttributeDatabase()

	return w
}

// Start begins listening on the Unix domain socket
func (w *Wire) Start() error {
	// Clean up any existing socket file
	os.Remove(w.socketPath)

	// Create listener
	listener, err := net.Listen("unix", w.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", w.socketPath, err)
	}

	w.listener = listener
	w.stopListening = make(chan struct{})

	// Log socket creation and initialize health monitoring
	w.connectionEventLog.LogSocketCreated("peripheral", w.socketPath)
	w.socketHealthMonitor.InitializeSocket("peripheral", w.socketPath)
	w.socketHealthMonitor.StartPeriodicSnapshots()

	// Accept incoming connections
	go w.acceptConnections()

	return nil
}

// Stop stops the wire and cleans up resources (idempotent - safe to call multiple times)
func (w *Wire) Stop() {
	// Stop accepting new connections (check if already stopped)
	w.mu.Lock()
	if w.stopListening != nil {
		select {
		case <-w.stopListening:
			// Already stopped
			w.mu.Unlock()
			return
		default:
			close(w.stopListening)
		}
	}
	w.mu.Unlock()

	// Close listener
	if w.listener != nil {
		w.listener.Close()
	}

	// Close all connections
	w.mu.Lock()
	for uuid, connection := range w.connections {
		// Stop reading goroutine
		w.stopMu.Lock()
		if stopChan, exists := w.stopReading[uuid]; exists {
			select {
			case <-stopChan:
				// Already closed
			default:
				close(stopChan)
			}
			delete(w.stopReading, uuid)
		}
		w.stopMu.Unlock()

		connection.conn.Close()
	}
	w.connections = make(map[string]*Connection)
	w.mu.Unlock()

	// Stop health monitor and log socket closure
	if w.socketHealthMonitor != nil {
		w.socketHealthMonitor.MarkSocketClosed("peripheral")
		w.socketHealthMonitor.Stop()
	}
	w.connectionEventLog.LogSocketClosed("peripheral", "", "", "shutdown")

	// Clean up socket file
	os.Remove(w.socketPath)
}

// acceptConnections handles incoming connections
func (w *Wire) acceptConnections() {
	for {
		select {
		case <-w.stopListening:
			return
		default:
		}

		conn, err := w.listener.Accept()
		if err != nil {
			// Check if we're stopping
			select {
			case <-w.stopListening:
				return
			default:
			}
			continue
		}

		// Read handshake: 4-byte UUID length + UUID bytes
		go w.handleIncomingConnection(conn)
	}
}

// handleIncomingConnection processes a new incoming connection (we become Peripheral)
func (w *Wire) handleIncomingConnection(conn net.Conn) {
	// Simulate connection establishment delay (real BLE takes 30-100ms)
	time.Sleep(randomDelay(MinConnectionDelay, MaxConnectionDelay))

	// Read UUID length (4 bytes)
	var uuidLen uint32
	err := binary.Read(conn, binary.BigEndian, &uuidLen)
	if err != nil {
		conn.Close()
		return
	}

	// Read UUID
	uuidBytes := make([]byte, uuidLen)
	_, err = io.ReadFull(conn, uuidBytes)
	if err != nil {
		conn.Close()
		return
	}

	peerUUID := string(uuidBytes)

	// Log connection accepted
	w.connectionEventLog.LogConnectionAccepted("peripheral", peerUUID, "")

	// Check if already connected
	w.mu.Lock()
	if _, exists := w.connections[peerUUID]; exists {
		w.mu.Unlock()
		conn.Close()
		return
	}

	// Store connection with Peripheral role (they initiated)
	connection := &Connection{
		conn:            conn,
		remoteUUID:      peerUUID,
		role:            RolePeripheral,
		mtu:             DefaultMTU,                            // Start with default MTU
		fragmenter:      att.NewFragmenter(),                   // Initialize fragmenter for long writes
		requestTracker:  att.NewRequestTracker(0),              // Initialize request tracker with default 30s timeout
		params:          l2cap.DefaultConnectionParameters(),   // Start with default connection parameters
		paramsUpdatedAt: time.Now(),
		discoveryCache:  gatt.NewDiscoveryCache(),              // Initialize discovery cache for client-side discovery
	}
	w.connections[peerUUID] = connection
	w.mu.Unlock()

	// Log connection established and record in health monitor
	w.connectionEventLog.LogConnectionEstablished("peripheral", peerUUID, "", "")
	w.socketHealthMonitor.RecordConnection("peripheral", peerUUID)

	// Notify callback
	w.callbackMu.RLock()
	connectCb := w.connectCallback
	w.callbackMu.RUnlock()
	if connectCb != nil {
		connectCb(peerUUID, RolePeripheral)
	}

	// Log read loop started
	w.connectionEventLog.LogReadLoopStarted("peripheral", peerUUID, "")

	// Start reading messages from this connection
	stopChan := make(chan struct{})
	w.stopMu.Lock()
	w.stopReading[peerUUID] = stopChan
	w.stopMu.Unlock()

	w.readMessages(peerUUID, connection, stopChan)
}

// Connect establishes a connection to a peer (we become Central)
// Returns error if already connected or if concurrent connection is detected
func (w *Wire) Connect(peerUUID string) error {
	// Check if already connected
	w.mu.RLock()
	_, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if exists {
		return fmt.Errorf("already connected to %s", peerUUID)
	}

	// Simulate connection establishment delay (real BLE takes 30-100ms)
	time.Sleep(randomDelay(MinConnectionDelay, MaxConnectionDelay))

	// Connect to peer's socket
	socketDir := util.GetSocketDir()
	peerSocketPath := filepath.Join(socketDir, fmt.Sprintf("auraphone-%s.sock", peerUUID))
	conn, err := net.Dial("unix", peerSocketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", peerUUID, err)
	}

	// Send handshake: our UUID
	uuidBytes := []byte(w.hardwareUUID)
	uuidLen := uint32(len(uuidBytes))

	err = binary.Write(conn, binary.BigEndian, uuidLen)
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to send handshake: %w", err)
	}

	_, err = conn.Write(uuidBytes)
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to send handshake: %w", err)
	}

	// Store connection with Central role (we initiated)
	connection := &Connection{
		conn:            conn,
		remoteUUID:      peerUUID,
		role:            RoleCentral,
		mtu:             DefaultMTU,                            // Start with default MTU
		fragmenter:      att.NewFragmenter(),                   // Initialize fragmenter for long writes
		requestTracker:  att.NewRequestTracker(0),              // Initialize request tracker with default 30s timeout
		params:          l2cap.DefaultConnectionParameters(),   // Start with default connection parameters
		paramsUpdatedAt: time.Now(),
		discoveryCache:  gatt.NewDiscoveryCache(),              // Initialize discovery cache for client-side discovery
	}

	w.mu.Lock()
	// Check again inside the lock to prevent race condition
	if _, exists := w.connections[peerUUID]; exists {
		w.mu.Unlock()
		conn.Close()
		return fmt.Errorf("concurrent connection detected - another goroutine already connected to %s", peerUUID)
	}
	w.connections[peerUUID] = connection
	w.mu.Unlock()

	// Log connection established and record in health monitor
	w.connectionEventLog.LogConnectionEstablished("central", peerUUID, "", peerSocketPath)
	w.socketHealthMonitor.RecordConnection("central", peerUUID)

	// Notify callback
	w.callbackMu.RLock()
	connectCb := w.connectCallback
	w.callbackMu.RUnlock()
	if connectCb != nil {
		connectCb(peerUUID, RoleCentral)
	}

	// Log read loop started
	w.connectionEventLog.LogReadLoopStarted("central", peerUUID, "")

	// Start reading messages from this connection
	stopChan := make(chan struct{})
	w.stopMu.Lock()
	w.stopReading[peerUUID] = stopChan
	w.stopMu.Unlock()

	go w.readMessages(peerUUID, connection, stopChan)

	// Initiate MTU exchange as Central (we initiated the connection)
	// In real BLE, the Central typically initiates MTU negotiation
	go func() {
		time.Sleep(10 * time.Millisecond) // Small delay to ensure read loop is running

		// Start request tracking
		w.mu.RLock()
		conn := w.connections[peerUUID]
		w.mu.RUnlock()
		if conn == nil || conn.requestTracker == nil {
			return
		}
		tracker := conn.requestTracker.(*att.RequestTracker)
		responseC, err := tracker.StartRequest(att.OpExchangeMTURequest, 0, 0)
		if err != nil {
			logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "❌ Failed to start MTU request tracking: %v", err)
			return
		}

		// Send MTU request
		mtuReq := &att.ExchangeMTURequest{
			ClientRxMTU: uint16(MaxMTU), // Request our maximum MTU
		}
		err = w.sendATTPacket(peerUUID, mtuReq)
		if err != nil {
			logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "❌ Failed to send MTU request to %s: %v", shortHash(peerUUID), err)
			tracker.FailRequest(err)
			return
		}
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "📤 MTU Request to %s: client_mtu=%d", shortHash(peerUUID), MaxMTU)

		// Wait for response (with timeout)
		select {
		case resp := <-responseC:
			if resp.Error != nil {
				logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "❌ MTU exchange with %s failed: %v", shortHash(peerUUID), resp.Error)
			}
			// Success is already logged in handleATTPacket
		}
	}()

	return nil
}

// readMessages continuously reads messages from a connection
func (w *Wire) readMessages(peerUUID string, connection *Connection, stopChan chan struct{}) {
	defer func() {
		// Log read loop ended
		socketType := string(connection.role)
		if connection.role == RolePeripheral {
			socketType = "peripheral"
		} else {
			socketType = "central"
		}
		w.connectionEventLog.LogReadLoopEnded(socketType, peerUUID, "", "connection closed")
		w.socketHealthMonitor.RemoveConnection(socketType, peerUUID)

		// Clean up on exit
		w.mu.Lock()
		delete(w.connections, peerUUID)
		w.mu.Unlock()

		w.stopMu.Lock()
		delete(w.stopReading, peerUUID)
		w.stopMu.Unlock()

		connection.conn.Close()

		// Notify disconnect callback
		w.callbackMu.RLock()
		disconnectCb := w.disconnectCallback
		w.callbackMu.RUnlock()
		if disconnectCb != nil {
			disconnectCb(peerUUID)
		}
	}()

	for {
		select {
		case <-stopChan:
			return
		default:
		}

		// Read L2CAP packet length (2 bytes, little-endian)
		var l2capLen uint16
		err := binary.Read(connection.conn, binary.LittleEndian, &l2capLen)
		if err != nil {
			return // Connection closed or error
		}

		// Read the rest of the L2CAP header and payload
		// Total packet size = 4 bytes header (2 len + 2 channel) + payload
		packetData := make([]byte, l2cap.L2CAPHeaderLen+int(l2capLen))
		binary.LittleEndian.PutUint16(packetData[0:2], l2capLen)

		_, err = io.ReadFull(connection.conn, packetData[2:])
		if err != nil {
			return // Connection closed or error
		}

		// Decode L2CAP packet
		l2capPacket, err := l2cap.Decode(packetData)
		if err != nil {
			logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "❌ Failed to decode L2CAP packet from %s: %v", shortHash(peerUUID), err)
			continue
		}

		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "📥 Received L2CAP packet from %s: channel=0x%04X, len=%d bytes",
			shortHash(peerUUID), l2capPacket.ChannelID, len(l2capPacket.Payload))

		// Debug log: L2CAP packet received
		w.debugLogger.LogL2CAPPacket("rx", peerUUID, l2capPacket)

		// Track message received in health monitor
		socketType := string(connection.role)
		if connection.role == RolePeripheral {
			socketType = "peripheral"
		} else {
			socketType = "central"
		}
		w.socketHealthMonitor.RecordMessageReceived(socketType, peerUUID)

		// Route based on L2CAP channel
		switch l2capPacket.ChannelID {
		case l2cap.ChannelATT:
			// Decode ATT packet
			attPacket, err := att.DecodePacket(l2capPacket.Payload)
			if err != nil {
				logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "❌ Failed to decode ATT packet from %s: %v", shortHash(peerUUID), err)
				continue
			}

			// Debug log: ATT packet received
			w.debugLogger.LogATTPacket("rx", peerUUID, attPacket, l2capPacket.Payload)

			// Handle ATT packet
			w.handleATTPacket(peerUUID, connection, attPacket)

		case l2cap.ChannelLESignal:
			// Handle L2CAP LE signaling channel (connection parameter updates)
			w.handleL2CAPSignaling(peerUUID, connection, l2capPacket.Payload)

		default:
			logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "⚠️  Unsupported L2CAP channel 0x%04X from %s", l2capPacket.ChannelID, shortHash(peerUUID))
		}
	}
}

// handleL2CAPSignaling processes L2CAP LE signaling channel packets
// This handles connection parameter update requests/responses
func (w *Wire) handleL2CAPSignaling(peerUUID string, connection *Connection, payload []byte) {
	if len(payload) < 4 {
		logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "⚠️  L2CAP signaling packet too short from %s", shortHash(peerUUID))
		return
	}

	commandCode := payload[0]

	switch commandCode {
	case l2cap.CodeConnectionParameterUpdateRequest:
		// Peer is requesting connection parameter update
		req, err := l2cap.DecodeConnectionParameterUpdateRequest(payload)
		if err != nil {
			logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "❌ Failed to decode connection parameter request from %s: %v", shortHash(peerUUID), err)
			return
		}

		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"📥 Connection parameter update request from %s: interval=%.1f-%.1fms, latency=%d, timeout=%dms",
			shortHash(peerUUID), req.Params.IntervalMinMs(), req.Params.IntervalMaxMs(),
			req.Params.SlaveLatency, req.Params.SupervisionTimeoutMs())

		// In real BLE, only the Central can accept or reject parameter update requests
		// Peripheral receiving a request is a protocol violation
		result := l2cap.ConnectionParameterRejected
		if connection.role == RoleCentral {
			// Central can accept or reject based on its policy
			// For simulation, accept if parameters are valid
			result = l2cap.ConnectionParameterAccepted
			connection.params = req.Params
			connection.paramsUpdatedAt = time.Now()
			logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
				"✅ Central accepted parameter update from %s", shortHash(peerUUID))
		} else {
			// Peripheral receiving a request is a protocol error
			logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
				"⚠️  Peripheral received connection parameter update request from %s - rejecting (protocol violation)",
				shortHash(peerUUID))
		}

		// Send response
		resp := &l2cap.ConnectionParameterUpdateResponse{
			Identifier: req.Identifier,
			Result:     result,
		}

		respData := l2cap.EncodeConnectionParameterUpdateResponse(resp)
		respPacket := &l2cap.Packet{
			ChannelID: l2cap.ChannelLESignal,
			Payload:   respData,
		}

		if err := w.sendL2CAPPacket(peerUUID, respPacket); err != nil {
			logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "❌ Failed to send connection parameter response to %s: %v", shortHash(peerUUID), err)
			return
		}

		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"📤 Connection parameter update response to %s: %s",
			shortHash(peerUUID), map[uint16]string{
				l2cap.ConnectionParameterAccepted: "accepted",
				l2cap.ConnectionParameterRejected: "rejected",
			}[result])

	case l2cap.CodeConnectionParameterUpdateResponse:
		// Peer responded to our connection parameter update request
		resp, err := l2cap.DecodeConnectionParameterUpdateResponse(payload)
		if err != nil {
			logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "❌ Failed to decode connection parameter response from %s: %v", shortHash(peerUUID), err)
			return
		}

		resultStr := "rejected"
		if resp.Result == l2cap.ConnectionParameterAccepted {
			resultStr = "accepted"
		}

		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"📥 Connection parameter update response from %s: %s",
			shortHash(peerUUID), resultStr)

	default:
		logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "⚠️  Unsupported L2CAP signaling command 0x%02X from %s", commandCode, shortHash(peerUUID))
	}
}

// handleATTPacket processes an incoming ATT packet
func (w *Wire) handleATTPacket(peerUUID string, connection *Connection, packet interface{}) {
	switch p := packet.(type) {
	case *att.ExchangeMTURequest:
		// Peer is requesting MTU exchange
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "📥 MTU Request from %s: client_mtu=%d", shortHash(peerUUID), p.ClientRxMTU)

		// Determine the MTU to use (minimum of client and our max)
		negotiatedMTU := int(p.ClientRxMTU)
		if negotiatedMTU > MaxMTU {
			negotiatedMTU = MaxMTU
		}
		if negotiatedMTU < l2cap.MinMTU {
			negotiatedMTU = l2cap.MinMTU
		}

		// Update connection MTU
		connection.mtu = negotiatedMTU

		// Send MTU response
		response := &att.ExchangeMTUResponse{
			ServerRxMTU: uint16(negotiatedMTU),
		}
		err := w.sendATTPacket(peerUUID, response)
		if err != nil {
			logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "❌ Failed to send MTU response to %s: %v", shortHash(peerUUID), err)
		}
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "📤 MTU Response to %s: server_mtu=%d", shortHash(peerUUID), negotiatedMTU)

	case *att.ExchangeMTUResponse:
		// Peer responded to our MTU request
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "📥 MTU Response from %s: server_mtu=%d", shortHash(peerUUID), p.ServerRxMTU)

		// Complete the pending MTU request
		if connection.requestTracker != nil {
			tracker := connection.requestTracker.(*att.RequestTracker)
			err := tracker.CompleteRequest(att.OpExchangeMTUResponse, p)
			if err != nil {
				logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "⚠️  MTU response without pending request: %v", err)
			}
		}

		// Determine the MTU to use (minimum of server and our request)
		negotiatedMTU := int(p.ServerRxMTU)
		if negotiatedMTU > MaxMTU {
			negotiatedMTU = MaxMTU
		}
		if negotiatedMTU < l2cap.MinMTU {
			negotiatedMTU = l2cap.MinMTU
		}

		// Update connection MTU
		connection.mtu = negotiatedMTU
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "✅ MTU negotiated with %s: %d bytes", shortHash(peerUUID), negotiatedMTU)

	case *att.PrepareWriteRequest:
		// Peer is sending a prepare write fragment
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"📥 Prepare Write Request from %s: handle=0x%04X, offset=%d, len=%d",
			shortHash(peerUUID), p.Handle, p.Offset, len(p.Value))

		// Add to fragmenter queue
		fragmenter := connection.fragmenter.(*att.Fragmenter)
		resp := &att.PrepareWriteResponse{
			Handle: p.Handle,
			Offset: p.Offset,
			Value:  p.Value, // Echo back the value
		}
		err := fragmenter.AddPrepareWriteResponse(resp)
		if err != nil {
			logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
				"❌ Failed to add prepare write fragment: %v", err)
			// Send error response
			errorResp := &att.ErrorResponse{
				RequestOpcode: att.OpPrepareWriteRequest,
				Handle:        p.Handle,
				ErrorCode:     att.ErrInvalidOffset,
			}
			w.sendATTPacket(peerUUID, errorResp)
			return
		}

		// Send echo response
		w.sendATTPacket(peerUUID, resp)

	case *att.ExecuteWriteRequest:
		// Peer is executing (committing) or canceling the prepared writes
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"📥 Execute Write Request from %s: flags=0x%02X", shortHash(peerUUID), p.Flags)

		fragmenter := connection.fragmenter.(*att.Fragmenter)

		if p.Flags == 0x01 {
			// Execute (commit) - get all queued handles and reassemble
			// For now, we'll assume there's only one handle being written to
			// TODO: Track which handle is being written in the prepare queue
			// This is a simplification - in a real implementation, we'd need to track
			// multiple handles and deliver each reassembled value separately

			// For now, just clear the queue and send success
			// The actual write will be handled when we implement proper handle tracking
			fragmenter.ClearAllQueues()

			logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
				"✅ Execute write committed")
		} else {
			// Cancel - clear the prepare queue
			fragmenter.ClearAllQueues()
			logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
				"❌ Execute write canceled")
		}

		// Send execute write response
		resp := &att.ExecuteWriteResponse{}
		w.sendATTPacket(peerUUID, resp)

	case *att.PrepareWriteResponse:
		// Response to our prepare write request
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"📥 Prepare Write Response from %s: handle=0x%04X, offset=%d, len=%d",
			shortHash(peerUUID), p.Handle, p.Offset, len(p.Value))

		// Complete the pending prepare write request
		if connection.requestTracker != nil {
			tracker := connection.requestTracker.(*att.RequestTracker)
			err := tracker.CompleteRequest(att.OpPrepareWriteResponse, p)
			if err != nil {
				logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "⚠️  Prepare write response without pending request: %v", err)
			}
		}

	case *att.ExecuteWriteResponse:
		// Response to our execute write request
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"✅ Execute Write Response from %s", shortHash(peerUUID))

		// Complete the pending execute write request
		if connection.requestTracker != nil {
			tracker := connection.requestTracker.(*att.RequestTracker)
			err := tracker.CompleteRequest(att.OpExecuteWriteResponse, p)
			if err != nil {
				logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "⚠️  Execute write response without pending request: %v", err)
			}
		}

	case *att.ReadResponse:
		// Complete pending read request
		if connection.requestTracker != nil {
			tracker := connection.requestTracker.(*att.RequestTracker)
			err := tracker.CompleteRequest(att.OpReadResponse, p)
			if err != nil {
				logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "⚠️  Read response without pending request: %v", err)
			}
		}
		// Also pass to GATT handler for backward compatibility
		msg := w.attToGATTMessage(packet)
		if msg != nil {
			w.handlerMu.RLock()
			handler := w.gattHandler
			w.handlerMu.RUnlock()
			if handler != nil {
				handler(peerUUID, msg)
			}
		}

	case *att.WriteResponse:
		// Complete pending write request
		if connection.requestTracker != nil {
			tracker := connection.requestTracker.(*att.RequestTracker)
			err := tracker.CompleteRequest(att.OpWriteResponse, p)
			if err != nil {
				logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "⚠️  Write response without pending request: %v", err)
			}
		}
		// Also pass to GATT handler for backward compatibility
		msg := w.attToGATTMessage(packet)
		if msg != nil {
			w.handlerMu.RLock()
			handler := w.gattHandler
			w.handlerMu.RUnlock()
			if handler != nil {
				handler(peerUUID, msg)
			}
		}

	case *att.ErrorResponse:
		// Complete pending request with error
		if connection.requestTracker != nil {
			tracker := connection.requestTracker.(*att.RequestTracker)
			err := tracker.CompleteRequest(att.OpErrorResponse, p)
			if err != nil {
				logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "⚠️  Error response without pending request: %v", err)
			}
		}
		// Also pass to GATT handler for backward compatibility
		msg := w.attToGATTMessage(packet)
		if msg != nil {
			w.handlerMu.RLock()
			handler := w.gattHandler
			w.handlerMu.RUnlock()
			if handler != nil {
				handler(peerUUID, msg)
			}
		}

	case *att.ReadByGroupTypeRequest:
		// Service discovery request
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"📥 Read By Group Type Request from %s: handles=0x%04X-0x%04X",
			shortHash(peerUUID), p.StartHandle, p.EndHandle)

		// Discover services from our attribute database
		w.dbMu.RLock()
		services := gatt.DiscoverServicesFromDatabase(w.attributeDB, p.StartHandle, p.EndHandle)
		w.dbMu.RUnlock()

		if len(services) == 0 {
			// No services found - send error response
			errorResp := &att.ErrorResponse{
				RequestOpcode: att.OpReadByGroupTypeRequest,
				Handle:        p.StartHandle,
				ErrorCode:     att.ErrAttributeNotFound,
			}
			w.sendATTPacket(peerUUID, errorResp)
			logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
				"📤 Error Response: Attribute Not Found")
		} else {
			// Build and send response
			responseData, err := gatt.BuildReadByGroupTypeResponse(services)
			if err != nil {
				logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
					"❌ Failed to build service discovery response: %v", err)
				return
			}

			response := &att.ReadByGroupTypeResponse{
				Length:        responseData[0],
				AttributeData: responseData[1:],
			}
			w.sendATTPacket(peerUUID, response)
			logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
				"📤 Service Discovery Response: %d services", len(services))
		}

	case *att.ReadByTypeRequest:
		// Characteristic discovery request
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"📥 Read By Type Request from %s: handles=0x%04X-0x%04X",
			shortHash(peerUUID), p.StartHandle, p.EndHandle)

		// Discover characteristics from our attribute database
		w.dbMu.RLock()
		characteristics := gatt.DiscoverCharacteristicsFromDatabase(w.attributeDB, p.StartHandle, p.EndHandle)
		w.dbMu.RUnlock()

		if len(characteristics) == 0 {
			// No characteristics found - send error response
			errorResp := &att.ErrorResponse{
				RequestOpcode: att.OpReadByTypeRequest,
				Handle:        p.StartHandle,
				ErrorCode:     att.ErrAttributeNotFound,
			}
			w.sendATTPacket(peerUUID, errorResp)
			logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
				"📤 Error Response: Attribute Not Found")
		} else {
			// Build and send response
			responseData, err := gatt.BuildReadByTypeResponse(characteristics)
			if err != nil {
				logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
					"❌ Failed to build characteristic discovery response: %v", err)
				return
			}

			response := &att.ReadByTypeResponse{
				Length:        responseData[0],
				AttributeData: responseData[1:],
			}
			w.sendATTPacket(peerUUID, response)
			logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
				"📤 Characteristic Discovery Response: %d characteristics", len(characteristics))
		}

	case *att.FindInformationRequest:
		// Descriptor discovery request
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"📥 Find Information Request from %s: handles=0x%04X-0x%04X",
			shortHash(peerUUID), p.StartHandle, p.EndHandle)

		// Discover descriptors from our attribute database
		w.dbMu.RLock()
		descriptors := gatt.DiscoverDescriptorsFromDatabase(w.attributeDB, p.StartHandle, p.EndHandle)
		w.dbMu.RUnlock()

		if len(descriptors) == 0 {
			// No descriptors found - send error response
			errorResp := &att.ErrorResponse{
				RequestOpcode: att.OpFindInformationRequest,
				Handle:        p.StartHandle,
				ErrorCode:     att.ErrAttributeNotFound,
			}
			w.sendATTPacket(peerUUID, errorResp)
			logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
				"📤 Error Response: Attribute Not Found")
		} else {
			// Build and send response
			responseData, err := gatt.BuildFindInformationResponse(descriptors)
			if err != nil {
				logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
					"❌ Failed to build descriptor discovery response: %v", err)
				return
			}

			response := &att.FindInformationResponse{
				Format: responseData[0],
				Data:   responseData[1:],
			}
			w.sendATTPacket(peerUUID, response)
			logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
				"📤 Descriptor Discovery Response: %d descriptors", len(descriptors))
		}

	case *att.ReadByGroupTypeResponse:
		// Service discovery response - store in connection's discovery cache
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"📥 Read By Group Type Response from %s", shortHash(peerUUID))

		// Parse the response
		responseData := make([]byte, 1+len(p.AttributeData))
		responseData[0] = p.Length
		copy(responseData[1:], p.AttributeData)

		services, err := gatt.ParseReadByGroupTypeResponse(responseData)
		if err != nil {
			logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
				"⚠️  Failed to parse service discovery response: %v", err)
		} else {
			// Store in discovery cache
			cache := connection.discoveryCache.(*gatt.DiscoveryCache)
			for _, service := range services {
				cache.AddService(service)
			}
			logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
				"✅ Stored %d services in discovery cache", len(services))
		}

		// Complete the pending request
		if connection.requestTracker != nil {
			tracker := connection.requestTracker.(*att.RequestTracker)
			err := tracker.CompleteRequest(att.OpReadByGroupTypeResponse, p)
			if err != nil {
				logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
					"⚠️  Service discovery response without pending request: %v", err)
			}
		}

	case *att.ReadByTypeResponse:
		// Characteristic discovery response - store in connection's discovery cache
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"📥 Read By Type Response from %s", shortHash(peerUUID))

		// Parse the response
		responseData := make([]byte, 1+len(p.AttributeData))
		responseData[0] = p.Length
		copy(responseData[1:], p.AttributeData)

		characteristics, err := gatt.ParseReadByTypeResponse(responseData)
		if err != nil {
			logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
				"⚠️  Failed to parse characteristic discovery response: %v", err)
		} else {
			// Store in discovery cache
			// Determine which service these characteristics belong to based on their handles
			cache := connection.discoveryCache.(*gatt.DiscoveryCache)

			if len(characteristics) > 0 {
				// Find the service that contains these characteristics
				charHandle := characteristics[0].DeclarationHandle
				for _, service := range cache.Services {
					if charHandle >= service.StartHandle && charHandle <= service.EndHandle {
						for _, char := range characteristics {
							cache.AddCharacteristic(service.StartHandle, char)
						}
						logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
							"✅ Stored %d characteristics in discovery cache for service 0x%04X",
							len(characteristics), service.StartHandle)
						break
					}
				}
			}
		}

		// Complete the pending request
		if connection.requestTracker != nil {
			tracker := connection.requestTracker.(*att.RequestTracker)
			err := tracker.CompleteRequest(att.OpReadByTypeResponse, p)
			if err != nil {
				logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
					"⚠️  Characteristic discovery response without pending request: %v", err)
			}
		}

	case *att.FindInformationResponse:
		// Descriptor discovery response - store in connection's discovery cache
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"📥 Find Information Response from %s", shortHash(peerUUID))

		// Parse the response
		responseData := make([]byte, 1+len(p.Data))
		responseData[0] = p.Format
		copy(responseData[1:], p.Data)

		descriptors, err := gatt.ParseFindInformationResponse(responseData)
		if err != nil {
			logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
				"⚠️  Failed to parse descriptor discovery response: %v", err)
		} else {
			// Store in discovery cache
			cache := connection.discoveryCache.(*gatt.DiscoveryCache)
			// TODO: Track which characteristic these descriptors belong to
			// For now, we just store them
			for _, desc := range descriptors {
				cache.AddDescriptor(0x0001, desc) // Placeholder handle
			}
			logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
				"✅ Stored %d descriptors in discovery cache", len(descriptors))
		}

		// Complete the pending request
		if connection.requestTracker != nil {
			tracker := connection.requestTracker.(*att.RequestTracker)
			err := tracker.CompleteRequest(att.OpFindInformationResponse, p)
			if err != nil {
				logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
					"⚠️  Descriptor discovery response without pending request: %v", err)
			}
		}

	case *att.ReadRequest, *att.WriteRequest, *att.WriteCommand,
		*att.HandleValueNotification, *att.HandleValueIndication:
		// These are GATT operations - convert to GATTMessage for compatibility
		// TODO: Remove this conversion once higher layers use binary protocol directly
		msg := w.attToGATTMessage(packet)
		if msg != nil {
			w.handlerMu.RLock()
			handler := w.gattHandler
			w.handlerMu.RUnlock()

			if handler != nil {
				logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "   ➡️  Calling GATT handler for ATT packet from %s", shortHash(peerUUID))
				handler(peerUUID, msg)
			} else {
				logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "⚠️  No GATT handler registered for ATT packet from %s", shortHash(peerUUID))
			}
		}

	default:
		logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "⚠️  Unsupported ATT packet type %T from %s", packet, shortHash(peerUUID))
	}
}

// attToGATTMessage converts an ATT packet to a GATTMessage for backward compatibility
// TODO: Remove this once higher layers use binary protocol directly
func (w *Wire) attToGATTMessage(packet interface{}) *GATTMessage {
	switch p := packet.(type) {
	case *att.ReadRequest:
		// Convert handle back to UUIDs (reverse of uuidToHandle)
		// For now, we use placeholder UUIDs since we don't have a reverse mapping
		return &GATTMessage{
			Type:               "gatt_request",
			Operation:          "read",
			ServiceUUID:        fmt.Sprintf("service-handle-%04x", p.Handle),
			CharacteristicUUID: fmt.Sprintf("char-handle-%04x", p.Handle),
		}

	case *att.ReadResponse:
		return &GATTMessage{
			Type:      "gatt_response",
			Operation: "read",
			Status:    "success",
			Data:      p.Value,
		}

	case *att.WriteRequest:
		return &GATTMessage{
			Type:               "gatt_request",
			Operation:          "write",
			ServiceUUID:        fmt.Sprintf("service-handle-%04x", p.Handle),
			CharacteristicUUID: fmt.Sprintf("char-handle-%04x", p.Handle),
			Data:               p.Value,
		}

	case *att.WriteCommand:
		return &GATTMessage{
			Type:               "gatt_request",
			Operation:          "write",
			ServiceUUID:        fmt.Sprintf("service-handle-%04x", p.Handle),
			CharacteristicUUID: fmt.Sprintf("char-handle-%04x", p.Handle),
			Data:               p.Value,
		}

	case *att.WriteResponse:
		return &GATTMessage{
			Type:      "gatt_response",
			Operation: "write",
			Status:    "success",
		}

	case *att.HandleValueNotification:
		return &GATTMessage{
			Type:               "gatt_notification",
			Operation:          "notify",
			ServiceUUID:        fmt.Sprintf("service-handle-%04x", p.Handle),
			CharacteristicUUID: fmt.Sprintf("char-handle-%04x", p.Handle),
			Data:               p.Value,
		}

	case *att.HandleValueIndication:
		return &GATTMessage{
			Type:               "gatt_notification",
			Operation:          "indicate",
			ServiceUUID:        fmt.Sprintf("service-handle-%04x", p.Handle),
			CharacteristicUUID: fmt.Sprintf("char-handle-%04x", p.Handle),
			Data:               p.Value,
		}

	case *att.ErrorResponse:
		return &GATTMessage{
			Type:      "gatt_response",
			Operation: "unknown",
			Status:    "error",
		}

	default:
		return nil
	}
}

// sendATTPacket sends an ATT packet to a peer
func (w *Wire) sendATTPacket(peerUUID string, packet interface{}) error {
	// Encode ATT packet
	attData, err := att.EncodePacket(packet)
	if err != nil {
		return fmt.Errorf("failed to encode ATT packet: %w", err)
	}

	// Get connection to check MTU
	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()
	if !exists {
		return fmt.Errorf("not connected to %s", peerUUID)
	}

	// Enforce MTU strictly (except for MTU exchange packets and error responses)
	// MTU exchange and error responses are always allowed regardless of MTU
	switch packet.(type) {
	case *att.ExchangeMTURequest, *att.ExchangeMTUResponse, *att.ErrorResponse:
		// These packets are exempt from MTU checks
	default:
		// Check if ATT payload exceeds MTU
		if len(attData) > connection.mtu {
			logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
				"❌ ATT packet exceeds MTU: len=%d, mtu=%d (use fragmentation for large writes)",
				len(attData), connection.mtu)
			return fmt.Errorf("ATT packet exceeds MTU: %d > %d (use Prepare Write + Execute Write for large values)", len(attData), connection.mtu)
		}
	}

	// Debug log: ATT packet sent
	w.debugLogger.LogATTPacket("tx", peerUUID, packet, attData)

	// Wrap in L2CAP packet
	l2capPacket := l2cap.NewATTPacket(attData)

	// Send L2CAP packet
	return w.sendL2CAPPacket(peerUUID, l2capPacket)
}

// sendL2CAPPacket sends an L2CAP packet to a peer
func (w *Wire) sendL2CAPPacket(peerUUID string, packet *l2cap.Packet) error {
	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if !exists {
		logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "❌ sendL2CAPPacket: not connected to %s", shortHash(peerUUID))
		return fmt.Errorf("not connected to %s", peerUUID)
	}

	// Encode L2CAP packet
	data := packet.Encode()

	// Debug log: L2CAP packet sent
	w.debugLogger.LogL2CAPPacket("tx", peerUUID, packet)

	// Simulate connection interval latency (real BLE has 7.5-50ms intervals)
	time.Sleep(connectionIntervalDelay())

	// Lock for thread-safe writes
	connection.sendMutex.Lock()
	defer connection.sendMutex.Unlock()

	// Write packet data
	_, err := connection.conn.Write(data)
	if err != nil {
		return fmt.Errorf("failed to send L2CAP packet: %w", err)
	}

	logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "📡 Sent L2CAP packet to %s: channel=0x%04X, len=%d bytes",
		shortHash(peerUUID), packet.ChannelID, len(data))

	// Track message sent in health monitor
	socketType := string(connection.role)
	if connection.role == RolePeripheral {
		socketType = "peripheral"
	} else {
		socketType = "central"
	}
	w.socketHealthMonitor.RecordMessageSent(socketType, peerUUID)

	return nil
}

// SendGATTMessage sends a GATT message to a peer
// This function converts the high-level GATTMessage to binary ATT packets
func (w *Wire) SendGATTMessage(peerUUID string, msg *GATTMessage) error {
	// Set sender UUID if not already set
	if msg.SenderUUID == "" {
		msg.SenderUUID = w.hardwareUUID
	}

	logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "📡 SendGATTMessage to %s: op=%s, type=%s",
		shortHash(peerUUID), msg.Operation, msg.Type)

	// Debug log: GATT operation
	handle := w.uuidToHandle(peerUUID, msg.ServiceUUID, msg.CharacteristicUUID)
	w.debugLogger.LogGATTOperation("tx", peerUUID, msg.Operation, msg.ServiceUUID, msg.CharacteristicUUID, fmt.Sprintf("0x%04X", handle), msg.Data)

	// Convert GATTMessage to ATT packet
	// For now, we use a simple handle mapping (UUID hash to handle)
	// TODO: Implement proper GATT handle database with discovery
	var attPacket interface{}
	var err error

	switch msg.Type {
	case "gatt_request":
		switch msg.Operation {
		case "write":
			// Map UUID to handle (tries discovery cache first, falls back to hash)
			handle := w.uuidToHandle(peerUUID, msg.ServiceUUID, msg.CharacteristicUUID)

			// Get connection to check MTU
			w.mu.RLock()
			connection, exists := w.connections[peerUUID]
			w.mu.RUnlock()
			if !exists {
				return fmt.Errorf("no connection to peer %s", peerUUID)
			}

			// Check if fragmentation is needed
			if att.ShouldFragment(connection.mtu, msg.Data) {
				// Use Prepare Write + Execute Write for long values
				logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
					"🔀 Fragmenting write (len=%d, mtu=%d)", len(msg.Data), connection.mtu)
				return w.sendFragmentedWrite(peerUUID, handle, msg.Data, connection)
			}

			// Normal write for small values
			attPacket = &att.WriteRequest{
				Handle: handle,
				Value:  msg.Data,
			}
		case "read":
			// Map UUID to handle (tries discovery cache first, falls back to hash)
			handle := w.uuidToHandle(peerUUID, msg.ServiceUUID, msg.CharacteristicUUID)
			attPacket = &att.ReadRequest{
				Handle: handle,
			}
		default:
			return fmt.Errorf("unsupported operation: %s", msg.Operation)
		}

	case "gatt_response":
		switch msg.Status {
		case "success":
			if msg.Operation == "read" {
				attPacket = &att.ReadResponse{
					Value: msg.Data,
				}
			} else if msg.Operation == "write" {
				attPacket = &att.WriteResponse{}
			} else {
				return fmt.Errorf("unsupported response operation: %s", msg.Operation)
			}
		case "error":
			// Generic error response
			attPacket = &att.ErrorResponse{
				RequestOpcode: att.OpReadRequest, // Default, should be set properly
				Handle:        0x0000,
				ErrorCode:     att.ErrAttributeNotFound,
			}
		default:
			return fmt.Errorf("unsupported status: %s", msg.Status)
		}

	case "gatt_notification":
		// Map UUID to handle (tries discovery cache first, falls back to hash)
		handle := w.uuidToHandle(peerUUID, msg.ServiceUUID, msg.CharacteristicUUID)
		attPacket = &att.HandleValueNotification{
			Handle: handle,
			Value:  msg.Data,
		}

	default:
		return fmt.Errorf("unsupported message type: %s", msg.Type)
	}

	// Send the ATT packet
	err = w.sendATTPacket(peerUUID, attPacket)
	if err != nil {
		logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "❌ Failed to send ATT packet: %v", err)
		return err
	}

	return nil
}

// sendFragmentedWrite sends a long write using ATT Prepare Write + Execute Write
// This is used when the value exceeds the negotiated MTU
func (w *Wire) sendFragmentedWrite(peerUUID string, handle uint16, value []byte, connection *Connection) error {
	// Fragment the write into Prepare Write requests
	requests, err := att.FragmentWrite(handle, value, connection.mtu)
	if err != nil {
		return fmt.Errorf("failed to fragment write: %w", err)
	}

	logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
		"   Sending %d prepare write fragments", len(requests))

	// Send each Prepare Write request and wait for response
	for i, req := range requests {
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"   Fragment %d/%d: offset=%d, len=%d", i+1, len(requests), req.Offset, len(req.Value))

		// Send the Prepare Write request
		err = w.sendATTPacket(peerUUID, req)
		if err != nil {
			return fmt.Errorf("failed to send prepare write fragment %d: %w", i, err)
		}

		// In a real implementation, we would wait for the PrepareWriteResponse
		// and verify the server echoed back the correct offset and value
		// For now, we assume success and continue
		// TODO: Add proper request/response tracking with timeouts
	}

	// Send Execute Write request to commit the write
	logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
		"   Sending execute write (commit)")

	executeReq := &att.ExecuteWriteRequest{
		Flags: 0x01, // 0x01 = execute (commit), 0x00 = cancel
	}
	err = w.sendATTPacket(peerUUID, executeReq)
	if err != nil {
		return fmt.Errorf("failed to send execute write: %w", err)
	}

	logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
		"   Fragmented write completed successfully")

	return nil
}

// uuidToHandle converts a service+characteristic UUID pair to a handle
// First tries to use the discovery cache, then falls back to hash-based mapping
func (w *Wire) uuidToHandle(peerUUID, serviceUUID, charUUID string) uint16 {
	// Try to get handle from discovery cache first
	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if exists && connection.discoveryCache != nil {
		cache := connection.discoveryCache.(*gatt.DiscoveryCache)

		// Convert char UUID string to bytes for lookup
		// Try to parse as hex string (e.g., "2A00" -> []byte{0x00, 0x2A})
		charUUIDBytes := stringToUUIDBytes(charUUID)
		if handle, err := cache.GetCharacteristicHandle(charUUIDBytes); err == nil {
			logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
				"   UUID->Handle (from discovery cache): char=%s -> 0x%04X",
				shortHash(charUUID), handle)
			return handle
		}
	}

	// Fall back to hash-based mapping for backward compatibility
	// In real BLE, handles are discovered via GATT service discovery
	hash := 0
	for i := 0; i < len(serviceUUID) && i < len(charUUID); i++ {
		hash = hash*31 + int(serviceUUID[i]) + int(charUUID[i])
	}
	// Map to handle range 0x0001-0xFFFF (0x0000 is reserved)
	handle := uint16((hash % 0xFFFE) + 1)
	logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
		"   UUID->Handle (hash-based fallback): svc=%s, char=%s -> 0x%04X",
		shortHash(serviceUUID), shortHash(charUUID), handle)
	return handle
}

// stringToUUIDBytes converts a UUID string to bytes
// Handles both short (16-bit) and long (128-bit) UUIDs
func stringToUUIDBytes(uuid string) []byte {
	// Remove any dashes or formatting
	cleaned := ""
	for _, c := range uuid {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			cleaned += string(c)
		}
	}

	// Convert hex string to bytes
	bytes := make([]byte, len(cleaned)/2)
	for i := 0; i < len(bytes); i++ {
		var b byte
		if cleaned[i*2] >= '0' && cleaned[i*2] <= '9' {
			b = (cleaned[i*2] - '0') << 4
		} else if cleaned[i*2] >= 'a' && cleaned[i*2] <= 'f' {
			b = (cleaned[i*2] - 'a' + 10) << 4
		} else if cleaned[i*2] >= 'A' && cleaned[i*2] <= 'F' {
			b = (cleaned[i*2] - 'A' + 10) << 4
		}

		if cleaned[i*2+1] >= '0' && cleaned[i*2+1] <= '9' {
			b |= cleaned[i*2+1] - '0'
		} else if cleaned[i*2+1] >= 'a' && cleaned[i*2+1] <= 'f' {
			b |= cleaned[i*2+1] - 'a' + 10
		} else if cleaned[i*2+1] >= 'A' && cleaned[i*2+1] <= 'F' {
			b |= cleaned[i*2+1] - 'A' + 10
		}

		bytes[i] = b
	}

	return bytes
}

// SetGATTMessageHandler sets the callback for incoming GATT messages
func (w *Wire) SetGATTMessageHandler(handler func(peerUUID string, msg *GATTMessage)) {
	w.handlerMu.Lock()
	w.gattHandler = handler
	w.handlerMu.Unlock()
}

// SetConnectCallback sets the callback for when a connection is established
func (w *Wire) SetConnectCallback(callback func(peerUUID string, role ConnectionRole)) {
	w.callbackMu.Lock()
	w.connectCallback = callback
	w.callbackMu.Unlock()
}

// SetDisconnectCallback sets the callback for when a connection is lost
func (w *Wire) SetDisconnectCallback(callback func(peerUUID string)) {
	w.callbackMu.Lock()
	w.disconnectCallback = callback
	w.callbackMu.Unlock()
}

// GetHardwareUUID returns this device's hardware UUID
func (w *Wire) GetHardwareUUID() string {
	return w.hardwareUUID
}

// IsConnected checks if we're connected to a peer
func (w *Wire) IsConnected(peerUUID string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	_, exists := w.connections[peerUUID]
	return exists
}

// GetConnectionRole returns our role in the connection with the peer
func (w *Wire) GetConnectionRole(peerUUID string) (ConnectionRole, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	connection, exists := w.connections[peerUUID]
	if !exists {
		return "", false
	}
	return connection.role, true
}

// GetConnectedPeers returns a list of all connected peer UUIDs
func (w *Wire) GetConnectedPeers() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	peers := make([]string, 0, len(w.connections))
	for uuid := range w.connections {
		peers = append(peers, uuid)
	}
	return peers
}

// Disconnect closes connection to a peer (stub for old API)
// TODO Step 4: Implement graceful disconnect
func (w *Wire) Disconnect(peerUUID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	connection, exists := w.connections[peerUUID]
	if !exists {
		return fmt.Errorf("not connected to %s", peerUUID)
	}

	// In real BLE, disconnect behavior differs by role:
	// - Central can force immediate disconnect
	// - Peripheral can request disconnect, but Central controls timing
	// For simulation, both can disconnect, but we log the semantic difference
	if connection.role == RolePeripheral {
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"🔌 Peripheral requesting disconnect from %s (in real BLE, Central controls timing)",
			shortHash(peerUUID))
	} else {
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"🔌 Central disconnecting from %s (immediate)",
			shortHash(peerUUID))
	}

	// Cancel any pending ATT requests
	if connection.requestTracker != nil {
		tracker := connection.requestTracker.(*att.RequestTracker)
		tracker.CancelPending()
	}

	// Stop reading goroutine
	w.stopMu.Lock()
	if stopChan, exists := w.stopReading[peerUUID]; exists {
		select {
		case <-stopChan:
			// Already closed
		default:
			close(stopChan)
		}
		delete(w.stopReading, peerUUID)
	}
	w.stopMu.Unlock()

	// Close connection
	connection.conn.Close()
	delete(w.connections, peerUUID)

	// Trigger disconnect callback after cleanup
	w.callbackMu.RLock()
	callback := w.disconnectCallback
	w.callbackMu.RUnlock()

	if callback != nil {
		// Call callback asynchronously to avoid blocking and potential deadlocks
		go callback(peerUUID)
	}

	return nil
}

// RequestConnectionParameterUpdate requests new connection parameters from the peer
// This is typically called by the Peripheral to request parameter updates from the Central
// In real BLE, iOS/Android as Central will accept or reject the request
func (w *Wire) RequestConnectionParameterUpdate(peerUUID string, params *l2cap.ConnectionParameters) error {
	if params == nil {
		return fmt.Errorf("nil connection parameters")
	}

	if err := params.Validate(); err != nil {
		return fmt.Errorf("invalid connection parameters: %w", err)
	}

	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if !exists {
		return fmt.Errorf("not connected to %s", peerUUID)
	}

	// In real BLE, only peripherals can request parameter updates via L2CAP signaling
	// The central then accepts or rejects the request
	if connection.role != RolePeripheral {
		return fmt.Errorf("only peripheral can request connection parameter updates (current role: %s)", connection.role)
	}
	logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
		"📶 Requesting connection parameters from %s: interval=%.1f-%.1fms, latency=%d, timeout=%dms",
		shortHash(peerUUID), params.IntervalMinMs(), params.IntervalMaxMs(),
		params.SlaveLatency, params.SupervisionTimeoutMs())

	// Generate identifier for request/response matching
	identifier := uint8(time.Now().UnixNano() & 0xFF)

	req := &l2cap.ConnectionParameterUpdateRequest{
		Identifier: identifier,
		Params:     params,
	}

	data, err := l2cap.EncodeConnectionParameterUpdateRequest(req)
	if err != nil {
		return fmt.Errorf("failed to encode connection parameter request: %w", err)
	}

	// Send via L2CAP signaling channel
	packet := &l2cap.Packet{
		ChannelID: l2cap.ChannelLESignal,
		Payload:   data,
	}

	return w.sendL2CAPPacket(peerUUID, packet)
}

// SetConnectionParameters sets new connection parameters (Central only)
// This allows the Central to unilaterally change connection parameters without peripheral request
// In real BLE, the Central can update parameters at any time to optimize for latency or power
func (w *Wire) SetConnectionParameters(peerUUID string, params *l2cap.ConnectionParameters) error {
	if params == nil {
		return fmt.Errorf("nil connection parameters")
	}

	if err := params.Validate(); err != nil {
		return fmt.Errorf("invalid connection parameters: %w", err)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	connection, exists := w.connections[peerUUID]
	if !exists {
		return fmt.Errorf("not connected to %s", peerUUID)
	}

	// In real BLE, only Central can set connection parameters unilaterally
	// Peripheral must use RequestConnectionParameterUpdate() instead
	if connection.role != RoleCentral {
		return fmt.Errorf("only central can set connection parameters directly (current role: %s)", connection.role)
	}

	connection.params = params
	connection.paramsUpdatedAt = time.Now()

	logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
		"📶 Central set connection parameters for %s: interval=%.1f-%.1fms, latency=%d, timeout=%dms",
		shortHash(peerUUID), params.IntervalMinMs(), params.IntervalMaxMs(),
		params.SlaveLatency, params.SupervisionTimeoutMs())

	return nil
}

// GetConnectionParameters returns the current connection parameters for a peer
func (w *Wire) GetConnectionParameters(peerUUID string) (*l2cap.ConnectionParameters, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	connection, exists := w.connections[peerUUID]
	if !exists {
		return nil, fmt.Errorf("not connected to %s", peerUUID)
	}

	if connection.params == nil {
		// Return default parameters if none have been negotiated
		return l2cap.DefaultConnectionParameters(), nil
	}

	return connection.params.(*l2cap.ConnectionParameters), nil
}

// DiscoverServices initiates GATT service discovery on a connected peer
// This sends a Read By Group Type Request to discover all primary services
// Results are stored in the connection's discovery cache and can be retrieved with GetDiscoveredServices()
func (w *Wire) DiscoverServices(peerUUID string) error {
	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if !exists {
		return fmt.Errorf("not connected to %s", peerUUID)
	}

	logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
		"🔍 Discovering services from %s", shortHash(peerUUID))

	// Simulate realistic discovery delay (iOS/Android: 100-500ms)
	time.Sleep(randomDelay(100*time.Millisecond, 500*time.Millisecond))

	// Clear existing services in discovery cache before starting fresh discovery
	cache := connection.discoveryCache.(*gatt.DiscoveryCache)
	cache.Services = []gatt.DiscoveredService{}

	// Send Read By Group Type Request for primary services
	// Start: 0x0001, End: 0xFFFF, Type: 0x2800 (Primary Service UUID)
	tracker := connection.requestTracker.(*att.RequestTracker)
	responseC, err := tracker.StartRequest(att.OpReadByGroupTypeRequest, 0x0001, 0)
	if err != nil {
		return fmt.Errorf("failed to start service discovery: %w", err)
	}

	req := &att.ReadByGroupTypeRequest{
		StartHandle: 0x0001,
		EndHandle:   0xFFFF,
		Type:        []byte{0x00, 0x28}, // Primary Service UUID (0x2800)
	}

	err = w.sendATTPacket(peerUUID, req)
	if err != nil {
		tracker.FailRequest(err)
		return fmt.Errorf("failed to send service discovery request: %w", err)
	}

	// Wait for response
	select {
	case resp := <-responseC:
		if resp.Error != nil {
			return fmt.Errorf("service discovery failed: %w", resp.Error)
		}
		// Services are already stored in discovery cache by handleATTPacket
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"✅ Service discovery complete: %d services found", len(cache.Services))
		return nil
	case <-time.After(30 * time.Second):
		return fmt.Errorf("service discovery timeout")
	}
}

// DiscoverCharacteristics initiates characteristic discovery for a specific service
// This sends a Read By Type Request to discover all characteristics within the service
// Results are stored in the connection's discovery cache
func (w *Wire) DiscoverCharacteristics(peerUUID string, serviceUUID []byte) error {
	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if !exists {
		return fmt.Errorf("not connected to %s", peerUUID)
	}

	// Find the service in the discovery cache
	cache := connection.discoveryCache.(*gatt.DiscoveryCache)
	var service *gatt.DiscoveredService
	for i := range cache.Services {
		if bytesEqual(cache.Services[i].UUID, serviceUUID) {
			service = &cache.Services[i]
			break
		}
	}

	if service == nil {
		return fmt.Errorf("service not found in discovery cache, run DiscoverServices first")
	}

	logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
		"🔍 Discovering characteristics from %s for service 0x%04X-0x%04X",
		shortHash(peerUUID), service.StartHandle, service.EndHandle)

	// Simulate realistic discovery delay (iOS/Android: 100-500ms)
	time.Sleep(randomDelay(100*time.Millisecond, 500*time.Millisecond))

	// Send Read By Type Request for characteristics
	// Type: 0x2803 (Characteristic Declaration UUID)
	tracker := connection.requestTracker.(*att.RequestTracker)
	responseC, err := tracker.StartRequest(att.OpReadByTypeRequest, service.StartHandle, 0)
	if err != nil {
		return fmt.Errorf("failed to start characteristic discovery: %w", err)
	}

	req := &att.ReadByTypeRequest{
		StartHandle: service.StartHandle,
		EndHandle:   service.EndHandle,
		Type:        []byte{0x03, 0x28}, // Characteristic Declaration UUID (0x2803)
	}

	err = w.sendATTPacket(peerUUID, req)
	if err != nil {
		tracker.FailRequest(err)
		return fmt.Errorf("failed to send characteristic discovery request: %w", err)
	}

	// Wait for response
	select {
	case resp := <-responseC:
		if resp.Error != nil {
			return fmt.Errorf("characteristic discovery failed: %w", resp.Error)
		}
		// Characteristics are already stored in discovery cache by handleATTPacket
		chars := cache.Characteristics[service.StartHandle]
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"✅ Characteristic discovery complete: %d characteristics found", len(chars))
		return nil
	case <-time.After(30 * time.Second):
		return fmt.Errorf("characteristic discovery timeout")
	}
}

// DiscoverDescriptors initiates descriptor discovery for a specific characteristic
// This sends a Find Information Request to discover all descriptors for the characteristic
// Results are stored in the connection's discovery cache
func (w *Wire) DiscoverDescriptors(peerUUID string, charHandle uint16) error {
	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if !exists {
		return fmt.Errorf("not connected to %s", peerUUID)
	}

	// Find the characteristic in the discovery cache to determine handle range
	cache := connection.discoveryCache.(*gatt.DiscoveryCache)
	var startHandle, endHandle uint16

	// Find the characteristic and determine its descriptor range
	// Descriptors are stored between the characteristic value handle + 1 and the next characteristic
	found := false
	for _, chars := range cache.Characteristics {
		for i, char := range chars {
			if char.ValueHandle == charHandle {
				startHandle = char.ValueHandle + 1
				// End handle is either the next characteristic or 0xFFFF
				if i+1 < len(chars) {
					endHandle = chars[i+1].DeclarationHandle - 1
				} else {
					endHandle = 0xFFFF
				}
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		return fmt.Errorf("characteristic handle 0x%04X not found in discovery cache", charHandle)
	}

	if startHandle > endHandle {
		// No descriptors for this characteristic
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"✅ Descriptor discovery complete: 0 descriptors (no descriptor range)")
		return nil
	}

	logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
		"🔍 Discovering descriptors from %s for characteristic 0x%04X (range 0x%04X-0x%04X)",
		shortHash(peerUUID), charHandle, startHandle, endHandle)

	// Simulate realistic discovery delay (iOS/Android: 100-500ms)
	time.Sleep(randomDelay(100*time.Millisecond, 500*time.Millisecond))

	// Send Find Information Request for descriptors
	tracker := connection.requestTracker.(*att.RequestTracker)
	responseC, err := tracker.StartRequest(att.OpFindInformationRequest, startHandle, 0)
	if err != nil {
		return fmt.Errorf("failed to start descriptor discovery: %w", err)
	}

	req := &att.FindInformationRequest{
		StartHandle: startHandle,
		EndHandle:   endHandle,
	}

	err = w.sendATTPacket(peerUUID, req)
	if err != nil {
		tracker.FailRequest(err)
		return fmt.Errorf("failed to send descriptor discovery request: %w", err)
	}

	// Wait for response
	select {
	case resp := <-responseC:
		if resp.Error != nil {
			// Attribute Not Found is OK - means no descriptors
			if attErr, ok := resp.Error.(*att.Error); ok && attErr.Code == att.ErrAttributeNotFound {
				logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
					"✅ Descriptor discovery complete: 0 descriptors")
				return nil
			}
			return fmt.Errorf("descriptor discovery failed: %w", resp.Error)
		}
		// Descriptors are already stored in discovery cache by handleATTPacket
		descs := cache.Descriptors[charHandle]
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"✅ Descriptor discovery complete: %d descriptors found", len(descs))
		return nil
	case <-time.After(30 * time.Second):
		return fmt.Errorf("descriptor discovery timeout")
	}
}

// GetDiscoveredServices returns all services discovered on a connection
func (w *Wire) GetDiscoveredServices(peerUUID string) ([]gatt.DiscoveredService, error) {
	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("not connected to %s", peerUUID)
	}

	cache := connection.discoveryCache.(*gatt.DiscoveryCache)
	return cache.Services, nil
}

// GetDiscoveredCharacteristics returns all characteristics discovered for a service
func (w *Wire) GetDiscoveredCharacteristics(peerUUID string, serviceStartHandle uint16) ([]gatt.DiscoveredCharacteristic, error) {
	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("not connected to %s", peerUUID)
	}

	cache := connection.discoveryCache.(*gatt.DiscoveryCache)
	chars := cache.Characteristics[serviceStartHandle]
	return chars, nil
}

// GetCharacteristicHandle returns the value handle for a characteristic UUID
// This uses the discovery cache to look up the handle
func (w *Wire) GetCharacteristicHandle(peerUUID string, charUUID []byte) (uint16, error) {
	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if !exists {
		return 0, fmt.Errorf("not connected to %s", peerUUID)
	}

	cache := connection.discoveryCache.(*gatt.DiscoveryCache)
	return cache.GetCharacteristicHandle(charUUID)
}

// SetAttributeDatabase sets the GATT attribute database for this device (server-side)
// This is used when acting as a peripheral to expose services to centrals
func (w *Wire) SetAttributeDatabase(db *gatt.AttributeDatabase) {
	w.dbMu.Lock()
	w.attributeDB = db
	w.dbMu.Unlock()
}
