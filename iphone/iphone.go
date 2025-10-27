package iphone

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/swift"
	"github.com/user/auraphone-blue/wire"
)

// HandshakeMessage is exchanged when two devices first connect
type HandshakeMessage struct {
	HardwareUUID string `json:"hardware_uuid"`
	DeviceID     string `json:"device_id"`
	DeviceName   string `json:"device_name"`
	FirstName    string `json:"first_name"`
}

// IPhone implements the Phone interface for iOS devices
type IPhone struct {
	hardwareUUID string
	deviceID     string
	deviceName   string
	firstName    string

	wire            *wire.Wire
	central         *swift.CBCentralManager
	peripheral      *swift.CBPeripheralManager

	discovered      map[string]phone.DiscoveredDevice // hardwareUUID -> device
	handshaked      map[string]*HandshakeMessage      // hardwareUUID -> handshake data
	connectedPeers  map[string]*swift.CBPeripheral    // hardwareUUID -> peripheral object

	mu              sync.RWMutex
	callback        phone.DeviceDiscoveryCallback
	profilePhoto    string
	profile         map[string]string
}

// NewIPhone creates a new iPhone instance
func NewIPhone(hardwareUUID string, deviceID string, firstName string) *IPhone {
	deviceName := fmt.Sprintf("iPhone (%s)", firstName)

	return &IPhone{
		hardwareUUID:   hardwareUUID,
		deviceID:       deviceID,
		deviceName:     deviceName,
		firstName:      firstName,
		discovered:     make(map[string]phone.DiscoveredDevice),
		handshaked:     make(map[string]*HandshakeMessage),
		connectedPeers: make(map[string]*swift.CBPeripheral),
		profile:        make(map[string]string),
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

	logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "✅ Started iPhone: %s (ID: %s)", ip.firstName, ip.deviceID)
}

// Stop shuts down the iPhone
func (ip *IPhone) Stop() {
	if ip.central != nil {
		ip.central.StopScan()
	}

	if ip.peripheral != nil {
		ip.peripheral.StopAdvertising()
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
				UUID:       phone.AuraProtocolCharUUID,
				Properties: swift.CBCharacteristicPropertyWrite | swift.CBCharacteristicPropertyNotify,
				Permissions: swift.CBAttributePermissionsWriteable | swift.CBAttributePermissionsReadable,
				Value:      nil,
			},
			{
				UUID:       phone.AuraPhotoCharUUID,
				Properties: swift.CBCharacteristicPropertyWrite | swift.CBCharacteristicPropertyNotify,
				Permissions: swift.CBAttributePermissionsWriteable,
				Value:      nil,
			},
			{
				UUID:       phone.AuraProfileCharUUID,
				Properties: swift.CBCharacteristicPropertyRead | swift.CBCharacteristicPropertyNotify,
				Permissions: swift.CBAttributePermissionsReadable,
				Value:      nil,
			},
		},
	}

	ip.peripheral.AddService(service)
}

