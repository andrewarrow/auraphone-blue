package phone

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/user/auraphone-blue/proto"
)

// TestMeshView_DuplicatePhotoDiscovery tests that the same photo is not discovered multiple times
func TestMeshView_DuplicatePhotoDiscovery(t *testing.T) {
	// Setup: Create a temporary data directory
	tmpDir := t.TempDir()
	deviceDir := filepath.Join(tmpDir, "test-device")
	if err := os.MkdirAll(deviceDir, 0755); err != nil {
		t.Fatalf("Failed to create device directory: %v", err)
	}

	// Create cache manager
	cacheManager := NewDeviceCacheManager(deviceDir)

	// Create mesh view
	ourDeviceID := "AAAAA"
	ourHardwareUUID := "11111111-1111-1111-1111-111111111111"
	meshView := NewMeshView(ourDeviceID, ourHardwareUUID, deviceDir, cacheManager)

	// Device A's photo hash
	deviceAID := "BBBBB"
	photoHashBytes := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	photoHashHex := hex.EncodeToString(photoHashBytes)

	// Create gossip message from device A
	gossipMsg := &proto.GossipMessage{
		SenderDeviceId: deviceAID,
		Timestamp:      time.Now().Unix(),
		MeshView: []*proto.DeviceState{
			{
				DeviceId:          deviceAID,
				PhotoHash:         photoHashBytes,
				LastSeenTimestamp: time.Now().Unix(),
				FirstName:         "Alice",
			},
		},
		GossipRound: 1,
	}

	// First merge - should discover photo
	discoveries1 := meshView.MergeGossip(gossipMsg)
	if len(discoveries1) != 1 {
		t.Errorf("First merge: expected 1 discovery, got %d", len(discoveries1))
	}
	if len(discoveries1) > 0 && discoveries1[0] != deviceAID {
		t.Errorf("First merge: expected discovery of %s, got %s", deviceAID, discoveries1[0])
	}

	// Mark photo as requested
	meshView.MarkPhotoRequested(deviceAID)

	// Second merge with same gossip - should NOT discover again
	gossipMsg.Timestamp = time.Now().Unix()
	gossipMsg.GossipRound = 2
	discoveries2 := meshView.MergeGossip(gossipMsg)
	if len(discoveries2) != 0 {
		t.Errorf("Second merge: expected 0 discoveries (duplicate), got %d", len(discoveries2))
	}

	// Verify PhotoRequestSent flag is still set
	devices := meshView.GetAllDevices()
	var deviceA *MeshDeviceState
	for _, dev := range devices {
		if dev.DeviceID == deviceAID {
			deviceA = dev
			break
		}
	}
	if deviceA == nil {
		t.Fatal("Device A not found in mesh view")
	}
	if !deviceA.PhotoRequestSent {
		t.Error("PhotoRequestSent flag should still be true after duplicate gossip")
	}
	if deviceA.PhotoHash != photoHashHex {
		t.Errorf("Photo hash mismatch: expected %s, got %s", photoHashHex, deviceA.PhotoHash)
	}
}

