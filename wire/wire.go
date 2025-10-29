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
)

// ConnectionRole represents the role in a specific connection
type ConnectionRole string

const (
	RoleCentral    ConnectionRole = "central"    // We initiated connection
	RolePeripheral ConnectionRole = "peripheral" // They initiated connection
)

// AdvertisingData represents BLE advertising packet data (stub for old API compatibility)
// TODO Step 4: Implement full advertising protocol
type AdvertisingData struct {
	DeviceName       string
	ServiceUUIDs     []string
	ManufacturerData []byte
	TxPowerLevel     *int
	IsConnectable    bool
}

// GATTTable represents a device's complete GATT database (stub for old API compatibility)
// TODO Step 4: Implement GATT service discovery
type GATTTable struct {
	Services []GATTService
}

// GATTService represents a BLE service (stub for old API compatibility)
type GATTService struct {
	UUID            string
	Type            string
	Characteristics []GATTCharacteristic
}

// GATTCharacteristic represents a BLE characteristic (stub for old API compatibility)
type GATTCharacteristic struct {
	UUID       string
	Properties []string
	Value      []byte
}

// CharacteristicMessage is deprecated, use GATTMessage instead
// Kept for backward compatibility with old swift code
type CharacteristicMessage = GATTMessage

// Connection represents a single bidirectional BLE connection
type Connection struct {
	conn       net.Conn
	remoteUUID string
	role       ConnectionRole // Our role in this connection
	sendMutex  sync.Mutex     // Protects writes to this connection
}

// GATTMessage represents a GATT operation over the wire
type GATTMessage struct {
	Type               string `json:"type"`                         // "gatt_request", "gatt_response", "gatt_notification"
	RequestID          string `json:"request_id,omitempty"`         // For request/response matching
	Operation          string `json:"operation,omitempty"`          // "read", "write", "subscribe", "unsubscribe"
	ServiceUUID        string `json:"service_uuid"`
	CharacteristicUUID string `json:"characteristic_uuid"`
	CharUUID           string `json:"char_uuid,omitempty"`          // Alias for CharacteristicUUID (old API compat)
	Data               []byte `json:"data,omitempty"`
	Status             string `json:"status,omitempty"`             // "success", "error"
	SenderUUID         string `json:"sender_uuid,omitempty"`        // Who sent this message
}

// shortHash safely returns up to the first 8 characters of a string (or the full string if shorter)
func shortHash(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}

// Global registry for advertising data (simulates BLE broadcast)
// In real BLE, advertising data is broadcast over the air
// In our simulator, devices write here and others read from here
var (
	globalAdvertisingData   = make(map[string]*AdvertisingData)
	globalAdvertisingDataMu sync.RWMutex
	globalGATTTables        = make(map[string]*GATTTable)
	globalGATTTablesMu      sync.RWMutex
)

// Wire handles Unix domain socket communication with BLE realism
// Single socket per device at /tmp/auraphone-{hardwareUUID}.sock
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
	w := &Wire{
		hardwareUUID: hardwareUUID,
		socketPath:   fmt.Sprintf("/tmp/auraphone-%s.sock", hardwareUUID),
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

	// Clean up from global registries
	globalAdvertisingDataMu.Lock()
	delete(globalAdvertisingData, w.hardwareUUID)
	globalAdvertisingDataMu.Unlock()

	globalGATTTablesMu.Lock()
	delete(globalGATTTables, w.hardwareUUID)
	globalGATTTablesMu.Unlock()
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
func (w *Wire) Connect(peerUUID string) error {
	// Check if already connected
	w.mu.RLock()
	_, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if exists {
		return nil // Already connected
	}

	// Connect to peer's socket
	peerSocketPath := fmt.Sprintf("/tmp/auraphone-%s.sock", peerUUID)
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
	}

	w.mu.Lock()
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

	logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "📡 SendGATTMessage to %s: op=%s, len=%d bytes",
		shortHash(peerUUID), msg.Operation, len(data))

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

