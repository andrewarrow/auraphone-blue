package phone

import (
	"fmt"
	"testing"
	"time"

	pb "github.com/user/auraphone-blue/proto"
	"github.com/user/auraphone-blue/util"
)

// ============================================================================
// Advanced Gossip Logic Tests
// ============================================================================

func TestGossipMerge_ProfileVersionComparison_OlderVsNewer(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	now := time.Now()

	// Device starts at version 5
	mv.UpdateDevice("DEVICE002", "photo1", "Alice", 5)
	device := mv.devices["DEVICE002"]
	device.LastSeenTime = now
	mv.devices["DEVICE002"] = device

	// Receive gossip with older version (3) but SAME timestamp
	gossipMsg := &pb.GossipMessage{
		SenderDeviceId: "DEVICE003",
		Timestamp:      now.Unix(),
		MeshView: []*pb.DeviceState{
			{
				DeviceId:          "DEVICE002",
				PhotoHash:         []byte{0xab, 0xcd},
				FirstName:         "OldAlice",
				ProfileVersion:    3,
				LastSeenTimestamp: now.Unix(),
			},
		},
	}

	mv.MergeGossip(gossipMsg)

	// Should NOT downgrade to older version even with same timestamp
	device = mv.devices["DEVICE002"]
	if device.ProfileVersion != 5 {
		t.Errorf("Expected ProfileVersion 5 to be preserved, got %d", device.ProfileVersion)
	}
	if device.FirstName != "Alice" {
		t.Errorf("Expected FirstName Alice to be preserved, got %s", device.FirstName)
	}
}

func TestGossipMerge_ProfileVersionComparison_NewerVersionWins(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	now := time.Now()

	// Device starts at version 3
	mv.UpdateDevice("DEVICE002", "photo1", "Alice", 3)
	device := mv.devices["DEVICE002"]
	device.LastSeenTime = now.Add(-1 * time.Second)
	mv.devices["DEVICE002"] = device

	// Receive gossip with newer version (5) and newer timestamp
	gossipMsg := &pb.GossipMessage{
		SenderDeviceId: "DEVICE003",
		Timestamp:      now.Unix(),
		MeshView: []*pb.DeviceState{
			{
				DeviceId:          "DEVICE002",
				PhotoHash:         []byte{0xef, 0x01},
				FirstName:         "NewAlice",
				ProfileVersion:    5,
				LastSeenTimestamp: now.Unix(),
			},
		},
	}

	mv.MergeGossip(gossipMsg)

	// Should upgrade to newer version
	device = mv.devices["DEVICE002"]
	if device.ProfileVersion != 5 {
		t.Errorf("Expected ProfileVersion 5, got %d", device.ProfileVersion)
	}
	if device.FirstName != "NewAlice" {
		t.Errorf("Expected FirstName NewAlice, got %s", device.FirstName)
	}
}

func TestGossipMerge_EmptyPartialProfile(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	// Receive gossip with empty first name (partial profile)
	gossipMsg := &pb.GossipMessage{
		SenderDeviceId: "DEVICE002",
		Timestamp:      time.Now().Unix(),
		MeshView: []*pb.DeviceState{
			{
				DeviceId:          "DEVICE003",
				PhotoHash:         []byte{0xab, 0xcd},
				FirstName:         "", // Empty name
				ProfileVersion:    1,
				LastSeenTimestamp: time.Now().Unix(),
			},
		},
	}

	newDevices := mv.MergeGossip(gossipMsg)

	// Should still add device even with empty name
	if len(newDevices) != 1 {
		t.Errorf("Expected 1 new device, got %d", len(newDevices))
	}

	device := mv.devices["DEVICE003"]
	if device == nil {
		t.Fatal("Device should be added even with empty name")
	}
	if device.FirstName != "" {
		t.Errorf("Expected empty FirstName, got %s", device.FirstName)
	}
	if device.ProfileVersion != 1 {
		t.Errorf("Expected ProfileVersion 1, got %d", device.ProfileVersion)
	}
}

