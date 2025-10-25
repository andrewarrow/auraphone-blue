package android

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/user/auraphone-blue/kotlin"
	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/phototransfer"
	"github.com/user/auraphone-blue/proto"
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

// Android represents an Android device with BLE capabilities
type Android struct {
	deviceUUID        string
	deviceName        string
	wire              *wire.Wire
	manager           *kotlin.BluetoothManager
	discoveryCallback phone.DeviceDiscoveryCallback
	photoPath         string
	photoHash         string
	photoData         []byte
	connectedGatts    map[string]*kotlin.BluetoothGatt // UUID -> GATT connection
	deviceIDToPhotoHash  map[string]string              // deviceID -> their TX photo hash
	receivedPhotoHashes  map[string]string              // deviceID -> RX hash (photos we got from them)
	photoReceiveState    *photoReceiveState
}

// NewAndroid creates a new Android instance
func NewAndroid() *Android {
	deviceUUID := uuid.New().String()
	deviceName := "Android Device"

	a := &Android{
		deviceUUID:           deviceUUID,
		deviceName:           deviceName,
		connectedGatts:       make(map[string]*kotlin.BluetoothGatt),
		deviceIDToPhotoHash:  make(map[string]string),
		receivedPhotoHashes:  make(map[string]string),
		photoReceiveState: &photoReceiveState{
			receivedChunks: make(map[uint16][]byte),
		},
	}

	// Initialize wire
	a.wire = wire.NewWire(deviceUUID)
	if err := a.wire.InitializeDevice(); err != nil {
		fmt.Printf("Failed to initialize Android device: %v\n", err)
		return nil
	}

	// Setup BLE
	a.setupBLE()

	// Create manager
	a.manager = kotlin.NewBluetoothManager(deviceUUID)

	return a
}

// setupBLE configures GATT table and advertising data
func (a *Android) setupBLE() {
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

	if err := a.wire.WriteGATTTable(gattTable); err != nil {
		fmt.Printf("Failed to write GATT table: %v\n", err)
		return
	}

	// Create advertising data
	txPowerLevel := 0
	advertisingData := &wire.AdvertisingData{
		DeviceName:    a.deviceName,
		ServiceUUIDs:  []string{auraServiceUUID},
		TxPowerLevel:  &txPowerLevel,
		IsConnectable: true,
	}

	if err := a.wire.WriteAdvertisingData(advertisingData); err != nil {
		fmt.Printf("Failed to write advertising data: %v\n", err)
		return
	}

	logger.Info(fmt.Sprintf("%s Android", a.deviceUUID[:8]), "Setup complete - advertising as: %s", a.deviceName)
}

// Start begins BLE advertising and scanning
func (a *Android) Start() {
	go func() {
		time.Sleep(500 * time.Millisecond)
		scanner := a.manager.Adapter.GetBluetoothLeScanner()
		scanner.StartScan(a)
		logger.Info(fmt.Sprintf("%s Android", a.deviceUUID[:8]), "Started scanning for devices")
	}()
}

// Stop stops BLE operations and cleans up resources
func (a *Android) Stop() {
	fmt.Printf("[%s Android] Stopping BLE operations\n", a.deviceUUID[:8])
	// Future: stop scanning, disconnect, cleanup
}

// SetDiscoveryCallback sets the callback for when devices are discovered
func (a *Android) SetDiscoveryCallback(callback phone.DeviceDiscoveryCallback) {
	a.discoveryCallback = callback
}

// GetDeviceUUID returns the device's UUID
func (a *Android) GetDeviceUUID() string {
	return a.deviceUUID
}

// GetDeviceName returns the device's name
func (a *Android) GetDeviceName() string {
	return a.deviceName
}

// GetPlatform returns the platform type
func (a *Android) GetPlatform() string {
	return "Android"
}

// ScanCallback methods

func (a *Android) OnScanResult(callbackType int, result *kotlin.ScanResult) {
	name := result.Device.Name
	if result.ScanRecord != nil && result.ScanRecord.DeviceName != "" {
		name = result.ScanRecord.DeviceName
	}

	rssi := float64(result.Rssi)

	prefix := fmt.Sprintf("%s Android", a.deviceUUID[:8])
	logger.Debug(prefix, "📱 DISCOVERED device %s (%s)", result.Device.Address[:8], name)
	logger.Debug(prefix, "   └─ RSSI: %.0f dBm", rssi)

	if a.discoveryCallback != nil {
		a.discoveryCallback(phone.DiscoveredDevice{
			DeviceID:  result.Device.Address,
			Name:      name,
			RSSI:      rssi,
			Platform:  "unknown",
			PhotoHash: "", // Photo hash is now exchanged via protocol buffer handshake, not advertising
		})
	}

	// Auto-connect if not already connected
	if _, exists := a.connectedGatts[result.Device.Address]; !exists {
		logger.Debug(prefix, "🔌 Connecting to %s", result.Device.Address[:8])
		gatt := result.Device.ConnectGatt(nil, false, a)
		a.connectedGatts[result.Device.Address] = gatt
	}
}