// ListAvailableDevices scans /tmp for .sock files and returns hardware UUIDs
func (w *Wire) ListAvailableDevices() []string {
	devices := make([]string, 0)

	// Scan /tmp for auraphone-*.sock files
	matches, err := filepath.Glob("/tmp/auraphone-*.sock")
	if err != nil {
		return devices
	}

	for _, path := range matches {
		// Extract UUID from filename
		filename := filepath.Base(path)
		// Format: auraphone-{UUID}.sock
		if len(filename) < len("auraphone-") || filepath.Ext(filename) != ".sock" {
			continue
		}

		uuid := filename[len("auraphone-") : len(filename)-len(".sock")]

		// Don't include ourselves
		if uuid != w.hardwareUUID {
			devices = append(devices, uuid)
		}
	}

	return devices
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

// ============================================================================
// STUB METHODS FOR OLD API COMPATIBILITY
// These will be properly implemented in Steps 4-6
// ============================================================================

// StartDiscovery starts scanning for devices (stub for old API)
// TODO Step 4: Implement proper discovery with callbacks
func (w *Wire) StartDiscovery(callback func(deviceUUID string)) chan struct{} {
	stopChan := make(chan struct{})

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				return
			case <-ticker.C:
				// Use our new ListAvailableDevices
				devices := w.ListAvailableDevices()
				for _, deviceUUID := range devices {
					callback(deviceUUID)
				}
			}
		}
	}()

	return stopChan
}

// ReadAdvertisingData reads advertising data for a device
func (w *Wire) ReadAdvertisingData(deviceUUID string) (*AdvertisingData, error) {
	globalAdvertisingDataMu.RLock()
	defer globalAdvertisingDataMu.RUnlock()

	advData, exists := globalAdvertisingData[deviceUUID]
	if !exists {
		// Return default advertising data if not found
		return &AdvertisingData{
			DeviceName:    fmt.Sprintf("Device-%s", shortHash(deviceUUID)),
			ServiceUUIDs:  []string{},
			IsConnectable: true,
		}, nil
	}

	// Return a copy to prevent race conditions
	return &AdvertisingData{
		DeviceName:       advData.DeviceName,
		ServiceUUIDs:     append([]string{}, advData.ServiceUUIDs...),
		ManufacturerData: append([]byte{}, advData.ManufacturerData...),
		TxPowerLevel:     advData.TxPowerLevel,
		IsConnectable:    advData.IsConnectable,
	}, nil
}

// GetRSSI returns simulated RSSI for a device (stub for old API)
// TODO Step 4: Implement realistic RSSI simulation
func (w *Wire) GetRSSI(deviceUUID string) float64 {
	return -45.0 // Good signal strength
}

