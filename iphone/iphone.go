package iphone

import (
	"fmt"
	"path/filepath"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/swift"
	"github.com/user/auraphone-blue/wire"
)

// NewIPhone creates a new iPhone instance
// Hardware UUID is provided, DeviceID is loaded from cache or generated
func NewIPhone(hardwareUUID string) *IPhone {
	// Load or generate device ID (persists to ~/.auraphone-blue-data/{uuid}/cache/device_id.json)
	deviceID, err := phone.LoadOrGenerateDeviceID(hardwareUUID)
	if err != nil {
		panic(fmt.Sprintf("Failed to load/generate device ID: %v", err))
	}

	// Default first name to platform name (will be set by GUI for simulation)
	firstName := "iPhone"
	deviceName := fmt.Sprintf("iPhone (%s)", firstName)

	// Create identity manager (tracks all hardware UUID ↔ device ID mappings)
	dataDir := filepath.Join(phone.GetDataDir(), hardwareUUID)
	identityManager := phone.NewIdentityManager(hardwareUUID, deviceID, dataDir)

	// Load previously known device mappings from disk
	if err := identityManager.LoadFromDisk(); err != nil {
		logger.Warn(fmt.Sprintf("%s iOS", hardwareUUID[:8]), "Failed to load identity mappings: %v", err)
	}

	photoCache := phone.NewPhotoCache(hardwareUUID)
	meshView := phone.NewMeshView(deviceID, hardwareUUID, dataDir, photoCache)
	meshView.SetIdentityManager(identityManager)

	logger.Debug(fmt.Sprintf("%s iOS", hardwareUUID[:8]), "🏗️  NewIPhone: firstName='%s', deviceID='%s'", firstName, deviceID)

	return &IPhone{
		hardwareUUID:    hardwareUUID,
		deviceID:        deviceID,
		deviceName:      deviceName,
		firstName:       firstName,
		identityManager: identityManager,
		photoCache:      photoCache,
		photoChunker:    phone.NewPhotoChunker(),
		discovered:      make(map[string]phone.DiscoveredDevice),
		handshaked:      make(map[string]*HandshakeMessage),
		connectedPeers:  make(map[string]*swift.CBPeripheral),
		photoTransfers:  make(map[string]*phone.PhotoTransferState),
		meshView:        meshView,
		stopGossip:      make(chan struct{}),
		profile:         make(map[string]string),
		profileVersion:  0, // Start at version 0 (no profile data yet)
	}
}

// Start initializes the iPhone and begins advertising/scanning
func (ip *IPhone) Start() {
	// Create wire
	ip.wire = wire.NewWire(ip.hardwareUUID)

	// Start listening on socket
	err := ip.wire.Start()
	if err != nil {
		panic(err)
	}

	// Set up GATT message handler - this is the ONE place we handle ALL incoming messages
	ip.wire.SetGATTMessageHandler(func(peerUUID string, msg *wire.GATTMessage) {
		ip.handleGATTMessage(peerUUID, msg)
	})

	// Set up connection callback - handles when Centrals connect to us (as Peripheral)
	ip.wire.SetConnectCallback(func(peerUUID string, role wire.ConnectionRole) {
		if role == wire.RolePeripheral {
			// Someone connected to us as Central - create peripheral object for them
			// This enables bidirectional photo requests
			ip.handleIncomingCentralConnection(peerUUID)
		}
	})

	// Create CBCentralManager (for scanning and connecting as Central)
	ip.central = swift.NewCBCentralManager(ip, ip.hardwareUUID, ip.wire)

	// Create CBPeripheralManager (for advertising and accepting connections as Peripheral)
	ip.peripheral = swift.NewCBPeripheralManager(ip, ip.hardwareUUID, ip.deviceName, ip.wire)

	// Set up GATT services
	ip.setupGATTServices()

	// Start advertising
	ip.startAdvertising()

	// Start scanning
	ip.startScanning()

	// Set our firstName in mesh view so gossip messages include it
	ip.mu.RLock()
	firstName := ip.firstName
	deviceID := ip.deviceID
	ip.mu.RUnlock()
	ip.meshView.SetOurFirstName(firstName)

	// Start gossip protocol timer (sends gossip every 5 seconds to connected peers)
	ip.startGossipTimer()

	logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "✅ Started iPhone: %s (ID: %s)", firstName, deviceID)
}

