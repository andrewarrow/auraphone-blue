package iphone

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
	logger.SetLevel(logger.ERROR) // Reduce noise in tests

	// Create two test devices
	uuidA := "test-device-a"
	uuidB := "test-device-b"

	deviceA := NewIPhone(uuidA)
	deviceB := NewIPhone(uuidB)

	// Set different profile photos for each device
	testPhotoA := []byte("test-photo-data-from-device-A-1234567890")
	testPhotoB := []byte("test-photo-data-from-device-B-abcdefghij")

	// Calculate hashes
	hashA := fmt.Sprintf("%x", sha256.Sum256(testPhotoA))
	hashB := fmt.Sprintf("%x", sha256.Sum256(testPhotoB))

	// Store photos in their caches
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

	// Track callbacks
	deviceAReceivedPhoto := make(chan []byte, 1)
	deviceBReceivedPhoto := make(chan []byte, 1)

	deviceA.SetDiscoveryCallback(func(device phone.DiscoveredDevice) {
		if len(device.PhotoData) > 0 {
			deviceAReceivedPhoto <- device.PhotoData
		}
	})

	deviceB.SetDiscoveryCallback(func(device phone.DiscoveredDevice) {
		if len(device.PhotoData) > 0 {
			deviceBReceivedPhoto <- device.PhotoData
		}
	})

	// Start both devices
	deviceA.Start()
	defer deviceA.Stop()

	deviceB.Start()
	defer deviceB.Stop()

	// Wait for discovery
	time.Sleep(500 * time.Millisecond)

	// Simulate connection establishment
	// In real code, this happens via wire layer, but we're testing the handleHandshake logic
	deviceA.handleIncomingCentralConnection(uuidB)
	deviceB.handleIncomingCentralConnection(uuidA)

	// Create handshake messages
	handshakeA := createTestHandshake(deviceA)
	handshakeB := createTestHandshake(deviceB)

	// *** KEY TEST: Send handshakes MULTIPLE times to simulate real scenario ***
	// In real BLE, handshakes can arrive 2-3 times due to bidirectional exchange
	for i := 0; i < 3; i++ {
		t.Logf("Sending handshake round %d", i+1)

		// Device A receives handshake from B
		deviceA.handleHandshake(uuidB, handshakeB)

		// Device B receives handshake from A
		deviceB.handleHandshake(uuidA, handshakeA)

		// Small delay to allow async operations
		time.Sleep(100 * time.Millisecond)
	}

	// Check transfer state - should have exactly ONE active transfer per device
	deviceA.mu.RLock()
	transfersA := len(deviceA.photoTransfers)
	deviceA.mu.RUnlock()

	deviceB.mu.RLock()
	transfersB := len(deviceB.photoTransfers)
	deviceB.mu.RUnlock()

	if transfersA > 1 {
		t.Errorf("Device A has %d active transfers, expected 0 or 1", transfersA)
	}
	if transfersB > 1 {
		t.Errorf("Device B has %d active transfers, expected 0 or 1", transfersB)
	}

	t.Logf("✅ Device A has %d active transfers (expected 0-1)", transfersA)
	t.Logf("✅ Device B has %d active transfers (expected 0-1)", transfersB)

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

	deviceA := NewIPhone(uuidA)
	deviceB := NewIPhone(uuidB)

	// Set photo for device B
	testPhoto := []byte("rapid-test-photo-data")
	hashB := fmt.Sprintf("%x", sha256.Sum256(testPhoto))

	deviceB.photoData = testPhoto
	deviceB.photoHash = hashB
	_, err := deviceB.photoCache.SavePhoto(testPhoto, uuidB, deviceB.deviceID)
	if err != nil {
		t.Fatalf("Failed to save photo: %v", err)
	}

	deviceA.Start()
	defer deviceA.Stop()

	deviceB.Start()
	defer deviceB.Stop()

	time.Sleep(200 * time.Millisecond)

	// Simulate connection
	deviceA.handleIncomingCentralConnection(uuidB)

	// Create handshake
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

	// Check that only ONE transfer was started
	deviceA.mu.RLock()
	transfers := len(deviceA.photoTransfers)
	transferState := deviceA.photoTransfers[uuidB]
	deviceA.mu.RUnlock()

	if transfers != 1 {
		t.Errorf("Expected exactly 1 active transfer, got %d", transfers)
	} else {
		t.Logf("✅ Only 1 active transfer despite 10 rapid handshakes")
	}

	if transferState != nil && transferState.PhotoHash != hashB {
		t.Errorf("Transfer has wrong photo hash: expected %s, got %s", hashB, transferState.PhotoHash)
	}
}

// Helper functions

func createTestHandshake(device *IPhone) []byte {
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

	data, _ := proto.Marshal(pbHandshake)
	return data
}

func sendTestPhotoChunks(t *testing.T, sender *IPhone, receiver *IPhone, senderUUID string, photoData []byte, photoHash string) {
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
		receiver.handlePhotoData(senderUUID, data)

		t.Logf("Sent chunk %d/%d from %s to receiver", i+1, len(chunks), senderUUID[:8])
	}
}
