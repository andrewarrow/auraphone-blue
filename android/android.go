package android

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/user/auraphone-blue/kotlin"
	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/proto"
	"github.com/user/auraphone-blue/wire"
)

// NewAndroid creates a new Android instance with gossip protocol integration
func NewAndroid(hardwareUUID string) *Android {
	deviceName := "Android Device"

	// Load or generate device ID (8-char base36, persists across app restarts)
	deviceID, err := phone.LoadOrGenerateDeviceID(hardwareUUID)
	if err != nil {
		fmt.Printf("Failed to load/generate device ID: %v\n", err)
		return nil
	}

	a := &Android{
		hardwareUUID:            hardwareUUID,
		deviceID:                deviceID,
		deviceName:              deviceName,
		connectedGatts:          make(map[string]*kotlin.BluetoothGatt),
		connectedCentrals:       make(map[string]bool),
		discoveredDevices:       make(map[string]*kotlin.BluetoothDevice),
		remoteUUIDToDeviceID:    make(map[string]string),
		deviceIDToPhotoHash:     make(map[string]string),
		receivedPhotoHashes:     make(map[string]string),
		receivedProfileVersion:  make(map[string]int32),
		lastHandshakeTime:       make(map[string]time.Time),
		staleCheckDone:          make(chan struct{}),
		photoReceiveState:       make(map[string]*photoReceiveState),
		photoReceiveStateServer: make(map[string]*photoReceiveState),
		useAutoConnect:          false, // Default: manual reconnect (matches real Android apps)
		gossipInterval:          5 * time.Second,
	}

	// Initialize wire
	a.wire = wire.NewWireWithPlatform(hardwareUUID, wire.PlatformAndroid, deviceName, nil)
	if err := a.wire.InitializeDevice(); err != nil {
		fmt.Printf("Failed to initialize Android device: %v\n", err)
		return nil
	}

	// Initialize cache manager
	a.cacheManager = phone.NewDeviceCacheManager(hardwareUUID)
	if err := a.cacheManager.InitializeCache(); err != nil {
		fmt.Printf("Failed to initialize cache: %v\n", err)
		return nil
	}

	// Initialize photo coordinator
	a.photoCoordinator = phone.NewPhotoTransferCoordinator(hardwareUUID)

	// Initialize mesh view for gossip protocol
	dataDir := fmt.Sprintf("data/%s", hardwareUUID)
	a.meshView = phone.NewMeshView(deviceID, hardwareUUID, dataDir, a.cacheManager)

	// Initialize connection manager for dual-role support
	a.connManager = phone.NewConnectionManager(hardwareUUID)
	a.connManager.SetSendFunctions(a.sendViaCentralMode, a.sendViaPeripheralMode)

	// Initialize message router
	a.messageRouter = phone.NewMessageRouter(
		a.meshView,
		a.cacheManager,
		a.photoCoordinator,
	)
	a.messageRouter.SetCallbacks(
		func(deviceID, photoHash string) { a.requestPhoto(deviceID, photoHash) },
		func(deviceID string, version int32) { a.requestProfile(deviceID, version) },
		func(senderUUID string, req *proto.PhotoRequestMessage) { a.handlePhotoRequest(senderUUID, req) },
		func(senderUUID string, req *proto.ProfileRequestMessage) { a.handleProfileRequest(senderUUID, req) },
	)

	// Load photo mappings and profile
	a.loadReceivedPhotoMappings()
	a.localProfile = phone.LoadLocalProfile(hardwareUUID)

	// Setup BLE
	a.setupBLE()
	a.manager = kotlin.NewBluetoothManager(hardwareUUID)
	a.initializePeripheralMode()

	return a
}