// DeviceExists checks if a device exists (stub for old API)
// TODO Step 4: Implement proper device discovery state tracking
func (w *Wire) DeviceExists(deviceUUID string) bool {
	// Check if device socket exists
	socketPath := fmt.Sprintf("/tmp/auraphone-%s.sock", deviceUUID)
	_, err := os.Stat(socketPath)
	return err == nil
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

// SimulatorStub is a stub for the old Simulator type
type SimulatorStub struct {
	ServiceDiscoveryDelay time.Duration
}

// GetSimulator returns a stub simulator (stub for old API)
// TODO Step 4: Remove simulator dependency from swift layer
func (w *Wire) GetSimulator() *SimulatorStub {
	return &SimulatorStub{
		ServiceDiscoveryDelay: 100 * time.Millisecond,
	}
}

// ReadGATTTable reads GATT table from peer
func (w *Wire) ReadGATTTable(peerUUID string) (*GATTTable, error) {
	globalGATTTablesMu.RLock()
	defer globalGATTTablesMu.RUnlock()

	table, exists := globalGATTTables[peerUUID]
	if !exists {
		// Return empty GATT table if not found
		return &GATTTable{
			Services: []GATTService{},
		}, nil
	}

	// Return a copy to prevent race conditions
	tableCopy := &GATTTable{
		Services: make([]GATTService, len(table.Services)),
	}
	copy(tableCopy.Services, table.Services)

	return tableCopy, nil
}

// WriteCharacteristic writes to a characteristic (stub for old API)
// TODO Step 5: Implement via SendGATTMessage
func (w *Wire) WriteCharacteristic(peerUUID, serviceUUID, charUUID string, data []byte) error {
	msg := &GATTMessage{
		Type:               "gatt_request",
		Operation:          "write",
		ServiceUUID:        serviceUUID,
		CharacteristicUUID: charUUID,
		Data:               data,
	}
	return w.SendGATTMessage(peerUUID, msg)
}

// WriteCharacteristicNoResponse writes without waiting for response (stub for old API)
// TODO Step 5: Implement via SendGATTMessage
func (w *Wire) WriteCharacteristicNoResponse(peerUUID, serviceUUID, charUUID string, data []byte) error {
	// Same as WriteCharacteristic for now
	return w.WriteCharacteristic(peerUUID, serviceUUID, charUUID, data)
}

// ReadCharacteristic reads from a characteristic (stub for old API)
// TODO Step 5: Implement via SendGATTMessage request/response
func (w *Wire) ReadCharacteristic(peerUUID, serviceUUID, charUUID string) error {
	// Not implemented yet - return error for now
	return fmt.Errorf("ReadCharacteristic not implemented in Step 3")
}

// SubscribeCharacteristic subscribes to notifications (stub for old API)
// TODO Step 6: Implement via SendGATTMessage subscribe operation
func (w *Wire) SubscribeCharacteristic(peerUUID, serviceUUID, charUUID string) error {
	msg := &GATTMessage{
		Type:               "gatt_request",
		Operation:          "subscribe",
		ServiceUUID:        serviceUUID,
		CharacteristicUUID: charUUID,
	}
	return w.SendGATTMessage(peerUUID, msg)
}

// UnsubscribeCharacteristic unsubscribes from notifications (stub for old API)
// TODO Step 6: Implement via SendGATTMessage unsubscribe operation
func (w *Wire) UnsubscribeCharacteristic(peerUUID, serviceUUID, charUUID string) error {
	msg := &GATTMessage{
		Type:               "gatt_request",
		Operation:          "unsubscribe",
		ServiceUUID:        serviceUUID,
		CharacteristicUUID: charUUID,
	}
	return w.SendGATTMessage(peerUUID, msg)
}

// WriteGATTTable writes GATT table to global registry
func (w *Wire) WriteGATTTable(table *GATTTable) error {
	globalGATTTablesMu.Lock()
	defer globalGATTTablesMu.Unlock()

	// Store a copy to prevent race conditions
	tableCopy := &GATTTable{
		Services: make([]GATTService, len(table.Services)),
	}
	copy(tableCopy.Services, table.Services)

	globalGATTTables[w.hardwareUUID] = tableCopy
	return nil
}

// WriteAdvertisingData writes advertising data to global registry
func (w *Wire) WriteAdvertisingData(data *AdvertisingData) error {
	globalAdvertisingDataMu.Lock()
	defer globalAdvertisingDataMu.Unlock()

	// Store a copy to prevent race conditions
	dataCopy := &AdvertisingData{
		DeviceName:       data.DeviceName,
		ServiceUUIDs:     append([]string{}, data.ServiceUUIDs...),
		ManufacturerData: append([]byte{}, data.ManufacturerData...),
		TxPowerLevel:     data.TxPowerLevel,
		IsConnectable:    data.IsConnectable,
	}

	globalAdvertisingData[w.hardwareUUID] = dataCopy
	return nil
}

// NotifyCharacteristic sends a notification (stub for old API)
// TODO Step 6: Implement via SendGATTMessage notification
func (w *Wire) NotifyCharacteristic(peerUUID, serviceUUID, charUUID string, data []byte) error {
	logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "📤 NotifyCharacteristic to %s: svc=%s, char=%s, len=%d",
		shortHash(peerUUID), shortHash(serviceUUID), shortHash(charUUID), len(data))
	msg := &GATTMessage{
		Type:               "gatt_notification",
		Operation:          "notify",
		ServiceUUID:        serviceUUID,
		CharacteristicUUID: charUUID,
		Data:               data,
	}
	err := w.SendGATTMessage(peerUUID, msg)
	if err != nil {
		logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "❌ NotifyCharacteristic failed: %v", err)
	}
	return err
}

// ReadAndConsumeCharacteristicMessagesFromInbox reads messages (stub for old API)
// TODO Step 5: Remove inbox polling pattern, use SetGATTMessageHandler instead
func (w *Wire) ReadAndConsumeCharacteristicMessagesFromInbox(deviceUUID string) ([]*CharacteristicMessage, error) {
	// Return empty list - new architecture uses callbacks, not polling
	return []*CharacteristicMessage{}, nil
}
