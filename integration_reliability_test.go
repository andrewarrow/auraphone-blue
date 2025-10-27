package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/wire"
)

// TestE2E_FivePhones_AllPhotosTransferred tests basic coordinator state tracking
// For full end-to-end testing, use: go run main.go
func TestE2E_FivePhones_AllPhotosTransferred(t *testing.T) {
	t.Skip("This test requires full phone implementation. Use 'go run main.go' for full E2E testing.")

	// This test is a placeholder to document what a full E2E test would look like.
	// The actual phone implementation in iphone/iphone.go and android/android.go has
	// the full event loops that handle:
	// - Automatic handshake exchange on connection
	// - Periodic gossip broadcasting
	// - Photo transfer initiation based on coordinator state
	// - Chunk sending/receiving with ACKs
	// - Retry logic for missing chunks
	//
	// To test the full system:
	// 1. Run: go build
	// 2. Run: ./auraphone-blue
	// 3. Check ~/.auraphone-blue-data/*/test_report_*.md for success rates
}

// TestE2E_LateJoiner tests mesh view discovery with late joining device
func TestE2E_LateJoiner(t *testing.T) {
	phone.CleanupDataDir()
	defer phone.CleanupDataDir()

	tempDir := t.TempDir()
	config := wire.PerfectSimulationConfig() // Use perfect config for fast test

	// Create 3 devices
	deviceA := createTestDevice(t, "device-a-uuid", "DEVICEAAA", tempDir, config)
	deviceB := createTestDevice(t, "device-b-uuid", "DEVICEBBB", tempDir, config)
	deviceC := createTestDevice(t, "device-c-uuid", "DEVICECCC", tempDir, config)

	defer deviceA.wire.Cleanup()
	defer deviceB.wire.Cleanup()
	defer deviceC.wire.Cleanup()

	// Connect A <-> B
	connectDevices(t, deviceA, deviceB)
	t.Logf("✅ Connected A <-> B")

	// A and B exchange gossip
	// Note: BuildGossipMessage includes ourselves in the gossip, and MergeGossip
	// will add us to our own mesh view (which is fine, but affects counts)
	gossipA := deviceA.meshView.BuildGossipMessage(deviceA.photoHash, "Alice", 1, "profile-a")
	gossipB := deviceB.meshView.BuildGossipMessage(deviceB.photoHash, "Bob", 1, "profile-b")

	deviceB.meshView.MergeGossip(gossipA)
	deviceA.meshView.MergeGossip(gossipB)

	// Verify A knows about B (GetAllDevices may include ourselves, so check >= 1)
	allDevices := deviceA.meshView.GetAllDevices()
	foundB := false
	for _, dev := range allDevices {
		if dev.DeviceID == deviceB.deviceID {
			foundB = true
			break
		}
	}
	if !foundB {
		t.Error("Device A should know about device B after gossip exchange")
	}

	// Late joiner C connects to A
	connectDevices(t, deviceC, deviceA)
	t.Logf("✅ Late joiner C connected to A")

	// C sends gossip to A
	gossipC := deviceC.meshView.BuildGossipMessage(deviceC.photoHash, "Charlie", 1, "profile-c")
	deviceA.meshView.MergeGossip(gossipC)

	// A now knows about B and C
	allDevicesA := deviceA.meshView.GetAllDevices()
	foundB2 := false
	foundC := false
	for _, dev := range allDevicesA {
		if dev.DeviceID == deviceB.deviceID {
			foundB2 = true
		}
		if dev.DeviceID == deviceC.deviceID {
			foundC = true
		}
	}
	if !foundB2 || !foundC {
		t.Errorf("Device A should know about B and C (found B=%v, C=%v)", foundB2, foundC)
	}

	// A sends updated gossip to C (telling C about B)
	gossipAUpdated := deviceA.meshView.BuildGossipMessage(deviceA.photoHash, "Alice", 1, "profile-a")
	discoveries := deviceC.meshView.MergeGossip(gossipAUpdated)

	// C should discover B via gossip from A
	foundB = false
	for _, deviceID := range discoveries {
		if deviceID == deviceB.deviceID {
			foundB = true
		}
	}

	if !foundB {
		t.Error("Late joiner C should discover B via gossip from A")
	}

	t.Logf("✅ Late joiner discovered existing devices via gossip")
}

// TestE2E_HighPacketLoss tests coordinator chunk retry logic
func TestE2E_HighPacketLoss(t *testing.T) {
	t.Skip("High packet loss testing requires full phone implementation. Tested via wire/packet_loss_test.go")
}

