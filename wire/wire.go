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

// AdvertisingData represents BLE advertising packet data
type AdvertisingData struct {
	DeviceName       string   `json:"device_name,omitempty"`
	ServiceUUIDs     []string `json:"service_uuids,omitempty"`
	ManufacturerData []byte   `json:"manufacturer_data,omitempty"`
	TxPowerLevel     *int     `json:"tx_power_level,omitempty"`
	IsConnectable    bool     `json:"is_connectable"`
}

// DeviceInfo represents a discovered device on the wire
type DeviceInfo struct {
	UUID string
	Name string
	Rssi int
}

// GATTDescriptor represents a BLE descriptor
type GATTDescriptor struct {
	UUID string `json:"uuid"`
	Type string `json:"type,omitempty"` // e.g., "CCCD" for Client Characteristic Configuration
}

// GATTCharacteristic represents a BLE characteristic
type GATTCharacteristic struct {
	UUID        string           `json:"uuid"`
	Properties  []string         `json:"properties"`  // "read", "write", "notify", "indicate", etc.
	Descriptors []GATTDescriptor `json:"descriptors,omitempty"`
	Value       []byte           `json:"-"` // Current value (not serialized in gatt.json)
}

// GATTService represents a BLE service
type GATTService struct {
	UUID            string               `json:"uuid"`
	Type            string               `json:"type"` // "primary" or "secondary"
	Characteristics []GATTCharacteristic `json:"characteristics"`
}

// GATTTable represents a device's complete GATT database
type GATTTable struct {
	Services []GATTService `json:"services"`
}

// CharacteristicMessage represents a read/write/notify operation on a characteristic
type CharacteristicMessage struct {
	Operation      string `json:"op"`             // "write", "read", "notify", "indicate"
	ServiceUUID    string `json:"service"`
	CharUUID       string `json:"characteristic"`
	Data           []byte `json:"data"`
	Timestamp      int64  `json:"timestamp"`
	SenderUUID     string `json:"sender,omitempty"`
}

// DeviceRole represents the BLE role(s) a device can perform
type DeviceRole int

const (
	RoleCentralOnly     DeviceRole = 1 << 0 // Can scan and connect (rarely used alone)
	RolePeripheralOnly  DeviceRole = 1 << 1 // Can advertise and accept connections
	RoleDual            DeviceRole = RoleCentralOnly | RolePeripheralOnly // Both roles (iOS and Android)
)

// Platform represents the device platform for role negotiation
type Platform string

const (
	PlatformIOS     Platform = "ios"
	PlatformAndroid Platform = "android"
	PlatformGeneric Platform = "generic"
)

// Wire implements BLE communication using Unix Domain Sockets
// This eliminates filesystem race conditions while maintaining realistic BLE simulation
type Wire struct {
	localUUID            string
	platform             Platform
	deviceName           string
	role                 DeviceRole
	simulator            *Simulator
	mtu                  int
	distance             float64

	// Socket infrastructure
	socketPath           string
	listener             net.Listener
	connections          map[string]net.Conn // targetUUID -> connection
	connMutex            sync.RWMutex

	// Connection state tracking
	connectionStates     map[string]ConnectionState
	monitorStopChans     map[string]chan struct{}
	disconnectCallback   func(deviceUUID string)

	// Message handlers
	messageHandlers      map[string]func(*CharacteristicMessage) // serviceUUID+charUUID -> handler
	handlerMutex         sync.RWMutex

	// Filesystem logging for debugging (optional)
	debugLogPath         string
	enableDebugLog       bool

	// Message queue for polling compatibility with old Wire API
	messageQueue         []*CharacteristicMessage
	queueMutex           sync.Mutex

	// Graceful shutdown
	stopChan             chan struct{}
	wg                   sync.WaitGroup
}

// NewWire creates a new wire with default platform (generic)
func NewWire(deviceUUID string) *Wire {
	w, _ := newWireInternal(deviceUUID, PlatformGeneric, "", nil)
	return w
}

// NewWireWithPlatform creates a wire with platform-specific behavior
// This is the main constructor used by iOS and Android implementations
func NewWireWithPlatform(deviceUUID string, platform Platform, deviceName string, config *SimulationConfig) *Wire {
	w, err := newWireInternal(deviceUUID, platform, deviceName, config)
	if err != nil {
		logger.Error(fmt.Sprintf("%s %s", deviceUUID[:8], platform),
			"FATAL: Failed to create wire: %v", err)
		panic(fmt.Sprintf("Failed to create wire: %v", err))
	}
	return w
}

