package android

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/user/auraphone-blue/kotlin"
	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/wire"
)

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
}

// NewAndroid creates a new Android instance
func NewAndroid() *Android {
	deviceUUID := uuid.New().String()
	deviceName := "Android Device"

	a := &Android{
		deviceUUID: deviceUUID,
		deviceName: deviceName,
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
		TxPhotoHash:   a.photoHash, // Hash of OUR photo we can send
		RxPhotoHash:   "",            // TODO: Set to hash of photos we've received from other devices
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

	txPhotoHash := ""
	if result.ScanRecord != nil && result.ScanRecord.TxPhotoHash != "" {
		txPhotoHash = result.ScanRecord.TxPhotoHash
	}

	rssi := float64(result.Rssi)

	prefix := fmt.Sprintf("%s Android", a.deviceUUID[:8])
	logger.Debug(prefix, "📱 DISCOVERED device %s (%s)", result.Device.Address[:8], name)
	logger.Debug(prefix, "   └─ RSSI: %.0f dBm", rssi)
	if txPhotoHash != "" {
		logger.Debug(prefix, "   └─ TX Photo Hash: %s", txPhotoHash[:8])
	} else {
		logger.Debug(prefix, "   └─ TX Photo Hash: (none)")
	}

	if a.discoveryCallback != nil {
		a.discoveryCallback(phone.DiscoveredDevice{
			DeviceID:  result.Device.Address,
			Name:      name,
			RSSI:      rssi,
			Platform:  "unknown",
			PhotoHash: txPhotoHash, // Remote device's TX hash (photo they have available)
		})
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
