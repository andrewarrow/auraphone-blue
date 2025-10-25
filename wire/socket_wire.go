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

// SocketWire implements BLE communication using Unix Domain Sockets
// This eliminates filesystem race conditions while maintaining realistic BLE simulation
type SocketWire struct {
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

// NewSocketWire creates a new socket-based wire implementation
func NewSocketWire(deviceUUID string, platform Platform, deviceName string, config *SimulationConfig) (*SocketWire, error) {
	if config == nil {
		config = DefaultSimulationConfig()
	}

	if deviceName == "" {
		deviceName = deviceUUID
	}

	// Create socket path in /tmp (portable across Unix systems)
	socketPath := fmt.Sprintf("/tmp/auraphone-%s.sock", deviceUUID)

	sw := &SocketWire{
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

	return sw, nil
}

// InitializeDevice sets up the socket listener and debug log directories
func (sw *SocketWire) InitializeDevice() error {
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
func (sw *SocketWire) acceptLoop() {
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
func (sw *SocketWire) handleConnection(conn net.Conn) {
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

	// Store connection
	sw.connMutex.Lock()
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
func (sw *SocketWire) readLoop(remoteUUID string, conn net.Conn) {
	for {
		// Read message length
		var msgLen uint32
		if err := binary.Read(conn, binary.BigEndian, &msgLen); err != nil {
			if err != io.EOF {
				logger.Trace(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
					"Read error from %s: %v", remoteUUID[:8], err)
			}
			return
		}

		// Read message data
		msgData := make([]byte, msgLen)
		if _, err := io.ReadFull(conn, msgData); err != nil {
			logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
				"Failed to read message from %s: %v", remoteUUID[:8], err)
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
func (sw *SocketWire) Connect(targetUUID string) error {
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
func (sw *SocketWire) Disconnect(targetUUID string) error {
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
func (sw *SocketWire) SendToDevice(targetUUID string, data []byte) error {
	sw.connMutex.RLock()
	conn, exists := sw.connections[targetUUID]
	sw.connMutex.RUnlock()

	if !exists {
		return fmt.Errorf("not connected to %s", targetUUID[:8])
	}

	// Fragment data based on MTU
	fragments := sw.simulator.FragmentData(data, sw.mtu)

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

			// Write fragment length + fragment data
			if err := binary.Write(conn, binary.BigEndian, uint32(len(fragment))); err != nil {
				lastErr = fmt.Errorf("failed to write fragment length: %w", err)
				continue
			}
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
func (sw *SocketWire) WriteCharacteristic(targetUUID, serviceUUID, charUUID string, data []byte) error {
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
func (sw *SocketWire) WriteCharacteristicNoResponse(targetUUID, serviceUUID, charUUID string, data []byte) error {
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
func (sw *SocketWire) sendCharacteristicMessage(targetUUID string, msg *CharacteristicMessage) error {
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
func (sw *SocketWire) dispatchMessage(msg *CharacteristicMessage) {
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
func (sw *SocketWire) ReadAndConsumeCharacteristicMessages() ([]*CharacteristicMessage, error) {
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
func (sw *SocketWire) RegisterMessageHandler(serviceUUID, charUUID string, handler func(*CharacteristicMessage)) {
	sw.handlerMutex.Lock()
	sw.messageHandlers[serviceUUID+charUUID] = handler
	sw.handlerMutex.Unlock()
}

// startConnectionMonitoring monitors connection health
func (sw *SocketWire) startConnectionMonitoring(targetUUID string) {
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
func (sw *SocketWire) stopConnectionMonitoring(targetUUID string) {
	sw.connMutex.Lock()
	defer sw.connMutex.Unlock()

	if ch, exists := sw.monitorStopChans[targetUUID]; exists {
		close(ch)
		delete(sw.monitorStopChans, targetUUID)
	}
}

// SetDisconnectCallback sets the callback for disconnections
func (sw *SocketWire) SetDisconnectCallback(callback func(deviceUUID string)) {
	sw.disconnectCallback = callback
}

// GetConnectionState returns the connection state for a device
func (sw *SocketWire) GetConnectionState(targetUUID string) ConnectionState {
	sw.connMutex.RLock()
	defer sw.connMutex.RUnlock()
	return sw.connectionStates[targetUUID]
}

// ShouldActAsCentral determines if this device should initiate connection
func (sw *SocketWire) ShouldActAsCentral(targetUUID string) bool {
	return sw.localUUID > targetUUID
}

// Cleanup closes all connections and stops the listener
func (sw *SocketWire) Cleanup() {
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
func (sw *SocketWire) logSentMessage(targetUUID string, msg *CharacteristicMessage) {
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
func (sw *SocketWire) logReceivedMessage(remoteUUID string, msg *CharacteristicMessage) {
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

func (sw *SocketWire) GetRole() DeviceRole {
	return sw.role
}

func (sw *SocketWire) GetPlatform() Platform {
	return sw.platform
}

func (sw *SocketWire) GetDeviceName() string {
	return sw.deviceName
}

func (sw *SocketWire) GetRSSI() int {
	return sw.simulator.GenerateRSSI(sw.distance)
}

func (sw *SocketWire) SetDistance(meters float64) {
	sw.distance = meters
}

func (sw *SocketWire) GetSimulator() *Simulator {
	return sw.simulator
}

// DiscoverDevices finds other devices by scanning for socket files
func (sw *SocketWire) DiscoverDevices() ([]string, error) {
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
func (sw *SocketWire) StartDiscovery(callback func(deviceUUID string)) chan struct{} {
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
func (sw *SocketWire) WriteGATTTable(table *GATTTable) error {
	devicePath := filepath.Join(sw.debugLogPath, sw.localUUID)
	gattPath := filepath.Join(devicePath, "gatt.json")

	data, err := json.MarshalIndent(table, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal GATT table: %w", err)
	}

	return os.WriteFile(gattPath, data, 0644)
}

// ReadGATTTable reads GATT table from filesystem
func (sw *SocketWire) ReadGATTTable(deviceUUID string) (*GATTTable, error) {
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
func (sw *SocketWire) WriteAdvertisingData(advData *AdvertisingData) error {
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
func (sw *SocketWire) ReadAdvertisingData(deviceUUID string) (*AdvertisingData, error) {
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
func (sw *SocketWire) NotifyCharacteristic(targetUUID, serviceUUID, charUUID string, data []byte) error {
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
func (sw *SocketWire) SubscribeCharacteristic(targetUUID, serviceUUID, charUUID string) error {
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
func (sw *SocketWire) UnsubscribeCharacteristic(targetUUID, serviceUUID, charUUID string) error {
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
func (sw *SocketWire) SetBasePath(path string) {
	sw.debugLogPath = path
}
