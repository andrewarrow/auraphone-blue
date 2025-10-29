package wire

import (
	"sync"
	"testing"
	"time"

	"github.com/user/auraphone-blue/util"
)

// TestMultiDeviceConnections verifies that one device can connect to multiple peers
func TestMultiDeviceConnections(t *testing.T) {
	util.SetRandom()
	deviceA := NewWire("device-a-uuid")
	deviceB := NewWire("device-b-uuid")
	deviceC := NewWire("device-c-uuid")
	deviceD := NewWire("device-d-uuid")

	// Start all devices
	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Failed to start device B: %v", err)
	}
	defer deviceB.Stop()

	if err := deviceC.Start(); err != nil {
		t.Fatalf("Failed to start device C: %v", err)
	}
	defer deviceC.Stop()

	if err := deviceD.Start(); err != nil {
		t.Fatalf("Failed to start device D: %v", err)
	}
	defer deviceD.Stop()

	time.Sleep(100 * time.Millisecond)

	// Device A connects to B, C, and D
	err := deviceA.Connect("device-b-uuid")
	if err != nil {
		t.Fatalf("Failed to connect A → B: %v", err)
	}

	err = deviceA.Connect("device-c-uuid")
	if err != nil {
		t.Fatalf("Failed to connect A → C: %v", err)
	}

	err = deviceA.Connect("device-d-uuid")
	if err != nil {
		t.Fatalf("Failed to connect A → D: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify A is connected to all three
	if !deviceA.IsConnected("device-b-uuid") {
		t.Error("Device A should be connected to Device B")
	}
	if !deviceA.IsConnected("device-c-uuid") {
		t.Error("Device A should be connected to Device C")
	}
	if !deviceA.IsConnected("device-d-uuid") {
		t.Error("Device A should be connected to Device D")
	}

	// Verify A has exactly 3 connections
	peers := deviceA.GetConnectedPeers()
	if len(peers) != 3 {
		t.Errorf("Device A should have 3 connections, got %d", len(peers))
	}

	// Verify each peer sees A as connected
	if !deviceB.IsConnected("device-a-uuid") {
		t.Error("Device B should be connected to Device A")
	}
	if !deviceC.IsConnected("device-a-uuid") {
		t.Error("Device C should be connected to Device A")
	}
	if !deviceD.IsConnected("device-a-uuid") {
		t.Error("Device D should be connected to Device A")
	}
}

// TestMultiDeviceMessaging verifies that messages can be sent to multiple connected peers
func TestMultiDeviceMessaging(t *testing.T) {
	util.SetRandom()
	deviceA := NewWire("device-a-uuid")
	deviceB := NewWire("device-b-uuid")
	deviceC := NewWire("device-c-uuid")

	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Failed to start device B: %v", err)
	}
	defer deviceB.Stop()

	if err := deviceC.Start(); err != nil {
		t.Fatalf("Failed to start device C: %v", err)
	}
	defer deviceC.Stop()

	// Track received messages
	var wg sync.WaitGroup
	wg.Add(2)

	var receivedByB *GATTMessage
	var receivedByC *GATTMessage

	deviceB.SetGATTMessageHandler(func(peerUUID string, msg *GATTMessage) {
		if peerUUID == "device-a-uuid" {
			receivedByB = msg
			wg.Done()
		}
	})

	deviceC.SetGATTMessageHandler(func(peerUUID string, msg *GATTMessage) {
		if peerUUID == "device-a-uuid" {
			receivedByC = msg
			wg.Done()
		}
	})

	time.Sleep(100 * time.Millisecond)

	// Device A connects to B and C
	deviceA.Connect("device-b-uuid")
	deviceA.Connect("device-c-uuid")

	time.Sleep(100 * time.Millisecond)

	// Device A sends different messages to B and C
	msgToB := &GATTMessage{
		Type:               "gatt_request",
		Operation:          "write",
		ServiceUUID:        "service-1",
		CharacteristicUUID: "char-1",
		Data:               []byte("Hello B"),
	}
	deviceA.SendGATTMessage("device-b-uuid", msgToB)

	msgToC := &GATTMessage{
		Type:               "gatt_request",
		Operation:          "write",
		ServiceUUID:        "service-2",
		CharacteristicUUID: "char-2",
		Data:               []byte("Hello C"),
	}
	deviceA.SendGATTMessage("device-c-uuid", msgToC)

	// Wait for both messages to be received
	wg.Wait()

	// Verify B received correct message
	if receivedByB == nil {
		t.Fatal("Device B did not receive message")
	}
	if string(receivedByB.Data) != "Hello B" {
		t.Errorf("Device B received wrong data: %s", string(receivedByB.Data))
	}

	// Verify C received correct message
	if receivedByC == nil {
		t.Fatal("Device C did not receive message")
	}
	if string(receivedByC.Data) != "Hello C" {
		t.Errorf("Device C received wrong data: %s", string(receivedByC.Data))
	}
}

