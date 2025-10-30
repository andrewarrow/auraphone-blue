package phone

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/user/auraphone-blue/proto"
	"github.com/user/auraphone-blue/util"
)

// ============================================================================
// Test Helpers
// ============================================================================

func setupTestMeshView(t *testing.T, deviceID, hardwareUUID string) *MeshView {
	tempDir := util.SetRandom()

	// No photo cache for these unit tests
	mv := NewMeshView(deviceID, hardwareUUID, tempDir, nil)
	mv.SetOurFirstName("TestDevice")

	return mv
}

// ============================================================================
// Device Update Tests
// ============================================================================

func TestUpdateDevice_NewDevice(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	// Add a new device
	mv.UpdateDevice("DEVICE002", "abc123", "Alice", 1)

	// Verify device was added
	devices := mv.GetAllDevices()
	if len(devices) != 1 {
		t.Fatalf("Expected 1 device, got %d", len(devices))
	}

	device := devices[0]
	if device.DeviceID != "DEVICE002" {
		t.Errorf("Expected DeviceID DEVICE002, got %s", device.DeviceID)
	}
	if device.PhotoHash != "abc123" {
		t.Errorf("Expected PhotoHash abc123, got %s", device.PhotoHash)
	}
	if device.FirstName != "Alice" {
		t.Errorf("Expected FirstName Alice, got %s", device.FirstName)
	}
	if device.ProfileVersion != 1 {
		t.Errorf("Expected ProfileVersion 1, got %d", device.ProfileVersion)
	}
}

func TestUpdateDevice_ProfileVersionUpdate(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	// Add device with version 1
	mv.UpdateDevice("DEVICE002", "abc123", "Alice", 1)

	// Update to version 2
	mv.UpdateDevice("DEVICE002", "abc123", "Alice", 2)

	devices := mv.GetAllDevices()
	if len(devices) != 1 {
		t.Fatalf("Expected 1 device, got %d", len(devices))
	}

	if devices[0].ProfileVersion != 2 {
		t.Errorf("Expected ProfileVersion 2, got %d", devices[0].ProfileVersion)
	}
}

func TestUpdateDevice_NameChange(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	// Add device
	mv.UpdateDevice("DEVICE002", "abc123", "Alice", 1)

	// Update name with new profile version
	mv.UpdateDevice("DEVICE002", "abc123", "Alicia", 2)

	devices := mv.GetAllDevices()
	if devices[0].FirstName != "Alicia" {
		t.Errorf("Expected FirstName Alicia, got %s", devices[0].FirstName)
	}
	if devices[0].ProfileVersion != 2 {
		t.Errorf("Expected ProfileVersion 2, got %d", devices[0].ProfileVersion)
	}
}

func TestUpdateDevice_PhotoHashUpdate(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	// Add device with photo hash
	mv.UpdateDevice("DEVICE002", "abc123", "Alice", 1)

	// Update photo hash
	mv.UpdateDevice("DEVICE002", "def456", "Alice", 2)

	devices := mv.GetAllDevices()
	if devices[0].PhotoHash != "def456" {
		t.Errorf("Expected PhotoHash def456, got %s", devices[0].PhotoHash)
	}

	// Verify PhotoRequestSent was reset
	if devices[0].PhotoRequestSent {
		t.Error("PhotoRequestSent should be reset when photo hash changes")
	}
}

// ============================================================================
// Gossip Building Tests
// ============================================================================

func TestBuildGossipMessage_EmptyMesh(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	gossipMsg := mv.BuildGossipMessage("myphoto123", 1)

	// Should contain only ourselves
	if len(gossipMsg.MeshView) != 1 {
		t.Errorf("Expected 1 device in gossip, got %d", len(gossipMsg.MeshView))
	}

	if gossipMsg.SenderDeviceId != "DEVICE001" {
		t.Errorf("Expected sender DEVICE001, got %s", gossipMsg.SenderDeviceId)
	}

	ourState := gossipMsg.MeshView[0]
	if ourState.DeviceId != "DEVICE001" {
		t.Errorf("Expected DeviceId DEVICE001, got %s", ourState.DeviceId)
	}
	if ourState.FirstName != "TestDevice" {
		t.Errorf("Expected FirstName TestDevice, got %s", ourState.FirstName)
	}
	if ourState.ProfileVersion != 1 {
		t.Errorf("Expected ProfileVersion 1, got %d", ourState.ProfileVersion)
	}
}

