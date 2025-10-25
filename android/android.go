package android

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/user/auraphone-blue/kotlin"
	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/phototransfer"
	"github.com/user/auraphone-blue/proto"
	"github.com/user/auraphone-blue/wire"
	"google.golang.org/protobuf/encoding/protojson"
	proto2 "google.golang.org/protobuf/proto"
)

// truncateHash safely truncates a hash string to the first n characters
// Returns the full string if it's shorter than n
func truncateHash(hash string, n int) string {
	if len(hash) <= n {
		return hash
	}
	return hash[:n]
}

// hashStringToBytes converts a hex hash string to raw bytes for protobuf
func hashStringToBytes(hashStr string) []byte {
	if hashStr == "" {
		return nil
	}
	bytes, err := hex.DecodeString(hashStr)
	if err != nil {
		return nil
	}
	return bytes
}

// hashBytesToString converts raw bytes from protobuf to hex hash string
func hashBytesToString(hashBytes []byte) string {
	if len(hashBytes) == 0 {
		return ""
	}
	return hex.EncodeToString(hashBytes)
}

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

// LocalProfile stores our local profile information
type LocalProfile struct {
	FirstName      string
	LastName       string
	Tagline        string
	Insta          string
	LinkedIn       string
	YouTube        string
	TikTok         string
	Gmail          string
	IMessage       string
	WhatsApp       string
	Signal         string
	Telegram       string
	ProfileVersion int32 // Increments on any profile change
}

// Android represents an Android device with BLE capabilities
type Android struct {
	hardwareUUID         string // Bluetooth hardware UUID (never changes, from testdata/hardware_uuids.txt)
	deviceID             string // Logical device ID (8-char base36, cached to disk)
	deviceName           string
	wire                 *wire.Wire
	cacheManager         *phone.DeviceCacheManager          // Persistent photo storage
	manager              *kotlin.BluetoothManager
	discoveryCallback    phone.DeviceDiscoveryCallback
	photoPath            string
	photoHash            string
	photoData            []byte
	localProfile         *LocalProfile                      // Our local profile data
	mu                   sync.RWMutex                       // Protects all maps below
	connectedGatts       map[string]*kotlin.BluetoothGatt   // remote UUID -> GATT connection
	discoveredDevices      map[string]*kotlin.BluetoothDevice // remote UUID -> discovered device (for reconnect)
	remoteUUIDToDeviceID   map[string]string                  // remote UUID -> logical device ID
	deviceIDToPhotoHash    map[string]string                  // deviceID -> their TX photo hash
	receivedPhotoHashes    map[string]string                  // deviceID -> RX hash (photos we got from them)
	receivedProfileVersion map[string]int32                   // deviceID -> their profile version
	lastHandshakeTime      map[string]time.Time               // deviceID -> last handshake timestamp
	photoSendInProgress    map[string]bool                    // deviceID -> true if photo send in progress
	photoReceiveState    *photoReceiveState
	useAutoConnect       bool          // Whether to use autoConnect=true mode
	staleCheckDone       chan struct{} // Signal channel for stopping background checker
}

// NewAndroid creates a new Android instance with a hardware UUID
func NewAndroid(hardwareUUID string) *Android {
	deviceName := "Android Device"

	// Load or generate device ID (8-char base36, persists across app restarts)
	deviceID, err := phone.LoadOrGenerateDeviceID(hardwareUUID)
	if err != nil {
		fmt.Printf("Failed to load/generate device ID: %v\n", err)
		return nil
	}

	a := &Android{
		hardwareUUID:           hardwareUUID,
		deviceID:               deviceID,
		deviceName:             deviceName,
		connectedGatts:         make(map[string]*kotlin.BluetoothGatt),
		discoveredDevices:      make(map[string]*kotlin.BluetoothDevice),
		remoteUUIDToDeviceID:   make(map[string]string),
		deviceIDToPhotoHash:    make(map[string]string),
		receivedPhotoHashes:    make(map[string]string),
		receivedProfileVersion: make(map[string]int32),
		lastHandshakeTime:      make(map[string]time.Time),
		photoSendInProgress:    make(map[string]bool),
		staleCheckDone:         make(chan struct{}),
		photoReceiveState: &photoReceiveState{
			receivedChunks: make(map[uint16][]byte),
		},
		useAutoConnect: false, // Default: manual reconnect (matches real Android apps)
	}

	// Initialize wire with hardware UUID
	a.wire = wire.NewWire(hardwareUUID)
	if err := a.wire.InitializeDevice(); err != nil {
		fmt.Printf("Failed to initialize Android device: %v\n", err)
		return nil
	}

	// Initialize cache manager with hardware UUID
	a.cacheManager = phone.NewDeviceCacheManager(hardwareUUID)
	if err := a.cacheManager.InitializeCache(); err != nil {
		fmt.Printf("Failed to initialize cache: %v\n", err)
		return nil
	}

	// Load existing photo mappings from disk
	a.loadReceivedPhotoMappings()

	// Load local profile from disk
	a.localProfile = a.loadLocalProfile()

	// Setup BLE
	a.setupBLE()

	// Create manager with hardware UUID
	a.manager = kotlin.NewBluetoothManager(hardwareUUID)

	return a
}

