package iphone

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/proto"
	"github.com/user/auraphone-blue/swift"
	"github.com/user/auraphone-blue/wire"
)

// NewIPhone creates a new iPhone instance with gossip protocol integration
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
		staleCheckDone:         make(chan struct{}),
		gossipInterval:         5 * time.Second,
	}

	// Initialize wire
	ip.wire = wire.NewWireWithPlatform(hardwareUUID, wire.PlatformIOS, deviceName, nil)
	if err := ip.wire.InitializeDevice(); err != nil {
		fmt.Printf("Failed to initialize iOS device: %v\n", err)
		return nil
	}

	// Initialize cache manager
	ip.cacheManager = phone.NewDeviceCacheManager(hardwareUUID)
	if err := ip.cacheManager.InitializeCache(); err != nil {
		fmt.Printf("Failed to initialize cache: %v\n", err)
		return nil
	}

	// Initialize photo coordinator
	ip.photoCoordinator = phone.NewPhotoTransferCoordinator(hardwareUUID)

	// Initialize mesh view for gossip protocol
	dataDir := phone.GetDeviceDir(hardwareUUID)
	ip.meshView = phone.NewMeshView(deviceID, hardwareUUID, dataDir, ip.cacheManager)

	// Initialize connection manager for dual-role support
	ip.connManager = phone.NewConnectionManager(hardwareUUID)
	ip.connManager.SetSendFunctions(ip.sendViaCentralMode, ip.sendViaPeripheralMode)

	// Initialize shared handlers (platform-agnostic code in phone/ package)
	ip.gossipHandler = phone.NewGossipHandler(ip, ip.gossipInterval, ip.staleCheckDone)
	ip.photoHandler = phone.NewPhotoHandler(ip)
	ip.profileHandler = phone.NewProfileHandler(ip)

	// Initialize message router
	ip.messageRouter = phone.NewMessageRouter(
		ip.meshView,
		ip.cacheManager,
		ip.photoCoordinator,
	)
	ip.messageRouter.SetCallbacks(
		func(deviceID, photoHash string) error { return ip.gossipHandler.RequestPhoto(deviceID, photoHash) },
		func(deviceID string, version int32) error { return ip.gossipHandler.RequestProfile(deviceID, version) },
		func(senderUUID string, req *proto.PhotoRequestMessage) { ip.photoHandler.HandlePhotoRequest(senderUUID, req) },
		func(senderUUID string, req *proto.ProfileRequestMessage) { ip.profileHandler.HandleProfileRequest(senderUUID, req) },
	)

	// Set callback to store hardware UUID → device ID mapping when discovered via gossip
	ip.messageRouter.SetDeviceIDDiscoveredCallback(func(hardwareUUID, deviceID string) {
		ip.mu.Lock()
		ip.peripheralToDeviceID[hardwareUUID] = deviceID
		ip.mu.Unlock()

		// If we received a message from this device, they must be connected to us
		// Register them as a peripheral connection so we can send messages back
		ip.connManager.RegisterPeripheralConnection(hardwareUUID)
	})

	// Load photo mappings and profile
	ip.loadReceivedPhotoMappings()
	ip.localProfile = phone.LoadLocalProfile(hardwareUUID)

	// Setup BLE
	ip.setupBLE()
	ip.manager = swift.NewCBCentralManager(ip, hardwareUUID, ip.wire)
	ip.initializePeripheralMode()

	return ip
}

