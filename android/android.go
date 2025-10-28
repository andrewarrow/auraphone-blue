package android

import (
	"fmt"
	"path/filepath"

	"github.com/user/auraphone-blue/kotlin"
	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/wire"
)

// NewAndroid creates a new Android instance
// Hardware UUID is provided, DeviceID is loaded from cache or generated
func NewAndroid(hardwareUUID string) *Android {
	// Load or generate device ID (persists to ~/.auraphone-blue-data/{uuid}/cache/device_id.json)
	deviceID, err := phone.LoadOrGenerateDeviceID(hardwareUUID)
	if err != nil {
		panic(fmt.Sprintf("Failed to load/generate device ID: %v", err))
	}

	// Default first name to platform name (will be set by GUI for simulation)
	firstName := "Android"
	deviceName := fmt.Sprintf("Android (%s)", firstName)

	// Create identity manager (tracks all hardware UUID ↔ device ID mappings)
	dataDir := filepath.Join(phone.GetDataDir(), hardwareUUID)
	identityManager := phone.NewIdentityManager(hardwareUUID, deviceID, dataDir)

	// Load previously known device mappings from disk
	if err := identityManager.LoadFromDisk(); err != nil {
		logger.Warn(fmt.Sprintf("%s Android", hardwareUUID[:8]), "Failed to load identity mappings: %v", err)
	}

	photoCache := phone.NewPhotoCache(hardwareUUID)
	meshView := phone.NewMeshView(deviceID, hardwareUUID, dataDir, photoCache)
	meshView.SetIdentityManager(identityManager)

	return &Android{
		hardwareUUID:    hardwareUUID,
		deviceID:        deviceID,
		deviceName:      deviceName,
		firstName:       firstName,
		identityManager: identityManager,
		photoCache:      photoCache,
		photoChunker:    phone.NewPhotoChunker(),
		discovered:      make(map[string]phone.DiscoveredDevice),
		handshaked:      make(map[string]*HandshakeMessage),
		connectedGatts:  make(map[string]*kotlin.BluetoothGatt),
		photoTransfers:  make(map[string]*phone.PhotoTransferState),
		meshView:        meshView,
		stopGossip:      make(chan struct{}),
		profile:         make(map[string]string),
		profileVersion:  1, // Start at version 1
	}
}

// Start initializes the Android device and begins advertising/scanning
func (a *Android) Start() {
	// Create wire
	a.wire = wire.NewWire(a.hardwareUUID)

	// Start listening on socket
	err := a.wire.Start()
	if err != nil {
		panic(err)
	}

	// Set up GATT message handler - this is the ONE place we handle ALL incoming messages
	a.wire.SetGATTMessageHandler(func(peerUUID string, msg *wire.GATTMessage) {
		a.handleGATTMessage(peerUUID, msg)
	})

	// Set up connection callback - handles when Centrals connect to us (as Peripheral)
	a.wire.SetConnectCallback(func(peerUUID string, role wire.ConnectionRole) {
		if role == wire.RolePeripheral {
			// Someone connected to us as Central - create GATT client for them
			// This enables bidirectional photo requests
			a.handleIncomingCentralConnection(peerUUID)
		}
	})

	// Create BluetoothManager with shared wire
	a.manager = kotlin.NewBluetoothManager(a.hardwareUUID, a.wire)

	// Get adapter
	adapter := a.manager.Adapter

	// Get scanner
	a.scanner = adapter.GetBluetoothLeScanner()

	// Create GATT server for peripheral mode (use wrapper to avoid method name collision)
	serverCallback := &androidGattServerCallback{android: a}
	a.gattServer = a.manager.OpenGattServer(serverCallback, a.deviceName, a.wire)

	// Get advertiser (links with GATT server)
	a.advertiser = adapter.GetBluetoothLeAdvertiser()
	a.advertiser.SetGattServer(a.gattServer)

	// Set up GATT services
	a.setupGATTServices()

	// Start advertising
	a.startAdvertising()

	// Start scanning
	a.startScanning()

	// Set our firstName in mesh view so gossip messages include it
	a.meshView.SetOurFirstName(a.firstName)

	// Start gossip protocol timer (sends gossip every 5 seconds to connected peers)
	a.startGossipTimer()

	logger.Info(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "✅ Started Android: %s (ID: %s)", a.firstName, a.deviceID)
}

// Stop stops the Android device
func (a *Android) Stop() {
	// Stop gossip timer
	if a.gossipTicker != nil {
		a.gossipTicker.Stop()
		close(a.stopGossip)
	}

	// Stop scanning
	if a.scanner != nil {
		a.scanner.StopScan()
	}

	// Stop advertising
	if a.advertiser != nil {
		a.advertiser.StopAdvertising()
	}

	// Disconnect all GATT connections
	a.mu.Lock()
	for _, gatt := range a.connectedGatts {
		gatt.Close()
	}
	a.mu.Unlock()

	// Close GATT server
	if a.gattServer != nil {
		a.gattServer.Close()
	}

	// Save identity mappings before shutdown
	if a.identityManager != nil {
		if err := a.identityManager.SaveToDisk(); err != nil {
			logger.Warn(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "Failed to save identity mappings: %v", err)
		}
	}

	// Stop wire
	if a.wire != nil {
		a.wire.Stop()
	}

	logger.Info(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "🛑 Stopped Android")
}