// setupBLE configures GATT table and advertising data
func (a *Android) setupBLE() {
	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"      // Handshake messages
	const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"    // Photo transfer
	const auraProfileCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5C" // Profile messages

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
					{
						UUID:       auraProfileCharUUID,
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

	logger.Info(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "Setup complete - advertising as: %s (deviceID: %s)", a.deviceName, a.deviceID)
}

// loadReceivedPhotoMappings loads deviceID->photoHash mappings from disk cache
// This restores knowledge of which photos we've already received from other devices
func (a *Android) loadReceivedPhotoMappings() {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	// Scan cache/photos directory for received photos
	photosDir := fmt.Sprintf("data/%s/cache/photos", a.hardwareUUID)
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
		metadataFiles, err := filepath.Glob(fmt.Sprintf("data/%s/cache/*_metadata.json", a.hardwareUUID))
		if err != nil {
			continue
		}

		for _, metadataFile := range metadataFiles {
			deviceID := filepath.Base(metadataFile)
			deviceID = deviceID[:len(deviceID)-len("_metadata.json")]

			// Check if this device's metadata references our photo hash
			hash, err := a.cacheManager.GetDevicePhotoHash(deviceID)
			if err == nil && hash == photoHash {
				a.receivedPhotoHashes[deviceID] = photoHash
				loadedCount++
				logger.Debug(prefix, "Loaded cached photo mapping: %s -> %s", deviceID[:8], truncateHash(photoHash, 8))
				break
			}
		}
	}

	if loadedCount > 0 {
		logger.Info(prefix, "Loaded %d cached photo mappings from disk", loadedCount)
	}
}

// Start begins BLE advertising and scanning
func (a *Android) Start() {
	// Start scanning for devices
	go func() {
		time.Sleep(500 * time.Millisecond)
		scanner := a.manager.Adapter.GetBluetoothLeScanner()
		scanner.StartScan(a)
		logger.Info(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "Started scanning for devices")
	}()

	// Start periodic stale handshake checker
	a.startStaleHandshakeChecker()
	logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "Started stale handshake checker (60s threshold, 30s interval)")
}

// Stop stops BLE operations and cleans up resources
func (a *Android) Stop() {
	fmt.Printf("[%s Android] Stopping BLE operations\n", a.hardwareUUID[:8])
	// Stop stale handshake checker
	close(a.staleCheckDone)
	// Future: stop scanning, disconnect, cleanup
}

// SetDiscoveryCallback sets the callback for when devices are discovered
func (a *Android) SetDiscoveryCallback(callback phone.DeviceDiscoveryCallback) {
	a.discoveryCallback = callback
}

// GetDeviceUUID returns the device's hardware UUID (for BLE operations)
func (a *Android) GetDeviceUUID() string {
	return a.hardwareUUID
}

// GetDeviceID returns the device's logical ID (8-char base36)
func (a *Android) GetDeviceID() string {
	return a.deviceID
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

	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
	logger.Debug(prefix, "📱 DISCOVERED device %s (%s)", result.Device.Address[:8], name)
	logger.Debug(prefix, "   └─ RSSI: %.0f dBm", rssi)

	// Store discovered device for potential reconnect
	a.mu.Lock()
	a.discoveredDevices[result.Device.Address] = result.Device
	_, alreadyConnected := a.connectedGatts[result.Device.Address]
	a.mu.Unlock()

	if a.discoveryCallback != nil {
		a.discoveryCallback(phone.DiscoveredDevice{
			DeviceID:  result.Device.Address,
			Name:      name,
			RSSI:      rssi,
			Platform:  "unknown",
			PhotoHash: "", // Photo hash is now exchanged via protocol buffer handshake, not advertising
		})
	}

	// Auto-connect if not already connected AND we should act as Central
	if !alreadyConnected {
		// Check if we should act as Central for this device using role negotiation
		if a.shouldActAsCentral(result.Device.Address, name) {
			logger.Debug(prefix, "🔌 Connecting to %s (acting as Central, autoConnect=%v)", result.Device.Address[:8], a.useAutoConnect)
			gatt := result.Device.ConnectGatt(nil, a.useAutoConnect, a)
			a.mu.Lock()
			a.connectedGatts[result.Device.Address] = gatt
			a.mu.Unlock()
		} else {
			logger.Debug(prefix, "⏸️  Not connecting to %s (will act as Peripheral, waiting for them to connect)", result.Device.Address[:8])
		}
	}
}

