package phone

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

// TestPhotoAssembly_OutOfOrderChunks tests that chunks can arrive in any order
func TestPhotoAssembly_OutOfOrderChunks(t *testing.T) {
	// Create a coordinator
	hardwareUUID := "11111111-1111-1111-1111-111111111111"
	coordinator := NewPhotoTransferCoordinator(hardwareUUID)
	defer coordinator.Shutdown()

	// Create test photo data (5 chunks of 4KB each = 20KB)
	photoData := make([]byte, 5*4096)
	for i := range photoData {
		photoData[i] = byte(i % 256)
	}

	// Calculate photo hash
	hash := sha256.Sum256(photoData)
	photoHashHex := hex.EncodeToString(hash[:])

	// Split into chunks
	chunks := SplitIntoChunks(photoData, DefaultChunkSize)
	if len(chunks) != 5 {
		t.Fatalf("Expected 5 chunks, got %d", len(chunks))
	}

	deviceID := "BBBBB"
	totalChunks := len(chunks)

	// Start receiving
	coordinator.StartReceive(deviceID, photoHashHex, totalChunks)

	// Deliver chunks OUT OF ORDER: 1, 3, 2, 0, 4
	order := []int{1, 3, 2, 0, 4}

	for _, chunkIndex := range order {
		coordinator.RecordReceivedChunk(deviceID, chunkIndex, chunks[chunkIndex])
	}

	// Verify all chunks received
	recvState := coordinator.GetReceiveState(deviceID)
	if recvState == nil {
		t.Fatal("Receive state should exist")
	}
	if recvState.ChunksReceived != totalChunks {
		t.Errorf("Expected %d chunks received, got %d", totalChunks, recvState.ChunksReceived)
	}
	if len(recvState.MissingChunks) != 0 {
		t.Errorf("Expected 0 missing chunks, got %d: %v", len(recvState.MissingChunks), recvState.MissingChunks)
	}

	// Verify chunks are stored correctly (indexed properly)
	for i := 0; i < totalChunks; i++ {
		chunk, exists := recvState.ReceivedChunks[i]
		if !exists {
			t.Errorf("Chunk %d not stored", i)
			continue
		}
		if len(chunk) != len(chunks[i]) {
			t.Errorf("Chunk %d: expected length %d, got %d", i, len(chunks[i]), len(chunk))
		}
	}
}

// TestPhotoAssembly_MissingChunks tests that missing chunks are detected
func TestPhotoAssembly_MissingChunks(t *testing.T) {
	hardwareUUID := "11111111-1111-1111-1111-111111111111"
	coordinator := NewPhotoTransferCoordinator(hardwareUUID)
	defer coordinator.Shutdown()

	// Create test photo data (5 chunks)
	photoData := make([]byte, 5*4096)
	for i := range photoData {
		photoData[i] = byte(i % 256)
	}

	hash := sha256.Sum256(photoData)
	photoHashHex := hex.EncodeToString(hash[:])

	chunks := SplitIntoChunks(photoData, DefaultChunkSize)
	deviceID := "CCCCC"
	totalChunks := len(chunks)

	// Start receiving
	coordinator.StartReceive(deviceID, photoHashHex, totalChunks)

	// Deliver only chunks 0, 1, 3 (skip 2 and 4)
	coordinator.RecordReceivedChunk(deviceID, 0, chunks[0])
	coordinator.RecordReceivedChunk(deviceID, 1, chunks[1])
	coordinator.RecordReceivedChunk(deviceID, 3, chunks[3])

	// Check receive state
	recvState := coordinator.GetReceiveState(deviceID)
	if recvState == nil {
		t.Fatal("Receive state should exist")
	}

	// Should have received 3 chunks
	if recvState.ChunksReceived != 3 {
		t.Errorf("Expected 3 chunks received, got %d", recvState.ChunksReceived)
	}

	// Should have 2 missing chunks: [2, 4]
	expectedMissing := map[int]bool{2: true, 4: true}
	if len(recvState.MissingChunks) != 2 {
		t.Errorf("Expected 2 missing chunks, got %d: %v", len(recvState.MissingChunks), recvState.MissingChunks)
	} else {
		for _, missing := range recvState.MissingChunks {
			if !expectedMissing[missing] {
				t.Errorf("Unexpected missing chunk: %d", missing)
			}
		}
	}

	// Transfer should still be in progress (not complete)
	_, _, _, inProgressReceives := coordinator.GetStats()
	if inProgressReceives != 1 {
		t.Errorf("Expected 1 in-progress receive, got %d", inProgressReceives)
	}
}

