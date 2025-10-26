package phone

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/user/auraphone-blue/proto"
)

// TestMeshViewUpdateDevice verifies that devices are added and updated correctly
func TestMeshViewUpdateDevice(t *testing.T) {
	tempDir := t.TempDir()
	ourDeviceID := "DEVICE1"
	ourUUID := "uuid-1"

	meshView := NewMeshView(ourDeviceID, ourUUID, tempDir, nil)

	// Add a new device
	deviceID := "DEVICE2"
	photoHash := "abcd1234"
	firstName := "Alice"
	profileVersion := int32(1)
	profileSummaryHash := "profile123"

	meshView.UpdateDevice(deviceID, "uuid-2", photoHash, firstName, profileVersion, profileSummaryHash)

	// Verify device was added
	meshView.mu.RLock()
	device, exists := meshView.devices[deviceID]
	meshView.mu.RUnlock()

	if !exists {
		t.Fatal("Expected device to be added to mesh view")
	}

	if device.DeviceID != deviceID {
		t.Errorf("Expected deviceID %s, got %s", deviceID, device.DeviceID)
	}

	if device.PhotoHash != photoHash {
		t.Errorf("Expected photo hash %s, got %s", photoHash, device.PhotoHash)
	}

	if device.FirstName != firstName {
		t.Errorf("Expected first name %s, got %s", firstName, device.FirstName)
	}

	// Update the device with new photo
	newPhotoHash := "newphoto567"
	meshView.UpdateDevice(deviceID, "uuid-2", newPhotoHash, firstName, profileVersion, profileSummaryHash)

	meshView.mu.RLock()
	updatedDevice := meshView.devices[deviceID]
	meshView.mu.RUnlock()

	if updatedDevice.PhotoHash != newPhotoHash {
		t.Errorf("Expected updated photo hash %s, got %s", newPhotoHash, updatedDevice.PhotoHash)
	}

	// Verify PhotoRequestSent was reset when photo changed
	if updatedDevice.PhotoRequestSent {
		t.Error("Expected PhotoRequestSent to be reset when photo hash changes")
	}
}

// TestMeshViewBuildGossipMessage verifies gossip message construction
func TestMeshViewBuildGossipMessage(t *testing.T) {
	tempDir := t.TempDir()
	ourDeviceID := "DEVICE1"
	ourUUID := "uuid-1"
	ourPhotoHash := "ourphoto123"
	ourFirstName := "Bob"
	ourProfileVersion := int32(2)
	ourProfileSummaryHash := "ourprofile456"

	meshView := NewMeshView(ourDeviceID, ourUUID, tempDir, nil)

	// Add some devices to mesh
	meshView.UpdateDevice("DEVICE2", "uuid-2", "photo2", "Alice", 1, "profile2")
	meshView.UpdateDevice("DEVICE3", "uuid-3", "photo3", "Charlie", 1, "profile3")

	// Build gossip message
	gossip := meshView.BuildGossipMessage(ourPhotoHash, ourFirstName, ourProfileVersion, ourProfileSummaryHash)

	// Verify gossip contains our device
	if gossip.SenderDeviceId != ourDeviceID {
		t.Errorf("Expected sender device ID %s, got %s", ourDeviceID, gossip.SenderDeviceId)
	}

	// Verify mesh view contains all devices (ourselves + 2 others)
	expectedDevices := 3
	if len(gossip.MeshView) != expectedDevices {
		t.Errorf("Expected %d devices in mesh view, got %d", expectedDevices, len(gossip.MeshView))
	}

	// Find and verify our device in the gossip
	var foundOurDevice bool
	for _, deviceState := range gossip.MeshView {
		if deviceState.DeviceId == ourDeviceID {
			foundOurDevice = true

			ourPhotoHashBytes, _ := hex.DecodeString(ourPhotoHash)
			if string(deviceState.PhotoHash) != string(ourPhotoHashBytes) {
				t.Error("Our photo hash not correctly encoded in gossip")
			}

			if deviceState.FirstName != ourFirstName {
				t.Errorf("Expected our first name %s, got %s", ourFirstName, deviceState.FirstName)
			}
			break
		}
	}

	if !foundOurDevice {
		t.Error("Expected to find our device in gossip mesh view")
	}

	// Verify gossip round increments
	firstRound := gossip.GossipRound
	gossip2 := meshView.BuildGossipMessage(ourPhotoHash, ourFirstName, ourProfileVersion, ourProfileSummaryHash)

	if gossip2.GossipRound != firstRound+1 {
		t.Errorf("Expected gossip round to increment, got %d then %d", firstRound, gossip2.GossipRound)
	}
}