func (a *Android) initializePeripheralMode() {
	// Create GATT server with wrapper callback, passing shared wire
	delegate := &androidGattServerDelegate{android: a}
	gattServer := kotlin.NewBluetoothGattServer(a.hardwareUUID, delegate, wire.PlatformAndroid, a.deviceName, a.wire)

	// Add the Aura service
	service := &kotlin.BluetoothGattService{
		UUID: phone.AuraServiceUUID,
		Type: kotlin.SERVICE_TYPE_PRIMARY,
	}

	// Add characteristics
	protocolChar := &kotlin.BluetoothGattCharacteristic{
		UUID:       phone.AuraProtocolCharUUID,
		Properties: kotlin.PROPERTY_READ | kotlin.PROPERTY_WRITE | kotlin.PROPERTY_NOTIFY,
	}
	photoChar := &kotlin.BluetoothGattCharacteristic{
		UUID:       phone.AuraPhotoCharUUID,
		Properties: kotlin.PROPERTY_READ | kotlin.PROPERTY_WRITE | kotlin.PROPERTY_NOTIFY,
	}
	profileChar := &kotlin.BluetoothGattCharacteristic{
		UUID:       phone.AuraProfileCharUUID,
		Properties: kotlin.PROPERTY_READ | kotlin.PROPERTY_WRITE | kotlin.PROPERTY_NOTIFY,
	}

	service.Characteristics = append(service.Characteristics, protocolChar, photoChar, profileChar)
	gattServer.AddService(service)

	// Create advertiser, passing shared wire
	a.advertiser = kotlin.NewBluetoothLeAdvertiser(a.hardwareUUID, wire.PlatformAndroid, a.deviceName, a.wire)
	a.advertiser.SetGattServer(gattServer)
}

func (a *Android) setupBLE() {
	gattTable := &wire.GATTTable{
		Services: []wire.GATTService{
			{
				UUID: phone.AuraServiceUUID,
				Type: "primary",
				Characteristics: []wire.GATTCharacteristic{
					{UUID: phone.AuraProtocolCharUUID, Properties: []string{"read", "write", "notify"}},
					{UUID: phone.AuraPhotoCharUUID, Properties: []string{"read", "write", "notify"}},
					{UUID: phone.AuraProfileCharUUID, Properties: []string{"read", "write", "notify"}},
				},
			},
		},
	}

	if err := a.wire.WriteGATTTable(gattTable); err != nil {
		fmt.Printf("Failed to write GATT table: %v\n", err)
		return
	}

	txPowerLevel := 0
	advertisingData := &wire.AdvertisingData{
		DeviceName:    a.deviceName,
		ServiceUUIDs:  []string{phone.AuraServiceUUID},
		TxPowerLevel:  &txPowerLevel,
		IsConnectable: true,
	}

	if err := a.wire.WriteAdvertisingData(advertisingData); err != nil {
		fmt.Printf("Failed to write advertising data: %v\n", err)
		return
	}

	logger.Info(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "Setup complete - deviceID: %s", a.deviceID)
}

func (a *Android) loadReceivedPhotoMappings() {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
	photosDir := fmt.Sprintf("data/%s/cache/photos", a.hardwareUUID)
	entries, err := os.ReadDir(photosDir)
	if err != nil {
		return
	}

	loadedCount := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jpg" {
			continue
		}

		photoHash := entry.Name()[:len(entry.Name())-4]
		metadataFiles, _ := filepath.Glob(fmt.Sprintf("data/%s/cache/*_metadata.json", a.hardwareUUID))

		for _, metadataFile := range metadataFiles {
			deviceID := filepath.Base(metadataFile)[:len(filepath.Base(metadataFile))-len("_metadata.json")]
			hash, _ := a.cacheManager.GetDevicePhotoHash(deviceID)
			if hash == photoHash {
				a.receivedPhotoHashes[deviceID] = photoHash
				loadedCount++
				break
			}
		}
	}

	if loadedCount > 0 {
		logger.Info(prefix, "Loaded %d cached photo mappings", loadedCount)
	}
}

