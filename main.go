package main

import (
	"fmt"
	"os"
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

	// Discover services
	d.peripheral.DiscoverServices(nil)

	// Start listening for incoming characteristic updates
	d.peripheral.StartListening()
}

func (d *FakeIOSDevice) DidFailToConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, err error) {
	fmt.Printf("[iOS] ❌ Connection failed: %v\n", err)
}

func (d *FakeIOSDevice) DidDiscoverServices(peripheral *swift.CBPeripheral, services []*swift.CBService, err error) {
	if err != nil {
		fmt.Printf("[iOS] ❌ Service discovery failed: %v\n", err)
		return
	}
	fmt.Printf("[iOS] Discovered %d services\n", len(services))
	for _, service := range services {
		fmt.Printf("[iOS]   - Service %s with %d characteristics\n", service.UUID, len(service.Characteristics))
	}
}

func (d *FakeIOSDevice) DidDiscoverCharacteristics(peripheral *swift.CBPeripheral, service *swift.CBService, err error) {
	// Silent - characteristics discovered with services
}

func (d *FakeIOSDevice) DidWriteValueForCharacteristic(peripheral *swift.CBPeripheral, characteristic *swift.CBCharacteristic, err error) {
	if err != nil {
		fmt.Printf("[iOS] ❌ Write failed: %v\n", err)
	}
}

func (d *FakeIOSDevice) DidUpdateValueForCharacteristic(peripheral *swift.CBPeripheral, characteristic *swift.CBCharacteristic, err error) {
	if err != nil {
		fmt.Printf("[iOS] ❌ Read failed: %v\n", err)
	} else {
		fmt.Printf("[iOS] 📩 RECEIVED on char %s: \"%s\"\n", characteristic.UUID, string(characteristic.Value))

		// Send response back
		time.Sleep(500 * time.Millisecond)
		responseChar := d.peripheral.GetCharacteristic("1800", "2A00")
		if responseChar != nil {
			d.peripheral.WriteValue([]byte("hello from iOS"), responseChar)
			fmt.Printf("[iOS] 📤 SENT: \"hello from iOS\"\n")
		}
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

		// Discover services first
		gatt.DiscoverServices()

		// Start listening for incoming data
		gatt.StartListening()
	} else if newState == 0 { // STATE_DISCONNECTED
		fmt.Printf("[Android] Disconnected from iOS device\n")
	}
}

func (d *FakeAndroidDevice) OnServicesDiscovered(gatt *kotlin.BluetoothGatt, status int) {
	if status == 0 {
		services := gatt.GetServices()
		fmt.Printf("[Android] Discovered %d services\n", len(services))
		for _, service := range services {
			fmt.Printf("[Android]   - Service %s with %d characteristics\n", service.UUID, len(service.Characteristics))
		}

		// Send "hi" message using the first writable characteristic
		time.Sleep(1 * time.Second)
		char := gatt.GetCharacteristic("1800", "2A00")
		if char != nil {
			char.Value = []byte("hi from Android")
			gatt.WriteCharacteristic(char)
			fmt.Printf("[Android] 📤 SENT: \"hi from Android\"\n")
		}
	} else {
		fmt.Printf("[Android] ❌ Service discovery failed with status %d\n", status)
	}
}

func (d *FakeAndroidDevice) OnCharacteristicWrite(gatt *kotlin.BluetoothGatt, characteristic *kotlin.BluetoothGattCharacteristic, status int) {
	// Silent - success already logged, only log failures
	if status != 0 {
		fmt.Printf("[Android] ❌ Write failed with status %d\n", status)
	}
}

func (d *FakeAndroidDevice) OnCharacteristicRead(gatt *kotlin.BluetoothGatt, characteristic *kotlin.BluetoothGattCharacteristic, status int) {
	if status == 0 {
		fmt.Printf("[Android] 📩 RECEIVED on char %s: \"%s\"\n", characteristic.UUID, string(characteristic.Value))
	} else {
		fmt.Printf("[Android] ❌ Read failed with status %d\n", status)
	}
}

func (d *FakeAndroidDevice) OnCharacteristicChanged(gatt *kotlin.BluetoothGatt, characteristic *kotlin.BluetoothGattCharacteristic) {
	fmt.Printf("[Android] 📩 NOTIFICATION on char %s: \"%s\"\n", characteristic.UUID, string(characteristic.Value))
}

func main() {
	fmt.Println("=== Fake Bluetooth Communication ===\n")

	// Clean up old device directories from previous runs
	os.RemoveAll("data/")

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

	// Create GATT tables for both devices
	// iOS device GATT table (Generic Access Service)
	iosGATT := &wire.GATTTable{
		Services: []wire.GATTService{
			{
				UUID: "1800", // Generic Access Service
				Type: "primary",
				Characteristics: []wire.GATTCharacteristic{
					{
						UUID:       "2A00", // Device Name characteristic
						Properties: []string{"read", "write"},
					},
					{
						UUID:       "2A01", // Appearance characteristic
						Properties: []string{"read"},
					},
				},
			},
		},
	}

	// Android device GATT table (Generic Access Service)
	androidGATT := &wire.GATTTable{
		Services: []wire.GATTService{
			{
				UUID: "1800", // Generic Access Service
				Type: "primary",
				Characteristics: []wire.GATTCharacteristic{
					{
						UUID:       "2A00", // Device Name characteristic
						Properties: []string{"read", "write", "notify"},
					},
				},
			},
		},
	}

	// Write GATT tables to filesystem
	if err := iosWire.WriteGATTTable(iosGATT); err != nil {
		panic(err)
	}
	if err := androidWire.WriteGATTTable(androidGATT); err != nil {
		panic(err)
	}

	fmt.Println("✓ Initialized device GATT tables\n")

	// iOS starts listening for peripherals
	iosDevice.manager.ScanForPeripherals(nil, nil)

	// Android starts scanning for devices
	androidDevice.manager.Adapter.GetBluetoothLeScanner().StartScan(androidDevice)

	// Wait for devices to discover each other, connect, and exchange data
	// Discovery (1s) + Connection (instant) + Service Discovery + Message Exchange (1-2s)
	time.Sleep(4 * time.Second)

	fmt.Println("\n=== Done ===")
}
