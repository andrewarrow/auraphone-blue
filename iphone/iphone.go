package iphone

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/phototransfer"
	"github.com/user/auraphone-blue/proto"
	"github.com/user/auraphone-blue/swift"
	"github.com/user/auraphone-blue/wire"
	"google.golang.org/protobuf/encoding/protojson"
	proto2 "google.golang.org/protobuf/proto"
)

// photoReceiveState tracks photo reception progress
type photoReceiveState struct {
	isReceiving     bool
	expectedSize    uint32
	expectedCRC     uint32
	expectedChunks  uint16
	receivedChunks  map[uint16][]byte
	senderDeviceID  string
	buffer          []byte
}

// iPhone represents an iOS device with BLE capabilities
type iPhone struct {
	deviceUUID           string
	deviceName           string
	wire                 *wire.Wire
	cacheManager         *wire.DeviceCacheManager       // Persistent photo storage
	manager              *swift.CBCentralManager
	discoveryCallback    phone.DeviceDiscoveryCallback
	photoPath            string
	photoHash            string
	photoData            []byte
	connectedPeripherals map[string]*swift.CBPeripheral // UUID -> peripheral
	deviceIDToPhotoHash  map[string]string              // deviceID -> their TX photo hash
	receivedPhotoHashes  map[string]string              // deviceID -> RX hash (photos we got from them)
	lastHandshakeTime    map[string]time.Time           // deviceID -> last handshake timestamp
	photoSendInProgress  map[string]bool                // deviceID -> true if photo send in progress
	photoReceiveState    *photoReceiveState
	staleCheckDone       chan struct{} // Signal channel for stopping background checker
}

// NewIPhone creates a new iPhone instance
func NewIPhone() *iPhone {
	deviceUUID := uuid.New().String()
	deviceName := "iOS Device"

	ip := &iPhone{
		deviceUUID:           deviceUUID,
		deviceName:           deviceName,
		connectedPeripherals: make(map[string]*swift.CBPeripheral),
		deviceIDToPhotoHash:  make(map[string]string),
		receivedPhotoHashes:  make(map[string]string),
		lastHandshakeTime:    make(map[string]time.Time),
		photoSendInProgress:  make(map[string]bool),
		staleCheckDone:       make(chan struct{}),
		photoReceiveState: &photoReceiveState{
			receivedChunks: make(map[uint16][]byte),
		},
	}

	// Initialize wire
	ip.wire = wire.NewWire(deviceUUID)
	if err := ip.wire.InitializeDevice(); err != nil {
		fmt.Printf("Failed to initialize iOS device: %v\n", err)
		return nil
	}

	// Initialize cache manager
	ip.cacheManager = wire.NewDeviceCacheManager(deviceUUID)
	if err := ip.cacheManager.InitializeCache(); err != nil {
		fmt.Printf("Failed to initialize cache: %v\n", err)
		return nil
	}

	// Load existing photo mappings from disk
	ip.loadReceivedPhotoMappings()

	// Setup BLE
	ip.setupBLE()

	// Create manager
	ip.manager = swift.NewCBCentralManager(ip, deviceUUID)

	return ip
}

// setupBLE configures GATT table and advertising data
func (ip *iPhone) setupBLE() {
	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"
	const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"

	// Create GATT table
	gattTable := &wire.GATTTable{
		Services: []wire.GATTService{
			{
				UUID: auraServiceUUID,
				Type: "primary",
				Characteristics: []wire.GATTCharacteristic{
					{
						UUID:       auraTextCharUUID,
						Properties: []string{"read", "write", "notify"},
					},
					{
						UUID:       auraPhotoCharUUID,
						Properties: []string{"read", "write", "notify"},
					},
				},
			},
		},
	}

	if err := ip.wire.WriteGATTTable(gattTable); err != nil {
		fmt.Printf("Failed to write GATT table: %v\n", err)
		return
	}

	// Create advertising data
	txPowerLevel := 0
	advertisingData := &wire.AdvertisingData{
		DeviceName:    ip.deviceName,
		ServiceUUIDs:  []string{auraServiceUUID},
		TxPowerLevel:  &txPowerLevel,
		IsConnectable: true,
	}

	if err := ip.wire.WriteAdvertisingData(advertisingData); err != nil {
		fmt.Printf("Failed to write advertising data: %v\n", err)
		return
	}

	logger.Info(fmt.Sprintf("%s iOS", ip.deviceUUID[:8]), "Setup complete - advertising as: %s", ip.deviceName)
}

