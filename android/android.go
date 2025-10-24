package android

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/user/auraphone-blue/kotlin"
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

	fmt.Printf("[%s Android] Setup complete - advertising as: %s\n", a.deviceUUID[:8], a.deviceName)
}

// Start begins BLE advertising and scanning
func (a *Android) Start() {
	go func() {
		time.Sleep(500 * time.Millisecond)
		scanner := a.manager.Adapter.GetBluetoothLeScanner()
		scanner.StartScan(a)
		fmt.Printf("[%s Android] Started scanning for devices\n", a.deviceUUID[:8])
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
	fmt.Printf("[%s Android] Discovered: %s (RSSI: %.0f dBm)\n", a.deviceUUID[:8], name, rssi)

	if a.discoveryCallback != nil {
		a.discoveryCallback(phone.DiscoveredDevice{
			DeviceID: result.Device.Address,
			Name:     name,
			RSSI:     rssi,
			Platform: "unknown",
		})
	}
}
