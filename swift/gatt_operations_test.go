package swift

import (
	"testing"
	"time"

	"github.com/user/auraphone-blue/wire"
)

// TestGATTWriteRequest tests that Central can write to Peripheral's characteristic
func TestGATTWriteRequest(t *testing.T) {
	// Create two devices with wire layers
	centralWire := wire.NewWire("central-uuid")
	peripheralWire := wire.NewWire("peripheral-uuid")

	// Start both wires
	if err := centralWire.Start(); err != nil {
		t.Fatalf("Failed to start central wire: %v", err)
	}
	defer centralWire.Stop()

	if err := peripheralWire.Start(); err != nil {
		t.Fatalf("Failed to start peripheral wire: %v", err)
	}
	defer peripheralWire.Stop()

	// Create test delegates
	centralDelegate := &testCentralDelegate{
		t:               t,
		didConnect:      make(chan *CBPeripheral, 1),
		didDiscoverSvcs: make(chan *CBPeripheral, 1),
		didWriteValue:   make(chan *CBCharacteristic, 1),
	}

	peripheralObjDelegate := &testPeripheralDelegate{
		t:               t,
		didDiscoverSvcs: make(chan []*CBService, 1),
		didWriteValue:   make(chan *CBCharacteristic, 1),
	}

	peripheralMgrDelegate := &testPeripheralManagerDelegate{
		t:                 t,
		didReceiveWrite:   make(chan []byte, 1),
		didStartAdvertise: make(chan bool, 1),
	}

	// Create CBCentralManager and CBPeripheralManager
	centralManager := NewCBCentralManager(centralDelegate, "central-uuid", centralWire)
	peripheralManager := NewCBPeripheralManager(peripheralMgrDelegate, "peripheral-uuid", "Test Peripheral", peripheralWire)

	// Set up GATT message routing between wires and managers
	centralWire.SetGATTMessageHandler(func(peerUUID string, msg *wire.GATTMessage) {
		// Route messages from peripheral to central manager
		centralManager.HandleGATTMessage(peerUUID, msg)
	})

	peripheralWire.SetGATTMessageHandler(func(peerUUID string, msg *wire.GATTMessage) {
		// Route messages from central to peripheral manager
		peripheralManager.HandleGATTMessage(peerUUID, msg)
	})

	// Add service to peripheral
	testService := &CBMutableService{
		UUID:      "test-service-uuid",
		IsPrimary: true,
		Characteristics: []*CBMutableCharacteristic{
			{
				UUID:        "test-char-uuid",
				Properties:  CBCharacteristicPropertyWrite | CBCharacteristicPropertyRead,
				Permissions: CBAttributePermissionsWriteable | CBAttributePermissionsReadable,
			},
		},
	}

	err := peripheralManager.AddService(testService)
	if err != nil {
		t.Fatalf("Failed to add service: %v", err)
	}

	// Start advertising
	err = peripheralManager.StartAdvertising(map[string]interface{}{
		"kCBAdvDataLocalName":    "Test Peripheral",
		"kCBAdvDataServiceUUIDs": []string{"test-service-uuid"},
	})
	if err != nil {
		t.Fatalf("Failed to start advertising: %v", err)
	}

	// Wait for advertising to start
	select {
	case <-peripheralMgrDelegate.didStartAdvertise:
		t.Logf("✅ Peripheral started advertising")
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for advertising to start")
	}

	time.Sleep(200 * time.Millisecond)

	// Central connects to peripheral
	peripheral := &CBPeripheral{
		UUID:     "peripheral-uuid",
		Name:     "Test Peripheral",
		Delegate: peripheralObjDelegate,
	}

	centralManager.Connect(peripheral, nil)

	// Wait for connection
	select {
	case connectedPeripheral := <-centralDelegate.didConnect:
		t.Logf("✅ Central connected to peripheral: %s", connectedPeripheral.UUID)
		// Don't reassign peripheral - keep using the original that's stored in pendingPeripherals
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for connection")
	}

	// Discover services
	peripheral.DiscoverServices([]string{"test-service-uuid"})

	// Wait for service discovery
	select {
	case services := <-peripheralObjDelegate.didDiscoverSvcs:
		if len(services) == 0 {
			t.Fatal("No services discovered")
		}
		t.Logf("✅ Discovered %d service(s)", len(services))
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for service discovery")
	}

	// Get the characteristic
	char := peripheral.GetCharacteristic("test-service-uuid", "test-char-uuid")
	if char == nil {
		t.Fatal("Failed to get test characteristic")
	}

	// Start write queue
	peripheral.StartWriteQueue()
	defer peripheral.StopWriteQueue()

	// Write to characteristic
	testData := []byte("Hello from Central!")
	err = peripheral.WriteValue(testData, char, CBCharacteristicWriteWithResponse)
	if err != nil {
		t.Fatalf("Failed to write value: %v", err)
	}

	// Wait for write confirmation at central (peripheral object delegate, not central manager delegate)
	select {
	case writtenChar := <-peripheralObjDelegate.didWriteValue:
		t.Logf("✅ Central received write confirmation for char: %s", writtenChar.UUID)
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for write confirmation at central")
	}

	// Wait for write request at peripheral
	select {
	case receivedData := <-peripheralMgrDelegate.didReceiveWrite:
		if string(receivedData) != string(testData) {
			t.Errorf("Data mismatch: expected %s, got %s", string(testData), string(receivedData))
		}
		t.Logf("✅ Peripheral received write request with data: %s", string(receivedData))
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for write request at peripheral")
	}
}

