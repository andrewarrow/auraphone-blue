package iphone

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	pb "github.com/user/auraphone-blue/proto"
	"github.com/user/auraphone-blue/swift"
	"github.com/user/auraphone-blue/wire"
	"google.golang.org/protobuf/proto"
)

// shortHash returns first 8 chars of a hash string, or the full string if shorter
func shortHash(s string) string {
	if len(s) >= 8 {
		return s[:8]
	}
	if s == "" {
		return "(none)"
	}
	return s
}

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
	identityManager *phone.IdentityManager // THE ONLY place for UUID ↔ DeviceID mapping
	photoCache      *phone.PhotoCache      // Photo caching and storage
	photoChunker    *phone.PhotoChunker    // Photo chunking for BLE transfer

	discovered      map[string]phone.DiscoveredDevice // hardwareUUID -> device
	handshaked      map[string]*HandshakeMessage      // hardwareUUID -> handshake data
	connectedPeers  map[string]*swift.CBPeripheral    // hardwareUUID -> peripheral object
	photoTransfers  map[string]*phone.PhotoTransferState // hardwareUUID -> in-progress transfer

	mu              sync.RWMutex
	callback        phone.DeviceDiscoveryCallback
	profilePhoto    string
	photoHash       string // SHA-256 hash of our current profile photo
	photoData       []byte // Our current profile photo data
	profile         map[string]string
}

// NewIPhone creates a new iPhone instance
// Hardware UUID is provided, DeviceID is loaded from cache or generated
func NewIPhone(hardwareUUID string) *IPhone {
	// Load or generate device ID (persists to ~/.auraphone-blue-data/{uuid}/cache/device_id.json)
	deviceID, err := phone.LoadOrGenerateDeviceID(hardwareUUID)
	if err != nil {
		panic(fmt.Sprintf("Failed to load/generate device ID: %v", err))
	}

	// Generate first name from device ID (first 4 chars for now, will be customizable later)
	firstName := deviceID[:4]
	deviceName := fmt.Sprintf("iPhone (%s)", firstName)

	// Create identity manager (tracks all hardware UUID ↔ device ID mappings)
	dataDir := filepath.Join(phone.GetDataDir(), hardwareUUID)
	identityManager := phone.NewIdentityManager(hardwareUUID, deviceID, dataDir)

	// Load previously known device mappings from disk
	if err := identityManager.LoadFromDisk(); err != nil {
		logger.Warn(fmt.Sprintf("%s iOS", hardwareUUID[:8]), "Failed to load identity mappings: %v", err)
	}

	return &IPhone{
		hardwareUUID:    hardwareUUID,
		deviceID:        deviceID,
		deviceName:      deviceName,
		firstName:       firstName,
		identityManager: identityManager,
		photoCache:      phone.NewPhotoCache(hardwareUUID),
		photoChunker:    phone.NewPhotoChunker(),
		discovered:      make(map[string]phone.DiscoveredDevice),
		handshaked:      make(map[string]*HandshakeMessage),
		connectedPeers:  make(map[string]*swift.CBPeripheral),
		photoTransfers:  make(map[string]*phone.PhotoTransferState),
		profile:         make(map[string]string),
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
		shortHash(peerUUID), msg.Type, msg.Operation, shortHash(msg.CharacteristicUUID))

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

// handleIncomingCentralConnection handles when a Central connects to us (we're Peripheral)
// This creates a CBPeripheral object so we can send requests back to them
func (ip *IPhone) handleIncomingCentralConnection(peerUUID string) {
	ip.mu.Lock()
	defer ip.mu.Unlock()

	// Check if already tracked
	if _, exists := ip.connectedPeers[peerUUID]; exists {
		return
	}

	// Get device name from discovered devices
	deviceName := "Unknown"
	if device, exists := ip.discovered[peerUUID]; exists {
		deviceName = device.Name
	}

	// Create CBPeripheral object for the Central that connected to us
	// This allows us to make requests back to them
	peripheral := swift.NewCBPeripheralFromConnection(peerUUID, deviceName, ip.wire)

	ip.connectedPeers[peerUUID] = peripheral

	// Register with central manager so notifications are routed correctly
	// This is critical for bidirectional communication: even though we're Peripheral
	// in the BLE connection, we can still receive notifications from the Central
	ip.central.RegisterReversePeripheral(peripheral)

	logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔌 Central %s connected (created reverse peripheral object)", shortHash(peerUUID))
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
		logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔌 Initiating connection to %s (role: Central)", shortHash(peripheral.UUID))

		// Store peripheral for message routing
		peripheralObj := &peripheral
		ip.connectedPeers[peripheral.UUID] = peripheralObj

		// Connect
		ip.central.Connect(peripheralObj, nil)
	} else {
		logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "⏳ Waiting for %s to connect (role: Peripheral)", shortHash(peripheral.UUID))
	}
}