func (ip *iPhone) initializePeripheralMode() {
	delegate := &iPhonePeripheralDelegate{iphone: ip}
	ip.peripheralManager = swift.NewCBPeripheralManager(delegate, ip.hardwareUUID, wire.PlatformIOS, ip.deviceName, ip.wire)

	service := &swift.CBMutableService{
		UUID:      phone.AuraServiceUUID,
		IsPrimary: true,
	}

	ip.protocolChar = &swift.CBMutableCharacteristic{
		UUID:        phone.AuraProtocolCharUUID,
		Properties:  swift.CBCharacteristicPropertyRead | swift.CBCharacteristicPropertyWrite | swift.CBCharacteristicPropertyNotify,
		Permissions: swift.CBAttributePermissionsReadable | swift.CBAttributePermissionsWriteable,
		Service:     service,
	}
	ip.photoChar = &swift.CBMutableCharacteristic{
		UUID:        phone.AuraPhotoCharUUID,
		Properties:  swift.CBCharacteristicPropertyRead | swift.CBCharacteristicPropertyWrite | swift.CBCharacteristicPropertyNotify,
		Permissions: swift.CBAttributePermissionsReadable | swift.CBAttributePermissionsWriteable,
		Service:     service,
	}
	ip.profileChar = &swift.CBMutableCharacteristic{
		UUID:        phone.AuraProfileCharUUID,
		Properties:  swift.CBCharacteristicPropertyRead | swift.CBCharacteristicPropertyWrite | swift.CBCharacteristicPropertyNotify,
		Permissions: swift.CBAttributePermissionsReadable | swift.CBAttributePermissionsWriteable,
		Service:     service,
	}

	service.Characteristics = []*swift.CBMutableCharacteristic{ip.protocolChar, ip.photoChar, ip.profileChar}
	ip.peripheralManager.AddService(service)
}

func (ip *iPhone) setupBLE() {
	gattTable := &wire.GATTTable{
		Services: []wire.GATTService{
			{
				UUID: phone.AuraServiceUUID,
				Type: "primary",
				Characteristics: []wire.GATTCharacteristic{
					{
						UUID:       phone.AuraProtocolCharUUID,
						Properties: []string{"read", "write", "notify"},
						Descriptors: []wire.GATTDescriptor{
							{UUID: "00002902-0000-1000-8000-00805f9b34fb", Type: "CCCD"},
						},
					},
					{
						UUID:       phone.AuraPhotoCharUUID,
						Properties: []string{"read", "write", "notify"},
						Descriptors: []wire.GATTDescriptor{
							{UUID: "00002902-0000-1000-8000-00805f9b34fb", Type: "CCCD"},
						},
					},
					{
						UUID:       phone.AuraProfileCharUUID,
						Properties: []string{"read", "write", "notify"},
						Descriptors: []wire.GATTDescriptor{
							{UUID: "00002902-0000-1000-8000-00805f9b34fb", Type: "CCCD"},
						},
					},
				},
			},
		},
	}

	if err := ip.wire.WriteGATTTable(gattTable); err != nil {
		fmt.Printf("Failed to write GATT table: %v\n", err)
		return
	}

	txPowerLevel := 0
	advertisingData := &wire.AdvertisingData{
		DeviceName:    ip.deviceName,
		ServiceUUIDs:  []string{phone.AuraServiceUUID},
		TxPowerLevel:  &txPowerLevel,
		IsConnectable: true,
	}

	if err := ip.wire.WriteAdvertisingData(advertisingData); err != nil {
		fmt.Printf("Failed to write advertising data: %v\n", err)
		return
	}

	logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Setup complete - deviceID: %s", ip.deviceID)
}

func (ip *iPhone) loadReceivedPhotoMappings() {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
	photosDir := fmt.Sprintf("data/%s/cache/photos", ip.hardwareUUID)
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
		metadataFiles, _ := filepath.Glob(fmt.Sprintf("data/%s/cache/*_metadata.json", ip.hardwareUUID))

		for _, metadataFile := range metadataFiles {
			deviceID := filepath.Base(metadataFile)[:len(filepath.Base(metadataFile))-len("_metadata.json")]
			hash, _ := ip.cacheManager.GetDevicePhotoHash(deviceID)
			if hash == photoHash {
				ip.receivedPhotoHashes[deviceID] = photoHash
				loadedCount++
				break
			}
		}
	}

	if loadedCount > 0 {
		logger.Info(prefix, "Loaded %d cached photo mappings", loadedCount)
	}
}

func (ip *iPhone) Start() {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])

	go func() {
		time.Sleep(500 * time.Millisecond)
		ip.manager.ScanForPeripherals(nil, nil)
		logger.Info(prefix, "Started scanning (Central mode)")
	}()

	advertisingData := map[string]interface{}{
		"kCBAdvDataLocalName":    ip.deviceName,
		"kCBAdvDataServiceUUIDs": []string{phone.AuraServiceUUID},
	}

	ip.peripheralManager.StartAdvertising(advertisingData)
	logger.Info(prefix, "✅ Started advertising (Peripheral mode)")

	go ip.gossipHandler.GossipLoop()
	logger.Info(prefix, "✅ Started gossip protocol (5s interval)")
}