func TestBuildGossipMessage_WithKnownDevices(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	// Add some known devices
	mv.UpdateDevice("DEVICE002", "photo2", "Alice", 1)
	mv.UpdateDevice("DEVICE003", "photo3", "Bob", 2)

	gossipMsg := mv.BuildGossipMessage("myphoto", 5)

	// Should contain ourselves + 2 known devices = 3 total
	if len(gossipMsg.MeshView) != 3 {
		t.Errorf("Expected 3 devices in gossip, got %d", len(gossipMsg.MeshView))
	}

	// Verify all devices are present
	deviceIDs := make(map[string]bool)
	for _, state := range gossipMsg.MeshView {
		deviceIDs[state.DeviceId] = true
	}

	if !deviceIDs["DEVICE001"] || !deviceIDs["DEVICE002"] || !deviceIDs["DEVICE003"] {
		t.Error("Not all devices present in gossip")
	}
}

func TestBuildGossipMessage_IncrementsGossipRound(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	msg1 := mv.BuildGossipMessage("photo", 1)
	msg2 := mv.BuildGossipMessage("photo", 1)

	if msg2.GossipRound != msg1.GossipRound+1 {
		t.Errorf("Expected gossip round to increment from %d to %d, got %d",
			msg1.GossipRound, msg1.GossipRound+1, msg2.GossipRound)
	}
}

// ============================================================================
// Gossip Merging Tests
// ============================================================================

func TestMergeGossip_NewDevice(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	// Create gossip from another device introducing DEVICE003
	gossipMsg := &pb.GossipMessage{
		SenderDeviceId: "DEVICE002",
		Timestamp:      time.Now().Unix(),
		MeshView: []*pb.DeviceState{
			{
				DeviceId:          "DEVICE002",
				PhotoHash:         []byte{0xab, 0xcd},
				FirstName:         "Alice",
				ProfileVersion:    1,
				LastSeenTimestamp: time.Now().Unix(),
			},
			{
				DeviceId:          "DEVICE003",
				PhotoHash:         []byte{0xef, 0x01},
				FirstName:         "Bob",
				ProfileVersion:    2,
				LastSeenTimestamp: time.Now().Unix(),
			},
		},
		GossipRound: 1,
	}

	newDevices := mv.MergeGossip(gossipMsg)

	// Should discover 2 new devices
	if len(newDevices) != 2 {
		t.Errorf("Expected 2 new devices, got %d", len(newDevices))
	}

	// Verify devices were added
	devices := mv.GetAllDevices()
	if len(devices) != 2 {
		t.Errorf("Expected 2 devices in mesh, got %d", len(devices))
	}

	// Verify DEVICE003 was added correctly
	var device3 *MeshDeviceState
	for _, d := range devices {
		if d.DeviceID == "DEVICE003" {
			device3 = d
			break
		}
	}

	if device3 == nil {
		t.Fatal("DEVICE003 not found in mesh")
	}

	if device3.FirstName != "Bob" {
		t.Errorf("Expected FirstName Bob, got %s", device3.FirstName)
	}
	if device3.ProfileVersion != 2 {
		t.Errorf("Expected ProfileVersion 2, got %d", device3.ProfileVersion)
	}
}

func TestMergeGossip_IgnoreSelf(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	// Gossip that includes ourselves
	gossipMsg := &pb.GossipMessage{
		SenderDeviceId: "DEVICE002",
		Timestamp:      time.Now().Unix(),
		MeshView: []*pb.DeviceState{
			{
				DeviceId:          "DEVICE001", // Ourselves
				FirstName:         "ShouldIgnore",
				ProfileVersion:    999,
				LastSeenTimestamp: time.Now().Unix(),
			},
			{
				DeviceId:          "DEVICE002",
				FirstName:         "Alice",
				ProfileVersion:    1,
				LastSeenTimestamp: time.Now().Unix(),
			},
		},
	}

	newDevices := mv.MergeGossip(gossipMsg)

	// Should only discover DEVICE002, not ourselves
	if len(newDevices) != 1 {
		t.Errorf("Expected 1 new device, got %d", len(newDevices))
	}

	if newDevices[0] != "DEVICE002" {
		t.Errorf("Expected to discover DEVICE002, got %s", newDevices[0])
	}
}