// NewWireWithRole is deprecated but kept for compatibility
func NewWireWithRole(deviceUUID string, role DeviceRole, config *SimulationConfig) *Wire {
	w, _ := newWireInternal(deviceUUID, PlatformGeneric, "", config)
	w.role = role
	return w
}

// newWireInternal creates a new socket-based wire implementation
func newWireInternal(deviceUUID string, platform Platform, deviceName string, config *SimulationConfig) (*Wire, error) {
	if config == nil {
		config = DefaultSimulationConfig()
	}

	if deviceName == "" {
		deviceName = deviceUUID
	}

	// Create socket path in /tmp (portable across Unix systems)
	socketPath := fmt.Sprintf("/tmp/auraphone-%s.sock", deviceUUID)

	w := &Wire{
		localUUID:        deviceUUID,
		platform:         platform,
		deviceName:       deviceName,
		role:             RoleDual,
		simulator:        NewSimulator(config),
		mtu:              config.DefaultMTU,
		distance:         1.0,
		socketPath:       socketPath,
		connections:      make(map[string]net.Conn),
		connectionStates: make(map[string]ConnectionState),
		monitorStopChans: make(map[string]chan struct{}),
		messageHandlers:  make(map[string]func(*CharacteristicMessage)),
		stopChan:         make(chan struct{}),
		enableDebugLog:   true,
		debugLogPath:     "data",
	}

	return w, nil
}

// InitializeDevice sets up the socket listener and debug log directories
func (sw *Wire) InitializeDevice() error {
	// Remove old socket if it exists
	os.Remove(sw.socketPath)

	// Create Unix domain socket listener
	listener, err := net.Listen("unix", sw.socketPath)
	if err != nil {
		return fmt.Errorf("failed to create socket listener: %w", err)
	}
	sw.listener = listener

	logger.Debug(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		"🔌 Socket listener created at %s", sw.socketPath)

	// Start accepting connections
	sw.wg.Add(1)
	go sw.acceptLoop()

	// Create debug log directories (optional, for inspection)
	if sw.enableDebugLog {
		devicePath := filepath.Join(sw.debugLogPath, sw.localUUID)
		dirs := []string{
			filepath.Join(devicePath, "sent_messages"),
			filepath.Join(devicePath, "received_messages"),
			filepath.Join(devicePath, "cache"),
		}
		for _, dir := range dirs {
			if err := os.MkdirAll(dir, 0755); err != nil {
				logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
					"Failed to create debug log dir %s: %v", dir, err)
			}
		}

		// Write GATT table and advertising data files for discovery
		// (these still use filesystem for device discovery)
		os.MkdirAll(devicePath, 0755)
	}

	return nil
}

// acceptLoop accepts incoming connections from other devices
func (sw *Wire) acceptLoop() {
	defer sw.wg.Done()

	for {
		select {
		case <-sw.stopChan:
			return
		default:
		}

		// Set accept deadline to allow periodic stopChan checks
		if tcpListener, ok := sw.listener.(*net.UnixListener); ok {
			tcpListener.SetDeadline(time.Now().Add(1 * time.Second))
		}

		conn, err := sw.listener.Accept()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue // Check stopChan and try again
			}
			select {
			case <-sw.stopChan:
				return
			default:
				logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
					"Accept error: %v", err)
				continue
			}
		}

		// Read remote UUID from first message
		sw.wg.Add(1)
		go sw.handleConnection(conn)
	}
}

