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
	Operation      string `json:"op"`             // "write", "read", "notify", "indicate", "subscribe", "unsubscribe", "subscribe_ack"
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

// ConnectionRole represents the role in a specific connection
type ConnectionRole string

const (
	ConnectionRoleCentral    ConnectionRole = "central"    // We initiated, we can write
	ConnectionRolePeripheral ConnectionRole = "peripheral" // They initiated, we can only notify
)

// RoleConnection represents a single directional BLE connection with a specific role
type RoleConnection struct {
	conn               net.Conn
	role               ConnectionRole // Our role on this connection (Central or Peripheral)
	remoteUUID         string
	mtu                int            // Current MTU (starts at 23)
	mtuNegotiated      bool           // Has MTU negotiation completed?
	mtuNegotiationTime time.Time      // When negotiation completed
	subscriptions      map[string]bool // Track subscriptions (key: serviceUUID+charUUID)
	subMutex           sync.RWMutex    // Protects subscriptions map
	sendMutex          sync.Mutex      // Protects writes to this connection
}

// DualConnection represents the dual-role BLE connection between two devices
// This matches real BLE where bidirectional communication requires TWO logical connections:
// - One where we are Central (we can write, they can notify)
// - One where we are Peripheral (they can write, we can notify)
type DualConnection struct {
	remoteUUID    string

	// Connection where we act as Central (we write to their characteristics)
	asCentral     *RoleConnection

	// Connection where we act as Peripheral (they write to our characteristics, we notify them)
	asPeripheral  *RoleConnection

	// Shared state
	state         ConnectionState
	monitorStop   chan struct{}
	stateMutex    sync.RWMutex
}

// Wire implements BLE communication using Unix Domain Sockets with dual-role architecture
// Each device creates TWO sockets:
// - peripheralSocket: Accepts connections from Centrals (we act as Peripheral)
// - centralSocket: Accepts connections from Peripherals (we act as Central)
// This naturally enforces BLE's asymmetric communication model through socket topology
type Wire struct {
	localUUID            string
	platform             Platform
	deviceName           string
	role                 DeviceRole
	simulator            *Simulator
	mtu                  int
	distance             float64

	// Dual socket infrastructure - matches real BLE dual-role architecture
	peripheralSocketPath string          // Socket for accepting Central connections (we are Peripheral)
	peripheralListener   net.Listener
	centralSocketPath    string          // Socket for accepting Peripheral connections (we are Central)
	centralListener      net.Listener

	// Dual connections to each peer
	connections          map[string]*DualConnection // remoteUUID -> dual connection
	connMutex            sync.RWMutex

	// Callbacks
	disconnectCallback        func(deviceUUID string)
	subscriptionCallback      func(remoteUUID, serviceUUID, charUUID string) // Called when a Central subscribes to our characteristic

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

// newWireInternal creates a new socket-based wire implementation with dual-role architecture
func newWireInternal(deviceUUID string, platform Platform, deviceName string, config *SimulationConfig) (*Wire, error) {
	if config == nil {
		config = DefaultSimulationConfig()
	}

	if deviceName == "" {
		deviceName = deviceUUID
	}

	// Create dual socket paths for realistic BLE dual-role simulation
	peripheralSocketPath := fmt.Sprintf("/tmp/auraphone-%s-peripheral.sock", deviceUUID)
	centralSocketPath := fmt.Sprintf("/tmp/auraphone-%s-central.sock", deviceUUID)

	w := &Wire{
		localUUID:            deviceUUID,
		platform:             platform,
		deviceName:           deviceName,
		role:                 RoleDual,
		simulator:            NewSimulator(config),
		mtu:                  config.DefaultMTU,
		distance:             1.0,
		peripheralSocketPath: peripheralSocketPath,
		centralSocketPath:    centralSocketPath,
		connections:          make(map[string]*DualConnection),
		messageHandlers:      make(map[string]func(*CharacteristicMessage)),
		stopChan:             make(chan struct{}),
		enableDebugLog:       true,
		debugLogPath:         "data",
	}

	return w, nil
}

// InitializeDevice sets up the dual socket listeners and debug log directories
func (sw *Wire) InitializeDevice() error {
	// Remove old sockets if they exist
	os.Remove(sw.peripheralSocketPath)
	os.Remove(sw.centralSocketPath)

	// Create Peripheral socket listener (accepts connections from Centrals)
	peripheralListener, err := net.Listen("unix", sw.peripheralSocketPath)
	if err != nil {
		return fmt.Errorf("failed to create peripheral socket listener: %w", err)
	}
	sw.peripheralListener = peripheralListener

	// Create Central socket listener (accepts connections from Peripherals)
	centralListener, err := net.Listen("unix", sw.centralSocketPath)
	if err != nil {
		peripheralListener.Close()
		return fmt.Errorf("failed to create central socket listener: %w", err)
	}
	sw.centralListener = centralListener

	logger.Debug(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		"🔌 Dual socket listeners created:\n  Peripheral: %s\n  Central: %s",
		sw.peripheralSocketPath, sw.centralSocketPath)

	// Start accepting connections on both sockets
	sw.wg.Add(2)
	go sw.acceptLoopPeripheral()
	go sw.acceptLoopCentral()

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

// acceptLoopPeripheral accepts incoming connections from Centrals (we act as Peripheral)
func (sw *Wire) acceptLoopPeripheral() {
	defer sw.wg.Done()

	for {
		select {
		case <-sw.stopChan:
			return
		default:
		}

		// Set accept deadline to allow periodic stopChan checks
		if unixListener, ok := sw.peripheralListener.(*net.UnixListener); ok {
			unixListener.SetDeadline(time.Now().Add(1 * time.Second))
		}

		conn, err := sw.peripheralListener.Accept()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue // Check stopChan and try again
			}
			select {
			case <-sw.stopChan:
				return
			default:
				logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
					"Peripheral accept error: %v", err)
				continue
			}
		}

		// Handle incoming connection where we are Peripheral (they are Central)
		sw.wg.Add(1)
		go sw.handleIncomingConnection(conn, ConnectionRolePeripheral)
	}
}

