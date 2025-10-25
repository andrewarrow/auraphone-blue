package iphone

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/phototransfer"
	"github.com/user/auraphone-blue/proto"
	"github.com/user/auraphone-blue/swift"
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
	mu              sync.Mutex
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

// iPhone represents an iOS device with BLE capabilities
type iPhone struct {
	hardwareUUID         string // Bluetooth hardware UUID (never changes, from testdata/hardware_uuids.txt)
	deviceID             string // Logical device ID (8-char base36, cached to disk)
	deviceName           string
	wire                 *wire.Wire
	cacheManager         *phone.DeviceCacheManager      // Persistent photo storage
	manager              *swift.CBCentralManager
	peripheralManager    *swift.CBPeripheralManager     // Peripheral mode: GATT server + advertising
	discoveryCallback    phone.DeviceDiscoveryCallback
	photoPath            string
	photoHash            string
	photoData            []byte
	localProfile         *LocalProfile                  // Our local profile data
	mu                   sync.RWMutex                   // Protects all maps below
	connectedPeripherals  map[string]*swift.CBPeripheral // peripheral UUID -> peripheral
	peripheralToDeviceID  map[string]string              // peripheral UUID -> logical device ID
	deviceIDToPhotoHash   map[string]string              // deviceID -> their TX photo hash
	receivedPhotoHashes   map[string]string              // deviceID -> RX hash (photos we got from them)
	receivedProfileVersion map[string]int32               // deviceID -> their profile version
	lastHandshakeTime     map[string]time.Time           // deviceID -> last handshake timestamp
	photoSendInProgress   map[string]bool                // peripheral UUID -> true if photo send in progress
	photoReceiveState     map[string]*photoReceiveState  // peripheral UUID -> receive state (central mode)
	photoReceiveStateServer map[string]*photoReceiveState // senderUUID -> receive state for peripheral mode
	staleCheckDone        chan struct{} // Signal channel for stopping background checker
}

// NewIPhone creates a new iPhone instance with a hardware UUID
func NewIPhone(hardwareUUID string) *iPhone {
	deviceName := "iOS Device"

	// Load or generate device ID (8-char base36, persists across app restarts)
	deviceID, err := phone.LoadOrGenerateDeviceID(hardwareUUID)
	if err != nil {
		fmt.Printf("Failed to load/generate device ID: %v\n", err)
		return nil
	}

	ip := &iPhone{
		hardwareUUID:           hardwareUUID,
		deviceID:               deviceID,
		deviceName:             deviceName,
		connectedPeripherals:   make(map[string]*swift.CBPeripheral),
		peripheralToDeviceID:   make(map[string]string),
		deviceIDToPhotoHash:    make(map[string]string),
		receivedPhotoHashes:    make(map[string]string),
		receivedProfileVersion: make(map[string]int32),
		lastHandshakeTime:      make(map[string]time.Time),
		photoSendInProgress:    make(map[string]bool),
		staleCheckDone:         make(chan struct{}),
		photoReceiveState:      make(map[string]*photoReceiveState),
		photoReceiveStateServer: make(map[string]*photoReceiveState),
	}

	// Initialize wire with hardware UUID
	ip.wire = wire.NewWire(hardwareUUID)
	if err := ip.wire.InitializeDevice(); err != nil {
		fmt.Printf("Failed to initialize iOS device: %v\n", err)
		return nil
	}

	// Initialize cache manager with hardware UUID
	ip.cacheManager = phone.NewDeviceCacheManager(hardwareUUID)
	if err := ip.cacheManager.InitializeCache(); err != nil {
		fmt.Printf("Failed to initialize cache: %v\n", err)
		return nil
	}

	// Load existing photo mappings from disk
	ip.loadReceivedPhotoMappings()

	// Load local profile from disk
	ip.localProfile = ip.loadLocalProfile()

	// Setup BLE
	ip.setupBLE()

	// Create manager with hardware UUID
	ip.manager = swift.NewCBCentralManager(ip, hardwareUUID)

	// Initialize peripheral mode (GATT server + advertising)
	ip.initializePeripheralMode()

	return ip
}

// initializePeripheralMode sets up CBPeripheralManager for peripheral role
func (ip *iPhone) initializePeripheralMode() {
	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"
	const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"
	const auraProfileCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5C"

	// Create peripheral manager with wrapper delegate
	delegate := &iPhonePeripheralDelegate{iphone: ip}
	ip.peripheralManager = swift.NewCBPeripheralManager(delegate, ip.hardwareUUID, wire.PlatformIOS, ip.deviceName)

	// Create the Aura service
	service := &swift.CBMutableService{
		UUID:      auraServiceUUID,
		IsPrimary: true,
	}

	// Add characteristics
	textChar := &swift.CBMutableCharacteristic{
		UUID:       auraTextCharUUID,
		Properties: swift.CBCharacteristicPropertyRead | swift.CBCharacteristicPropertyWrite | swift.CBCharacteristicPropertyNotify,
		Permissions: swift.CBAttributePermissionsReadable | swift.CBAttributePermissionsWriteable,
		Service:    service,
	}
	photoChar := &swift.CBMutableCharacteristic{
		UUID:       auraPhotoCharUUID,
		Properties: swift.CBCharacteristicPropertyRead | swift.CBCharacteristicPropertyWrite | swift.CBCharacteristicPropertyNotify,
		Permissions: swift.CBAttributePermissionsReadable | swift.CBAttributePermissionsWriteable,
		Service:    service,
	}
	profileChar := &swift.CBMutableCharacteristic{
		UUID:       auraProfileCharUUID,
		Properties: swift.CBCharacteristicPropertyRead | swift.CBCharacteristicPropertyWrite | swift.CBCharacteristicPropertyNotify,
		Permissions: swift.CBAttributePermissionsReadable | swift.CBAttributePermissionsWriteable,
		Service:    service,
	}

	service.Characteristics = []*swift.CBMutableCharacteristic{textChar, photoChar, profileChar}

	ip.peripheralManager.AddService(service)
}

// setupBLE configures GATT table and advertising data
func (ip *iPhone) setupBLE() {
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

	logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Setup complete - advertising as: %s (deviceID: %s)", ip.deviceName, ip.deviceID)
}

// loadReceivedPhotoMappings loads deviceID->photoHash mappings from disk cache
// This restores knowledge of which photos we've already received from other devices
func (ip *iPhone) loadReceivedPhotoMappings() {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])

	// Scan cache/photos directory for received photos
	photosDir := fmt.Sprintf("data/%s/cache/photos", ip.hardwareUUID)
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
		metadataFiles, err := filepath.Glob(fmt.Sprintf("data/%s/cache/*_metadata.json", ip.hardwareUUID))
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
func (ip *iPhone) Start() {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])

	// Start scanning for peripherals (Central mode)
	go func() {
		time.Sleep(500 * time.Millisecond)
		ip.manager.ScanForPeripherals(nil, nil)
		logger.Info(prefix, "Started scanning for peripherals (Central mode)")
	}()

	// Start advertising (Peripheral mode)
	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	advertisingData := map[string]interface{}{
		"kCBAdvDataLocalName":    ip.deviceName,
		"kCBAdvDataServiceUUIDs": []string{auraServiceUUID},
	}

	ip.peripheralManager.StartAdvertising(advertisingData)
	logger.Info(prefix, "✅ Started peripheral mode (CBPeripheralManager + advertising)")

	// Start periodic stale handshake checker
	ip.startStaleHandshakeChecker()
	logger.Debug(prefix, "Started stale handshake checker (60s threshold, 30s interval)")
}