// handleConnection handles an incoming connection
func (sw *Wire) handleConnection(conn net.Conn) {
	defer sw.wg.Done()
	defer conn.Close()

	// Read the first 4 bytes to get the remote UUID length
	var uuidLen uint32
	if err := binary.Read(conn, binary.BigEndian, &uuidLen); err != nil {
		logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
			"Failed to read UUID length from incoming connection: %v", err)
		return
	}

	// Read the remote UUID
	uuidBytes := make([]byte, uuidLen)
	if _, err := io.ReadFull(conn, uuidBytes); err != nil {
		logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
			"Failed to read UUID from incoming connection: %v", err)
		return
	}
	remoteUUID := string(uuidBytes)

	logger.Debug(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		"📞 Accepted connection from %s", remoteUUID[:8])

	// Store connection (close any existing duplicate connection first)
	sw.connMutex.Lock()
	if existingConn, exists := sw.connections[remoteUUID]; exists {
		// Close the old connection before accepting new one
		// This prevents multiple readLoop goroutines from fragmenting the message stream
		logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
			"🔌 Closing duplicate connection from %s (new connection will replace it)", remoteUUID[:8])
		existingConn.Close()
	}
	sw.connections[remoteUUID] = conn
	sw.connectionStates[remoteUUID] = StateConnected
	sw.connMutex.Unlock()

	// Start connection monitoring
	sw.startConnectionMonitoring(remoteUUID)

	// Read messages from this connection
	sw.readLoop(remoteUUID, conn)

	// Connection closed
	sw.connMutex.Lock()
	delete(sw.connections, remoteUUID)
	sw.connectionStates[remoteUUID] = StateDisconnected
	sw.connMutex.Unlock()

	logger.Debug(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		"🔌 Connection closed from %s", remoteUUID[:8])

	// Notify disconnect callback
	if sw.disconnectCallback != nil {
		sw.disconnectCallback(remoteUUID)
	}
}

// readLoop reads messages from a connection
func (sw *Wire) readLoop(remoteUUID string, conn net.Conn) {
	for {
		// Read total message length (sent by SendToDevice)
		var totalLen uint32
		if err := binary.Read(conn, binary.BigEndian, &totalLen); err != nil {
			if err != io.EOF {
				logger.Trace(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
					"Read error from %s: %v", remoteUUID[:8], err)
			}
			return
		}

		// Read complete message data (may arrive in fragments from SendToDevice)
		msgData := make([]byte, totalLen)
		if _, err := io.ReadFull(conn, msgData); err != nil {
			logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
				"Failed to read complete message from %s (expected %d bytes): %v",
				remoteUUID[:8], totalLen, err)
			return
		}

		// Parse message
		var msg CharacteristicMessage
		if err := json.Unmarshal(msgData, &msg); err != nil {
			logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
				"Failed to parse message from %s: %v", remoteUUID[:8], err)
			continue
		}

		// Log for debugging
		if sw.enableDebugLog {
			sw.logReceivedMessage(remoteUUID, &msg)
		}

		logger.TraceJSON(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
			fmt.Sprintf("📥 RX %s (from %s, svc=%s, char=%s, %d bytes)",
				msg.Operation, remoteUUID[:8],
				msg.ServiceUUID[len(msg.ServiceUUID)-4:],
				msg.CharUUID[len(msg.CharUUID)-4:], len(msg.Data)), &msg)

		// Dispatch to handler
		sw.dispatchMessage(&msg)
	}
}

// Connect establishes a connection to a remote device
func (sw *Wire) Connect(targetUUID string) error {
	sw.connMutex.Lock()
	currentState := sw.connectionStates[targetUUID]
	if currentState != StateDisconnected && currentState != 0 {
		sw.connMutex.Unlock()
		logger.Debug(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
			"🔌 Connect attempt to %s BLOCKED (current state: %d)", targetUUID[:8], currentState)
		return fmt.Errorf("already connected or connecting to %s", targetUUID[:8])
	}
	sw.connectionStates[targetUUID] = StateConnecting
	sw.connMutex.Unlock()

	logger.Debug(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		"🔌 Connecting to %s (delay: simulated)", targetUUID[:8])

	// Simulate connection delay
	delay := sw.simulator.ConnectionDelay()
	time.Sleep(delay)

	// Check if connection succeeds
	if !sw.simulator.ShouldConnectionSucceed() {
		sw.connMutex.Lock()
		sw.connectionStates[targetUUID] = StateDisconnected
		sw.connMutex.Unlock()
		logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
			"❌ Connection to %s FAILED (simulated interference)", targetUUID[:8])
		return fmt.Errorf("connection failed (timeout or interference)")
	}

	// Connect to target's socket
	targetSocketPath := fmt.Sprintf("/tmp/auraphone-%s.sock", targetUUID)
	conn, err := net.Dial("unix", targetSocketPath)
	if err != nil {
		sw.connMutex.Lock()
		sw.connectionStates[targetUUID] = StateDisconnected
		sw.connMutex.Unlock()
		return fmt.Errorf("failed to dial %s: %w", targetUUID[:8], err)
	}

	// Send our UUID as first message (handshake)
	uuidBytes := []byte(sw.localUUID)
	if err := binary.Write(conn, binary.BigEndian, uint32(len(uuidBytes))); err != nil {
		conn.Close()
		sw.connMutex.Lock()
		sw.connectionStates[targetUUID] = StateDisconnected
		sw.connMutex.Unlock()
		return fmt.Errorf("failed to send UUID length: %w", err)
	}
	if _, err := conn.Write(uuidBytes); err != nil {
		conn.Close()
		sw.connMutex.Lock()
		sw.connectionStates[targetUUID] = StateDisconnected
		sw.connMutex.Unlock()
		return fmt.Errorf("failed to send UUID: %w", err)
	}

	// Store connection
	sw.connMutex.Lock()
	sw.connections[targetUUID] = conn
	sw.connectionStates[targetUUID] = StateConnected
	sw.connMutex.Unlock()

	logger.Info(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		"✅ Connected to %s at wire level", targetUUID[:8])

	// Start reading from this connection
	sw.wg.Add(1)
	go func() {
		defer sw.wg.Done()
		sw.readLoop(targetUUID, conn)

		// Connection closed
		sw.connMutex.Lock()
		delete(sw.connections, targetUUID)
		sw.connectionStates[targetUUID] = StateDisconnected
		sw.connMutex.Unlock()

		// Notify disconnect
		if sw.disconnectCallback != nil {
			sw.disconnectCallback(targetUUID)
		}
	}()

	// Start connection monitoring
	sw.startConnectionMonitoring(targetUUID)

	return nil
}