// acceptLoopCentral accepts incoming connections from Peripherals (we act as Central)
func (sw *Wire) acceptLoopCentral() {
	defer sw.wg.Done()

	for {
		select {
		case <-sw.stopChan:
			return
		default:
		}

		// Set accept deadline to allow periodic stopChan checks
		if unixListener, ok := sw.centralListener.(*net.UnixListener); ok {
			unixListener.SetDeadline(time.Now().Add(1 * time.Second))
		}

		conn, err := sw.centralListener.Accept()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue // Check stopChan and try again
			}
			select {
			case <-sw.stopChan:
				return
			default:
				logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
					"Central accept error: %v", err)
				continue
			}
		}

		// Handle incoming connection where we are Central (they are Peripheral)
		sw.wg.Add(1)
		go sw.handleIncomingConnection(conn, ConnectionRoleCentral)
	}
}

// handleIncomingConnection handles an incoming connection with specified role
func (sw *Wire) handleIncomingConnection(conn net.Conn, ourRole ConnectionRole) {
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
		"📞 Accepted %s connection from %s", ourRole, remoteUUID[:8])

	// Create RoleConnection for this directional connection
	roleConn := &RoleConnection{
		conn:          conn,
		role:          ourRole,
		remoteUUID:    remoteUUID,
		mtu:           23, // Start with BLE minimum MTU
		mtuNegotiated: false,
		subscriptions: make(map[string]bool),
	}

	// Get or create DualConnection for this peer
	sw.connMutex.Lock()
	dualConn, exists := sw.connections[remoteUUID]
	if !exists {
		dualConn = &DualConnection{
			remoteUUID:  remoteUUID,
			state:       StateConnected,
			monitorStop: make(chan struct{}),
		}
		sw.connections[remoteUUID] = dualConn
	}

	// Attach this connection to the appropriate role slot
	if ourRole == ConnectionRoleCentral {
		if dualConn.asCentral != nil {
			logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
				"🔌 Replacing existing Central connection to %s", remoteUUID[:8])
			dualConn.asCentral.conn.Close()
		}
		dualConn.asCentral = roleConn
		logger.Info(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
			"✅ Central connection established with %s (we write, they notify)", remoteUUID[:8])
	} else {
		if dualConn.asPeripheral != nil {
			logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
				"🔌 Replacing existing Peripheral connection to %s", remoteUUID[:8])
			dualConn.asPeripheral.conn.Close()
		}
		dualConn.asPeripheral = roleConn
		logger.Info(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
			"✅ Peripheral connection established with %s (they write, we notify)", remoteUUID[:8])
	}

	// Start connection monitoring if this is the first connection
	if !exists {
		sw.startConnectionMonitoringWithConn(dualConn, remoteUUID)
	}
	sw.connMutex.Unlock()

	// Read messages from this connection
	sw.readLoop(remoteUUID, roleConn)

	// Connection closed - clean up this role connection
	sw.connMutex.Lock()
	if ourRole == ConnectionRoleCentral {
		if dualConn.asCentral == roleConn {
			dualConn.asCentral = nil
			logger.Debug(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
				"🔌 Central connection closed from %s", remoteUUID[:8])
		}
	} else {
		if dualConn.asPeripheral == roleConn {
			dualConn.asPeripheral = nil
			logger.Debug(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
				"🔌 Peripheral connection closed from %s", remoteUUID[:8])
		}
	}

	// If both connections are gone, clean up DualConnection
	if dualConn.asCentral == nil && dualConn.asPeripheral == nil {
		delete(sw.connections, remoteUUID)
		dualConn.stateMutex.Lock()
		dualConn.state = StateDisconnected
		dualConn.stateMutex.Unlock()
		sw.connMutex.Unlock()

		logger.Info(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
			"🔌 All connections closed to %s", remoteUUID[:8])

		// Notify disconnect callback
		if sw.disconnectCallback != nil {
			sw.disconnectCallback(remoteUUID)
		}
	} else {
		sw.connMutex.Unlock()
	}
}

// readLoop reads messages from a role connection
func (sw *Wire) readLoop(remoteUUID string, roleConn *RoleConnection) {
	conn := roleConn.conn
	ourRole := roleConn.role

	// Determine what operations are valid based on our role
	// If we are Central: they can notify us (they are Peripheral)
	// If we are Peripheral: they can write to us (they are Central)
	var validOps map[string]bool
	if ourRole == ConnectionRoleCentral {
		// They are Peripheral, they can notify/indicate to us
		validOps = map[string]bool{"notify": true, "indicate": true, "subscribe_ack": true}
	} else {
		// They are Central, they can write/read from us
		validOps = map[string]bool{"write": true, "write_no_response": true, "read": true, "subscribe": true, "unsubscribe": true}
	}

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

		// Validate protocol: check if operation is valid for this connection's role
		if !validOps[msg.Operation] {
			theirRole := ConnectionRoleCentral
			if ourRole == ConnectionRoleCentral {
				theirRole = ConnectionRolePeripheral
			}
			logger.Error(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
				"⚠️  PROTOCOL VIOLATION: %s (role=%s) tried to %s on connection where we are %s",
				remoteUUID[:8], theirRole, msg.Operation, ourRole)
			continue // Drop invalid message
		}

		// Log for debugging
		if sw.enableDebugLog {
			sw.logReceivedMessage(remoteUUID, &msg)
		}

		theirRole := "peripheral"
		if ourRole == ConnectionRolePeripheral {
			theirRole = "central"
		}
		logger.TraceJSON(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
			fmt.Sprintf("📥 RX %s [%s→%s] (from %s, svc=%s, char=%s, %d bytes)",
				msg.Operation, theirRole, ourRole, remoteUUID[:8],
				msg.ServiceUUID[len(msg.ServiceUUID)-4:],
				msg.CharUUID[len(msg.CharUUID)-4:], len(msg.Data)), &msg)

		// Dispatch to handler
		sw.dispatchMessage(&msg)
	}
}

