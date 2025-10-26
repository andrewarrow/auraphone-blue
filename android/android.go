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
	mu              sync.Mutex
	isReceiving     bool
	expectedSize    uint32
	expectedCRC     uint32
	expectedChunks  uint16
	receivedChunks  map[uint16][]byte
	senderDeviceID  string
	buffer          []byte
	lastChunkTime   time.Time  // Time of last chunk received (for timeout detection)
	retransmitCount int        // Number of retransmit requests sent
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
	advertiser           *kotlin.BluetoothLeAdvertiser      // Peripheral mode: advertising + inbox polling
	discoveryCallback    phone.DeviceDiscoveryCallback
	photoPath            string
	photoHash            string
	photoData            []byte
	localProfile         *LocalProfile                      // Our local profile data
	mu                     sync.RWMutex                       // Protects all maps below
	connectedGatts         map[string]*kotlin.BluetoothGatt   // remote UUID -> GATT connection (devices we connected to as Central)
	connectedCentrals      map[string]bool                    // remote UUID -> true (devices that connected to us as Peripheral)
	discoveredDevices      map[string]*kotlin.BluetoothDevice // remote UUID -> discovered device (for reconnect)
	remoteUUIDToDeviceID   map[string]string                  // hardware UUID -> logical device ID
	deviceIDToPhotoHash    map[string]string                  // deviceID -> their TX photo hash
	receivedPhotoHashes    map[string]string                  // deviceID -> RX hash (photos we got from them)
	receivedProfileVersion map[string]int32                   // deviceID -> their profile version
	lastHandshakeTime      map[string]time.Time               // hardware UUID -> last handshake timestamp (connection-scoped)
	photoSendInProgress    map[string]bool                    // hardware UUID -> true if photo send in progress (connection-scoped)
	photoReceiveState      map[string]*photoReceiveState      // remote UUID -> receive state (central mode)
	photoReceiveStateServer map[string]*photoReceiveState     // senderUUID -> receive state for server mode
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
		connectedCentrals:      make(map[string]bool),
		discoveredDevices:      make(map[string]*kotlin.BluetoothDevice),
		remoteUUIDToDeviceID:   make(map[string]string),
		deviceIDToPhotoHash:    make(map[string]string),
		receivedPhotoHashes:    make(map[string]string),
		receivedProfileVersion: make(map[string]int32),
		lastHandshakeTime:      make(map[string]time.Time),
		photoSendInProgress:    make(map[string]bool),
		staleCheckDone:         make(chan struct{}),
		photoReceiveState:      make(map[string]*photoReceiveState),
		photoReceiveStateServer: make(map[string]*photoReceiveState),
		useAutoConnect: false, // Default: manual reconnect (matches real Android apps)
	}

	// Initialize wire with hardware UUID and device name
	a.wire = wire.NewWireWithPlatform(hardwareUUID, wire.PlatformAndroid, deviceName, nil)
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

	// Initialize peripheral mode (GATT server + advertiser)
	a.initializePeripheralMode()

	return a
}

// androidGattServerDelegate wraps Android to provide separate delegate for GATT server
type androidGattServerDelegate struct {
	android *Android
}

