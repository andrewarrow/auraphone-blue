package main

import (
	"fmt"
	"image/color"
	"os"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	"github.com/user/auraphone-blue/kotlin"
	"github.com/user/auraphone-blue/swift"
	"github.com/user/auraphone-blue/tests"
	"github.com/user/auraphone-blue/wire"
)

type PhoneGUI struct {
	statusLabel *widget.Label
	photoImage  *canvas.Image
	logText     *widget.Label
	deviceName  string
}

func NewPhoneGUI(deviceName string) *PhoneGUI {
	return &PhoneGUI{
		statusLabel: widget.NewLabel("Idle"),
		photoImage:  canvas.NewImageFromFile(""),
		logText:     widget.NewLabel(""),
		deviceName:  deviceName,
	}
}

func (pg *PhoneGUI) UpdateStatus(status string) {
	pg.statusLabel.SetText(status)
}

func (pg *PhoneGUI) UpdatePhoto(photoPath string) {
	if photoPath != "" {
		pg.photoImage = canvas.NewImageFromFile(photoPath)
		pg.photoImage.FillMode = canvas.ImageFillContain
		pg.photoImage.Refresh()
	}
}

func (pg *PhoneGUI) AddLog(message string) {
	current := pg.logText.Text
	if current != "" {
		current += "\n"
	}
	pg.logText.SetText(current + message)
}

