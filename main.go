package main

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/user/auraphone-blue/kotlin"
	"github.com/user/auraphone-blue/swift"
	"github.com/user/auraphone-blue/wire"
)

type FakeIOSDevice struct {
	manager *swift.CBCentralManager
	uuid    string
}

func NewFakeIOSDevice() *FakeIOSDevice {
	d := &FakeIOSDevice{
		uuid: uuid.New().String(),
	}
	d.manager = swift.NewCBCentralManager(d, d.uuid)
	return d
}

func (d *FakeIOSDevice) DidUpdateState(central swift.CBCentralManager) {
	fmt.Printf("iOS device %s state updated to %s", d.uuid, central.State)
}

func (d *FakeIOSDevice) DidDiscoverPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, advertisementData map[string]interface{}, rssi float64) {
	fmt.Printf("iOS device %s discovered Android device %s", d.uuid, peripheral.Name)
}

type FakeAndroidDevice struct {
	manager *kotlin.BluetoothManager
	uuid    string
}

func NewFakeAndroidDevice() *FakeAndroidDevice {
	d := &FakeAndroidDevice{
		uuid: uuid.New().String(),
	}
	d.manager = kotlin.NewBluetoothManager(d.uuid)
	return d
}

func (d *FakeAndroidDevice) OnScanResult(callbackType int, result *kotlin.ScanResult) {
	fmt.Printf("Android device %s discovered iOS device %s", d.uuid, result.Device.Name)
}

func main() {
	iosDevice := NewFakeIOSDevice()
	androidDevice := NewFakeAndroidDevice()

	// Initialize device directories using the wire package
	iosWire := wire.NewWire(iosDevice.uuid)
	androidWire := wire.NewWire(androidDevice.uuid)

	if err := iosWire.InitializeDevice(); err != nil {
		panic(err)
	}
	if err := androidWire.InitializeDevice(); err != nil {
		panic(err)
	}

	iosDevice.manager.ScanForPeripherals(nil, nil)
	androidDevice.manager.Adapter.GetBluetoothLeScanner().StartScan(androidDevice)

	time.Sleep(5 * time.Second)
}
