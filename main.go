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
	manager        *swift.CBCentralManager
	uuid           string
	peripheral     *swift.CBPeripheral
	discoveredOnce bool
}

func NewFakeIOSDevice() *FakeIOSDevice {
	d := &FakeIOSDevice{
		uuid: uuid.New().String(),
	}
	d.manager = swift.NewCBCentralManager(d, d.uuid)
	return d
}

func (d *FakeIOSDevice) DidUpdateState(central swift.CBCentralManager) {
	fmt.Printf("iOS device %s state updated to %s\n", d.uuid, central.State)
}

func (d *FakeIOSDevice) DidDiscoverPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, advertisementData map[string]interface{}, rssi float64) {
	fmt.Printf("iOS device %s discovered Android device %s\n", d.uuid, peripheral.Name)

	// Connect to the first discovered peripheral (for listening mode)
	if !d.discoveredOnce {
		d.discoveredOnce = true
		fmt.Printf("iOS device preparing to receive data from Android device %s\n", peripheral.UUID)
		d.manager.Connect(&peripheral, nil)
	}
}

func (d *FakeIOSDevice) DidConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral) {
	fmt.Printf("iOS device %s connected to Android device %s\n", d.uuid, peripheral.UUID)
	d.peripheral = &peripheral
	d.peripheral.Delegate = d
	d.peripheral.StartListening()
}

func (d *FakeIOSDevice) DidFailToConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, err error) {
	fmt.Printf("iOS device %s failed to connect to Android device %s: %v\n", d.uuid, peripheral.UUID, err)
}

func (d *FakeIOSDevice) DidDiscoverServices(peripheral swift.CBPeripheral, err error) {
	fmt.Printf("iOS device discovered services\n")
}

func (d *FakeIOSDevice) DidWriteValueForCharacteristic(peripheral swift.CBPeripheral, err error) {
	if err != nil {
		fmt.Printf("iOS device write error: %v\n", err)
	} else {
		fmt.Printf("iOS device wrote value successfully\n")
	}
}

func (d *FakeIOSDevice) DidUpdateValueForCharacteristic(peripheral swift.CBPeripheral, data []byte, err error) {
	if err != nil {
		fmt.Printf("iOS device read error: %v\n", err)
	} else {
		fmt.Printf("iOS device received data: %s\n", string(data))
	}
}

type FakeAndroidDevice struct {
	manager         *kotlin.BluetoothManager
	uuid            string
	gatt            *kotlin.BluetoothGatt
	discoveredOnce  bool
	connectedDevice *kotlin.BluetoothDevice
}

func NewFakeAndroidDevice() *FakeAndroidDevice {
	d := &FakeAndroidDevice{
		uuid: uuid.New().String(),
	}
	d.manager = kotlin.NewBluetoothManager(d.uuid)
	return d
}

func (d *FakeAndroidDevice) OnScanResult(callbackType int, result *kotlin.ScanResult) {
	fmt.Printf("Android device %s discovered iOS device %s\n", d.uuid, result.Device.Name)

	// Connect to the first discovered device
	if !d.discoveredOnce {
		d.discoveredOnce = true
		d.connectedDevice = result.Device
		fmt.Printf("Android device attempting to connect to iOS device %s\n", result.Device.Address)

		// Connect to GATT
		d.gatt = result.Device.ConnectGatt(nil, false, d)
	}
}

func (d *FakeAndroidDevice) OnConnectionStateChange(gatt *kotlin.BluetoothGatt, status int, newState int) {
	if newState == 2 { // STATE_CONNECTED
		fmt.Printf("Android device connected to iOS device\n")

		// Start listening for incoming data
		gatt.StartListening()

		// Send "hi" message
		time.Sleep(1 * time.Second) // Give a moment for connection to settle
		err := gatt.WriteCharacteristic([]byte("hi"))
		if err != nil {
			fmt.Printf("Android device failed to send message: %v\n", err)
		}
	} else if newState == 0 { // STATE_DISCONNECTED
		fmt.Printf("Android device disconnected from iOS device\n")
	}
}

func (d *FakeAndroidDevice) OnServicesDiscovered(gatt *kotlin.BluetoothGatt, status int) {
	fmt.Printf("Android device discovered services\n")
}

func (d *FakeAndroidDevice) OnCharacteristicWrite(gatt *kotlin.BluetoothGatt, status int) {
	if status == 0 {
		fmt.Printf("Android device wrote characteristic successfully\n")
	} else {
		fmt.Printf("Android device write failed with status %d\n", status)
	}
}

func (d *FakeAndroidDevice) OnCharacteristicRead(gatt *kotlin.BluetoothGatt, data []byte, status int) {
	if status == 0 {
		fmt.Printf("Android device received data: %s\n", string(data))
	} else {
		fmt.Printf("Android device read failed with status %d\n", status)
	}
}

func main() {
	fmt.Println("=== Starting Fake Bluetooth Communication ===\n")

	iosDevice := NewFakeIOSDevice()
	androidDevice := NewFakeAndroidDevice()

	// Initialize device directories using the wire package
	iosWire := wire.NewWire(iosDevice.uuid)
	androidWire := wire.NewWire(androidDevice.uuid)

	fmt.Printf("iOS device UUID: %s\n", iosDevice.uuid)
	fmt.Printf("Android device UUID: %s\n\n", androidDevice.uuid)

	if err := iosWire.InitializeDevice(); err != nil {
		panic(err)
	}
	if err := androidWire.InitializeDevice(); err != nil {
		panic(err)
	}

	// iOS starts listening for peripherals
	fmt.Println("iOS device starting to scan for peripherals...")
	iosDevice.manager.ScanForPeripherals(nil, nil)

	// Android starts scanning for devices
	fmt.Println("Android device starting scan...\n")
	androidDevice.manager.Adapter.GetBluetoothLeScanner().StartScan(androidDevice)

	// Wait for devices to discover each other, connect, and exchange data
	time.Sleep(8 * time.Second)

	fmt.Println("\n=== Communication complete ===")
}