// Stop stops BLE operations and cleans up resources
func (ip *iPhone) Stop() {
	fmt.Printf("[%s iOS] Stopping BLE operations\n", ip.hardwareUUID[:8])
	// Stop stale handshake checker
	close(ip.staleCheckDone)
	// Future: stop scanning, disconnect, cleanup
}

// SetDiscoveryCallback sets the callback for when devices are discovered
func (ip *iPhone) SetDiscoveryCallback(callback phone.DeviceDiscoveryCallback) {
	ip.discoveryCallback = callback
}

// GetDeviceUUID returns the device's hardware UUID (for BLE operations)
func (ip *iPhone) GetDeviceUUID() string {
	return ip.hardwareUUID
}

// GetDeviceID returns the device's logical ID (8-char base36)
func (ip *iPhone) GetDeviceID() string {
	return ip.deviceID
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

	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
	logger.Debug(prefix, "📱 DISCOVERED device %s (%s)", peripheral.UUID[:8], name)
	logger.Debug(prefix, "   └─ RSSI: %.0f dBm", rssi)
	if txPhotoHash != "" {
		logger.Debug(prefix, "   └─ TX Photo Hash: %s", truncateHash(txPhotoHash, 8))
	} else {
		logger.Debug(prefix, "   └─ TX Photo Hash: (none)")
	}

	// Trigger discovery callback immediately with hardware UUID
	// This ensures the device shows up in GUI right away
	// Will be updated later with logical base36 deviceID after handshake
	if ip.discoveryCallback != nil {
		ip.discoveryCallback(phone.DiscoveredDevice{
			DeviceID:     "", // Will be filled in after handshake
			HardwareUUID: peripheral.UUID,
			Name:         name,
			RSSI:         rssi,
			Platform:     "unknown", // Will be updated after handshake
			PhotoHash:    txPhotoHash,
		})
	}

	// Auto-connect if not already connected AND we should act as Central
	ip.mu.RLock()
	_, alreadyConnected := ip.connectedPeripherals[peripheral.UUID]
	ip.mu.RUnlock()

	if !alreadyConnected {
		// Check if we should act as Central for this device using role negotiation
		if ip.shouldActAsCentral(peripheral.UUID, name) {
			logger.Debug(prefix, "🔌 Connecting to %s (acting as Central)", peripheral.UUID[:8])
			ip.manager.Connect(&peripheral, nil)
		} else {
			logger.Debug(prefix, "⏸️  Not connecting to %s (will act as Peripheral, waiting for them to connect)", peripheral.UUID[:8])
		}
	}
}

// shouldActAsCentral determines if this iPhone should initiate connection to a discovered device
// Returns true if we should connect (act as Central), false if we should wait (act as Peripheral)
// Uses simple hardware UUID comparison regardless of remote platform
func (ip *iPhone) shouldActAsCentral(remoteUUID, remoteName string) bool {
	// Use hardware UUID comparison for all devices
	// Device with LARGER UUID acts as Central (deterministic collision avoidance)
	return ip.hardwareUUID > remoteUUID
}

func (ip *iPhone) DidConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral) {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
	logger.Info(prefix, "✅ Connected to %s", peripheral.UUID[:8])

	// Store peripheral
	ip.mu.Lock()
	ip.connectedPeripherals[peripheral.UUID] = &peripheral
	ip.mu.Unlock()

	// Set self as delegate
	peripheral.Delegate = ip

	// Discover services
	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	peripheral.DiscoverServices([]string{auraServiceUUID})
}

func (ip *iPhone) DidFailToConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, err error) {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
	logger.Error(prefix, "❌ Failed to connect to %s: %v", peripheral.UUID[:8], err)
}

func (ip *iPhone) DidDisconnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, err error) {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
	if err != nil {
		logger.Error(prefix, "❌ Disconnected from %s with error: %v", peripheral.UUID[:8], err)
	} else {
		logger.Info(prefix, "📡 Disconnected from %s (interference/distance)", peripheral.UUID[:8])
	}

	// Stop listening and write queue on the peripheral
	ip.mu.Lock()
	if storedPeripheral, exists := ip.connectedPeripherals[peripheral.UUID]; exists {
		storedPeripheral.StopListening()
		storedPeripheral.StopWriteQueue()
		delete(ip.connectedPeripherals, peripheral.UUID)
	}
	ip.mu.Unlock()

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
	cachePath := fmt.Sprintf("data/%s/cache/my_photo.jpg", ip.hardwareUUID)
	cacheDir := fmt.Sprintf("data/%s/cache", ip.hardwareUUID)
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

	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
	logger.Info(prefix, "📸 Updated profile photo (hash: %s)", truncateHash(photoHash, 8))
	logger.Debug(prefix, "   └─ Cached to disk and broadcasting TX hash in advertising data")

	// Re-send handshake to all connected devices to notify them of the new photo
	ip.mu.RLock()
	peripheralsCopy := make([]*swift.CBPeripheral, 0, len(ip.connectedPeripherals))
	for _, p := range ip.connectedPeripherals {
		peripheralsCopy = append(peripheralsCopy, p)
	}
	ip.mu.RUnlock()

	if len(peripheralsCopy) > 0 {
		logger.Debug(prefix, "   └─ Notifying %d central-mode connected device(s) of photo change", len(peripheralsCopy))
		for _, peripheral := range peripheralsCopy {
			go ip.sendHandshakeMessage(peripheral)
		}
	}

	// Also notify devices connected in peripheral mode (where they are central to our peripheral)
	ip.mu.RLock()
	peripheralModeDevices := make([]string, 0, len(ip.peripheralToDeviceID))
	for uuid := range ip.peripheralToDeviceID {
		peripheralModeDevices = append(peripheralModeDevices, uuid)
	}
	ip.mu.RUnlock()

	if len(peripheralModeDevices) > 0 {
		logger.Debug(prefix, "   └─ Notifying %d peripheral-mode connected device(s) of photo change", len(peripheralModeDevices))
		for _, uuid := range peripheralModeDevices {
			go ip.sendHandshakeToDevice(uuid)
		}
	}

	return nil
}

// GetProfilePhotoHash returns the hash of the current profile photo
func (ip *iPhone) GetProfilePhotoHash() string {
	return ip.photoHash
}