// Disconnect closes connection to a device
func (sw *Wire) Disconnect(targetUUID string) error {
	sw.connMutex.Lock()
	conn, exists := sw.connections[targetUUID]
	if !exists || sw.connectionStates[targetUUID] != StateConnected {
		sw.connMutex.Unlock()
		return fmt.Errorf("not connected to %s", targetUUID[:8])
	}

	sw.connectionStates[targetUUID] = StateDisconnecting
	sw.connMutex.Unlock()

	// Stop monitoring
	sw.stopConnectionMonitoring(targetUUID)

	// Simulate disconnection delay
	delay := sw.simulator.DisconnectDelay()
	time.Sleep(delay)

	// Close connection
	conn.Close()

	sw.connMutex.Lock()
	delete(sw.connections, targetUUID)
	sw.connectionStates[targetUUID] = StateDisconnected
	sw.connMutex.Unlock()

	logger.Debug(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		"🔌 Disconnected from %s", targetUUID[:8])

	return nil
}

// SendToDevice sends data to a target device via socket
func (sw *Wire) SendToDevice(targetUUID string, data []byte) error {
	sw.connMutex.RLock()
	conn, exists := sw.connections[targetUUID]
	sw.connMutex.RUnlock()

	if !exists {
		return fmt.Errorf("not connected to %s", targetUUID[:8])
	}

	// Fragment data based on MTU for realistic BLE simulation
	fragments := sw.simulator.FragmentData(data, sw.mtu)

	// Send complete message length first (so receiver knows total size)
	totalLen := uint32(len(data))
	if err := binary.Write(conn, binary.BigEndian, totalLen); err != nil {
		return fmt.Errorf("failed to write message length: %w", err)
	}

	// Send each fragment with realistic BLE timing and packet loss
	for i, fragment := range fragments {
		// Inter-packet delay (realistic BLE timing)
		if i > 0 {
			time.Sleep(2 * time.Millisecond)
		}

		// Simulate packet loss with retries
		var lastErr error
		for attempt := 0; attempt <= sw.simulator.config.MaxRetries; attempt++ {
			if attempt > 0 {
				time.Sleep(time.Duration(sw.simulator.config.RetryDelay) * time.Millisecond)
				logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
					"🔄 Retrying packet to %s (attempt %d/%d, fragment %d/%d)",
					targetUUID[:8], attempt+1, sw.simulator.config.MaxRetries+1, i+1, len(fragments))
			}

			// Simulate packet loss
			if !sw.simulator.ShouldPacketSucceed() && attempt < sw.simulator.config.MaxRetries {
				logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
					"📉 Simulated packet loss to %s (attempt %d/%d, fragment %d/%d)",
					targetUUID[:8], attempt+1, sw.simulator.config.MaxRetries+1, i+1, len(fragments))
				lastErr = fmt.Errorf("packet loss (attempt %d/%d)", attempt+1, sw.simulator.config.MaxRetries+1)
				continue
			}

			// Write fragment data (no per-fragment length prefix)
			if _, err := conn.Write(fragment); err != nil {
				lastErr = fmt.Errorf("failed to write fragment: %w", err)
				continue
			}

			// Success
			lastErr = nil
			break
		}

		if lastErr != nil {
			logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
				"❌ Failed to send fragment %d/%d to %s after %d retries: %v",
				i+1, len(fragments), targetUUID[:8], sw.simulator.config.MaxRetries, lastErr)
			return lastErr
		}
	}

	return nil
}