// Connect establishes dual connections to a remote device (both Central and Peripheral roles)
// This matches real BLE where bidirectional communication requires TWO logical connections
func (sw *Wire) Connect(targetUUID string) error {
	sw.connMutex.Lock()
	dualConn, exists := sw.connections[targetUUID]
	if exists {
		dualConn.stateMutex.RLock()
		state := dualConn.state
		dualConn.stateMutex.RUnlock()
		if state != StateDisconnected && state != 0 {
			sw.connMutex.Unlock()
			logger.Debug(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
				"🔌 Connect attempt to %s BLOCKED (current state: %s)", targetUUID[:8], state.String())
			return fmt.Errorf("already connected or connecting to %s", targetUUID[:8])
		}
	} else {
		dualConn = &DualConnection{
			remoteUUID:  targetUUID,
			state:       StateConnecting,
			monitorStop: make(chan struct{}),
		}
		sw.connections[targetUUID] = dualConn
	}
	dualConn.stateMutex.Lock()
	dualConn.state = StateConnecting
	dualConn.stateMutex.Unlock()
	sw.connMutex.Unlock()

	logger.Debug(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		"🔌 Establishing dual connections to %s (delay: simulated)", targetUUID[:8])

	// Simulate connection delay
	delay := sw.simulator.ConnectionDelay()
	time.Sleep(delay)

	// Check if connection succeeds
	if !sw.simulator.ShouldConnectionSucceed() {
		sw.connMutex.Lock()
		dualConn.stateMutex.Lock()
		dualConn.state = StateDisconnected
		dualConn.stateMutex.Unlock()
		sw.connMutex.Unlock()
		logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
			"❌ Connection to %s FAILED (simulated interference)", targetUUID[:8])
		return fmt.Errorf("connection failed (timeout or interference)")
	}

	// Establish FIRST connection: We connect to their Peripheral socket (we act as Central)
	targetPeripheralSocket := fmt.Sprintf("/tmp/auraphone-%s-peripheral.sock", targetUUID)
	centralConn, err := sw.dialAndHandshake(targetPeripheralSocket)
	if err != nil {
		sw.connMutex.Lock()
		dualConn.stateMutex.Lock()
		dualConn.state = StateDisconnected
		dualConn.stateMutex.Unlock()
		sw.connMutex.Unlock()
		return fmt.Errorf("failed to establish Central connection: %w", err)
	}

	// Establish SECOND connection: We connect to their Central socket (we act as Peripheral)
	targetCentralSocket := fmt.Sprintf("/tmp/auraphone-%s-central.sock", targetUUID)
	peripheralConn, err := sw.dialAndHandshake(targetCentralSocket)
	if err != nil {
		centralConn.Close()
		sw.connMutex.Lock()
		dualConn.stateMutex.Lock()
		dualConn.state = StateDisconnected
		dualConn.stateMutex.Unlock()
		sw.connMutex.Unlock()
		return fmt.Errorf("failed to establish Peripheral connection: %w", err)
	}

	// Create RoleConnections
	centralRoleConn := &RoleConnection{
		conn:          centralConn,
		role:          ConnectionRoleCentral,
		remoteUUID:    targetUUID,
		mtu:           23,
		mtuNegotiated: false,
		subscriptions: make(map[string]bool),
	}

	peripheralRoleConn := &RoleConnection{
		conn:          peripheralConn,
		role:          ConnectionRolePeripheral,
		remoteUUID:    targetUUID,
		mtu:           23,
		mtuNegotiated: false,
		subscriptions: make(map[string]bool),
	}

	// Store both connections
	sw.connMutex.Lock()
	dualConn.asCentral = centralRoleConn
	dualConn.asPeripheral = peripheralRoleConn
	dualConn.stateMutex.Lock()
	dualConn.state = StateConnected
	dualConn.stateMutex.Unlock()
	sw.connMutex.Unlock()

	logger.Info(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		"✅ Dual connections established with %s:\n  As Central: %s\n  As Peripheral: %s",
		targetUUID[:8], targetPeripheralSocket, targetCentralSocket)

	// Start read loops for both connections
	sw.wg.Add(2)
	go func() {
		defer sw.wg.Done()
		sw.readLoop(targetUUID, centralRoleConn)

		// Central connection closed
		sw.connMutex.Lock()
		if dualConn.asCentral == centralRoleConn {
			dualConn.asCentral = nil
			logger.Debug(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
				"🔌 Central connection closed to %s", targetUUID[:8])
		}
		sw.checkAndCleanupDualConnection(dualConn)
		sw.connMutex.Unlock()
	}()

	go func() {
		defer sw.wg.Done()
		sw.readLoop(targetUUID, peripheralRoleConn)

		// Peripheral connection closed
		sw.connMutex.Lock()
		if dualConn.asPeripheral == peripheralRoleConn {
			dualConn.asPeripheral = nil
			logger.Debug(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
				"🔌 Peripheral connection closed to %s", targetUUID[:8])
		}
		sw.checkAndCleanupDualConnection(dualConn)
		sw.connMutex.Unlock()
	}()

	// Start connection monitoring
	sw.startConnectionMonitoring(targetUUID)

	// Start automatic MTU negotiation in background for both connections
	// This simulates iOS auto-negotiation and Android RequestMtu() behavior
	go sw.negotiateMTU(centralRoleConn, targetUUID, "Central")
	go sw.negotiateMTU(peripheralRoleConn, targetUUID, "Peripheral")

	return nil
}