// GetLocalProfile returns the local profile as a map
func (ip *iPhone) GetLocalProfile() map[string]string {
	return map[string]string{
		"first_name": ip.localProfile.FirstName,
		"last_name":  ip.localProfile.LastName,
		"tagline":    ip.localProfile.Tagline,
		"insta":      ip.localProfile.Insta,
		"linkedin":   ip.localProfile.LinkedIn,
		"youtube":    ip.localProfile.YouTube,
		"tiktok":     ip.localProfile.TikTok,
		"gmail":      ip.localProfile.Gmail,
		"imessage":   ip.localProfile.IMessage,
		"whatsapp":   ip.localProfile.WhatsApp,
		"signal":     ip.localProfile.Signal,
		"telegram":   ip.localProfile.Telegram,
	}
}

// UpdateLocalProfile updates the local profile
func (ip *iPhone) UpdateLocalProfile(profile map[string]string) error {
	ip.localProfile.FirstName = profile["first_name"]
	ip.localProfile.LastName = profile["last_name"]
	ip.localProfile.Tagline = profile["tagline"]
	ip.localProfile.Insta = profile["insta"]
	ip.localProfile.LinkedIn = profile["linkedin"]
	ip.localProfile.YouTube = profile["youtube"]
	ip.localProfile.TikTok = profile["tiktok"]
	ip.localProfile.Gmail = profile["gmail"]
	ip.localProfile.IMessage = profile["imessage"]
	ip.localProfile.WhatsApp = profile["whatsapp"]
	ip.localProfile.Signal = profile["signal"]
	ip.localProfile.Telegram = profile["telegram"]

	return ip.UpdateProfile(ip.localProfile)
}

// CBPeripheralDelegate methods

func (ip *iPhone) DidDiscoverServices(peripheral *swift.CBPeripheral, services []*swift.CBService, err error) {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
	if err != nil {
		logger.Error(prefix, "❌ Service discovery failed: %v", err)
		return
	}

	logger.Debug(prefix, "🔍 Discovered %d services", len(services))

	// Discover characteristics for all services
	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"
	const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"
	const auraProfileCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5C"

	for _, service := range services {
		peripheral.DiscoverCharacteristics([]string{auraTextCharUUID, auraPhotoCharUUID, auraProfileCharUUID}, service)
	}
}

func (ip *iPhone) DidDiscoverCharacteristics(peripheral *swift.CBPeripheral, service *swift.CBService, err error) {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
	if err != nil {
		logger.Error(prefix, "❌ Characteristic discovery failed: %v", err)
		return
	}

	logger.Debug(prefix, "🔍 Discovered %d characteristics", len(service.Characteristics))

	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"
	const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"
	const auraProfileCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5C"

	// Enable notifications for characteristics (matches real iOS behavior)
	for _, char := range service.Characteristics {
		if char.UUID == auraTextCharUUID || char.UUID == auraPhotoCharUUID || char.UUID == auraProfileCharUUID {
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
		prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
		logger.Error(prefix, "❌ Write failed for characteristic %s: %v", characteristic.UUID[:8], err)
	}
}

func (ip *iPhone) DidUpdateValueForCharacteristic(peripheral *swift.CBPeripheral, characteristic *swift.CBCharacteristic, err error) {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
	if err != nil {
		logger.Error(prefix, "❌ Read failed: %v", err)
		return
	}

	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"
	const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"
	const auraProfileCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5C"

	// Handle based on characteristic type
	if characteristic.UUID == auraTextCharUUID {
		// Text characteristic is for HandshakeMessage only
		ip.handleHandshakeMessage(peripheral, characteristic.Value)
	} else if characteristic.UUID == auraPhotoCharUUID {
		// Photo characteristic is for photo transfer
		ip.handlePhotoMessage(peripheral, characteristic.Value)
	} else if characteristic.UUID == auraProfileCharUUID {
		// Profile characteristic is for ProfileMessage
		ip.handleProfileMessage(peripheral, characteristic.Value)
	}
}

// sendHandshakeMessage sends a handshake to a connected peripheral
func (ip *iPhone) sendHandshakeMessage(peripheral *swift.CBPeripheral) error {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])

	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"

	// Get the text characteristic
	textChar := peripheral.GetCharacteristic(auraServiceUUID, auraTextCharUUID)
	if textChar == nil {
		return fmt.Errorf("text characteristic not found")
	}

	firstName := ip.localProfile.FirstName
	if firstName == "" {
		firstName = "iOS" // Default if not set
	}

	// Get the deviceID for this peripheral
	ip.mu.RLock()
	deviceID := ip.peripheralToDeviceID[peripheral.UUID]
	rxPhotoHash := ip.receivedPhotoHashes[deviceID]
	ip.mu.RUnlock()

	msg := &proto.HandshakeMessage{
		DeviceId:        ip.deviceID, // Send logical deviceID, not hardware UUID
		FirstName:       firstName,
		ProtocolVersion: 1,
		TxPhotoHash:     hashStringToBytes(ip.photoHash),
		RxPhotoHash:     hashStringToBytes(rxPhotoHash),
		ProfileVersion:  ip.localProfile.ProfileVersion,
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
		// Record handshake timestamp on successful send (indexed by deviceID, not UUID)
		ip.mu.Lock()
		// Use deviceID if we have it, otherwise use peripheral UUID temporarily
		if deviceID != "" {
			ip.lastHandshakeTime[deviceID] = time.Now()
		} else {
			ip.lastHandshakeTime[peripheral.UUID] = time.Now()
		}
		ip.mu.Unlock()
	}
	return err
}