// shouldActAsCentral determines if this Android device should initiate connection to a discovered device
// Returns true if we should connect (act as Central), false if we should wait (act as Peripheral)
func (a *Android) shouldActAsCentral(remoteUUID, remoteName string) bool {
	// Determine remote platform from device name
	isRemoteIOS := false
	isRemoteAndroid := false

	if remoteName != "" {
		if len(remoteName) >= 3 && remoteName[:3] == "iOS" {
			isRemoteIOS = true
		} else if len(remoteName) >= 7 && remoteName[:7] == "Android" {
			isRemoteAndroid = true
		}
	}

	// Android → iOS: Android acts as Peripheral (waits for iOS to connect)
	if isRemoteIOS {
		return false
	}

	// Android → Android: Device with larger name acts as Central
	if isRemoteAndroid {
		return a.deviceName > remoteName
	}

	// Unknown platform: use hardware UUID comparison as fallback
	return a.hardwareUUID > remoteUUID
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
	cachePath := fmt.Sprintf("data/%s/cache/my_photo.jpg", a.hardwareUUID)
	cacheDir := fmt.Sprintf("data/%s/cache", a.hardwareUUID)
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

	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
	logger.Info(prefix, "📸 Updated profile photo (hash: %s)", truncateHash(photoHash, 8))
	logger.Debug(prefix, "   └─ Cached to disk and broadcasting TX hash in advertising data")

	// Re-send handshake to all connected devices to notify them of the new photo
	a.mu.RLock()
	gattsCopy := make([]*kotlin.BluetoothGatt, 0, len(a.connectedGatts))
	for _, g := range a.connectedGatts {
		gattsCopy = append(gattsCopy, g)
	}
	a.mu.RUnlock()

	if len(gattsCopy) > 0 {
		logger.Debug(prefix, "   └─ Notifying %d connected device(s) of photo change", len(gattsCopy))
		for _, gatt := range gattsCopy {
			go a.sendHandshakeMessage(gatt)
		}
	}

	return nil
}

// GetProfilePhotoHash returns the hash of the current profile photo
func (a *Android) GetProfilePhotoHash() string {
	return a.photoHash
}

// GetLocalProfile returns the local profile as a map
func (a *Android) GetLocalProfile() map[string]string {
	return map[string]string{
		"first_name": a.localProfile.FirstName,
		"last_name":  a.localProfile.LastName,
		"tagline":    a.localProfile.Tagline,
		"insta":      a.localProfile.Insta,
		"linkedin":   a.localProfile.LinkedIn,
		"youtube":    a.localProfile.YouTube,
		"tiktok":     a.localProfile.TikTok,
		"gmail":      a.localProfile.Gmail,
		"imessage":   a.localProfile.IMessage,
		"whatsapp":   a.localProfile.WhatsApp,
		"signal":     a.localProfile.Signal,
		"telegram":   a.localProfile.Telegram,
	}
}

// UpdateLocalProfile updates the local profile
func (a *Android) UpdateLocalProfile(profile map[string]string) error {
	a.localProfile.FirstName = profile["first_name"]
	a.localProfile.LastName = profile["last_name"]
	a.localProfile.Tagline = profile["tagline"]
	a.localProfile.Insta = profile["insta"]
	a.localProfile.LinkedIn = profile["linkedin"]
	a.localProfile.YouTube = profile["youtube"]
	a.localProfile.TikTok = profile["tiktok"]
	a.localProfile.Gmail = profile["gmail"]
	a.localProfile.IMessage = profile["imessage"]
	a.localProfile.WhatsApp = profile["whatsapp"]
	a.localProfile.Signal = profile["signal"]
	a.localProfile.Telegram = profile["telegram"]

	return a.UpdateProfile(a.localProfile)
}