// TestPhotoAssembly_AllChunksMissing tests when no chunks are delivered
func TestPhotoAssembly_AllChunksMissing(t *testing.T) {
	hardwareUUID := "11111111-1111-1111-1111-111111111111"
	coordinator := NewPhotoTransferCoordinator(hardwareUUID)
	defer coordinator.Shutdown()

	photoHashBytes := []byte{0x01, 0x02, 0x03, 0x04}
	photoHashHex := hex.EncodeToString(photoHashBytes)
	deviceID := "DDDDD"
	totalChunks := 5

	// Start receiving
	coordinator.StartReceive(deviceID, photoHashHex, totalChunks)

	// Don't deliver any chunks

	// Check receive state
	recvState := coordinator.GetReceiveState(deviceID)
	if recvState == nil {
		t.Fatal("Receive state should exist")
	}

	// Should have 0 chunks received
	if recvState.ChunksReceived != 0 {
		t.Errorf("Expected 0 chunks received, got %d", recvState.ChunksReceived)
	}

	// Should have all chunks missing
	if len(recvState.MissingChunks) != totalChunks {
		t.Errorf("Expected %d missing chunks, got %d", totalChunks, len(recvState.MissingChunks))
	}
}

// TestPhotoAssembly_StaleTransferCleanup tests that stale transfers are cleaned up
func TestPhotoAssembly_StaleTransferCleanup(t *testing.T) {
	hardwareUUID := "11111111-1111-1111-1111-111111111111"
	coordinator := NewPhotoTransferCoordinator(hardwareUUID)
	defer coordinator.Shutdown()

	photoHashBytes := []byte{0x01, 0x02, 0x03, 0x04}
	photoHashHex := hex.EncodeToString(photoHashBytes)
	deviceID := "EEEEE"
	totalChunks := 5

	// Start receiving
	coordinator.StartReceive(deviceID, photoHashHex, totalChunks)

	// Deliver one chunk
	coordinator.RecordReceivedChunk(deviceID, 0, []byte{0x01})

	// Verify transfer is in progress
	recvState := coordinator.GetReceiveState(deviceID)
	if recvState == nil {
		t.Fatal("Receive state should exist after starting")
	}

	// Manually set LastActivity to simulate stale transfer
	recvState.LastActivity = time.Now().Add(-35 * time.Second)

	// Run cleanup with 30 second timeout
	cleaned := coordinator.CleanupStaleTransfers(30 * time.Second)
	if cleaned != 1 {
		t.Errorf("Expected 1 transfer cleaned up, got %d", cleaned)
	}

	// Verify transfer was removed
	recvStateAfter := coordinator.GetReceiveState(deviceID)
	if recvStateAfter != nil {
		t.Error("Receive state should be nil after cleanup")
	}
}

