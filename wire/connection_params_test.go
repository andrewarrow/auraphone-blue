package wire

import (
	"testing"
	"time"

	"github.com/user/auraphone-blue/util"
	"github.com/user/auraphone-blue/wire/l2cap"
)

// TestConnectionParameterUpdate verifies that connection parameters can be requested and updated
func TestConnectionParameterUpdate(t *testing.T) {
	util.SetRandom()

	// Create two devices
	deviceA := NewWire("device-a") // Will be Peripheral
	deviceB := NewWire("device-b") // Will be Central

	// Start both devices
	if err := deviceA.Start(); err != nil {
		t.Fatalf("Device A failed to start: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Device B failed to start: %v", err)
	}
	defer deviceB.Stop()

	time.Sleep(100 * time.Millisecond) // Wait for listeners

	// Connect A to B (A becomes Central, B becomes Peripheral)
	if err := deviceA.Connect("device-b"); err != nil {
		t.Fatalf("Device A failed to connect to Device B: %v", err)
	}
	time.Sleep(150 * time.Millisecond) // Wait for connection establishment

	// Verify connection established
	if !deviceA.IsConnected("device-b") {
		t.Fatal("Device A is not connected to Device B")
	}
	if !deviceB.IsConnected("device-a") {
		t.Fatal("Device B is not connected to Device A")
	}

	// Get initial connection parameters
	initialParams, err := deviceA.GetConnectionParameters("device-b")
	if err != nil {
		t.Fatalf("Failed to get initial connection parameters: %v", err)
	}
	t.Logf("Initial connection parameters: interval=%.1f-%.1fms, latency=%d, timeout=%dms",
		initialParams.IntervalMinMs(), initialParams.IntervalMaxMs(),
		initialParams.SlaveLatency, initialParams.SupervisionTimeoutMs())

	// Device B (Peripheral) requests faster connection parameters
	fastParams := l2cap.FastConnectionParameters()
	if err := deviceB.RequestConnectionParameterUpdate("device-a", fastParams); err != nil {
		t.Fatalf("Device B failed to request connection parameter update: %v", err)
	}

	// Wait for parameter update to be processed
	time.Sleep(100 * time.Millisecond)

	// Verify parameters were updated on Device A (Central)
	updatedParams, err := deviceA.GetConnectionParameters("device-b")
	if err != nil {
		t.Fatalf("Failed to get updated connection parameters: %v", err)
	}

	if updatedParams.IntervalMin != fastParams.IntervalMin {
		t.Errorf("IntervalMin not updated: got %d, want %d", updatedParams.IntervalMin, fastParams.IntervalMin)
	}
	if updatedParams.IntervalMax != fastParams.IntervalMax {
		t.Errorf("IntervalMax not updated: got %d, want %d", updatedParams.IntervalMax, fastParams.IntervalMax)
	}
	if updatedParams.SlaveLatency != fastParams.SlaveLatency {
		t.Errorf("SlaveLatency not updated: got %d, want %d", updatedParams.SlaveLatency, fastParams.SlaveLatency)
	}
	if updatedParams.SupervisionTimeout != fastParams.SupervisionTimeout {
		t.Errorf("SupervisionTimeout not updated: got %d, want %d", updatedParams.SupervisionTimeout, fastParams.SupervisionTimeout)
	}

	t.Logf("Updated connection parameters: interval=%.1f-%.1fms, latency=%d, timeout=%dms",
		updatedParams.IntervalMinMs(), updatedParams.IntervalMaxMs(),
		updatedParams.SlaveLatency, updatedParams.SupervisionTimeoutMs())
}