// initializePeripheralMode sets up advertiser and GATT server for peripheral role
func (a *Android) initializePeripheralMode() {
	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"
	const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"
	const auraProfileCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5C"

	// Create GATT server with wrapper callback, passing shared wire
	delegate := &androidGattServerDelegate{android: a}
	gattServer := kotlin.NewBluetoothGattServer(a.hardwareUUID, delegate, wire.PlatformAndroid, a.deviceName, a.wire)

	// Add the Aura service
	service := &kotlin.BluetoothGattService{
		UUID: auraServiceUUID,
		Type: kotlin.SERVICE_TYPE_PRIMARY,
	}

	// Add characteristics (no Permissions field, it's server-side so properties are enough)
	textChar := &kotlin.BluetoothGattCharacteristic{
		UUID:       auraTextCharUUID,
		Properties: kotlin.PROPERTY_READ | kotlin.PROPERTY_WRITE | kotlin.PROPERTY_NOTIFY,
	}
	photoChar := &kotlin.BluetoothGattCharacteristic{
		UUID:       auraPhotoCharUUID,
		Properties: kotlin.PROPERTY_READ | kotlin.PROPERTY_WRITE | kotlin.PROPERTY_NOTIFY,
	}
	profileChar := &kotlin.BluetoothGattCharacteristic{
		UUID:       auraProfileCharUUID,
		Properties: kotlin.PROPERTY_READ | kotlin.PROPERTY_WRITE | kotlin.PROPERTY_NOTIFY,
	}

	service.Characteristics = append(service.Characteristics, textChar, photoChar, profileChar)
	gattServer.AddService(service)

	// Create advertiser, passing shared wire
	a.advertiser = kotlin.NewBluetoothLeAdvertiser(a.hardwareUUID, wire.PlatformAndroid, a.deviceName, a.wire)
	a.advertiser.SetGattServer(gattServer)
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
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	// Start scanning for devices (Central mode)
	go func() {
		time.Sleep(500 * time.Millisecond)
		scanner := a.manager.Adapter.GetBluetoothLeScanner()
		scanner.StartScan(a)
		logger.Info(prefix, "Started scanning for devices (Central mode)")
	}()

	// Start advertising and GATT server (Peripheral mode)
	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	settings := &kotlin.AdvertiseSettings{
		AdvertiseMode: kotlin.ADVERTISE_MODE_LOW_LATENCY,
		Connectable:   true,
		Timeout:       0,
		TxPowerLevel:  kotlin.ADVERTISE_TX_POWER_MEDIUM,
	}

	advertiseData := &kotlin.AdvertiseData{
		ServiceUUIDs:        []string{auraServiceUUID},
		IncludeDeviceName:   true,
		IncludeTxPowerLevel: true,
	}

	a.advertiser.StartAdvertising(settings, advertiseData, nil, a)
	logger.Info(prefix, "✅ Started peripheral mode (GATT server + advertising)")

	// Start periodic stale handshake checker
	a.startStaleHandshakeChecker()
	logger.Debug(prefix, "Started stale handshake checker (60s threshold, 30s interval)")

	// Start periodic stale photo transfer checker
	a.startStalePhotoTransferChecker()
	logger.Debug(prefix, "Started stale photo transfer checker (5s timeout, 2s interval)")
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

	// Trigger discovery callback immediately with hardware UUID
	// This ensures the device shows up in GUI right away
	// Will be updated later with logical base36 deviceID after handshake
	if a.discoveryCallback != nil {
		a.discoveryCallback(phone.DiscoveredDevice{
			DeviceID:     "", // Will be filled in after handshake
			HardwareUUID: result.Device.Address,
			Name:         name,
			RSSI:         rssi,
			Platform:     "unknown", // Will be updated after handshake
			PhotoHash:    "",        // Will be filled in after handshake
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
// Uses simple hardware UUID comparison regardless of remote platform
func (a *Android) shouldActAsCentral(remoteUUID, remoteName string) bool {
	// Use hardware UUID comparison for all devices
	// Device with LARGER UUID acts as Central (deterministic collision avoidance)
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
		logger.Debug(prefix, "   └─ Notifying %d central-mode connected device(s) of photo change", len(gattsCopy))
		for _, gatt := range gattsCopy {
			go a.sendHandshakeMessage(gatt)
		}
	}

	// Also notify devices connected in peripheral mode (where they are central to our peripheral)
	a.mu.RLock()
	peripheralModeDevices := make([]string, 0, len(a.connectedCentrals))
	for uuid := range a.connectedCentrals {
		peripheralModeDevices = append(peripheralModeDevices, uuid)
	}
	a.mu.RUnlock()

	if len(peripheralModeDevices) > 0 {
		logger.Debug(prefix, "   └─ Notifying %d peripheral-mode connected device(s) of photo change", len(peripheralModeDevices))
		for _, uuid := range peripheralModeDevices {
			go a.sendHandshakeToDevice(uuid)
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
		// IMPORTANT: The simulator does NOT auto-reconnect for the app anymore!
		// The app code in this file must implement its own reconnection logic.
		if a.useAutoConnect {
			logger.Info(prefix, "🔄 Android autoConnect=true: Will retry in background...")
			// Auto-reconnect is handled by BluetoothGatt.attemptReconnect() in kotlin layer
		} else {
			logger.Warn(prefix, "❌ Android autoConnect=false: App must manually reconnect (disconnected from %s)", remoteUUID[:8])
			// REALISTIC BEHAVIOR: App stays disconnected until it manually calls connectGatt()
			// If you want reconnection, implement it in the app layer (this file)
			// Example: go a.manualReconnect(remoteUUID)
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
		// Record handshake timestamp on successful send (indexed by hardware UUID for connection-scoped state)
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
	logger.Debug(prefix, "🔗 [UUID-MAP] Mapped hardwareUUID=%s → deviceID=%s", remoteUUID[:8], deviceID[:8])
	a.mu.Unlock()

	// Record handshake timestamp when received (indexed by hardware UUID for connection-scoped state)
	a.mu.Lock()
	a.lastHandshakeTime[remoteUUID] = time.Now()
	a.mu.Unlock()

	// Convert photo hashes from bytes to hex strings
	txPhotoHash := hashBytesToString(handshake.TxPhotoHash)
	rxPhotoHash := hashBytesToString(handshake.RxPhotoHash)

	logger.Debug(prefix, "🤝 [HANDSHAKE-RX] Received from %s: txHash=%s, rxHash=%s, firstName=%s",
		remoteUUID[:8], truncateHash(txPhotoHash, 8), truncateHash(rxPhotoHash, 8), handshake.FirstName)

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

	logger.Debug(prefix, "📸 [PHOTO-DECISION-RX] Remote txHash=%s, we have received=%s, needReceive=%v",
		truncateHash(txPhotoHash, 8), truncateHash(ourReceivedHash, 8), txPhotoHash != "" && txPhotoHash != ourReceivedHash)

	if txPhotoHash != "" && txPhotoHash != ourReceivedHash {
		logger.Debug(prefix, "📸 Remote has new photo (hash: %s), replying with handshake to request it", truncateHash(txPhotoHash, 8))
		// Reply with a handshake that shows we don't have their new photo yet
		// This will trigger them to send it to us
		go a.sendHandshakeMessage(gatt)
	}

	// Check if we need to send our photo
	logger.Debug(prefix, "📸 [PHOTO-DECISION-TX] Remote rxHash=%s, our photoHash=%s, needSend=%v",
		truncateHash(rxPhotoHash, 8), truncateHash(a.photoHash, 8), rxPhotoHash != a.photoHash)

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
			DeviceID:     deviceID,
			HardwareUUID: remoteUUID,
			Name:         name,
			RSSI:         -50, // Placeholder, actual RSSI not needed for handshake update
			Platform:     "unknown",
			PhotoHash:    txPhotoHash,
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
		logger.Debug(prefix, "⏭️  [PHOTO-TX-STATE] Already in progress to %s, skipping duplicate", remoteUUID[:8])
		return nil
	}

	// Mark photo send as in progress
	a.photoSendInProgress[remoteUUID] = true
	logger.Debug(prefix, "📸 [PHOTO-TX-STATE] IDLE → SENDING to %s", remoteUUID[:8])
	a.mu.Unlock()
	defer func() {
		// Clear flag when done
		a.mu.Lock()
		delete(a.photoSendInProgress, remoteUUID)
		logger.Debug(prefix, "📸 [PHOTO-TX-STATE] Cleared send-in-progress flag for %s", remoteUUID[:8])
		a.mu.Unlock()
	}()

	// Load our cached photo
	cachePath := fmt.Sprintf("data/%s/cache/my_photo.jpg", a.hardwareUUID)
	logger.Debug(prefix, "📸 [PHOTO-TX-LOAD] Loading photo from %s", cachePath)
	photoData, err := os.ReadFile(cachePath)
	if err != nil {
		logger.Error(prefix, "❌ [PHOTO-TX-ERROR] Failed to load photo: %v", err)
		return fmt.Errorf("failed to load photo: %w", err)
	}

	// Calculate total CRC
	totalCRC := phototransfer.CalculateCRC32(photoData)

	// Split into chunks
	chunks := phototransfer.SplitIntoChunks(photoData, phototransfer.DefaultChunkSize)

	logger.Info(prefix, "📸 Sending photo to %s (%d bytes, %d chunks, CRC: %08X)",
		remoteUUID[:8], len(photoData), len(chunks), totalCRC)
	logger.Debug(prefix, "📸 [PHOTO-TX-STATE] Loaded photo: size=%d, chunks=%d, crc=%08X",
		len(photoData), len(chunks), totalCRC)

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
	logger.Debug(prefix, "📸 [PHOTO-TX-META] Sending metadata packet (size=%d, crc=%08X, chunks=%d) with response",
		len(photoData), totalCRC, len(chunks))
	if !gatt.WriteCharacteristic(photoChar) {
		logger.Error(prefix, "❌ [PHOTO-TX-ERROR] Failed to send metadata")
		return fmt.Errorf("failed to send metadata")
	}
	logger.Debug(prefix, "📸 [PHOTO-TX-META] Metadata sent successfully, starting chunk stream")

	// Send chunks with NO_RESPONSE for speed (fire and forget)
	// This is realistic - photo chunks use fast writes, app-level CRC catches errors
	for i, chunk := range chunks {
		chunkPacket := phototransfer.EncodeChunk(uint16(i), chunk)
		photoChar.Value = chunkPacket
		photoChar.WriteType = kotlin.WRITE_TYPE_NO_RESPONSE
		if !gatt.WriteCharacteristic(photoChar) {
			logger.Warn(prefix, "❌ [PHOTO-TX-ERROR] Failed to send chunk %d/%d to %s", i+1, len(chunks), remoteUUID[:8])
			return fmt.Errorf("failed to send chunk %d", i)
		}
		time.Sleep(10 * time.Millisecond)

		if i == 0 || i == len(chunks)-1 {
			logger.Debug(prefix, "📤 [PHOTO-TX-CHUNK] Sent chunk %d/%d (size=%d bytes)", i+1, len(chunks), len(chunk))
		}
	}

	logger.Debug(prefix, "📸 [PHOTO-TX-STATE] SENDING → COMPLETE (all %d chunks sent to %s)", len(chunks), remoteUUID[:8])
	logger.Info(prefix, "✅ Photo send complete to %s", remoteUUID[:8])
	return nil
}

// handlePhotoMessage processes incoming photo data
func (a *Android) handlePhotoMessage(gatt *kotlin.BluetoothGatt, data []byte) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
	remoteUUID := gatt.GetRemoteUUID()

	// Get or create per-device receive state
	a.mu.Lock()
	state, exists := a.photoReceiveState[remoteUUID]
	if !exists {
		state = &photoReceiveState{
			receivedChunks: make(map[uint16][]byte),
		}
		a.photoReceiveState[remoteUUID] = state
	}
	a.mu.Unlock()

	// Try to decode metadata
	if len(data) >= phototransfer.MetadataSize {
		meta, remaining, err := phototransfer.DecodeMetadata(data)
		if err == nil {
			// Metadata packet received
			logger.Info(prefix, "📸 Receiving photo from %s (size: %d, CRC: %08X, chunks: %d)",
				remoteUUID[:8], meta.TotalSize, meta.TotalCRC, meta.TotalChunks)

			state.mu.Lock()
			logger.Debug(prefix, "📸 [PHOTO-RX-STATE] IDLE → RECEIVING from %s (size=%d, chunks=%d, crc=%08X)",
				remoteUUID[:8], meta.TotalSize, meta.TotalChunks, meta.TotalCRC)
			state.isReceiving = true
			state.expectedSize = meta.TotalSize
			state.expectedCRC = meta.TotalCRC
			state.expectedChunks = meta.TotalChunks
			state.receivedChunks = make(map[uint16][]byte)
			state.senderDeviceID = remoteUUID
			state.buffer = remaining
			state.lastChunkTime = time.Now() // Initialize timeout tracking
			state.mu.Unlock()

			if len(remaining) > 0 {
				logger.Debug(prefix, "📸 [PHOTO-RX-STATE] Metadata has %d bytes trailing data, processing immediately", len(remaining))
				a.processPhotoChunks(remoteUUID, state)
			}
			return
		}
	}

	// Regular chunk data
	state.mu.Lock()
	isReceiving := state.isReceiving
	if isReceiving {
		bufferBefore := len(state.buffer)
		state.buffer = append(state.buffer, data...)
		logger.Debug(prefix, "📸 [PHOTO-RX-DATA] Appended %d bytes to buffer (buffer now %d bytes, receiving=%v)",
			len(data), len(state.buffer), isReceiving)
		if bufferBefore == 0 && len(state.buffer) > 0 {
			logger.Debug(prefix, "📸 [PHOTO-RX-STATE] Buffer now has data, will process chunks")
		}
	}
	state.mu.Unlock()

	if isReceiving {
		a.processPhotoChunks(remoteUUID, state)
	}
}

// processPhotoChunks processes buffered photo chunks
func (a *Android) processPhotoChunks(remoteUUID string, state *photoReceiveState) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	state.mu.Lock()
	defer state.mu.Unlock()

	logger.Debug(prefix, "📸 [PHOTO-RX-PARSE] Processing buffer with %d bytes (need %d for header)",
		len(state.buffer), phototransfer.ChunkHeaderSize)

	for {
		if len(state.buffer) < phototransfer.ChunkHeaderSize {
			logger.Debug(prefix, "📸 [PHOTO-RX-PARSE] Buffer too small (%d bytes), waiting for more data", len(state.buffer))
			break
		}

		chunk, consumed, err := phototransfer.DecodeChunk(state.buffer)
		if err != nil {
			logger.Debug(prefix, "📸 [PHOTO-RX-PARSE] DecodeChunk failed: %v (buffer len=%d)", err, len(state.buffer))
			break
		}

		state.receivedChunks[chunk.Index] = chunk.Data
		state.buffer = state.buffer[consumed:]
		state.lastChunkTime = time.Now() // Update last chunk time

		logger.Debug(prefix, "📸 [PHOTO-RX-CHUNK] Chunk[%d] received, size=%d (have %d/%d chunks, buffer remaining=%d)",
			chunk.Index, len(chunk.Data), len(state.receivedChunks), state.expectedChunks, len(state.buffer))

		if chunk.Index == 0 || chunk.Index == state.expectedChunks-1 {
			logger.Debug(prefix, "📥 [PHOTO-RX-MILESTONE] Received chunk %d/%d from %s",
				len(state.receivedChunks), state.expectedChunks, remoteUUID[:8])
		}

		// Check if complete
		if uint16(len(state.receivedChunks)) == state.expectedChunks {
			logger.Debug(prefix, "📸 [PHOTO-RX-STATE] CHUNK_ACCUMULATING → COMPLETE (all %d chunks received)", state.expectedChunks)
			a.reassembleAndSavePhoto(remoteUUID, state)
			state.isReceiving = false
			// Clean up state
			a.mu.Lock()
			delete(a.photoReceiveState, remoteUUID)
			a.mu.Unlock()
			break
		}
	}
}

// reassembleAndSavePhoto reassembles chunks and saves the photo
func (a *Android) reassembleAndSavePhoto(remoteUUID string, state *photoReceiveState) error {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	logger.Debug(prefix, "📸 [PHOTO-RX-STATE] COMPLETE → REASSEMBLING (expected chunks=%d, received=%d)",
		state.expectedChunks, len(state.receivedChunks))

	// Reassemble in order
	var photoData []byte
	for i := uint16(0); i < state.expectedChunks; i++ {
		chunk, exists := state.receivedChunks[i]
		if !exists {
			logger.Error(prefix, "❌ [PHOTO-RX-ERROR] Missing chunk %d during reassembly", i)
			return fmt.Errorf("missing chunk %d", i)
		}
		photoData = append(photoData, chunk...)
	}

	logger.Debug(prefix, "📸 [PHOTO-RX-REASSEMBLE] Assembled %d bytes from %d chunks",
		len(photoData), state.expectedChunks)

	// Verify CRC
	calculatedCRC := phototransfer.CalculateCRC32(photoData)
	logger.Debug(prefix, "📸 [PHOTO-RX-VERIFY] CRC check: expected=%08X, calculated=%08X, match=%v",
		state.expectedCRC, calculatedCRC, calculatedCRC == state.expectedCRC)
	if calculatedCRC != state.expectedCRC {
		logger.Error(prefix, "❌ [PHOTO-RX-ERROR] Photo CRC mismatch: expected %08X, got %08X",
			state.expectedCRC, calculatedCRC)
		return fmt.Errorf("CRC mismatch")
	}

	// Calculate hash
	hash := sha256.Sum256(photoData)
	hashStr := hex.EncodeToString(hash[:])
	logger.Debug(prefix, "📸 [PHOTO-RX-HASH] Photo hash calculated: %s (full: %s)", hashStr[:8], hashStr)

	// Get the logical deviceID from the remote UUID
	a.mu.Lock()
	deviceID := a.remoteUUIDToDeviceID[remoteUUID]
	a.mu.Unlock()

	if deviceID == "" {
		logger.Error(prefix, "❌ [PHOTO-RX-ERROR] No deviceID mapping for remoteUUID=%s (handshake not completed?)", remoteUUID[:8])
		return fmt.Errorf("no deviceID for remoteUUID %s", remoteUUID[:8])
	}

	logger.Debug(prefix, "📸 [PHOTO-RX-MAPPING] deviceID=%s ← remoteUUID=%s", deviceID[:8], remoteUUID[:8])

	// Save photo using cache manager (persists deviceID -> photoHash mapping)
	logger.Debug(prefix, "📸 [PHOTO-RX-CACHE] Calling SaveDevicePhoto(deviceID=%s, size=%d, hash=%s)",
		deviceID[:8], len(photoData), hashStr[:8])
	if err := a.cacheManager.SaveDevicePhoto(deviceID, photoData, hashStr); err != nil {
		logger.Error(prefix, "❌ [PHOTO-RX-ERROR] Failed to save photo: %v", err)
		return err
	}

	logger.Debug(prefix, "📸 [PHOTO-RX-STATE] REASSEMBLING → SAVED (deviceID=%s, hash=%s, size=%d)",
		deviceID[:8], hashStr[:8], len(photoData))
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
				DeviceID:     deviceID,
				HardwareUUID: remoteUUID,
				Name:         name,
				RSSI:         -55, // Default RSSI, actual value not critical for photo update
				Platform:     "unknown",
				PhotoHash:    hashStr,
				PhotoData:    photoData,
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
		// Look up deviceID for logging purposes only
		a.mu.RLock()
		deviceID := a.remoteUUIDToDeviceID[gattUUID]
		a.mu.RUnlock()

		// lastHandshakeTime is now always indexed by hardware UUID (connection-scoped state)
		a.mu.RLock()
		lastHandshake, exists := a.lastHandshakeTime[gattUUID]
		a.mu.RUnlock()

		// If no handshake record or handshake is stale
		if !exists || now.Sub(lastHandshake) > staleThreshold {
			timeSince := "never"
			if exists {
				timeSince = fmt.Sprintf("%.0fs ago", now.Sub(lastHandshake).Seconds())
			}

			deviceIDStr := "unknown"
			if deviceID != "" {
				deviceIDStr = deviceID[:8]
			}

			logger.Debug(prefix, "🔄 [STALE-HANDSHAKE] Handshake stale for %s (deviceID: %s, last: %s), re-handshaking",
				gattUUID[:8], deviceIDStr, timeSince)

			// Re-handshake in place (no need to reconnect, already connected)
			go a.sendHandshakeMessage(gatt)
		}
	}
}

// startStalePhotoTransferChecker runs a background task to check for stale photo transfers
func (a *Android) startStalePhotoTransferChecker() {
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				a.checkStalePhotoTransfers()
			case <-a.staleCheckDone:
				return
			}
		}
	}()
}