// startAdvertising begins BLE advertising
func (ip *IPhone) startAdvertising() {
	advData := map[string]interface{}{
		"kCBAdvDataLocalName":    ip.deviceName,
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
	logger.Trace(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "📬 GATT message from %s: type=%s, op=%s, char=%s",
		peerUUID[:8], msg.Type, msg.Operation, msg.CharacteristicUUID[:8])

	// Route to appropriate handler based on message type
	handled := false

	// Try central manager (handles notifications from peripherals we're connected to)
	if ip.central != nil {
		if ip.central.HandleGATTMessage(peerUUID, msg) {
			handled = true
		}
	}

	// Try peripheral manager (handles requests from centrals connecting to us)
	if !handled && ip.peripheral != nil {
		if ip.peripheral.HandleGATTMessage(peerUUID, msg) {
			handled = true
		}
	}

	if !handled {
		logger.Trace(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "⚠️  Unhandled GATT message: %s", msg.Type)
	}
}

// ============================================================================
// CBCentralManagerDelegate Implementation (Central role - we're scanning/connecting)
// ============================================================================

func (ip *IPhone) DidUpdateCentralState(central swift.CBCentralManager) {
	logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Central manager state: %s", central.State)
}

func (ip *IPhone) DidDiscoverPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, advertisementData map[string]interface{}, rssi float64) {
	ip.mu.Lock()
	defer ip.mu.Unlock()

	// Check if already discovered
	if _, exists := ip.discovered[peripheral.UUID]; exists {
		return
	}

	logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "📡 Discovered: %s (RSSI: %.0f)", peripheral.Name, rssi)

	// Store discovered device
	device := phone.DiscoveredDevice{
		HardwareUUID: peripheral.UUID,
		Name:         peripheral.Name,
		RSSI:         rssi,
	}
	ip.discovered[peripheral.UUID] = device

	// Call callback
	if ip.callback != nil {
		ip.callback(device)
	}

	// Decide if we should initiate connection based on role negotiation
	if ip.central.ShouldInitiateConnection(peripheral.UUID) {
		logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔌 Initiating connection to %s (role: Central)", peripheral.UUID[:8])

		// Store peripheral for message routing
		peripheralObj := &peripheral
		ip.connectedPeers[peripheral.UUID] = peripheralObj

		// Connect
		ip.central.Connect(peripheralObj, nil)
	} else {
		logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "⏳ Waiting for %s to connect (role: Peripheral)", peripheral.UUID[:8])
	}
}

func (ip *IPhone) DidConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral) {
	logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "✅ Connected to %s", peripheral.UUID[:8])

	// Send handshake
	ip.sendHandshake(peripheral.UUID)
}

func (ip *IPhone) DidFailToConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, err error) {
	logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "❌ Failed to connect to %s: %v", peripheral.UUID[:8], err)
}

func (ip *IPhone) DidDisconnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, err error) {
	logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔌 Disconnected from %s", peripheral.UUID[:8])

	ip.mu.Lock()
	delete(ip.handshaked, peripheral.UUID)
	delete(ip.connectedPeers, peripheral.UUID)
	ip.mu.Unlock()
}

// ============================================================================
// CBPeripheralManagerDelegate Implementation (Peripheral role - accepting connections)
// ============================================================================

func (ip *IPhone) DidUpdatePeripheralState(peripheralManager *swift.CBPeripheralManager) {
	logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Peripheral manager state: %s", peripheralManager.State)
}

func (ip *IPhone) DidStartAdvertising(peripheralManager *swift.CBPeripheralManager, err error) {
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to start advertising: %v", err)
	} else {
		logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "📡 Advertising started")
	}
}

func (ip *IPhone) DidReceiveReadRequest(peripheralManager *swift.CBPeripheralManager, request *swift.CBATTRequest) {
	logger.Trace(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "📖 Read request from %s for %s",
		request.Central.UUID[:8], request.Characteristic.UUID[:8])

	// For now, respond with empty data
	peripheralManager.RespondToRequest(request, 0) // 0 = success
}

func (ip *IPhone) DidReceiveWriteRequests(peripheralManager *swift.CBPeripheralManager, requests []*swift.CBATTRequest) {
	for _, request := range requests {
		logger.Trace(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "✍️  Write request from %s to %s (%d bytes)",
			request.Central.UUID[:8], request.Characteristic.UUID[:8], len(request.Value))

		// Handle based on characteristic
		if request.Characteristic.UUID == phone.AuraProtocolCharUUID {
			// Handshake message
			ip.handleHandshake(request.Central.UUID, request.Value)
		} else if request.Characteristic.UUID == phone.AuraPhotoCharUUID {
			// Photo data
			ip.handlePhotoData(request.Central.UUID, request.Value)
		}
	}

	peripheralManager.RespondToRequest(requests[0], 0) // 0 = success
}