func TestGossipMerge_PartialProfileUpdate(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	now := time.Now()

	// Device starts with empty name
	mv.UpdateDevice("DEVICE002", "photo1", "", 1)
	device := mv.devices["DEVICE002"]
	device.LastSeenTime = now.Add(-1 * time.Second)
	mv.devices["DEVICE002"] = device

	// Receive gossip with name filled in
	gossipMsg := &pb.GossipMessage{
		SenderDeviceId: "DEVICE003",
		Timestamp:      now.Unix(),
		MeshView: []*pb.DeviceState{
			{
				DeviceId:          "DEVICE002",
				PhotoHash:         []byte{0xab, 0xcd},
				FirstName:         "Alice", // Name now filled
				ProfileVersion:    2,
				LastSeenTimestamp: now.Unix(),
			},
		},
	}

	mv.MergeGossip(gossipMsg)

	// Should update to complete profile
	device = mv.devices["DEVICE002"]
	if device.FirstName != "Alice" {
		t.Errorf("Expected FirstName Alice, got %s", device.FirstName)
	}
	if device.ProfileVersion != 2 {
		t.Errorf("Expected ProfileVersion 2, got %d", device.ProfileVersion)
	}
}

func TestGossipMerge_EmptyPhotoHash(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	// Receive gossip with empty photo hash
	gossipMsg := &pb.GossipMessage{
		SenderDeviceId: "DEVICE002",
		Timestamp:      time.Now().Unix(),
		MeshView: []*pb.DeviceState{
			{
				DeviceId:          "DEVICE003",
				PhotoHash:         []byte{}, // Empty photo hash
				FirstName:         "Bob",
				ProfileVersion:    1,
				LastSeenTimestamp: time.Now().Unix(),
			},
		},
	}

	mv.MergeGossip(gossipMsg)

	device := mv.devices["DEVICE003"]
	if device == nil {
		t.Fatal("Device should be added even with empty photo hash")
	}
	if device.PhotoHash != "" {
		t.Errorf("Expected empty PhotoHash, got %s", device.PhotoHash)
	}
}

func TestGossipMerge_ZeroProfileVersion(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	// Receive gossip with zero profile version (profile not set)
	gossipMsg := &pb.GossipMessage{
		SenderDeviceId: "DEVICE002",
		Timestamp:      time.Now().Unix(),
		MeshView: []*pb.DeviceState{
			{
				DeviceId:          "DEVICE003",
				PhotoHash:         []byte{0xab, 0xcd},
				FirstName:         "Charlie",
				ProfileVersion:    0, // No profile set
				LastSeenTimestamp: time.Now().Unix(),
			},
		},
	}

	mv.MergeGossip(gossipMsg)

	device := mv.devices["DEVICE003"]
	if device == nil {
		t.Fatal("Device should be added")
	}
	if device.ProfileVersion != 0 {
		t.Errorf("Expected ProfileVersion 0, got %d", device.ProfileVersion)
	}
}

// ============================================================================
// Profile Version System Tests
// ============================================================================

func TestProfileVersion_MultipleIncrements(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	// Simulate profile updates through gossip
	baseTime := time.Now()

	for i := 1; i <= 10; i++ {
		gossipMsg := &pb.GossipMessage{
			SenderDeviceId: "DEVICE002",
			Timestamp:      baseTime.Add(time.Duration(i) * time.Second).Unix(),
			MeshView: []*pb.DeviceState{
				{
					DeviceId:          "DEVICE002",
					PhotoHash:         []byte{0xab, 0xcd},
					FirstName:         "Alice",
					ProfileVersion:    int32(i),
					LastSeenTimestamp: baseTime.Add(time.Duration(i) * time.Second).Unix(),
				},
			},
		}
		mv.MergeGossip(gossipMsg)
	}

	device := mv.devices["DEVICE002"]
	if device.ProfileVersion != 10 {
		t.Errorf("Expected ProfileVersion 10 after increments, got %d", device.ProfileVersion)
	}
}