// negotiateMTU performs automatic MTU negotiation after connection is established
// This simulates:
//   - iOS: OS automatically negotiates MTU after service discovery (~500ms delay)
//   - Android: App calls RequestMtu() after discovering services
func (sw *Wire) negotiateMTU(roleConn *RoleConnection, targetUUID string, roleName string) {
	// Simulate service discovery delay before MTU negotiation
	// Real BLE: iOS takes 500ms-2s, Android varies by device
	delay := sw.simulator.ServiceDiscoveryDelay()
	time.Sleep(delay)

	// Check if connection still exists
	sw.connMutex.RLock()
	dualConn, exists := sw.connections[targetUUID]
	sw.connMutex.RUnlock()

	if !exists {
		logger.Debug(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
			"⚠️  MTU negotiation skipped: connection to %s no longer exists", targetUUID[:8])
		return
	}

	dualConn.stateMutex.RLock()
	state := dualConn.state
	dualConn.stateMutex.RUnlock()

	if state != StateConnected {
		logger.Debug(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
			"⚠️  MTU negotiation skipped: connection to %s not in Connected state (current: %s)",
			targetUUID[:8], state.String())
		return
	}

	// Negotiate MTU (both sides propose their max, take minimum)
	// iOS: typically 185 bytes, Android: 185-512 bytes
	proposedMTU := sw.simulator.config.DefaultMTU
	negotiatedMTU := sw.simulator.NegotiatedMTU(proposedMTU, proposedMTU)

	// Update MTU for this connection
	roleConn.sendMutex.Lock()
	roleConn.mtu = negotiatedMTU
	roleConn.mtuNegotiated = true
	roleConn.mtuNegotiationTime = time.Now()
	roleConn.sendMutex.Unlock()

	logger.Info(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		"📏 MTU negotiated: %d bytes with %s [%s role] (delay: %dms)",
		negotiatedMTU, targetUUID[:8], roleName, delay.Milliseconds())
}

// dialAndHandshake dials a socket and performs UUID handshake
func (sw *Wire) dialAndHandshake(socketPath string) (net.Conn, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to dial %s: %w", socketPath, err)
	}

	// Send our UUID as first message (handshake)
	uuidBytes := []byte(sw.localUUID)
	if err := binary.Write(conn, binary.BigEndian, uint32(len(uuidBytes))); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to send UUID length: %w", err)
	}
	if _, err := conn.Write(uuidBytes); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to send UUID: %w", err)
	}

	return conn, nil
}

// checkAndCleanupDualConnection cleans up if both connections are closed (must be called with connMutex held)
func (sw *Wire) checkAndCleanupDualConnection(dualConn *DualConnection) {
	if dualConn.asCentral == nil && dualConn.asPeripheral == nil {
		delete(sw.connections, dualConn.remoteUUID)
		dualConn.stateMutex.Lock()
		dualConn.state = StateDisconnected
		dualConn.stateMutex.Unlock()

		logger.Info(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
			"🔌 All connections closed to %s", dualConn.remoteUUID[:8])

		// Notify disconnect callback
		if sw.disconnectCallback != nil {
			sw.disconnectCallback(dualConn.remoteUUID)
		}
	}
}

// Disconnect closes both connections to a device
func (sw *Wire) Disconnect(targetUUID string) error {
	sw.connMutex.Lock()
	dualConn, exists := sw.connections[targetUUID]
	if !exists {
		sw.connMutex.Unlock()
		return fmt.Errorf("not connected to %s", targetUUID[:8])
	}

	dualConn.stateMutex.Lock()
	if dualConn.state != StateConnected {
		dualConn.stateMutex.Unlock()
		sw.connMutex.Unlock()
		return fmt.Errorf("not connected to %s (state: %s)", targetUUID[:8], dualConn.state.String())
	}
	dualConn.state = StateDisconnecting
	dualConn.stateMutex.Unlock()
	sw.connMutex.Unlock()

	// Stop monitoring
	sw.stopConnectionMonitoring(targetUUID)

	// Simulate disconnection delay
	delay := sw.simulator.DisconnectDelay()
	time.Sleep(delay)

	// Close both connections
	sw.connMutex.Lock()
	if dualConn.asCentral != nil {
		dualConn.asCentral.conn.Close()
		dualConn.asCentral = nil
	}
	if dualConn.asPeripheral != nil {
		dualConn.asPeripheral.conn.Close()
		dualConn.asPeripheral = nil
	}
	delete(sw.connections, targetUUID)
	dualConn.stateMutex.Lock()
	dualConn.state = StateDisconnected
	dualConn.stateMutex.Unlock()
	sw.connMutex.Unlock()

	logger.Debug(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		"🔌 Disconnected from %s (both connections closed)", targetUUID[:8])

	return nil
}

