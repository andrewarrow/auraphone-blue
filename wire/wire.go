package wire

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/util"
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

		// Read message length (4 bytes)
		var msgLen uint32
		err := binary.Read(connection.conn, binary.BigEndian, &msgLen)
		if err != nil {
			return // Connection closed or error
		}

		// Read message data (JSON-encoded GATTMessage)
		msgData := make([]byte, msgLen)
		_, err = io.ReadFull(connection.conn, msgData)
		if err != nil {
			return // Connection closed or error
		}

		// Parse GATT message
		var msg GATTMessage
		err = json.Unmarshal(msgData, &msg)
		if err != nil {
			// Invalid message, skip it
			logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "❌ Failed to unmarshal GATT message from %s: %v", shortHash(peerUUID), err)
			continue
		}

		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "📥 Received GATT message from %s: op=%s, len=%d bytes",
			shortHash(peerUUID), msg.Operation, len(msgData))

		// Track message received in health monitor
		socketType := string(connection.role)
		if connection.role == RolePeripheral {
			socketType = "peripheral"
		} else {
			socketType = "central"
		}
		w.socketHealthMonitor.RecordMessageReceived(socketType, peerUUID)

		// Call GATT handler
		w.handlerMu.RLock()
		handler := w.gattHandler
		w.handlerMu.RUnlock()

		if handler != nil {
			logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "   ➡️  Calling GATT handler for message from %s", shortHash(peerUUID))
			handler(peerUUID, &msg)
		} else {
			logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "⚠️  No GATT handler registered for message from %s", shortHash(peerUUID))
		}
	}
}

// SendGATTMessage sends a GATT message to a peer
func (w *Wire) SendGATTMessage(peerUUID string, msg *GATTMessage) error {
	// Set sender UUID if not already set
	if msg.SenderUUID == "" {
		msg.SenderUUID = w.hardwareUUID
	}

	// Marshal message to JSON
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal GATT message: %w", err)
	}

	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if !exists {
		logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "❌ SendGATTMessage: not connected to %s", shortHash(peerUUID))
		return fmt.Errorf("not connected to %s", peerUUID)
	}

	// Enforce MTU limits (real BLE requires manual fragmentation)
	// We allow the full message through for now, but warn if it exceeds MTU
	// In real BLE, messages larger than MTU must be manually fragmented or they fail
	if len(data) > connection.mtu {
		logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"⚠️  Message size (%d bytes) exceeds MTU (%d bytes) - would fail in real BLE without fragmentation",
			len(data), connection.mtu)
		// For now, we'll allow it through since tests expect large messages to work
		// TODO: Make this a hard error once fragmentation is implemented
	}

	logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "📡 SendGATTMessage to %s: op=%s, len=%d bytes",
		shortHash(peerUUID), msg.Operation, len(data))

	// Simulate connection interval latency (real BLE has 7.5-50ms intervals)
	time.Sleep(connectionIntervalDelay())

	// Lock for thread-safe writes
	connection.sendMutex.Lock()
	defer connection.sendMutex.Unlock()

	// Send length-prefixed message
	msgLen := uint32(len(data))

	// Write length
	err = binary.Write(connection.conn, binary.BigEndian, msgLen)
	if err != nil {
		return fmt.Errorf("failed to send message length: %w", err)
	}

	// Write data
	_, err = connection.conn.Write(data)
	if err != nil {
		return fmt.Errorf("failed to send message data: %w", err)
	}

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