// handleHandshakeMessage processes incoming handshake messages
func (ip *iPhone) handleHandshakeMessage(peripheral *swift.CBPeripheral, data []byte) {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])

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

	// IMPORTANT: Map peripheral.UUID to the logical device_id from handshake
	deviceID := handshake.DeviceId
	if deviceID == "" {
		logger.Error(prefix, "❌ Handshake missing device_id, using peripheral UUID as fallback")
		deviceID = peripheral.UUID
	}
	ip.mu.Lock()
	ip.peripheralToDeviceID[peripheral.UUID] = deviceID
	ip.mu.Unlock()

	// Record handshake timestamp when received
	ip.mu.Lock()
	ip.lastHandshakeTime[deviceID] = time.Now()
	ip.mu.Unlock()

	// Convert photo hashes from bytes to hex strings
	txPhotoHash := hashBytesToString(handshake.TxPhotoHash)
	rxPhotoHash := hashBytesToString(handshake.RxPhotoHash)

	// Store their TX photo hash
	if txPhotoHash != "" {
		ip.mu.Lock()
		ip.deviceIDToPhotoHash[deviceID] = txPhotoHash
		ip.mu.Unlock()
	}

	// Save first_name to device metadata
	if handshake.FirstName != "" {
		metadata, _ := ip.cacheManager.LoadDeviceMetadata(deviceID)
		if metadata == nil {
			metadata = &phone.DeviceMetadata{
				DeviceID: deviceID,
			}
		}
		metadata.FirstName = handshake.FirstName
		ip.cacheManager.SaveDeviceMetadata(metadata)
	}

	// Check if they have a new photo for us
	// If their TxPhotoHash differs from what we've received, reply with handshake to trigger them to send
	ip.mu.RLock()
	ourReceivedHash := ip.receivedPhotoHashes[deviceID]
	ip.mu.RUnlock()

	if txPhotoHash != "" && txPhotoHash != ourReceivedHash {
		logger.Debug(prefix, "📸 Remote has new photo (hash: %s), replying with handshake to request it", truncateHash(txPhotoHash, 8))
		// Reply with a handshake that shows we don't have their new photo yet
		// This will trigger them to send it to us
		go ip.sendHandshakeMessage(peripheral)
	}

	// Check if we need to send our photo
	if rxPhotoHash != ip.photoHash {
		logger.Debug(prefix, "📸 Remote doesn't have our photo, sending...")
		go ip.sendPhoto(peripheral, rxPhotoHash)
	} else {
		logger.Debug(prefix, "⏭️  Remote already has our photo")
	}

	// Check if they have a new profile version
	// If their ProfileVersion differs from what we've received, request ProfileMessage
	ip.mu.RLock()
	lastProfileVersion := ip.receivedProfileVersion[deviceID]
	ip.mu.RUnlock()
	if handshake.ProfileVersion > lastProfileVersion {
		logger.Debug(prefix, "📝 Remote has new profile (version %d > %d), requesting ProfileMessage",
			handshake.ProfileVersion, lastProfileVersion)
		ip.mu.Lock()
		ip.receivedProfileVersion[deviceID] = handshake.ProfileVersion
		ip.mu.Unlock()
		// Request their profile by sending handshake back (they'll see we need it)
		go ip.sendHandshakeMessage(peripheral)
	}

	// Trigger discovery callback to update GUI with correct deviceID and name
	if ip.discoveryCallback != nil {
		name := deviceID[:8]
		if handshake.FirstName != "" {
			name = handshake.FirstName
		}
		ip.discoveryCallback(phone.DiscoveredDevice{
			DeviceID:     deviceID,
			HardwareUUID: peripheral.UUID,
			Name:         name,
			RSSI:         -50, // Placeholder, actual RSSI not needed for handshake update
			Platform:     "unknown",
			PhotoHash:    txPhotoHash,
		})
	}
}

// sendPhoto sends our profile photo to a connected device
func (ip *iPhone) sendPhoto(peripheral *swift.CBPeripheral, remoteRxPhotoHash string) error {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])

	// Check if they already have our photo
	if remoteRxPhotoHash == ip.photoHash {
		logger.Debug(prefix, "⏭️  Remote already has our photo, skipping")
		return nil
	}

	// Check if a photo send is already in progress to this device
	ip.mu.Lock()
	if ip.photoSendInProgress[peripheral.UUID] {
		ip.mu.Unlock()
		logger.Debug(prefix, "⏭️  Photo send already in progress to %s, skipping duplicate", peripheral.UUID[:8])
		return nil
	}

	// Mark photo send as in progress
	ip.photoSendInProgress[peripheral.UUID] = true
	ip.mu.Unlock()
	defer func() {
		// Clear flag when done
		ip.mu.Lock()
		delete(ip.photoSendInProgress, peripheral.UUID)
		ip.mu.Unlock()
	}()

	// Load our cached photo
	cachePath := fmt.Sprintf("data/%s/cache/my_photo.jpg", ip.hardwareUUID)
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
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])

	// Get or create per-device receive state
	ip.mu.Lock()
	state, exists := ip.photoReceiveState[peripheral.UUID]
	if !exists {
		state = &photoReceiveState{
			receivedChunks: make(map[uint16][]byte),
		}
		ip.photoReceiveState[peripheral.UUID] = state
	}
	ip.mu.Unlock()

	// Try to decode metadata
	if len(data) >= phototransfer.MetadataSize {
		meta, remaining, err := phototransfer.DecodeMetadata(data)
		if err == nil {
			// Metadata packet received
			logger.Info(prefix, "📸 Receiving photo from %s (size: %d, CRC: %08X, chunks: %d)",
				peripheral.UUID[:8], meta.TotalSize, meta.TotalCRC, meta.TotalChunks)

			state.mu.Lock()
			state.isReceiving = true
			state.expectedSize = meta.TotalSize
			state.expectedCRC = meta.TotalCRC
			state.expectedChunks = meta.TotalChunks
			state.receivedChunks = make(map[uint16][]byte)
			state.senderDeviceID = peripheral.UUID
			state.buffer = remaining
			state.mu.Unlock()

			if len(remaining) > 0 {
				ip.processPhotoChunks(peripheral.UUID, state)
			}
			return
		}
	}

	// Regular chunk data
	state.mu.Lock()
	isReceiving := state.isReceiving
	if isReceiving {
		state.buffer = append(state.buffer, data...)
	}
	state.mu.Unlock()

	if isReceiving {
		ip.processPhotoChunks(peripheral.UUID, state)
	}
}

// processPhotoChunks processes buffered photo chunks
func (ip *iPhone) processPhotoChunks(peripheralUUID string, state *photoReceiveState) {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])

	state.mu.Lock()
	defer state.mu.Unlock()

	for {
		if len(state.buffer) < phototransfer.ChunkHeaderSize {
			break
		}

		chunk, consumed, err := phototransfer.DecodeChunk(state.buffer)
		if err != nil {
			break
		}

		state.receivedChunks[chunk.Index] = chunk.Data
		state.buffer = state.buffer[consumed:]

		if chunk.Index == 0 || chunk.Index == state.expectedChunks-1 {
			logger.Debug(prefix, "📥 Received chunk %d/%d from %s",
				len(state.receivedChunks), state.expectedChunks, peripheralUUID[:8])
		}

		// Check if complete
		if uint16(len(state.receivedChunks)) == state.expectedChunks {
			ip.reassembleAndSavePhoto(peripheralUUID, state)
			state.isReceiving = false
			// Clean up state
			ip.mu.Lock()
			delete(ip.photoReceiveState, peripheralUUID)
			ip.mu.Unlock()
			break
		}
	}
}

