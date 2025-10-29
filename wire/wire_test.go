package wire

import (
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
