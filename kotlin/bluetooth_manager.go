
package kotlin

import (
	"fmt"
	"os"
)

type BluetoothManager struct {
	Adapter *BluetoothAdapter
}

func NewBluetoothManager() *BluetoothManager {
	return &BluetoothManager{
		Adapter: NewBluetoothAdapter(),
	}
}

type BluetoothAdapter struct {
	scanner *BluetoothLeScanner
}

func NewBluetoothAdapter() *BluetoothAdapter {
	return &BluetoothAdapter{
		scanner: NewBluetoothLeScanner(),
	}
}

func (a *BluetoothAdapter) GetBluetoothLeScanner() *BluetoothLeScanner {
	return a.scanner
}

type BluetoothLeScanner struct {
	callback ScanCallback
}

func NewBluetoothLeScanner() *BluetoothLeScanner {
	return &BluetoothLeScanner{}
}

func (s *BluetoothLeScanner) StartScan(callback ScanCallback) {
	s.callback = callback
	fmt.Println("Starting scan...")
}

type ScanCallback interface {
	OnScanResult(callbackType int, result *ScanResult)
}

type ScanResult struct {
	Device   *BluetoothDevice
	Rssi     int
	ScanRecord []byte
}