// TestConnectionParameterUpdateWithInvalidParams verifies that invalid parameters are rejected
func TestConnectionParameterUpdateWithInvalidParams(t *testing.T) {
	util.SetRandom()

	// Create two devices
	deviceA := NewWire("device-a")
	deviceB := NewWire("device-b")

	// Start both devices
	if err := deviceA.Start(); err != nil {
		t.Fatalf("Device A failed to start: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Device B failed to start: %v", err)
	}
	defer deviceB.Stop()

	time.Sleep(100 * time.Millisecond)

	// Connect A to B
	if err := deviceA.Connect("device-b"); err != nil {
		t.Fatalf("Device A failed to connect to Device B: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	// Try to request invalid parameters (IntervalMax < IntervalMin)
	invalidParams := &l2cap.ConnectionParameters{
		IntervalMin:        40,
		IntervalMax:        24, // Invalid: less than IntervalMin
		SlaveLatency:       0,
		SupervisionTimeout: 600,
	}

	err := deviceB.RequestConnectionParameterUpdate("device-a", invalidParams)
	if err == nil {
		t.Error("RequestConnectionParameterUpdate should reject invalid parameters")
	}
}

// TestGetConnectionParametersBeforeConnection verifies error handling for non-existent connections
func TestGetConnectionParametersBeforeConnection(t *testing.T) {
	util.SetRandom()

	device := NewWire("device-a")
	if err := device.Start(); err != nil {
		t.Fatalf("Device failed to start: %v", err)
	}
	defer device.Stop()

	// Try to get parameters for non-existent connection
	_, err := device.GetConnectionParameters("nonexistent-device")
	if err == nil {
		t.Error("GetConnectionParameters should return error for non-existent connection")
	}
}

// TestPowerSavingConnectionParameters verifies power-saving parameter updates
func TestPowerSavingConnectionParameters(t *testing.T) {
	util.SetRandom()

	// Create two devices
	deviceA := NewWire("device-a")
	deviceB := NewWire("device-b")

	// Start both devices
	if err := deviceA.Start(); err != nil {
		t.Fatalf("Device A failed to start: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Device B failed to start: %v", err)
	}
	defer deviceB.Stop()

	time.Sleep(100 * time.Millisecond)

	// Connect A to B
	if err := deviceA.Connect("device-b"); err != nil {
		t.Fatalf("Device A failed to connect to Device B: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	// Device B requests power-saving parameters (longer interval, higher latency)
	powerSavingParams := l2cap.PowerSavingConnectionParameters()
	if err := deviceB.RequestConnectionParameterUpdate("device-a", powerSavingParams); err != nil {
		t.Fatalf("Device B failed to request power-saving parameters: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify parameters were updated
	updatedParams, err := deviceA.GetConnectionParameters("device-b")
	if err != nil {
		t.Fatalf("Failed to get updated connection parameters: %v", err)
	}

	// Power-saving mode should have non-zero slave latency
	if updatedParams.SlaveLatency == 0 {
		t.Error("Power-saving parameters should have non-zero slave latency")
	}

	// Power-saving mode should have longer intervals
	if updatedParams.IntervalMinMs() < 50 {
		t.Errorf("Power-saving parameters should have longer interval: %.1fms", updatedParams.IntervalMinMs())
	}

	t.Logf("Power-saving parameters: interval=%.1f-%.1fms, latency=%d, timeout=%dms",
		updatedParams.IntervalMinMs(), updatedParams.IntervalMaxMs(),
		updatedParams.SlaveLatency, updatedParams.SupervisionTimeoutMs())
}

// TestConnectionParametersWithMultipleConnections verifies independent parameter updates
func TestConnectionParametersWithMultipleConnections(t *testing.T) {
	util.SetRandom()

	// Create three devices
	deviceA := NewWire("device-a") // Central to both B and C
	deviceB := NewWire("device-b") // Peripheral
	deviceC := NewWire("device-c") // Peripheral

	// Start all devices
	for _, dev := range []*Wire{deviceA, deviceB, deviceC} {
		if err := dev.Start(); err != nil {
			t.Fatalf("Device failed to start: %v", err)
		}
		defer dev.Stop()
	}

	time.Sleep(100 * time.Millisecond)

	// Connect A to both B and C
	if err := deviceA.Connect("device-b"); err != nil {
		t.Fatalf("Device A failed to connect to Device B: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	if err := deviceA.Connect("device-c"); err != nil {
		t.Fatalf("Device A failed to connect to Device C: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	// Device B requests fast parameters
	fastParams := l2cap.FastConnectionParameters()
	if err := deviceB.RequestConnectionParameterUpdate("device-a", fastParams); err != nil {
		t.Fatalf("Device B failed to request fast parameters: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Device C requests power-saving parameters
	powerParams := l2cap.PowerSavingConnectionParameters()
	if err := deviceC.RequestConnectionParameterUpdate("device-a", powerParams); err != nil {
		t.Fatalf("Device C failed to request power-saving parameters: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Verify Device A has different parameters for each connection
	paramsB, err := deviceA.GetConnectionParameters("device-b")
	if err != nil {
		t.Fatalf("Failed to get parameters for device-b: %v", err)
	}

	paramsC, err := deviceA.GetConnectionParameters("device-c")
	if err != nil {
		t.Fatalf("Failed to get parameters for device-c: %v", err)
	}

	// Verify parameters are different
	if paramsB.IntervalMin == paramsC.IntervalMin && paramsB.SlaveLatency == paramsC.SlaveLatency {
		t.Error("Connection parameters should be different for each connection")
	}

	t.Logf("Device B (fast): interval=%.1f-%.1fms, latency=%d",
		paramsB.IntervalMinMs(), paramsB.IntervalMaxMs(), paramsB.SlaveLatency)
	t.Logf("Device C (power-saving): interval=%.1f-%.1fms, latency=%d",
		paramsC.IntervalMinMs(), paramsC.IntervalMaxMs(), paramsC.SlaveLatency)
}