// setupGATTServices sets up the GATT services for peripheral mode
func (a *Android) setupGATTServices() {
	// Create Aura service with protocol, photo, and profile characteristics
	service := &kotlin.BluetoothGattService{
		UUID: phone.AuraServiceUUID,
		Type: kotlin.SERVICE_TYPE_PRIMARY,
		Characteristics: []*kotlin.BluetoothGattCharacteristic{
			{
				UUID:       phone.AuraProtocolCharUUID,
				Properties: kotlin.PROPERTY_READ | kotlin.PROPERTY_WRITE | kotlin.PROPERTY_NOTIFY,
			},
			{
				UUID:       phone.AuraPhotoCharUUID,
				Properties: kotlin.PROPERTY_WRITE | kotlin.PROPERTY_NOTIFY,
			},
			{
				UUID:       phone.AuraProfileCharUUID,
				Properties: kotlin.PROPERTY_READ | kotlin.PROPERTY_WRITE | kotlin.PROPERTY_NOTIFY,
			},
		},
	}

	a.gattServer.AddService(service)
}

// startAdvertising starts advertising our GATT services
func (a *Android) startAdvertising() {
	settings := &kotlin.AdvertiseSettings{
		AdvertiseMode: kotlin.ADVERTISE_MODE_LOW_LATENCY, // 100ms interval
		Connectable:   true,
		Timeout:       0, // No timeout
		TxPowerLevel:  kotlin.ADVERTISE_TX_POWER_MEDIUM,
	}

	advertiseData := &kotlin.AdvertiseData{
		ServiceUUIDs:        []string{phone.AuraServiceUUID},
		IncludeTxPowerLevel: true,
		IncludeDeviceName:   true,
	}

	a.advertiser.StartAdvertising(settings, advertiseData, nil, a)
}

// startScanning starts scanning for other devices
func (a *Android) startScanning() {
	a.scanner.StartScan(a)
}

// handleGATTMessage routes incoming GATT messages to the appropriate handler
// This is the central message routing point - ALL messages come through here
func (a *Android) handleGATTMessage(peerUUID string, msg *wire.GATTMessage) {
	logger.Trace(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "📬 GATT message from %s: type=%s, op=%s, char=%s",
		shortHash(peerUUID), msg.Type, msg.Operation, shortHash(msg.CharacteristicUUID))

	// Route to appropriate handler based on message type
	// For bidirectional communication, we try BOTH handlers
	// Don't use 'handled' flag - both should see the message

	// Try GATT client (handles notifications from peripherals we're connected to)
	a.mu.RLock()
	gatt, exists := a.connectedGatts[peerUUID]
	a.mu.RUnlock()

	if exists && gatt != nil {
		gatt.HandleGATTMessage(msg)
	}

	// Try GATT server (handles requests from centrals connecting to us)
	if a.advertiser != nil {
		a.advertiser.HandleGATTMessage(msg)
	}
}

// SetDiscoveryCallback sets the callback for when devices are discovered
func (a *Android) SetDiscoveryCallback(callback phone.DeviceDiscoveryCallback) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.callback = callback
}

// GetDeviceUUID returns the hardware UUID
func (a *Android) GetDeviceUUID() string {
	return a.hardwareUUID
}

// GetDeviceName returns the device name
func (a *Android) GetDeviceName() string {
	return a.deviceName
}

// GetPlatform returns the platform name
func (a *Android) GetPlatform() string {
	return "android"
}

// handleIncomingCentralConnection handles when a Central connects to us (we're Peripheral)
// This creates a BluetoothGatt object so we can send requests back to them
func (a *Android) handleIncomingCentralConnection(peerUUID string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Check if already tracked
	if _, exists := a.connectedGatts[peerUUID]; exists {
		return
	}

	// Get device name from discovered devices, or create a minimal entry
	deviceName := "Unknown"
	if device, exists := a.discovered[peerUUID]; exists {
		deviceName = device.Name
	} else {
		// Central connected to us but we haven't discovered them yet
		// Add them to discovered map so photo callback will work later
		a.discovered[peerUUID] = phone.DiscoveredDevice{
			HardwareUUID: peerUUID,
			Name:         deviceName,
			RSSI:         0,
		}
		if a.callback != nil {
			a.callback(a.discovered[peerUUID])
		}
	}

	// Note: In real BLE, when a Central connects to us (we're Peripheral), we CANNOT
	// create a reverse GATT client to discover their services. We can only send
	// notifications back on our own characteristics (which they've subscribed to).
	// This is the proper BLE behavior - Peripherals respond via notifications, not writes.

	logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "🔌 Central %s connected (acting as Peripheral)", shortHash(peerUUID))
}
