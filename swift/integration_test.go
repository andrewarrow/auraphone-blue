package swift

import (
	"fmt"
	"testing"
	"time"

	"github.com/user/auraphone-blue/wire"
)

// TestCentralPeripheralConnection tests that Central and Peripheral can connect via wire
func TestCentralPeripheralConnection(t *testing.T) {
	// Create two wires
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

	time.Sleep(100 * time.Millisecond)

	// Test 1: Verify devices can discover each other
	devices := centralWire.ListAvailableDevices()
	found := false
	for _, dev := range devices {
		if dev == "peripheral-uuid" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("Central should discover peripheral via wire.ListAvailableDevices()")
	}
	t.Logf("✅ Central discovered peripheral")

	// Test 2: Verify CBCentralManager.Connect() establishes connection
	err := centralWire.Connect("peripheral-uuid")
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Test 3: Verify connection state from both sides
	if !centralWire.IsConnected("peripheral-uuid") {
		t.Error("Central should be connected to peripheral")
	}
	if !peripheralWire.IsConnected("central-uuid") {
		t.Error("Peripheral should be connected to central")
	}
	t.Logf("✅ Both sides report connected state")

	// Test 4: Verify roles are correct
	centralRole, ok := centralWire.GetConnectionRole("peripheral-uuid")
	if !ok || centralRole != wire.RoleCentral {
		t.Errorf("Central should have RoleCentral, got %s", centralRole)
	}

	peripheralRole, ok := peripheralWire.GetConnectionRole("central-uuid")
	if !ok || peripheralRole != wire.RolePeripheral {
		t.Errorf("Peripheral should have RolePeripheral, got %s", peripheralRole)
	}
	t.Logf("✅ Roles correctly assigned: Central=%s, Peripheral=%s", centralRole, peripheralRole)
}

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

// Test delegate implementations

type testCentralDelegate struct {
	t               *testing.T
	didConnect      chan *CBPeripheral
	didDisconnect   chan *CBPeripheral
	didDiscoverSvcs chan *CBPeripheral
	didWriteValue   chan *CBCharacteristic
	didUpdateValue  chan []byte
}

func (d *testCentralDelegate) DidUpdateCentralState(central CBCentralManager) {}

func (d *testCentralDelegate) DidDiscoverPeripheral(central CBCentralManager, peripheral CBPeripheral, advertisementData map[string]interface{}, rssi float64) {
}

func (d *testCentralDelegate) DidConnectPeripheral(central CBCentralManager, peripheral CBPeripheral) {
	if d.didConnect != nil {
		d.didConnect <- &peripheral
	}
}

func (d *testCentralDelegate) DidFailToConnectPeripheral(central CBCentralManager, peripheral CBPeripheral, err error) {
	d.t.Errorf("Failed to connect: %v", err)
}

func (d *testCentralDelegate) DidDisconnectPeripheral(central CBCentralManager, peripheral CBPeripheral, err error) {
	if d.didDisconnect != nil {
		d.didDisconnect <- &peripheral
	}
}

type testPeripheralDelegate struct {
	t               *testing.T
	didConnect      chan *CBPeripheral
	didDiscoverSvcs chan []*CBService
	didWriteValue   chan *CBCharacteristic
	didUpdateValue  chan []byte
}

func (d *testPeripheralDelegate) DidDiscoverServices(peripheral *CBPeripheral, services []*CBService, err error) {
	if err != nil {
		d.t.Errorf("Service discovery error: %v", err)
		return
	}
	if d.didDiscoverSvcs != nil {
		d.didDiscoverSvcs <- services
	}
}

func (d *testPeripheralDelegate) DidDiscoverCharacteristics(peripheral *CBPeripheral, service *CBService, err error) {
}

func (d *testPeripheralDelegate) DidWriteValueForCharacteristic(peripheral *CBPeripheral, characteristic *CBCharacteristic, err error) {
	if err != nil {
		d.t.Errorf("Write error: %v", err)
		return
	}
	if d.didWriteValue != nil {
		d.didWriteValue <- characteristic
	}
}

func (d *testPeripheralDelegate) DidUpdateValueForCharacteristic(peripheral *CBPeripheral, characteristic *CBCharacteristic, err error) {
	if err != nil {
		d.t.Errorf("Update value error: %v", err)
		return
	}
	if d.didUpdateValue != nil && characteristic.Value != nil {
		// Make a copy of the data
		dataCopy := make([]byte, len(characteristic.Value))
		copy(dataCopy, characteristic.Value)
		d.didUpdateValue <- dataCopy
	}
}

