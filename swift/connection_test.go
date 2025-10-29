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
