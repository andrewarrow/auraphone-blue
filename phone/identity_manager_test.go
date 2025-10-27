package phone

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityManager_NewIdentityManager(t *testing.T) {
	tempDir := t.TempDir()
	ourHardwareUUID := "test-hardware-uuid-123"
	ourDeviceID := "test-device-id-abc"

	im := NewIdentityManager(ourHardwareUUID, ourDeviceID, tempDir)

	if im.GetOurHardwareUUID() != ourHardwareUUID {
		t.Errorf("Expected hardware UUID %s, got %s", ourHardwareUUID, im.GetOurHardwareUUID())
	}

	if im.GetOurDeviceID() != ourDeviceID {
		t.Errorf("Expected device ID %s, got %s", ourDeviceID, im.GetOurDeviceID())
	}

	// Verify we can look ourselves up
	if deviceID, ok := im.GetDeviceID(ourHardwareUUID); !ok || deviceID != ourDeviceID {
		t.Errorf("Expected to find our own device ID, got %s, %v", deviceID, ok)
	}

	if hardwareUUID, ok := im.GetHardwareUUID(ourDeviceID); !ok || hardwareUUID != ourHardwareUUID {
		t.Errorf("Expected to find our own hardware UUID, got %s, %v", hardwareUUID, ok)
	}
}

func TestIdentityManager_RegisterDevice(t *testing.T) {
	tempDir := t.TempDir()
	im := NewIdentityManager("our-uuid", "our-device", tempDir)

	// Register a new device
	hardwareUUID := "remote-hardware-uuid"
	deviceID := "remote-device-id"
	im.RegisterDevice(hardwareUUID, deviceID)

	// Verify bidirectional lookup
	if retrievedDeviceID, ok := im.GetDeviceID(hardwareUUID); !ok || retrievedDeviceID != deviceID {
		t.Errorf("Expected device ID %s, got %s, %v", deviceID, retrievedDeviceID, ok)
	}

	if retrievedHardwareUUID, ok := im.GetHardwareUUID(deviceID); !ok || retrievedHardwareUUID != hardwareUUID {
		t.Errorf("Expected hardware UUID %s, got %s, %v", hardwareUUID, retrievedHardwareUUID, ok)
	}

	// Verify mapping count
	if count := im.GetMappingCount(); count != 1 {
		t.Errorf("Expected 1 mapping (excluding self), got %d", count)
	}
}

func TestIdentityManager_RegisterDevice_Empty(t *testing.T) {
	tempDir := t.TempDir()
	im := NewIdentityManager("our-uuid", "our-device", tempDir)

	// Register with empty values should be ignored
	im.RegisterDevice("", "device-id")
	im.RegisterDevice("hardware-uuid", "")
	im.RegisterDevice("", "")

	// Should still only have our own mapping
	if count := im.GetMappingCount(); count != 0 {
		t.Errorf("Expected 0 external mappings, got %d", count)
	}
}

func TestIdentityManager_UpdateMapping(t *testing.T) {
	tempDir := t.TempDir()
	im := NewIdentityManager("our-uuid", "our-device", tempDir)

	// Register initial mapping
	im.RegisterDevice("hardware-1", "device-1")

	// Update: same hardware UUID, different device ID
	im.RegisterDevice("hardware-1", "device-2")

	// Verify new mapping
	if deviceID, ok := im.GetDeviceID("hardware-1"); !ok || deviceID != "device-2" {
		t.Errorf("Expected device-2, got %s, %v", deviceID, ok)
	}

	// Old device ID should not map to anything
	if _, ok := im.GetHardwareUUID("device-1"); ok {
		t.Error("Expected device-1 to be removed from mapping")
	}

	// New device ID should map correctly
	if hardwareUUID, ok := im.GetHardwareUUID("device-2"); !ok || hardwareUUID != "hardware-1" {
		t.Errorf("Expected hardware-1, got %s, %v", hardwareUUID, ok)
	}
}