func (ip *IPhone) DidConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral) {
	logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "✅ Connected to %s", shortHash(peripheral.UUID))

	// Mark as connected in IdentityManager (tracks connection state by hardware UUID)
	ip.identityManager.MarkConnected(peripheral.UUID)

	// Send handshake
	ip.sendHandshake(peripheral.UUID)
}

func (ip *IPhone) DidFailToConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, err error) {
	logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "❌ Failed to connect to %s: %v", shortHash(peripheral.UUID), err)
}

func (ip *IPhone) DidDisconnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, err error) {
	logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔌 Disconnected from %s", shortHash(peripheral.UUID))

	// Mark as disconnected in IdentityManager
	ip.identityManager.MarkDisconnected(peripheral.UUID)

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
		shortHash(request.Central.UUID), shortHash(request.Characteristic.UUID))

	// For now, respond with empty data
	peripheralManager.RespondToRequest(request, 0) // 0 = success
}

func (ip *IPhone) DidReceiveWriteRequests(peripheralManager *swift.CBPeripheralManager, requests []*swift.CBATTRequest) {
	for _, request := range requests {
		logger.Trace(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "✍️  Write request from %s to %s (%d bytes)",
			shortHash(request.Central.UUID), shortHash(request.Characteristic.UUID), len(request.Value))

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
		shortHash(central.UUID), shortHash(characteristic.UUID))

	// If they subscribed to photo characteristic, send them our photo
	if characteristic.UUID == phone.AuraPhotoCharUUID {
		logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "📸 Central %s subscribed to photo - sending chunks",
			shortHash(central.UUID))
		go ip.sendPhotoChunks(central.UUID)
	}
}

func (ip *IPhone) CentralDidUnsubscribe(peripheralManager *swift.CBPeripheralManager, central swift.CBCentral, characteristic *swift.CBMutableCharacteristic) {
	logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔕 Central %s unsubscribed from %s",
		shortHash(central.UUID), shortHash(characteristic.UUID))
}

// ============================================================================
// Handshake Protocol (Step 7)
// ============================================================================

func (ip *IPhone) sendHandshake(peerUUID string) {
	ip.mu.RLock()
	photoHashBytes := []byte{}
	if ip.photoHash != "" {
		// Convert hex string to bytes
		for i := 0; i < len(ip.photoHash); i += 2 {
			var b byte
			fmt.Sscanf(ip.photoHash[i:i+2], "%02x", &b)
			photoHashBytes = append(photoHashBytes, b)
		}
	}
	ip.mu.RUnlock()

	// Use protobuf HandshakeMessage
	pbHandshake := &pb.HandshakeMessage{
		DeviceId:        ip.deviceID,
		FirstName:       ip.firstName,
		ProtocolVersion: 1,
		TxPhotoHash:     photoHashBytes, // Photo hash we're offering to send
	}

	data, err := proto.Marshal(pbHandshake)
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to marshal handshake: %v", err)
		return
	}

	// Write to peer's AuraProtocolCharUUID
	err = ip.wire.WriteCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Failed to send handshake to %s: %v", shortHash(peerUUID), err)
	} else {
		logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "🤝 Sent handshake to %s (photo: %s)", shortHash(peerUUID), shortHash(ip.photoHash))
	}
}