// Stop shuts down the iPhone
func (ip *IPhone) Stop() {
	// Stop gossip timer first
	if ip.gossipTicker != nil {
		ip.gossipTicker.Stop()
		close(ip.stopGossip)
	}

	// Save mesh view before shutdown
	if ip.meshView != nil {
		if err := ip.meshView.Close(); err != nil {
			logger.Warn(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to save mesh view: %v", err)
		}
	}

	if ip.central != nil {
		ip.central.StopScan()
	}

	if ip.peripheral != nil {
		ip.peripheral.StopAdvertising()
	}

	// Save identity mappings before shutdown
	if ip.identityManager != nil {
		if err := ip.identityManager.SaveToDisk(); err != nil {
			logger.Warn(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to save identity mappings: %v", err)
		}
	}

	if ip.wire != nil {
		ip.wire.Stop()
	}

	logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🛑 Stopped iPhone")
}

// setupGATTServices creates the GATT database for this iPhone
func (ip *IPhone) setupGATTServices() {
	// Create Aura service with protocol and photo characteristics
	service := &swift.CBMutableService{
		UUID:      phone.AuraServiceUUID,
		IsPrimary: true,
		Characteristics: []*swift.CBMutableCharacteristic{
			{
				UUID:        phone.AuraProtocolCharUUID,
				Properties:  swift.CBCharacteristicPropertyWrite | swift.CBCharacteristicPropertyNotify,
				Permissions: swift.CBAttributePermissionsWriteable | swift.CBAttributePermissionsReadable,
				Value:       nil,
			},
			{
				UUID:        phone.AuraPhotoCharUUID,
				Properties:  swift.CBCharacteristicPropertyWrite | swift.CBCharacteristicPropertyNotify,
				Permissions: swift.CBAttributePermissionsWriteable,
				Value:       nil,
			},
			{
				UUID:        phone.AuraProfileCharUUID,
				Properties:  swift.CBCharacteristicPropertyRead | swift.CBCharacteristicPropertyNotify,
				Permissions: swift.CBAttributePermissionsReadable,
				Value:       nil,
			},
		},
	}

	ip.peripheral.AddService(service)
}

// startAdvertising begins BLE advertising
func (ip *IPhone) startAdvertising() {
	// REALISTIC iOS BLE: Advertising packet is limited to 31 bytes
	// To fit within this limit, we only advertise service UUIDs
	// Real iOS omits local name in background advertising for privacy and space
	advData := map[string]interface{}{
		"kCBAdvDataServiceUUIDs": []string{phone.AuraServiceUUID},
	}

	err := ip.peripheral.StartAdvertising(advData)
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to start advertising: %v", err)
	}
}

// startScanning begins scanning for other devices
func (ip *IPhone) startScanning() {
	ip.central.ScanForPeripherals([]string{phone.AuraServiceUUID}, nil)
}

// handleGATTMessage is the master dispatcher for ALL GATT messages
// This is called from the wire layer for every incoming message
func (ip *IPhone) handleGATTMessage(peerUUID string, msg *wire.GATTMessage) {
	logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "📬 GATT message from %s: type=%s, op=%s, char=%s",
		shortHash(peerUUID), msg.Type, msg.Operation, shortHash(msg.CharacteristicUUID))

	// Route to appropriate handler based on message type
	handled := false

	// Try central manager (handles notifications from peripherals we're connected to)
	if ip.central != nil {
		logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "   ➡️  Trying central manager")
		if ip.central.HandleGATTMessage(peerUUID, msg) {
			logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "   ✅ Central manager handled it")
			handled = true
		}
	}

	// Try peripheral manager (handles requests from centrals connecting to us)
	if !handled && ip.peripheral != nil {
		logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "   ➡️  Trying peripheral manager")
		if ip.peripheral.HandleGATTMessage(peerUUID, msg) {
			logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "   ✅ Peripheral manager handled it")
			handled = true
		}
	}

	if !handled {
		logger.Warn(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "⚠️  Unhandled GATT message: %s", msg.Type)
	}
}

// handleIncomingCentralConnection handles when a Central connects to us (we're Peripheral)
// This creates a CBPeripheral object so we can send requests back to them
func (ip *IPhone) handleIncomingCentralConnection(peerUUID string) {
	ip.mu.Lock()
	defer ip.mu.Unlock()

	// Check if already tracked
	if _, exists := ip.connectedPeers[peerUUID]; exists {
		return
	}

	// Get device name from discovered devices, or create a minimal entry
	deviceName := "Unknown"
	if device, exists := ip.discovered[peerUUID]; exists {
		deviceName = device.Name
	} else {
		// Central connected to us but we haven't discovered them yet
		// Add them to discovered map so photo callback will work later
		logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "📝 Adding Central %s to discovered map (connected before discovery)", shortHash(peerUUID))
		ip.discovered[peerUUID] = phone.DiscoveredDevice{
			HardwareUUID: peerUUID,
			Name:         deviceName,
			RSSI:         -45, // Default RSSI
		}
		// Trigger GUI callback so device appears in list
		if ip.callback != nil {
			ip.callback(ip.discovered[peerUUID])
		}
	}

	// Note: In real BLE, when a Central connects to us (we're Peripheral), we CANNOT
	// create a CBPeripheral object to discover their services. We can only send
	// notifications back on our own characteristics (which they've subscribed to).
	// This is the proper BLE behavior - Peripherals respond via notifications, not writes.

	logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔌 Central %s connected (acting as Peripheral)", shortHash(peerUUID))
}