// BluetoothGattCallback methods

func (a *Android) OnConnectionStateChange(gatt *kotlin.BluetoothGatt, status int, newState int) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
	remoteUUID := gatt.GetRemoteUUID()

	if newState == 2 { // STATE_CONNECTED
		logger.Info(prefix, "✅ Connected to device")

		// Re-add to connected list (might have been removed on disconnect)
		a.mu.Lock()
		a.connectedGatts[remoteUUID] = gatt
		a.mu.Unlock()

		// Discover services
		gatt.DiscoverServices()
	} else if newState == 0 { // STATE_DISCONNECTED
		if status != 0 {
			logger.Error(prefix, "❌ Disconnected from %s with error (status=%d)", remoteUUID[:8], status)
		} else {
			logger.Info(prefix, "📡 Disconnected from %s (interference/distance)", remoteUUID[:8])
		}

		// Stop listening and write queue on the gatt
		gatt.StopListening()
		gatt.StopWriteQueue()

		// Remove from connected list
		a.mu.Lock()
		if _, exists := a.connectedGatts[remoteUUID]; exists {
			delete(a.connectedGatts, remoteUUID)
		}
		a.mu.Unlock()

		// Android reconnect behavior depends on autoConnect parameter:
		// - If autoConnect=true: BluetoothGatt will retry automatically in background
		// - If autoConnect=false: App must manually call connectGatt() again
		if a.useAutoConnect {
			logger.Info(prefix, "🔄 Android autoConnect=true: Will retry in background...")
		} else {
			logger.Info(prefix, "💡 Android autoConnect=false: App will manually reconnect...")
			// Manually reconnect after a delay (simulates real Android app behavior)
			go a.manualReconnect(remoteUUID)
		}
	}
}

func (a *Android) OnServicesDiscovered(gatt *kotlin.BluetoothGatt, status int) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	if status != 0 { // GATT_SUCCESS = 0
		logger.Error(prefix, "❌ Service discovery failed")
		return
	}

	logger.Debug(prefix, "🔍 Discovered %d services", len(gatt.GetServices()))

	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"
	const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"
	const auraProfileCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5C"

	// Enable notifications for characteristics (matches real Android behavior)
	textChar := gatt.GetCharacteristic(auraServiceUUID, auraTextCharUUID)
	if textChar != nil {
		if !gatt.SetCharacteristicNotification(textChar, true) {
			logger.Error(prefix, "❌ Failed to enable notifications for text characteristic")
		}
	}

	photoChar := gatt.GetCharacteristic(auraServiceUUID, auraPhotoCharUUID)
	if photoChar != nil {
		if !gatt.SetCharacteristicNotification(photoChar, true) {
			logger.Error(prefix, "❌ Failed to enable notifications for photo characteristic")
		}
	}

	profileChar := gatt.GetCharacteristic(auraServiceUUID, auraProfileCharUUID)
	if profileChar != nil {
		if !gatt.SetCharacteristicNotification(profileChar, true) {
			logger.Error(prefix, "❌ Failed to enable notifications for profile characteristic")
		}
	}

	// Start write queue for async writes (matches real Android behavior)
	gatt.StartWriteQueue()

	// Start listening for notifications
	gatt.StartListening()

	// Send handshake
	a.sendHandshakeMessage(gatt)
}

func (a *Android) OnCharacteristicWrite(gatt *kotlin.BluetoothGatt, characteristic *kotlin.BluetoothGattCharacteristic, status int) {
	if status != 0 { // GATT_SUCCESS = 0
		prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
		logger.Error(prefix, "❌ Write failed for characteristic %s", characteristic.UUID[:8])
	}
}

func (a *Android) OnCharacteristicRead(gatt *kotlin.BluetoothGatt, characteristic *kotlin.BluetoothGattCharacteristic, status int) {
	// Not used in this implementation
}

func (a *Android) OnCharacteristicChanged(gatt *kotlin.BluetoothGatt, characteristic *kotlin.BluetoothGattCharacteristic) {
	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"
	const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"
	const auraProfileCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5C"

	// Handle based on characteristic type
	if characteristic.UUID == auraTextCharUUID {
		// Text characteristic is for HandshakeMessage only
		a.handleHandshakeMessage(gatt, characteristic.Value)
	} else if characteristic.UUID == auraPhotoCharUUID {
		// Photo characteristic is for photo transfer
		a.handlePhotoMessage(gatt, characteristic.Value)
	} else if characteristic.UUID == auraProfileCharUUID {
		// Profile characteristic is for ProfileMessage
		a.handleProfileMessage(gatt, characteristic.Value)
	}
}