func TestProfileVersion_OutOfOrderUpdates(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	baseTime := time.Now()

	// Receive version 5 first (newest)
	gossipMsg5 := &pb.GossipMessage{
		SenderDeviceId: "DEVICE002",
		Timestamp:      baseTime.Add(5 * time.Second).Unix(),
		MeshView: []*pb.DeviceState{
			{
				DeviceId:          "DEVICE003",
				FirstName:         "Version5",
				ProfileVersion:    5,
				LastSeenTimestamp: baseTime.Add(5 * time.Second).Unix(),
			},
		},
	}
	mv.MergeGossip(gossipMsg5)

	// Now receive version 3 (older)
	gossipMsg3 := &pb.GossipMessage{
		SenderDeviceId: "DEVICE002",
		Timestamp:      baseTime.Add(3 * time.Second).Unix(),
		MeshView: []*pb.DeviceState{
			{
				DeviceId:          "DEVICE003",
				FirstName:         "Version3",
				ProfileVersion:    3,
				LastSeenTimestamp: baseTime.Add(3 * time.Second).Unix(),
			},
		},
	}
	mv.MergeGossip(gossipMsg3)

	// Should keep version 5 (newer timestamp)
	device := mv.devices["DEVICE003"]
	if device.ProfileVersion != 5 {
		t.Errorf("Expected ProfileVersion 5 (newer), got %d", device.ProfileVersion)
	}
	if device.FirstName != "Version5" {
		t.Errorf("Expected FirstName Version5, got %s", device.FirstName)
	}
}

func TestMultiHopRouting_ProfileRelay(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	// Add two neighbors
	mv.UpdateDevice("DEVICE002", "", "Neighbor1", 1)
	mv.UpdateDevice("DEVICE003", "", "Neighbor2", 1)
	mv.MarkDeviceConnected("DEVICE002")
	mv.MarkDeviceConnected("DEVICE003")

	// Neighbor 1 has DEVICE004's profile v7
	gossipMsg1 := &pb.GossipMessage{
		SenderDeviceId: "DEVICE002",
		MeshView: []*pb.DeviceState{
			{
				DeviceId:       "DEVICE004",
				FirstName:      "Target",
				ProfileVersion: 7,
			},
		},
	}
	mv.UpdateNeighborData("DEVICE002", "uuid-002", gossipMsg1)

	// Neighbor 2 has DEVICE004's profile v9 (newer)
	gossipMsg2 := &pb.GossipMessage{
		SenderDeviceId: "DEVICE003",
		MeshView: []*pb.DeviceState{
			{
				DeviceId:       "DEVICE004",
				FirstName:      "Target",
				ProfileVersion: 9,
			},
		},
	}
	mv.UpdateNeighborData("DEVICE003", "uuid-003", gossipMsg2)

	// Find neighbors with at least version 8
	neighbors := mv.FindNeighborsWithProfile("DEVICE004", 8)

	if len(neighbors) != 1 {
		t.Errorf("Expected 1 neighbor with v8+, got %d", len(neighbors))
	}

	if len(neighbors) > 0 && neighbors[0] != "uuid-003" {
		t.Errorf("Expected uuid-003 (v9), got %s", neighbors[0])
	}
}