// TestMultiDeviceDisconnect verifies that disconnecting one peer doesn't affect others
func TestMultiDeviceDisconnect(t *testing.T) {
	util.SetRandom()
	deviceA := NewWire("device-a-uuid")
	deviceB := NewWire("device-b-uuid")
	deviceC := NewWire("device-c-uuid")

	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Failed to start device B: %v", err)
	}
	defer deviceB.Stop()

	if err := deviceC.Start(); err != nil {
		t.Fatalf("Failed to start device C: %v", err)
	}
	defer deviceC.Stop()

	time.Sleep(100 * time.Millisecond)

	// Device A connects to B and C
	deviceA.Connect("device-b-uuid")
	deviceA.Connect("device-c-uuid")

	time.Sleep(100 * time.Millisecond)

	// Verify both connections exist
	if len(deviceA.GetConnectedPeers()) != 2 {
		t.Errorf("Device A should have 2 connections, got %d", len(deviceA.GetConnectedPeers()))
	}

	// Disconnect from B only
	deviceA.Disconnect("device-b-uuid")

	time.Sleep(100 * time.Millisecond)

	// Verify A is still connected to C but not B
	if deviceA.IsConnected("device-b-uuid") {
		t.Error("Device A should not be connected to B after disconnect")
	}
	if !deviceA.IsConnected("device-c-uuid") {
		t.Error("Device A should still be connected to C")
	}

	// Verify A now has only 1 connection
	peers := deviceA.GetConnectedPeers()
	if len(peers) != 1 {
		t.Errorf("Device A should have 1 connection, got %d", len(peers))
	}
	if peers[0] != "device-c-uuid" {
		t.Errorf("Device A should be connected to C, got %s", peers[0])
	}
}

// TestDiscoveryWithMultipleDevices verifies that discovery finds all available devices
func TestDiscoveryWithMultipleDevices(t *testing.T) {
	util.SetRandom()
	deviceA := NewWire("device-a-uuid")
	deviceB := NewWire("device-b-uuid")
	deviceC := NewWire("device-c-uuid")
	deviceD := NewWire("device-d-uuid")

	// Start all devices
	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Failed to start device B: %v", err)
	}
	defer deviceB.Stop()

	if err := deviceC.Start(); err != nil {
		t.Fatalf("Failed to start device C: %v", err)
	}
	defer deviceC.Stop()

	if err := deviceD.Start(); err != nil {
		t.Fatalf("Failed to start device D: %v", err)
	}
	defer deviceD.Stop()

	time.Sleep(100 * time.Millisecond)

	// Device A lists available devices
	devices := deviceA.ListAvailableDevices()

	// Should find B, C, and D (but not itself)
	if len(devices) != 3 {
		t.Errorf("Expected to find 3 devices, got %d", len(devices))
	}

	// Verify found devices
	foundB := false
	foundC := false
	foundD := false
	for _, uuid := range devices {
		switch uuid {
		case "device-b-uuid":
			foundB = true
		case "device-c-uuid":
			foundC = true
		case "device-d-uuid":
			foundD = true
		case "device-a-uuid":
			t.Error("Device A should not find itself in discovery")
		}
	}

	if !foundB {
		t.Error("Device A should have found device B")
	}
	if !foundC {
		t.Error("Device A should have found device C")
	}
	if !foundD {
		t.Error("Device A should have found device D")
	}
}