// WriteCharacteristic sends a write request to target device
func (sw *Wire) WriteCharacteristic(targetUUID, serviceUUID, charUUID string, data []byte) error {
	msg := CharacteristicMessage{
		Operation:   "write",
		ServiceUUID: serviceUUID,
		CharUUID:    charUUID,
		Data:        data,
		Timestamp:   time.Now().UnixNano(),
		SenderUUID:  sw.localUUID,
	}

	logger.TraceJSON(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		fmt.Sprintf("📤 TX Write WITH Response (to %s, svc=%s, char=%s, %d bytes)",
			targetUUID[:8], serviceUUID[len(serviceUUID)-4:], charUUID[len(charUUID)-4:], len(data)), &msg)

	return sw.sendCharacteristicMessage(targetUUID, &msg)
}

// WriteCharacteristicNoResponse sends a write without waiting for response
func (sw *Wire) WriteCharacteristicNoResponse(targetUUID, serviceUUID, charUUID string, data []byte) error {
	msg := CharacteristicMessage{
		Operation:   "write_no_response",
		ServiceUUID: serviceUUID,
		CharUUID:    charUUID,
		Data:        data,
		Timestamp:   time.Now().UnixNano(),
		SenderUUID:  sw.localUUID,
	}

	logger.TraceJSON(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		fmt.Sprintf("📤 TX Write NO Response (to %s, svc=%s, char=%s, %d bytes)",
			targetUUID[:8], serviceUUID[len(serviceUUID)-4:], charUUID[len(charUUID)-4:], len(data)), &msg)

	// Send asynchronously (fire and forget)
	go func() {
		if err := sw.sendCharacteristicMessage(targetUUID, &msg); err != nil {
			logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
				"❌ Write NO Response transmission FAILED to %s: %v", targetUUID[:8], err)
		}
	}()

	return nil
}

// sendCharacteristicMessage marshals and sends a message
func (sw *Wire) sendCharacteristicMessage(targetUUID string, msg *CharacteristicMessage) error {
	msgData, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	// Log for debugging
	if sw.enableDebugLog {
		sw.logSentMessage(targetUUID, msg)
	}

	return sw.SendToDevice(targetUUID, msgData)
}

// dispatchMessage routes incoming messages to handlers or queues them
func (sw *Wire) dispatchMessage(msg *CharacteristicMessage) {
	sw.handlerMutex.RLock()
	handler, exists := sw.messageHandlers[msg.ServiceUUID+msg.CharUUID]
	sw.handlerMutex.RUnlock()

	if exists {
		handler(msg)
	} else {
		// No specific handler, queue for polling
		sw.queueMutex.Lock()
		sw.messageQueue = append(sw.messageQueue, msg)
		sw.queueMutex.Unlock()
	}
}

// ReadAndConsumeCharacteristicMessages reads and consumes queued messages
// This provides compatibility with the old Wire polling-based API
func (sw *Wire) ReadAndConsumeCharacteristicMessages() ([]*CharacteristicMessage, error) {
	sw.queueMutex.Lock()
	defer sw.queueMutex.Unlock()

	if len(sw.messageQueue) == 0 {
		return nil, nil
	}

	// Return all queued messages and clear queue
	messages := make([]*CharacteristicMessage, len(sw.messageQueue))
	copy(messages, sw.messageQueue)
	sw.messageQueue = nil

	return messages, nil
}

// RegisterMessageHandler registers a handler for messages on a characteristic
func (sw *Wire) RegisterMessageHandler(serviceUUID, charUUID string, handler func(*CharacteristicMessage)) {
	sw.handlerMutex.Lock()
	sw.messageHandlers[serviceUUID+charUUID] = handler
	sw.handlerMutex.Unlock()
}