// TestMeshView_PhotoHashChange tests that a changed photo hash triggers a new discovery
func TestMeshView_PhotoHashChange(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	deviceDir := filepath.Join(tmpDir, "test-device")
	if err := os.MkdirAll(deviceDir, 0755); err != nil {
		t.Fatalf("Failed to create device directory: %v", err)
	}

	cacheManager := NewDeviceCacheManager(deviceDir)
	ourDeviceID := "AAAAA"
	ourHardwareUUID := "11111111-1111-1111-1111-111111111111"
	meshView := NewMeshView(ourDeviceID, ourHardwareUUID, deviceDir, cacheManager)

	deviceAID := "BBBBB"

	// First photo hash
	photoHash1 := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	gossipMsg1 := &proto.GossipMessage{
		SenderDeviceId: deviceAID,
		Timestamp:      time.Now().Unix(),
		MeshView: []*proto.DeviceState{
			{
				DeviceId:          deviceAID,
				PhotoHash:         photoHash1,
				LastSeenTimestamp: time.Now().Unix(),
				FirstName:         "Alice",
			},
		},
		GossipRound: 1,
	}

	// First discovery
	discoveries1 := meshView.MergeGossip(gossipMsg1)
	if len(discoveries1) != 1 {
		t.Fatalf("First merge: expected 1 discovery, got %d", len(discoveries1))
	}
	meshView.MarkPhotoRequested(deviceAID)

	// Wait a bit and send gossip with DIFFERENT photo hash
	time.Sleep(10 * time.Millisecond)
	photoHash2 := []byte{0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10}
	gossipMsg2 := &proto.GossipMessage{
		SenderDeviceId: deviceAID,
		Timestamp:      time.Now().Unix(),
		MeshView: []*proto.DeviceState{
			{
				DeviceId:          deviceAID,
				PhotoHash:         photoHash2,
				LastSeenTimestamp: time.Now().Unix(),
				FirstName:         "Alice",
			},
		},
		GossipRound: 2,
	}

	// Second discovery - should trigger because photo hash changed
	discoveries2 := meshView.MergeGossip(gossipMsg2)
	if len(discoveries2) != 1 {
		t.Errorf("Second merge with new hash: expected 1 discovery, got %d", len(discoveries2))
	}

	// Verify PhotoRequestSent flag was reset
	devices := meshView.GetAllDevices()
	var deviceA *MeshDeviceState
	for _, dev := range devices {
		if dev.DeviceID == deviceAID {
			deviceA = dev
			break
		}
	}
	if deviceA == nil {
		t.Fatal("Device A not found in mesh view")
	}
	if deviceA.PhotoRequestSent {
		t.Error("PhotoRequestSent flag should be false after photo hash change")
	}
	photoHash2Hex := hex.EncodeToString(photoHash2)
	if deviceA.PhotoHash != photoHash2Hex {
		t.Errorf("Photo hash should be updated to %s, got %s", photoHash2Hex, deviceA.PhotoHash)
	}
}

// TestMarkPhotoRequested_Idempotent tests that marking a photo as requested multiple times is safe
func TestMarkPhotoRequested_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	deviceDir := filepath.Join(tmpDir, "test-device")
	if err := os.MkdirAll(deviceDir, 0755); err != nil {
		t.Fatalf("Failed to create device directory: %v", err)
	}

	cacheManager := NewDeviceCacheManager(deviceDir)
	ourDeviceID := "AAAAA"
	ourHardwareUUID := "11111111-1111-1111-1111-111111111111"
	meshView := NewMeshView(ourDeviceID, ourHardwareUUID, deviceDir, cacheManager)

	// Add a device
	deviceAID := "BBBBB"
	photoHashBytes := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	meshView.UpdateDevice(deviceAID, hex.EncodeToString(photoHashBytes), "Alice", 1, "")

	// Mark as requested multiple times
	meshView.MarkPhotoRequested(deviceAID)
	meshView.MarkPhotoRequested(deviceAID)
	meshView.MarkPhotoRequested(deviceAID)

	// Verify flag is set
	devices := meshView.GetAllDevices()
	var deviceA *MeshDeviceState
	for _, dev := range devices {
		if dev.DeviceID == deviceAID {
			deviceA = dev
			break
		}
	}
	if deviceA == nil {
		t.Fatal("Device A not found in mesh view")
	}
	if !deviceA.PhotoRequestSent {
		t.Error("PhotoRequestSent flag should be true")
	}

	// Verify device should not appear in missing photos list
	missing := meshView.GetMissingPhotos()
	for _, dev := range missing {
		if dev.DeviceID == deviceAID {
			t.Error("Device A should not appear in missing photos list after request was marked")
		}
	}
}