// reassembleAndSavePhoto reassembles chunks and saves the photo
func (ip *iPhone) reassembleAndSavePhoto(peripheralUUID string, state *photoReceiveState) error {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])

	logger.Debug(prefix, "📸 Starting photo reassembly: expected chunks=%d, received=%d",
		state.expectedChunks, len(state.receivedChunks))

	// Reassemble in order
	var photoData []byte
	for i := uint16(0); i < state.expectedChunks; i++ {
		chunk, exists := state.receivedChunks[i]
		if !exists {
			logger.Error(prefix, "❌ Missing chunk %d during reassembly", i)
			return fmt.Errorf("missing chunk %d", i)
		}
		photoData = append(photoData, chunk...)
	}

	logger.Debug(prefix, "   └─ Reassembled %d bytes from %d chunks",
		len(photoData), state.expectedChunks)

	// Verify CRC
	calculatedCRC := phototransfer.CalculateCRC32(photoData)
	if calculatedCRC != state.expectedCRC {
		logger.Error(prefix, "❌ Photo CRC mismatch: expected %08X, got %08X",
			state.expectedCRC, calculatedCRC)
		return fmt.Errorf("CRC mismatch")
	}
	logger.Debug(prefix, "   └─ CRC verified: %08X", calculatedCRC)

	// Calculate hash
	hash := sha256.Sum256(photoData)
	hashStr := hex.EncodeToString(hash[:])
	logger.Debug(prefix, "   └─ Photo hash: %s", hashStr[:8])

	// Get the logical deviceID from the peripheral UUID
	ip.mu.Lock()
	deviceID := ip.peripheralToDeviceID[peripheralUUID]
	ip.mu.Unlock()

	logger.Debug(prefix, "   └─ Saving for deviceID=%s (peripheralUUID=%s)", deviceID[:8], peripheralUUID[:8])

	// Save photo using cache manager (persists deviceID -> photoHash mapping)
	if err := ip.cacheManager.SaveDevicePhoto(deviceID, photoData, hashStr); err != nil {
		logger.Error(prefix, "❌ Failed to save photo: %v", err)
		return err
	}

	// Update in-memory mapping
	ip.mu.Lock()
	ip.receivedPhotoHashes[deviceID] = hashStr
	ip.mu.Unlock()

	logger.Info(prefix, "✅ Photo saved from %s (hash: %s, size: %d bytes)",
		deviceID[:8], hashStr[:8], len(photoData))

	// Notify GUI about the new photo by re-triggering discovery callback
	if ip.discoveryCallback != nil {
		ip.mu.RLock()
		peripheral, exists := ip.connectedPeripherals[peripheralUUID]
		ip.mu.RUnlock()
		if exists && deviceID != "" {
			// Get device name from advertising data or use UUID
			name := deviceID[:8]
			if advData, err := ip.wire.ReadAdvertisingData(peripheralUUID); err == nil && advData != nil {
				if advData.DeviceName != "" {
					name = advData.DeviceName
				}
			}

			ip.discoveryCallback(phone.DiscoveredDevice{
				DeviceID:     deviceID,
				HardwareUUID: peripheralUUID,
				Name:         name,
				RSSI:         -50, // Default RSSI, actual value not critical for photo update
				Platform:     "unknown",
				PhotoHash:    hashStr,
				PhotoData:    photoData,
			})
			logger.Debug(prefix, "🔔 Notified GUI about received photo from %s", peripheral.UUID[:8])
		} else {
			logger.Warn(prefix, "⚠️  No discovery callback or peripheral not found to notify GUI")
		}
	} else {
		logger.Warn(prefix, "⚠️  No discovery callback registered, GUI not notified")
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
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
	now := time.Now()
	staleThreshold := 60 * time.Second

	// Create a snapshot of connected peripherals to avoid holding lock during iteration
	ip.mu.RLock()
	peripheralsToCheck := make(map[string]*swift.CBPeripheral)
	for peripheralUUID, peripheral := range ip.connectedPeripherals {
		peripheralsToCheck[peripheralUUID] = peripheral
	}
	ip.mu.RUnlock()

	for peripheralUUID, peripheral := range peripheralsToCheck {
		// Look up deviceID for this peripheral (lastHandshakeTime is indexed by deviceID)
		ip.mu.RLock()
		deviceID := ip.peripheralToDeviceID[peripheralUUID]
		ip.mu.RUnlock()

		// If we don't have a deviceID yet, use peripheralUUID as fallback
		lookupKey := deviceID
		if lookupKey == "" {
			lookupKey = peripheralUUID
		}

		ip.mu.RLock()
		lastHandshake, exists := ip.lastHandshakeTime[lookupKey]
		ip.mu.RUnlock()

		// If no handshake record or handshake is stale
		if !exists || now.Sub(lastHandshake) > staleThreshold {
			timeSince := "never"
			if exists {
				timeSince = fmt.Sprintf("%.0fs ago", now.Sub(lastHandshake).Seconds())
			}

			logger.Debug(prefix, "🔄 [STALE-HANDSHAKE] Handshake stale for %s (deviceID: %s, last: %s), re-handshaking",
				peripheralUUID[:8], lookupKey[:8], timeSince)

			// Re-handshake in place (no need to reconnect, already connected)
			go ip.sendHandshakeMessage(peripheral)
		}
	}
}

