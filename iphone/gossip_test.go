package iphone

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/wire"
)

// TestIOSGossipExchange verifies that gossip protocol works end-to-end with iOS devices
func TestIOSGossipExchange(t *testing.T) {
	// Use perfect simulation for deterministic tests
	config := wire.PerfectSimulationConfig()

	// Create temporary directories for each device
	tempDir1 := t.TempDir()
	tempDir2 := t.TempDir()
	tempDir3 := t.TempDir()

	// Create 3 iOS devices
	device1 := NewIPhone("device1-uuid", "iPhone 1", tempDir1, config)
	device2 := NewIPhone("device2-uuid", "iPhone 2", tempDir2, config)
	device3 := NewIPhone("device3-uuid", "iPhone 3", tempDir3, config)

	defer device1.Cleanup()
	defer device2.Cleanup()
	defer device3.Cleanup()

	// Set device IDs (simulate handshakes)
	device1.deviceID = "DEVICE1"
	device2.deviceID = "DEVICE2"
	device3.deviceID = "DEVICE3"

	// Set unique photos for each device
	photo1 := []byte("photo data for device 1")
	photo2 := []byte("photo data for device 2")
	photo3 := []byte("photo data for device 3")

	hash1 := sha256.Sum256(photo1)
	hash2 := sha256.Sum256(photo2)
	hash3 := sha256.Sum256(photo3)

	device1.photoHash = hex.EncodeToString(hash1[:])
	device2.photoHash = hex.EncodeToString(hash2[:])
	device3.photoHash = hex.EncodeToString(hash3[:])

	// Initialize mesh views
	device1.meshView = phone.NewMeshView(device1.deviceID, device1.hardwareUUID, tempDir1, device1.logFunc)
	device2.meshView = phone.NewMeshView(device2.deviceID, device2.hardwareUUID, tempDir2, device2.logFunc)
	device3.meshView = phone.NewMeshView(device3.deviceID, device3.hardwareUUID, tempDir3, device3.logFunc)

	// Start all devices (begins advertising and scanning)
	if err := device1.Start(); err != nil {
		t.Fatalf("Failed to start device1: %v", err)
	}
	if err := device2.Start(); err != nil {
		t.Fatalf("Failed to start device2: %v", err)
	}
	if err := device3.Start(); err != nil {
		t.Fatalf("Failed to start device3: %v", err)
	}

	// Wait for discovery and connections to establish
	time.Sleep(500 * time.Millisecond)

	// Verify devices discovered each other
	if len(device1.wire.GetConnectedPeers()) == 0 {
		t.Error("Device1 should have connected to at least one peer")
	}
	if len(device2.wire.GetConnectedPeers()) == 0 {
		t.Error("Device2 should have connected to at least one peer")
	}
	if len(device3.wire.GetConnectedPeers()) == 0 {
		t.Error("Device3 should have connected to at least one peer")
	}

	// Trigger gossip manually on device1
	device1.sendGossipToNeighbors()

	// Wait for gossip to propagate
	time.Sleep(200 * time.Millisecond)

	// Trigger gossip on device2 (should include info from device1)
	device2.sendGossipToNeighbors()

	// Wait for propagation
	time.Sleep(200 * time.Millisecond)

	// Verify mesh views converged
	// Each device should eventually know about all 3 devices
	checkMeshConvergence := func(device *IPhone, expectedCount int) {
		device.meshView.RLock()
		actualCount := len(device.meshView.GetAllDevices())
		device.meshView.RUnlock()

		if actualCount < expectedCount {
			t.Errorf("%s mesh view has %d devices, expected at least %d",
				device.name, actualCount, expectedCount)
		}
	}

	// Give time for multiple gossip rounds
	time.Sleep(1 * time.Second)

	// Each device should know about itself + others (minimum 2 with partial mesh)
	checkMeshConvergence(device1, 2)
	checkMeshConvergence(device2, 2)
	checkMeshConvergence(device3, 2)

	t.Logf("✅ Device1 mesh view: %d devices", len(device1.meshView.GetAllDevices()))
	t.Logf("✅ Device2 mesh view: %d devices", len(device2.meshView.GetAllDevices()))
	t.Logf("✅ Device3 mesh view: %d devices", len(device3.meshView.GetAllDevices()))
}