// TestGATTNotification tests that Peripheral can send notifications to Central
func TestGATTNotification(t *testing.T) {
	// Create two devices with wire layers
	centralWire := wire.NewWire("central-uuid")
	peripheralWire := wire.NewWire("peripheral-uuid")

	// Start both wires
	if err := centralWire.Start(); err != nil {
		t.Fatalf("Failed to start central wire: %v", err)
	}
	defer centralWire.Stop()

	if err := peripheralWire.Start(); err != nil {
		t.Fatalf("Failed to start peripheral wire: %v", err)
	}
	defer peripheralWire.Stop()

	// Create test delegates
	centralDelegate := &testCentralDelegate{
		t:              t,
		didConnect:     make(chan *CBPeripheral, 1),
		didUpdateValue: make(chan []byte, 10), // Buffer for multiple notifications
	}

	peripheralObjDelegate := &testPeripheralDelegate{
		t:               t,
		didDiscoverSvcs: make(chan []*CBService, 1),
		didUpdateValue:  make(chan []byte, 10),
	}

	peripheralMgrDelegate := &testPeripheralManagerDelegate{
		t:                 t,
		didStartAdvertise: make(chan bool, 1),
		didSubscribe:      make(chan string, 1),
	}

	// Create managers
	centralManager := NewCBCentralManager(centralDelegate, "central-uuid", centralWire)
	peripheralManager := NewCBPeripheralManager(peripheralMgrDelegate, "peripheral-uuid", "Test Peripheral", peripheralWire)

	// Set up GATT message routing between wires and managers
	centralWire.SetGATTMessageHandler(func(peerUUID string, msg *wire.GATTMessage) {
		// Route messages from peripheral to central manager
		centralManager.HandleGATTMessage(peerUUID, msg)
	})

	peripheralWire.SetGATTMessageHandler(func(peerUUID string, msg *wire.GATTMessage) {
		// Route messages from central to peripheral manager
		peripheralManager.HandleGATTMessage(peerUUID, msg)
	})

	// Add service with notifiable characteristic
	testService := &CBMutableService{
		UUID:      "test-service-uuid",
		IsPrimary: true,
		Characteristics: []*CBMutableCharacteristic{
			{
				UUID:        "test-char-uuid",
				Properties:  CBCharacteristicPropertyNotify | CBCharacteristicPropertyRead,
				Permissions: CBAttributePermissionsReadable,
			},
		},
	}

	err := peripheralManager.AddService(testService)
	if err != nil {
		t.Fatalf("Failed to add service: %v", err)
	}

	// Start advertising
	err = peripheralManager.StartAdvertising(map[string]interface{}{
		"kCBAdvDataLocalName":    "Test Peripheral",
		"kCBAdvDataServiceUUIDs": []string{"test-service-uuid"},
	})
	if err != nil {
		t.Fatalf("Failed to start advertising: %v", err)
	}

	<-peripheralMgrDelegate.didStartAdvertise
	time.Sleep(200 * time.Millisecond)

	// Central connects
	peripheral := &CBPeripheral{
		UUID:     "peripheral-uuid",
		Name:     "Test Peripheral",
		Delegate: peripheralObjDelegate,
	}
	centralManager.Connect(peripheral, nil)

	// Wait for connection
	select {
	case <-centralDelegate.didConnect:
		t.Logf("✅ Connected")
		// Don't reassign peripheral - keep using the original that's stored in pendingPeripherals
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for connection")
	}

	// Discover services
	peripheral.DiscoverServices([]string{"test-service-uuid"})

	select {
	case services := <-peripheralObjDelegate.didDiscoverSvcs:
		if len(services) == 0 {
			t.Fatal("No services discovered")
		}
		t.Logf("✅ Services discovered")
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for service discovery")
	}

	// Get characteristic
	char := peripheral.GetCharacteristic("test-service-uuid", "test-char-uuid")
	if char == nil {
		t.Fatal("Failed to get test characteristic")
	}

	// Subscribe to notifications
	err = peripheral.SetNotifyValue(true, char)
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	// Wait for subscription at peripheral
	select {
	case charUUID := <-peripheralMgrDelegate.didSubscribe:
		if charUUID != "test-char-uuid" {
			t.Errorf("Wrong characteristic subscribed: %s", charUUID)
		}
		t.Logf("✅ Central subscribed to notifications")
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for subscription")
	}

	// Get the mutable characteristic from peripheral manager
	pmChar := peripheralManager.GetCharacteristic("test-service-uuid", "test-char-uuid")
	if pmChar == nil {
		t.Fatal("Failed to get mutable characteristic from peripheral manager")
	}

	// Send multiple notifications
	testData := [][]byte{
		[]byte("Notification 1"),
		[]byte("Notification 2"),
		[]byte("Notification 3"),
	}

	for i, data := range testData {
		success := peripheralManager.UpdateValue(data, pmChar, nil)
		if !success {
			t.Errorf("Failed to send notification %d", i+1)
		}
		time.Sleep(100 * time.Millisecond) // Small delay between notifications
	}

	t.Logf("✅ Peripheral sent %d notifications", len(testData))

	// Verify all notifications received at central (via peripheral object delegate)
	receivedCount := 0
	for i := 0; i < len(testData); i++ {
		select {
		case receivedData := <-peripheralObjDelegate.didUpdateValue:
			receivedCount++
			expectedData := testData[i]
			if string(receivedData) != string(expectedData) {
				t.Errorf("Notification %d mismatch: expected %s, got %s", i+1, string(expectedData), string(receivedData))
			}
			t.Logf("✅ Central received notification %d: %s", i+1, string(receivedData))
		case <-time.After(2 * time.Second):
			t.Fatalf("Timeout waiting for notification %d (received %d/%d)", i+1, receivedCount, len(testData))
		}
	}

	if receivedCount != len(testData) {
		t.Errorf("Expected %d notifications, got %d", len(testData), receivedCount)
	}
}