// sendViaRoleConnection sends data over a specific role connection
// This is the low-level send that implements fragmentation, MTU, and packet loss
func (sw *Wire) sendViaRoleConnection(roleConn *RoleConnection, targetUUID string, data []byte) error {
	roleConn.sendMutex.Lock()
	defer roleConn.sendMutex.Unlock()

	conn := roleConn.conn
	mtu := roleConn.mtu

	// Fragment data based on connection-specific MTU
	fragments := sw.simulator.FragmentData(data, mtu)

	// Send complete message length first
	totalLen := uint32(len(data))
	if err := binary.Write(conn, binary.BigEndian, totalLen); err != nil {
		return fmt.Errorf("failed to write message length: %w", err)
	}

	// Send each fragment with realistic BLE timing and packet loss
	for i, fragment := range fragments {
		if i > 0 {
			time.Sleep(2 * time.Millisecond)
		}

		var lastErr error
		for attempt := 0; attempt <= sw.simulator.config.MaxRetries; attempt++ {
			if attempt > 0 {
				time.Sleep(time.Duration(sw.simulator.config.RetryDelay) * time.Millisecond)
				logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
					"🔄 Retrying packet to %s [%s] (attempt %d/%d, fragment %d/%d)",
					targetUUID[:8], roleConn.role, attempt+1, sw.simulator.config.MaxRetries+1, i+1, len(fragments))
			}

			if !sw.simulator.ShouldPacketSucceed() && attempt < sw.simulator.config.MaxRetries {
				logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
					"📉 Simulated packet loss to %s [%s] (attempt %d/%d, fragment %d/%d)",
					targetUUID[:8], roleConn.role, attempt+1, sw.simulator.config.MaxRetries+1, i+1, len(fragments))
				lastErr = fmt.Errorf("packet loss (attempt %d/%d)", attempt+1, sw.simulator.config.MaxRetries+1)
				continue
			}

			if _, err := conn.Write(fragment); err != nil {
				lastErr = fmt.Errorf("failed to write fragment: %w", err)
				continue
			}

			lastErr = nil
			break
		}

		if lastErr != nil {
			logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
				"❌ Failed to send fragment %d/%d to %s [%s] after %d retries: %v",
				i+1, len(fragments), targetUUID[:8], roleConn.role, sw.simulator.config.MaxRetries, lastErr)
			return lastErr
		}
	}

	return nil
}

// WriteCharacteristic writes to a characteristic (automatically uses Central connection)
// In real BLE, writes ALWAYS go from Central to Peripheral
func (sw *Wire) WriteCharacteristic(targetUUID, serviceUUID, charUUID string, data []byte) error {
	// Get Central connection (where we write to their characteristics)
	sw.connMutex.RLock()
	dualConn, exists := sw.connections[targetUUID]
	sw.connMutex.RUnlock()

	if !exists {
		return fmt.Errorf("not connected to %s", targetUUID[:8])
	}

	if dualConn.asCentral == nil {
		return fmt.Errorf("no Central connection to %s (cannot write)", targetUUID[:8])
	}

	msg := CharacteristicMessage{
		Operation:   "write",
		ServiceUUID: serviceUUID,
		CharUUID:    charUUID,
		Data:        data,
		Timestamp:   time.Now().UnixNano(),
		SenderUUID:  sw.localUUID,
	}

	logger.TraceJSON(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		fmt.Sprintf("📤 TX Write [central→peripheral] (to %s, svc=%s, char=%s, %d bytes)",
			targetUUID[:8], serviceUUID[len(serviceUUID)-4:], charUUID[len(charUUID)-4:], len(data)), &msg)

	return sw.sendCharacteristicMessageViaRole(dualConn.asCentral, targetUUID, &msg)
}

// WriteCharacteristicNoResponse sends a write without waiting for response (uses Central connection)
func (sw *Wire) WriteCharacteristicNoResponse(targetUUID, serviceUUID, charUUID string, data []byte) error {
	// Get Central connection
	sw.connMutex.RLock()
	dualConn, exists := sw.connections[targetUUID]
	sw.connMutex.RUnlock()

	if !exists {
		return fmt.Errorf("not connected to %s", targetUUID[:8])
	}

	if dualConn.asCentral == nil {
		return fmt.Errorf("no Central connection to %s (cannot write)", targetUUID[:8])
	}

	msg := CharacteristicMessage{
		Operation:   "write_no_response",
		ServiceUUID: serviceUUID,
		CharUUID:    charUUID,
		Data:        data,
		Timestamp:   time.Now().UnixNano(),
		SenderUUID:  sw.localUUID,
	}

	logger.TraceJSON(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		fmt.Sprintf("📤 TX Write NO Response [central→peripheral] (to %s, svc=%s, char=%s, %d bytes)",
			targetUUID[:8], serviceUUID[len(serviceUUID)-4:], charUUID[len(charUUID)-4:], len(data)), &msg)

	// Send asynchronously (fire and forget)
	go func() {
		if err := sw.sendCharacteristicMessageViaRole(dualConn.asCentral, targetUUID, &msg); err != nil {
			logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
				"❌ Write NO Response transmission FAILED to %s: %v", targetUUID[:8], err)
		}
	}()

	return nil
}

// sendCharacteristicMessageViaRole marshals and sends a message via specific role connection
func (sw *Wire) sendCharacteristicMessageViaRole(roleConn *RoleConnection, targetUUID string, msg *CharacteristicMessage) error {
	msgData, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	// Log for debugging
	if sw.enableDebugLog {
		sw.logSentMessage(targetUUID, msg)
	}

	return sw.sendViaRoleConnection(roleConn, targetUUID, msgData)
}