func TestMultiHopRouting_ProfileRelayIntermediateNodeMissingData(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	// Add three neighbors
	mv.UpdateDevice("DEVICE002", "", "Neighbor1", 1)
	mv.UpdateDevice("DEVICE003", "", "Neighbor2", 1)
	mv.UpdateDevice("DEVICE004", "", "Neighbor3", 1)
	mv.MarkDeviceConnected("DEVICE002")
	mv.MarkDeviceConnected("DEVICE003")
	mv.MarkDeviceConnected("DEVICE004")

	// Neighbor 1 has DEVICE005's profile v3
	gossipMsg1 := &pb.GossipMessage{
		SenderDeviceId: "DEVICE002",
		MeshView: []*pb.DeviceState{
			{
				DeviceId:       "DEVICE005",
				FirstName:      "Target",
				ProfileVersion: 3,
			},
		},
	}
	mv.UpdateNeighborData("DEVICE002", "uuid-002", gossipMsg1)

	// Neighbor 2 has NO data about DEVICE005

	// Neighbor 3 has DEVICE005's profile v5
	gossipMsg3 := &pb.GossipMessage{
		SenderDeviceId: "DEVICE004",
		MeshView: []*pb.DeviceState{
			{
				DeviceId:       "DEVICE005",
				FirstName:      "Target",
				ProfileVersion: 5,
			},
		},
	}
	mv.UpdateNeighborData("DEVICE004", "uuid-004", gossipMsg3)

	// Find neighbors with at least version 5
	neighbors := mv.FindNeighborsWithProfile("DEVICE005", 5)

	// Should only return neighbor 3
	if len(neighbors) != 1 {
		t.Errorf("Expected 1 neighbor with v5+, got %d", len(neighbors))
	}

	if len(neighbors) > 0 && neighbors[0] != "uuid-004" {
		t.Errorf("Expected uuid-004, got %s", neighbors[0])
	}
}

func TestOutdatedProfiles_WithMultipleVersions(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	cacheManager := NewDeviceCacheManager("uuid-001")

	// Add devices with various profile versions
	mv.UpdateDevice("DEVICE002", "photo2", "Alice", 5)
	mv.UpdateDevice("DEVICE003", "photo3", "Bob", 3)
	mv.UpdateDevice("DEVICE004", "photo4", "Charlie", 7)

	// Cache has older versions
	cacheManager.SaveDeviceMetadata("DEVICE002", &DeviceMetadata{FirstName: "Alice", ProfileVersion: 3})
	cacheManager.SaveDeviceMetadata("DEVICE003", &DeviceMetadata{FirstName: "Bob", ProfileVersion: 3})
	cacheManager.SaveDeviceMetadata("DEVICE004", &DeviceMetadata{FirstName: "Charlie", ProfileVersion: 7})

	// Get outdated profiles
	outdated := mv.GetDevicesWithOutdatedProfiles()

	// DEVICE002 is outdated (mesh v5 > cache v3)
	// DEVICE003 is up-to-date (mesh v3 == cache v3)
	// DEVICE004 is up-to-date (mesh v7 == cache v7)
	if len(outdated) != 1 {
		t.Errorf("Expected 1 outdated profile, got %d", len(outdated))
	}

	if len(outdated) > 0 && outdated[0].DeviceID != "DEVICE002" {
		t.Errorf("Expected DEVICE002 to be outdated, got %s", outdated[0].DeviceID)
	}
}

// ============================================================================
// Edge Case Tests
// ============================================================================

func TestEdgeCase_ProfileUpdateDuringDisconnection(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	baseTime := time.Now().Add(-1 * time.Hour)

	// Device connected with version 1 (older timestamp)
	mv.UpdateDevice("DEVICE002", "photo1", "Alice", 1)
	device := mv.devices["DEVICE002"]
	device.LastSeenTime = baseTime
	mv.devices["DEVICE002"] = device
	mv.MarkDeviceConnected("DEVICE002")

	// Disconnect
	mv.MarkDeviceDisconnected("DEVICE002")

	// While disconnected, learn about version 5 via gossip (newer timestamp)
	now := time.Now()
	gossipMsg := &pb.GossipMessage{
		SenderDeviceId: "DEVICE003",
		Timestamp:      now.Unix(),
		MeshView: []*pb.DeviceState{
			{
				DeviceId:          "DEVICE002",
				PhotoHash:         []byte{0xef, 0x01},
				FirstName:         "Alice",
				ProfileVersion:    5,
				LastSeenTimestamp: now.Unix(),
			},
		},
	}
	mv.MergeGossip(gossipMsg)

	// Should update profile even while disconnected
	device = mv.devices["DEVICE002"]
	if device.ProfileVersion != 5 {
		t.Errorf("Expected ProfileVersion 5 (updated while disconnected), got %d", device.ProfileVersion)
	}

	// Should NOT be in connected devices
	if mv.IsDeviceConnected("DEVICE002") {
		t.Error("Device should not be marked as connected")
	}
}

