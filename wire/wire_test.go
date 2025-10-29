package wire

import (
	"sync"
	"testing"
	"time"

	"github.com/user/auraphone-blue/util"
)

// TestSingleConnection verifies that two devices create a single bidirectional connection
func TestSingleConnection(t *testing.T) {
	util.SetRandom()

	// Create two devices
	deviceA := NewWire("device-a-uuid")
	deviceB := NewWire("device-b-uuid")

	// Start both devices
	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Failed to start device B: %v", err)
	}
	defer deviceB.Stop()

	// Wait for listeners to be ready
	time.Sleep(100 * time.Millisecond)

	// Device A connects to Device B (A becomes Central)
	err := deviceA.Connect("device-b-uuid")
	if err != nil {
		t.Fatalf("Failed to connect A → B: %v", err)
	}

	// Wait for connection to establish
	time.Sleep(100 * time.Millisecond)

	// Verify A is connected to B
	if !deviceA.IsConnected("device-b-uuid") {
		t.Error("Device A should be connected to Device B")
	}

	// Verify B is connected to A
	if !deviceB.IsConnected("device-a-uuid") {
		t.Error("Device B should be connected to Device A")
	}

	// Verify only ONE connection exists (not two)
	peersA := deviceA.GetConnectedPeers()
	if len(peersA) != 1 {
		t.Errorf("Device A should have 1 connection, got %d", len(peersA))
	}

	peersB := deviceB.GetConnectedPeers()
	if len(peersB) != 1 {
		t.Errorf("Device B should have 1 connection, got %d", len(peersB))
	}
}

// TestConnectionRoles verifies that roles are assigned correctly
func TestConnectionRoles(t *testing.T) {
	util.SetRandom()
	// Create two devices
	deviceA := NewWire("device-a-uuid")
	deviceB := NewWire("device-b-uuid")

	// Start both devices
	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Failed to start device B: %v", err)
	}
	defer deviceB.Stop()

	// Track connection callbacks
	var aRole, bRole ConnectionRole
	var wg sync.WaitGroup
	wg.Add(2)

	deviceA.SetConnectCallback(func(peerUUID string, role ConnectionRole) {
		if peerUUID == "device-b-uuid" {
			aRole = role
			wg.Done()
		}
	})

	deviceB.SetConnectCallback(func(peerUUID string, role ConnectionRole) {
		if peerUUID == "device-a-uuid" {
			bRole = role
			wg.Done()
		}
	})

	// Wait for listeners to be ready
	time.Sleep(100 * time.Millisecond)

	// Device A connects to Device B
	err := deviceA.Connect("device-b-uuid")
	if err != nil {
		t.Fatalf("Failed to connect A → B: %v", err)
	}

	// Wait for both callbacks
	wg.Wait()

	// Verify A has Central role (it initiated)
	if aRole != RoleCentral {
		t.Errorf("Device A should be Central, got %s", aRole)
	}

	// Verify B has Peripheral role (it accepted)
	if bRole != RolePeripheral {
		t.Errorf("Device B should be Peripheral, got %s", bRole)
	}

	// Double-check via GetConnectionRole
	roleA, ok := deviceA.GetConnectionRole("device-b-uuid")
	if !ok {
		t.Error("Device A should have connection role for B")
	}
	if roleA != RoleCentral {
		t.Errorf("Device A should have Central role, got %s", roleA)
	}

	roleB, ok := deviceB.GetConnectionRole("device-a-uuid")
	if !ok {
		t.Error("Device B should have connection role for A")
	}
	if roleB != RolePeripheral {
		t.Errorf("Device B should have Peripheral role, got %s", roleB)
	}
}

