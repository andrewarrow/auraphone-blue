package swift

import (
	"testing"
	"time"

	"github.com/user/auraphone-blue/util"
	"github.com/user/auraphone-blue/wire"
)

func TestCBPeripheralManager_AddService(t *testing.T) {
	util.SetRandom()

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
	util.SetRandom()

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
	util.SetRandom()

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