// TestIOSGossipPersistence verifies that mesh view survives device restart
func TestIOSGossipPersistence(t *testing.T) {
	config := wire.PerfectSimulationConfig()
	tempDir := t.TempDir()

	// Create device and initialize mesh view
	device1 := NewIPhone("device1-uuid", "iPhone 1", tempDir, config)
	device1.deviceID = "DEVICE1"
	device1.meshView = phone.NewMeshView(device1.deviceID, device1.hardwareUUID, tempDir, device1.logFunc)

	// Add some devices to mesh view
	device1.meshView.UpdateDevice("DEVICE2", "photohash2", "Alice", 1, "profile2")
	device1.meshView.UpdateDevice("DEVICE3", "photohash3", "Bob", 1, "profile3")

	// Save to disk
	if err := device1.meshView.SaveToDisk(); err != nil {
		t.Fatalf("Failed to save mesh view: %v", err)
	}

	// Cleanup first device
	device1.Cleanup()

	// Create new device with same UUID and directory (simulates restart)
	device2 := NewIPhone("device1-uuid", "iPhone 1", tempDir, config)
	device2.deviceID = "DEVICE1"
	device2.meshView = phone.NewMeshView(device2.deviceID, device2.hardwareUUID, tempDir, device2.logFunc)

	defer device2.Cleanup()

	// Verify devices were loaded from disk
	device2.meshView.RLock()
	devices := device2.meshView.GetAllDevices()
	device2.meshView.RUnlock()

	if len(devices) < 2 {
		t.Errorf("Expected at least 2 devices to be loaded from disk, got %d", len(devices))
	}

	foundDevice2 := false
	foundDevice3 := false
	for _, dev := range devices {
		if dev.DeviceID == "DEVICE2" && dev.FirstName == "Alice" {
			foundDevice2 = true
		}
		if dev.DeviceID == "DEVICE3" && dev.FirstName == "Bob" {
			foundDevice3 = true
		}
	}

	if !foundDevice2 {
		t.Error("Expected DEVICE2 to be loaded from disk")
	}
	if !foundDevice3 {
		t.Error("Expected DEVICE3 to be loaded from disk")
	}

	t.Logf("✅ Mesh view successfully persisted and restored across restart")
}

// TestIOSGossipNeighborSelection verifies that iOS devices select neighbors deterministically
func TestIOSGossipNeighborSelection(t *testing.T) {
	config := wire.PerfectSimulationConfig()
	tempDir := t.TempDir()

	device := NewIPhone("device-uuid", "iPhone Test", tempDir, config)
	defer device.Cleanup()

	device.deviceID = "DEVICE1"
	device.meshView = phone.NewMeshView(device.deviceID, device.hardwareUUID, tempDir, device.logFunc)

	// Add 10 devices to mesh
	for i := 2; i <= 11; i++ {
		deviceID := "DEVICE" + string(rune('0'+i))
		device.meshView.UpdateDevice(deviceID, "photohash", "Name", 1, "profile")
	}

	// Select neighbors twice - should be deterministic
	neighbors1 := device.meshView.SelectRandomNeighbors()
	neighbors2 := device.meshView.SelectRandomNeighbors()

	if len(neighbors1) != len(neighbors2) {
		t.Error("Neighbor selection should be deterministic (same count)")
	}

	// Verify same neighbors selected
	for i, n := range neighbors1 {
		if n != neighbors2[i] {
			t.Error("Neighbor selection should be deterministic (same devices)")
			break
		}
	}

	// Should respect max neighbors limit (3)
	if len(neighbors1) > 3 {
		t.Errorf("Expected at most 3 neighbors, got %d", len(neighbors1))
	}

	t.Logf("✅ Selected %d neighbors deterministically", len(neighbors1))
}

// TestMain handles cleanup after all tests
func TestMain(m *testing.M) {
	code := m.Run()

	// Cleanup test socket files
	os.RemoveAll("/tmp/auraphone-*.sock")

	os.Exit(code)
}