type testPeripheralManagerDelegate struct {
	t                 *testing.T
	didReceiveWrite   chan []byte
	didReceiveRead    chan bool
	didSubscribe      chan string
	didUnsubscribe    chan string
	didStartAdvertise chan bool
}

func (d *testPeripheralManagerDelegate) DidUpdatePeripheralState(peripheralManager *CBPeripheralManager) {
}

func (d *testPeripheralManagerDelegate) DidStartAdvertising(peripheralManager *CBPeripheralManager, err error) {
	if err != nil {
		d.t.Errorf("Advertising error: %v", err)
		return
	}
	if d.didStartAdvertise != nil {
		d.didStartAdvertise <- true
	}
}

func (d *testPeripheralManagerDelegate) DidReceiveReadRequest(peripheralManager *CBPeripheralManager, request *CBATTRequest) {
	if d.didReceiveRead != nil {
		d.didReceiveRead <- true
	}
}

func (d *testPeripheralManagerDelegate) DidReceiveWriteRequests(peripheralManager *CBPeripheralManager, requests []*CBATTRequest) {
	if d.didReceiveWrite != nil && len(requests) > 0 {
		// Make a copy of the data
		dataCopy := make([]byte, len(requests[0].Value))
		copy(dataCopy, requests[0].Value)
		d.didReceiveWrite <- dataCopy
	}
}

func (d *testPeripheralManagerDelegate) CentralDidSubscribe(peripheralManager *CBPeripheralManager, central CBCentral, characteristic *CBMutableCharacteristic) {
	if d.didSubscribe != nil {
		d.didSubscribe <- characteristic.UUID
	}
}

func (d *testPeripheralManagerDelegate) CentralDidUnsubscribe(peripheralManager *CBPeripheralManager, central CBCentral, characteristic *CBMutableCharacteristic) {
	if d.didUnsubscribe != nil {
		d.didUnsubscribe <- characteristic.UUID
	}
}

// TestRoleAssignment tests that roles are correctly assigned based on who initiates
func TestRoleAssignment(t *testing.T) {
	// Create two wires
	wireA := wire.NewWire("device-a")
	wireB := wire.NewWire("device-b")

	// Start both
	if err := wireA.Start(); err != nil {
		t.Fatalf("Failed to start wire A: %v", err)
	}
	defer wireA.Stop()

	if err := wireB.Start(); err != nil {
		t.Fatalf("Failed to start wire B: %v", err)
	}
	defer wireB.Stop()

	time.Sleep(100 * time.Millisecond)

	// Device A connects to Device B (A becomes Central, B becomes Peripheral)
	err := wireA.Connect("device-b")
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify roles
	roleA, ok := wireA.GetConnectionRole("device-b")
	if !ok {
		t.Fatal("Wire A should have connection to B")
	}
	if roleA != wire.RoleCentral {
		t.Errorf("Wire A should be Central, got %s", roleA)
	}

	roleB, ok := wireB.GetConnectionRole("device-a")
	if !ok {
		t.Fatal("Wire B should have connection to A")
	}
	if roleB != wire.RolePeripheral {
		t.Errorf("Wire B should be Peripheral, got %s", roleB)
	}

	t.Logf("✅ Roles correctly assigned: A=Central, B=Peripheral")
}