// sendHandshakeMessage sends a handshake to a connected device
func (a *Android) sendHandshakeMessage(gatt *kotlin.BluetoothGatt) error {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"

	// Get the text characteristic
	textChar := gatt.GetCharacteristic(auraServiceUUID, auraTextCharUUID)
	if textChar == nil {
		return fmt.Errorf("text characteristic not found")
	}

	firstName := a.localProfile.FirstName
	if firstName == "" {
		firstName = "Android" // Default if not set
	}

	// Get the deviceID for this remote connection
	remoteUUID := gatt.GetRemoteUUID()
	a.mu.RLock()
	deviceID := a.remoteUUIDToDeviceID[remoteUUID]
	rxPhotoHash := a.receivedPhotoHashes[deviceID]
	a.mu.RUnlock()

	msg := &proto.HandshakeMessage{
		DeviceId:        a.deviceID, // Send logical deviceID, not hardware UUID
		FirstName:       firstName,
		ProtocolVersion: 1,
		TxPhotoHash:     hashStringToBytes(a.photoHash),
		RxPhotoHash:     hashStringToBytes(rxPhotoHash),
		ProfileVersion:  a.localProfile.ProfileVersion,
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
	textChar.Value = data
	textChar.WriteType = kotlin.WRITE_TYPE_DEFAULT
	success := gatt.WriteCharacteristic(textChar)
	if success {
		// Record handshake timestamp on successful send
		a.mu.Lock()
		a.lastHandshakeTime[gatt.GetRemoteUUID()] = time.Now()
		a.mu.Unlock()
	}
	return nil
}

// handleHandshakeMessage processes incoming handshake messages
func (a *Android) handleHandshakeMessage(gatt *kotlin.BluetoothGatt, data []byte) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

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

	// IMPORTANT: Map remoteUUID to the logical device_id from handshake
	deviceID := handshake.DeviceId
	a.mu.Lock()
	a.remoteUUIDToDeviceID[remoteUUID] = deviceID
	a.mu.Unlock()

	// Record handshake timestamp when received
	a.mu.Lock()
	a.lastHandshakeTime[deviceID] = time.Now()
	a.mu.Unlock()

	// Convert photo hashes from bytes to hex strings
	txPhotoHash := hashBytesToString(handshake.TxPhotoHash)
	rxPhotoHash := hashBytesToString(handshake.RxPhotoHash)

	// Store their TX photo hash
	if txPhotoHash != "" {
		a.mu.Lock()
		a.deviceIDToPhotoHash[deviceID] = txPhotoHash
		a.mu.Unlock()
	}

	// Save first_name to device metadata
	if handshake.FirstName != "" {
		metadata, _ := a.cacheManager.LoadDeviceMetadata(deviceID)
		if metadata == nil {
			metadata = &phone.DeviceMetadata{
				DeviceID: deviceID,
			}
		}
		metadata.FirstName = handshake.FirstName
		a.cacheManager.SaveDeviceMetadata(metadata)
	}

	// Check if they have a new photo for us
	// If their TxPhotoHash differs from what we've received, reply with handshake to trigger them to send
	a.mu.RLock()
	ourReceivedHash := a.receivedPhotoHashes[deviceID]
	a.mu.RUnlock()

	if txPhotoHash != "" && txPhotoHash != ourReceivedHash {
		logger.Debug(prefix, "📸 Remote has new photo (hash: %s), replying with handshake to request it", truncateHash(txPhotoHash, 8))
		// Reply with a handshake that shows we don't have their new photo yet
		// This will trigger them to send it to us
		go a.sendHandshakeMessage(gatt)
	}

	// Check if we need to send our photo
	if rxPhotoHash != a.photoHash {
		logger.Debug(prefix, "📸 Remote doesn't have our photo, sending...")
		go a.sendPhoto(gatt, rxPhotoHash)
	} else {
		logger.Debug(prefix, "⏭️  Remote already has our photo")
	}

	// Check if they have a new profile version
	// If their ProfileVersion differs from what we've received, request ProfileMessage
	a.mu.RLock()
	lastProfileVersion := a.receivedProfileVersion[deviceID]
	a.mu.RUnlock()
	if handshake.ProfileVersion > lastProfileVersion {
		logger.Debug(prefix, "📝 Remote has new profile (version %d > %d), requesting ProfileMessage",
			handshake.ProfileVersion, lastProfileVersion)
		a.mu.Lock()
		a.receivedProfileVersion[deviceID] = handshake.ProfileVersion
		a.mu.Unlock()
		// Request their profile by sending handshake back (they'll see we need it)
		go a.sendHandshakeMessage(gatt)
	}

	// Trigger discovery callback to update GUI with correct deviceID and name
	if a.discoveryCallback != nil {
		name := deviceID[:8]
		if handshake.FirstName != "" {
			name = handshake.FirstName
		}
		a.discoveryCallback(phone.DiscoveredDevice{
			DeviceID:  deviceID,
			Name:      name,
			RSSI:      -50, // Placeholder, actual RSSI not needed for handshake update
			Platform:  "unknown",
			PhotoHash: txPhotoHash,
		})
	}
}

