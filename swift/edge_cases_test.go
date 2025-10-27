package swift

import (
	"testing"

	"github.com/user/auraphone-blue/wire"
)

// TestEdgeCase_WriteToDisconnectedPeripheral tests writing to a disconnected peripheral
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

// TestEdgeCase_InvalidCharacteristic tests writing with invalid characteristic
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

// TestEdgeCase_AddServiceWhileAdvertising tests adding a service while advertising
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