// SetProfilePhoto sets the profile photo and broadcasts the hash
func (a *Android) SetProfilePhoto(photoPath string) error {
	// Read photo file
	data, err := os.ReadFile(photoPath)
	if err != nil {
		return fmt.Errorf("failed to read photo: %w", err)
	}

	// Calculate hash
	hash := sha256.Sum256(data)
	photoHash := hex.EncodeToString(hash[:])

	// Cache photo to disk
	cachePath := fmt.Sprintf("data/%s/cache/my_photo.jpg", a.deviceUUID)
	cacheDir := fmt.Sprintf("data/%s/cache", a.deviceUUID)
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}
	if err := os.WriteFile(cachePath, data, 0644); err != nil {
		return fmt.Errorf("failed to cache photo: %w", err)
	}

	// Update fields
	a.photoPath = photoPath
	a.photoHash = photoHash
	a.photoData = data

	// Update advertising data to broadcast new hash
	a.setupBLE()

	logger.Info(fmt.Sprintf("%s Android", a.deviceUUID[:8]), "📸 Updated profile photo (hash: %s)", photoHash[:8])
	logger.Debug(fmt.Sprintf("%s Android", a.deviceUUID[:8]), "   └─ Cached to disk and broadcasting TX hash in advertising data")

	return nil
}

// GetProfilePhotoHash returns the hash of the current profile photo
func (a *Android) GetProfilePhotoHash() string {
	return a.photoHash
}

// BluetoothGattCallback methods

func (a *Android) OnConnectionStateChange(gatt *kotlin.BluetoothGatt, status int, newState int) {
	prefix := fmt.Sprintf("%s Android", a.deviceUUID[:8])

	if newState == 2 { // STATE_CONNECTED
		logger.Info(prefix, "✅ Connected to device")
		// Discover services
		gatt.DiscoverServices()
	} else if newState == 0 { // STATE_DISCONNECTED
		logger.Info(prefix, "❌ Disconnected from device")
	}
}

func (a *Android) OnServicesDiscovered(gatt *kotlin.BluetoothGatt, status int) {
	prefix := fmt.Sprintf("%s Android", a.deviceUUID[:8])

	if status != 0 { // GATT_SUCCESS = 0
		logger.Error(prefix, "❌ Service discovery failed")
		return
	}

	logger.Debug(prefix, "🔍 Discovered %d services", len(gatt.GetServices()))

	// Start listening for notifications
	gatt.StartListening()

	// Send handshake
	a.sendHandshakeMessage(gatt)
}

func (a *Android) OnCharacteristicWrite(gatt *kotlin.BluetoothGatt, characteristic *kotlin.BluetoothGattCharacteristic, status int) {
	if status != 0 { // GATT_SUCCESS = 0
		prefix := fmt.Sprintf("%s Android", a.deviceUUID[:8])
		logger.Error(prefix, "❌ Write failed for characteristic %s", characteristic.UUID[:8])
	}
}

func (a *Android) OnCharacteristicRead(gatt *kotlin.BluetoothGatt, characteristic *kotlin.BluetoothGattCharacteristic, status int) {
	// Not used in this implementation
}

func (a *Android) OnCharacteristicChanged(gatt *kotlin.BluetoothGatt, characteristic *kotlin.BluetoothGattCharacteristic) {
	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"
	const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"

	// Handle based on characteristic type
	if characteristic.UUID == auraTextCharUUID {
		a.handleHandshakeMessage(gatt, characteristic.Value)
	} else if characteristic.UUID == auraPhotoCharUUID {
		a.handlePhotoMessage(gatt, characteristic.Value)
	}
}