func TestMergeGossip_TimestampConflictResolution(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	now := time.Now()

	// Add device with older timestamp
	mv.UpdateDevice("DEVICE002", "oldphoto", "OldName", 1)
	devices := mv.GetAllDevices()
	devices[0].LastSeenTime = now.Add(-1 * time.Hour)
	mv.devices["DEVICE002"] = devices[0]

	// Merge gossip with newer timestamp
	gossipMsg := &pb.GossipMessage{
		SenderDeviceId: "DEVICE003",
		Timestamp:      now.Unix(),
		MeshView: []*pb.DeviceState{
			{
				DeviceId:          "DEVICE002",
				PhotoHash:         []byte{0xab, 0xcd},
				FirstName:         "NewName",
				ProfileVersion:    2,
				LastSeenTimestamp: now.Unix(),
			},
		},
	}

	mv.MergeGossip(gossipMsg)

	// Should update to newer version
	device := mv.devices["DEVICE002"]
	if device.FirstName != "NewName" {
		t.Errorf("Expected FirstName NewName, got %s", device.FirstName)
	}
	if device.ProfileVersion != 2 {
		t.Errorf("Expected ProfileVersion 2, got %d", device.ProfileVersion)
	}
}

func TestMergeGossip_IgnoreOlderTimestamp(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	now := time.Now()

	// Add device with newer timestamp
	mv.UpdateDevice("DEVICE002", "newphoto", "NewName", 2)

	// Try to merge gossip with older timestamp
	gossipMsg := &pb.GossipMessage{
		SenderDeviceId: "DEVICE003",
		Timestamp:      now.Add(-1 * time.Hour).Unix(),
		MeshView: []*pb.DeviceState{
			{
				DeviceId:          "DEVICE002",
				PhotoHash:         []byte{0xab, 0xcd},
				FirstName:         "OldName",
				ProfileVersion:    1,
				LastSeenTimestamp: now.Add(-1 * time.Hour).Unix(),
			},
		},
	}

	mv.MergeGossip(gossipMsg)

	// Should NOT update to older version
	device := mv.devices["DEVICE002"]
	if device.FirstName != "NewName" {
		t.Errorf("Expected FirstName NewName (should not update), got %s", device.FirstName)
	}
	if device.ProfileVersion != 2 {
		t.Errorf("Expected ProfileVersion 2 (should not update), got %d", device.ProfileVersion)
	}
}

// ============================================================================
// Profile Version Detection Tests
// ============================================================================

func TestGetDevicesWithOutdatedProfiles_EmptyCache(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	// Add device with version 1 to mesh
	mv.UpdateDevice("DEVICE002", "photo", "Alice", 1)

	// No cached profile exists, so should be outdated
	outdated := mv.GetDevicesWithOutdatedProfiles()

	if len(outdated) != 1 {
		t.Errorf("Expected 1 outdated profile, got %d", len(outdated))
	}

	if len(outdated) > 0 && outdated[0].DeviceID != "DEVICE002" {
		t.Errorf("Expected DEVICE002 to be outdated, got %s", outdated[0].DeviceID)
	}
}

func TestGetDevicesWithOutdatedProfiles_WithCachedProfile(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	// Add device with version 3 to mesh
	mv.UpdateDevice("DEVICE002", "photo", "Alice", 3)

	// Create cached profile with version 2
	cacheManager := NewDeviceCacheManager("uuid-001")
	metadata := &DeviceMetadata{
		FirstName:      "Alice",
		ProfileVersion: 2,
	}
	err := cacheManager.SaveDeviceMetadata("DEVICE002", metadata)
	if err != nil {
		t.Fatalf("Failed to save metadata: %v", err)
	}

	// Mesh has version 3, cache has version 2 -> outdated
	outdated := mv.GetDevicesWithOutdatedProfiles()

	if len(outdated) != 1 {
		t.Errorf("Expected 1 outdated profile, got %d", len(outdated))
	}

	if len(outdated) > 0 && outdated[0].ProfileVersion != 3 {
		t.Errorf("Expected ProfileVersion 3, got %d", outdated[0].ProfileVersion)
	}
}