func (ip *IPhone) handleHandshake(peerUUID string, data []byte) {
	// Try to parse as protobuf first
	var pbHandshake pb.HandshakeMessage
	err := proto.Unmarshal(data, &pbHandshake)
	if err != nil {
		// Fall back to JSON for backward compatibility
		var jsonHandshake HandshakeMessage
		err = json.Unmarshal(data, &jsonHandshake)
		if err != nil {
			logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to unmarshal handshake: %v", err)
			return
		}
		// Convert JSON to protobuf format
		pbHandshake.DeviceId = jsonHandshake.DeviceID
		pbHandshake.FirstName = jsonHandshake.FirstName
	}

	// Convert photo hash bytes to hex string
	photoHashHex := ""
	if len(pbHandshake.TxPhotoHash) > 0 {
		photoHashHex = fmt.Sprintf("%x", pbHandshake.TxPhotoHash)
	}

	logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "🤝 Received handshake from %s: %s (ID: %s, photo: %s)",
		shortHash(peerUUID), pbHandshake.FirstName, pbHandshake.DeviceId, shortHash(photoHashHex))

	// CRITICAL: Register the hardware UUID ↔ device ID mapping in IdentityManager
	// This is THE ONLY place where we learn about other devices' DeviceIDs
	ip.identityManager.RegisterDevice(peerUUID, pbHandshake.DeviceId)

	// Persist mappings to disk
	if err := ip.identityManager.SaveToDisk(); err != nil {
		logger.Warn(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to save identity mappings: %v", err)
	}

	ip.mu.Lock()
	alreadyHandshaked := ip.handshaked[peerUUID] != nil

	// Mark handshake complete (store as JSON struct for compatibility)
	ip.handshaked[peerUUID] = &HandshakeMessage{
		HardwareUUID: peerUUID,
		DeviceID:     pbHandshake.DeviceId,
		DeviceName:   fmt.Sprintf("iPhone (%s)", pbHandshake.FirstName),
		FirstName:    pbHandshake.FirstName,
	}

	// Update discovered device with DeviceID, name, and photo hash
	if device, exists := ip.discovered[peerUUID]; exists {
		device.DeviceID = pbHandshake.DeviceId
		device.Name = fmt.Sprintf("iPhone (%s)", pbHandshake.FirstName)
		device.PhotoHash = photoHashHex
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
		logger.Debug(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "🤝 Sending handshake back to %s", shortHash(peerUUID))
		ip.sendHandshake(peerUUID)
	}

	// Check if we need to start a photo transfer
	// Conditions:
	// 1. They have a photo (photoHashHex != "")
	// 2. We don't have it cached yet
	// 3. We're not already transferring from this peer (prevents duplicate subscriptions)
	if photoHashHex != "" && !ip.photoCache.HasPhoto(photoHashHex) {
		ip.mu.Lock()
		existingTransfer, transferInProgress := ip.photoTransfers[peerUUID]
		ip.mu.Unlock()

		if transferInProgress {
			// Check if it's for the same photo
			if existingTransfer.PhotoHash == photoHashHex {
				logger.Debug(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)),
					"📸 Photo transfer already in progress from %s (hash: %s)",
					shortHash(peerUUID), shortHash(photoHashHex))
				return // Don't start duplicate transfer
			} else {
				// Different photo - old transfer might be stale, allow new one
				logger.Warn(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)),
					"📸 Replacing stale photo transfer from %s (old: %s, new: %s)",
					shortHash(peerUUID), shortHash(existingTransfer.PhotoHash), shortHash(photoHashHex))
			}
		}

		logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📸 Starting photo transfer from %s (hash: %s)",
			shortHash(peerUUID), shortHash(photoHashHex))
		go ip.requestAndReceivePhoto(peerUUID, photoHashHex, pbHandshake.DeviceId)
	}
}

