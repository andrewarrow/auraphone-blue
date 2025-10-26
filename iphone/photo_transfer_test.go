package iphone

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/user/auraphone-blue/phone"
)

// TestIOSPhotoTransfer verifies end-to-end photo transfer between iOS devices
func TestIOSPhotoTransfer(t *testing.T) {
	// Clean up any leftover test data to prevent cross-test contamination
	phone.CleanupDataDir()

	// Create sender and receiver
	sender := NewIPhone("sender-uuid")
	receiver := NewIPhone("receiver-uuid")

	if sender == nil || receiver == nil {
		t.Fatal("Failed to create iPhone devices")
	}

	defer sender.Stop()
	defer receiver.Stop()

	// Create temporary test photos
	testDir := t.TempDir()
	senderPhotoPath := testDir + "/sender_photo.jpg"
	testPhoto := []byte("test photo data for transfer")

	if err := os.WriteFile(senderPhotoPath, testPhoto, 0644); err != nil {
		t.Fatal(err)
	}

	// Set sender's photo
	if err := sender.SetProfilePhoto(senderPhotoPath); err != nil {
		t.Fatalf("Failed to set sender's photo: %v", err)
	}

	// Start both devices
	sender.Start()
	receiver.Start()

	// Wait for full photo transfer cycle:
	// - Discovery (500ms scan delay)
	// - Connection establishment (up to 100ms)
	// - Initial gossip exchange (immediate after connection)
	// - Gossip processing triggers photo request
	// - Photo chunking and transfer (depends on photo size, default 4KB chunks)
	// - Photo reassembly and save
	// Total: need at least 6-7 seconds for small photos
	time.Sleep(7 * time.Second)

	// Get device IDs
	senderID := sender.GetDeviceID()
	receiverID := receiver.GetDeviceID()

	// Check photo coordinator states
	senderCoord := sender.GetPhotoCoordinator()
	receiverCoord := receiver.GetPhotoCoordinator()

	sendState := senderCoord.GetSendState(receiverID)
	if sendState != nil {
		t.Logf("Sender state: %d/%d chunks sent", sendState.ChunksSent, sendState.TotalChunks)
	}

	recvState := receiverCoord.GetReceiveState(senderID)
	if recvState != nil {
		t.Logf("Receiver state: %d/%d chunks received", recvState.ChunksReceived, recvState.TotalChunks)
	}

	// Try to load received photo
	receiverCache := receiver.GetCacheManager()
	receivedPhoto, err := receiverCache.LoadDevicePhoto(senderID)
	if err != nil {
		t.Fatalf("Photo transfer did not complete: %v", err)
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
		deviceID := fmt.Sprintf("DEVICE%d", i)
		photoHash := fmt.Sprintf("photohash%d", i)
		coord.StartSend(deviceID, photoHash, 10)
	}

	// Verify all 5 sends are active
	for i := 1; i <= 5; i++ {
		deviceID := fmt.Sprintf("DEVICE%d", i)
		sendState := coord.GetSendState(deviceID)
		if sendState == nil {
			t.Errorf("Expected send state for %s", deviceID)
		}
	}

	// Complete 3 of them
	for i := 1; i <= 3; i++ {
		deviceID := fmt.Sprintf("DEVICE%d", i)
		photoHash := fmt.Sprintf("photohash%d", i)
		coord.CompleteSend(deviceID, photoHash)
	}

	// Verify 3 are completed, 2 are still active
	completedCount := 0
	activeCount := 0
	for i := 1; i <= 5; i++ {
		deviceID := fmt.Sprintf("DEVICE%d", i)
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