func TestGetDevicesWithOutdatedProfiles_UpToDate(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	// Add device with version 2 to mesh
	mv.UpdateDevice("DEVICE002", "photo", "Alice", 2)

	// Create cached profile with version 2 (up to date)
	cacheManager := NewDeviceCacheManager("uuid-001")
	metadata := &DeviceMetadata{
		FirstName:      "Alice",
		ProfileVersion: 2,
	}
	err := cacheManager.SaveDeviceMetadata("DEVICE002", metadata)
	if err != nil {
		t.Fatalf("Failed to save metadata: %v", err)
	}

	// Cache is up to date, should not be in outdated list
	outdated := mv.GetDevicesWithOutdatedProfiles()

	if len(outdated) != 0 {
		t.Errorf("Expected 0 outdated profiles, got %d", len(outdated))
	}
}

func TestGetDevicesWithOutdatedProfiles_IgnoreSelf(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	// Add ourselves to mesh (shouldn't happen but let's handle it)
	mv.UpdateDevice("DEVICE001", "photo", "Me", 5)

	// Should not be in outdated list
	outdated := mv.GetDevicesWithOutdatedProfiles()

	if len(outdated) != 0 {
		t.Errorf("Expected 0 outdated profiles (should ignore self), got %d", len(outdated))
	}
}

// ============================================================================
// Connection State Tests
// ============================================================================

func TestMarkDeviceConnected_Disconnected(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	// Add device
	mv.UpdateDevice("DEVICE002", "photo", "Alice", 1)

	// Mark as connected
	mv.MarkDeviceConnected("DEVICE002")

	if !mv.IsDeviceConnected("DEVICE002") {
		t.Error("Device should be marked as connected")
	}

	// Mark as disconnected
	mv.MarkDeviceDisconnected("DEVICE002")

	if mv.IsDeviceConnected("DEVICE002") {
		t.Error("Device should be marked as disconnected")
	}
}

func TestMarkDeviceDisconnected_ResetsPhotoRequestFlag(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	// Add device and mark photo requested
	mv.UpdateDevice("DEVICE002", "photo", "Alice", 1)
	mv.MarkPhotoRequested("DEVICE002")

	device := mv.devices["DEVICE002"]
	if !device.PhotoRequestSent {
		t.Error("PhotoRequestSent should be true")
	}

	// Disconnect
	mv.MarkDeviceDisconnected("DEVICE002")

	// Photo request flag should be reset
	device = mv.devices["DEVICE002"]
	if device.PhotoRequestSent {
		t.Error("PhotoRequestSent should be reset on disconnect")
	}
}

func TestGetConnectedDevices(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	// Add 3 devices
	mv.UpdateDevice("DEVICE002", "photo2", "Alice", 1)
	mv.UpdateDevice("DEVICE003", "photo3", "Bob", 1)
	mv.UpdateDevice("DEVICE004", "photo4", "Charlie", 1)

	// Mark 2 as connected
	mv.MarkDeviceConnected("DEVICE002")
	mv.MarkDeviceConnected("DEVICE003")

	connected := mv.GetConnectedDevices()

	if len(connected) != 2 {
		t.Errorf("Expected 2 connected devices, got %d", len(connected))
	}

	// Verify correct devices
	connectedIDs := make(map[string]bool)
	for _, d := range connected {
		connectedIDs[d.DeviceID] = true
	}

	if !connectedIDs["DEVICE002"] || !connectedIDs["DEVICE003"] {
		t.Error("Wrong devices marked as connected")
	}
	if connectedIDs["DEVICE004"] {
		t.Error("DEVICE004 should not be marked as connected")
	}
}

// ============================================================================
// Neighbor Data Tracking Tests (Multi-hop)
// ============================================================================