// requestAndReceivePhoto subscribes to photo characteristic to receive photo chunks
func (ip *IPhone) requestAndReceivePhoto(peerUUID string, photoHash string, deviceID string) {
	// Reserve transfer slot immediately to prevent duplicate subscriptions
	// We don't know total chunks yet, but we mark the transfer as in-progress
	ip.mu.Lock()
	ip.photoTransfers[peerUUID] = phone.NewPhotoTransferState(photoHash, 0, peerUUID, deviceID)
	ip.mu.Unlock()

	// Clean up transfer state if we exit early due to errors
	defer func() {
		if r := recover(); r != nil {
			ip.mu.Lock()
			delete(ip.photoTransfers, peerUUID)
			ip.mu.Unlock()
			panic(r)
		}
	}()

	// Find peripheral for this peer
	ip.mu.RLock()
	peripheral, exists := ip.connectedPeers[peerUUID]
	ip.mu.RUnlock()

	if !exists {
		logger.Warn(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Cannot request photo: not connected to %s", shortHash(peerUUID))
		ip.mu.Lock()
		delete(ip.photoTransfers, peerUUID)
		ip.mu.Unlock()
		return
	}

	// Discover services if not already done
	if len(peripheral.Services) == 0 {
		logger.Debug(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Discovering services on %s for photo transfer", shortHash(peerUUID))

		// Set up a delegate to handle service discovery
		peripheral.Delegate = &photoTransferDelegate{
			iphone:    ip,
			peerUUID:  peerUUID,
			photoHash: photoHash,
			deviceID:  deviceID,
		}

		peripheral.DiscoverServices([]string{phone.AuraServiceUUID})
		// Note: Transfer state will be updated with actual chunk count when first chunk arrives
		return
	}

	// Find photo characteristic
	photoChar := peripheral.GetCharacteristic(phone.AuraServiceUUID, phone.AuraPhotoCharUUID)
	if photoChar == nil {
		logger.Warn(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Cannot request photo: characteristic not found on %s", shortHash(peerUUID))
		ip.mu.Lock()
		delete(ip.photoTransfers, peerUUID)
		ip.mu.Unlock()
		return
	}

	// Subscribe to photo notifications (this triggers the sender to start sending chunks)
	err := peripheral.SetNotifyValue(true, photoChar)
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Failed to subscribe to photo characteristic: %v", err)
		ip.mu.Lock()
		delete(ip.photoTransfers, peerUUID)
		ip.mu.Unlock()
		return
	}

	logger.Debug(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📸 Subscribed to photo characteristic from %s", shortHash(peerUUID))
}

// photoTransferDelegate handles service discovery for photo transfers
type photoTransferDelegate struct {
	iphone    *IPhone
	peerUUID  string
	photoHash string
	deviceID  string
}

func (d *photoTransferDelegate) DidDiscoverServices(peripheral *swift.CBPeripheral, services []*swift.CBService, err error) {
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", d.iphone.hardwareUUID[:8]), "Failed to discover services for photo transfer: %v", err)
		return
	}

	logger.Debug(fmt.Sprintf("%s iOS", shortHash(d.iphone.hardwareUUID)), "✅ Services discovered for photo transfer from %s", shortHash(d.peerUUID))

	// Now subscribe to photo characteristic
	d.iphone.requestAndReceivePhoto(d.peerUUID, d.photoHash, d.deviceID)
}

func (d *photoTransferDelegate) DidDiscoverCharacteristics(peripheral *swift.CBPeripheral, service *swift.CBService, err error) {
	// Not needed for photo transfer
}

func (d *photoTransferDelegate) DidWriteValueForCharacteristic(peripheral *swift.CBPeripheral, characteristic *swift.CBCharacteristic, err error) {
	// Not needed for photo transfer
}

func (d *photoTransferDelegate) DidUpdateValueForCharacteristic(peripheral *swift.CBPeripheral, characteristic *swift.CBCharacteristic, err error) {
	// Handle incoming photo chunk notification
	if characteristic.UUID == phone.AuraPhotoCharUUID {
		d.iphone.handlePhotoData(d.peerUUID, characteristic.Value)
	}
}

// sendPhotoChunks sends photo chunks to a peer who subscribed
func (ip *IPhone) sendPhotoChunks(peerUUID string) {
	ip.mu.RLock()
	photoData := ip.photoData
	photoHash := ip.photoHash
	deviceID := ip.deviceID
	ip.mu.RUnlock()

	if len(photoData) == 0 {
		logger.Warn(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "No photo to send to %s", shortHash(peerUUID))
		return
	}

	// Chunk the photo
	chunks := ip.photoChunker.ChunkPhoto(photoData)
	totalChunks := len(chunks)

	logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📤 Sending %d photo chunks to %s (hash: %s)",
		totalChunks, shortHash(peerUUID), shortHash(photoHash))

	// Convert photo hash hex to bytes
	photoHashBytes := []byte{}
	for i := 0; i < len(photoHash); i += 2 {
		var b byte
		fmt.Sscanf(photoHash[i:i+2], "%02x", &b)
		photoHashBytes = append(photoHashBytes, b)
	}

	// Send each chunk
	for i, chunk := range chunks {
		chunkMsg := &pb.PhotoChunkMessage{
			SenderDeviceId: deviceID,
			TargetDeviceId: "", // Will be filled by receiver
			PhotoHash:      photoHashBytes,
			ChunkIndex:     int32(i),
			TotalChunks:    int32(totalChunks),
			ChunkData:      chunk,
		}

		data, err := proto.Marshal(chunkMsg)
		if err != nil {
			logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to marshal chunk %d: %v", i, err)
			continue
		}

		// Send via notification
		err = ip.wire.NotifyCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraPhotoCharUUID, data)
		if err != nil {
			logger.Error(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Failed to send chunk %d to %s: %v", i, shortHash(peerUUID), err)
		} else {
			logger.Trace(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📤 Sent chunk %d/%d to %s (%d bytes)",
				i+1, totalChunks, shortHash(peerUUID), len(chunk))
		}
	}

	logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "✅ Finished sending photo to %s", shortHash(peerUUID))
}