// startConnectionMonitoring monitors connection health
func (sw *Wire) startConnectionMonitoring(targetUUID string) {
	sw.connMutex.Lock()
	if sw.monitorStopChans[targetUUID] != nil {
		sw.connMutex.Unlock()
		return
	}

	stopChan := make(chan struct{})
	sw.monitorStopChans[targetUUID] = stopChan
	sw.connMutex.Unlock()

	sw.wg.Add(1)
	go func() {
		defer sw.wg.Done()

		interval := time.Duration(sw.simulator.config.ConnectionMonitorInterval) * time.Millisecond
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				return
			case <-sw.stopChan:
				return
			case <-ticker.C:
				sw.connMutex.Lock()
				if sw.connectionStates[targetUUID] == StateConnected && sw.simulator.ShouldRandomlyDisconnect() {
					// Force disconnect
					if conn, exists := sw.connections[targetUUID]; exists {
						conn.Close()
					}
					delete(sw.connections, targetUUID)
					sw.connectionStates[targetUUID] = StateDisconnected

					callback := sw.disconnectCallback
					sw.connMutex.Unlock()

					if callback != nil {
						callback(targetUUID)
					}

					// Stop monitoring
					sw.stopConnectionMonitoring(targetUUID)
					return
				}
				sw.connMutex.Unlock()
			}
		}
	}()
}

// stopConnectionMonitoring stops monitoring a connection
func (sw *Wire) stopConnectionMonitoring(targetUUID string) {
	sw.connMutex.Lock()
	defer sw.connMutex.Unlock()

	if ch, exists := sw.monitorStopChans[targetUUID]; exists {
		close(ch)
		delete(sw.monitorStopChans, targetUUID)
	}
}

// SetDisconnectCallback sets the callback for disconnections
func (sw *Wire) SetDisconnectCallback(callback func(deviceUUID string)) {
	sw.disconnectCallback = callback
}

// GetConnectionState returns the connection state for a device
func (sw *Wire) GetConnectionState(targetUUID string) ConnectionState {
	sw.connMutex.RLock()
	defer sw.connMutex.RUnlock()
	return sw.connectionStates[targetUUID]
}

// ShouldActAsCentral determines if this device should initiate connection
func (sw *Wire) ShouldActAsCentral(targetUUID string) bool {
	return sw.localUUID > targetUUID
}

// Cleanup closes all connections and stops the listener
func (sw *Wire) Cleanup() {
	// Signal shutdown
	close(sw.stopChan)

	// Close all connections
	sw.connMutex.Lock()
	for uuid, conn := range sw.connections {
		conn.Close()
		delete(sw.connections, uuid)
	}
	sw.connMutex.Unlock()

	// Close listener
	if sw.listener != nil {
		sw.listener.Close()
	}

	// Wait for goroutines
	sw.wg.Wait()

	// Remove socket file
	os.Remove(sw.socketPath)

	logger.Debug(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		"🧹 Cleaned up socket wire")
}

// logSentMessage logs a sent message to filesystem for debugging
func (sw *Wire) logSentMessage(targetUUID string, msg *CharacteristicMessage) {
	if !sw.enableDebugLog {
		return
	}

	logDir := filepath.Join(sw.debugLogPath, sw.localUUID, "sent_messages")
	filename := fmt.Sprintf("to_%s_msg_%d.json", targetUUID[:8], msg.Timestamp)
	logPath := filepath.Join(logDir, filename)

	data, _ := json.MarshalIndent(msg, "", "  ")
	os.WriteFile(logPath, data, 0644)
}

// logReceivedMessage logs a received message to filesystem for debugging
func (sw *Wire) logReceivedMessage(remoteUUID string, msg *CharacteristicMessage) {
	if !sw.enableDebugLog {
		return
	}

	logDir := filepath.Join(sw.debugLogPath, sw.localUUID, "received_messages")
	filename := fmt.Sprintf("from_%s_msg_%d.json", remoteUUID[:8], msg.Timestamp)
	logPath := filepath.Join(logDir, filename)

	data, _ := json.MarshalIndent(msg, "", "  ")
	os.WriteFile(logPath, data, 0644)
}

// Helper methods for compatibility with existing Wire interface

func (sw *Wire) GetRole() DeviceRole {
	return sw.role
}

func (sw *Wire) GetPlatform() Platform {
	return sw.platform
}

func (sw *Wire) GetDeviceName() string {
	return sw.deviceName
}

func (sw *Wire) GetRSSI() int {
	return sw.simulator.GenerateRSSI(sw.distance)
}

func (sw *Wire) SetDistance(meters float64) {
	sw.distance = meters
}