// TestMeshViewMergeGossip verifies that gossip from other devices is properly merged
func TestMeshViewMergeGossip(t *testing.T) {
	tempDir := t.TempDir()
	ourDeviceID := "DEVICE1"
	ourUUID := "uuid-1"

	meshView := NewMeshView(ourDeviceID, ourUUID, tempDir, nil)

	// Create gossip from another device
	photo2Hash := sha256.Sum256([]byte("photo2"))
	photo3Hash := sha256.Sum256([]byte("photo3"))

	gossip := &proto.GossipMessage{
		SenderDeviceId: "DEVICE2",
		Timestamp:      time.Now().Unix(),
		GossipRound:    1,
		MeshView: []*proto.DeviceState{
			{
				DeviceId:          "DEVICE2",
				PhotoHash:         photo2Hash[:],
				LastSeenTimestamp: time.Now().Unix(),
				FirstName:         "Alice",
				ProfileVersion:    1,
			},
			{
				DeviceId:          "DEVICE3",
				PhotoHash:         photo3Hash[:],
				LastSeenTimestamp: time.Now().Unix(),
				FirstName:         "Charlie",
				ProfileVersion:    1,
			},
		},
	}

	// Merge gossip
	newDiscoveries := meshView.MergeGossip(gossip)

	// Verify new devices were discovered
	if len(newDiscoveries) != 2 {
		t.Errorf("Expected 2 new discoveries, got %d", len(newDiscoveries))
	}

	// Verify devices are in our mesh view
	meshView.mu.RLock()
	device2, exists2 := meshView.devices["DEVICE2"]
	device3, exists3 := meshView.devices["DEVICE3"]
	meshView.mu.RUnlock()

	if !exists2 || !exists3 {
		t.Error("Expected both devices to be added to mesh view")
	}

	if device2.FirstName != "Alice" {
		t.Errorf("Expected first name Alice, got %s", device2.FirstName)
	}

	if device3.FirstName != "Charlie" {
		t.Errorf("Expected first name Charlie, got %s", device3.FirstName)
	}

	// Merge again with no new info - should return empty discoveries
	newDiscoveries2 := meshView.MergeGossip(gossip)
	if len(newDiscoveries2) != 0 {
		t.Errorf("Expected no new discoveries on second merge, got %d", len(newDiscoveries2))
	}
}

// TestMeshViewNeighborSelection verifies deterministic neighbor selection
func TestMeshViewNeighborSelection(t *testing.T) {
	tempDir := t.TempDir()
	ourDeviceID := "DEVICE1"
	ourUUID := "uuid-1"

	meshView := NewMeshView(ourDeviceID, ourUUID, tempDir, nil)

	// Add 10 devices
	for i := 2; i <= 11; i++ {
		deviceID := "DEVICE" + string(rune('0'+i))
		hardwareUUID := "uuid-" + string(rune('0'+i))
		meshView.UpdateDevice(deviceID, hardwareUUID, "photohash", "Name", 1, "profilehash")
	}

	// Select neighbors (should select max 3)
	neighbors := meshView.SelectRandomNeighbors()

	if len(neighbors) > meshView.maxNeighbors {
		t.Errorf("Expected at most %d neighbors, got %d", meshView.maxNeighbors, len(neighbors))
	}

	// Selection should be deterministic - select again and compare
	neighbors2 := meshView.SelectRandomNeighbors()

	if len(neighbors) != len(neighbors2) {
		t.Error("Neighbor selection should be deterministic (same count)")
	}

	// Verify same neighbors selected
	for i, n := range neighbors {
		if n != neighbors2[i] {
			t.Error("Neighbor selection should be deterministic (same devices)")
			break
		}
	}
}