// dispatchMessage routes incoming messages to handlers or queues them
func (sw *Wire) dispatchMessage(msg *CharacteristicMessage) {
	// Handle subscription operations specially (they update connection state at wire level)
	// But still dispatch them to the message queue so CBPeripheralManager can process them
	if msg.Operation == "subscribe" || msg.Operation == "unsubscribe" {
		sw.handleSubscriptionMessage(msg)
		// Continue to dispatch - don't return yet
	}

	// Handle subscription acknowledgments (informational only, no action needed)
	if msg.Operation == "subscribe_ack" {
		logger.Trace(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
			"📥 RX Subscribe ACK from %s (svc=%s, char=%s) - subscription confirmed",
			msg.SenderUUID[:8], msg.ServiceUUID[len(msg.ServiceUUID)-4:], msg.CharUUID[len(msg.CharUUID)-4:])
		return
	}

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

// handleSubscriptionMessage processes subscribe/unsubscribe requests from Central
// This updates the subscription state on the Peripheral connection so notifications can be sent
func (sw *Wire) handleSubscriptionMessage(msg *CharacteristicMessage) {
	sw.connMutex.RLock()
	dualConn := sw.connections[msg.SenderUUID]
	sw.connMutex.RUnlock()

	if dualConn == nil {
		logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
			"⚠️  Received %s from unknown device %s", msg.Operation, msg.SenderUUID[:8])
		return
	}

	if dualConn.asPeripheral == nil {
		logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
			"⚠️  Received %s but no Peripheral connection exists to %s", msg.Operation, msg.SenderUUID[:8])
		return
	}

	// Update subscription state on our Peripheral connection
	// When they subscribe, we can now send notifications to them
	key := msg.ServiceUUID + msg.CharUUID
	dualConn.asPeripheral.subMutex.Lock()
	if msg.Operation == "subscribe" {
		dualConn.asPeripheral.subscriptions[key] = true
		logger.Debug(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
			"✅ Central %s subscribed to characteristic %s (svc=%s) - notifications enabled",
			msg.SenderUUID[:8], msg.CharUUID[len(msg.CharUUID)-4:], msg.ServiceUUID[len(msg.ServiceUUID)-4:])

		// Notify application layer about subscription (after unlocking mutex)
		if sw.subscriptionCallback != nil {
			remoteUUID := msg.SenderUUID
			serviceUUID := msg.ServiceUUID
			charUUID := msg.CharUUID
			dualConn.asPeripheral.subMutex.Unlock()
			sw.subscriptionCallback(remoteUUID, serviceUUID, charUUID)
			dualConn.asPeripheral.subMutex.Lock()
		}
	} else {
		delete(dualConn.asPeripheral.subscriptions, key)
		logger.Debug(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
			"🔕 Central %s unsubscribed from characteristic %s (svc=%s) - notifications disabled",
			msg.SenderUUID[:8], msg.CharUUID[len(msg.CharUUID)-4:], msg.ServiceUUID[len(msg.ServiceUUID)-4:])
	}
	dualConn.asPeripheral.subMutex.Unlock()

	// Send acknowledgment back to Central to prevent race conditions
	// This ensures Central knows subscription is active before sending first notification
	ackMsg := CharacteristicMessage{
		Operation:   "subscribe_ack",
		ServiceUUID: msg.ServiceUUID,
		CharUUID:    msg.CharUUID,
		Timestamp:   time.Now().UnixNano(),
		SenderUUID:  sw.localUUID,
	}

	logger.Trace(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		"📤 TX Subscribe ACK [peripheral→central] (to %s, svc=%s, char=%s)",
		msg.SenderUUID[:8], msg.ServiceUUID[len(msg.ServiceUUID)-4:], msg.CharUUID[len(msg.CharUUID)-4:])

	// Send ACK via Peripheral connection (we notify them of subscription state change)
	go sw.sendCharacteristicMessageViaRole(dualConn.asPeripheral, msg.SenderUUID, &ackMsg)
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

// startConnectionMonitoring monitors dual connection health (looks up connection)
func (sw *Wire) startConnectionMonitoring(targetUUID string) {
	sw.connMutex.Lock()
	dualConn, exists := sw.connections[targetUUID]
	if !exists {
		sw.connMutex.Unlock()
		return
	}
	sw.connMutex.Unlock()

	sw.startConnectionMonitoringWithConn(dualConn, targetUUID)
}

// startConnectionMonitoringWithConn monitors dual connection health (connection already obtained)
// This version is used when caller already holds connMutex to avoid deadlock
func (sw *Wire) startConnectionMonitoringWithConn(dualConn *DualConnection, targetUUID string) {
	// Use DualConnection's monitorStop channel
	if dualConn.monitorStop == nil {
		dualConn.monitorStop = make(chan struct{})
	}
	stopChan := dualConn.monitorStop

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
				dualConn, exists := sw.connections[targetUUID]
				if !exists {
					sw.connMutex.Unlock()
					return
				}

				dualConn.stateMutex.RLock()
				state := dualConn.state
				dualConn.stateMutex.RUnlock()

				if state == StateConnected && sw.simulator.ShouldRandomlyDisconnect() {
					// Simulate random disconnect - close one or both connections
					logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
						"⚠️  Random disconnect simulation: closing connections to %s", targetUUID[:8])

					if dualConn.asCentral != nil {
						dualConn.asCentral.conn.Close()
					}
					if dualConn.asPeripheral != nil {
						dualConn.asPeripheral.conn.Close()
					}

					callback := sw.disconnectCallback
					sw.connMutex.Unlock()

					if callback != nil {
						callback(targetUUID)
					}

					// Stop monitoring
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

	dualConn, exists := sw.connections[targetUUID]
	if exists && dualConn.monitorStop != nil {
		close(dualConn.monitorStop)
		dualConn.monitorStop = nil
	}
}

