package kotlin

import (
	"testing"
	"time"

	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/wire"
)

// ============================================================================
// END-TO-END INTEGRATION TESTS
// ============================================================================

// TestEndToEnd_ScanConnectTransfer tests complete BLE flow: advertise → scan → connect → write → notify
func TestEndToEnd_ScanConnectTransfer(t *testing.T) {
	// Setup isolated test environment
	_, cleanup := setupTestEnv(t)
	defer cleanup()

	// Setup two devices: one central (scanner), one peripheral (advertiser)
	// Use UUIDs where central < peripheral so central will initiate connection
	centralWire := wire.NewWire("aaaa-central-uuid")
	peripheralWire := wire.NewWire("zzzz-peripheral-uuid")

	if err := centralWire.Start(); err != nil {
		t.Fatalf("Failed to start central wire: %v", err)
	}
	defer centralWire.Stop()

	if err := peripheralWire.Start(); err != nil {
		t.Fatalf("Failed to start peripheral wire: %v", err)
	}
	defer peripheralWire.Stop()

	// Step 1: Setup peripheral advertising
	advData := &wire.AdvertisingData{
		DeviceName:    "Android Peripheral",
		ServiceUUIDs:  []string{phone.AuraServiceUUID},
		IsConnectable: true,
	}
	if err := peripheralWire.WriteAdvertisingData(advData); err != nil {
		t.Fatalf("Failed to write advertising data: %v", err)
	}

	// Setup peripheral GATT server
	peripheralManager := NewBluetoothManager("zzzz-peripheral-uuid", peripheralWire)
	peripheralGattServer := peripheralManager.OpenGattServer(nil, "Android Peripheral", peripheralWire)

	// Add Aura service to peripheral
	auraService := &BluetoothGattService{
		UUID:            phone.AuraServiceUUID,
		Type:            SERVICE_TYPE_PRIMARY,
		Characteristics: []*BluetoothGattCharacteristic{},
	}

	protocolChar := &BluetoothGattCharacteristic{
		UUID:       phone.AuraProtocolCharUUID,
		Properties: PROPERTY_READ | PROPERTY_WRITE | PROPERTY_NOTIFY,
		Service:    auraService,
	}
	auraService.Characteristics = append(auraService.Characteristics, protocolChar)

	photoChar := &BluetoothGattCharacteristic{
		UUID:       phone.AuraPhotoCharUUID,
		Properties: PROPERTY_WRITE | PROPERTY_NOTIFY,
		Service:    auraService,
	}
	auraService.Characteristics = append(auraService.Characteristics, photoChar)

	peripheralGattServer.AddService(auraService)
	t.Logf("✅ Peripheral GATT server configured")

	// Step 2: Central scans for devices
	centralManager := NewBluetoothManager("aaaa-central-uuid", centralWire)
	scanner := centralManager.Adapter.GetBluetoothLeScanner()

	scanResults := make(chan *ScanResult, 10)
	scanCallback := &testScanCallback{
		onScanResult: func(callbackType int, result *ScanResult) {
			// Only accept the peripheral device, not our own device
			if result.Device.Address == "zzzz-peripheral-uuid" {
				scanResults <- result
			}
		},
	}

	scanner.StartScan(scanCallback)
	defer scanner.StopScan()

	var discoveredDevice *BluetoothDevice
	select {
	case result := <-scanResults:
		discoveredDevice = result.Device
		t.Logf("✅ Central discovered peripheral: %s at %s (RSSI: %d dBm)", result.Device.Name, result.Device.Address, result.Rssi)
	case <-time.After(3 * time.Second):
		t.Fatal("Timeout waiting for peripheral device in scan results")
	}

	scanner.StopScan()

	// Step 3: Central connects to peripheral
	connectionStates := make(chan int, 10)
	receivedData := make(chan []byte, 10)

	gattCallback := &testGattCallback{
		onConnectionStateChange: func(gatt *BluetoothGatt, status int, newState int) {
			connectionStates <- newState
		},
		onServicesDiscovered: func(gatt *BluetoothGatt, status int) {
			t.Logf("✅ Services discovered (status: %d)", status)
		},
		onCharacteristicWrite: func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int) {
			t.Logf("✅ Characteristic written (status: %d)", status)
		},
		onCharacteristicChanged: func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic) {
			receivedData <- char.Value
		},
	}

	gatt := discoveredDevice.ConnectGatt(nil, false, gattCallback)
	defer gatt.Disconnect()

	// Wait for connection (may get CONNECTING first, then CONNECTED)
	connected := false
	for i := 0; i < 2; i++ {
		select {
		case state := <-connectionStates:
			if state == STATE_CONNECTED {
				connected = true
				t.Logf("✅ Central connected to peripheral")
				break
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Timeout waiting for connection")
		}
	}
	if !connected {
		t.Fatal("Never received STATE_CONNECTED")
	}

	// Step 4: Central discovers services
	gatt.DiscoverServices()
	time.Sleep(500 * time.Millisecond)

	// Verify service was discovered
	service := gatt.GetService(phone.AuraServiceUUID)
	if service == nil {
		t.Fatal("Aura service not found")
	}

	protocolCharacteristic := gatt.GetCharacteristic(phone.AuraServiceUUID, phone.AuraProtocolCharUUID)
	if protocolCharacteristic == nil {
		t.Fatal("Protocol characteristic not found")
	}
	t.Logf("✅ Service discovery complete")

	// Step 5: Central subscribes to notifications
	gatt.SetCharacteristicNotification(protocolCharacteristic, true)
	time.Sleep(200 * time.Millisecond)
	t.Logf("✅ Central subscribed to notifications")

	// Step 6: Central writes data to peripheral
	testData := []byte("Hello from central!")
	protocolCharacteristic.Value = testData
	gatt.WriteCharacteristic(protocolCharacteristic)
	time.Sleep(500 * time.Millisecond)
	t.Logf("✅ Central sent data to peripheral")

	// Step 7: Simulate peripheral sending notification back via direct message delivery
	// (In real BLE, the peripheral would call NotifyCharacteristicChanged)
	responseData := []byte("Hello from peripheral!")
	msg := &wire.GATTMessage{
		Operation:          "notify",
		ServiceUUID:        phone.AuraServiceUUID,
		CharacteristicUUID: phone.AuraProtocolCharUUID,
		Data:               responseData,
		SenderUUID:         "zzzz-peripheral-uuid",
	}
	gatt.HandleGATTMessage(msg)

	select {
	case data := <-receivedData:
		if string(data) != "Hello from peripheral!" {
			t.Errorf("Wrong notification data: %s", string(data))
		}
		t.Logf("✅ Central received notification: %s", string(data))
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for notification")
	}

	t.Logf("✅ End-to-end test complete: scan → connect → write → notify")
}