// loadReceivedPhotoMappings loads deviceID->photoHash mappings from disk cache
// This restores knowledge of which photos we've already received from other devices
func (ip *iPhone) loadReceivedPhotoMappings() {
	prefix := fmt.Sprintf("%s iOS", ip.deviceUUID[:8])

	// Scan cache/photos directory for received photos
	photosDir := fmt.Sprintf("data/%s/cache/photos", ip.deviceUUID)
	entries, err := os.ReadDir(photosDir)
	if err != nil {
		// Directory doesn't exist yet - no photos received
		return
	}

	// For each photo file, check if we have metadata mapping it to a device
	loadedCount := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jpg" {
			continue
		}

		// Extract hash from filename (e.g., "abc123...def.jpg" -> "abc123...def")
		photoHash := entry.Name()[:len(entry.Name())-4]

		// Look for device metadata files to find which device sent this photo
		metadataFiles, err := filepath.Glob(fmt.Sprintf("data/%s/cache/*_metadata.json", ip.deviceUUID))
		if err != nil {
			continue
		}

		for _, metadataFile := range metadataFiles {
			deviceID := filepath.Base(metadataFile)
			deviceID = deviceID[:len(deviceID)-len("_metadata.json")]

			// Check if this device's metadata references our photo hash
			hash, err := ip.cacheManager.GetDevicePhotoHash(deviceID)
			if err == nil && hash == photoHash {
				ip.receivedPhotoHashes[deviceID] = photoHash
				loadedCount++
				logger.Debug(prefix, "Loaded cached photo mapping: %s -> %s", deviceID[:8], photoHash[:8])
				break
			}
		}
	}

	if loadedCount > 0 {
		logger.Info(prefix, "Loaded %d cached photo mappings from disk", loadedCount)
	}
}

// Start begins BLE advertising and scanning
func (ip *iPhone) Start() {
	// Start scanning for peripherals
	go func() {
		time.Sleep(500 * time.Millisecond)
		ip.manager.ScanForPeripherals(nil, nil)
		logger.Info(fmt.Sprintf("%s iOS", ip.deviceUUID[:8]), "Started scanning for peripherals")
	}()

	// Start periodic stale handshake checker
	ip.startStaleHandshakeChecker()
	logger.Debug(fmt.Sprintf("%s iOS", ip.deviceUUID[:8]), "Started stale handshake checker (60s threshold, 30s interval)")
}

// Stop stops BLE operations and cleans up resources
func (ip *iPhone) Stop() {
	fmt.Printf("[%s iOS] Stopping BLE operations\n", ip.deviceUUID[:8])
	// Stop stale handshake checker
	close(ip.staleCheckDone)
	// Future: stop scanning, disconnect, cleanup
}

// SetDiscoveryCallback sets the callback for when devices are discovered
func (ip *iPhone) SetDiscoveryCallback(callback phone.DeviceDiscoveryCallback) {
	ip.discoveryCallback = callback
}

// GetDeviceUUID returns the device's UUID
func (ip *iPhone) GetDeviceUUID() string {
	return ip.deviceUUID
}

// GetDeviceName returns the device's name
func (ip *iPhone) GetDeviceName() string {
	return ip.deviceName
}

// GetPlatform returns the platform type
func (ip *iPhone) GetPlatform() string {
	return "iOS"
}

// CBCentralManagerDelegate methods

func (ip *iPhone) DidUpdateState(central swift.CBCentralManager) {
	// State updated
}