func (sw *Wire) GetSimulator() *Simulator {
	return sw.simulator
}

// CanScan returns true if device can scan (Central role)
func (w *Wire) CanScan() bool {
	return w.role&RoleCentralOnly != 0
}

// CanAdvertise returns true if device can advertise (Peripheral role)
func (w *Wire) CanAdvertise() bool {
	return w.role&RolePeripheralOnly != 0
}

// DiscoverDevices finds other devices by scanning for socket files
func (sw *Wire) DiscoverDevices() ([]string, error) {
	// For now, use filesystem-based discovery (scan data/ directory)
	// This is still filesystem-based but only for discovery, not IPC
	basePath := sw.debugLogPath
	files, err := os.ReadDir(basePath)
	if err != nil {
		return nil, err
	}

	var devices []string
	for _, file := range files {
		if file.IsDir() {
			deviceName := file.Name()
			// Check if it's a valid UUID and not our own device
			if len(deviceName) > 8 && deviceName != sw.localUUID {
				// Also check if socket exists
				sockPath := fmt.Sprintf("/tmp/auraphone-%s.sock", deviceName)
				if _, err := os.Stat(sockPath); err == nil {
					devices = append(devices, deviceName)
				}
			}
		}
	}

	return devices, nil
}

// StartDiscovery continuously scans for devices
func (sw *Wire) StartDiscovery(callback func(deviceUUID string)) chan struct{} {
	stopChan := make(chan struct{})

	sw.wg.Add(1)
	go func() {
		defer sw.wg.Done()

		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		discoveredDevices := make(map[string]bool)

		for {
			select {
			case <-stopChan:
				return
			case <-sw.stopChan:
				return
			case <-ticker.C:
				devices, err := sw.DiscoverDevices()
				if err != nil {
					continue
				}

				for _, deviceUUID := range devices {
					if discoveredDevices[deviceUUID] {
						callback(deviceUUID)
						continue
					}

					// Simulate discovery delay
					delay := sw.simulator.DiscoveryDelay()
					time.Sleep(delay)

					discoveredDevices[deviceUUID] = true
					callback(deviceUUID)
				}
			}
		}
	}()

	return stopChan
}

// WriteGATTTable writes the GATT table to filesystem (for discovery)
func (sw *Wire) WriteGATTTable(table *GATTTable) error {
	devicePath := filepath.Join(sw.debugLogPath, sw.localUUID)
	gattPath := filepath.Join(devicePath, "gatt.json")

	data, err := json.MarshalIndent(table, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal GATT table: %w", err)
	}

	return os.WriteFile(gattPath, data, 0644)
}

// ReadGATTTable reads GATT table from filesystem
func (sw *Wire) ReadGATTTable(deviceUUID string) (*GATTTable, error) {
	devicePath := filepath.Join(sw.debugLogPath, deviceUUID)
	gattPath := filepath.Join(devicePath, "gatt.json")

	data, err := os.ReadFile(gattPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read GATT table: %w", err)
	}

	var table GATTTable
	if err := json.Unmarshal(data, &table); err != nil {
		return nil, fmt.Errorf("failed to unmarshal GATT table: %w", err)
	}

	return &table, nil
}

// WriteAdvertisingData writes advertising data to filesystem
func (sw *Wire) WriteAdvertisingData(advData *AdvertisingData) error {
	devicePath := filepath.Join(sw.debugLogPath, sw.localUUID)
	advPath := filepath.Join(devicePath, "advertising.json")

	data, err := json.MarshalIndent(advData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal advertising data: %w", err)
	}

	logger.TraceJSON(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		"📡 TX Advertising Data", advData)

	return os.WriteFile(advPath, data, 0644)
}

// ReadAdvertisingData reads advertising data from filesystem
func (sw *Wire) ReadAdvertisingData(deviceUUID string) (*AdvertisingData, error) {
	devicePath := filepath.Join(sw.debugLogPath, deviceUUID)
	advPath := filepath.Join(devicePath, "advertising.json")

	data, err := os.ReadFile(advPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &AdvertisingData{
				IsConnectable: true,
			}, nil
		}
		return nil, fmt.Errorf("failed to read advertising data: %w", err)
	}

	var advData AdvertisingData
	if err := json.Unmarshal(data, &advData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal advertising data: %w", err)
	}

	logger.TraceJSON(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		fmt.Sprintf("📡 RX Advertising Data (from %s)", deviceUUID[:8]), &advData)

	return &advData, nil
}