// loadLocalProfile loads our profile from disk cache
func (ip *iPhone) loadLocalProfile() *LocalProfile {
	cachePath := filepath.Join("data", ip.hardwareUUID, "cache", "local_profile.json")
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
func (ip *iPhone) saveLocalProfile() error {
	cachePath := filepath.Join("data", ip.hardwareUUID, "cache", "local_profile.json")
	data, err := json.MarshalIndent(ip.localProfile, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(cachePath, data, 0644)
}

// UpdateProfile updates local profile and sends ProfileMessage to all connected devices
func (ip *iPhone) UpdateProfile(profile *LocalProfile) error {
	// Increment profile version on any change
	profile.ProfileVersion++
	ip.localProfile = profile

	// Save to disk
	if err := ip.saveLocalProfile(); err != nil {
		return err
	}

	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
	logger.Info(prefix, "📝 Updated local profile (version %d)", profile.ProfileVersion)

	// Always send updated handshake (includes first_name and profile_version)
	// Make a safe copy of connected peripherals while holding lock
	ip.mu.RLock()
	peripheralsCopy := make([]*swift.CBPeripheral, 0, len(ip.connectedPeripherals))
	for _, p := range ip.connectedPeripherals {
		peripheralsCopy = append(peripheralsCopy, p)
	}
	ip.mu.RUnlock()

	logger.Debug(prefix, "📤 Sending updated handshake to %d connected device(s)", len(peripheralsCopy))
	for _, peripheral := range peripheralsCopy {
		go ip.sendHandshakeMessage(peripheral)
	}

	// Always send ProfileMessage to sync all profile fields
	logger.Debug(prefix, "📤 Sending ProfileMessage to %d connected device(s)", len(peripheralsCopy))
	for _, peripheral := range peripheralsCopy {
		go ip.sendProfileMessage(peripheral)
	}

	return nil
}

// sendProfileMessage sends a ProfileMessage to a connected peripheral
func (ip *iPhone) sendProfileMessage(peripheral *swift.CBPeripheral) error {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])

	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	const auraProfileCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5C"

	profileChar := peripheral.GetCharacteristic(auraServiceUUID, auraProfileCharUUID)
	if profileChar == nil {
		return fmt.Errorf("profile characteristic not found")
	}

	msg := &proto.ProfileMessage{
		DeviceId:    ip.deviceID, // Send logical deviceID
		LastName:    ip.localProfile.LastName,
		PhoneNumber: ip.localProfile.IMessage, // Phone number = iMessage
		Tagline:     ip.localProfile.Tagline,
		Insta:       ip.localProfile.Insta,
		Linkedin:    ip.localProfile.LinkedIn,
		Youtube:     ip.localProfile.YouTube,
		Tiktok:      ip.localProfile.TikTok,
		Gmail:       ip.localProfile.Gmail,
		Imessage:    ip.localProfile.IMessage,
		Whatsapp:    ip.localProfile.WhatsApp,
		Signal:      ip.localProfile.Signal,
		Telegram:    ip.localProfile.Telegram,
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

	return peripheral.WriteValue(data, profileChar, swift.CBCharacteristicWriteWithResponse)
}

// handleProfileMessage processes incoming ProfileMessage
func (ip *iPhone) handleProfileMessage(peripheral *swift.CBPeripheral, data []byte) {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])

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

	// Get the logical deviceID for this peripheral connection
	ip.mu.RLock()
	deviceID := ip.peripheralToDeviceID[peripheral.UUID]
	ip.mu.RUnlock()

	// If we don't have a deviceID mapping yet, use hardware UUID as fallback
	if deviceID == "" {
		deviceID = peripheral.UUID
		logger.Warn(prefix, "⚠️  No deviceID mapping for %s, using hardware UUID as fallback", peripheral.UUID[:8])
	}

	// Load existing metadata or create new using logical deviceID
	metadata, err := ip.cacheManager.LoadDeviceMetadata(deviceID)
	if err != nil {
		logger.Error(prefix, "❌ Failed to load device metadata: %v", err)
		return
	}
	if metadata == nil {
		metadata = &phone.DeviceMetadata{
			DeviceID: deviceID,
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
	if err := ip.cacheManager.SaveDeviceMetadata(metadata); err != nil {
		logger.Error(prefix, "❌ Failed to save device metadata: %v", err)
		return
	}

	logger.Info(prefix, "✅ Saved profile data for %s (deviceID: %s)", peripheral.UUID[:8], deviceID[:8])
}

// ====================================================================================
// CBPeripheralManagerDelegate implementation (Peripheral mode)
// ====================================================================================

// iPhonePeripheralDelegate wraps iPhone to provide separate delegate for peripheral manager
type iPhonePeripheralDelegate struct {
	iphone *iPhone
}

// DidUpdateState for peripheral manager
func (d *iPhonePeripheralDelegate) DidUpdateState(peripheralManager *swift.CBPeripheralManager) {
	// State updated for peripheral manager
}

// DidStartAdvertising is called when advertising starts
func (d *iPhonePeripheralDelegate) DidStartAdvertising(peripheralManager *swift.CBPeripheralManager, err error) {
	prefix := fmt.Sprintf("%s iOS", d.iphone.hardwareUUID[:8])
	if err != nil {
		logger.Error(prefix, "❌ Failed to start advertising: %v", err)
	} else {
		logger.Debug(prefix, "✅ Peripheral advertising started successfully")
	}
}

// DidReceiveReadRequest is called when a central device reads a characteristic
func (d *iPhonePeripheralDelegate) DidReceiveReadRequest(peripheralManager *swift.CBPeripheralManager, request *swift.CBATTRequest) {
	// For now, we don't support reads - all data is pushed via writes
	peripheralManager.RespondToRequest(request, 0) // 0 = success
}

// DidReceiveWriteRequests is called when a central device writes to characteristics
func (d *iPhonePeripheralDelegate) DidReceiveWriteRequests(peripheralManager *swift.CBPeripheralManager, requests []*swift.CBATTRequest) {
	prefix := fmt.Sprintf("%s iOS", d.iphone.hardwareUUID[:8])

	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"
	const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"
	const auraProfileCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5C"

	for _, request := range requests {
		logger.Debug(prefix, "📥 Peripheral GATT write from %s to char %s (%d bytes)",
			request.Central.UUID[:8], request.Characteristic.UUID[:8], len(request.Value))

		// Get or create GATT connection for this sender
		senderUUID := request.Central.UUID

		// Process based on characteristic - use existing handler methods
		switch request.Characteristic.UUID {
		case auraTextCharUUID:
			// Handshake message - decode and process
			handshake := &proto.HandshakeMessage{}
			if err := proto2.Unmarshal(request.Value, handshake); err == nil {
				deviceID := handshake.DeviceId
				txPhotoHash := hashBytesToString(handshake.TxPhotoHash)

				// Check if we've already handshaked recently (before updating timestamp)
				d.iphone.mu.RLock()
				lastHandshake, alreadyHandshaked := d.iphone.lastHandshakeTime[deviceID]
				d.iphone.mu.RUnlock()

				shouldReply := !alreadyHandshaked || time.Since(lastHandshake) > 5*time.Second

				// Now update our state
				d.iphone.mu.Lock()
				d.iphone.peripheralToDeviceID[senderUUID] = deviceID
				if txPhotoHash != "" {
					d.iphone.deviceIDToPhotoHash[deviceID] = txPhotoHash
				}
				d.iphone.lastHandshakeTime[deviceID] = time.Now()
				d.iphone.mu.Unlock()

				logger.Info(prefix, "📥 RX Handshake from peripheral write: deviceID=%s, firstName=%s", deviceID, handshake.FirstName)

				// Save first_name to device metadata
				if handshake.FirstName != "" {
					metadata, _ := d.iphone.cacheManager.LoadDeviceMetadata(deviceID)
					if metadata == nil {
						metadata = &phone.DeviceMetadata{
							DeviceID: deviceID,
						}
					}
					metadata.FirstName = handshake.FirstName
					d.iphone.cacheManager.SaveDeviceMetadata(metadata)
				}

				// Always trigger discovery callback to update GUI (even if we don't reply)
				// This ensures the GUI updates when first_name changes
				if d.iphone.discoveryCallback != nil {
					name := deviceID[:8]
					if handshake.FirstName != "" {
						name = handshake.FirstName
					}
					d.iphone.discoveryCallback(phone.DiscoveredDevice{
						DeviceID:     deviceID,
						HardwareUUID: senderUUID,
						Name:         name,
						RSSI:         -50,
						Platform:     "unknown",
						PhotoHash:    txPhotoHash,
					})
				}

				// Check if they have a new photo for us
				// If their TxPhotoHash differs from what we've received, reply with handshake to trigger them to send
				d.iphone.mu.RLock()
				ourReceivedHash := d.iphone.receivedPhotoHashes[deviceID]
				d.iphone.mu.RUnlock()

				if txPhotoHash != "" && txPhotoHash != ourReceivedHash {
					logger.Debug(prefix, "📸 Remote has new photo (hash: %s), replying with handshake to request it", truncateHash(txPhotoHash, 8))
					// Reply with a handshake that shows we don't have their new photo yet
					// This will trigger them to send it to us
					shouldReply = true // Force a reply to request the photo
				}

				// Only send handshake back if this is the first time or it's been a while (or if we need their new photo)
				if shouldReply {
					// Send our handshake back
					go d.iphone.sendHandshakeToDevice(senderUUID)
				} else {
					logger.Debug(prefix, "⏭️  Skipping handshake reply to %s (already handshaked recently)", deviceID[:8])
				}

				// Check if we need to send our photo
				rxPhotoHash := hashBytesToString(handshake.RxPhotoHash)
				if rxPhotoHash != d.iphone.photoHash && d.iphone.photoHash != "" {
					logger.Debug(prefix, "📸 Remote doesn't have our photo, sending it via server mode...")
					go d.iphone.sendPhotoToDevice(senderUUID, rxPhotoHash)
				}
			}
		case auraPhotoCharUUID:
			// Photo chunk - copy data before passing to goroutine to avoid race condition
			dataCopy := make([]byte, len(request.Value))
			copy(dataCopy, request.Value)
			go d.iphone.handlePhotoMessageFromUUID(senderUUID, dataCopy)
		case auraProfileCharUUID:
			// Profile message - copy data before passing to goroutine
			dataCopy := make([]byte, len(request.Value))
			copy(dataCopy, request.Value)
			go d.iphone.handleProfileMessageFromUUID(senderUUID, dataCopy)
		}
	}

	// Respond to all requests
	peripheralManager.RespondToRequest(requests[0], 0) // 0 = success
}

func (ip *iPhone) sendHandshakeToDevice(targetUUID string) {
	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"

	firstName := ip.localProfile.FirstName
	if firstName == "" {
		firstName = "iOS"
	}

	msg := &proto.HandshakeMessage{
		DeviceId:        ip.deviceID,
		FirstName:       firstName,
		ProtocolVersion: 1,
		TxPhotoHash:     hashStringToBytes(ip.photoHash),
	}

	data, _ := proto2.Marshal(msg)
	if err := ip.wire.WriteCharacteristic(targetUUID, auraServiceUUID, auraTextCharUUID, data); err != nil {
		prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
		logger.Error(prefix, "❌ Failed to send handshake: %v", err)
	} else {
		prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
		logger.Debug(prefix, "📤 Sent handshake back to %s", targetUUID[:8])
	}
}

// sendPhotoToDevice sends our photo to a device via wire layer (server/peripheral mode)
func (ip *iPhone) sendPhotoToDevice(targetUUID string, remoteRxPhotoHash string) error {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])

	// Check if they already have our photo
	if remoteRxPhotoHash == ip.photoHash {
		logger.Debug(prefix, "⏭️  Remote already has our photo, skipping")
		return nil
	}

	// Check if a photo send is already in progress to this device
	ip.mu.Lock()
	if ip.photoSendInProgress[targetUUID] {
		ip.mu.Unlock()
		logger.Debug(prefix, "⏭️  Photo send already in progress to %s, skipping duplicate", targetUUID[:8])
		return nil
	}

	// Mark photo send as in progress
	ip.photoSendInProgress[targetUUID] = true
	ip.mu.Unlock()
	defer func() {
		// Clear flag when done
		ip.mu.Lock()
		delete(ip.photoSendInProgress, targetUUID)
		ip.mu.Unlock()
	}()

	// Load our cached photo
	cachePath := fmt.Sprintf("data/%s/cache/my_photo.jpg", ip.hardwareUUID)
	photoData, err := os.ReadFile(cachePath)
	if err != nil {
		return fmt.Errorf("failed to load photo: %w", err)
	}

	// Calculate total CRC
	totalCRC := phototransfer.CalculateCRC32(photoData)

	// Split into chunks
	chunks := phototransfer.SplitIntoChunks(photoData, phototransfer.DefaultChunkSize)

	logger.Info(prefix, "📸 Sending photo to %s via server mode (%d bytes, %d chunks, CRC: %08X)",
		targetUUID[:8], len(photoData), len(chunks), totalCRC)

	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"

	// Send metadata packet
	metadata := phototransfer.EncodeMetadata(uint32(len(photoData)), totalCRC, uint16(len(chunks)), nil)
	if err := ip.wire.WriteCharacteristic(targetUUID, auraServiceUUID, auraPhotoCharUUID, metadata); err != nil {
		return fmt.Errorf("failed to send metadata: %w", err)
	}

	// Send chunks
	for i, chunk := range chunks {
		chunkPacket := phototransfer.EncodeChunk(uint16(i), chunk)
		if err := ip.wire.WriteCharacteristic(targetUUID, auraServiceUUID, auraPhotoCharUUID, chunkPacket); err != nil {
			return fmt.Errorf("failed to send chunk %d: %w", i, err)
		}
		time.Sleep(10 * time.Millisecond)

		if i == 0 || i == len(chunks)-1 {
			logger.Debug(prefix, "📤 Sent chunk %d/%d via server mode", i+1, len(chunks))
		}
	}

	logger.Info(prefix, "✅ Photo send complete to %s via server mode", targetUUID[:8])
	return nil
}