func (pg *PhoneGUI) Build() *fyne.Container {
	// Phone header
	header := widget.NewLabelWithStyle(pg.deviceName, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	// Status display
	statusBox := container.NewVBox(
		widget.NewLabel("Status:"),
		pg.statusLabel,
	)

	// Photo display area (300x300)
	photoRect := canvas.NewRectangle(color.RGBA{R: 240, G: 240, B: 240, A: 255})
	photoRect.SetMinSize(fyne.NewSize(300, 300))

	photoContainer := container.NewMax(photoRect, pg.photoImage)

	// Event log (scrollable)
	logScroll := container.NewVScroll(pg.logText)
	logScroll.SetMinSize(fyne.NewSize(300, 200))

	// Main phone container
	phoneBox := container.NewVBox(
		header,
		widget.NewSeparator(),
		statusBox,
		widget.NewLabel("Photo:"),
		photoContainer,
		widget.NewLabel("Events:"),
		logScroll,
	)

	// Add border
	bordered := container.NewPadded(phoneBox)

	return bordered
}

type FakeIOSDevice struct {
	manager        *swift.CBCentralManager
	uuid           string
	peripheral     *swift.CBPeripheral
	discoveredOnce bool
	gui            *PhoneGUI
	photoPath      string
}

func NewFakeIOSDevice(gui *PhoneGUI) *FakeIOSDevice {
	d := &FakeIOSDevice{
		uuid: uuid.New().String(),
		gui:  gui,
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
		msg := fmt.Sprintf("Discovered: %s", peripheral.Name)
		d.gui.AddLog(msg)
		d.gui.UpdateStatus("Discovered")
		fmt.Printf("[iOS] Discovered Android device: %s\n", peripheral.Name)
		if advertisementData != nil {
			if name, ok := advertisementData["kCBAdvDataLocalName"].(string); ok {
				fmt.Printf("[iOS]   - Device Name: %s\n", name)
			}
			if services, ok := advertisementData["kCBAdvDataServiceUUIDs"].([]string); ok && len(services) > 0 {
				fmt.Printf("[iOS]   - Service UUIDs: %v\n", services)
			}
			if txPower, ok := advertisementData["kCBAdvDataTxPowerLevel"].(int); ok {
				fmt.Printf("[iOS]   - TX Power: %d dBm\n", txPower)
			}
			if isConnectable, ok := advertisementData["kCBAdvDataIsConnectable"].(bool); ok {
				fmt.Printf("[iOS]   - Connectable: %v\n", isConnectable)
			}
		}
		d.gui.AddLog("Connecting...")
		d.gui.UpdateStatus("Connecting")
		fmt.Printf("[iOS] Connecting to Android device...\n")
		d.manager.Connect(&peripheral, nil)
	}
}

func (d *FakeIOSDevice) DidConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral) {
	d.gui.AddLog("Connected!")
	d.gui.UpdateStatus("Connected")
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
		d.gui.AddLog("Service discovery failed")
		fmt.Printf("[iOS] ❌ Service discovery failed: %v\n", err)
		return
	}
	d.gui.AddLog(fmt.Sprintf("Found %d services", len(services)))
	fmt.Printf("[iOS] Discovered %d services\n", len(services))
	for _, service := range services {
		fmt.Printf("[iOS]   - Service %s with %d characteristics\n", service.UUID, len(service.Characteristics))
	}

	// If we have a photo, send it after handshake
	if d.photoPath != "" {
		time.Sleep(1 * time.Second)
		const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"
		photoChar := d.peripheral.GetCharacteristic("E621E1F8-C36C-495A-93FC-0C247A3E6E5F", auraPhotoCharUUID)
		if photoChar != nil {
			photoData, err := os.ReadFile(d.photoPath)
			if err == nil {
				d.gui.AddLog("Sending photo...")
				d.gui.UpdateStatus("Sending Photo")
				d.peripheral.WriteValue(photoData, photoChar)
				d.gui.AddLog("Photo sent!")
				d.gui.UpdateStatus("Photo Sent")
				fmt.Printf("[iOS] 📤 SENT: Photo (%d bytes)\n", len(photoData))
			}
		}
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
		d.gui.AddLog("Read failed")
		fmt.Printf("[iOS] ❌ Read failed: %v\n", err)
	} else {
		// Check if this is a photo characteristic (contains binary data)
		const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"
		if characteristic.UUID == auraPhotoCharUUID {
			d.gui.AddLog("Receiving photo...")
			d.gui.UpdateStatus("Receiving Photo")
			// Save photo and display it
			photoPath := "data/received_photo.png"
			if err := os.WriteFile(photoPath, characteristic.Value, 0644); err == nil {
				d.gui.UpdatePhoto(photoPath)
				d.gui.AddLog("Photo received!")
				d.gui.UpdateStatus("Photo Received")
			}
		} else {
			msg := fmt.Sprintf("Received: %s", string(characteristic.Value))
			d.gui.AddLog(msg)
			fmt.Printf("[iOS] 📩 RECEIVED on char %s: \"%s\"\n", characteristic.UUID, string(characteristic.Value))
		}

		// Send response back
		time.Sleep(500 * time.Millisecond)
		responseChar := d.peripheral.GetCharacteristic("1800", "2A00")
		if responseChar != nil {
			d.peripheral.WriteValue([]byte("hello from iOS"), responseChar)
			d.gui.AddLog("Sent: hello from iOS")
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
	gui             *PhoneGUI
	photoPath       string
}

func NewFakeAndroidDevice(gui *PhoneGUI, photoPath string) *FakeAndroidDevice {
	d := &FakeAndroidDevice{
		uuid:      uuid.New().String(),
		gui:       gui,
		photoPath: photoPath,
	}
	d.manager = kotlin.NewBluetoothManager(d.uuid)

	// If device has a photo, display it
	if photoPath != "" {
		gui.UpdatePhoto(photoPath)
	}

	return d
}

func (d *FakeAndroidDevice) OnScanResult(callbackType int, result *kotlin.ScanResult) {
	// Connect to the first discovered device
	if !d.discoveredOnce {
		d.discoveredOnce = true
		d.connectedDevice = result.Device
		msg := fmt.Sprintf("Discovered: %s", result.Device.Name)
		d.gui.AddLog(msg)
		d.gui.UpdateStatus("Discovered")
		fmt.Printf("[Android] Discovered iOS device: %s\n", result.Device.Name)
		if result.ScanRecord != nil {
			if result.ScanRecord.DeviceName != "" {
				fmt.Printf("[Android]   - Device Name: %s\n", result.ScanRecord.DeviceName)
			}
			if len(result.ScanRecord.ServiceUUIDs) > 0 {
				fmt.Printf("[Android]   - Service UUIDs: %v\n", result.ScanRecord.ServiceUUIDs)
			}
			if result.ScanRecord.TxPowerLevel != nil {
				fmt.Printf("[Android]   - TX Power: %d dBm\n", *result.ScanRecord.TxPowerLevel)
			}
			if len(result.ScanRecord.ManufacturerData) > 0 {
				fmt.Printf("[Android]   - Manufacturer Data: %v\n", result.ScanRecord.ManufacturerData)
			}
		}
		d.gui.AddLog("Connecting...")
		d.gui.UpdateStatus("Connecting")
		fmt.Printf("[Android] Connecting to iOS device...\n")

		// Connect to GATT
		d.gatt = result.Device.ConnectGatt(nil, false, d)
	}
}

func (d *FakeAndroidDevice) OnConnectionStateChange(gatt *kotlin.BluetoothGatt, status int, newState int) {
	if newState == 2 { // STATE_CONNECTED
		d.gui.AddLog("Connected!")
		d.gui.UpdateStatus("Connected")
		fmt.Printf("[Android] Connected to iOS device\n")

		// Discover services first
		gatt.DiscoverServices()

		// Start listening for incoming data
		gatt.StartListening()
	} else if newState == 0 { // STATE_DISCONNECTED
		d.gui.AddLog("Disconnected")
		d.gui.UpdateStatus("Disconnected")
		fmt.Printf("[Android] Disconnected from iOS device\n")
	}
}

func (d *FakeAndroidDevice) OnServicesDiscovered(gatt *kotlin.BluetoothGatt, status int) {
	if status == 0 {
		services := gatt.GetServices()
		d.gui.AddLog(fmt.Sprintf("Found %d services", len(services)))
		d.gui.AddLog("Waiting for photo...")
		d.gui.UpdateStatus("Waiting for Photo")
		fmt.Printf("[Android] Discovered %d services\n", len(services))
		for _, service := range services {
			fmt.Printf("[Android]   - Service %s with %d characteristics\n", service.UUID, len(service.Characteristics))
		}
	} else {
		d.gui.AddLog("Service discovery failed")
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
		// Check if this is a photo characteristic
		const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"
		if characteristic.UUID == auraPhotoCharUUID {
			d.gui.AddLog("Receiving photo...")
			d.gui.UpdateStatus("Receiving Photo")
			// Save photo and display it
			photoPath := "data/bob_received_photo.png"
			if err := os.WriteFile(photoPath, characteristic.Value, 0644); err == nil {
				d.gui.UpdatePhoto(photoPath)
				d.gui.AddLog("Photo received!")
				d.gui.UpdateStatus("Photo Received")
				fmt.Printf("[Android] 📩 RECEIVED: Photo (%d bytes)\n", len(characteristic.Value))
			}
		} else {
			msg := fmt.Sprintf("Received: %s", string(characteristic.Value))
			d.gui.AddLog(msg)
			fmt.Printf("[Android] 📩 RECEIVED on char %s: \"%s\"\n", characteristic.UUID, string(characteristic.Value))
		}
	} else {
		d.gui.AddLog("Read failed")
		fmt.Printf("[Android] ❌ Read failed with status %d\n", status)
	}
}

func (d *FakeAndroidDevice) OnCharacteristicChanged(gatt *kotlin.BluetoothGatt, characteristic *kotlin.BluetoothGattCharacteristic) {
	fmt.Printf("[Android] 📩 NOTIFICATION on char %s: \"%s\"\n", characteristic.UUID, string(characteristic.Value))
}

func main() {
	// Load the photo handshake scenario
	scenario, err := tests.LoadScenario("scenarios/photo_handshake.json")
	if err != nil {
		fmt.Printf("Failed to load scenario: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("=== Running Scenario: %s ===\n", scenario.Name)
	fmt.Printf("%s\n\n", scenario.Description)

	// Clean up old device directories from previous runs
	os.RemoveAll("data/")

	// Create Fyne app
	myApp := app.New()
	myWindow := myApp.NewWindow("Auraphone Blue - Photo Handshake Simulator")

	// Get device configs from scenario
	aliceConfig := scenario.GetDeviceByID("ios-alice")
	bobConfig := scenario.GetDeviceByID("android-bob")

	// Create phone GUIs
	aliceGUI := NewPhoneGUI(aliceConfig.DeviceName + "\n(Alice)")
	bobGUI := NewPhoneGUI(bobConfig.DeviceName + "\n(Bob)")

	// Create fake devices with GUIs
	// Alice (iOS) has the photo, Bob (Android) doesn't
	iosDevice := NewFakeIOSDevice(aliceGUI)
	androidDevice := NewFakeAndroidDevice(bobGUI, "")

	// Alice should display her photo from the start
	if aliceConfig.Profile.PhotoPath != "" {
		aliceGUI.UpdatePhoto(aliceConfig.Profile.PhotoPath)
	}

	// Store photo path for iOS device to send later
	iosDevice.photoPath = aliceConfig.Profile.PhotoPath

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
	const auraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	const auraTextCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"
	const auraPhotoCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E"

	gattTable := &wire.GATTTable{
		Services: []wire.GATTService{
			{
				UUID: auraServiceUUID,
				Type: "primary",
				Characteristics: []wire.GATTCharacteristic{
					{
						UUID:       auraTextCharUUID,
						Properties: []string{"read", "write", "notify"},
					},
					{
						UUID:       auraPhotoCharUUID,
						Properties: []string{"write", "notify"},
					},
				},
			},
			{
				UUID: "1800",
				Type: "primary",
				Characteristics: []wire.GATTCharacteristic{
					{
						UUID:       "2A00",
						Properties: []string{"read", "write", "notify"},
					},
				},
			},
		},
	}

	if err := iosWire.WriteGATTTable(gattTable); err != nil {
		panic(err)
	}
	if err := androidWire.WriteGATTTable(gattTable); err != nil {
		panic(err)
	}

	// Create advertising data
	txPowerLevel := 0
	iosAdvertising := &wire.AdvertisingData{
		DeviceName:    aliceConfig.DeviceName,
		ServiceUUIDs:  []string{auraServiceUUID},
		TxPowerLevel:  &txPowerLevel,
		IsConnectable: true,
	}
	androidAdvertising := &wire.AdvertisingData{
		DeviceName:    bobConfig.DeviceName,
		ServiceUUIDs:  []string{auraServiceUUID},
		TxPowerLevel:  &txPowerLevel,
		IsConnectable: true,
	}

	if err := iosWire.WriteAdvertisingData(iosAdvertising); err != nil {
		panic(err)
	}
	if err := androidWire.WriteAdvertisingData(androidAdvertising); err != nil {
		panic(err)
	}

	aliceGUI.AddLog("Ready to discover...")
	aliceGUI.UpdateStatus("Scanning")
	bobGUI.AddLog("Ready to discover...")
	bobGUI.UpdateStatus("Advertising")

	// Build GUI layout
	phonesContainer := container.New(
		layout.NewHBoxLayout(),
		aliceGUI.Build(),
		widget.NewSeparator(),
		bobGUI.Build(),
	)

	scenarioInfo := widget.NewLabel(fmt.Sprintf("Scenario: %s\n%s", scenario.Name, scenario.Description))
	scenarioInfo.Wrapping = fyne.TextWrapWord

	mainContainer := container.NewBorder(
		container.NewVBox(scenarioInfo, widget.NewSeparator()),
		nil, nil, nil,
		phonesContainer,
	)

	myWindow.SetContent(mainContainer)
	myWindow.Resize(fyne.NewSize(800, 700))

	// Start BLE simulation in background
	go func() {
		time.Sleep(500 * time.Millisecond)

		// iOS starts listening for peripherals
		iosDevice.manager.ScanForPeripherals(nil, nil)

		// Android starts scanning for devices
		androidDevice.manager.Adapter.GetBluetoothLeScanner().StartScan(androidDevice)
	}()

	myWindow.ShowAndRun()
}
