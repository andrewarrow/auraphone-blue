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

type BluetoothAdapter struct {
	scanner *BluetoothLeScanner
	uuid    string
}

func NewBluetoothAdapter(uuid string) *BluetoothAdapter {
	return &BluetoothAdapter{
		scanner: NewBluetoothLeScanner(uuid),
		uuid:    uuid,
	}
}

func (a *BluetoothAdapter) GetBluetoothLeScanner() *BluetoothLeScanner {
	return a.scanner
}

type BluetoothLeScanner struct {
	callback ScanCallback
	uuid     string
	wire     *wire.Wire
	stopChan chan struct{}
}

func NewBluetoothLeScanner(uuid string) *BluetoothLeScanner {
	return &BluetoothLeScanner{
		uuid: uuid,
		wire: wire.NewWire(uuid),
	}
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