func (ip *iPhone) DidDiscoverPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, advertisementData map[string]interface{}, rssi float64) {
	name := peripheral.Name
	if advName, ok := advertisementData["kCBAdvDataLocalName"].(string); ok {
		name = advName
	}

	txPhotoHash := ""
	if hash, ok := advertisementData["kCBAdvDataTxPhotoHash"].(string); ok {
		txPhotoHash = hash
	}

	prefix := fmt.Sprintf("%s iOS", ip.deviceUUID[:8])
	logger.Debug(prefix, "📱 DISCOVERED device %s (%s)", peripheral.UUID[:8], name)
	logger.Debug(prefix, "   └─ RSSI: %.0f dBm", rssi)
	if txPhotoHash != "" {
		logger.Debug(prefix, "   └─ TX Photo Hash: %s", txPhotoHash[:8])
	} else {
		logger.Debug(prefix, "   └─ TX Photo Hash: (none)")
	}

	if ip.discoveryCallback != nil {
		ip.discoveryCallback(phone.DiscoveredDevice{
			DeviceID:  peripheral.UUID,
			Name:      name,
			RSSI:      rssi,
			Platform:  "unknown",
			PhotoHash: txPhotoHash, // Remote device's TX hash (photo they have available)
		})
	}

	// Auto-connect if not already connected
	if _, exists := ip.connectedPeripherals[peripheral.UUID]; !exists {
		logger.Debug(prefix, "🔌 Connecting to %s", peripheral.UUID[:8])
		ip.manager.Connect(&peripheral, nil)
	}
}

func (ip *iPhone) DidConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral) {
	prefix := fmt.Sprintf("%s iOS", ip.deviceUUID[:8])
	logger.Info(prefix, "✅ Connected to %s", peripheral.UUID[:8])

	// Store peripheral
	ip.connectedPeripherals[peripheral.UUID] = &peripheral

	// Set self as delegate
	peripheral.Delegate = ip

	// Discover services
	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	peripheral.DiscoverServices([]string{auraServiceUUID})
}

func (ip *iPhone) DidFailToConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, err error) {
	prefix := fmt.Sprintf("%s iOS", ip.deviceUUID[:8])
	logger.Error(prefix, "❌ Failed to connect to %s: %v", peripheral.UUID[:8], err)
}

func (ip *iPhone) DidDisconnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, err error) {
	prefix := fmt.Sprintf("%s iOS", ip.deviceUUID[:8])
	if err != nil {
		logger.Error(prefix, "❌ Disconnected from %s with error: %v", peripheral.UUID[:8], err)
	} else {
		logger.Info(prefix, "📡 Disconnected from %s (interference/distance)", peripheral.UUID[:8])
	}

	// Stop listening and write queue on the peripheral
	if storedPeripheral, exists := ip.connectedPeripherals[peripheral.UUID]; exists {
		storedPeripheral.StopListening()
		storedPeripheral.StopWriteQueue()
		delete(ip.connectedPeripherals, peripheral.UUID)
	}

	// iOS auto-reconnect: CBCentralManager will automatically retry connection
	// The app just waits for DidConnectPeripheral callback again
	logger.Info(prefix, "🔄 iOS will auto-reconnect to %s in background...", peripheral.UUID[:8])
}

// SetProfilePhoto sets the profile photo and broadcasts the hash
func (ip *iPhone) SetProfilePhoto(photoPath string) error {
	// Read photo file
	data, err := os.ReadFile(photoPath)
	if err != nil {
		return fmt.Errorf("failed to read photo: %w", err)
	}

	// Calculate hash
	hash := sha256.Sum256(data)
	photoHash := hex.EncodeToString(hash[:])

	// Cache photo to disk
	cachePath := fmt.Sprintf("data/%s/cache/my_photo.jpg", ip.deviceUUID)
	cacheDir := fmt.Sprintf("data/%s/cache", ip.deviceUUID)
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}
	if err := os.WriteFile(cachePath, data, 0644); err != nil {
		return fmt.Errorf("failed to cache photo: %w", err)
	}

	// Update fields
	ip.photoPath = photoPath
	ip.photoHash = photoHash
	ip.photoData = data

	// Update advertising data to broadcast new hash
	ip.setupBLE()

	prefix := fmt.Sprintf("%s iOS", ip.deviceUUID[:8])
	logger.Info(prefix, "📸 Updated profile photo (hash: %s)", photoHash[:8])
	logger.Debug(prefix, "   └─ Cached to disk and broadcasting TX hash in advertising data")

	// Re-send handshake to all connected devices to notify them of the new photo
	if len(ip.connectedPeripherals) > 0 {
		logger.Debug(prefix, "   └─ Notifying %d connected device(s) of photo change", len(ip.connectedPeripherals))
		for _, peripheral := range ip.connectedPeripherals {
			go ip.sendHandshakeMessage(peripheral)
		}
	}

	return nil
}

