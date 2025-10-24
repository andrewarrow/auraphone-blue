
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/user/auraphone-blue/kotlin"
	"github.com/user/auraphone-blue/swift"
)

type FakeIOSDevice struct {
	manager *swift.CBCentralManager
	uuid    string
}

func NewFakeIOSDevice() *FakeIOSDevice {
	d := &FakeIOSDevice{
		uuid: uuid.New().String(),
	}
	d.manager = swift.NewCBCentralManager(d)
	return d
}

func (d *FakeIOSDevice) DidUpdateState(central swift.CBCentralManager) {
	fmt.Printf("iOS device %s state updated to %s
", d.uuid, central.State)
}

func (d *FakeIOSDevice) DidDiscoverPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, advertisementData map[string]interface{}, rssi float64) {
	fmt.Printf("iOS device %s discovered Android device %s
", d.uuid, peripheral.Name)
}

type FakeAndroidDevice struct {
	manager *kotlin.BluetoothManager
	uuid    string
}

func NewFakeAndroidDevice() *FakeAndroidDevice {
	d := &FakeAndroidDevice{
		uuid: uuid.New().String(),
	}
	d.manager = kotlin.NewBluetoothManager()
	return d
}

func (d *FakeAndroidDevice) OnScanResult(callbackType int, result *kotlin.ScanResult) {
	fmt.Printf("Android device %s discovered iOS device %s
", d.uuid, result.Device.Name)
}

func main() {
	iosDevice := NewFakeIOSDevice()
	androidDevice := NewFakeAndroidDevice()

	createDeviceDirs(iosDevice.uuid)
	createDeviceDirs(androidDevice.uuid)

	iosDevice.manager.ScanForPeripherals(nil, nil)
	androidDevice.manager.GetBluetoothLeScanner().StartScan(androidDevice)

	// Simulate discovery
	iosDevice.DidDiscoverPeripheral(*iosDevice.manager, swift.CBPeripheral{Name: "Android Test Device", UUID: androidDevice.uuid}, nil, -50)
	androidDevice.OnScanResult(0, &kotlin.ScanResult{Device: &kotlin.BluetoothDevice{Name: "iOS Test Device", Address: iosDevice.uuid}, Rssi: -55})
}

func createDeviceDirs(uuid string) {
	basePath := filepath.Join(".", uuid)
	inboxPath := filepath.Join(basePath, "inbox")
	outboxPath := filepath.Join(basePath, "outbox")

	os.MkdirAll(inboxPath, 0755)
	os.MkdirAll(outboxPath, 0755)
}