func TestEdgeCase_RapidGossipMerging(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	baseTime := time.Now()

	// Simulate rapid gossip messages from multiple peers
	// Use 1 second intervals to ensure timestamp differences are detectable
	for i := 0; i < 20; i++ {
		gossipMsg := &pb.GossipMessage{
			SenderDeviceId: "DEVICE002",
			Timestamp:      baseTime.Add(time.Duration(i) * time.Second).Unix(),
			MeshView: []*pb.DeviceState{
				{
					DeviceId:          "DEVICE003",
					PhotoHash:         []byte{0xab, byte(i)},
					FirstName:         "Alice",
					ProfileVersion:    int32(i + 1),
					LastSeenTimestamp: baseTime.Add(time.Duration(i) * time.Second).Unix(),
				},
			},
		}
		mv.MergeGossip(gossipMsg)
	}

	// Should have latest version
	device := mv.devices["DEVICE003"]
	if device.ProfileVersion != 20 {
		t.Errorf("Expected ProfileVersion 20 after rapid merges, got %d", device.ProfileVersion)
	}
}

func TestEdgeCase_LargeMeshWithManyVersions(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	// Add 50 devices with various profile versions
	for i := 1; i <= 50; i++ {
		deviceID := fmt.Sprintf("DEVICE%03d", i)
		photoHash := fmt.Sprintf("photo%d", i)
		name := fmt.Sprintf("Device%d", i)
		version := int32(i % 10) // Versions 0-9
		mv.UpdateDevice(deviceID, photoHash, name, version)
	}

	devices := mv.GetAllDevices()
	if len(devices) != 50 {
		t.Errorf("Expected 50 devices, got %d", len(devices))
	}

	// Verify specific devices
	device25 := mv.devices["DEVICE025"]
	if device25.ProfileVersion != 5 {
		t.Errorf("Expected DEVICE025 to have version 5, got %d", device25.ProfileVersion)
	}
}

func TestEdgeCase_NeighborDataStaleness(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	// Add neighbor
	mv.UpdateDevice("DEVICE002", "", "Neighbor", 1)
	mv.MarkDeviceConnected("DEVICE002")

	// Update neighbor data
	gossipMsg := &pb.GossipMessage{
		SenderDeviceId: "DEVICE002",
		MeshView: []*pb.DeviceState{
			{
				DeviceId:       "DEVICE003",
				FirstName:      "Target",
				ProfileVersion: 5,
			},
		},
	}
	mv.UpdateNeighborData("DEVICE002", "uuid-002", gossipMsg)

	// Verify neighbor has the profile
	neighbors := mv.FindNeighborsWithProfile("DEVICE003", 5)
	if len(neighbors) != 1 {
		t.Errorf("Expected 1 neighbor, got %d", len(neighbors))
	}

	// Disconnect neighbor
	mv.MarkDeviceDisconnected("DEVICE002")

	// Now neighbor should not be returned (not connected)
	neighbors = mv.FindNeighborsWithProfile("DEVICE003", 5)
	if len(neighbors) != 0 {
		t.Errorf("Expected 0 neighbors (disconnected), got %d", len(neighbors))
	}
}

func TestEdgeCase_PhotoHashConflict(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	// Two devices with same photo hash (unlikely but possible)
	mv.UpdateDevice("DEVICE002", "samephoto", "Alice", 1)
	mv.UpdateDevice("DEVICE003", "samephoto", "Bob", 1)

	devices := mv.GetAllDevices()
	if len(devices) != 2 {
		t.Errorf("Expected 2 devices, got %d", len(devices))
	}

	// Both should have same photo hash but different IDs
	deviceMap := make(map[string]*MeshDeviceState)
	for _, d := range devices {
		deviceMap[d.DeviceID] = d
	}

	if deviceMap["DEVICE002"].PhotoHash != "samephoto" {
		t.Error("DEVICE002 should have samephoto hash")
	}
	if deviceMap["DEVICE003"].PhotoHash != "samephoto" {
		t.Error("DEVICE003 should have samephoto hash")
	}
}