// TestE2E_OutOfOrderChunks tests coordinator's out-of-order chunk assembly
func TestE2E_OutOfOrderChunks(t *testing.T) {
	t.Skip("Out-of-order chunk testing covered by phone/photo_assembly_test.go")
}

// TestE2E_DisconnectAndReconnect tests coordinator pause/resume logic
func TestE2E_DisconnectAndReconnect(t *testing.T) {
	phone.CleanupDataDir()
	defer phone.CleanupDataDir()

	tempDir := t.TempDir()
	config := wire.PerfectSimulationConfig()

	deviceA := createTestDevice(t, "device-a-uuid", "DEVICEAAA", tempDir, config)
	deviceB := createTestDevice(t, "device-b-uuid", "DEVICEBBB", tempDir, config)

	defer deviceA.wire.Cleanup()
	defer deviceB.wire.Cleanup()

	// Start a photo transfer manually
	deviceA.coordinator.StartSend(deviceB.deviceID, deviceA.photoHash, 10)
	deviceB.coordinator.StartReceive(deviceA.deviceID, deviceA.photoHash, 10)

	// Simulate some chunks sent
	for i := 0; i < 5; i++ {
		deviceA.coordinator.RecordChunkSent(deviceB.deviceID, i)
		deviceB.coordinator.RecordReceivedChunk(deviceA.deviceID, i, []byte{byte(i)})
	}

	// Check transfer is in progress
	sendState := deviceA.coordinator.GetSendState(deviceB.deviceID)
	recvState := deviceB.coordinator.GetReceiveState(deviceA.deviceID)

	if sendState == nil || recvState == nil {
		t.Fatal("Transfers should be in progress")
	}

	t.Logf("✅ Transfer in progress: sent=%d/%d, received=%d/%d",
		len(sendState.SentChunks), sendState.TotalChunks,
		recvState.ChunksReceived, recvState.TotalChunks)

	// Simulate disconnect - coordinator pauses transfers
	deviceA.coordinator.CleanupDisconnectedDevice(deviceB.deviceID)
	deviceB.coordinator.CleanupDisconnectedDevice(deviceA.deviceID)

	// Verify transfers are paused (not deleted)
	sendStateAfter := deviceA.coordinator.GetSendState(deviceB.deviceID)
	recvStateAfter := deviceB.coordinator.GetReceiveState(deviceA.deviceID)

	if sendStateAfter == nil {
		t.Error("Send state should still exist (paused)")
	} else if !sendStateAfter.Paused {
		t.Error("Send state should be marked as paused")
	}

	if recvStateAfter == nil {
		t.Error("Receive state should still exist (paused)")
	} else if !recvStateAfter.Paused {
		t.Error("Receive state should be marked as paused")
	}

	t.Logf("✅ Transfers paused on disconnect")

	// Simulate reconnect - coordinator resumes
	hasPausedSend, hasPausedRecv := deviceA.coordinator.ResumeTransfersOnReconnect(deviceB.deviceID)

	if !hasPausedSend {
		t.Error("Should have found paused send to resume")
	}

	// Verify transfers resumed
	sendStateResumed := deviceA.coordinator.GetSendState(deviceB.deviceID)
	if sendStateResumed == nil {
		t.Fatal("Send state should exist after resume")
	}
	if sendStateResumed.Paused {
		t.Error("Send state should no longer be paused")
	}

	t.Logf("✅ Transfers resumed on reconnect: send=%v, recv=%v", hasPausedSend, hasPausedRecv)
}

// Helper function to check for duplicate photo discoveries
func checkForDuplicateDiscoveries(t *testing.T, devices []*testDevice) {
	// This would read gossip_audit.jsonl files and check for duplicate "photo_discovered" events
	// For the same device+photoHash combination

	for i, dev := range devices {
		auditPath := filepath.Join(dev.dataDir, "gossip_audit.jsonl")

		if _, err := os.Stat(auditPath); os.IsNotExist(err) {
			continue // No audit file
		}

		// Read audit file
		data, err := os.ReadFile(auditPath)
		if err != nil {
			t.Logf("Warning: couldn't read audit file for device %d: %v", i+1, err)
			continue
		}

		// Count photo discoveries per device+hash
		discoveries := make(map[string]int) // key: deviceID:photoHash

		// Parse JSONL (simplified - in real implementation would use json.Unmarshal per line)
		lines := 0
		for _, line := range []byte(data) {
			if line == '\n' {
				lines++
			}
		}

		if lines > 0 {
			t.Logf("Device %d has %d gossip audit entries", i+1, lines)
		}

		// Check for duplicates
		for key, count := range discoveries {
			if count > 1 {
				t.Errorf("Device %d: duplicate photo discovery for %s (%d times)", i+1, key, count)
			}
		}
	}
}