// TestGATTMessageFormat tests that GATT messages are properly formatted
func TestGATTMessageFormat(t *testing.T) {
	// Create two wires
	wireA := wire.NewWire("device-a")
	wireB := wire.NewWire("device-b")

	// Start both
	if err := wireA.Start(); err != nil {
		t.Fatalf("Failed to start wire A: %v", err)
	}
	defer wireA.Stop()

	if err := wireB.Start(); err != nil {
		t.Fatalf("Failed to start wire B: %v", err)
	}
	defer wireB.Stop()

	// Set up handler to verify message format
	receivedMsg := make(chan *wire.GATTMessage, 1)
	wireB.SetGATTMessageHandler(func(peerUUID string, msg *wire.GATTMessage) {
		receivedMsg <- msg
	})

	time.Sleep(100 * time.Millisecond)

	// Connect
	err := wireA.Connect("device-b")
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Send a GATT write request
	msg := &wire.GATTMessage{
		Type:               "gatt_request",
		RequestID:          "test-req-1",
		Operation:          "write",
		ServiceUUID:        "service-uuid-1",
		CharacteristicUUID: "char-uuid-1",
		Data:               []byte("test data"),
	}

	err = wireA.SendGATTMessage("device-b", msg)
	if err != nil {
		t.Fatalf("Failed to send GATT message: %v", err)
	}

	// Wait for message
	select {
	case received := <-receivedMsg:
		if received.Type != "gatt_request" {
			t.Errorf("Expected type=gatt_request, got %s", received.Type)
		}
		if received.Operation != "write" {
			t.Errorf("Expected operation=write, got %s", received.Operation)
		}
		if received.ServiceUUID != "service-uuid-1" {
			t.Errorf("Expected service UUID mismatch")
		}
		if string(received.Data) != "test data" {
			t.Errorf("Data mismatch: got %s", string(received.Data))
		}
		t.Logf("✅ GATT message format correct")
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for GATT message")
	}
}

// TestBidirectionalCommunication verifies both directions work over single connection
func TestBidirectionalCommunication(t *testing.T) {
	// Create two wires
	wireA := wire.NewWire("device-a")
	wireB := wire.NewWire("device-b")

	// Start both
	if err := wireA.Start(); err != nil {
		t.Fatalf("Failed to start wire A: %v", err)
	}
	defer wireA.Stop()

	if err := wireB.Start(); err != nil {
		t.Fatalf("Failed to start wire B: %v", err)
	}
	defer wireB.Stop()

	// Set up handlers
	messagesA := make(chan *wire.GATTMessage, 10)
	messagesB := make(chan *wire.GATTMessage, 10)

	wireA.SetGATTMessageHandler(func(peerUUID string, msg *wire.GATTMessage) {
		messagesA <- msg
	})

	wireB.SetGATTMessageHandler(func(peerUUID string, msg *wire.GATTMessage) {
		messagesB <- msg
	})

	time.Sleep(100 * time.Millisecond)

	// Connect (A is Central, B is Peripheral)
	err := wireA.Connect("device-b")
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Central (A) sends write request to Peripheral (B)
	writeRequest := &wire.GATTMessage{
		Type:               "gatt_request",
		RequestID:          "req-1",
		Operation:          "write",
		ServiceUUID:        "service-1",
		CharacteristicUUID: "char-1",
		Data:               []byte("write from central"),
	}
	wireA.SendGATTMessage("device-b", writeRequest)

	// Peripheral (B) sends notification to Central (A)
	notification := &wire.GATTMessage{
		Type:               "gatt_notification",
		ServiceUUID:        "service-2",
		CharacteristicUUID: "char-2",
		Data:               []byte("notification from peripheral"),
	}
	wireB.SendGATTMessage("device-a", notification)

	// Verify A received notification
	select {
	case msg := <-messagesA:
		if msg.Type != "gatt_notification" {
			t.Errorf("A should receive notification, got %s", msg.Type)
		}
		if string(msg.Data) != "notification from peripheral" {
			t.Errorf("A received wrong data: %s", string(msg.Data))
		}
		t.Logf("✅ Central received notification from Peripheral")
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for notification at A")
	}

	// Verify B received write request
	select {
	case msg := <-messagesB:
		if msg.Type != "gatt_request" {
			t.Errorf("B should receive request, got %s", msg.Type)
		}
		if msg.Operation != "write" {
			t.Errorf("B should receive write, got %s", msg.Operation)
		}
		if string(msg.Data) != "write from central" {
			t.Errorf("B received wrong data: %s", string(msg.Data))
		}
		t.Logf("✅ Peripheral received write request from Central")
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for write request at B")
	}
}

// Unit Tests for CBCentralManager

