package kotlin

import (
	"testing"

	"github.com/user/auraphone-blue/wire"
)

// TestBluetoothManager_SharedWire tests that BluetoothManager uses shared wire instance
func TestBluetoothManager_SharedWire(t *testing.T) {
	w := wire.NewWire("test-uuid")
	if err := w.Start(); err != nil {
		t.Fatalf("Failed to start wire: %v", err)
	}
	defer w.Stop()

	// Create manager with shared wire
	manager := NewBluetoothManager("test-uuid", w)

	// Verify adapter has wire
	if manager.Adapter.wire != w {
		t.Error("Adapter does not have shared wire")
	}

	// Verify scanner has wire
	scanner := manager.Adapter.GetBluetoothLeScanner()
	if scanner.wire != w {
		t.Error("Scanner does not have shared wire")
	}

	t.Logf("✅ BluetoothManager uses shared wire correctly")
}

// TestBluetoothAdapter_RoleNegotiation tests UUID-based role negotiation
func TestBluetoothAdapter_RoleNegotiation(t *testing.T) {
	w1 := wire.NewWire("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	w2 := wire.NewWire("ffffffff-0000-1111-2222-333333333333")

	adapter1 := NewBluetoothAdapter("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", w1)
	adapter2 := NewBluetoothAdapter("ffffffff-0000-1111-2222-333333333333", w2)

	// Device with larger UUID should initiate
	// "ffffffff..." > "aaaaaaaa..." so adapter2 should connect to adapter1
	if adapter1.ShouldInitiateConnection("ffffffff-0000-1111-2222-333333333333") {
		t.Error("Adapter1 (smaller UUID) should NOT initiate connection to adapter2")
	}

	if !adapter2.ShouldInitiateConnection("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee") {
		t.Error("Adapter2 (larger UUID) should initiate connection to adapter1")
	}

	t.Logf("✅ Role negotiation: device with larger UUID initiates connection")
}

// TestBluetoothAdapter_GetRemoteDevice tests getting remote device by address
func TestBluetoothAdapter_GetRemoteDevice(t *testing.T) {
	w1 := wire.NewWire("device1-uuid")
	w2 := wire.NewWire("device2-uuid")

	if err := w1.Start(); err != nil {
		t.Fatalf("Failed to start w1: %v", err)
	}
	defer w1.Stop()

	if err := w2.Start(); err != nil {
		t.Fatalf("Failed to start w2: %v", err)
	}
	defer w2.Stop()

	// Write advertising data for device2
	advData := &wire.AdvertisingData{
		DeviceName:    "Test Android Device",
		ServiceUUIDs:  []string{"E621E1F8-C36C-495A-93FC-0C247A3E6E5F"},
		IsConnectable: true,
	}
	if err := w2.WriteAdvertisingData(advData); err != nil {
		t.Fatalf("Failed to write advertising data: %v", err)
	}

	adapter1 := NewBluetoothAdapter("device1-uuid", w1)

	// Get remote device
	device := adapter1.GetRemoteDevice("device2-uuid")
	if device == nil {
		t.Fatal("Failed to get remote device")
	}

	if device.Address != "device2-uuid" {
		t.Errorf("Wrong device address: %s", device.Address)
	}

	if device.Name != "Test Android Device" {
		t.Errorf("Wrong device name: %s", device.Name)
	}

	t.Logf("✅ GetRemoteDevice returns correct device info")
}
