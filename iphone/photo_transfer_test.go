package iphone

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/wire"
)

// TestIOSPhotoTransfer verifies end-to-end photo transfer between iOS devices
func TestIOSPhotoTransfer(t *testing.T) {
	config := wire.PerfectSimulationConfig()

	tempDir1 := t.TempDir()
	tempDir2 := t.TempDir()

	// Create sender and receiver
	sender := NewIPhone("sender-uuid", "iPhone Sender", tempDir1, config)
	receiver := NewIPhone("receiver-uuid", "iPhone Receiver", tempDir2, config)

	defer sender.Cleanup()
	defer receiver.Cleanup()

	// Set device IDs
	sender.deviceID = "SENDER123"
	receiver.deviceID = "RECEIVER456"

	// Sender has a photo
	testPhoto := []byte("test photo data for transfer")
	photoHash := sha256.Sum256(testPhoto)
	photoHashHex := hex.EncodeToString(photoHash[:])

	sender.photoHash = photoHashHex
	sender.photoData = testPhoto

	// Save sender's photo to cache
	if err := sender.cacheManager.SavePhoto(sender.deviceID, testPhoto, photoHashHex); err != nil {
		t.Fatalf("Failed to save sender's photo: %v", err)
	}

	// Initialize photo coordinators
	sender.photoCoordinator = phone.NewPhotoTransferCoordinator(sender.deviceID)
	receiver.photoCoordinator = phone.NewPhotoTransferCoordinator(receiver.deviceID)

	// Start both devices
	if err := sender.Start(); err != nil {
		t.Fatalf("Failed to start sender: %v", err)
	}
	if err := receiver.Start(); err != nil {
		t.Fatalf("Failed to start receiver: %v", err)
	}

	// Wait for connection
	time.Sleep(200 * time.Millisecond)

	// Receiver requests photo from sender
	receiverUUID := receiver.hardwareUUID
	senderUUID := sender.hardwareUUID

	// Map UUIDs to device IDs
	sender.uuidToDeviceIDMutex.Lock()
	sender.uuidToDeviceID[receiverUUID] = receiver.deviceID
	sender.uuidToDeviceIDMutex.Unlock()

	receiver.uuidToDeviceIDMutex.Lock()
	receiver.uuidToDeviceID[senderUUID] = sender.deviceID
	receiver.uuidToDeviceIDMutex.Unlock()

	// Simulate photo request from receiver
	requestMsg, err := phone.CreatePhotoRequestMessage(receiver.deviceID, sender.deviceID, photoHash[:])
	if err != nil {
		t.Fatalf("Failed to create photo request: %v", err)
	}

	// Send request to sender
	if err := receiver.wire.WriteCharacteristic(senderUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, requestMsg); err != nil {
		t.Fatalf("Failed to send photo request: %v", err)
	}

	// Give sender time to process request and send photo
	time.Sleep(500 * time.Millisecond)

	// Sender should start sending photo chunks
	sendState := sender.photoCoordinator.GetSendState(receiver.deviceID)
	if sendState == nil {
		t.Fatal("Expected sender to have active send state after photo request")
	}

	t.Logf("Sender state: %d/%d chunks sent", sendState.ChunksSent, sendState.TotalChunks)

	// Wait for complete transfer
	time.Sleep(1 * time.Second)

	// Verify receiver got all chunks
	recvState := receiver.photoCoordinator.GetReceiveState(sender.deviceID)
	if recvState != nil && recvState.ChunksReceived < recvState.TotalChunks {
		t.Logf("Receiver state: %d/%d chunks received", recvState.ChunksReceived, recvState.TotalChunks)
	}

	// Check if receiver saved the photo
	receivedPhotoPath := receiver.cacheManager.GetPhotoPath(sender.deviceID)
	receivedPhoto, err := receiver.cacheManager.LoadPhoto(sender.deviceID)
	if err != nil {
		t.Fatalf("Failed to load received photo: %v (path: %s)", err, receivedPhotoPath)
	}

	// Verify photo content matches
	if !bytes.Equal(receivedPhoto, testPhoto) {
		t.Errorf("Received photo doesn't match original. Expected %d bytes, got %d bytes",
			len(testPhoto), len(receivedPhoto))
	}

	t.Logf("✅ Photo transferred successfully: %d bytes", len(receivedPhoto))
}