// NotifyCharacteristic sends a notification
func (sw *Wire) NotifyCharacteristic(targetUUID, serviceUUID, charUUID string, data []byte) error {
	msg := CharacteristicMessage{
		Operation:   "notify",
		ServiceUUID: serviceUUID,
		CharUUID:    charUUID,
		Data:        data,
		Timestamp:   time.Now().UnixNano(),
		SenderUUID:  sw.localUUID,
	}

	// Simulate notification drops
	if sw.simulator.ShouldNotificationDrop() {
		logger.Trace(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
			"📉 Notification DROPPED (realistic BLE behavior, %d bytes lost)", len(data))
		return nil
	}

	// Simulate notification latency
	if sw.simulator.EnableNotificationReordering() {
		delay := sw.simulator.NotificationDeliveryDelay()
		go func() {
			time.Sleep(delay)
			sw.sendCharacteristicMessage(targetUUID, &msg)
		}()
		return nil
	}

	return sw.sendCharacteristicMessage(targetUUID, &msg)
}

// SubscribeCharacteristic sends a subscription request
func (sw *Wire) SubscribeCharacteristic(targetUUID, serviceUUID, charUUID string) error {
	msg := CharacteristicMessage{
		Operation:   "subscribe",
		ServiceUUID: serviceUUID,
		CharUUID:    charUUID,
		Timestamp:   time.Now().UnixNano(),
		SenderUUID:  sw.localUUID,
	}

	logger.TraceJSON(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		fmt.Sprintf("📤 TX Subscribe (to %s, svc=%s, char=%s)",
			targetUUID[:8], serviceUUID[len(serviceUUID)-4:], charUUID[len(charUUID)-4:]), &msg)

	return sw.sendCharacteristicMessage(targetUUID, &msg)
}

// UnsubscribeCharacteristic sends an unsubscription request
func (sw *Wire) UnsubscribeCharacteristic(targetUUID, serviceUUID, charUUID string) error {
	msg := CharacteristicMessage{
		Operation:   "unsubscribe",
		ServiceUUID: serviceUUID,
		CharUUID:    charUUID,
		Timestamp:   time.Now().UnixNano(),
		SenderUUID:  sw.localUUID,
	}

	logger.TraceJSON(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		fmt.Sprintf("📤 TX Unsubscribe (to %s, svc=%s, char=%s)",
			targetUUID[:8], serviceUUID[len(serviceUUID)-4:], charUUID[len(charUUID)-4:]), &msg)

	return sw.sendCharacteristicMessage(targetUUID, &msg)
}

// SetBasePath sets the base path for debug logs and discovery
func (sw *Wire) SetBasePath(path string) {
	sw.debugLogPath = path
}

// ReadCharacteristic sends a read request to target device (not typically used in this implementation)
func (w *Wire) ReadCharacteristic(targetUUID, serviceUUID, charUUID string) error {
	msg := CharacteristicMessage{
		Operation:   "read",
		ServiceUUID: serviceUUID,
		CharUUID:    charUUID,
		Timestamp:   time.Now().UnixNano(),
		SenderUUID:  w.localUUID,
	}

	logger.TraceJSON(fmt.Sprintf("%s %s", w.localUUID[:8], w.platform),
		fmt.Sprintf("📤 TX Read Request (to %s, svc=%s, char=%s)",
			targetUUID[:8], serviceUUID[len(serviceUUID)-4:], charUUID[len(charUUID)-4:]), &msg)

	return w.sendCharacteristicMessage(targetUUID, &msg)
}

// ReadAndConsumeCharacteristicMessagesFromInbox reads messages from a specific inbox type
// This provides compatibility with dual-role devices (iOS/Android) that have separate
// central_inbox and peripheral_inbox for bidirectional communication
//
// In the socket implementation, there's only one message queue, so we just return all messages
// regardless of the inbox type specified. The role-based separation was a filesystem artifact.
func (w *Wire) ReadAndConsumeCharacteristicMessagesFromInbox(inboxType string) ([]*CharacteristicMessage, error) {
	// In socket implementation, we don't have separate inboxes
	// All messages go to the same queue, so just return them all
	logger.Trace(fmt.Sprintf("%s %s", w.localUUID[:8], w.platform),
		"📬 Reading from %s (socket impl: unified queue)", inboxType)
	return w.ReadAndConsumeCharacteristicMessages()
}