// sendPhoto sends our profile photo to a connected device
func (a *Android) sendPhoto(gatt *kotlin.BluetoothGatt, remoteRxPhotoHash string) error {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
	remoteUUID := gatt.GetRemoteUUID()

	// Check if they already have our photo
	if remoteRxPhotoHash == a.photoHash {
		logger.Debug(prefix, "⏭️  Remote already has our photo, skipping")
		return nil
	}

	// Check if a photo send is already in progress to this device
	a.mu.Lock()
	if a.photoSendInProgress[remoteUUID] {
		a.mu.Unlock()
		logger.Debug(prefix, "⏭️  Photo send already in progress to %s, skipping duplicate", remoteUUID[:8])
		return nil
	}

	// Mark photo send as in progress
	a.photoSendInProgress[remoteUUID] = true
	a.mu.Unlock()
	defer func() {
		// Clear flag when done
		a.mu.Lock()
		delete(a.photoSendInProgress, remoteUUID)
		a.mu.Unlock()
	}()

	// Load our cached photo
	cachePath := fmt.Sprintf("data/%s/cache/my_photo.jpg", a.hardwareUUID)
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

	// Send metadata packet - critical, use withResponse
	metadata := phototransfer.EncodeMetadata(uint32(len(photoData)), totalCRC, uint16(len(chunks)), nil)
	photoChar.Value = metadata
	photoChar.WriteType = kotlin.WRITE_TYPE_DEFAULT
	if !gatt.WriteCharacteristic(photoChar) {
		return fmt.Errorf("failed to send metadata")
	}

	// Send chunks with NO_RESPONSE for speed (fire and forget)
	// This is realistic - photo chunks use fast writes, app-level CRC catches errors
	for i, chunk := range chunks {
		chunkPacket := phototransfer.EncodeChunk(uint16(i), chunk)
		photoChar.Value = chunkPacket
		photoChar.WriteType = kotlin.WRITE_TYPE_NO_RESPONSE
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
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
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
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

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
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

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

	// Get the logical deviceID from the remote UUID
	remoteUUID := a.photoReceiveState.senderDeviceID
	a.mu.Lock()
	deviceID := a.remoteUUIDToDeviceID[remoteUUID]
	a.mu.Unlock()

	// Save photo using cache manager (persists deviceID -> photoHash mapping)
	if err := a.cacheManager.SaveDevicePhoto(deviceID, photoData, hashStr); err != nil {
		logger.Error(prefix, "❌ Failed to save photo: %v", err)
		return err
	}

	logger.Info(prefix, "✅ Photo saved from %s (hash: %s, size: %d bytes)",
		deviceID[:8], hashStr[:8], len(photoData))

	// Update in-memory mapping
	a.mu.Lock()
	a.receivedPhotoHashes[deviceID] = hashStr
	a.mu.Unlock()

	// Notify GUI about the new photo by re-triggering discovery callback
	if a.discoveryCallback != nil {
		a.mu.RLock()
		_, exists := a.connectedGatts[remoteUUID]
		a.mu.RUnlock()
		if exists && deviceID != "" {
			// Get device name from advertising data or use UUID
			name := deviceID[:8]
			if advData, err := a.wire.ReadAdvertisingData(remoteUUID); err == nil && advData != nil {
				if advData.DeviceName != "" {
					name = advData.DeviceName
				}
			}

			a.discoveryCallback(phone.DiscoveredDevice{
				DeviceID:  deviceID,
				Name:      name,
				RSSI:      -55, // Default RSSI, actual value not critical for photo update
				Platform:  "unknown",
				PhotoHash: hashStr,
				PhotoData: photoData,
			})
			logger.Debug(prefix, "🔔 Notified GUI about received photo from %s", deviceID[:8])
		}
	}

	return nil
}

// manualReconnect implements Android's manual reconnect behavior (autoConnect=false)
// Real Android apps must detect disconnect and call connectGatt() again manually
func (a *Android) manualReconnect(deviceUUID string) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	// Wait before retrying (simulates app detecting disconnect and deciding to reconnect)
	time.Sleep(3 * time.Second)

	// Check if we have the device in our discovered list
	a.mu.RLock()
	device, exists := a.discoveredDevices[deviceUUID]
	_, alreadyConnected := a.connectedGatts[deviceUUID]
	a.mu.RUnlock()

	if !exists {
		logger.Debug(prefix, "⚠️  Cannot reconnect to %s: device not in discovered list", deviceUUID[:8])
		return
	}

	// Check if already reconnected
	if alreadyConnected {
		logger.Debug(prefix, "⏭️  Already reconnected to %s", deviceUUID[:8])
		return
	}

	// Manually call connectGatt() again (Android requires this!)
	logger.Info(prefix, "🔄 Manually calling connectGatt() for %s", deviceUUID[:8])
	gatt := device.ConnectGatt(nil, a.useAutoConnect, a)
	a.mu.Lock()
	a.connectedGatts[deviceUUID] = gatt
	a.mu.Unlock()
}