func TestIdentityManager_ConnectionState(t *testing.T) {
	tempDir := t.TempDir()
	im := NewIdentityManager("our-uuid", "our-device", tempDir)

	hardwareUUID := "remote-hardware-uuid"
	deviceID := "remote-device-id"
	im.RegisterDevice(hardwareUUID, deviceID)

	// Initially should not be connected
	if im.IsConnected(hardwareUUID) {
		t.Error("Device should not be connected initially")
	}

	if im.IsConnectedByDeviceID(deviceID) {
		t.Error("Device should not be connected initially (by device ID)")
	}

	// Mark as connected
	im.MarkConnected(hardwareUUID)

	if !im.IsConnected(hardwareUUID) {
		t.Error("Device should be connected after MarkConnected")
	}

	if !im.IsConnectedByDeviceID(deviceID) {
		t.Error("Device should be connected (by device ID)")
	}

	// Verify connected count
	if count := im.GetConnectedCount(); count != 1 {
		t.Errorf("Expected 1 connected device, got %d", count)
	}

	// Mark as disconnected
	im.MarkDisconnected(hardwareUUID)

	if im.IsConnected(hardwareUUID) {
		t.Error("Device should not be connected after MarkDisconnected")
	}

	if im.IsConnectedByDeviceID(deviceID) {
		t.Error("Device should not be connected (by device ID) after disconnect")
	}

	// Verify connected count
	if count := im.GetConnectedCount(); count != 0 {
		t.Errorf("Expected 0 connected devices, got %d", count)
	}
}

func TestIdentityManager_GetAllConnectedDevices(t *testing.T) {
	tempDir := t.TempDir()
	im := NewIdentityManager("our-uuid", "our-device", tempDir)

	// Register and connect multiple devices
	devices := map[string]string{
		"hardware-1": "device-1",
		"hardware-2": "device-2",
		"hardware-3": "device-3",
	}

	for hardwareUUID, deviceID := range devices {
		im.RegisterDevice(hardwareUUID, deviceID)
		im.MarkConnected(hardwareUUID)
	}

	// Disconnect one device
	im.MarkDisconnected("hardware-2")

	// Get all connected devices
	connectedDevices := im.GetAllConnectedDevices()

	// Should have 2 connected devices
	if len(connectedDevices) != 2 {
		t.Errorf("Expected 2 connected devices, got %d", len(connectedDevices))
	}

	// Verify correct devices are in the list
	connectedMap := make(map[string]bool)
	for _, deviceID := range connectedDevices {
		connectedMap[deviceID] = true
	}

	if !connectedMap["device-1"] {
		t.Error("Expected device-1 to be in connected list")
	}
	if connectedMap["device-2"] {
		t.Error("Expected device-2 to NOT be in connected list")
	}
	if !connectedMap["device-3"] {
		t.Error("Expected device-3 to be in connected list")
	}
}

func TestIdentityManager_GetAllKnownDevices(t *testing.T) {
	tempDir := t.TempDir()
	im := NewIdentityManager("our-uuid", "our-device", tempDir)

	// Register multiple devices
	devices := map[string]string{
		"hardware-1": "device-1",
		"hardware-2": "device-2",
		"hardware-3": "device-3",
	}

	for hardwareUUID, deviceID := range devices {
		im.RegisterDevice(hardwareUUID, deviceID)
	}

	// Get all known devices
	knownDevices := im.GetAllKnownDevices()

	// Should have 3 devices (not including ourselves)
	if len(knownDevices) != 3 {
		t.Errorf("Expected 3 known devices, got %d", len(knownDevices))
	}

	// Verify our own device ID is not in the list
	for _, deviceID := range knownDevices {
		if deviceID == "our-device" {
			t.Error("Our own device ID should not be in the known devices list")
		}
	}

	// Verify all external devices are in the list
	knownMap := make(map[string]bool)
	for _, deviceID := range knownDevices {
		knownMap[deviceID] = true
	}

	for _, deviceID := range devices {
		if !knownMap[deviceID] {
			t.Errorf("Expected %s to be in known devices list", deviceID)
		}
	}
}

func TestIdentityManager_Persistence(t *testing.T) {
	tempDir := t.TempDir()
	ourHardwareUUID := "our-uuid"
	ourDeviceID := "our-device"

	// Create first instance and register devices
	im1 := NewIdentityManager(ourHardwareUUID, ourDeviceID, tempDir)
	im1.RegisterDevice("hardware-1", "device-1")
	im1.RegisterDevice("hardware-2", "device-2")

	// Save to disk
	if err := im1.SaveToDisk(); err != nil {
		t.Fatalf("Failed to save to disk: %v", err)
	}

	// Verify file was created
	statePath := filepath.Join(tempDir, "identity_mappings.json")
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		t.Fatal("State file was not created")
	}

	// Create second instance and load from disk
	im2 := NewIdentityManager(ourHardwareUUID, ourDeviceID, tempDir)
	if err := im2.LoadFromDisk(); err != nil {
		t.Fatalf("Failed to load from disk: %v", err)
	}

	// Verify mappings were restored
	if deviceID, ok := im2.GetDeviceID("hardware-1"); !ok || deviceID != "device-1" {
		t.Errorf("Expected to restore device-1 mapping, got %s, %v", deviceID, ok)
	}

	if deviceID, ok := im2.GetDeviceID("hardware-2"); !ok || deviceID != "device-2" {
		t.Errorf("Expected to restore device-2 mapping, got %s, %v", deviceID, ok)
	}

	if count := im2.GetMappingCount(); count != 2 {
		t.Errorf("Expected 2 restored mappings, got %d", count)
	}
}