func (ip *iPhone) handlePhotoMessageFromUUID(senderUUID string, data []byte) {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
	logger.Debug(prefix, "📥 Photo message from GATT server %s (%d bytes)", senderUUID[:8], len(data))

	// Get or create photo receive state for this sender
	ip.mu.Lock()
	state, exists := ip.photoReceiveStateServer[senderUUID]
	if !exists {
		state = &photoReceiveState{
			receivedChunks: make(map[uint16][]byte),
		}
		ip.photoReceiveStateServer[senderUUID] = state
	}
	ip.mu.Unlock()

	// Try to decode metadata
	if len(data) >= phototransfer.MetadataSize {
		meta, remaining, err := phototransfer.DecodeMetadata(data)
		if err == nil {
			// Metadata packet received
			logger.Info(prefix, "📸 Receiving photo from %s (size: %d, CRC: %08X, chunks: %d)",
				senderUUID[:8], meta.TotalSize, meta.TotalCRC, meta.TotalChunks)

			state.mu.Lock()
			state.isReceiving = true
			state.expectedSize = meta.TotalSize
			state.expectedCRC = meta.TotalCRC
			state.expectedChunks = meta.TotalChunks
			state.receivedChunks = make(map[uint16][]byte)
			state.senderDeviceID = senderUUID
			state.buffer = remaining
			state.mu.Unlock()

			if len(remaining) > 0 {
				ip.processPhotoChunksFromServer(senderUUID, state)
			}
			return
		}
	}

	// Regular chunk data
	state.mu.Lock()
	isReceiving := state.isReceiving
	if isReceiving {
		state.buffer = append(state.buffer, data...)
	}
	state.mu.Unlock()

	if isReceiving {
		ip.processPhotoChunksFromServer(senderUUID, state)
	}
}