func TestCBCentralManager_ScanForPeripherals_ServiceFiltering(t *testing.T) {
	w := wire.NewWire("test-uuid")
	if err := w.Start(); err != nil {
		t.Fatalf("Failed to start wire: %v", err)
	}
	defer w.Stop()

	discoveredDevices := make(chan string, 10)

	// We need a custom delegate type that captures device discoveries
	// For now, let's test the wire-level service filtering

	// Create peripheral with specific service UUID
	peripheralWire := wire.NewWire("peripheral-uuid")
	if err := peripheralWire.Start(); err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheralWire.Stop()

	// Write advertising data with service UUID
	advData := &wire.AdvertisingData{
		DeviceName:    "Test Peripheral",
		ServiceUUIDs:  []string{"E621E1F8-C36C-495A-93FC-0C247A3E6E5F"},
		IsConnectable: true,
	}
	if err := peripheralWire.WriteAdvertisingData(advData); err != nil {
		t.Fatalf("Failed to write advertising data: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Verify we can read the advertising data
	readAdvData, err := w.ReadAdvertisingData("peripheral-uuid")
	if err != nil {
		t.Fatalf("Failed to read advertising data: %v", err)
	}

	if len(readAdvData.ServiceUUIDs) == 0 {
		t.Error("No service UUIDs in advertising data")
	}

	if readAdvData.ServiceUUIDs[0] != "E621E1F8-C36C-495A-93FC-0C247A3E6E5F" {
		t.Errorf("Wrong service UUID: %s", readAdvData.ServiceUUIDs[0])
	}

	t.Logf("✅ Service UUID filtering data structure works correctly")

	// Close discovery channel
	close(discoveredDevices)
}

func TestCBCentralManager_ShouldInitiateConnection(t *testing.T) {
	w1 := wire.NewWire("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	w2 := wire.NewWire("ffffffff-0000-1111-2222-333333333333")

	if err := w1.Start(); err != nil {
		t.Fatalf("Failed to start w1: %v", err)
	}
	defer w1.Stop()

	if err := w2.Start(); err != nil {
		t.Fatalf("Failed to start w2: %v", err)
	}
	defer w2.Stop()

	delegate1 := &testCentralDelegate{t: t}
	delegate2 := &testCentralDelegate{t: t}

	cm1 := NewCBCentralManager(delegate1, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", w1)
	cm2 := NewCBCentralManager(delegate2, "ffffffff-0000-1111-2222-333333333333", w2)

	// Test role negotiation: device with larger UUID should initiate
	should1InitTo2 := cm1.ShouldInitiateConnection("ffffffff-0000-1111-2222-333333333333")
	should2InitTo1 := cm2.ShouldInitiateConnection("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")

	// Only one should initiate
	if should1InitTo2 == should2InitTo1 {
		t.Errorf("Role conflict: both or neither want to initiate (1->2: %v, 2->1: %v)", should1InitTo2, should2InitTo1)
	}

	// Device with larger UUID should initiate
	if should1InitTo2 {
		t.Error("Device with smaller UUID should NOT initiate")
	}
	if !should2InitTo1 {
		t.Error("Device with larger UUID SHOULD initiate")
	}

	t.Logf("✅ Role negotiation works correctly: larger UUID initiates")
}

func TestCBCentralManager_AutoReconnect(t *testing.T) {
	centralWire := wire.NewWire("central-uuid")
	peripheralWire := wire.NewWire("peripheral-uuid")

	if err := centralWire.Start(); err != nil {
		t.Fatalf("Failed to start central: %v", err)
	}
	defer centralWire.Stop()

	if err := peripheralWire.Start(); err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheralWire.Stop()

	connectCount := 0
	disconnectCount := 0

	delegate := &testCentralDelegate{
		t:             t,
		didConnect:    make(chan *CBPeripheral, 10),
		didDisconnect: make(chan *CBPeripheral, 10),
	}

	cm := NewCBCentralManager(delegate, "central-uuid", centralWire)

	peripheral := &CBPeripheral{
		UUID: "peripheral-uuid",
		Name: "Test Peripheral",
	}

	// Initial connection
	cm.Connect(peripheral, nil)

	// Wait for first connection
	select {
	case <-delegate.didConnect:
		connectCount++
		t.Logf("✅ Initial connection established")
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for initial connection")
	}

	// Simulate disconnect
	peripheralWire.Disconnect("central-uuid")

	// Wait for disconnect notification
	select {
	case <-delegate.didDisconnect:
		disconnectCount++
		t.Logf("✅ Disconnect detected")
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for disconnect notification")
	}

	// iOS auto-reconnect should trigger (after 2s delay)
	// Wait for auto-reconnect
	select {
	case <-delegate.didConnect:
		connectCount++
		t.Logf("✅ Auto-reconnect successful")
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for auto-reconnect")
	}

	if connectCount != 2 {
		t.Errorf("Expected 2 connections (initial + reconnect), got %d", connectCount)
	}
	if disconnectCount != 1 {
		t.Errorf("Expected 1 disconnect, got %d", disconnectCount)
	}
}

func TestCBCentralManager_CancelPeripheralConnection(t *testing.T) {
	centralWire := wire.NewWire("central-uuid")
	peripheralWire := wire.NewWire("peripheral-uuid")

	if err := centralWire.Start(); err != nil {
		t.Fatalf("Failed to start central: %v", err)
	}
	defer centralWire.Stop()

	if err := peripheralWire.Start(); err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheralWire.Stop()

	delegate := &testCentralDelegate{
		t:             t,
		didConnect:    make(chan *CBPeripheral, 1),
		didDisconnect: make(chan *CBPeripheral, 1),
	}

	cm := NewCBCentralManager(delegate, "central-uuid", centralWire)

	peripheral := &CBPeripheral{
		UUID: "peripheral-uuid",
		Name: "Test Peripheral",
	}

	// Connect
	cm.Connect(peripheral, nil)
	<-delegate.didConnect
	t.Logf("✅ Connected")

	// Cancel connection (should stop auto-reconnect)
	cm.CancelPeripheralConnection(peripheral)

	// Verify disconnected
	if centralWire.IsConnected("peripheral-uuid") {
		t.Error("Should be disconnected after cancel")
	}

	// Simulate disconnect and verify NO auto-reconnect happens
	time.Sleep(3 * time.Second)

	// No reconnection should occur
	select {
	case <-delegate.didConnect:
		t.Error("Auto-reconnect should NOT happen after CancelPeripheralConnection")
	default:
		t.Logf("✅ Auto-reconnect correctly disabled")
	}
}

// Unit Tests for CBPeripheralManager

func TestCBPeripheralManager_AddService(t *testing.T) {
	w := wire.NewWire("test-uuid")
	if err := w.Start(); err != nil {
		t.Fatalf("Failed to start wire: %v", err)
	}
	defer w.Stop()

	delegate := &testPeripheralManagerDelegate{
		t: t,
	}

	pm := NewCBPeripheralManager(delegate, "test-uuid", "Test Device", w)

	service := &CBMutableService{
		UUID:      "service-uuid-1",
		IsPrimary: true,
		Characteristics: []*CBMutableCharacteristic{
			{
				UUID:        "char-uuid-1",
				Properties:  CBCharacteristicPropertyRead | CBCharacteristicPropertyWrite,
				Permissions: CBAttributePermissionsReadable | CBAttributePermissionsWriteable,
			},
		},
	}

	err := pm.AddService(service)
	if err != nil {
		t.Fatalf("Failed to add service: %v", err)
	}

	// Verify GATT table was written
	gattTable, err := w.ReadGATTTable("test-uuid")
	if err != nil {
		t.Fatalf("Failed to read GATT table: %v", err)
	}

	if len(gattTable.Services) != 1 {
		t.Fatalf("Expected 1 service, got %d", len(gattTable.Services))
	}

	if gattTable.Services[0].UUID != "service-uuid-1" {
		t.Errorf("Wrong service UUID: %s", gattTable.Services[0].UUID)
	}

	if len(gattTable.Services[0].Characteristics) != 1 {
		t.Fatalf("Expected 1 characteristic, got %d", len(gattTable.Services[0].Characteristics))
	}

	t.Logf("✅ Service added and GATT table updated correctly")
}

func TestCBPeripheralManager_StartAdvertising(t *testing.T) {
	w := wire.NewWire("test-uuid")
	if err := w.Start(); err != nil {
		t.Fatalf("Failed to start wire: %v", err)
	}
	defer w.Stop()

	delegate := &testPeripheralManagerDelegate{
		t:                 t,
		didStartAdvertise: make(chan bool, 1),
	}

	pm := NewCBPeripheralManager(delegate, "test-uuid", "Test Device", w)

	// Add service first
	service := &CBMutableService{
		UUID:      "E621E1F8-C36C-495A-93FC-0C247A3E6E5F",
		IsPrimary: true,
		Characteristics: []*CBMutableCharacteristic{
			{
				UUID:        "char-uuid-1",
				Properties:  CBCharacteristicPropertyRead,
				Permissions: CBAttributePermissionsReadable,
			},
		},
	}
	pm.AddService(service)

	// Start advertising
	err := pm.StartAdvertising(map[string]interface{}{
		"kCBAdvDataLocalName":    "My Test Device",
		"kCBAdvDataServiceUUIDs": []string{"E621E1F8-C36C-495A-93FC-0C247A3E6E5F"},
	})
	if err != nil {
		t.Fatalf("Failed to start advertising: %v", err)
	}

	// Wait for delegate callback
	select {
	case <-delegate.didStartAdvertise:
		t.Logf("✅ Advertising started")
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for advertising to start")
	}

	// Verify advertising state
	if !pm.IsAdvertising {
		t.Error("IsAdvertising should be true")
	}

	// Verify advertising data was written
	advData, err := w.ReadAdvertisingData("test-uuid")
	if err != nil {
		t.Fatalf("Failed to read advertising data: %v", err)
	}

	if advData.DeviceName != "My Test Device" {
		t.Errorf("Wrong device name: %s", advData.DeviceName)
	}

	if len(advData.ServiceUUIDs) != 1 || advData.ServiceUUIDs[0] != "E621E1F8-C36C-495A-93FC-0C247A3E6E5F" {
		t.Errorf("Wrong service UUIDs: %v", advData.ServiceUUIDs)
	}

	t.Logf("✅ Advertising data written correctly")

	// Stop advertising
	pm.StopAdvertising()
	if pm.IsAdvertising {
		t.Error("IsAdvertising should be false after stop")
	}
}

func TestCBPeripheralManager_UpdateValue_MultipleSubscribers(t *testing.T) {
	peripheralWire := wire.NewWire("peripheral-uuid")
	central1Wire := wire.NewWire("central1-uuid")
	central2Wire := wire.NewWire("central2-uuid")

	if err := peripheralWire.Start(); err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheralWire.Stop()

	if err := central1Wire.Start(); err != nil {
		t.Fatalf("Failed to start central1: %v", err)
	}
	defer central1Wire.Stop()

	if err := central2Wire.Start(); err != nil {
		t.Fatalf("Failed to start central2: %v", err)
	}
	defer central2Wire.Stop()

	time.Sleep(200 * time.Millisecond)

	delegate := &testPeripheralManagerDelegate{
		t:                 t,
		didStartAdvertise: make(chan bool, 1),
		didSubscribe:      make(chan string, 10),
	}

	pm := NewCBPeripheralManager(delegate, "peripheral-uuid", "Test Peripheral", peripheralWire)

	// Add service
	service := &CBMutableService{
		UUID:      "service-uuid",
		IsPrimary: true,
		Characteristics: []*CBMutableCharacteristic{
			{
				UUID:        "char-uuid",
				Properties:  CBCharacteristicPropertyNotify,
				Permissions: CBAttributePermissionsReadable,
			},
		},
	}
	pm.AddService(service)
	pm.StartAdvertising(map[string]interface{}{
		"kCBAdvDataLocalName": "Test",
	})
	<-delegate.didStartAdvertise

	// Connect both centrals
	central1Wire.Connect("peripheral-uuid")
	central2Wire.Connect("peripheral-uuid")
	time.Sleep(300 * time.Millisecond)

	// Set up message handlers for centrals
	notifications1 := make(chan []byte, 10)
	notifications2 := make(chan []byte, 10)

	central1Wire.SetGATTMessageHandler(func(peerUUID string, msg *wire.GATTMessage) {
		if msg.Type == "gatt_notification" {
			notifications1 <- msg.Data
		}
	})

	central2Wire.SetGATTMessageHandler(func(peerUUID string, msg *wire.GATTMessage) {
		if msg.Type == "gatt_notification" {
			notifications2 <- msg.Data
		}
	})

	// Simulate subscriptions from both centrals
	char := pm.GetCharacteristic("service-uuid", "char-uuid")
	if char == nil {
		t.Fatal("Failed to get characteristic")
	}

	// Manually add subscriptions (simulating subscribe requests)
	if char.subscribedCentrals == nil {
		char.subscribedCentrals = make(map[string]bool)
	}
	char.subscribedCentrals["central1-uuid"] = true
	char.subscribedCentrals["central2-uuid"] = true

	// Send notification to all subscribers
	testData := []byte("Test notification")
	success := pm.UpdateValue(testData, char, nil)
	if !success {
		t.Error("UpdateValue should return true")
	}

	time.Sleep(200 * time.Millisecond)

	// Verify both centrals received the notification
	receivedCount := 0
	timeout := time.After(2 * time.Second)

	for receivedCount < 2 {
		select {
		case data1 := <-notifications1:
			if string(data1) != string(testData) {
				t.Errorf("Central1 received wrong data: %s", string(data1))
			}
			receivedCount++
			t.Logf("✅ Central1 received notification")
		case data2 := <-notifications2:
			if string(data2) != string(testData) {
				t.Errorf("Central2 received wrong data: %s", string(data2))
			}
			receivedCount++
			t.Logf("✅ Central2 received notification")
		case <-timeout:
			t.Fatalf("Timeout: only %d/2 centrals received notification", receivedCount)
		}
	}

	if receivedCount != 2 {
		t.Errorf("Expected 2 notifications, got %d", receivedCount)
	}
}

// Unit Tests for CBPeripheral

func TestCBPeripheral_WriteQueue(t *testing.T) {
	w := wire.NewWire("test-uuid")
	if err := w.Start(); err != nil {
		t.Fatalf("Failed to start wire: %v", err)
	}
	defer w.Stop()

	remoteWire := wire.NewWire("remote-uuid")
	if err := remoteWire.Start(); err != nil {
		t.Fatalf("Failed to start remote wire: %v", err)
	}
	defer remoteWire.Stop()

	time.Sleep(100 * time.Millisecond)

	// Connect
	if err := w.Connect("remote-uuid"); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	delegate := &testPeripheralDelegate{
		t:             t,
		didWriteValue: make(chan *CBCharacteristic, 20),
	}

	peripheral := &CBPeripheral{
		UUID:       "remote-uuid",
		Name:       "Remote Device",
		wire:       w,
		remoteUUID: "remote-uuid",
		Delegate:   delegate,
		Services: []*CBService{
			{
				UUID:      "service-uuid",
				IsPrimary: true,
				Characteristics: []*CBCharacteristic{
					{
						UUID:       "char-uuid",
						Properties: []string{"write"},
					},
				},
			},
		},
	}

	// Start write queue
	peripheral.StartWriteQueue()
	defer peripheral.StopWriteQueue()

	// Send multiple writes rapidly (queue should handle them)
	numWrites := 10
	for i := 0; i < numWrites; i++ {
		data := []byte(fmt.Sprintf("Write %d", i))
		char := peripheral.GetCharacteristic("service-uuid", "char-uuid")
		err := peripheral.WriteValue(data, char, CBCharacteristicWriteWithResponse)
		if err != nil {
			t.Errorf("Write %d failed: %v", i, err)
		}
	}

	// Verify all writes completed
	receivedCount := 0
	timeout := time.After(5 * time.Second)

	for receivedCount < numWrites {
		select {
		case <-delegate.didWriteValue:
			receivedCount++
		case <-timeout:
			t.Fatalf("Timeout: only %d/%d writes completed", receivedCount, numWrites)
		}
	}

	if receivedCount != numWrites {
		t.Errorf("Expected %d write confirmations, got %d", numWrites, receivedCount)
	}

	t.Logf("✅ Write queue handled %d writes successfully", numWrites)
}

func TestCBPeripheral_WriteWithoutResponse(t *testing.T) {
	w := wire.NewWire("test-uuid")
	if err := w.Start(); err != nil {
		t.Fatalf("Failed to start wire: %v", err)
	}
	defer w.Stop()

	remoteWire := wire.NewWire("remote-uuid")
	if err := remoteWire.Start(); err != nil {
		t.Fatalf("Failed to start remote wire: %v", err)
	}
	defer remoteWire.Stop()

	time.Sleep(100 * time.Millisecond)

	// Connect
	if err := w.Connect("remote-uuid"); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	delegate := &testPeripheralDelegate{
		t:             t,
		didWriteValue: make(chan *CBCharacteristic, 1),
	}

	peripheral := &CBPeripheral{
		UUID:       "remote-uuid",
		Name:       "Remote Device",
		wire:       w,
		remoteUUID: "remote-uuid",
		Delegate:   delegate,
		Services: []*CBService{
			{
				UUID:      "service-uuid",
				IsPrimary: true,
				Characteristics: []*CBCharacteristic{
					{
						UUID:       "char-uuid",
						Properties: []string{"write_without_response"},
					},
				},
			},
		},
	}

	peripheral.StartWriteQueue()
	defer peripheral.StopWriteQueue()

	// Write without response (should complete immediately)
	char := peripheral.GetCharacteristic("service-uuid", "char-uuid")
	testData := []byte("Fast write")

	start := time.Now()
	err := peripheral.WriteValue(testData, char, CBCharacteristicWriteWithoutResponse)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Wait for callback (should be immediate)
	select {
	case <-delegate.didWriteValue:
		elapsed := time.Since(start)
		t.Logf("✅ Write without response completed in %v", elapsed)
		if elapsed > 100*time.Millisecond {
			t.Errorf("Write without response took too long: %v", elapsed)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timeout waiting for write without response callback")
	}
}

// Edge Case Tests

func TestEdgeCase_WriteToDisconnectedPeripheral(t *testing.T) {
	w := wire.NewWire("test-uuid")
	if err := w.Start(); err != nil {
		t.Fatalf("Failed to start wire: %v", err)
	}
	defer w.Stop()

	delegate := &testPeripheralDelegate{
		t: t,
	}

	peripheral := &CBPeripheral{
		UUID:     "remote-uuid",
		Name:     "Remote Device",
		Delegate: delegate,
		// Note: wire is nil (not connected)
	}

	char := &CBCharacteristic{
		UUID: "char-uuid",
		Service: &CBService{
			UUID: "service-uuid",
		},
	}

	// Attempt write to disconnected peripheral
	err := peripheral.WriteValue([]byte("test"), char, CBCharacteristicWriteWithResponse)
	if err == nil {
		t.Error("Expected error when writing to disconnected peripheral")
	}

	t.Logf("✅ Correctly returned error: %v", err)
}

func TestEdgeCase_InvalidCharacteristic(t *testing.T) {
	w := wire.NewWire("test-uuid")
	if err := w.Start(); err != nil {
		t.Fatalf("Failed to start wire: %v", err)
	}
	defer w.Stop()

	peripheral := &CBPeripheral{
		UUID: "remote-uuid",
		Name: "Remote Device",
		wire: w,
	}

	// Write with nil characteristic
	err := peripheral.WriteValue([]byte("test"), nil, CBCharacteristicWriteWithResponse)
	if err == nil {
		t.Error("Expected error with nil characteristic")
	}

	// Write with characteristic missing service
	char := &CBCharacteristic{
		UUID:    "char-uuid",
		Service: nil,
	}
	err = peripheral.WriteValue([]byte("test"), char, CBCharacteristicWriteWithResponse)
	if err == nil {
		t.Error("Expected error with characteristic missing service")
	}

	t.Logf("✅ Invalid characteristic errors handled correctly")
}

func TestEdgeCase_AddServiceWhileAdvertising(t *testing.T) {
	w := wire.NewWire("test-uuid")
	if err := w.Start(); err != nil {
		t.Fatalf("Failed to start wire: %v", err)
	}
	defer w.Stop()

	delegate := &testPeripheralManagerDelegate{
		t:                 t,
		didStartAdvertise: make(chan bool, 1),
	}

	pm := NewCBPeripheralManager(delegate, "test-uuid", "Test Device", w)

	service1 := &CBMutableService{
		UUID:      "service-1",
		IsPrimary: true,
		Characteristics: []*CBMutableCharacteristic{
			{
				UUID:       "char-1",
				Properties: CBCharacteristicPropertyRead,
			},
		},
	}

	pm.AddService(service1)
	pm.StartAdvertising(map[string]interface{}{
		"kCBAdvDataLocalName": "Test",
	})
	<-delegate.didStartAdvertise

	// Try to add service while advertising (should fail)
	service2 := &CBMutableService{
		UUID:            "service-2",
		IsPrimary:       true,
		Characteristics: []*CBMutableCharacteristic{},
	}

	err := pm.AddService(service2)
	if err == nil {
		t.Error("Expected error when adding service while advertising")
	}

	t.Logf("✅ Correctly prevented service addition while advertising: %v", err)
}