func TestIdentityManager_PersistenceIdentityMismatch(t *testing.T) {
	tempDir := t.TempDir()

	// Create first instance with one identity
	im1 := NewIdentityManager("uuid-1", "device-1", tempDir)
	im1.RegisterDevice("hardware-1", "device-1")
	if err := im1.SaveToDisk(); err != nil {
		t.Fatalf("Failed to save to disk: %v", err)
	}

	// Create second instance with different identity
	im2 := NewIdentityManager("uuid-2", "device-2", tempDir)
	if err := im2.LoadFromDisk(); err != nil {
		t.Fatalf("Failed to load from disk: %v", err)
	}

	// Should not load mappings due to identity mismatch
	if count := im2.GetMappingCount(); count != 0 {
		t.Errorf("Expected 0 mappings due to identity mismatch, got %d", count)
	}
}

func TestIdentityManager_LoadFromDisk_NoFile(t *testing.T) {
	tempDir := t.TempDir()
	im := NewIdentityManager("our-uuid", "our-device", tempDir)

	// Should not error when file doesn't exist
	if err := im.LoadFromDisk(); err != nil {
		t.Errorf("Expected no error when file doesn't exist, got %v", err)
	}

	// Should have no external mappings
	if count := im.GetMappingCount(); count != 0 {
		t.Errorf("Expected 0 mappings, got %d", count)
	}
}

func TestIdentityManager_MarkConnected_Empty(t *testing.T) {
	tempDir := t.TempDir()
	im := NewIdentityManager("our-uuid", "our-device", tempDir)

	// Should not crash or affect state
	im.MarkConnected("")
	im.MarkDisconnected("")

	if count := im.GetConnectedCount(); count != 0 {
		t.Errorf("Expected 0 connected devices, got %d", count)
	}
}

func TestIdentityManager_ConcurrentAccess(t *testing.T) {
	tempDir := t.TempDir()
	im := NewIdentityManager("our-uuid", "our-device", tempDir)

	// Run concurrent operations
	done := make(chan bool)

	// Writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			hardwareUUID := "hardware-" + string(rune(i))
			deviceID := "device-" + string(rune(i))
			im.RegisterDevice(hardwareUUID, deviceID)
			im.MarkConnected(hardwareUUID)
			im.MarkDisconnected(hardwareUUID)
		}
		done <- true
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < 100; i++ {
			im.GetMappingCount()
			im.GetConnectedCount()
			im.GetAllKnownDevices()
			im.GetAllConnectedDevices()
		}
		done <- true
	}()

	// Wait for both goroutines
	<-done
	<-done

	// Should not crash (race detector will catch issues)
}

func TestIdentityManager_IsConnectedByDeviceID_UnknownDevice(t *testing.T) {
	tempDir := t.TempDir()
	im := NewIdentityManager("our-uuid", "our-device", tempDir)

	// Unknown device should return false
	if im.IsConnectedByDeviceID("unknown-device") {
		t.Error("Unknown device should not be connected")
	}
}

func TestIdentityManager_MultipleConnectionCycles(t *testing.T) {
	tempDir := t.TempDir()
	im := NewIdentityManager("our-uuid", "our-device", tempDir)

	hardwareUUID := "hardware-1"
	deviceID := "device-1"
	im.RegisterDevice(hardwareUUID, deviceID)

	// Connect and disconnect multiple times
	for i := 0; i < 10; i++ {
		im.MarkConnected(hardwareUUID)
		if !im.IsConnected(hardwareUUID) {
			t.Errorf("Iteration %d: Device should be connected", i)
		}

		im.MarkDisconnected(hardwareUUID)
		if im.IsConnected(hardwareUUID) {
			t.Errorf("Iteration %d: Device should be disconnected", i)
		}
	}

	// Final state should be disconnected
	if im.IsConnected(hardwareUUID) {
		t.Error("Final state should be disconnected")
	}

	if count := im.GetConnectedCount(); count != 0 {
		t.Errorf("Expected 0 connected devices, got %d", count)
	}
}