func (ip *IPhone) CentralDidSubscribe(peripheralManager *swift.CBPeripheralManager, central swift.CBCentral, characteristic *swift.CBMutableCharacteristic) {
	logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔔 Central %s subscribed to %s",
		central.UUID[:8], characteristic.UUID[:8])
}

func (ip *IPhone) CentralDidUnsubscribe(peripheralManager *swift.CBPeripheralManager, central swift.CBCentral, characteristic *swift.CBMutableCharacteristic) {
	logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔕 Central %s unsubscribed from %s",
		central.UUID[:8], characteristic.UUID[:8])
}

// ============================================================================
// Handshake Protocol (Step 7)
// ============================================================================

func (ip *IPhone) sendHandshake(peerUUID string) {
	handshake := HandshakeMessage{
		HardwareUUID: ip.hardwareUUID,
		DeviceID:     ip.deviceID,
		DeviceName:   ip.deviceName,
		FirstName:    ip.firstName,
	}

	data, err := json.Marshal(handshake)
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to marshal handshake: %v", err)
		return
	}

	// Write to peer's AuraProtocolCharUUID
	err = ip.wire.WriteCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to send handshake to %s: %v", peerUUID[:8], err)
	} else {
		logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🤝 Sent handshake to %s", peerUUID[:8])
	}
}

func (ip *IPhone) handleHandshake(peerUUID string, data []byte) {
	var handshake HandshakeMessage
	err := json.Unmarshal(data, &handshake)
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to unmarshal handshake: %v", err)
		return
	}

	logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🤝 Received handshake from %s: %s (ID: %s)",
		peerUUID[:8], handshake.FirstName, handshake.DeviceID)

	ip.mu.Lock()
	alreadyHandshaked := ip.handshaked[peerUUID] != nil

	// Mark handshake complete
	ip.handshaked[peerUUID] = &handshake

	// Update discovered device with real name
	if device, exists := ip.discovered[peerUUID]; exists {
		device.Name = handshake.DeviceName
		ip.discovered[peerUUID] = device

		// Notify GUI
		if ip.callback != nil {
			ip.callback(device)
		}
	}
	ip.mu.Unlock()

	// Send our handshake back if we haven't already
	// This ensures bidirectional handshake completion
	if !alreadyHandshaked {
		logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🤝 Sending handshake back to %s", peerUUID[:8])
		ip.sendHandshake(peerUUID)
	}
}

func (ip *IPhone) handlePhotoData(peerUUID string, data []byte) {
	logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "📸 Received photo data from %s (%d bytes)",
		peerUUID[:8], len(data))
	// TODO: Step 8 - implement photo transfer
}

// ============================================================================
// Phone Interface Implementation
// ============================================================================

func (ip *IPhone) SetDiscoveryCallback(callback phone.DeviceDiscoveryCallback) {
	ip.mu.Lock()
	ip.callback = callback
	ip.mu.Unlock()
}

func (ip *IPhone) GetDeviceUUID() string {
	return ip.hardwareUUID
}

func (ip *IPhone) GetDeviceName() string {
	return ip.deviceName
}

func (ip *IPhone) GetPlatform() string {
	return "iOS"
}

func (ip *IPhone) SetProfilePhoto(photoPath string) error {
	ip.mu.Lock()
	defer ip.mu.Unlock()
	ip.profilePhoto = photoPath
	// TODO: Step 8 - broadcast photo hash
	return nil
}

func (ip *IPhone) GetLocalProfileMap() map[string]string {
	ip.mu.RLock()
	defer ip.mu.RUnlock()

	result := make(map[string]string)
	for k, v := range ip.profile {
		result[k] = v
	}
	return result
}

func (ip *IPhone) UpdateLocalProfile(profile map[string]string) error {
	ip.mu.Lock()
	defer ip.mu.Unlock()

	for k, v := range profile {
		ip.profile[k] = v
	}
	// TODO: Step 8 - broadcast profile changes
	return nil
}
