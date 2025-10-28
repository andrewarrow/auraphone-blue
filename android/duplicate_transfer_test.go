package android

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	pb "github.com/user/auraphone-blue/proto"
	"google.golang.org/protobuf/proto"
)

// TestDuplicateHandshakesNoPhotoCorruption tests the scenario where:
// - Two devices connect and exchange handshakes
// - Handshakes are received MULTIPLE times (which happens in real scenarios)
// - Only ONE photo transfer should occur per device pair
// - Photos should transfer correctly without corruption
//
// This is a regression test for the bug where duplicate handshakes caused
// multiple photo subscriptions, leading to interleaved chunks and corrupted images.
func TestDuplicateHandshakesNoPhotoCorruption(t *testing.T) {
	logger.SetLevel(logger.DEBUG) // Enable debug to find hang

	// Create two test devices
	uuidA := "test-device-a"
	uuidB := "test-device-b"

	deviceA := NewAndroid(uuidA)
	deviceB := NewAndroid(uuidB)

	// DON'T set photos yet - we want to control when handshakes with photos happen
	// This prevents the natural handshake from starting photo transfers

	// Track callbacks
	deviceAReceivedPhoto := make(chan []byte, 10) // Larger buffer to catch all transfers
	deviceBReceivedPhoto := make(chan []byte, 10)

	deviceA.SetDiscoveryCallback(func(device phone.DiscoveredDevice) {
		if len(device.PhotoData) > 0 {
			select {
			case deviceAReceivedPhoto <- device.PhotoData:
			default:
				t.Logf("Warning: Device A photo channel full, dropping photo")
			}
		}
	})

	deviceB.SetDiscoveryCallback(func(device phone.DiscoveredDevice) {
		if len(device.PhotoData) > 0 {
			select {
			case deviceBReceivedPhoto <- device.PhotoData:
			default:
				t.Logf("Warning: Device B photo channel full, dropping photo")
			}
		}
	})

	// Start both devices
	deviceA.Start()
	defer deviceA.Stop()

	deviceB.Start()
	defer deviceB.Stop()

	// Wait for discovery and automatic connection
	// The devices will discover each other and connect based on role negotiation
	time.Sleep(2 * time.Second)

	// Verify connections are established
	if !deviceA.wire.IsConnected(uuidB) {
		t.Fatalf("Device A not connected to B after 2 seconds")
	}
	if !deviceB.wire.IsConnected(uuidA) {
		t.Fatalf("Device B not connected to A after 2 seconds")
	}

	t.Logf("✅ Connections established, now setting photos")

	// NOW set photos - after connection but before our manual handshakes
	testPhotoA := []byte("test-photo-data-from-device-A-1234567890")
	testPhotoB := []byte("test-photo-data-from-device-B-abcdefghij")

	// Calculate hashes
	hashA := fmt.Sprintf("%x", sha256.Sum256(testPhotoA))
	hashB := fmt.Sprintf("%x", sha256.Sum256(testPhotoB))

	// Store photos in device state and caches
	deviceA.photoData = testPhotoA
	deviceA.photoHash = hashA
	_, err := deviceA.photoCache.SavePhoto(testPhotoA, uuidA, deviceA.deviceID)
	if err != nil {
		t.Fatalf("Failed to save photo A: %v", err)
	}

	deviceB.photoData = testPhotoB
	deviceB.photoHash = hashB
	_, err = deviceB.photoCache.SavePhoto(testPhotoB, uuidB, deviceB.deviceID)
	if err != nil {
		t.Fatalf("Failed to save photo B: %v", err)
	}

	// Create handshake messages WITH photo hashes
	handshakeA := createTestHandshake(deviceA)
	handshakeB := createTestHandshake(deviceB)

	// *** KEY TEST: Send handshakes MULTIPLE times to simulate real scenario ***
	// In real BLE, handshakes can arrive 2-3 times due to bidirectional exchange
	for i := 0; i < 3; i++ {
		t.Logf("======== Sending handshake round %d ========", i+1)

		// Device A receives handshake from B
		t.Logf("Calling deviceA.handleHandshake(uuidB=%s, handshakeB)", uuidB[:8])
		deviceA.handleHandshake(uuidB, handshakeB)
		t.Logf("deviceA.handleHandshake returned")

		// Device B receives handshake from A
		t.Logf("Calling deviceB.handleHandshake(uuidA=%s, handshakeA)", uuidA[:8])
		deviceB.handleHandshake(uuidA, handshakeA)
		t.Logf("deviceB.handleHandshake returned")

		// Small delay to allow async operations
		t.Logf("Sleeping 100ms...")
		time.Sleep(100 * time.Millisecond)
	}

	// Check transfer state - should have AT MOST ONE active transfer per device
	// There may already be transfers from the natural handshake exchange
	deviceA.mu.RLock()
	transfersA := len(deviceA.photoTransfers)
	deviceA.mu.RUnlock()

	deviceB.mu.RLock()
	transfersB := len(deviceB.photoTransfers)
	deviceB.mu.RUnlock()

	if transfersA > 1 {
		t.Errorf("❌ Device A has %d active transfers, expected 0 or 1 (duplicate transfers detected!)", transfersA)
	}
	if transfersB > 1 {
		t.Errorf("❌ Device B has %d active transfers, expected 0 or 1 (duplicate transfers detected!)", transfersB)
	}

	t.Logf("✅ Device A has %d active transfers (no duplicates detected)", transfersA)
	t.Logf("✅ Device B has %d active transfers (no duplicates detected)", transfersB)

	// Simulate photo chunk transfer from B to A
	sendTestPhotoChunks(t, deviceB, deviceA, uuidB, testPhotoB, hashB)

	// Wait for device A to receive and process photo
	select {
	case receivedPhoto := <-deviceAReceivedPhoto:
		if !bytes.Equal(receivedPhoto, testPhotoB) {
			t.Errorf("Device A received corrupted photo: expected %d bytes, got %d bytes",
				len(testPhotoB), len(receivedPhoto))
			t.Errorf("Expected hash: %s", hashB)
			t.Errorf("Received hash: %x", sha256.Sum256(receivedPhoto))
		} else {
			t.Logf("✅ Device A received correct photo from B (%d bytes)", len(receivedPhoto))
		}
	case <-time.After(3 * time.Second):
		t.Error("❌ Timeout: Device A did not receive photo from B")
	}

	// Simulate photo chunk transfer from A to B
	sendTestPhotoChunks(t, deviceA, deviceB, uuidA, testPhotoA, hashA)

	// Wait for device B to receive and process photo
	select {
	case receivedPhoto := <-deviceBReceivedPhoto:
		if !bytes.Equal(receivedPhoto, testPhotoA) {
			t.Errorf("Device B received corrupted photo: expected %d bytes, got %d bytes",
				len(testPhotoA), len(receivedPhoto))
			t.Errorf("Expected hash: %s", hashA)
			t.Errorf("Received hash: %x", sha256.Sum256(receivedPhoto))
		} else {
			t.Logf("✅ Device B received correct photo from A (%d bytes)", len(receivedPhoto))
		}
	case <-time.After(3 * time.Second):
		t.Error("❌ Timeout: Device B did not receive photo from A")
	}

	t.Logf("✅ Test complete: No photo corruption from duplicate handshakes")
}