// sendHandshakeMessage sends a handshake to a connected device
func (a *Android) sendHandshakeMessage(gatt *kotlin.BluetoothGatt) error {
	prefix := fmt.Sprintf("%s Android", a.deviceUUID[:8])

	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"

	// Get the text characteristic
	textChar := gatt.GetCharacteristic(auraServiceUUID, auraTextCharUUID)
	if textChar == nil {
		return fmt.Errorf("text characteristic not found")
	}

	msg := &proto.HandshakeMessage{
		DeviceId:        a.deviceUUID,
		FirstName:       "Android",
		ProtocolVersion: 1,
		TxPhotoHash:     a.photoHash,
		RxPhotoHash:     a.receivedPhotoHashes[gatt.GetRemoteUUID()],
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

	textChar.Value = data
	gatt.WriteCharacteristic(textChar)
	return nil
}

// handleHandshakeMessage processes incoming handshake messages
func (a *Android) handleHandshakeMessage(gatt *kotlin.BluetoothGatt, data []byte) {
	prefix := fmt.Sprintf("%s Android", a.deviceUUID[:8])

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

	remoteUUID := gatt.GetRemoteUUID()

	// Store their TX photo hash
	if handshake.TxPhotoHash != "" {
		a.deviceIDToPhotoHash[remoteUUID] = handshake.TxPhotoHash
	}

	// Check if we need to send our photo
	if handshake.RxPhotoHash != a.photoHash {
		logger.Debug(prefix, "📸 Remote doesn't have our photo, sending...")
		go a.sendPhoto(gatt, handshake.RxPhotoHash)
	} else {
		logger.Debug(prefix, "⏭️  Remote already has our photo")
	}
}

// sendPhoto sends our profile photo to a connected device
func (a *Android) sendPhoto(gatt *kotlin.BluetoothGatt, remoteRxPhotoHash string) error {
	prefix := fmt.Sprintf("%s Android", a.deviceUUID[:8])
	remoteUUID := gatt.GetRemoteUUID()

	// Check if they already have our photo
	if remoteRxPhotoHash == a.photoHash {
		logger.Debug(prefix, "⏭️  Remote already has our photo, skipping")
		return nil
	}

	// Load our cached photo
	cachePath := fmt.Sprintf("data/%s/cache/my_photo.jpg", a.deviceUUID)
	photoData, err := os.ReadFile(cachePath)
	if err != nil {
		return fmt.Errorf("failed to load photo: %w", err)
	}

	// Calculate total CRC
	totalCRC := phototransfer.CalculateCRC32(photoData)

	// Split into chunks
	chunks := phototransfer.SplitIntoChunks(photoData, phototransfer.DefaultChunkSize)

	logger.Info(prefix, "📸 Sending photo to %s (%d bytes, %d chunks, CRC: %08X)",
		remoteUUID[:8], len(photoData), len(chunks), totalCRC)

	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"

	photoChar := gatt.GetCharacteristic(auraServiceUUID, auraPhotoCharUUID)
	if photoChar == nil {
		return fmt.Errorf("photo characteristic not found")
	}

	// Send metadata packet
	metadata := phototransfer.EncodeMetadata(uint32(len(photoData)), totalCRC, uint16(len(chunks)), nil)
	photoChar.Value = metadata
	if !gatt.WriteCharacteristic(photoChar) {
		return fmt.Errorf("failed to send metadata")
	}

	// Send chunks with delays
	for i, chunk := range chunks {
		chunkPacket := phototransfer.EncodeChunk(uint16(i), chunk)
		photoChar.Value = chunkPacket
		if !gatt.WriteCharacteristic(photoChar) {
			return fmt.Errorf("failed to send chunk %d", i)
		}
		time.Sleep(10 * time.Millisecond)

		if i == 0 || i == len(chunks)-1 {
			logger.Debug(prefix, "📤 Sent chunk %d/%d", i+1, len(chunks))
		}
	}

	logger.Info(prefix, "✅ Photo send complete to %s", remoteUUID[:8])
	return nil
}

// handlePhotoMessage processes incoming photo data
func (a *Android) handlePhotoMessage(gatt *kotlin.BluetoothGatt, data []byte) {
	prefix := fmt.Sprintf("%s Android", a.deviceUUID[:8])
	remoteUUID := gatt.GetRemoteUUID()

	// Try to decode metadata
	if len(data) >= phototransfer.MetadataSize {
		meta, remaining, err := phototransfer.DecodeMetadata(data)
		if err == nil {
			// Metadata packet received
			logger.Info(prefix, "📸 Receiving photo from %s (size: %d, CRC: %08X, chunks: %d)",
				remoteUUID[:8], meta.TotalSize, meta.TotalCRC, meta.TotalChunks)

			a.photoReceiveState.isReceiving = true
			a.photoReceiveState.expectedSize = meta.TotalSize
			a.photoReceiveState.expectedCRC = meta.TotalCRC
			a.photoReceiveState.expectedChunks = meta.TotalChunks
			a.photoReceiveState.receivedChunks = make(map[uint16][]byte)
			a.photoReceiveState.senderDeviceID = remoteUUID
			a.photoReceiveState.buffer = remaining

			if len(remaining) > 0 {
				a.processPhotoChunks()
			}
			return
		}
	}

	// Regular chunk data
	if a.photoReceiveState.isReceiving {
		a.photoReceiveState.buffer = append(a.photoReceiveState.buffer, data...)
		a.processPhotoChunks()
	}
}

// processPhotoChunks processes buffered photo chunks
func (a *Android) processPhotoChunks() {
	prefix := fmt.Sprintf("%s Android", a.deviceUUID[:8])

	for {
		if len(a.photoReceiveState.buffer) < phototransfer.ChunkHeaderSize {
			break
		}

		chunk, consumed, err := phototransfer.DecodeChunk(a.photoReceiveState.buffer)
		if err != nil {
			break
		}

		a.photoReceiveState.receivedChunks[chunk.Index] = chunk.Data
		a.photoReceiveState.buffer = a.photoReceiveState.buffer[consumed:]

		if chunk.Index == 0 || chunk.Index == a.photoReceiveState.expectedChunks-1 {
			logger.Debug(prefix, "📥 Received chunk %d/%d",
				len(a.photoReceiveState.receivedChunks), a.photoReceiveState.expectedChunks)
		}

		// Check if complete
		if uint16(len(a.photoReceiveState.receivedChunks)) == a.photoReceiveState.expectedChunks {
			a.reassembleAndSavePhoto()
			a.photoReceiveState.isReceiving = false
			break
		}
	}
}

// reassembleAndSavePhoto reassembles chunks and saves the photo
func (a *Android) reassembleAndSavePhoto() error {
	prefix := fmt.Sprintf("%s Android", a.deviceUUID[:8])

	// Reassemble in order
	var photoData []byte
	for i := uint16(0); i < a.photoReceiveState.expectedChunks; i++ {
		photoData = append(photoData, a.photoReceiveState.receivedChunks[i]...)
	}

	// Verify CRC
	calculatedCRC := phototransfer.CalculateCRC32(photoData)
	if calculatedCRC != a.photoReceiveState.expectedCRC {
		logger.Error(prefix, "❌ Photo CRC mismatch: expected %08X, got %08X",
			a.photoReceiveState.expectedCRC, calculatedCRC)
		return fmt.Errorf("CRC mismatch")
	}

	// Calculate hash
	hash := sha256.Sum256(photoData)
	hashStr := hex.EncodeToString(hash[:])

	// Save with hash as filename
	cachePath := fmt.Sprintf("data/%s/cache/photos/%s.jpg", a.deviceUUID, hashStr)
	os.MkdirAll(filepath.Dir(cachePath), 0755)
	if err := os.WriteFile(cachePath, photoData, 0644); err != nil {
		return err
	}

	logger.Info(prefix, "✅ Photo saved from %s (hash: %s, size: %d bytes)",
		a.photoReceiveState.senderDeviceID[:8], hashStr[:8], len(photoData))

	// Update received photo hash mapping
	a.receivedPhotoHashes[a.photoReceiveState.senderDeviceID] = hashStr

	// Notify GUI about the new photo by re-triggering discovery callback
	if a.discoveryCallback != nil {
		senderID := a.photoReceiveState.senderDeviceID
		if _, exists := a.connectedGatts[senderID]; exists {
			// Get device name from advertising data or use UUID
			name := senderID[:8]
			if advData, err := a.wire.ReadAdvertisingData(senderID); err == nil && advData != nil {
				if advData.DeviceName != "" {
					name = advData.DeviceName
				}
			}

			a.discoveryCallback(phone.DiscoveredDevice{
				DeviceID:  senderID,
				Name:      name,
				RSSI:      -55, // Default RSSI, actual value not critical for photo update
				Platform:  "unknown",
				PhotoHash: hashStr,
				PhotoData: photoData,
			})
			logger.Debug(prefix, "🔔 Notified GUI about received photo from %s", senderID[:8])
		}
	}

	return nil
}