// TestStartDiscoveryCallback verifies that StartDiscovery callback is called for each device
func TestStartDiscoveryCallback(t *testing.T) {
	util.SetRandom()
	deviceA := NewWire("device-a-uuid")
	deviceB := NewWire("device-b-uuid")
	deviceC := NewWire("device-c-uuid")

	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Failed to start device B: %v", err)
	}
	defer deviceB.Stop()

	if err := deviceC.Start(); err != nil {
		t.Fatalf("Failed to start device C: %v", err)
	}
	defer deviceC.Stop()

	time.Sleep(100 * time.Millisecond)

	// Track discovered devices
	discoveredDevices := make(map[string]bool)
	var mu sync.Mutex

	stopChan := deviceA.StartDiscovery(func(deviceUUID string) {
		mu.Lock()
		discoveredDevices[deviceUUID] = true
		mu.Unlock()
	})
	defer close(stopChan)

	// Wait for discovery to run
	time.Sleep(1500 * time.Millisecond)

	// Verify both B and C were discovered
	mu.Lock()
	defer mu.Unlock()

	if !discoveredDevices["device-b-uuid"] {
		t.Error("Device B was not discovered")
	}
	if !discoveredDevices["device-c-uuid"] {
		t.Error("Device C was not discovered")
	}
	if discoveredDevices["device-a-uuid"] {
		t.Error("Device A should not discover itself")
	}
}

// TestConnectionRolesWithMultipleDevices verifies roles are correctly assigned with multiple connections
func TestConnectionRolesWithMultipleDevices(t *testing.T) {
	util.SetRandom()
	deviceA := NewWire("device-a-uuid")
	deviceB := NewWire("device-b-uuid")
	deviceC := NewWire("device-c-uuid")

	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Failed to start device B: %v", err)
	}
	defer deviceB.Stop()

	if err := deviceC.Start(); err != nil {
		t.Fatalf("Failed to start device C: %v", err)
	}
	defer deviceC.Stop()

	time.Sleep(100 * time.Millisecond)

	// Device A connects to B (A is Central)
	deviceA.Connect("device-b-uuid")

	// Device C connects to A (C is Central)
	deviceC.Connect("device-a-uuid")

	time.Sleep(100 * time.Millisecond)

	// Verify A's role with B (should be Central)
	roleAtoB, ok := deviceA.GetConnectionRole("device-b-uuid")
	if !ok {
		t.Error("Device A should have connection role for B")
	}
	if roleAtoB != RoleCentral {
		t.Errorf("Device A should be Central for B, got %s", roleAtoB)
	}

	// Verify A's role with C (should be Peripheral)
	roleAtoC, ok := deviceA.GetConnectionRole("device-c-uuid")
	if !ok {
		t.Error("Device A should have connection role for C")
	}
	if roleAtoC != RolePeripheral {
		t.Errorf("Device A should be Peripheral for C, got %s", roleAtoC)
	}

	// Verify B's role with A (should be Peripheral)
	roleBtoA, ok := deviceB.GetConnectionRole("device-a-uuid")
	if !ok {
		t.Error("Device B should have connection role for A")
	}
	if roleBtoA != RolePeripheral {
		t.Errorf("Device B should be Peripheral for A, got %s", roleBtoA)
	}

	// Verify C's role with A (should be Central)
	roleCtoA, ok := deviceC.GetConnectionRole("device-a-uuid")
	if !ok {
		t.Error("Device C should have connection role for A")
	}
	if roleCtoA != RoleCentral {
		t.Errorf("Device C should be Central for A, got %s", roleCtoA)
	}
}