// TestMeshViewGetMissingPhotos verifies that we correctly identify photos we need
func TestMeshViewGetMissingPhotos(t *testing.T) {
	tempDir := t.TempDir()
	ourDeviceID := "DEVICE1"
	ourUUID := "uuid-1"

	meshView := NewMeshView(ourDeviceID, ourUUID, tempDir, nil)

	// Add devices with different photo states
	// Device 2 has a photo we don't have
	meshView.UpdateDevice("DEVICE2", "uuid-2", "photohash456", "Bob", 1, "profile2")

	// Device 3 has no photo hash (shouldn't appear in missing)
	meshView.UpdateDevice("DEVICE3", "uuid-3", "", "Charlie", 1, "profile3")

	// Get missing photos
	missing := meshView.GetMissingPhotos()

	// Should have DEVICE2 in missing (we don't have their photo)
	// Note: The actual number depends on whether HavePhoto is correctly set during UpdateDevice
	if len(missing) == 0 {
		t.Error("Expected at least 1 missing photo")
	}

	// Find DEVICE2 in missing
	foundDevice2 := false
	for _, m := range missing {
		if m.DeviceID == "DEVICE2" {
			foundDevice2 = true
			break
		}
	}

	if !foundDevice2 {
		t.Error("Expected DEVICE2 to be in missing photos list")
	}

	// Mark photo as requested for all missing
	for _, m := range missing {
		meshView.MarkPhotoRequested(m.DeviceID)
	}

	// Should no longer appear in missing
	missing2 := meshView.GetMissingPhotos()
	if len(missing2) != 0 {
		t.Errorf("Expected no missing photos after marking all as requested, got %d", len(missing2))
	}
}

// TestMeshViewPersistence verifies that mesh view can be saved and loaded
func TestMeshViewPersistence(t *testing.T) {
	tempDir := t.TempDir()
	ourDeviceID := "DEVICE1"
	ourUUID := "uuid-1"

	meshView := NewMeshView(ourDeviceID, ourUUID, tempDir, nil)

	// Add some devices
	meshView.UpdateDevice("DEVICE2", "uuid-2", "photo2", "Alice", 1, "profile2")
	meshView.UpdateDevice("DEVICE3", "uuid-3", "photo3", "Bob", 2, "profile3")

	// Save to disk
	if err := meshView.SaveToDisk(); err != nil {
		t.Fatalf("Failed to save mesh view: %v", err)
	}

	// Create new mesh view and load
	meshView2 := NewMeshView(ourDeviceID, ourUUID, tempDir, nil)

	// Verify devices were loaded
	meshView2.mu.RLock()
	device2, exists2 := meshView2.devices["DEVICE2"]
	device3, exists3 := meshView2.devices["DEVICE3"]
	meshView2.mu.RUnlock()

	if !exists2 || !exists3 {
		t.Error("Expected devices to be loaded from disk")
	}

	if device2.FirstName != "Alice" {
		t.Errorf("Expected first name Alice after load, got %s", device2.FirstName)
	}

	if device3.ProfileVersion != 2 {
		t.Errorf("Expected profile version 2 after load, got %d", device3.ProfileVersion)
	}
}

// TestGetCurrentNeighbors verifies we can retrieve current neighbors
func TestGetCurrentNeighbors(t *testing.T) {
	tempDir := t.TempDir()
	ourDeviceID := "DEVICE1"
	ourUUID := "uuid-1"

	meshView := NewMeshView(ourDeviceID, ourUUID, tempDir, nil)

	// Add some devices
	for i := 2; i <= 5; i++ {
		deviceID := "DEVICE" + string(rune('0'+i))
		hardwareUUID := "uuid-" + string(rune('0'+i))
		meshView.UpdateDevice(deviceID, hardwareUUID, "photo", "Name", 1, "profile")
	}

	// Select neighbors
	meshView.SelectRandomNeighbors()

	// Get current neighbors
	neighbors := meshView.GetCurrentNeighbors()

	if len(neighbors) == 0 {
		t.Error("Expected to have current neighbors")
	}

	if len(neighbors) > meshView.maxNeighbors {
		t.Errorf("Expected at most %d neighbors, got %d", meshView.maxNeighbors, len(neighbors))
	}
}

// TestMarkPhotoReceived verifies photo received tracking
func TestMarkPhotoReceived(t *testing.T) {
	tempDir := t.TempDir()
	ourDeviceID := "DEVICE1"
	ourUUID := "uuid-1"

	meshView := NewMeshView(ourDeviceID, ourUUID, tempDir, nil)

	deviceID := "DEVICE2"
	photoHash := "photohash123"

	// Add device
	meshView.UpdateDevice(deviceID, "uuid-2", photoHash, "Alice", 1, "profile")

	// Initially should not have photo
	meshView.mu.RLock()
	device := meshView.devices[deviceID]
	initialHavePhoto := device.HavePhoto
	meshView.mu.RUnlock()

	if initialHavePhoto {
		t.Error("Expected HavePhoto to be false initially")
	}

	// Mark as received
	meshView.MarkPhotoReceived(deviceID, photoHash)

	// Now should have photo
	meshView.mu.RLock()
	device = meshView.devices[deviceID]
	finalHavePhoto := device.HavePhoto
	meshView.mu.RUnlock()

	if !finalHavePhoto {
		t.Error("Expected HavePhoto to be true after marking as received")
	}
}