func TestUpdateNeighborData_NewNeighbor(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	// Create gossip from neighbor
	gossipMsg := &pb.GossipMessage{
		SenderDeviceId: "DEVICE002",
		MeshView: []*pb.DeviceState{
			{
				DeviceId:       "DEVICE002",
				PhotoHash:      []byte{0xab, 0xcd},
				FirstName:      "Alice",
				ProfileVersion: 1,
			},
			{
				DeviceId:       "DEVICE003",
				PhotoHash:      []byte{0xef, 0x01},
				FirstName:      "Bob",
				ProfileVersion: 2,
			},
		},
	}

	mv.UpdateNeighborData("DEVICE002", "uuid-002", gossipMsg)

	// Verify neighbor data was recorded
	neighbor := mv.neighbors["uuid-002"]
	if neighbor == nil {
		t.Fatal("Neighbor data not recorded")
	}

	if neighbor.DeviceID != "DEVICE002" {
		t.Errorf("Expected DeviceID DEVICE002, got %s", neighbor.DeviceID)
	}

	// Verify neighbor knows about DEVICE003's photo
	if !neighbor.PhotoHashes["ef01"] {
		t.Error("Neighbor should know about DEVICE003's photo")
	}

	// Verify neighbor has DEVICE003's profile version
	if neighbor.ProfileVersions["DEVICE003"] != 2 {
		t.Errorf("Expected profile version 2 for DEVICE003, got %d", neighbor.ProfileVersions["DEVICE003"])
	}
}

func TestFindNeighborsWithProfile(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	// Add 2 neighbors
	mv.UpdateDevice("DEVICE002", "", "Alice", 1)
	mv.UpdateDevice("DEVICE003", "", "Bob", 1)
	mv.MarkDeviceConnected("DEVICE002")
	mv.MarkDeviceConnected("DEVICE003")

	// Neighbor 1 has DEVICE004's profile v3
	gossipMsg1 := &pb.GossipMessage{
		SenderDeviceId: "DEVICE002",
		MeshView: []*pb.DeviceState{
			{
				DeviceId:       "DEVICE004",
				FirstName:      "Charlie",
				ProfileVersion: 3,
			},
		},
	}
	mv.UpdateNeighborData("DEVICE002", "uuid-002", gossipMsg1)

	// Neighbor 2 has DEVICE004's profile v2 (older)
	gossipMsg2 := &pb.GossipMessage{
		SenderDeviceId: "DEVICE003",
		MeshView: []*pb.DeviceState{
			{
				DeviceId:       "DEVICE004",
				FirstName:      "Charlie",
				ProfileVersion: 2,
			},
		},
	}
	mv.UpdateNeighborData("DEVICE003", "uuid-003", gossipMsg2)

	// Find neighbors with DEVICE004's profile v3 or newer
	neighbors := mv.FindNeighborsWithProfile("DEVICE004", 3)

	// Should only return neighbor 1
	if len(neighbors) != 1 {
		t.Errorf("Expected 1 neighbor with profile v3+, got %d", len(neighbors))
	}

	if len(neighbors) > 0 && neighbors[0] != "uuid-002" {
		t.Errorf("Expected uuid-002, got %s", neighbors[0])
	}
}

func TestFindNeighborsWithProfile_OnlyConnected(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	// Add neighbor but don't mark as connected
	mv.UpdateDevice("DEVICE002", "", "Alice", 1)

	gossipMsg := &pb.GossipMessage{
		SenderDeviceId: "DEVICE002",
		MeshView: []*pb.DeviceState{
			{
				DeviceId:       "DEVICE003",
				ProfileVersion: 1,
			},
		},
	}
	mv.UpdateNeighborData("DEVICE002", "uuid-002", gossipMsg)

	// Find neighbors - should be empty because DEVICE002 is not connected
	neighbors := mv.FindNeighborsWithProfile("DEVICE003", 1)

	if len(neighbors) != 0 {
		t.Errorf("Expected 0 neighbors (not connected), got %d", len(neighbors))
	}

	// Now mark as connected
	mv.MarkDeviceConnected("DEVICE002")

	neighbors = mv.FindNeighborsWithProfile("DEVICE003", 1)

	if len(neighbors) != 1 {
		t.Errorf("Expected 1 neighbor (now connected), got %d", len(neighbors))
	}
}

// ============================================================================
// Persistence Tests
// ============================================================================

