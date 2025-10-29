package wire

import (
	"strings"
	"testing"
	"time"

	"github.com/user/auraphone-blue/util"
	"github.com/user/auraphone-blue/wire/l2cap"
)

// TestPeripheralCanRequestParameters verifies that Peripheral can request parameter updates
func TestPeripheralCanRequestParameters(t *testing.T) {
	util.SetRandom()

	deviceA := NewWire("device-a")
	deviceB := NewWire("device-b")

	deviceA.Start()
	deviceB.Start()
	defer deviceA.Stop()
	defer deviceB.Stop()

	time.Sleep(100 * time.Millisecond)

	// A connects to B: A is Central, B is Peripheral
	err := deviceA.Connect("device-b")
	if err != nil {
		t.Fatalf("Connection failed: %v", err)
	}
	time.Sleep(150 * time.Millisecond) // Wait for MTU exchange to complete

	// B (Peripheral) should be able to request parameters
	err = deviceB.RequestConnectionParameterUpdate("device-a", l2cap.FastConnectionParameters())
	if err != nil {
		t.Errorf("Peripheral should be able to request parameters: %v", err)
	}

	time.Sleep(100 * time.Millisecond) // Wait for parameter update cycle

	// Verify parameters were updated on Central side
	params, err := deviceA.GetConnectionParameters("device-b")
	if err != nil {
		t.Fatalf("Failed to get connection parameters: %v", err)
	}

	expectedInterval := l2cap.FastConnectionParameters().IntervalMinMs()
	if params.IntervalMinMs() != expectedInterval {
		t.Errorf("Parameters not updated: got interval %.1fms, want %.1fms",
			params.IntervalMinMs(), expectedInterval)
	}
}