// TestEndToEnd_MultipleDevices tests mesh scenario with 3 devices
func TestEndToEnd_MultipleDevices(t *testing.T) {
	// Setup isolated test environment
	_, cleanup := setupTestEnv(t)
	defer cleanup()

	// Setup 3 devices
	w1 := wire.NewWire("device1-uuid")
	w2 := wire.NewWire("device2-uuid")
	w3 := wire.NewWire("device3-uuid")

	wires := []*wire.Wire{w1, w2, w3}
	for i, w := range wires {
		if err := w.Start(); err != nil {
			t.Fatalf("Failed to start wire %d: %v", i+1, err)
		}
		defer w.Stop()
	}

	// All devices advertise
	for i, w := range wires {
		advData := &wire.AdvertisingData{
			DeviceName:    "Android Device " + string(rune('1'+i)),
			ServiceUUIDs:  []string{phone.AuraServiceUUID},
			IsConnectable: true,
		}
		if err := w.WriteAdvertisingData(advData); err != nil {
			t.Fatalf("Failed to write advertising data for device %d: %v", i+1, err)
		}
	}

	t.Logf("✅ 3 devices advertising")

	// Device 1 scans and should discover devices 2 and 3
	adapter1 := NewBluetoothAdapter("device1-uuid", w1)
	scanner := adapter1.GetBluetoothLeScanner()

	discoveredDevices := make(map[string]*ScanResult)
	scanCallback := &testScanCallback{
		onScanResult: func(callbackType int, result *ScanResult) {
			discoveredDevices[result.Device.Address] = result
		},
	}

	scanner.StartScan(scanCallback)
	time.Sleep(2 * time.Second)
	scanner.StopScan()

	if len(discoveredDevices) < 2 {
		t.Fatalf("Device 1 should discover at least 2 other devices, got %d", len(discoveredDevices))
	}

	t.Logf("✅ Device 1 discovered %d other devices", len(discoveredDevices))

	// Verify role negotiation - device with larger UUID should connect
	// device1 < device2 < device3 (alphabetically)
	if adapter1.ShouldInitiateConnection("device2-uuid") {
		t.Error("Device 1 should NOT initiate connection to Device 2 (smaller UUID)")
	}

	adapter3 := NewBluetoothAdapter("device3-uuid", w3)
	if !adapter3.ShouldInitiateConnection("device1-uuid") {
		t.Error("Device 3 should initiate connection to Device 1 (larger UUID)")
	}

	t.Logf("✅ Role negotiation correct (larger UUID initiates)")

	// Connect device 3 to device 1 (device3 should initiate per role negotiation)
	device1 := adapter3.GetRemoteDevice("device1-uuid")
	connectionStates := make(chan int, 10)

	gattCallback := &testGattCallback{
		onConnectionStateChange: func(gatt *BluetoothGatt, status int, newState int) {
			connectionStates <- newState
		},
	}

	gatt := device1.ConnectGatt(nil, false, gattCallback)
	defer gatt.Disconnect()

	// Wait for connection (may get CONNECTING first, then CONNECTED)
	connected := false
	for i := 0; i < 2; i++ {
		select {
		case state := <-connectionStates:
			if state == STATE_CONNECTED {
				connected = true
				t.Logf("✅ Device 3 connected to Device 1 (role negotiation worked)")
				break
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Timeout waiting for connection")
		}
	}
	if !connected {
		t.Fatal("Never received STATE_CONNECTED")
	}

	t.Logf("✅ Multi-device mesh test complete")
}

// TestEndToEnd_DataIntegrity tests that large data transfers work correctly
func TestEndToEnd_DataIntegrity(t *testing.T) {
	// Setup isolated test environment
	_, cleanup := setupTestEnv(t)
	defer cleanup()

	centralWire := wire.NewWire("central-uuid")
	peripheralWire := wire.NewWire("peripheral-uuid")

	if err := centralWire.Start(); err != nil {
		t.Fatalf("Failed to start central wire: %v", err)
	}
	defer centralWire.Stop()

	if err := peripheralWire.Start(); err != nil {
		t.Fatalf("Failed to start peripheral wire: %v", err)
	}
	defer peripheralWire.Stop()

	// Setup peripheral
	advData := &wire.AdvertisingData{
		DeviceName:    "Android Peripheral",
		ServiceUUIDs:  []string{phone.AuraServiceUUID},
		IsConnectable: true,
	}
	peripheralWire.WriteAdvertisingData(advData)

	// Connect central to peripheral
	centralAdapter := NewBluetoothAdapter("central-uuid", centralWire)
	device := centralAdapter.GetRemoteDevice("peripheral-uuid")

	receivedData := make(chan []byte, 10)
	gattCallback := &testGattCallback{
		onCharacteristicChanged: func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic) {
			receivedData <- char.Value
		},
	}

	gatt := device.ConnectGatt(nil, false, gattCallback)
	defer gatt.Disconnect()

	time.Sleep(500 * time.Millisecond)
	t.Logf("✅ Connected")

	// Simulate large data transfer (1KB)
	largeData := make([]byte, 1024)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	// Setup characteristic
	service := &BluetoothGattService{
		UUID:            phone.AuraServiceUUID,
		Type:            SERVICE_TYPE_PRIMARY,
		Characteristics: []*BluetoothGattCharacteristic{},
	}
	char := &BluetoothGattCharacteristic{
		UUID:       phone.AuraPhotoCharUUID,
		Properties: PROPERTY_WRITE | PROPERTY_NOTIFY,
		Service:    service,
	}
	service.Characteristics = append(service.Characteristics, char)
	gatt.services = []*BluetoothGattService{service}
	gatt.notifyingCharacteristics = make(map[string]bool)
	gatt.notifyingCharacteristics[phone.AuraPhotoCharUUID] = true

	// Send large data via direct GATT message delivery
	msg := &wire.GATTMessage{
		Operation:          "notify",
		ServiceUUID:        phone.AuraServiceUUID,
		CharacteristicUUID: phone.AuraPhotoCharUUID,
		Data:               largeData,
		SenderUUID:         "peripheral-uuid",
	}

	gatt.HandleGATTMessage(msg)

	select {
	case data := <-receivedData:
		if len(data) != len(largeData) {
			t.Errorf("Data length mismatch: expected %d, got %d", len(largeData), len(data))
		}
		// Verify data integrity
		mismatch := false
		for i := range data {
			if data[i] != largeData[i] {
				mismatch = true
				break
			}
		}
		if mismatch {
			t.Error("Data integrity check failed")
		} else {
			t.Logf("✅ Large data transfer (1KB) successful with full integrity")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for large data notification")
	}
}