// TestRapidHandshakesDontStartDuplicateTransfers tests that rapid handshakes
// don't cause race conditions in transfer state management
func TestRapidHandshakesDontStartDuplicateTransfers(t *testing.T) {
	logger.SetLevel(logger.ERROR)

	uuidA := "rapid-test-a"
	uuidB := "rapid-test-b"

	deviceA := NewAndroid(uuidA)
	deviceB := NewAndroid(uuidB)

	// DON'T set photo yet - wait for connection first

	deviceA.Start()
	defer deviceA.Stop()

	deviceB.Start()
	defer deviceB.Stop()

	// Wait for discovery and automatic connection
	time.Sleep(2 * time.Second)

	// Verify connection is established
	if !deviceA.wire.IsConnected(uuidB) {
		t.Fatalf("Device A not connected to B after 2 seconds")
	}

	t.Logf("✅ Connection established, now setting photo")

	// NOW set photo for device B - after connection established
	testPhoto := []byte("rapid-test-photo-data")
	hashB := fmt.Sprintf("%x", sha256.Sum256(testPhoto))

	deviceB.photoData = testPhoto
	deviceB.photoHash = hashB
	_, err := deviceB.photoCache.SavePhoto(testPhoto, uuidB, deviceB.deviceID)
	if err != nil {
		t.Fatalf("Failed to save photo: %v", err)
	}

	// Create handshake WITH photo hash
	handshakeB := createTestHandshake(deviceB)

	// *** KEY TEST: Send handshakes RAPIDLY in parallel ***
	// This simulates the worst-case race condition
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(iteration int) {
			deviceA.handleHandshake(uuidB, handshakeB)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	time.Sleep(500 * time.Millisecond)

	// Check that AT MOST ONE transfer was started
	// There may be 0 transfers if natural handshake already completed photo transfer
	// or 1 transfer if it's in progress
	deviceA.mu.RLock()
	transfers := len(deviceA.photoTransfers)
	transferState := deviceA.photoTransfers[uuidB]
	deviceA.mu.RUnlock()

	if transfers > 1 {
		t.Errorf("❌ Expected at most 1 active transfer, got %d (duplicate transfers detected!)", transfers)
	} else {
		t.Logf("✅ Only %d active transfer despite 10 rapid handshakes (no duplicates)", transfers)
	}

	if transferState != nil && transferState.PhotoHash != hashB {
		t.Errorf("❌ Transfer has wrong photo hash: expected %s, got %s", hashB, transferState.PhotoHash)
	}
}

// Helper functions

func createTestHandshake(device *Android) *pb.HandshakeMessage {
	// Convert photo hash to bytes for protobuf
	var photoHashBytes []byte
	if device.photoHash != "" {
		for i := 0; i < len(device.photoHash); i += 2 {
			if i+2 <= len(device.photoHash) {
				var b byte
				fmt.Sscanf(device.photoHash[i:i+2], "%02x", &b)
				photoHashBytes = append(photoHashBytes, b)
			}
		}
	}

	pbHandshake := &pb.HandshakeMessage{
		DeviceId:        device.deviceID,
		FirstName:       device.firstName,
		ProtocolVersion: 1,
		TxPhotoHash:     photoHashBytes,
	}

	return pbHandshake
}

func sendTestPhotoChunks(t *testing.T, sender *Android, receiver *Android, senderUUID string, photoData []byte, photoHash string) {
	chunks := sender.photoChunker.ChunkPhoto(photoData)

	// Convert photo hash to bytes for protobuf
	var photoHashBytes []byte
	for i := 0; i < len(photoHash); i += 2 {
		if i+2 <= len(photoHash) {
			var b byte
			fmt.Sscanf(photoHash[i:i+2], "%02x", &b)
			photoHashBytes = append(photoHashBytes, b)
		}
	}

	for i, chunk := range chunks {
		// Create chunk message
		chunkMsg := &pb.PhotoChunkMessage{
			SenderDeviceId: sender.deviceID,
			TargetDeviceId: receiver.deviceID,
			PhotoHash:      photoHashBytes,
			ChunkIndex:     int32(i),
			TotalChunks:    int32(len(chunks)),
			ChunkData:      chunk,
		}

		data, _ := proto.Marshal(chunkMsg)

		// Send to receiver
		receiver.handlePhotoChunk(senderUUID, data)

		t.Logf("Sent chunk %d/%d from %s to receiver", i+1, len(chunks), senderUUID[:8])
	}
}