func (ip *IPhone) handlePhotoData(peerUUID string, data []byte) {
	// Parse as PhotoChunkMessage
	var chunkMsg pb.PhotoChunkMessage
	err := proto.Unmarshal(data, &chunkMsg)
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to unmarshal photo chunk: %v", err)
		return
	}

	photoHashHex := fmt.Sprintf("%x", chunkMsg.PhotoHash)

	logger.Trace(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📥 Received chunk %d/%d from %s (hash: %s, %d bytes)",
		chunkMsg.ChunkIndex+1, chunkMsg.TotalChunks, shortHash(peerUUID), shortHash(photoHashHex), len(chunkMsg.ChunkData))

	// Get or create transfer state
	ip.mu.Lock()
	transfer, exists := ip.photoTransfers[peerUUID]
	if !exists {
		transfer = phone.NewPhotoTransferState(photoHashHex, int(chunkMsg.TotalChunks), peerUUID, chunkMsg.SenderDeviceId)
		ip.photoTransfers[peerUUID] = transfer
	} else if transfer.TotalChunks == 0 {
		// Update total chunks if this was pre-created by requestAndReceivePhoto
		transfer.TotalChunks = int(chunkMsg.TotalChunks)
	}
	ip.mu.Unlock()

	// Add chunk to transfer state
	transfer.AddChunk(chunkMsg.ChunkData)

	// Check if transfer is complete
	if transfer.IsComplete() {
		logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "✅ Photo transfer complete from %s (%d bytes)",
			shortHash(peerUUID), len(transfer.GetData()))

		// Save photo to cache
		photoData := transfer.GetData()
		savedHash, err := ip.photoCache.SavePhoto(photoData, peerUUID, chunkMsg.SenderDeviceId)
		if err != nil {
			logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to save photo: %v", err)
		} else {
			logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "💾 Saved photo to cache (hash: %s)", shortHash(savedHash))

			// Update discovered device with photo data
			ip.mu.Lock()
			if device, exists := ip.discovered[peerUUID]; exists {
				device.PhotoData = photoData
				device.PhotoHash = photoHashHex
				ip.discovered[peerUUID] = device

				// Notify GUI
				if ip.callback != nil {
					ip.callback(device)
				}
			}

			// Clean up transfer state
			delete(ip.photoTransfers, peerUUID)
			ip.mu.Unlock()
		}
	}
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

func (ip *IPhone) GetDeviceID() string {
	return ip.deviceID
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

	// Load photo and calculate hash
	photoData, photoHash, err := ip.photoCache.LoadPhoto(photoPath, "")
	if err != nil {
		return fmt.Errorf("failed to load photo: %w", err)
	}

	// Save to cache
	_, err = ip.photoCache.SavePhoto(photoData, ip.hardwareUUID, ip.deviceID)
	if err != nil {
		return fmt.Errorf("failed to cache photo: %w", err)
	}

	ip.profilePhoto = photoPath
	ip.photoHash = photoHash
	ip.photoData = photoData

	logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "📸 Set profile photo: %s (hash: %s)", photoPath, shortHash(photoHash))

	// TODO: Broadcast updated photo hash to connected devices
	// For now, new connections will get it in handshake

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