// TestPhotoAssembly_ChunkOrder tests that chunks can be reassembled in correct order
func TestPhotoAssembly_ChunkOrder(t *testing.T) {
	hardwareUUID := "11111111-1111-1111-1111-111111111111"
	coordinator := NewPhotoTransferCoordinator(hardwareUUID)
	defer coordinator.Shutdown()

	// Create photo data with distinctive pattern per chunk
	chunks := [][]byte{
		{0x01, 0x01, 0x01, 0x01}, // Chunk 0
		{0x02, 0x02, 0x02, 0x02}, // Chunk 1
		{0x03, 0x03, 0x03, 0x03}, // Chunk 2
		{0x04, 0x04, 0x04, 0x04}, // Chunk 3
	}

	// Assemble expected photo data
	var photoData []byte
	for _, chunk := range chunks {
		photoData = append(photoData, chunk...)
	}

	hash := sha256.Sum256(photoData)
	photoHashHex := hex.EncodeToString(hash[:])
	deviceID := "FFFFF"
	totalChunks := len(chunks)

	// Start receiving
	coordinator.StartReceive(deviceID, photoHashHex, totalChunks)

	// Deliver chunks in random order: 3, 0, 2, 1
	coordinator.RecordReceivedChunk(deviceID, 3, chunks[3])
	coordinator.RecordReceivedChunk(deviceID, 0, chunks[0])
	coordinator.RecordReceivedChunk(deviceID, 2, chunks[2])
	coordinator.RecordReceivedChunk(deviceID, 1, chunks[1])

	// Get receive state
	recvState := coordinator.GetReceiveState(deviceID)
	if recvState == nil {
		t.Fatal("Receive state should exist")
	}

	// Manually assemble chunks in correct order
	var assembled []byte
	for i := 0; i < totalChunks; i++ {
		chunk, exists := recvState.ReceivedChunks[i]
		if !exists {
			t.Fatalf("Chunk %d missing", i)
		}
		assembled = append(assembled, chunk...)
	}

	// Verify assembled data matches original
	if len(assembled) != len(photoData) {
		t.Errorf("Assembled length %d != expected %d", len(assembled), len(photoData))
	}
	for i := range assembled {
		if assembled[i] != photoData[i] {
			t.Errorf("Byte %d: got %02X, expected %02X", i, assembled[i], photoData[i])
		}
	}

	// Verify hash
	assembledHash := sha256.Sum256(assembled)
	if hex.EncodeToString(assembledHash[:]) != photoHashHex {
		t.Error("Assembled photo hash doesn't match expected hash")
	}
}

// TestPhotoAssembly_DuplicateChunks tests that duplicate chunks are handled gracefully
func TestPhotoAssembly_DuplicateChunks(t *testing.T) {
	hardwareUUID := "11111111-1111-1111-1111-111111111111"
	coordinator := NewPhotoTransferCoordinator(hardwareUUID)
	defer coordinator.Shutdown()

	chunks := [][]byte{
		{0x01, 0x02, 0x03},
		{0x04, 0x05, 0x06},
		{0x07, 0x08, 0x09},
	}

	var photoData []byte
	for _, chunk := range chunks {
		photoData = append(photoData, chunk...)
	}

	hash := sha256.Sum256(photoData)
	photoHashHex := hex.EncodeToString(hash[:])
	deviceID := "GGGGG"
	totalChunks := len(chunks)

	// Start receiving
	coordinator.StartReceive(deviceID, photoHashHex, totalChunks)

	// Deliver chunk 0 multiple times
	coordinator.RecordReceivedChunk(deviceID, 0, chunks[0])
	coordinator.RecordReceivedChunk(deviceID, 0, chunks[0])
	coordinator.RecordReceivedChunk(deviceID, 0, chunks[0])

	// Deliver other chunks normally
	coordinator.RecordReceivedChunk(deviceID, 1, chunks[1])
	coordinator.RecordReceivedChunk(deviceID, 2, chunks[2])

	// Check receive state
	recvState := coordinator.GetReceiveState(deviceID)
	if recvState == nil {
		t.Fatal("Receive state should exist")
	}

	// Should have all chunks (duplicates should be overwritten/ignored)
	if recvState.ChunksReceived != totalChunks {
		t.Errorf("Expected %d chunks received, got %d", totalChunks, recvState.ChunksReceived)
	}

	// No missing chunks
	if len(recvState.MissingChunks) != 0 {
		t.Errorf("Expected 0 missing chunks, got %d", len(recvState.MissingChunks))
	}
}