// GetProfilePhotoHash returns the hash of the current profile photo
func (ip *iPhone) GetProfilePhotoHash() string {
	return ip.photoHash
}

// CBPeripheralDelegate methods

func (ip *iPhone) DidDiscoverServices(peripheral *swift.CBPeripheral, services []*swift.CBService, err error) {
	prefix := fmt.Sprintf("%s iOS", ip.deviceUUID[:8])
	if err != nil {
		logger.Error(prefix, "❌ Service discovery failed: %v", err)
		return
	}

	logger.Debug(prefix, "🔍 Discovered %d services", len(services))

	// Discover characteristics for all services
	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"
	const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"

	for _, service := range services {
		peripheral.DiscoverCharacteristics([]string{auraTextCharUUID, auraPhotoCharUUID}, service)
	}
}

func (ip *iPhone) DidDiscoverCharacteristics(peripheral *swift.CBPeripheral, service *swift.CBService, err error) {
	prefix := fmt.Sprintf("%s iOS", ip.deviceUUID[:8])
	if err != nil {
		logger.Error(prefix, "❌ Characteristic discovery failed: %v", err)
		return
	}

	logger.Debug(prefix, "🔍 Discovered %d characteristics", len(service.Characteristics))

	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"
	const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"

	// Enable notifications for characteristics (matches real iOS behavior)
	for _, char := range service.Characteristics {
		if char.UUID == auraTextCharUUID || char.UUID == auraPhotoCharUUID {
			if err := peripheral.SetNotifyValue(true, char); err != nil {
				logger.Error(prefix, "❌ Failed to enable notifications for %s: %v", char.UUID[:8], err)
			}
		}
	}

	// Start write queue for async writes (matches real iOS behavior)
	peripheral.StartWriteQueue()

	// Start listening for notifications
	peripheral.StartListening()

	// Send handshake
	ip.sendHandshakeMessage(peripheral)
}

func (ip *iPhone) DidWriteValueForCharacteristic(peripheral *swift.CBPeripheral, characteristic *swift.CBCharacteristic, err error) {
	if err != nil {
		prefix := fmt.Sprintf("%s iOS", ip.deviceUUID[:8])
		logger.Error(prefix, "❌ Write failed for characteristic %s: %v", characteristic.UUID[:8], err)
	}
}

func (ip *iPhone) DidUpdateValueForCharacteristic(peripheral *swift.CBPeripheral, characteristic *swift.CBCharacteristic, err error) {
	prefix := fmt.Sprintf("%s iOS", ip.deviceUUID[:8])
	if err != nil {
		logger.Error(prefix, "❌ Read failed: %v", err)
		return
	}

	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"
	const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"

	// Handle based on characteristic type
	if characteristic.UUID == auraTextCharUUID {
		ip.handleHandshakeMessage(peripheral, characteristic.Value)
	} else if characteristic.UUID == auraPhotoCharUUID {
		ip.handlePhotoMessage(peripheral, characteristic.Value)
	}
}