func TestPersistence_SaveAndLoad(t *testing.T) {
	tempDir := util.SetRandom()

	// Create mesh view and add devices
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)
	mv.UpdateDevice("DEVICE002", "photo2", "Alice", 1)
	mv.UpdateDevice("DEVICE003", "photo3", "Bob", 2)

	// Save to disk
	err := mv.SaveToDisk()
	if err != nil {
		t.Fatalf("Failed to save to disk: %v", err)
	}

	// Create new mesh view (will load from disk)
	mv2 := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	// Verify devices were loaded
	devices := mv2.GetAllDevices()
	if len(devices) != 2 {
		t.Errorf("Expected 2 devices after load, got %d", len(devices))
	}

	// Verify device details
	deviceMap := make(map[string]*MeshDeviceState)
	for _, d := range devices {
		deviceMap[d.DeviceID] = d
	}

	if device2, ok := deviceMap["DEVICE002"]; ok {
		if device2.FirstName != "Alice" || device2.ProfileVersion != 1 {
			t.Error("DEVICE002 loaded incorrectly")
		}
	} else {
		t.Error("DEVICE002 not found after load")
	}

	if device3, ok := deviceMap["DEVICE003"]; ok {
		if device3.FirstName != "Bob" || device3.ProfileVersion != 2 {
			t.Error("DEVICE003 loaded incorrectly")
		}
	} else {
		t.Error("DEVICE003 not found after load")
	}
}

func TestPersistence_FileLocation(t *testing.T) {

	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)
	mv.UpdateDevice("DEVICE002", "photo", "Alice", 1)

	err := mv.SaveToDisk()
	if err != nil {
		t.Fatalf("Failed to save: %v", err)
	}

	// Verify file exists at expected location
	expectedPath := filepath.Join(tempDir, "cache", "mesh_view.json")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Errorf("Expected file at %s, but it doesn't exist", expectedPath)
	}
}

// ============================================================================
// Edge Case Tests
// ============================================================================

func TestEdgeCase_ZeroProfileVersion(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	// Add device with version 0 (no profile set yet)
	mv.UpdateDevice("DEVICE002", "photo", "Alice", 0)

	// Should not be in outdated list (no profile to request)
	outdated := mv.GetDevicesWithOutdatedProfiles()

	// Device with version 0 should not trigger profile requests
	if len(outdated) != 0 {
		t.Errorf("Expected 0 outdated profiles for version 0, got %d", len(outdated))
	}
}

func TestEdgeCase_EmptyPhotoHash(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	// Add device with empty photo hash
	mv.UpdateDevice("DEVICE002", "", "Alice", 1)

	devices := mv.GetAllDevices()
	if len(devices) != 1 {
		t.Fatalf("Expected 1 device, got %d", len(devices))
	}

	if devices[0].PhotoHash != "" {
		t.Errorf("Expected empty PhotoHash, got %s", devices[0].PhotoHash)
	}
}

func TestEdgeCase_UpdateWithEmptyPhotoHash_PreservesExisting(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	// Add device with photo hash
	mv.UpdateDevice("DEVICE002", "photo123", "Alice", 1)

	// Update with empty photo hash (should preserve existing)
	mv.UpdateDevice("DEVICE002", "", "Alice", 2)

	devices := mv.GetAllDevices()
	if devices[0].PhotoHash != "photo123" {
		t.Errorf("Expected PhotoHash to be preserved as photo123, got %s", devices[0].PhotoHash)
	}
}

func TestEdgeCase_MultipleRapidUpdates(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	// Simulate rapid profile updates (like real gossip scenario)
	for i := 1; i <= 10; i++ {
		mv.UpdateDevice("DEVICE002", "photo", "Alice", int32(i))
	}

	devices := mv.GetAllDevices()
	if len(devices) != 1 {
		t.Errorf("Expected 1 device after rapid updates, got %d", len(devices))
	}

	if devices[0].ProfileVersion != 10 {
		t.Errorf("Expected final ProfileVersion 10, got %d", devices[0].ProfileVersion)
	}
}

func TestGossipInterval_Timing(t *testing.T) {
	mv := setupTestMeshView(t, "DEVICE001", "uuid-001")

	// Initially should be ready to gossip
	if !mv.ShouldGossip() {
		t.Error("Should be ready to gossip initially")
	}

	// Build gossip (updates lastGossipTime)
	mv.BuildGossipMessage("photo", 1)

	// Should NOT be ready to gossip immediately after
	if mv.ShouldGossip() {
		t.Error("Should not be ready to gossip immediately after building")
	}

	// Sleep for gossip interval + a bit
	time.Sleep(5*time.Second + 100*time.Millisecond)

	// Now should be ready again
	if !mv.ShouldGossip() {
		t.Error("Should be ready to gossip after interval")
	}
}