// TestReversePeripheralNotifications tests the scenario where:
// - Device A connects to Device B (A = Central, B = Peripheral)
// - B creates a "reverse" CBPeripheral object to make requests back to A
// - B subscribes to A's characteristics and receives notifications
// This is the exact scenario that fails in the bug report
func TestReversePeripheralNotifications(t *testing.T) {
	// Create two devices
	deviceAWire := wire.NewWire("device-a-uuid")
	deviceBWire := wire.NewWire("device-b-uuid")

	if err := deviceAWire.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceAWire.Stop()

	if err := deviceBWire.Start(); err != nil {
		t.Fatalf("Failed to start device B: %v", err)
	}
	defer deviceBWire.Stop()

	// Device A: Both Central and Peripheral managers
	deviceAPeripheralDelegate := &testPeripheralManagerDelegate{
		t:                 t,
		didStartAdvertise: make(chan bool, 1),
		didSubscribe:      make(chan string, 1),
	}
	deviceAPeripheralMgr := NewCBPeripheralManager(deviceAPeripheralDelegate, "device-a-uuid", "Device A", deviceAWire)

	// Add service to A with notifiable characteristic
	serviceA := &CBMutableService{
		UUID:      "service-a",
		IsPrimary: true,
		Characteristics: []*CBMutableCharacteristic{
			{
				UUID:        "char-a",
				Properties:  CBCharacteristicPropertyNotify | CBCharacteristicPropertyRead,
				Permissions: CBAttributePermissionsReadable,
			},
		},
	}
	deviceAPeripheralMgr.AddService(serviceA)
	deviceAPeripheralMgr.StartAdvertising(map[string]interface{}{
		"kCBAdvDataLocalName": "Device A",
	})
	<-deviceAPeripheralDelegate.didStartAdvertise
	t.Logf("✅ Device A advertising")

	// Device B: Both Central and Peripheral managers
	deviceBCentralDelegate := &testCentralDelegate{
		t:          t,
		didConnect: make(chan *CBPeripheral, 1),
	}
	deviceBPeripheralDelegate := &testPeripheralManagerDelegate{
		t:                 t,
		didStartAdvertise: make(chan bool, 1),
	}

	deviceBCentralMgr := NewCBCentralManager(deviceBCentralDelegate, "device-b-uuid", deviceBWire)
	deviceBPeripheralMgr := NewCBPeripheralManager(deviceBPeripheralDelegate, "device-b-uuid", "Device B", deviceBWire)

	// Set up GATT message routing for both devices
	deviceAWire.SetGATTMessageHandler(func(peerUUID string, msg *wire.GATTMessage) {
		// Device A routes messages to peripheral manager
		deviceAPeripheralMgr.HandleGATTMessage(peerUUID, msg)
	})

	deviceBWire.SetGATTMessageHandler(func(peerUUID string, msg *wire.GATTMessage) {
		// Device B routes messages to BOTH central and peripheral managers
		handled := deviceBCentralMgr.HandleGATTMessage(peerUUID, msg)
		if !handled {
			deviceBPeripheralMgr.HandleGATTMessage(peerUUID, msg)
		}
	})

	// Add service to B
	serviceB := &CBMutableService{
		UUID:      "service-b",
		IsPrimary: true,
		Characteristics: []*CBMutableCharacteristic{
			{
				UUID:        "char-b",
				Properties:  CBCharacteristicPropertyWrite,
				Permissions: CBAttributePermissionsWriteable,
			},
		},
	}
	deviceBPeripheralMgr.AddService(serviceB)
	deviceBPeripheralMgr.StartAdvertising(map[string]interface{}{
		"kCBAdvDataLocalName": "Device B",
	})
	<-deviceBPeripheralDelegate.didStartAdvertise
	t.Logf("✅ Device B advertising")

	time.Sleep(200 * time.Millisecond)

	// Device B connects to Device A (B = Central, A = Peripheral)
	peripheralA := &CBPeripheral{
		UUID: "device-a-uuid",
		Name: "Device A",
	}

	// Create a delegate for the reverse peripheral that B will create
	reversePeripheralDelegate := &testPeripheralDelegate{
		t:               t,
		didDiscoverSvcs: make(chan []*CBService, 1),
		didUpdateValue:  make(chan []byte, 10),
	}
	peripheralA.Delegate = reversePeripheralDelegate

	// B connects to A
	deviceBCentralMgr.Connect(peripheralA, nil)

	// Wait for connection
	select {
	case connectedPeripheral := <-deviceBCentralDelegate.didConnect:
		t.Logf("✅ Device B connected to Device A: %s", connectedPeripheral.UUID)
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for B to connect to A")
	}

	// Now B discovers services on A
	peripheralA.DiscoverServices([]string{"service-a"})

	select {
	case services := <-reversePeripheralDelegate.didDiscoverSvcs:
		if len(services) == 0 {
			t.Fatal("No services discovered on A")
		}
		t.Logf("✅ Device B discovered services on Device A")
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for service discovery")
	}

	// B subscribes to A's characteristic
	charA := peripheralA.GetCharacteristic("service-a", "char-a")
	if charA == nil {
		t.Fatal("Failed to get characteristic from A")
	}

	err := peripheralA.SetNotifyValue(true, charA)
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	// Wait for subscription at A
	select {
	case charUUID := <-deviceAPeripheralDelegate.didSubscribe:
		if charUUID != "char-a" {
			t.Errorf("Wrong characteristic subscribed: %s", charUUID)
		}
		t.Logf("✅ Device B subscribed to A's notifications")
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for subscription at A")
	}

	// A sends notification to B
	charAMutable := deviceAPeripheralMgr.GetCharacteristic("service-a", "char-a")
	if charAMutable == nil {
		t.Fatal("Failed to get mutable characteristic from A")
	}

	testData := []byte("Notification from A to B")
	success := deviceAPeripheralMgr.UpdateValue(testData, charAMutable, nil)
	if !success {
		t.Error("Failed to send notification from A")
	}

	t.Logf("✅ Device A sent notification to B")

	// *** THIS IS THE KEY TEST ***
	// B should receive the notification via the reverse peripheral's delegate
	select {
	case receivedData := <-reversePeripheralDelegate.didUpdateValue:
		if string(receivedData) != string(testData) {
			t.Errorf("Data mismatch: expected %s, got %s", string(testData), string(receivedData))
		}
		t.Logf("✅ Device B received notification from A via reverse peripheral delegate")
	case <-time.After(2 * time.Second):
		t.Fatal("❌ TIMEOUT: Device B did not receive notification from A (THIS IS THE BUG)")
	}
}