// sendHandshakeMessage sends a handshake to a connected peripheral
func (ip *iPhone) sendHandshakeMessage(peripheral *swift.CBPeripheral) error {
	prefix := fmt.Sprintf("%s iOS", ip.deviceUUID[:8])

	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"

	// Get the text characteristic
	textChar := peripheral.GetCharacteristic(auraServiceUUID, auraTextCharUUID)
	if textChar == nil {
		return fmt.Errorf("text characteristic not found")
	}

	msg := &proto.HandshakeMessage{
		DeviceId:        ip.deviceUUID,
		FirstName:       "iOS",
		ProtocolVersion: 1,
		TxPhotoHash:     ip.photoHash,
		RxPhotoHash:     ip.receivedPhotoHashes[peripheral.UUID],
	}

	// Marshal to binary protobuf (sent over the wire)
	data, err := proto2.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal handshake: %w", err)
	}

	// Log as JSON with snake_case for debugging
	marshaler := protojson.MarshalOptions{
		UseProtoNames: true,
	}
	jsonData, _ := marshaler.Marshal(msg)
	logger.Debug(prefix, "📤 TX Handshake (binary protobuf, %d bytes): %s", len(data), string(jsonData))

	// Handshake is critical - use withResponse to ensure delivery
	err = peripheral.WriteValue(data, textChar, swift.CBCharacteristicWriteWithResponse)
	if err == nil {
		// Record handshake timestamp on successful send
		ip.lastHandshakeTime[peripheral.UUID] = time.Now()
	}
	return err
}

// handleHandshakeMessage processes incoming handshake messages
func (ip *iPhone) handleHandshakeMessage(peripheral *swift.CBPeripheral, data []byte) {
	prefix := fmt.Sprintf("%s iOS", ip.deviceUUID[:8])

	// Unmarshal binary protobuf
	var handshake proto.HandshakeMessage
	if err := proto2.Unmarshal(data, &handshake); err != nil {
		logger.Error(prefix, "❌ Failed to parse handshake: %v", err)
		return
	}

	// Log as JSON with snake_case for debugging
	marshaler := protojson.MarshalOptions{
		UseProtoNames: true,
	}
	jsonData, _ := marshaler.Marshal(&handshake)
	logger.Debug(prefix, "📥 RX Handshake (binary protobuf, %d bytes): %s", len(data), string(jsonData))

	// Record handshake timestamp when received
	ip.lastHandshakeTime[peripheral.UUID] = time.Now()

	// Store their TX photo hash
	if handshake.TxPhotoHash != "" {
		ip.deviceIDToPhotoHash[peripheral.UUID] = handshake.TxPhotoHash
	}

	// Check if we need to send our photo
	if handshake.RxPhotoHash != ip.photoHash {
		logger.Debug(prefix, "📸 Remote doesn't have our photo, sending...")
		go ip.sendPhoto(peripheral, handshake.RxPhotoHash)
	} else {
		logger.Debug(prefix, "⏭️  Remote already has our photo")
	}
}