func TestEdgeCase_EmptyFirstName(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	// Device with empty first name
	mv.UpdateDevice("DEVICE002", "photo", "", 1)

	device := mv.devices["DEVICE002"]
	if device.FirstName != "" {
		t.Errorf("Expected empty FirstName, got '%s'", device.FirstName)
	}
}

func TestEdgeCase_NeighborWithMultipleProfileVersionUpdates(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	// Add neighbor
	mv.UpdateDevice("DEVICE002", "", "Neighbor", 1)
	mv.MarkDeviceConnected("DEVICE002")

	// Neighbor initially has DEVICE003's profile v1
	gossipMsg1 := &pb.GossipMessage{
		SenderDeviceId: "DEVICE002",
		MeshView: []*pb.DeviceState{
			{
				DeviceId:       "DEVICE003",
				FirstName:      "Target",
				ProfileVersion: 1,
			},
		},
	}
	mv.UpdateNeighborData("DEVICE002", "uuid-002", gossipMsg1)

	// Later, neighbor updates to v5
	gossipMsg2 := &pb.GossipMessage{
		SenderDeviceId: "DEVICE002",
		MeshView: []*pb.DeviceState{
			{
				DeviceId:       "DEVICE003",
				FirstName:      "Target",
				ProfileVersion: 5,
			},
		},
	}
	mv.UpdateNeighborData("DEVICE002", "uuid-002", gossipMsg2)

	// Neighbor should have latest version
	neighbor := mv.neighbors["uuid-002"]
	if neighbor.ProfileVersions["DEVICE003"] != 5 {
		t.Errorf("Expected neighbor to have version 5, got %d", neighbor.ProfileVersions["DEVICE003"])
	}
}

func TestEdgeCase_GossipFromUnknownPeer(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	// Receive gossip from a peer we've never seen
	gossipMsg := &pb.GossipMessage{
		SenderDeviceId: "UNKNOWN_DEVICE",
		Timestamp:      time.Now().Unix(),
		MeshView: []*pb.DeviceState{
			{
				DeviceId:          "DEVICE002",
				PhotoHash:         []byte{0xab, 0xcd},
				FirstName:         "Alice",
				ProfileVersion:    1,
				LastSeenTimestamp: time.Now().Unix(),
			},
		},
	}

	newDevices := mv.MergeGossip(gossipMsg)

	// Should learn about DEVICE002 even though sender is unknown
	if len(newDevices) != 1 {
		t.Errorf("Expected 1 new device, got %d", len(newDevices))
	}

	if newDevices[0] != "DEVICE002" {
		t.Errorf("Expected DEVICE002, got %s", newDevices[0])
	}
}

func TestEdgeCase_MultipleNeighborsSameProfile(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	// Add three neighbors, all with same profile version
	for i := 2; i <= 4; i++ {
		deviceID := fmt.Sprintf("DEVICE%03d", i)
		hardwareUUID := fmt.Sprintf("uuid-%03d", i)

		mv.UpdateDevice(deviceID, "", "Neighbor", 1)
		mv.MarkDeviceConnected(deviceID)

		gossipMsg := &pb.GossipMessage{
			SenderDeviceId: deviceID,
			MeshView: []*pb.DeviceState{
				{
					DeviceId:       "DEVICE999",
					FirstName:      "Target",
					ProfileVersion: 10,
				},
			},
		}
		mv.UpdateNeighborData(deviceID, hardwareUUID, gossipMsg)
	}

	// All three neighbors should have the profile
	neighbors := mv.FindNeighborsWithProfile("DEVICE999", 10)
	if len(neighbors) != 3 {
		t.Errorf("Expected 3 neighbors with profile v10, got %d", len(neighbors))
	}
}