// checkStalePhotoTransfers checks for stale photo transfers and requests retransmission
func (a *Android) checkStalePhotoTransfers() {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
	now := time.Now()
	staleTimeout := 5 * time.Second
	maxRetransmits := 3

	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"

	// Check central mode transfers (receiving from remote GATT servers)
	a.mu.Lock()
	for remoteUUID, state := range a.photoReceiveState {
		state.mu.Lock()
		if !state.isReceiving || state.lastChunkTime.IsZero() {
			state.mu.Unlock()
			continue
		}

		timeSinceLastChunk := now.Sub(state.lastChunkTime)
		if timeSinceLastChunk > staleTimeout {
			// Transfer is stale
			if state.retransmitCount >= maxRetransmits {
				// Give up after max retries
				logger.Warn(prefix, "❌ Photo transfer from %s timed out after %d retransmit attempts, giving up",
					remoteUUID[:8], state.retransmitCount)
				state.isReceiving = false
				state.mu.Unlock()
				delete(a.photoReceiveState, remoteUUID)
				continue
			}

			// Find missing chunks
			missingChunks := []uint16{}
			for i := uint16(0); i < state.expectedChunks; i++ {
				if _, exists := state.receivedChunks[i]; !exists {
					missingChunks = append(missingChunks, i)
				}
			}

			if len(missingChunks) == 0 {
				// No missing chunks, shouldn't happen
				state.mu.Unlock()
				continue
			}

			state.retransmitCount++
			state.lastChunkTime = now // Reset timeout for next attempt
			state.mu.Unlock()

			logger.Info(prefix, "🔄 Photo transfer from %s stalled, requesting %d missing chunks (attempt %d/%d)",
				remoteUUID[:8], len(missingChunks), state.retransmitCount, maxRetransmits)

			// Send retransmit request
			go func(rUUID string, chunks []uint16) {
				retransReq := phototransfer.EncodeRetransmitRequest(chunks)
				gatt := a.connectedGatts[rUUID]
				if gatt != nil {
					char := gatt.GetCharacteristic(auraServiceUUID, auraPhotoCharUUID)
					if char != nil {
						char.Value = retransReq
						if !gatt.WriteCharacteristic(char) {
							logger.Warn(prefix, "❌ Failed to send retransmit request to %s", rUUID[:8])
						}
					} else {
						logger.Warn(prefix, "❌ Photo characteristic not found for retransmit request to %s", rUUID[:8])
					}
				} else {
					logger.Warn(prefix, "❌ GATT not found for retransmit request to %s", rUUID[:8])
				}
			}(remoteUUID, missingChunks)
		} else {
			state.mu.Unlock()
		}
	}
	a.mu.Unlock()

	// Check server mode transfers (receiving from centrals)
	a.mu.Lock()
	for senderUUID, state := range a.photoReceiveStateServer {
		state.mu.Lock()
		if !state.isReceiving || state.lastChunkTime.IsZero() {
			state.mu.Unlock()
			continue
		}

		timeSinceLastChunk := now.Sub(state.lastChunkTime)
		if timeSinceLastChunk > staleTimeout {
			// Transfer is stale
			if state.retransmitCount >= maxRetransmits {
				// Give up after max retries
				logger.Warn(prefix, "❌ Photo transfer from %s (server mode) timed out after %d retransmit attempts, giving up",
					senderUUID[:8], state.retransmitCount)
				state.isReceiving = false
				state.mu.Unlock()
				delete(a.photoReceiveStateServer, senderUUID)
				continue
			}

			// Find missing chunks
			missingChunks := []uint16{}
			for i := uint16(0); i < state.expectedChunks; i++ {
				if _, exists := state.receivedChunks[i]; !exists {
					missingChunks = append(missingChunks, i)
				}
			}

			if len(missingChunks) == 0 {
				// No missing chunks, shouldn't happen
				state.mu.Unlock()
				continue
			}

			state.retransmitCount++
			state.lastChunkTime = now // Reset timeout for next attempt
			state.mu.Unlock()

			logger.Info(prefix, "🔄 Photo transfer from %s (server mode) stalled, requesting %d missing chunks (attempt %d/%d)",
				senderUUID[:8], len(missingChunks), state.retransmitCount, maxRetransmits)

			// Send retransmit request via wire (server mode)
			go func(sUUID string, chunks []uint16) {
				retransReq := phototransfer.EncodeRetransmitRequest(chunks)
				if err := a.wire.WriteCharacteristic(sUUID, auraServiceUUID, auraPhotoCharUUID, retransReq); err != nil {
					logger.Warn(prefix, "❌ Failed to send retransmit request to %s (server mode): %v", sUUID[:8], err)
				}
			}(senderUUID, missingChunks)
		} else {
			state.mu.Unlock()
		}
	}
	a.mu.Unlock()
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

	// Send updated handshake to all connected devices
	// This includes both:
	// 1. Devices we connected TO (as Central) - via GATT
	// 2. Devices that connected TO US (as Peripheral) - via wire.WriteCharacteristic

	a.mu.RLock()
	gattsCopy := make([]*kotlin.BluetoothGatt, 0, len(a.connectedGatts))
	for _, g := range a.connectedGatts {
		gattsCopy = append(gattsCopy, g)
	}
	centralsCopy := make([]string, 0, len(a.connectedCentrals))
	for uuid := range a.connectedCentrals {
		centralsCopy = append(centralsCopy, uuid)
	}
	a.mu.RUnlock()

	totalConnections := len(gattsCopy) + len(centralsCopy)
	logger.Debug(prefix, "📤 Sending updated handshake to %d connected device(s) (%d as Central, %d as Peripheral)",
		totalConnections, len(gattsCopy), len(centralsCopy))

	// Send via GATT (for devices we connected to as Central)
	for _, gatt := range gattsCopy {
		go a.sendHandshakeMessage(gatt)
		go a.sendProfileMessage(gatt)
	}

	// Send via wire (for devices that connected to us as Peripheral)
	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"
	const auraProfileCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5C"

	firstName := profile.FirstName
	if firstName == "" {
		firstName = "Android"
	}

	for _, centralUUID := range centralsCopy {
		// Send handshake
		go func(uuid string) {
			msg := &proto.HandshakeMessage{
				DeviceId:        a.deviceID,
				FirstName:       firstName,
				ProtocolVersion: 1,
				TxPhotoHash:     hashStringToBytes(a.photoHash),
				ProfileVersion:  profile.ProfileVersion,
			}
			data, _ := proto2.Marshal(msg)
			if err := a.wire.WriteCharacteristic(uuid, auraServiceUUID, auraTextCharUUID, data); err != nil {
				logger.Error(prefix, "❌ Failed to send handshake to central %s: %v", uuid[:8], err)
			} else {
				logger.Debug(prefix, "📤 Sent handshake to central %s", uuid[:8])
			}
		}(centralUUID)

		// Send profile message
		go func(uuid string) {
			profileMsg := &proto.ProfileMessage{
				DeviceId:    a.deviceID,
				LastName:    profile.LastName,
				PhoneNumber: "", // Not in LocalProfile struct
				Tagline:     profile.Tagline,
				Insta:       profile.Insta,
				Linkedin:    profile.LinkedIn,
				Youtube:     profile.YouTube,
				Tiktok:      profile.TikTok,
				Gmail:       profile.Gmail,
				Imessage:    profile.IMessage,
				Whatsapp:    profile.WhatsApp,
				Signal:      profile.Signal,
				Telegram:    profile.Telegram,
			}
			data, _ := proto2.Marshal(profileMsg)
			if err := a.wire.WriteCharacteristic(uuid, auraServiceUUID, auraProfileCharUUID, data); err != nil {
				logger.Error(prefix, "❌ Failed to send profile to central %s: %v", uuid[:8], err)
			} else {
				logger.Debug(prefix, "📤 Sent profile to central %s", uuid[:8])
			}
		}(centralUUID)
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

	// Get the logical deviceID for this GATT connection
	remoteUUID := gatt.GetRemoteUUID()
	a.mu.RLock()
	deviceID := a.remoteUUIDToDeviceID[remoteUUID]
	a.mu.RUnlock()

	// If we don't have a deviceID mapping yet, use hardware UUID as fallback
	if deviceID == "" {
		deviceID = remoteUUID
		logger.Warn(prefix, "⚠️  No deviceID mapping for %s, using hardware UUID as fallback", remoteUUID[:8])
	}

	// Load existing metadata or create new using logical deviceID
	metadata, err := a.cacheManager.LoadDeviceMetadata(deviceID)
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
	if err := a.cacheManager.SaveDeviceMetadata(metadata); err != nil {
		logger.Error(prefix, "❌ Failed to save device metadata: %v", err)
		return
	}

	logger.Info(prefix, "✅ Saved profile data for %s (deviceID: %s)", remoteUUID[:8], deviceID[:8])
}

// ====================================================================================
// AdvertiseCallback implementation (Peripheral mode)
// ====================================================================================

// OnStartSuccess is called when advertising starts successfully
func (a *Android) OnStartSuccess(settingsInEffect *kotlin.AdvertiseSettings) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
	logger.Debug(prefix, "✅ Advertising started successfully")
}

