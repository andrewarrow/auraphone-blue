package swift

import (
	"testing"
	"time"

	"github.com/user/auraphone-blue/util"
	"github.com/user/auraphone-blue/wire"
)

// TestGATTWriteRequest tests that Central can write to Peripheral's characteristic
func TestGATTWriteRequest(t *testing.T) {
	util.SetRandom()

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