// TestBidirectionalGATTMessages verifies that GATT messages can flow both directions
func TestBidirectionalGATTMessages(t *testing.T) {
	util.SetRandom()
	// Create two devices
	deviceA := NewWire("device-a-uuid")
	deviceB := NewWire("device-b-uuid")

	// Start both devices
	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Failed to start device B: %v", err)
	}
	defer deviceB.Stop()

	// Track received messages
	var wg sync.WaitGroup
	wg.Add(2)

	var receivedByA *GATTMessage
	var receivedByB *GATTMessage

	deviceA.SetGATTMessageHandler(func(peerUUID string, msg *GATTMessage) {
		if peerUUID == "device-b-uuid" {
			receivedByA = msg
			wg.Done()
		}
	})

	deviceB.SetGATTMessageHandler(func(peerUUID string, msg *GATTMessage) {
		if peerUUID == "device-a-uuid" {
			receivedByB = msg
			wg.Done()
		}
	})

	// Wait for listeners to be ready
	time.Sleep(100 * time.Millisecond)

	// Device A connects to Device B
	err := deviceA.Connect("device-b-uuid")
	if err != nil {
		t.Fatalf("Failed to connect A → B: %v", err)
	}

	// Wait for connection to establish
	time.Sleep(100 * time.Millisecond)

	// Device A (Central) sends GATT write request to B
	msgAtoB := &GATTMessage{
		Type:               "gatt_request",
		RequestID:          "req-1",
		Operation:          "write",
		ServiceUUID:        "service-uuid-1",
		CharacteristicUUID: "char-uuid-1",
		Data:               []byte("Hello from A"),
	}
	err = deviceA.SendGATTMessage("device-b-uuid", msgAtoB)
	if err != nil {
		t.Fatalf("Failed to send A → B: %v", err)
	}

	// Device B (Peripheral) sends GATT notification to A
	msgBtoA := &GATTMessage{
		Type:               "gatt_notification",
		ServiceUUID:        "service-uuid-2",
		CharacteristicUUID: "char-uuid-2",
		Data:               []byte("Notification from B"),
	}
	err = deviceB.SendGATTMessage("device-a-uuid", msgBtoA)
	if err != nil {
		t.Fatalf("Failed to send B → A: %v", err)
	}

	// Wait for both messages to be received
	wg.Wait()

	// Verify A received notification from B
	if receivedByA == nil {
		t.Fatal("Device A did not receive message from B")
	}
	if receivedByA.Type != "gatt_notification" {
		t.Errorf("Expected notification, got %s", receivedByA.Type)
	}
	if string(receivedByA.Data) != "Notification from B" {
		t.Errorf("Unexpected data in A: %s", string(receivedByA.Data))
	}

	// Verify B received write request from A
	if receivedByB == nil {
		t.Fatal("Device B did not receive message from A")
	}
	if receivedByB.Type != "gatt_request" {
		t.Errorf("Expected request, got %s", receivedByB.Type)
	}
	if receivedByB.Operation != "write" {
		t.Errorf("Expected write operation, got %s", receivedByB.Operation)
	}
	if string(receivedByB.Data) != "Hello from A" {
		t.Errorf("Unexpected data in B: %s", string(receivedByB.Data))
	}
}

// TestDisconnectCallback verifies that disconnect callbacks are triggered
func TestDisconnectCallback(t *testing.T) {
	util.SetRandom()
	// Create two devices
	deviceA := NewWire("device-a-uuid")
	deviceB := NewWire("device-b-uuid")

	// Start both devices
	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Failed to start device B: %v", err)
	}
	defer deviceB.Stop()

	// Track disconnections
	var wg sync.WaitGroup
	wg.Add(2)

	var aDisconnected, bDisconnected bool

	deviceA.SetDisconnectCallback(func(peerUUID string) {
		if peerUUID == "device-b-uuid" {
			aDisconnected = true
			wg.Done()
		}
	})

	deviceB.SetDisconnectCallback(func(peerUUID string) {
		if peerUUID == "device-a-uuid" {
			bDisconnected = true
			wg.Done()
		}
	})

	// Wait for listeners to be ready
	time.Sleep(100 * time.Millisecond)

	// Connect
	err := deviceA.Connect("device-b-uuid")
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify connected
	if !deviceA.IsConnected("device-b-uuid") {
		t.Fatal("Devices should be connected")
	}

	// Stop device B (triggers disconnect)
	deviceB.Stop()

	// Wait for disconnect callbacks
	wg.Wait()

	// Verify both devices detected the disconnect
	if !aDisconnected {
		t.Error("Device A should have detected disconnect")
	}
	if !bDisconnected {
		t.Error("Device B should have detected disconnect")
	}

	// Verify A no longer shows B as connected
	if deviceA.IsConnected("device-b-uuid") {
		t.Error("Device A should not be connected to B after disconnect")
	}
}

// TestNoDoubleConnection verifies that connecting twice doesn't create two connections
func TestNoDoubleConnection(t *testing.T) {
	util.SetRandom()
	// Create two devices
	deviceA := NewWire("device-a-uuid")
	deviceB := NewWire("device-b-uuid")

	// Start both devices
	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Failed to start device B: %v", err)
	}
	defer deviceB.Stop()

	time.Sleep(100 * time.Millisecond)

	// Connect first time
	err := deviceA.Connect("device-b-uuid")
	if err != nil {
		t.Fatalf("Failed first connect: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Try to connect again (should return error - already connected)
	err = deviceA.Connect("device-b-uuid")
	if err == nil {
		t.Fatal("Second connect should have failed with 'already connected' error")
	}

	// Verify still only ONE connection
	peersA := deviceA.GetConnectedPeers()
	if len(peersA) != 1 {
		t.Errorf("Device A should have exactly 1 connection, got %d", len(peersA))
	}

	peersB := deviceB.GetConnectedPeers()
	if len(peersB) != 1 {
		t.Errorf("Device B should have exactly 1 connection, got %d", len(peersB))
	}
}