func (a *Android) Start() {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	go func() {
		time.Sleep(500 * time.Millisecond)
		scanner := a.manager.Adapter.GetBluetoothLeScanner()
		scanner.StartScan(a)
		logger.Info(prefix, "Started scanning (Central mode)")
	}()

	settings := &kotlin.AdvertiseSettings{
		AdvertiseMode: kotlin.ADVERTISE_MODE_LOW_LATENCY,
		Connectable:   true,
		Timeout:       0,
		TxPowerLevel:  kotlin.ADVERTISE_TX_POWER_MEDIUM,
	}

	advertiseData := &kotlin.AdvertiseData{
		ServiceUUIDs:        []string{phone.AuraServiceUUID},
		IncludeDeviceName:   true,
		IncludeTxPowerLevel: true,
	}

	a.advertiser.StartAdvertising(settings, advertiseData, nil, a)
	logger.Info(prefix, "✅ Started advertising (Peripheral mode)")

	go a.gossipLoop()
	logger.Info(prefix, "✅ Started gossip protocol (5s interval)")
}

func (a *Android) Stop() {
	fmt.Printf("[%s Android] Stopping\n", a.hardwareUUID[:8])
	close(a.staleCheckDone)
}

// Getters and setters

func (a *Android) SetDiscoveryCallback(callback phone.DeviceDiscoveryCallback) {
	a.discoveryCallback = callback
}

func (a *Android) GetDeviceUUID() string { return a.hardwareUUID }
func (a *Android) GetDeviceID() string   { return a.deviceID }
func (a *Android) GetDeviceName() string { return a.deviceName }
func (a *Android) GetPlatform() string   { return "Android" }

func (a *Android) SetProfilePhoto(photoPath string) error {
	data, err := os.ReadFile(photoPath)
	if err != nil {
		return fmt.Errorf("failed to read photo: %w", err)
	}

	hash := sha256.Sum256(data)
	photoHash := hex.EncodeToString(hash[:])

	cachePath := fmt.Sprintf("data/%s/cache/my_photo.jpg", a.hardwareUUID)
	cacheDir := filepath.Dir(cachePath)
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(cachePath, data, 0644); err != nil {
		return err
	}

	a.mu.Lock()
	a.photoPath, a.photoHash, a.photoData = photoPath, photoHash, data
	a.mu.Unlock()

	logger.Info(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "📸 Set photo: %s", phone.TruncateHash(photoHash, 8))
	go a.sendGossipToNeighbors()
	return nil
}

func (a *Android) GetProfilePhotoHash() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.photoHash
}

func (a *Android) GetLocalProfile() map[string]string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return phone.ConvertProfileToMap(a.localProfile)
}

func (a *Android) UpdateLocalProfile(profile map[string]string) error {
	a.mu.Lock()
	phone.UpdateProfileFromMap(a.localProfile, profile)
	a.mu.Unlock()

	if err := phone.IncrementProfileVersion(a.hardwareUUID, a.localProfile); err != nil {
		return err
	}

	logger.Info(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "📝 Updated profile to v%d", a.localProfile.ProfileVersion)
	go a.sendGossipToNeighbors()
	return nil
}

// sendViaCentralMode sends data via Central mode (GATT client write)
func (a *Android) sendViaCentralMode(remoteUUID, charUUID string, data []byte) error {
	a.mu.RLock()
	gatt, exists := a.connectedGatts[remoteUUID]
	a.mu.RUnlock()

	if !exists {
		return fmt.Errorf("not connected as central to %s", remoteUUID[:8])
	}

	char := gatt.GetCharacteristic(phone.AuraServiceUUID, charUUID)
	if char == nil {
		return fmt.Errorf("characteristic %s not found", charUUID[:8])
	}

	char.Value = data
	if !gatt.WriteCharacteristic(char) {
		return fmt.Errorf("write failed for characteristic %s", charUUID[:8])
	}

	return nil
}

// sendViaPeripheralMode sends data via Peripheral mode (GATT server notification)
func (a *Android) sendViaPeripheralMode(remoteUUID, charUUID string, data []byte) error {
	// Android uses wire layer to send notifications from GATT server
	if err := a.wire.WriteCharacteristic(remoteUUID, phone.AuraServiceUUID, charUUID, data); err != nil {
		return fmt.Errorf("failed to send notification: %w", err)
	}
	return nil
}