// OnStartFailure is called when advertising fails to start
func (a *Android) OnStartFailure(errorCode int) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
	logger.Error(prefix, "❌ Advertising failed to start (error code: %d)", errorCode)
}

// ====================================================================================
// BluetoothGattServerCallback implementation (Peripheral mode - GATT server)
// ====================================================================================

// OnConnectionStateChange is called when a central device connects/disconnects
func (d *androidGattServerDelegate) OnConnectionStateChange(device *kotlin.BluetoothDevice, status int, newState int) {
	prefix := fmt.Sprintf("%s Android", d.android.hardwareUUID[:8])
	if newState == kotlin.STATE_CONNECTED {
		logger.Debug(prefix, "📥 GATT server: Central %s connected", device.Address[:8])
		// Track this central connection
		d.android.mu.Lock()
		d.android.connectedCentrals[device.Address] = true
		d.android.mu.Unlock()
	} else if newState == kotlin.STATE_DISCONNECTED {
		logger.Debug(prefix, "📥 GATT server: Central %s disconnected", device.Address[:8])
		// Remove from connected centrals
		d.android.mu.Lock()
		delete(d.android.connectedCentrals, device.Address)
		d.android.mu.Unlock()
	}
}

// OnCharacteristicReadRequest is called when a central reads a characteristic
func (d *androidGattServerDelegate) OnCharacteristicReadRequest(device *kotlin.BluetoothDevice, requestId int, offset int, characteristic *kotlin.BluetoothGattCharacteristic) {
	prefix := fmt.Sprintf("%s Android", d.android.hardwareUUID[:8])
	logger.Debug(prefix, "📥 GATT server: Read request from %s for char %s", device.Address[:8], characteristic.UUID[:8])
	// For now, we don't support reads - all data is pushed via writes
}

