package swift

import (
	"testing"
	"time"

	"github.com/user/auraphone-blue/util"
	"github.com/user/auraphone-blue/wire"
)

// TestCentralPeripheralConnection tests that Central and Peripheral can connect via wire
func TestCentralPeripheralConnection(t *testing.T) {
	util.SetRandom()

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

// TestRoleAssignment tests that roles are correctly assigned based on who initiates
func TestRoleAssignment(t *testing.T) {
	util.SetRandom()

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

// TestBidirectionalCommunication verifies both directions work over single connection
func TestBidirectionalCommunication(t *testing.T) {
	util.SetRandom()

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

// TestGATTMessageFormat tests that GATT messages are properly formatted
func TestGATTMessageFormat(t *testing.T) {
	util.SetRandom()

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