// SetDisconnectCallback sets the callback for disconnections
func (sw *Wire) SetDisconnectCallback(callback func(deviceUUID string)) {
	sw.disconnectCallback = callback
}

// SetSubscriptionCallback sets a callback that's triggered when a Central subscribes to our characteristic
func (sw *Wire) SetSubscriptionCallback(callback func(remoteUUID, serviceUUID, charUUID string)) {
	sw.subscriptionCallback = callback
}

// GetConnectionState returns the connection state for a device
func (sw *Wire) GetConnectionState(targetUUID string) ConnectionState {
	sw.connMutex.RLock()
	dualConn, exists := sw.connections[targetUUID]
	sw.connMutex.RUnlock()

	if !exists {
		return StateDisconnected
	}

	dualConn.stateMutex.RLock()
	state := dualConn.state
	dualConn.stateMutex.RUnlock()
	return state
}

// ShouldActAsCentral determines if this device should initiate connection
func (sw *Wire) ShouldActAsCentral(targetUUID string) bool {
	return sw.localUUID > targetUUID
}

// Cleanup closes all connections and stops both listeners
func (sw *Wire) Cleanup() {
	// Signal shutdown
	close(sw.stopChan)

	// Close all dual connections
	sw.connMutex.Lock()
	for uuid, dualConn := range sw.connections {
		if dualConn.asCentral != nil {
			dualConn.asCentral.conn.Close()
		}
		if dualConn.asPeripheral != nil {
			dualConn.asPeripheral.conn.Close()
		}
		delete(sw.connections, uuid)
	}
	sw.connMutex.Unlock()

	// Close both listeners
	if sw.peripheralListener != nil {
		sw.peripheralListener.Close()
	}
	if sw.centralListener != nil {
		sw.centralListener.Close()
	}

	// Wait for goroutines
	sw.wg.Wait()

	// Remove both socket files
	os.Remove(sw.peripheralSocketPath)
	os.Remove(sw.centralSocketPath)

	logger.Debug(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		"🧹 Cleaned up dual socket wire")
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
				// Check if dual sockets exist (both peripheral and central)
				peripheralSock := fmt.Sprintf("/tmp/auraphone-%s-peripheral.sock", deviceName)
				centralSock := fmt.Sprintf("/tmp/auraphone-%s-central.sock", deviceName)
				if _, err1 := os.Stat(peripheralSock); err1 == nil {
					if _, err2 := os.Stat(centralSock); err2 == nil {
						devices = append(devices, deviceName)
					}
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

	// Ensure directory exists
	if err := os.MkdirAll(devicePath, 0755); err != nil {
		return fmt.Errorf("failed to create device directory: %w", err)
	}

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

	// Ensure directory exists
	if err := os.MkdirAll(devicePath, 0755); err != nil {
		return fmt.Errorf("failed to create device directory: %w", err)
	}

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

// NotifyCharacteristic sends a notification (automatically uses Peripheral connection)
// In real BLE, notifications ALWAYS go from Peripheral to Central
func (sw *Wire) NotifyCharacteristic(targetUUID, serviceUUID, charUUID string, data []byte) error {
	// Get Peripheral connection (where we notify them)
	sw.connMutex.RLock()
	dualConn, exists := sw.connections[targetUUID]
	sw.connMutex.RUnlock()

	if !exists {
		return fmt.Errorf("not connected to %s", targetUUID[:8])
	}

	if dualConn.asPeripheral == nil {
		return fmt.Errorf("no Peripheral connection to %s (cannot notify)", targetUUID[:8])
	}

	// Check subscription state - notifications require prior subscription
	// This matches real BLE behavior where Central must subscribe before receiving notifications
	key := serviceUUID + charUUID
	dualConn.asPeripheral.subMutex.RLock()
	subscribed := dualConn.asPeripheral.subscriptions[key]
	dualConn.asPeripheral.subMutex.RUnlock()

	if !subscribed {
		logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
			"⚠️  Cannot notify %s: not subscribed to characteristic %s (svc=%s)",
			targetUUID[:8], charUUID[len(charUUID)-4:], serviceUUID[len(serviceUUID)-4:])
		return fmt.Errorf("central not subscribed to characteristic %s", charUUID[:8])
	}

	msg := CharacteristicMessage{
		Operation:   "notify",
		ServiceUUID: serviceUUID,
		CharUUID:    charUUID,
		Data:        data,
		Timestamp:   time.Now().UnixNano(),
		SenderUUID:  sw.localUUID,
	}

	logger.TraceJSON(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		fmt.Sprintf("📤 TX Notify [peripheral→central] (to %s, svc=%s, char=%s, %d bytes)",
			targetUUID[:8], serviceUUID[len(serviceUUID)-4:], charUUID[len(charUUID)-4:], len(data)), &msg)

	// Simulate notification drops (realistic BLE behavior)
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
			if err := sw.sendCharacteristicMessageViaRole(dualConn.asPeripheral, targetUUID, &msg); err != nil {
				logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
					"❌ Notification transmission FAILED to %s (async): %v", targetUUID[:8], err)
			}
		}()
		return nil
	}

	return sw.sendCharacteristicMessageViaRole(dualConn.asPeripheral, targetUUID, &msg)
}