// TestCentralCannotRequestParameters verifies that Central cannot request (must use Set instead)
func TestCentralCannotRequestParameters(t *testing.T) {
	util.SetRandom()

	deviceA := NewWire("device-a")
	deviceB := NewWire("device-b")

	deviceA.Start()
	deviceB.Start()
	defer deviceA.Stop()
	defer deviceB.Stop()

	time.Sleep(100 * time.Millisecond)

	// A connects to B: A is Central, B is Peripheral
	err := deviceA.Connect("device-b")
	if err != nil {
		t.Fatalf("Connection failed: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// A (Central) should NOT be able to request parameters
	err = deviceA.RequestConnectionParameterUpdate("device-b", l2cap.FastConnectionParameters())
	if err == nil {
		t.Error("Central should not be able to request parameters (should use SetConnectionParameters)")
	}

	// Error should mention role restriction
	if !strings.Contains(err.Error(), "only peripheral") {
		t.Errorf("Error should mention role restriction, got: %v", err)
	}
}

// TestCentralCanSetParameters verifies that Central can set parameters directly
func TestCentralCanSetParameters(t *testing.T) {
	util.SetRandom()

	deviceA := NewWire("device-a")
	deviceB := NewWire("device-b")

	deviceA.Start()
	deviceB.Start()
	defer deviceA.Stop()
	defer deviceB.Stop()

	time.Sleep(100 * time.Millisecond)

	// A connects to B: A is Central, B is Peripheral
	err := deviceA.Connect("device-b")
	if err != nil {
		t.Fatalf("Connection failed: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// A (Central) can set parameters directly
	fastParams := l2cap.FastConnectionParameters()
	err = deviceA.SetConnectionParameters("device-b", fastParams)
	if err != nil {
		t.Errorf("Central should be able to set parameters: %v", err)
	}

	// Verify parameters were set
	params, err := deviceA.GetConnectionParameters("device-b")
	if err != nil {
		t.Fatalf("Failed to get connection parameters: %v", err)
	}

	if params.IntervalMinMs() != fastParams.IntervalMinMs() {
		t.Errorf("Parameters not set: got interval %.1fms, want %.1fms",
			params.IntervalMinMs(), fastParams.IntervalMinMs())
	}
}

// TestPeripheralCannotSetParameters verifies that Peripheral cannot set parameters
func TestPeripheralCannotSetParameters(t *testing.T) {
	util.SetRandom()

	deviceA := NewWire("device-a")
	deviceB := NewWire("device-b")

	deviceA.Start()
	deviceB.Start()
	defer deviceA.Stop()
	defer deviceB.Stop()

	time.Sleep(100 * time.Millisecond)

	// A connects to B: A is Central, B is Peripheral
	err := deviceA.Connect("device-b")
	if err != nil {
		t.Fatalf("Connection failed: %v", err)
	}
	time.Sleep(150 * time.Millisecond) // Wait for MTU exchange to complete

	// B (Peripheral) should NOT be able to set parameters
	err = deviceB.SetConnectionParameters("device-a", l2cap.PowerSavingConnectionParameters())
	if err == nil {
		t.Error("Peripheral should not be able to set parameters directly")
	}

	// Error should mention role restriction
	if !strings.Contains(err.Error(), "only central") {
		t.Errorf("Error should mention role restriction, got: %v", err)
	}
}

// TestCentralAcceptsPeripheralRequest verifies Central accepts valid parameter requests
func TestCentralAcceptsPeripheralRequest(t *testing.T) {
	util.SetRandom()

	deviceA := NewWire("device-a")
	deviceB := NewWire("device-b")

	deviceA.Start()
	deviceB.Start()
	defer deviceA.Stop()
	defer deviceB.Stop()

	time.Sleep(100 * time.Millisecond)

	// A connects to B: A is Central, B is Peripheral
	err := deviceA.Connect("device-b")
	if err != nil {
		t.Fatalf("Connection failed: %v", err)
	}
	time.Sleep(150 * time.Millisecond) // Wait for MTU exchange to complete

	// B (Peripheral) requests parameter update
	powerSavingParams := l2cap.PowerSavingConnectionParameters()
	err = deviceB.RequestConnectionParameterUpdate("device-a", powerSavingParams)
	if err != nil {
		t.Fatalf("Parameter request failed: %v", err)
	}

	// Wait for request/response cycle
	time.Sleep(100 * time.Millisecond)

	// Central should have accepted and applied the new parameters
	params, err := deviceA.GetConnectionParameters("device-b")
	if err != nil {
		t.Fatalf("Failed to get connection parameters: %v", err)
	}

	if params.IntervalMinMs() != powerSavingParams.IntervalMinMs() {
		t.Errorf("Central did not accept parameters: got interval %.1fms, want %.1fms",
			params.IntervalMinMs(), powerSavingParams.IntervalMinMs())
	}
}

// TestRoleBasedDisconnect verifies disconnect logging differs by role
func TestRoleBasedDisconnect(t *testing.T) {
	util.SetRandom()

	// Test 1: Central disconnect
	t.Run("CentralDisconnect", func(t *testing.T) {
		deviceA := NewWire("device-a-1")
		deviceB := NewWire("device-b-1")

		deviceA.Start()
		deviceB.Start()
		defer deviceA.Stop()
		defer deviceB.Stop()

		time.Sleep(100 * time.Millisecond)

		err := deviceA.Connect("device-b-1")
		if err != nil {
			t.Fatalf("Connection failed: %v", err)
		}
		time.Sleep(100 * time.Millisecond)

		// Central (A) disconnects - should succeed
		err = deviceA.Disconnect("device-b-1")
		if err != nil {
			t.Errorf("Central disconnect failed: %v", err)
		}
	})

	// Test 2: Peripheral disconnect
	t.Run("PeripheralDisconnect", func(t *testing.T) {
		deviceA := NewWire("device-a-2")
		deviceB := NewWire("device-b-2")

		deviceA.Start()
		deviceB.Start()
		defer deviceA.Stop()
		defer deviceB.Stop()

		time.Sleep(100 * time.Millisecond)

		err := deviceA.Connect("device-b-2")
		if err != nil {
			t.Fatalf("Connection failed: %v", err)
		}
		time.Sleep(100 * time.Millisecond)

		// Peripheral (B) disconnects - should succeed (but logged differently)
		err = deviceB.Disconnect("device-a-2")
		if err != nil {
			t.Errorf("Peripheral disconnect failed: %v", err)
		}
	})
}

// TestRoleAssignmentByConnectionDirection verifies roles are assigned correctly
func TestRoleAssignmentByConnectionDirection(t *testing.T) {
	util.SetRandom()

	deviceA := NewWire("device-a")
	deviceB := NewWire("device-b")

	deviceA.Start()
	deviceB.Start()
	defer deviceA.Stop()
	defer deviceB.Stop()

	time.Sleep(100 * time.Millisecond)

	// A connects to B
	err := deviceA.Connect("device-b")
	if err != nil {
		t.Fatalf("Connection failed: %v", err)
	}
	time.Sleep(150 * time.Millisecond) // Wait for MTU exchange to complete

	// Verify roles
	deviceA.mu.RLock()
	connAB := deviceA.connections["device-b"]
	if connAB == nil {
		t.Fatal("Connection not found on device A")
	}
	roleA := connAB.role
	deviceA.mu.RUnlock()

	deviceB.mu.RLock()
	connBA := deviceB.connections["device-a"]
	if connBA == nil {
		t.Fatal("Connection not found on device B")
	}
	roleB := connBA.role
	deviceB.mu.RUnlock()

	if roleA != RoleCentral {
		t.Errorf("Device A should be Central, got: %s", roleA)
	}

	if roleB != RolePeripheral {
		t.Errorf("Device B should be Peripheral, got: %s", roleB)
	}

	// Verify role-based operations work as expected

	// Central (A) can set parameters
	err = deviceA.SetConnectionParameters("device-b", l2cap.FastConnectionParameters())
	if err != nil {
		t.Errorf("Central should be able to set parameters: %v", err)
	}

	// Peripheral (B) can request parameters
	err = deviceB.RequestConnectionParameterUpdate("device-a", l2cap.PowerSavingConnectionParameters())
	if err != nil {
		t.Errorf("Peripheral should be able to request parameters: %v", err)
	}
}