// OnCharacteristicWriteRequest is called when a central writes to a characteristic
func (d *androidGattServerDelegate) OnCharacteristicWriteRequest(device *kotlin.BluetoothDevice, requestId int, characteristic *kotlin.BluetoothGattCharacteristic, preparedWrite bool, responseNeeded bool, offset int, value []byte) {
	prefix := fmt.Sprintf("%s Android", d.android.hardwareUUID[:8])

	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"
	const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"
	const auraProfileCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5C"

	logger.Debug(prefix, "📥 GATT server: Write request from %s to char %s (%d bytes)", device.Address[:8], characteristic.UUID[:8], len(value))

	// Process based on characteristic
	// We use specialized handlers that work with device UUID instead of requiring a GATT connection
	senderUUID := device.Address
	switch characteristic.UUID {
	case auraTextCharUUID:
		// Handshake message
		d.android.handleHandshakeMessageFromServer(senderUUID, value)
	case auraPhotoCharUUID:
		// Photo chunk - copy data before passing to goroutine to avoid race condition
		dataCopy := make([]byte, len(value))
		copy(dataCopy, value)
		go d.android.handlePhotoMessageFromServer(senderUUID, dataCopy)
	case auraProfileCharUUID:
		// Profile message - copy data before passing to goroutine
		dataCopy := make([]byte, len(value))
		copy(dataCopy, value)
		go d.android.handleProfileMessageFromServer(senderUUID, dataCopy)
	}
}