// startStaleHandshakeChecker runs a background task to check for stale handshakes
func (a *Android) startStaleHandshakeChecker() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				a.checkStaleHandshakes()
			case <-a.staleCheckDone:
				return
			}
		}
	}()
}

// checkStaleHandshakes checks all connected devices for stale handshakes and re-handshakes if needed
func (a *Android) checkStaleHandshakes() {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
	now := time.Now()
	staleThreshold := 60 * time.Second

	// Create a snapshot of connected gatts to avoid holding lock during iteration
	a.mu.RLock()
	gattsToCheck := make(map[string]*kotlin.BluetoothGatt)
	for gattUUID, gatt := range a.connectedGatts {
		gattsToCheck[gattUUID] = gatt
	}
	a.mu.RUnlock()

	for gattUUID, gatt := range gattsToCheck {
		a.mu.RLock()
		lastHandshake, exists := a.lastHandshakeTime[gattUUID]
		a.mu.RUnlock()

		// If no handshake record or handshake is stale
		if !exists || now.Sub(lastHandshake) > staleThreshold {
			timeSince := "never"
			if exists {
				timeSince = fmt.Sprintf("%.0fs ago", now.Sub(lastHandshake).Seconds())
			}

			logger.Debug(prefix, "🔄 [STALE-HANDSHAKE] Handshake stale for %s (last: %s), re-handshaking",
				gattUUID[:8], timeSince)

			// Re-handshake in place (no need to reconnect, already connected)
			go a.sendHandshakeMessage(gatt)
		}
	}
}

// loadLocalProfile loads our profile from disk cache
func (a *Android) loadLocalProfile() *LocalProfile {
	cachePath := filepath.Join("data", a.hardwareUUID, "cache", "local_profile.json")
	data, err := os.ReadFile(cachePath)
	if err != nil {
		// No profile yet, return empty
		return &LocalProfile{}
	}

	var profile LocalProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return &LocalProfile{}
	}

	return &profile
}

