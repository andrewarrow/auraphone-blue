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
	// Silent - not needed for demo
}

func (d *FakeIOSDevice) DidDiscoverPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, advertisementData map[string]interface{}, rssi float64) {
	// Connect to the first discovered peripheral (for listening mode)
	if !d.discoveredOnce {
		d.discoveredOnce = true
		fmt.Printf("[iOS] Discovered Android device, connecting...\n")
		d.manager.Connect(&peripheral, nil)
	}
}

func (d *FakeIOSDevice) DidConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral) {
	fmt.Printf("[iOS] Connected to Android device\n")
	d.peripheral = &peripheral
	d.peripheral.Delegate = d
	d.peripheral.StartListening()
}

func (d *FakeIOSDevice) DidFailToConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, err error) {
	fmt.Printf("[iOS] ❌ Connection failed: %v\n", err)
}

func (d *FakeIOSDevice) DidDiscoverServices(peripheral swift.CBPeripheral, err error) {
	// Silent - not needed for demo
}

func (d *FakeIOSDevice) DidWriteValueForCharacteristic(peripheral swift.CBPeripheral, err error) {
	if err != nil {
		fmt.Printf("[iOS] ❌ Write failed: %v\n", err)
	}
}

func (d *FakeIOSDevice) DidUpdateValueForCharacteristic(peripheral swift.CBPeripheral, data []byte, err error) {
	if err != nil {
		fmt.Printf("[iOS] ❌ Read failed: %v\n", err)
	} else {
		fmt.Printf("[iOS] 📩 RECEIVED: \"%s\"\n", string(data))
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
	// Connect to the first discovered device
	if !d.discoveredOnce {
		d.discoveredOnce = true
		d.connectedDevice = result.Device
		fmt.Printf("[Android] Discovered iOS device, connecting...\n")

		// Connect to GATT
		d.gatt = result.Device.ConnectGatt(nil, false, d)
	}
}

func (d *FakeAndroidDevice) OnConnectionStateChange(gatt *kotlin.BluetoothGatt, status int, newState int) {
	if newState == 2 { // STATE_CONNECTED
		fmt.Printf("[Android] Connected to iOS device\n")

		// Start listening for incoming data
		gatt.StartListening()

		// Send "hi" message
		time.Sleep(1 * time.Second) // Give a moment for connection to settle
		err := gatt.WriteCharacteristic([]byte("hi"))
		if err != nil {
			fmt.Printf("[Android] ❌ Send failed: %v\n", err)
		} else {
			fmt.Printf("[Android] 📤 SENT: \"hi\"\n")
		}
	} else if newState == 0 { // STATE_DISCONNECTED
		fmt.Printf("[Android] Disconnected from iOS device\n")
	}
}

func (d *FakeAndroidDevice) OnServicesDiscovered(gatt *kotlin.BluetoothGatt, status int) {
	// Silent - not needed for demo
}

func (d *FakeAndroidDevice) OnCharacteristicWrite(gatt *kotlin.BluetoothGatt, status int) {
	// Silent - success already logged, only log failures
	if status != 0 {
		fmt.Printf("[Android] ❌ Write failed with status %d\n", status)
	}
}

func (d *FakeAndroidDevice) OnCharacteristicRead(gatt *kotlin.BluetoothGatt, data []byte, status int) {
	if status == 0 {
		fmt.Printf("[Android] 📩 RECEIVED: \"%s\"\n", string(data))
	} else {
		fmt.Printf("[Android] ❌ Read failed with status %d\n", status)
	}
}

func main() {
	fmt.Println("=== Fake Bluetooth Communication ===\n")

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

	// iOS starts listening for peripherals
	iosDevice.manager.ScanForPeripherals(nil, nil)

	// Android starts scanning for devices
	androidDevice.manager.Adapter.GetBluetoothLeScanner().StartScan(androidDevice)

	// Wait for devices to discover each other, connect, and exchange data
	time.Sleep(8 * time.Second)

	fmt.Println("\n=== Done ===")
}