func TestEdgeCase_PhotoHashUpdateResetsPending(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	// Add device and mark photo requested
	mv.UpdateDevice("DEVICE002", "photo1", "Alice", 1)
	mv.MarkPhotoRequested("DEVICE002")

	device := mv.devices["DEVICE002"]
	if !device.PhotoRequestSent {
		t.Fatal("PhotoRequestSent should be true")
	}

	// Update with different photo hash
	mv.UpdateDevice("DEVICE002", "photo2", "Alice", 2)

	device = mv.devices["DEVICE002"]
	if device.PhotoRequestSent {
		t.Error("PhotoRequestSent should be reset when photo hash changes")
	}
}

// ============================================================================
// Concurrent Access Simulation Tests
// ============================================================================

func TestConcurrent_MultipleGossipMerges(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	done := make(chan bool)
	baseTime := time.Now()

	// Simulate 10 concurrent gossip merges
	for i := 0; i < 10; i++ {
		go func(idx int) {
			gossipMsg := &pb.GossipMessage{
				SenderDeviceId: fmt.Sprintf("DEVICE%03d", idx+2),
				Timestamp:      baseTime.Add(time.Duration(idx) * time.Second).Unix(),
				MeshView: []*pb.DeviceState{
					{
						DeviceId:          fmt.Sprintf("DEVICE%03d", idx+2),
						FirstName:         fmt.Sprintf("Device%d", idx+2),
						ProfileVersion:    int32(idx + 1),
						LastSeenTimestamp: baseTime.Add(time.Duration(idx) * time.Second).Unix(),
					},
				},
			}
			mv.MergeGossip(gossipMsg)
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should have 10 devices
	devices := mv.GetAllDevices()
	if len(devices) != 10 {
		t.Errorf("Expected 10 devices after concurrent merges, got %d", len(devices))
	}
}

func TestConcurrent_ReadWhileUpdating(t *testing.T) {
	tempDir := util.SetRandom()
	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	// Pre-populate with some devices
	for i := 1; i <= 5; i++ {
		mv.UpdateDevice(fmt.Sprintf("DEVICE%03d", i), "photo", "Name", 1)
	}

	done := make(chan bool)

	// Writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			mv.UpdateDevice("DEVICE002", "photo", "Alice", int32(i))
		}
		done <- true
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < 100; i++ {
			_ = mv.GetAllDevices()
		}
		done <- true
	}()

	// Wait for both to complete
	<-done
	<-done

	// No panic = success
}

// ============================================================================
// Persistence Edge Cases
// ============================================================================

func TestPersistence_EmptyMesh(t *testing.T) {
	tempDir := util.SetRandom()

	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	err := mv.SaveToDisk()
	if err != nil {
		t.Fatalf("Failed to save empty mesh: %v", err)
	}

	// Load into new instance
	mv2 := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	devices := mv2.GetAllDevices()
	if len(devices) != 0 {
		t.Errorf("Expected 0 devices after loading empty mesh, got %d", len(devices))
	}
}

func TestPersistence_LargeMesh(t *testing.T) {
	tempDir := util.SetRandom()

	mv := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	// Add 100 devices
	for i := 1; i <= 100; i++ {
		mv.UpdateDevice(
			fmt.Sprintf("DEVICE%03d", i),
			fmt.Sprintf("photo%d", i),
			fmt.Sprintf("Name%d", i),
			int32(i),
		)
	}

	err := mv.SaveToDisk()
	if err != nil {
		t.Fatalf("Failed to save large mesh: %v", err)
	}

	// Load into new instance
	mv2 := NewMeshView("DEVICE001", "uuid-001", tempDir, nil)

	devices := mv2.GetAllDevices()
	if len(devices) != 100 {
		t.Errorf("Expected 100 devices after loading, got %d", len(devices))
	}
}