// sendPhoto sends our profile photo to a connected device
func (ip *iPhone) sendPhoto(peripheral *swift.CBPeripheral, remoteRxPhotoHash string) error {
	prefix := fmt.Sprintf("%s iOS", ip.deviceUUID[:8])

	// Check if they already have our photo
	if remoteRxPhotoHash == ip.photoHash {
		logger.Debug(prefix, "⏭️  Remote already has our photo, skipping")
		return nil
	}

	// Check if a photo send is already in progress to this device
	if ip.photoSendInProgress[peripheral.UUID] {
		logger.Debug(prefix, "⏭️  Photo send already in progress to %s, skipping duplicate", peripheral.UUID[:8])
		return nil
	}

	// Mark photo send as in progress
	ip.photoSendInProgress[peripheral.UUID] = true
	defer func() {
		// Clear flag when done
		delete(ip.photoSendInProgress, peripheral.UUID)
	}()

	// Load our cached photo
	cachePath := fmt.Sprintf("data/%s/cache/my_photo.jpg", ip.deviceUUID)
	photoData, err := os.ReadFile(cachePath)
	if err != nil {
		return fmt.Errorf("failed to load photo: %w", err)
	}

	// Calculate total CRC
	totalCRC := phototransfer.CalculateCRC32(photoData)

	// Split into chunks
	chunks := phototransfer.SplitIntoChunks(photoData, phototransfer.DefaultChunkSize)

	logger.Info(prefix, "📸 Sending photo to %s (%d bytes, %d chunks, CRC: %08X)",
		peripheral.UUID[:8], len(photoData), len(chunks), totalCRC)

	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"

	photoChar := peripheral.GetCharacteristic(auraServiceUUID, auraPhotoCharUUID)
	if photoChar == nil {
		return fmt.Errorf("photo characteristic not found")
	}

	// Send metadata packet - critical, use withResponse
	metadata := phototransfer.EncodeMetadata(uint32(len(photoData)), totalCRC, uint16(len(chunks)), nil)
	if err := peripheral.WriteValue(metadata, photoChar, swift.CBCharacteristicWriteWithResponse); err != nil {
		return fmt.Errorf("failed to send metadata: %w", err)
	}

	// Send chunks with withoutResponse for speed (fire and forget)
	// This is realistic - photo chunks use fast writes, app-level CRC catches errors
	for i, chunk := range chunks {
		chunkPacket := phototransfer.EncodeChunk(uint16(i), chunk)
		if err := peripheral.WriteValue(chunkPacket, photoChar, swift.CBCharacteristicWriteWithoutResponse); err != nil {
			return fmt.Errorf("failed to send chunk %d: %w", i, err)
		}
		time.Sleep(10 * time.Millisecond)

		if i == 0 || i == len(chunks)-1 {
			logger.Debug(prefix, "📤 Sent chunk %d/%d", i+1, len(chunks))
		}
	}

	logger.Info(prefix, "✅ Photo send complete to %s", peripheral.UUID[:8])
	return nil
}

// handlePhotoMessage processes incoming photo data
func (ip *iPhone) handlePhotoMessage(peripheral *swift.CBPeripheral, data []byte) {
	prefix := fmt.Sprintf("%s iOS", ip.deviceUUID[:8])

	// Try to decode metadata
	if len(data) >= phototransfer.MetadataSize {
		meta, remaining, err := phototransfer.DecodeMetadata(data)
		if err == nil {
			// Metadata packet received
			logger.Info(prefix, "📸 Receiving photo from %s (size: %d, CRC: %08X, chunks: %d)",
				peripheral.UUID[:8], meta.TotalSize, meta.TotalCRC, meta.TotalChunks)

			ip.photoReceiveState.isReceiving = true
			ip.photoReceiveState.expectedSize = meta.TotalSize
			ip.photoReceiveState.expectedCRC = meta.TotalCRC
			ip.photoReceiveState.expectedChunks = meta.TotalChunks
			ip.photoReceiveState.receivedChunks = make(map[uint16][]byte)
			ip.photoReceiveState.senderDeviceID = peripheral.UUID
			ip.photoReceiveState.buffer = remaining

			if len(remaining) > 0 {
				ip.processPhotoChunks()
			}
			return
		}
	}

	// Regular chunk data
	if ip.photoReceiveState.isReceiving {
		ip.photoReceiveState.buffer = append(ip.photoReceiveState.buffer, data...)
		ip.processPhotoChunks()
	}
}

// processPhotoChunks processes buffered photo chunks
func (ip *iPhone) processPhotoChunks() {
	prefix := fmt.Sprintf("%s iOS", ip.deviceUUID[:8])

	for {
		if len(ip.photoReceiveState.buffer) < phototransfer.ChunkHeaderSize {
			break
		}

		chunk, consumed, err := phototransfer.DecodeChunk(ip.photoReceiveState.buffer)
		if err != nil {
			break
		}

		ip.photoReceiveState.receivedChunks[chunk.Index] = chunk.Data
		ip.photoReceiveState.buffer = ip.photoReceiveState.buffer[consumed:]

		if chunk.Index == 0 || chunk.Index == ip.photoReceiveState.expectedChunks-1 {
			logger.Debug(prefix, "📥 Received chunk %d/%d",
				len(ip.photoReceiveState.receivedChunks), ip.photoReceiveState.expectedChunks)
		}

		// Check if complete
		if uint16(len(ip.photoReceiveState.receivedChunks)) == ip.photoReceiveState.expectedChunks {
			ip.reassembleAndSavePhoto()
			ip.photoReceiveState.isReceiving = false
			break
		}
	}
}