func (ip *iPhone) Stop() {
	fmt.Printf("[%s iOS] Stopping\n", ip.hardwareUUID[:8])
	close(ip.staleCheckDone)
}

// Getters and setters

func (ip *iPhone) SetDiscoveryCallback(callback phone.DeviceDiscoveryCallback) {
	ip.discoveryCallback = callback
}

// TriggerDiscoveryUpdate triggers the discovery callback to update the GUI
func (ip *iPhone) TriggerDiscoveryUpdate(hardwareUUID, deviceID, photoHash string, photoData []byte) {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
	logger.Debug(prefix, "🔔 TriggerDiscoveryUpdate ENTER: hardwareUUID=%s, deviceID=%s, photoHash=%s, photoDataLen=%d",
		hardwareUUID[:8], deviceID[:8], photoHash[:8], len(photoData))

	if ip.discoveryCallback == nil {
		logger.Warn(prefix, "⚠️  discoveryCallback is NIL - cannot update GUI!")
		return
	}

	logger.Debug(prefix, "🔔 discoveryCallback is NOT nil, proceeding...")

	// Look up device name from peripheralToDeviceID map
	ip.mu.RLock()
	// Default name
	name := "Unknown Device"
	// Try to get a better name from discovered peripherals or connected devices
	for pUUID, dID := range ip.peripheralToDeviceID {
		if pUUID == hardwareUUID || dID == deviceID {
			if peripheral, exists := ip.connectedPeripherals[pUUID]; exists {
				name = peripheral.Name
				break
			}
		}
	}
	ip.mu.RUnlock()

	logger.Debug(prefix, "🔔 BEFORE calling discoveryCallback with name=%s", name)

	ip.discoveryCallback(phone.DiscoveredDevice{
		DeviceID:     deviceID,
		HardwareUUID: hardwareUUID,
		Name:         name,
		RSSI:         0, // RSSI not relevant for cached photo updates
		Platform:     "unknown",
		PhotoHash:    photoHash,
		PhotoData:    photoData,
	})

	logger.Debug(prefix, "🔔 AFTER calling discoveryCallback - GUI update complete")
}

func (ip *iPhone) GetDeviceUUID() string { return ip.hardwareUUID }

func (ip *iPhone) SetProfilePhoto(photoPath string) error {
	data, err := os.ReadFile(photoPath)
	if err != nil {
		return fmt.Errorf("failed to read photo: %w", err)
	}

	hash := sha256.Sum256(data)
	photoHash := hex.EncodeToString(hash[:])

	cachePath := fmt.Sprintf("data/%s/cache/my_photo.jpg", ip.hardwareUUID)
	cacheDir := filepath.Dir(cachePath)
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(cachePath, data, 0644); err != nil {
		return err
	}

	ip.mu.Lock()
	ip.photoPath, ip.photoHash, ip.photoData = photoPath, photoHash, data
	ip.mu.Unlock()

	logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "📸 Set photo: %s", phone.TruncateHash(photoHash, 8))
	go ip.gossipHandler.SendGossipToNeighbors()
	return nil
}

func (ip *iPhone) GetProfilePhotoHash() string {
	ip.mu.RLock()
	defer ip.mu.RUnlock()
	return ip.photoHash
}

// GetLocalProfileMap returns the local profile as a map (for external API)
func (ip *iPhone) GetLocalProfileMap() map[string]string {
	ip.mu.RLock()
	defer ip.mu.RUnlock()
	return phone.ConvertProfileToMap(ip.localProfile)
}

func (ip *iPhone) UpdateLocalProfile(profile map[string]string) error {
	ip.mu.Lock()
	phone.UpdateProfileFromMap(ip.localProfile, profile)
	ip.mu.Unlock()

	if err := phone.IncrementProfileVersion(ip.hardwareUUID, ip.localProfile); err != nil {
		return err
	}

	logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "📝 Updated profile to v%d", ip.localProfile.ProfileVersion)
	go ip.gossipHandler.SendGossipToNeighbors()
	return nil
}

func (ip *iPhone) shouldActAsCentral(remoteUUID, remoteName string) bool {
	return ip.hardwareUUID > remoteUUID
}
