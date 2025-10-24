package kotlin

import (
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
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
}

func NewBluetoothLeScanner(uuid string) *BluetoothLeScanner {
	return &BluetoothLeScanner{uuid: uuid}
}

func (s *BluetoothLeScanner) StartScan(callback ScanCallback) {
	s.callback = callback
	fmt.Println("Starting scan...")
	go func() {
		for {
			files, err := os.ReadDir(".")
			if err != nil {
				continue
			}

			for _, file := range files {
				if file.IsDir() {
					deviceName := file.Name()
					if _, err := uuid.Parse(deviceName); err == nil {
						if deviceName != s.uuid {
							s.callback.OnScanResult(0, &ScanResult{Device: &BluetoothDevice{Name: "iOS Test Device", Address: deviceName}, Rssi: -55})
						}
					}
				}
			}
			time.Sleep(1 * time.Second)
		}
	}()
}

type ScanCallback interface {
	OnScanResult(callbackType int, result *ScanResult)
}

type ScanResult struct {
	Device     *BluetoothDevice
	Rssi       int
	ScanRecord []byte
}
