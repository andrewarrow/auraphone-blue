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
		conn:       conn,
		remoteUUID: peerUUID,
		role:       RolePeripheral,
		mtu:        DefaultMTU, // Start with default MTU
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
		conn:       conn,
		remoteUUID: peerUUID,
		role:       RoleCentral,
		mtu:        DefaultMTU, // Start with default MTU
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
		mtuReq := &att.ExchangeMTURequest{
			ClientRxMTU: uint16(MaxMTU), // Request our maximum MTU
		}
		err := w.sendATTPacket(peerUUID, mtuReq)
		if err != nil {
			logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "❌ Failed to send MTU request to %s: %v", shortHash(peerUUID), err)
		} else {
			logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "📤 MTU Request to %s: client_mtu=%d", shortHash(peerUUID), MaxMTU)
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

		default:
			logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "⚠️  Unsupported L2CAP channel 0x%04X from %s", l2capPacket.ChannelID, shortHash(peerUUID))
		}
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

	case *att.ReadRequest, *att.WriteRequest, *att.WriteCommand,
		*att.ReadResponse, *att.WriteResponse, *att.ErrorResponse,
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
	handle := w.uuidToHandle(msg.ServiceUUID, msg.CharacteristicUUID)
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
			// Map UUID to handle (simplified for now)
			handle := w.uuidToHandle(msg.ServiceUUID, msg.CharacteristicUUID)
			attPacket = &att.WriteRequest{
				Handle: handle,
				Value:  msg.Data,
			}
		case "read":
			// Map UUID to handle
			handle := w.uuidToHandle(msg.ServiceUUID, msg.CharacteristicUUID)
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
		// Map UUID to handle
		handle := w.uuidToHandle(msg.ServiceUUID, msg.CharacteristicUUID)
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

// uuidToHandle converts a service+characteristic UUID pair to a handle
// This is a temporary simplified implementation
// TODO: Implement proper GATT handle database with discovery
func (w *Wire) uuidToHandle(serviceUUID, charUUID string) uint16 {
	// Simple hash-based mapping for now
	// In real BLE, handles are discovered via GATT service discovery
	hash := 0
	for i := 0; i < len(serviceUUID) && i < len(charUUID); i++ {
		hash = hash*31 + int(serviceUUID[i]) + int(charUUID[i])
	}
	// Map to handle range 0x0001-0xFFFF (0x0000 is reserved)
	handle := uint16((hash % 0xFFFE) + 1)
	logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "   UUID->Handle mapping: svc=%s, char=%s -> 0x%04X",
		shortHash(serviceUUID), shortHash(charUUID), handle)
	return handle
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