// OnDescriptorReadRequest is called when a central reads a descriptor
func (d *androidGattServerDelegate) OnDescriptorReadRequest(device *kotlin.BluetoothDevice, requestId int, offset int, descriptor *kotlin.BluetoothGattDescriptor) {
	// Not used in this implementation
}

// OnDescriptorWriteRequest is called when a central writes to a descriptor
func (d *androidGattServerDelegate) OnDescriptorWriteRequest(device *kotlin.BluetoothDevice, requestId int, descriptor *kotlin.BluetoothGattDescriptor, preparedWrite bool, responseNeeded bool, offset int, value []byte) {
	// Used for enabling/disabling notifications - handle subscribe/unsubscribe
	prefix := fmt.Sprintf("%s Android", d.android.hardwareUUID[:8])
	logger.Debug(prefix, "📥 GATT server: Descriptor write from %s", device.Address[:8])
}

// ====================================================================================
// Server-side message handlers (for incoming writes to our GATT server)
// ====================================================================================

// handleHandshakeMessageFromServer processes handshake from a central device
func (a *Android) handleHandshakeMessageFromServer(senderUUID string, data []byte) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	// Unmarshal binary protobuf
	var handshake proto.HandshakeMessage
	if err := proto2.Unmarshal(data, &handshake); err != nil {
		logger.Error(prefix, "❌ Failed to parse handshake from server: %v", err)
		return
	}

	// Log as JSON with snake_case for debugging
	marshaler := protojson.MarshalOptions{
		UseProtoNames: true,
	}
	jsonData, _ := marshaler.Marshal(&handshake)
	logger.Debug(prefix, "📥 RX Handshake from GATT server (binary protobuf, %d bytes): %s", len(data), string(jsonData))

	// Map sender UUID to logical device ID
	deviceID := handshake.DeviceId

	// Check if we've already handshaked recently (before updating timestamp)
	// lastHandshakeTime is indexed by hardware UUID (connection-scoped state)
	a.mu.RLock()
	lastHandshake, alreadyHandshaked := a.lastHandshakeTime[senderUUID]
	a.mu.RUnlock()

	shouldReply := !alreadyHandshaked || time.Since(lastHandshake) > 5*time.Second

	// Update our state
	a.mu.Lock()
	a.remoteUUIDToDeviceID[senderUUID] = deviceID
	logger.Debug(prefix, "🔗 [UUID-MAP] (SERVER) Mapped hardwareUUID=%s → deviceID=%s", senderUUID[:8], deviceID[:8])
	a.lastHandshakeTime[senderUUID] = time.Now()
	a.mu.Unlock()

	// Convert photo hashes from bytes to hex strings
	txPhotoHash := hashBytesToString(handshake.TxPhotoHash)
	rxPhotoHash := hashBytesToString(handshake.RxPhotoHash)

	logger.Debug(prefix, "🤝 [HANDSHAKE-RX] (SERVER) Received from %s: txHash=%s, rxHash=%s, firstName=%s",
		senderUUID[:8], truncateHash(txPhotoHash, 8), truncateHash(rxPhotoHash, 8), handshake.FirstName)

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

	logger.Info(prefix, "✅ Processed handshake from GATT server: deviceID=%s, firstName=%s", deviceID, handshake.FirstName)

	// Always trigger discovery callback to update GUI (even if we don't reply)
	// This ensures the GUI updates when first_name changes
	if a.discoveryCallback != nil {
		name := deviceID[:8]
		if handshake.FirstName != "" {
			name = handshake.FirstName
		}
		a.discoveryCallback(phone.DiscoveredDevice{
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
	a.mu.RLock()
	ourReceivedHash := a.receivedPhotoHashes[deviceID]
	a.mu.RUnlock()

	if txPhotoHash != "" && txPhotoHash != ourReceivedHash {
		logger.Debug(prefix, "📸 Remote has new photo (hash: %s), replying with handshake to request it", truncateHash(txPhotoHash, 8))
		// Reply with a handshake that shows we don't have their new photo yet
		// This will trigger them to send it to us
		shouldReply = true // Force a reply to request the photo
	}

	// Only send handshake back if this is the first time or it's been a while (or if we need their new photo)
	if shouldReply {
		// Send our handshake back to the central device
		const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
		const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"

		firstName := a.localProfile.FirstName
		if firstName == "" {
			firstName = "Android"
		}

		replyMsg := &proto.HandshakeMessage{
			DeviceId:        a.deviceID,
			FirstName:       firstName,
			ProtocolVersion: 1,
			TxPhotoHash:     hashStringToBytes(a.photoHash),
			RxPhotoHash:     hashStringToBytes(rxPhotoHash),
		}

		replyData, _ := proto2.Marshal(replyMsg)
		if err := a.wire.WriteCharacteristic(senderUUID, auraServiceUUID, auraTextCharUUID, replyData); err != nil {
			logger.Error(prefix, "❌ Failed to send handshake back: %v", err)
		} else {
			logger.Debug(prefix, "📤 Sent handshake back to %s", senderUUID[:8])
		}
	} else {
		logger.Debug(prefix, "⏭️  Skipping handshake reply to %s (already handshaked recently)", deviceID[:8])
	}

	// Check if we need to send our photo
	if rxPhotoHash != a.photoHash && a.photoHash != "" {
		logger.Debug(prefix, "📸 Remote doesn't have our photo, sending it via server mode...")
		go a.sendPhotoToDevice(senderUUID, rxPhotoHash)
	}
}

// sendPhotoToDevice sends our photo to a device via wire layer (server/peripheral mode)
func (a *Android) sendPhotoToDevice(targetUUID string, remoteRxPhotoHash string) error {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	// Check if they already have our photo
	if remoteRxPhotoHash == a.photoHash {
		logger.Debug(prefix, "⏭️  Remote already has our photo, skipping")
		return nil
	}

	// Check if a photo send is already in progress to this device
	a.mu.Lock()
	if a.photoSendInProgress[targetUUID] {
		a.mu.Unlock()
		logger.Debug(prefix, "⏭️  Photo send already in progress to %s, skipping duplicate", targetUUID[:8])
		return nil
	}

	// Mark photo send as in progress
	a.photoSendInProgress[targetUUID] = true
	a.mu.Unlock()
	defer func() {
		// Clear flag when done
		a.mu.Lock()
		delete(a.photoSendInProgress, targetUUID)
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

	logger.Info(prefix, "📸 Sending photo to %s via server mode (%d bytes, %d chunks, CRC: %08X)",
		targetUUID[:8], len(photoData), len(chunks), totalCRC)

	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"

	// Send metadata packet
	metadata := phototransfer.EncodeMetadata(uint32(len(photoData)), totalCRC, uint16(len(chunks)), nil)
	if err := a.wire.WriteCharacteristic(targetUUID, auraServiceUUID, auraPhotoCharUUID, metadata); err != nil {
		return fmt.Errorf("failed to send metadata: %w", err)
	}

	// Send chunks
	for i, chunk := range chunks {
		chunkPacket := phototransfer.EncodeChunk(uint16(i), chunk)
		if err := a.wire.WriteCharacteristic(targetUUID, auraServiceUUID, auraPhotoCharUUID, chunkPacket); err != nil {
			logger.Warn(prefix, "❌ Failed to send chunk %d/%d to %s (server mode): %v", i+1, len(chunks), targetUUID[:8], err)
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

// sendHandshakeToDevice sends a handshake to a device via wire layer (server/peripheral mode)
func (a *Android) sendHandshakeToDevice(targetUUID string) error {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"

	firstName := a.localProfile.FirstName
	if firstName == "" {
		firstName = "Android"
	}

	// Get the RxPhotoHash for this device (the hash of their photo that we have)
	a.mu.RLock()
	deviceID := a.remoteUUIDToDeviceID[targetUUID]
	rxPhotoHash := a.receivedPhotoHashes[deviceID]
	a.mu.RUnlock()

	msg := &proto.HandshakeMessage{
		DeviceId:        a.deviceID,
		FirstName:       firstName,
		ProtocolVersion: 1,
		TxPhotoHash:     hashStringToBytes(a.photoHash),
		RxPhotoHash:     hashStringToBytes(rxPhotoHash),
		ProfileVersion:  a.localProfile.ProfileVersion,
	}

	data, err := proto2.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal handshake: %w", err)
	}

	if err := a.wire.WriteCharacteristic(targetUUID, auraServiceUUID, auraTextCharUUID, data); err != nil {
		return fmt.Errorf("failed to send handshake: %w", err)
	}

	logger.Debug(prefix, "📤 Sent handshake to %s via server mode", targetUUID[:8])
	return nil
}

// handlePhotoMessageFromServer processes photo data from a central device
func (a *Android) handlePhotoMessageFromServer(senderUUID string, data []byte) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
	logger.Debug(prefix, "📥 Photo message from GATT server %s (%d bytes)", senderUUID[:8], len(data))

	// Get or create photo receive state for this sender
	a.mu.Lock()
	state, exists := a.photoReceiveStateServer[senderUUID]
	if !exists {
		state = &photoReceiveState{
			receivedChunks: make(map[uint16][]byte),
		}
		a.photoReceiveStateServer[senderUUID] = state
	}
	a.mu.Unlock()

	// Try to decode metadata
	if len(data) >= phototransfer.MetadataSize {
		meta, remaining, err := phototransfer.DecodeMetadata(data)
		if err == nil {
			// Metadata packet received
			logger.Info(prefix, "📸 Receiving photo from %s (size: %d, CRC: %08X, chunks: %d)",
				senderUUID[:8], meta.TotalSize, meta.TotalCRC, meta.TotalChunks)

			state.mu.Lock()
			logger.Debug(prefix, "📸 [PHOTO-RX-STATE] (SERVER) IDLE → RECEIVING from %s (size=%d, chunks=%d, crc=%08X)",
				senderUUID[:8], meta.TotalSize, meta.TotalChunks, meta.TotalCRC)
			state.isReceiving = true
			state.expectedSize = meta.TotalSize
			state.expectedCRC = meta.TotalCRC
			state.expectedChunks = meta.TotalChunks
			state.receivedChunks = make(map[uint16][]byte)
			state.senderDeviceID = senderUUID
			state.buffer = remaining
			state.lastChunkTime = time.Now() // Initialize timeout tracking
			state.mu.Unlock()

			if len(remaining) > 0 {
				logger.Debug(prefix, "📸 [PHOTO-RX-STATE] (SERVER) Metadata has %d bytes trailing data, processing immediately", len(remaining))
				a.processPhotoChunksFromServer(senderUUID, state)
			}
			return
		}
	}

	// Regular chunk data
	state.mu.Lock()
	isReceiving := state.isReceiving
	if isReceiving {
		bufferBefore := len(state.buffer)
		state.buffer = append(state.buffer, data...)
		logger.Debug(prefix, "📸 [PHOTO-RX-DATA] (SERVER) Appended %d bytes to buffer (buffer now %d bytes, receiving=%v)",
			len(data), len(state.buffer), isReceiving)
		if bufferBefore == 0 && len(state.buffer) > 0 {
			logger.Debug(prefix, "📸 [PHOTO-RX-STATE] (SERVER) Buffer now has data, will process chunks")
		}
	}
	state.mu.Unlock()

	if isReceiving {
		a.processPhotoChunksFromServer(senderUUID, state)
	}
}

// processPhotoChunksFromServer processes buffered photo chunks from server mode
func (a *Android) processPhotoChunksFromServer(senderUUID string, state *photoReceiveState) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	state.mu.Lock()
	defer state.mu.Unlock()

	logger.Debug(prefix, "📸 [PHOTO-RX-PARSE] (SERVER) Processing buffer with %d bytes (need %d for header)",
		len(state.buffer), phototransfer.ChunkHeaderSize)

	for {
		if len(state.buffer) < phototransfer.ChunkHeaderSize {
			logger.Debug(prefix, "📸 [PHOTO-RX-PARSE] (SERVER) Buffer too small (%d bytes), waiting for more data", len(state.buffer))
			break
		}

		chunk, consumed, err := phototransfer.DecodeChunk(state.buffer)
		if err != nil {
			logger.Debug(prefix, "📸 [PHOTO-RX-PARSE] (SERVER) DecodeChunk failed: %v (buffer len=%d)", err, len(state.buffer))
			break
		}

		state.receivedChunks[chunk.Index] = chunk.Data
		state.buffer = state.buffer[consumed:]
		state.lastChunkTime = time.Now() // Update last chunk time

		logger.Debug(prefix, "📸 [PHOTO-RX-CHUNK] (SERVER) Chunk[%d] received, size=%d (have %d/%d chunks, buffer remaining=%d)",
			chunk.Index, len(chunk.Data), len(state.receivedChunks), state.expectedChunks, len(state.buffer))

		if chunk.Index == 0 || chunk.Index == state.expectedChunks-1 {
			logger.Debug(prefix, "📥 [PHOTO-RX-MILESTONE] (SERVER) Received chunk %d/%d from %s",
				len(state.receivedChunks), state.expectedChunks, senderUUID[:8])
		}

		// Check if complete
		if uint16(len(state.receivedChunks)) == state.expectedChunks {
			logger.Debug(prefix, "📸 [PHOTO-RX-STATE] (SERVER) CHUNK_ACCUMULATING → COMPLETE (all %d chunks received)", state.expectedChunks)
			a.reassembleAndSavePhotoFromServer(senderUUID, state)
			state.isReceiving = false
			// Clean up state
			a.mu.Lock()
			delete(a.photoReceiveStateServer, senderUUID)
			a.mu.Unlock()
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

// reassembleAndSavePhotoFromServer reassembles chunks and saves the photo from server mode
func (a *Android) reassembleAndSavePhotoFromServer(senderUUID string, state *photoReceiveState) error {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	logger.Debug(prefix, "📸 [PHOTO-RX-STATE] (SERVER) COMPLETE → REASSEMBLING (expected chunks=%d, received=%d)",
		state.expectedChunks, len(state.receivedChunks))

	// Reassemble in order
	var photoData []byte
	for i := uint16(0); i < state.expectedChunks; i++ {
		photoData = append(photoData, state.receivedChunks[i]...)
	}

	logger.Debug(prefix, "📸 [PHOTO-RX-REASSEMBLE] (SERVER) Assembled %d bytes from %d chunks",
		len(photoData), state.expectedChunks)

	// Verify CRC
	calculatedCRC := phototransfer.CalculateCRC32(photoData)
	logger.Debug(prefix, "📸 [PHOTO-RX-VERIFY] (SERVER) CRC check: expected=%08X, calculated=%08X, match=%v",
		state.expectedCRC, calculatedCRC, calculatedCRC == state.expectedCRC)
	if calculatedCRC != state.expectedCRC {
		logger.Error(prefix, "❌ [PHOTO-RX-ERROR] (SERVER) Photo CRC mismatch from %s: expected %08X, got %08X",
			senderUUID[:8], state.expectedCRC, calculatedCRC)
		return fmt.Errorf("CRC mismatch")
	}

	// Calculate hash
	hash := sha256.Sum256(photoData)
	hashStr := hex.EncodeToString(hash[:])
	logger.Debug(prefix, "📸 [PHOTO-RX-HASH] (SERVER) Photo hash calculated: %s (full: %s)", hashStr[:8], hashStr)

	// Get the logical deviceID from the sender UUID
	// For server mode, we need to look this up from our cached handshakes
	a.mu.Lock()
	deviceID, exists := a.remoteUUIDToDeviceID[senderUUID]
	a.mu.Unlock()

	if !exists || deviceID == "" {
		// Reject photo from unknown device - handshake must complete first
		// This ensures we always use base36 device IDs as primary keys (per CLAUDE.md)
		logger.Error(prefix, "❌ [PHOTO-RX-ERROR] (SERVER) No deviceID mapping for senderUUID=%s (handshake not completed?)", senderUUID[:8])
		logger.Warn(prefix, "⚠️  Rejecting photo from %s: no handshake received yet (deviceID unknown)", senderUUID[:8])
		return fmt.Errorf("device ID unknown for %s, handshake required", senderUUID[:8])
	}

	logger.Debug(prefix, "📸 [PHOTO-RX-MAPPING] (SERVER) deviceID=%s ← senderUUID=%s", deviceID[:8], senderUUID[:8])

	// Save photo using cache manager (persists deviceID -> photoHash mapping)
	logger.Debug(prefix, "📸 [PHOTO-RX-CACHE] (SERVER) Calling SaveDevicePhoto(deviceID=%s, size=%d, hash=%s)",
		deviceID[:8], len(photoData), hashStr[:8])
	if err := a.cacheManager.SaveDevicePhoto(deviceID, photoData, hashStr); err != nil {
		logger.Error(prefix, "❌ [PHOTO-RX-ERROR] (SERVER) Failed to save photo from %s: %v", senderUUID[:8], err)
		return err
	}

	// Update in-memory mapping
	a.mu.Lock()
	a.receivedPhotoHashes[deviceID] = hashStr
	a.mu.Unlock()

	logger.Debug(prefix, "📸 [PHOTO-RX-STATE] (SERVER) REASSEMBLING → SAVED (deviceID=%s, hash=%s, size=%d)",
		deviceID[:8], hashStr[:8], len(photoData))
	logger.Info(prefix, "✅ Photo saved from %s (hash: %s, size: %d bytes)",
		senderUUID[:8], hashStr[:8], len(photoData))

	// Notify GUI about the new photo by re-triggering discovery callback
	if a.discoveryCallback != nil && deviceID != "" {
		// Get device name from advertising data or use device ID
		name := deviceID[:8]
		if len(deviceID) == 8 { // It's a proper device ID
			// Try to get first name from metadata
			if metadata, err := a.cacheManager.LoadDeviceMetadata(deviceID); err == nil && metadata != nil && metadata.FirstName != "" {
				name = metadata.FirstName
			}
		}

		a.discoveryCallback(phone.DiscoveredDevice{
			DeviceID:     deviceID,
			HardwareUUID: senderUUID,
			Name:         name,
			RSSI:         -50, // Default RSSI, actual value not critical for photo update
			Platform:     "unknown",
			PhotoHash:    hashStr,
			PhotoData:    photoData,
		})
		logger.Debug(prefix, "🔔 Notified GUI about received photo from %s", senderUUID[:8])
	}

	return nil
}

// handleProfileMessageFromServer processes profile data from a central device
func (a *Android) handleProfileMessageFromServer(senderUUID string, data []byte) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
	logger.Debug(prefix, "📥 Profile message from GATT server %s (%d bytes)", senderUUID[:8], len(data))
	// TODO: Implement profile handling for server-side
}
