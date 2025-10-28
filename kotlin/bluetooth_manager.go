package kotlin

import (
	"github.com/user/auraphone-blue/wire"
)

type BluetoothManager struct {
	Adapter *BluetoothAdapter
	uuid    string
}

func NewBluetoothManager(uuid string, sharedWire *wire.Wire) *BluetoothManager {
	return &BluetoothManager{
		Adapter: NewBluetoothAdapter(uuid, sharedWire),
		uuid:    uuid,
	}
}

// OpenGattServer opens a GATT server for peripheral mode
// Matches: bluetoothManager.openGattServer(context, callback)
func (m *BluetoothManager) OpenGattServer(callback BluetoothGattServerCallback, deviceName string, sharedWire *wire.Wire) *BluetoothGattServer {
	return NewBluetoothGattServer(m.uuid, callback, deviceName, sharedWire)
}

type BluetoothAdapter struct {
	scanner    *BluetoothLeScanner
	advertiser *BluetoothLeAdvertiser
	uuid       string
	deviceName string
	wire       *wire.Wire
}

func NewBluetoothAdapter(uuid string, sharedWire *wire.Wire) *BluetoothAdapter {
	return &BluetoothAdapter{
		scanner: NewBluetoothLeScanner(uuid, sharedWire),
		uuid:    uuid,
		wire:    sharedWire,
	}
}

// NewBluetoothAdapterWithDeviceName creates an adapter with device name for advertising
func NewBluetoothAdapterWithDeviceName(uuid string, deviceName string, sharedWire *wire.Wire) *BluetoothAdapter {
	return &BluetoothAdapter{
		scanner:    NewBluetoothLeScanner(uuid, sharedWire),
		uuid:       uuid,
		deviceName: deviceName,
		wire:       sharedWire,
	}
}

func (a *BluetoothAdapter) GetBluetoothLeScanner() *BluetoothLeScanner {
	return a.scanner
}

// ShouldInitiateConnection determines if this Android device should initiate connection to target
// Simple Role Policy: Use hardware UUID comparison regardless of platform
// Device with LARGER UUID acts as Central (initiates connection)
func (a *BluetoothAdapter) ShouldInitiateConnection(targetUUID string) bool {
	// Use hardware UUID comparison for all devices
	// Device with LARGER UUID initiates the connection (deterministic collision avoidance)
	return a.uuid > targetUUID
}

// GetBluetoothLeAdvertiser returns the advertiser for peripheral mode
// Matches: bluetoothAdapter.getBluetoothLeAdvertiser()
func (a *BluetoothAdapter) GetBluetoothLeAdvertiser() *BluetoothLeAdvertiser {
	if a.advertiser == nil {
		a.advertiser = NewBluetoothLeAdvertiser(a.uuid, a.deviceName, a.wire)
	}
	return a.advertiser
}

// GetRemoteDevice returns a BluetoothDevice for the given address
// Matches: bluetoothAdapter.getRemoteDevice(address)
// This allows connecting to devices by address without scanning (learned via gossip)
// In real Android, this always succeeds even if device doesn't exist (connection will fail later)
func (a *BluetoothAdapter) GetRemoteDevice(address string) *BluetoothDevice {
	// Check if device exists (optional - real Android doesn't check this)
	if !a.wire.DeviceExists(address) {
		return nil // Device not reachable
	}

	// Read advertising data to get device name if available
	advData, err := a.wire.ReadAdvertisingData(address)
	deviceName := "Unknown Device"
	if err == nil && advData.DeviceName != "" {
		deviceName = advData.DeviceName
	}

	device := &BluetoothDevice{
		Name:    deviceName,
		Address: address,
	}
	device.SetWire(a.wire)

	return device
}

type BluetoothLeScanner struct {
	callback ScanCallback
	uuid     string
	wire     *wire.Wire
	stopChan chan struct{}
}

func NewBluetoothLeScanner(uuid string, sharedWire *wire.Wire) *BluetoothLeScanner {
	scanner := &BluetoothLeScanner{
		uuid: uuid,
		wire: sharedWire,
	}

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