// reassembleAndSavePhoto reassembles chunks and saves the photo
func (ip *iPhone) reassembleAndSavePhoto() error {
	prefix := fmt.Sprintf("%s iOS", ip.deviceUUID[:8])

	// Reassemble in order
	var photoData []byte
	for i := uint16(0); i < ip.photoReceiveState.expectedChunks; i++ {
		photoData = append(photoData, ip.photoReceiveState.receivedChunks[i]...)
	}

	// Verify CRC
	calculatedCRC := phototransfer.CalculateCRC32(photoData)
	if calculatedCRC != ip.photoReceiveState.expectedCRC {
		logger.Error(prefix, "❌ Photo CRC mismatch: expected %08X, got %08X",
			ip.photoReceiveState.expectedCRC, calculatedCRC)
		return fmt.Errorf("CRC mismatch")
	}

	// Calculate hash
	hash := sha256.Sum256(photoData)
	hashStr := hex.EncodeToString(hash[:])

	// Save photo using cache manager (persists deviceID -> photoHash mapping)
	if err := ip.cacheManager.SaveDevicePhoto(ip.photoReceiveState.senderDeviceID, photoData, hashStr); err != nil {
		logger.Error(prefix, "❌ Failed to save photo: %v", err)
		return err
	}

	logger.Info(prefix, "✅ Photo saved from %s (hash: %s, size: %d bytes)",
		ip.photoReceiveState.senderDeviceID[:8], hashStr[:8], len(photoData))

	// Update in-memory mapping
	ip.receivedPhotoHashes[ip.photoReceiveState.senderDeviceID] = hashStr

	// Notify GUI about the new photo by re-triggering discovery callback
	if ip.discoveryCallback != nil {
		senderID := ip.photoReceiveState.senderDeviceID
		if peripheral, exists := ip.connectedPeripherals[senderID]; exists {
			// Get device name from advertising data or use UUID
			name := senderID[:8]
			if advData, err := ip.wire.ReadAdvertisingData(senderID); err == nil && advData != nil {
				if advData.DeviceName != "" {
					name = advData.DeviceName
				}
			}

			ip.discoveryCallback(phone.DiscoveredDevice{
				DeviceID:  senderID,
				Name:      name,
				RSSI:      -50, // Default RSSI, actual value not critical for photo update
				Platform:  "unknown",
				PhotoHash: hashStr,
				PhotoData: photoData,
			})
			logger.Debug(prefix, "🔔 Notified GUI about received photo from %s", peripheral.UUID[:8])
		}
	}

	return nil
}

// startStaleHandshakeChecker runs a background task to check for stale handshakes
func (ip *iPhone) startStaleHandshakeChecker() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				ip.checkStaleHandshakes()
			case <-ip.staleCheckDone:
				return
			}
		}
	}()
}

// checkStaleHandshakes checks all connected devices for stale handshakes and re-handshakes if needed
func (ip *iPhone) checkStaleHandshakes() {
	prefix := fmt.Sprintf("%s iOS", ip.deviceUUID[:8])
	now := time.Now()
	staleThreshold := 60 * time.Second

	for peripheralUUID, peripheral := range ip.connectedPeripherals {
		lastHandshake, exists := ip.lastHandshakeTime[peripheralUUID]

		// If no handshake record or handshake is stale
		if !exists || now.Sub(lastHandshake) > staleThreshold {
			timeSince := "never"
			if exists {
				timeSince = fmt.Sprintf("%.0fs ago", now.Sub(lastHandshake).Seconds())
			}

			logger.Debug(prefix, "🔄 [STALE-HANDSHAKE] Handshake stale for %s (last: %s), re-handshaking",
				peripheralUUID[:8], timeSince)

			// Re-handshake in place (no need to reconnect, already connected)
			go ip.sendHandshakeMessage(peripheral)
		}
	}
}
