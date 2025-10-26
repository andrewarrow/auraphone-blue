package iphone

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/user/auraphone-blue/phone"
)

// TestIOSGossipExchange verifies that gossip protocol works end-to-end with iOS devices
func TestIOSGossipExchange(t *testing.T) {
	// Clean up any leftover test data to prevent cross-test contamination
	phone.CleanupDataDir()

	// Create 3 iOS devices
	device1 := NewIPhone("device1-uuid")
	device2 := NewIPhone("device2-uuid")
	device3 := NewIPhone("device3-uuid")

	if device1 == nil || device2 == nil || device3 == nil {
		t.Fatal("Failed to create iPhone devices")
	}

	defer device1.Stop()
	defer device2.Stop()
	defer device3.Stop()

	// Create temporary test photos
	testDir := t.TempDir()
	photo1Path := filepath.Join(testDir, "photo1.jpg")
	photo2Path := filepath.Join(testDir, "photo2.jpg")
	photo3Path := filepath.Join(testDir, "photo3.jpg")

	if err := os.WriteFile(photo1Path, []byte("photo data for device 1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(photo2Path, []byte("photo data for device 2"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(photo3Path, []byte("photo data for device 3"), 0644); err != nil {
		t.Fatal(err)
	}

	// Set profile photos (this will initialize photoHash properly)
	if err := device1.SetProfilePhoto(photo1Path); err != nil {
		t.Fatal(err)
	}
	if err := device2.SetProfilePhoto(photo2Path); err != nil {
		t.Fatal(err)
	}
	if err := device3.SetProfilePhoto(photo3Path); err != nil {
		t.Fatal(err)
	}

	// Start all devices (begins advertising and scanning)
	device1.Start()
	device2.Start()
	device3.Start()

	// Wait for:
	// - Discovery (500ms scan delay)
	// - Connections to establish (up to 100ms per connection)
	// - Initial gossip exchange (immediate after connection)
	// - Gossip to be processed and merged into mesh views
	// - First gossip loop to run (5s interval starts when device starts)
	time.Sleep(6 * time.Second)

	// Get mesh views
	mesh1 := device1.GetMeshView()
	mesh2 := device2.GetMeshView()
	mesh3 := device3.GetMeshView()

	// Verify mesh views converged
	// Each device should eventually know about other devices (not including self)
	// With 3 devices total, each should know about at least 1-2 others
	checkMeshConvergence := func(deviceName string, meshView *phone.MeshView, expectedMinCount int) {
		devices := meshView.GetAllDevices()
		actualCount := len(devices)

		if actualCount < expectedMinCount {
			t.Errorf("%s mesh view has %d devices, expected at least %d",
				deviceName, actualCount, expectedMinCount)
		}
	}

	// Each device should know about at least 1 other device
	// (mesh view doesn't include self, only other devices)
	checkMeshConvergence("device1", mesh1, 1)
	checkMeshConvergence("device2", mesh2, 1)
	checkMeshConvergence("device3", mesh3, 1)

	t.Logf("✅ Device1 mesh view: %d devices", len(mesh1.GetAllDevices()))
	t.Logf("✅ Device2 mesh view: %d devices", len(mesh2.GetAllDevices()))
	t.Logf("✅ Device3 mesh view: %d devices", len(mesh3.GetAllDevices()))
}

// TestIOSGossipPersistence verifies that mesh view survives device restart
func TestIOSGossipPersistence(t *testing.T) {
	// Clean up any leftover test data to prevent cross-test contamination
	phone.CleanupDataDir()

	// Create device and get mesh view
	device1 := NewIPhone("device1-persist-uuid")
	if device1 == nil {
		t.Fatal("Failed to create iPhone device")
	}

	mesh1 := device1.GetMeshView()

	// Add some devices to mesh view
	mesh1.UpdateDevice("DEVICE2", "uuid-2", "photohash2", "Alice", 1, "profile2")
	mesh1.UpdateDevice("DEVICE3", "uuid-3", "photohash3", "Bob", 1, "profile3")

	// Save to disk
	if err := mesh1.SaveToDisk(); err != nil {
		t.Fatalf("Failed to save mesh view: %v", err)
	}

	// Stop first device
	device1.Stop()

	// Create new device with same UUID (simulates restart)
	device2 := NewIPhone("device1-persist-uuid")
	if device2 == nil {
		t.Fatal("Failed to create iPhone device on restart")
	}
	defer device2.Stop()

	mesh2 := device2.GetMeshView()

	// Verify devices were loaded from disk
	devices := mesh2.GetAllDevices()

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
	// Clean up any leftover test data to prevent cross-test contamination
	phone.CleanupDataDir()

	device := NewIPhone("device-neighbor-uuid")
	if device == nil {
		t.Fatal("Failed to create iPhone device")
	}
	defer device.Stop()

	meshView := device.GetMeshView()

	// Add 10 devices to mesh
	for i := 2; i <= 11; i++ {
		deviceID := fmt.Sprintf("DEVICE%d", i)
		hardwareUUID := fmt.Sprintf("uuid-%d", i)
		meshView.UpdateDevice(deviceID, hardwareUUID, "photohash", "Name", 1, "profile")
	}

	// Select neighbors twice - should be deterministic
	neighbors1 := meshView.SelectRandomNeighbors()
	neighbors2 := meshView.SelectRandomNeighbors()

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