// processPhotoChunksFromServer processes buffered photo chunks from peripheral mode
func (ip *iPhone) processPhotoChunksFromServer(senderUUID string, state *photoReceiveState) {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])

	state.mu.Lock()
	defer state.mu.Unlock()

	for {
		if len(state.buffer) < phototransfer.ChunkHeaderSize {
			break
		}

		chunk, consumed, err := phototransfer.DecodeChunk(state.buffer)
		if err != nil {
			break
		}

		state.receivedChunks[chunk.Index] = chunk.Data
		state.buffer = state.buffer[consumed:]

		if chunk.Index == 0 || chunk.Index == state.expectedChunks-1 {
			logger.Debug(prefix, "📥 Received chunk %d/%d from %s",
				len(state.receivedChunks), state.expectedChunks, senderUUID[:8])
		}

		// Check if complete
		if uint16(len(state.receivedChunks)) == state.expectedChunks {
			ip.reassembleAndSavePhotoFromServer(senderUUID, state)
			state.isReceiving = false
			// Clean up state
			ip.mu.Lock()
			delete(ip.photoReceiveStateServer, senderUUID)
			ip.mu.Unlock()
			break
		} else if chunk.Index == state.expectedChunks-1 {
			// Received last chunk but still incomplete - log missing chunks
			var missing []uint16
			for i := uint16(0); i < state.expectedChunks; i++ {
				if _, exists := state.receivedChunks[i]; !exists {
					missing = append(missing, i)
				}
			}
			logger.Warn(prefix, "⚠️  Received final chunk %d but only have %d/%d chunks. Missing: %v",
				chunk.Index, len(state.receivedChunks), state.expectedChunks, missing)
		}
	}
}

// reassembleAndSavePhotoFromServer reassembles chunks and saves the photo from peripheral mode
func (ip *iPhone) reassembleAndSavePhotoFromServer(senderUUID string, state *photoReceiveState) error {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])

	logger.Debug(prefix, "📸 Starting photo reassembly from server: expected chunks=%d, received=%d",
		state.expectedChunks, len(state.receivedChunks))

	// Reassemble in order
	var photoData []byte
	for i := uint16(0); i < state.expectedChunks; i++ {
		chunk, exists := state.receivedChunks[i]
		if !exists {
			logger.Error(prefix, "❌ Missing chunk %d during reassembly from %s", i, senderUUID[:8])
			return fmt.Errorf("missing chunk %d", i)
		}
		photoData = append(photoData, chunk...)
	}

	logger.Debug(prefix, "   └─ Reassembled %d bytes from %d chunks",
		len(photoData), state.expectedChunks)

	// Verify CRC
	calculatedCRC := phototransfer.CalculateCRC32(photoData)
	if calculatedCRC != state.expectedCRC {
		logger.Error(prefix, "❌ Photo CRC mismatch from %s: expected %08X, got %08X",
			senderUUID[:8], state.expectedCRC, calculatedCRC)
		return fmt.Errorf("CRC mismatch")
	}
	logger.Debug(prefix, "   └─ CRC verified: %08X", calculatedCRC)

	// Calculate hash
	hash := sha256.Sum256(photoData)
	hashStr := hex.EncodeToString(hash[:])
	logger.Debug(prefix, "   └─ Photo hash: %s", hashStr[:8])

	// Get the logical deviceID from the sender UUID
	// For peripheral mode, we need to look this up from our cached handshakes
	ip.mu.Lock()
	deviceID := ""
	// First try peripheralToDeviceID mapping (if they connected to us as central)
	if id, exists := ip.peripheralToDeviceID[senderUUID]; exists {
		deviceID = id
		logger.Debug(prefix, "   └─ Found deviceID from peripheralToDeviceID: %s", deviceID[:8])
	} else {
		// If not found, scan through lastHandshakeTime to find the device that matches this UUID
		// This happens when we're the peripheral and they're the central writing to us
		// In this case, we should have received a handshake from them via write request
		// For now, use senderUUID as fallback (will be mapped later on next handshake)
		deviceID = senderUUID // Temporary until we get proper device ID from handshake
		logger.Warn(prefix, "   └─ DeviceID not found for %s, using UUID as fallback", senderUUID[:8])
	}
	ip.mu.Unlock()

	logger.Debug(prefix, "   └─ Saving for deviceID=%s (senderUUID=%s)", deviceID[:8], senderUUID[:8])

	// Save photo using cache manager (persists deviceID -> photoHash mapping)
	if err := ip.cacheManager.SaveDevicePhoto(deviceID, photoData, hashStr); err != nil {
		logger.Error(prefix, "❌ Failed to save photo from %s: %v", senderUUID[:8], err)
		return err
	}

	// Update in-memory mapping
	ip.mu.Lock()
	ip.receivedPhotoHashes[deviceID] = hashStr
	ip.mu.Unlock()

	logger.Info(prefix, "✅ Photo saved from %s (hash: %s, size: %d bytes)",
		senderUUID[:8], hashStr[:8], len(photoData))

	// Notify GUI about the new photo by re-triggering discovery callback
	if ip.discoveryCallback != nil && deviceID != "" {
		// Get device name from advertising data or use device ID
		name := deviceID[:8]
		if len(deviceID) == 8 { // It's a proper device ID
			// Try to get first name from metadata
			if metadata, err := ip.cacheManager.LoadDeviceMetadata(deviceID); err == nil && metadata != nil && metadata.FirstName != "" {
				name = metadata.FirstName
			}
		}

		ip.discoveryCallback(phone.DiscoveredDevice{
			DeviceID:     deviceID,
			HardwareUUID: senderUUID,
			Name:         name,
			RSSI:         -50, // Default RSSI, actual value not critical for photo update
			Platform:     "unknown",
			PhotoHash:    hashStr,
			PhotoData:    photoData,
		})
		logger.Debug(prefix, "🔔 Notified GUI about received photo from %s", senderUUID[:8])
	} else {
		logger.Warn(prefix, "⚠️  Cannot notify GUI: discoveryCallback=%v, deviceID=%s",
			ip.discoveryCallback != nil, deviceID[:8])
	}

	return nil
}

func (ip *iPhone) handleProfileMessageFromUUID(senderUUID string, data []byte) {
	// Reuse existing profile handling logic - simplified
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
	logger.Debug(prefix, "📥 Profile message from %s (%d bytes)", senderUUID[:8], len(data))
	// TODO: Implement profile handling
}

// CentralDidSubscribe is called when a central subscribes to notifications
func (d *iPhonePeripheralDelegate) CentralDidSubscribe(peripheralManager *swift.CBPeripheralManager, central swift.CBCentral, characteristic *swift.CBMutableCharacteristic) {
	prefix := fmt.Sprintf("%s iOS", d.iphone.hardwareUUID[:8])
	logger.Debug(prefix, "📥 Central %s subscribed to char %s", central.UUID[:8], characteristic.UUID[:8])
}

// CentralDidUnsubscribe is called when a central unsubscribes from notifications
func (d *iPhonePeripheralDelegate) CentralDidUnsubscribe(peripheralManager *swift.CBPeripheralManager, central swift.CBCentral, characteristic *swift.CBMutableCharacteristic) {
	prefix := fmt.Sprintf("%s iOS", d.iphone.hardwareUUID[:8])
	logger.Debug(prefix, "📥 Central %s unsubscribed from char %s", central.UUID[:8], characteristic.UUID[:8])
}