// saveLocalProfile saves our profile to disk cache
func (a *Android) saveLocalProfile() error {
	cachePath := filepath.Join("data", a.hardwareUUID, "cache", "local_profile.json")
	data, err := json.MarshalIndent(a.localProfile, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(cachePath, data, 0644)
}

// UpdateProfile updates local profile and sends ProfileMessage to all connected devices
func (a *Android) UpdateProfile(profile *LocalProfile) error {
	// Increment profile version on any change
	profile.ProfileVersion++
	a.localProfile = profile

	// Save to disk
	if err := a.saveLocalProfile(); err != nil {
		return err
	}

	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
	logger.Info(prefix, "📝 Updated local profile (version %d)", profile.ProfileVersion)

	// Always send updated handshake (includes first_name and profile_version)
	// Make a safe copy of connected gatts while holding lock
	a.mu.RLock()
	gattsCopy := make([]*kotlin.BluetoothGatt, 0, len(a.connectedGatts))
	for _, g := range a.connectedGatts {
		gattsCopy = append(gattsCopy, g)
	}
	a.mu.RUnlock()

	logger.Debug(prefix, "📤 Sending updated handshake to %d connected device(s)", len(gattsCopy))
	for _, gatt := range gattsCopy {
		go a.sendHandshakeMessage(gatt)
	}

	// Always send ProfileMessage to sync all profile fields
	logger.Debug(prefix, "📤 Sending ProfileMessage to %d connected device(s)", len(gattsCopy))
	for _, gatt := range gattsCopy {
		go a.sendProfileMessage(gatt)
	}

	return nil
}

// sendProfileMessage sends a ProfileMessage to a connected GATT
func (a *Android) sendProfileMessage(gatt *kotlin.BluetoothGatt) error {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	const auraProfileCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5C"

	profileChar := gatt.GetCharacteristic(auraServiceUUID, auraProfileCharUUID)
	if profileChar == nil {
		return fmt.Errorf("profile characteristic not found")
	}

	msg := &proto.ProfileMessage{
		DeviceId:    a.deviceID, // Send logical deviceID
		LastName:    a.localProfile.LastName,
		PhoneNumber: a.localProfile.IMessage, // Phone number = iMessage
		Tagline:     a.localProfile.Tagline,
		Insta:       a.localProfile.Insta,
		Linkedin:    a.localProfile.LinkedIn,
		Youtube:     a.localProfile.YouTube,
		Tiktok:      a.localProfile.TikTok,
		Gmail:       a.localProfile.Gmail,
		Imessage:    a.localProfile.IMessage,
		Whatsapp:    a.localProfile.WhatsApp,
		Signal:      a.localProfile.Signal,
		Telegram:    a.localProfile.Telegram,
	}

	// Marshal to binary protobuf
	data, err := proto2.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal profile: %w", err)
	}

	// Log as JSON for debugging
	marshaler := protojson.MarshalOptions{
		UseProtoNames: true,
	}
	jsonData, _ := marshaler.Marshal(msg)
	logger.Debug(prefix, "📤 TX ProfileMessage (binary protobuf, %d bytes): %s", len(data), string(jsonData))

	profileChar.Value = data
	profileChar.WriteType = kotlin.WRITE_TYPE_DEFAULT
	success := gatt.WriteCharacteristic(profileChar)
	if !success {
		return fmt.Errorf("failed to write profile message")
	}
	return nil
}

// handleProfileMessage processes incoming ProfileMessage
func (a *Android) handleProfileMessage(gatt *kotlin.BluetoothGatt, data []byte) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	var profileMsg proto.ProfileMessage
	if err := proto2.Unmarshal(data, &profileMsg); err != nil {
		logger.Error(prefix, "❌ Failed to parse ProfileMessage: %v", err)
		return
	}

	// Log as JSON for debugging
	marshaler := protojson.MarshalOptions{
		UseProtoNames: true,
	}
	jsonData, _ := marshaler.Marshal(&profileMsg)
	logger.Debug(prefix, "📥 RX ProfileMessage (binary protobuf, %d bytes): %s", len(data), string(jsonData))

	// Load existing metadata or create new
	remoteUUID := gatt.GetRemoteUUID()
	metadata, err := a.cacheManager.LoadDeviceMetadata(remoteUUID)
	if err != nil {
		logger.Error(prefix, "❌ Failed to load device metadata: %v", err)
		return
	}
	if metadata == nil {
		metadata = &phone.DeviceMetadata{
			DeviceID: remoteUUID,
		}
	}

	// Update fields from ProfileMessage
	metadata.LastName = profileMsg.LastName
	metadata.PhoneNumber = profileMsg.PhoneNumber
	metadata.Tagline = profileMsg.Tagline
	metadata.Insta = profileMsg.Insta
	metadata.LinkedIn = profileMsg.Linkedin
	metadata.YouTube = profileMsg.Youtube
	metadata.TikTok = profileMsg.Tiktok
	metadata.Gmail = profileMsg.Gmail
	metadata.IMessage = profileMsg.Imessage
	metadata.WhatsApp = profileMsg.Whatsapp
	metadata.Signal = profileMsg.Signal
	metadata.Telegram = profileMsg.Telegram

	// Save updated metadata
	if err := a.cacheManager.SaveDeviceMetadata(metadata); err != nil {
		logger.Error(prefix, "❌ Failed to save device metadata: %v", err)
		return
	}

	logger.Info(prefix, "✅ Saved profile data for %s", remoteUUID[:8])
}
