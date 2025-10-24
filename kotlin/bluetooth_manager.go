package kotlin

import (
	"fmt"

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
	fmt.Println("Starting scan...")

	s.stopChan = s.wire.StartDiscovery(func(deviceUUID string) {
		s.callback.OnScanResult(0, &ScanResult{
			Device: &BluetoothDevice{
				Name:    "iOS Test Device",
				Address: deviceUUID,
			},
			Rssi: -55,
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
	ScanRecord []byte
}