// TestIOSPhotoChunking verifies that large photos are properly chunked
func TestIOSPhotoChunking(t *testing.T) {
	// Create a large photo (larger than default chunk size)
	largePhoto := make([]byte, phone.DefaultChunkSize*3+100)
	for i := range largePhoto {
		largePhoto[i] = byte(i % 256)
	}

	photoHash := sha256.Sum256(largePhoto)
	senderID := "SENDER"
	receiverID := "RECEIVER"

	// Split into chunks
	chunks := phone.SplitIntoChunks(largePhoto, phone.DefaultChunkSize)
	totalChunks := int32(len(chunks))

	if len(chunks) != 4 {
		t.Errorf("Expected 4 chunks for large photo, got %d", len(chunks))
	}

	// Encode each chunk
	var encodedChunks [][]byte
	for i, chunkData := range chunks {
		chunk, err := phone.CreatePhotoChunk(senderID, receiverID, photoHash[:], int32(i), totalChunks, chunkData)
		if err != nil {
			t.Fatalf("Failed to create chunk %d: %v", i, err)
		}

		encoded, err := phone.EncodePhotoChunk(chunk)
		if err != nil {
			t.Fatalf("Failed to encode chunk %d: %v", i, err)
		}

		encodedChunks = append(encodedChunks, encoded)
	}

	// Decode and reassemble
	reassembled := make([]byte, 0)
	for i, encoded := range encodedChunks {
		chunk, err := phone.DecodePhotoChunk(encoded)
		if err != nil {
			t.Fatalf("Failed to decode chunk %d: %v", i, err)
		}

		if int(chunk.ChunkIndex) != i {
			t.Errorf("Chunk %d has wrong index: %d", i, chunk.ChunkIndex)
		}

		reassembled = append(reassembled, chunk.ChunkData...)
	}

	// Verify reassembled matches original
	if !bytes.Equal(reassembled, largePhoto) {
		t.Errorf("Reassembled photo doesn't match original. Expected %d bytes, got %d bytes",
			len(largePhoto), len(reassembled))
	}

	t.Logf("✅ Large photo chunked and reassembled correctly: %d chunks, %d bytes total",
		len(chunks), len(largePhoto))
}

// TestIOSPhotoTransferCoordinator verifies state machine for iOS photo transfers
func TestIOSPhotoTransferCoordinator(t *testing.T) {
	coord := phone.NewPhotoTransferCoordinator("test-device")

	deviceID := "DEVICE123"
	photoHash := "abcd1234"
	totalChunks := 10

	// Start a send
	coord.StartSend(deviceID, photoHash, totalChunks)
	sendState := coord.GetSendState(deviceID)

	if sendState == nil {
		t.Fatal("Expected send state to be created")
	}

	if sendState.PhotoHash != photoHash {
		t.Errorf("Expected photo hash %s, got %s", photoHash, sendState.PhotoHash)
	}

	// Simulate sending chunks
	for i := 0; i < totalChunks; i++ {
		coord.UpdateSendProgress(deviceID, i+1)
	}

	sendState = coord.GetSendState(deviceID)
	if sendState.ChunksSent != totalChunks {
		t.Errorf("Expected %d chunks sent, got %d", totalChunks, sendState.ChunksSent)
	}

	// Complete send
	coord.CompleteSend(deviceID, photoHash)
	sendState = coord.GetSendState(deviceID)

	if sendState != nil {
		t.Error("Expected send state to be cleaned up after completion")
	}

	t.Logf("✅ Photo transfer coordinator state machine works correctly")
}

// TestIOSPhotoTransferTimeout verifies timeout cleanup
func TestIOSPhotoTransferTimeout(t *testing.T) {
	coord := phone.NewPhotoTransferCoordinator("test-device")

	deviceID := "DEVICE123"
	photoHash := "abcd1234"

	// Start a send
	coord.StartSend(deviceID, photoHash, 10)

	// Wait for timeout
	time.Sleep(20 * time.Millisecond)

	// Cleanup with short timeout (10ms)
	coord.CleanupStaleTransfers(10 * time.Millisecond)

	// Verify state was cleaned up
	sendState := coord.GetSendState(deviceID)
	if sendState != nil {
		t.Error("Expected stale send state to be cleaned up after timeout")
	}

	t.Logf("✅ Stale transfers cleaned up correctly")
}

// TestIOSMultiplePhotoTransfersConcurrent verifies handling of multiple simultaneous transfers
func TestIOSMultiplePhotoTransfersConcurrent(t *testing.T) {
	coord := phone.NewPhotoTransferCoordinator("test-device")

	// Start sends to 5 different devices
	for i := 1; i <= 5; i++ {
		deviceID := "DEVICE" + string(rune('0'+i))
		photoHash := "photohash" + string(rune('0'+i))
		coord.StartSend(deviceID, photoHash, 10)
	}

	// Verify all 5 sends are active
	for i := 1; i <= 5; i++ {
		deviceID := "DEVICE" + string(rune('0'+i))
		sendState := coord.GetSendState(deviceID)
		if sendState == nil {
			t.Errorf("Expected send state for %s", deviceID)
		}
	}

	// Complete 3 of them
	for i := 1; i <= 3; i++ {
		deviceID := "DEVICE" + string(rune('0'+i))
		photoHash := "photohash" + string(rune('0'+i))
		coord.CompleteSend(deviceID, photoHash)
	}

	// Verify 3 are completed, 2 are still active
	completedCount := 0
	activeCount := 0
	for i := 1; i <= 5; i++ {
		deviceID := "DEVICE" + string(rune('0'+i))
		sendState := coord.GetSendState(deviceID)
		if sendState == nil {
			completedCount++
		} else {
			activeCount++
		}
	}

	if completedCount != 3 {
		t.Errorf("Expected 3 completed sends, got %d", completedCount)
	}

	if activeCount != 2 {
		t.Errorf("Expected 2 active sends, got %d", activeCount)
	}

	t.Logf("✅ Concurrent photo transfers managed correctly: %d completed, %d active",
		completedCount, activeCount)
}
