package iphone

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/swift"
	"github.com/user/auraphone-blue/wire"
)

// iPhone represents an iOS device with BLE capabilities
type iPhone struct {
	deviceUUID        string
	deviceName        string
	wire              *wire.Wire
	manager           *swift.CBCentralManager
	discoveryCallback phone.DeviceDiscoveryCallback
}

// NewIPhone creates a new iPhone instance
func NewIPhone() *iPhone {
	deviceUUID := uuid.New().String()
	deviceName := "iOS Device"

	ip := &iPhone{
		deviceUUID: deviceUUID,
		deviceName: deviceName,
	}

	// Initialize wire
	ip.wire = wire.NewWire(deviceUUID)
	if err := ip.wire.InitializeDevice(); err != nil {
		fmt.Printf("Failed to initialize iOS device: %v\n", err)
		return nil
	}

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

	fmt.Printf("[%s iOS] Setup complete - advertising as: %s\n", ip.deviceUUID[:8], ip.deviceName)
}

// Start begins BLE advertising and scanning
func (ip *iPhone) Start() {
	go func() {
		time.Sleep(500 * time.Millisecond)
		ip.manager.ScanForPeripherals(nil, nil)
		fmt.Printf("[%s iOS] Started scanning for peripherals\n", ip.deviceUUID[:8])
	}()
}

// Stop stops BLE operations and cleans up resources
func (ip *iPhone) Stop() {
	fmt.Printf("[%s iOS] Stopping BLE operations\n", ip.deviceUUID[:8])
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

	fmt.Printf("[%s iOS] Discovered: %s (RSSI: %.0f dBm)\n", ip.deviceUUID[:8], name, rssi)

	if ip.discoveryCallback != nil {
		ip.discoveryCallback(phone.DiscoveredDevice{
			DeviceID: peripheral.UUID,
			Name:     name,
			RSSI:     rssi,
			Platform: "unknown",
		})
	}
}

func (ip *iPhone) DidConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral) {
	// Connection handling for future
}

func (ip *iPhone) DidFailToConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, err error) {
	// Connection failure handling for future
}