// SubscribeCharacteristic sends a subscription request (uses Central connection)
// In real BLE, subscribe operations are sent from Central to Peripheral
func (sw *Wire) SubscribeCharacteristic(targetUUID, serviceUUID, charUUID string) error {
	// Get Central connection (subscriptions are Central → Peripheral operations)
	sw.connMutex.RLock()
	dualConn, exists := sw.connections[targetUUID]
	sw.connMutex.RUnlock()

	if !exists {
		return fmt.Errorf("not connected to %s", targetUUID[:8])
	}

	if dualConn.asCentral == nil {
		return fmt.Errorf("no Central connection to %s (cannot subscribe)", targetUUID[:8])
	}

	msg := CharacteristicMessage{
		Operation:   "subscribe",
		ServiceUUID: serviceUUID,
		CharUUID:    charUUID,
		Timestamp:   time.Now().UnixNano(),
		SenderUUID:  sw.localUUID,
	}

	logger.TraceJSON(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		fmt.Sprintf("📤 TX Subscribe [central→peripheral] (to %s, svc=%s, char=%s)",
			targetUUID[:8], serviceUUID[len(serviceUUID)-4:], charUUID[len(charUUID)-4:]), &msg)

	return sw.sendCharacteristicMessageViaRole(dualConn.asCentral, targetUUID, &msg)
}

// UnsubscribeCharacteristic sends an unsubscription request (uses Central connection)
// In real BLE, unsubscribe operations are sent from Central to Peripheral
func (sw *Wire) UnsubscribeCharacteristic(targetUUID, serviceUUID, charUUID string) error {
	// Get Central connection
	sw.connMutex.RLock()
	dualConn, exists := sw.connections[targetUUID]
	sw.connMutex.RUnlock()

	if !exists {
		return fmt.Errorf("not connected to %s", targetUUID[:8])
	}

	if dualConn.asCentral == nil {
		return fmt.Errorf("no Central connection to %s (cannot unsubscribe)", targetUUID[:8])
	}

	msg := CharacteristicMessage{
		Operation:   "unsubscribe",
		ServiceUUID: serviceUUID,
		CharUUID:    charUUID,
		Timestamp:   time.Now().UnixNano(),
		SenderUUID:  sw.localUUID,
	}

	logger.TraceJSON(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
		fmt.Sprintf("📤 TX Unsubscribe [central→peripheral] (to %s, svc=%s, char=%s)",
			targetUUID[:8], serviceUUID[len(serviceUUID)-4:], charUUID[len(charUUID)-4:]), &msg)

	return sw.sendCharacteristicMessageViaRole(dualConn.asCentral, targetUUID, &msg)
}

// SetBasePath sets the base path for debug logs and discovery
func (sw *Wire) SetBasePath(path string) {
	sw.debugLogPath = path
}

// ReadCharacteristic sends a read request to target device (uses Central connection)
// In real BLE, read operations are sent from Central to Peripheral
func (w *Wire) ReadCharacteristic(targetUUID, serviceUUID, charUUID string) error {
	// Get Central connection (reads are Central → Peripheral operations)
	w.connMutex.RLock()
	dualConn, exists := w.connections[targetUUID]
	w.connMutex.RUnlock()

	if !exists {
		return fmt.Errorf("not connected to %s", targetUUID[:8])
	}

	if dualConn.asCentral == nil {
		return fmt.Errorf("no Central connection to %s (cannot read)", targetUUID[:8])
	}

	msg := CharacteristicMessage{
		Operation:   "read",
		ServiceUUID: serviceUUID,
		CharUUID:    charUUID,
		Timestamp:   time.Now().UnixNano(),
		SenderUUID:  w.localUUID,
	}

	logger.TraceJSON(fmt.Sprintf("%s %s", w.localUUID[:8], w.platform),
		fmt.Sprintf("📤 TX Read Request [central→peripheral] (to %s, svc=%s, char=%s)",
			targetUUID[:8], serviceUUID[len(serviceUUID)-4:], charUUID[len(charUUID)-4:]), &msg)

	return w.sendCharacteristicMessageViaRole(dualConn.asCentral, targetUUID, &msg)
}

// ReadAndConsumeCharacteristicMessagesFromInbox reads messages from a specific inbox type
// This provides compatibility with dual-role devices (iOS/Android) that have separate
// central_inbox and peripheral_inbox for bidirectional communication
//
// In the socket implementation, there's one unified message queue, but we need to filter
// messages by their intended recipient role to prevent one polling loop from consuming
// messages meant for the other role.
func (w *Wire) ReadAndConsumeCharacteristicMessagesFromInbox(inboxType string) ([]*CharacteristicMessage, error) {
	w.queueMutex.Lock()
	defer w.queueMutex.Unlock()

	logger.Trace(fmt.Sprintf("%s %s", w.localUUID[:8], w.platform),
		"📬 Reading from %s (filtering by role)", inboxType)

	if len(w.messageQueue) == 0 {
		return nil, nil
	}

	// Filter messages based on inbox type (operation determines which role should handle it)
	var filtered []*CharacteristicMessage
	var remaining []*CharacteristicMessage

	for _, msg := range w.messageQueue {
		var belongsToThisInbox bool

		if inboxType == "central_inbox" {
			// Central inbox receives notifications/indications from peripherals
			belongsToThisInbox = (msg.Operation == "notify" || msg.Operation == "indicate")
		} else { // "peripheral_inbox"
			// Peripheral inbox receives writes/reads/subscribes from centrals
			belongsToThisInbox = (msg.Operation == "write" || msg.Operation == "write_no_response" ||
			                      msg.Operation == "read" || msg.Operation == "subscribe" || msg.Operation == "unsubscribe")
		}

		if belongsToThisInbox {
			filtered = append(filtered, msg)
		} else {
			remaining = append(remaining, msg)
		}
	}

	// Update queue to only contain messages not consumed by this call
	w.messageQueue = remaining

	return filtered, nil
}
