package kotlin

import (
	"github.com/user/auraphone-blue/wire"
)

type BluetoothManager struct {
	Adapter *BluetoothAdapter
	uuid    string
}

func NewBluetoothManager(uuid string) *BluetoothManager {
	return &BluetoothManager{
		Adapter: NewBluetoothAdapter(uuid),
		uuid:    uuid,
	}
}

// OpenGattServer opens a GATT server for peripheral mode
// Matches: bluetoothManager.openGattServer(context, callback)
func (m *BluetoothManager) OpenGattServer(callback BluetoothGattServerCallback, platform wire.Platform, deviceName string, sharedWire *wire.Wire) *BluetoothGattServer {
	return NewBluetoothGattServer(m.uuid, callback, platform, deviceName, sharedWire)
}

type BluetoothAdapter struct {
	scanner    *BluetoothLeScanner
	advertiser *BluetoothLeAdvertiser
	uuid       string
	platform   wire.Platform
	deviceName string
}

func NewBluetoothAdapter(uuid string) *BluetoothAdapter {
	return &BluetoothAdapter{
		scanner:  NewBluetoothLeScanner(uuid),
		uuid:     uuid,
		platform: wire.PlatformAndroid,
	}
}

// NewBluetoothAdapterWithPlatform creates an adapter with platform info for advertising
func NewBluetoothAdapterWithPlatform(uuid string, platform wire.Platform, deviceName string) *BluetoothAdapter {
	return &BluetoothAdapter{
		scanner:    NewBluetoothLeScanner(uuid),
		uuid:       uuid,
		platform:   platform,
		deviceName: deviceName,
	}
}

func (a *BluetoothAdapter) GetBluetoothLeScanner() *BluetoothLeScanner {
	return a.scanner
}

// ShouldInitiateConnection determines if this Android device should initiate connection to target
// Simple Role Policy: Use hardware UUID comparison regardless of platform
// Device with LARGER UUID acts as Central (initiates connection)
func (a *BluetoothAdapter) ShouldInitiateConnection(targetPlatform wire.Platform, targetUUID string) bool {
	// Use hardware UUID comparison for all devices
	// Device with LARGER UUID initiates the connection (deterministic collision avoidance)
	return a.uuid > targetUUID
}

// GetBluetoothLeAdvertiser returns the advertiser for peripheral mode
// Matches: bluetoothAdapter.getBluetoothLeAdvertiser()
func (a *BluetoothAdapter) GetBluetoothLeAdvertiser(sharedWire *wire.Wire) *BluetoothLeAdvertiser {
	if a.advertiser == nil {
		a.advertiser = NewBluetoothLeAdvertiser(a.uuid, a.platform, a.deviceName, sharedWire)
	}
	return a.advertiser
}

type BluetoothLeScanner struct {
	callback ScanCallback
	uuid     string
	wire     *wire.Wire
	stopChan chan struct{}
}

func NewBluetoothLeScanner(uuid string) *BluetoothLeScanner {
	w := wire.NewWire(uuid)

	scanner := &BluetoothLeScanner{
		uuid: uuid,
		wire: w,
	}

	// Set up disconnect callback - will be used when connections are made
	w.SetDisconnectCallback(func(deviceUUID string) {
		// Connection was randomly dropped
		// Callback will be triggered on individual GATT connections
		if scanner.callback != nil {
			// Note: We don't have direct access to the gatt object here
			// The gatt's callback will need to be notified through wire state
		}
	})

	return scanner
}

func (s *BluetoothLeScanner) StartScan(callback ScanCallback) {
	s.callback = callback

	s.stopChan = s.wire.StartDiscovery(func(deviceUUID string) {
		// Read advertising data from the discovered device
		advData, err := s.wire.ReadAdvertisingData(deviceUUID)
		if err != nil {
			// Fall back to empty advertising data if not available
			advData = &wire.AdvertisingData{
				IsConnectable: true,
			}
		}

		// Build ScanRecord from advertising data
		scanRecord := &ScanRecord{
			DeviceName:       advData.DeviceName,
			ServiceUUIDs:     advData.ServiceUUIDs,
			ManufacturerData: make(map[int][]byte),
			TxPowerLevel:     advData.TxPowerLevel,
			AdvertiseFlags:   0x06, // General discoverable mode, BR/EDR not supported
		}

		// Parse manufacturer data if present (Android format uses company ID)
		if len(advData.ManufacturerData) > 0 {
			// For simplicity, use company ID 0xFFFF (reserved) for our fake data
			scanRecord.ManufacturerData[0xFFFF] = advData.ManufacturerData
		}

		// Use device name from advertising data if available
		deviceName := advData.DeviceName
		if deviceName == "" {
			deviceName = "Unknown Device"
		}

		device := &BluetoothDevice{
			Name:    deviceName,
			Address: deviceUUID,
		}
		device.SetWire(s.wire)

		// Get realistic RSSI from wire layer
		rssi := s.wire.GetRSSI()

		s.callback.OnScanResult(0, &ScanResult{
			Device:     device,
			Rssi:       rssi,
			ScanRecord: scanRecord,
		})
	})
}

func (s *BluetoothLeScanner) StopScan() {
	if s.stopChan != nil {
		close(s.stopChan)
		s.stopChan = nil
	}
}

type ScanCallback interface {
	OnScanResult(callbackType int, result *ScanResult)
}

type ScanResult struct {
	Device     *BluetoothDevice
	Rssi       int
	ScanRecord *ScanRecord
}

// ScanRecord represents the advertising data from a BLE scan
type ScanRecord struct {
	DeviceName       string
	ServiceUUIDs     []string
	ManufacturerData map[int][]byte // Company ID -> data
	TxPowerLevel     *int
	AdvertiseFlags   int
}
