package phone

import (
	"crypto/sha256"
	"testing"
	"time"
)

// TestPhotoChunkCreationAndEncoding verifies photo chunk creation
func TestPhotoChunkCreationAndEncoding(t *testing.T) {
	testPhoto := []byte("test photo data for chunking")
	photoHash := sha256.Sum256(testPhoto)
	senderID := "SENDER123"
	receiverID := "RECEIVER456"

	// Split into chunks
	chunks := SplitIntoChunks(testPhoto, DefaultChunkSize)
	totalChunks := int32(len(chunks))

	// Create and encode chunks
	var encodedChunks [][]byte
	for i, chunkData := range chunks {
		chunk, err := CreatePhotoChunk(
			senderID,
			receiverID,
			photoHash[:],
			int32(i),
			totalChunks,
			chunkData,
		)
		if err != nil {
			t.Fatalf("Failed to create chunk %d: %v", i, err)
		}

		encoded, err := EncodePhotoChunk(chunk)
		if err != nil {
			t.Fatalf("Failed to encode chunk %d: %v", i, err)
		}

		encodedChunks = append(encodedChunks, encoded)
	}

	// Verify we got all chunks
	if len(encodedChunks) != len(chunks) {
		t.Errorf("Expected %d encoded chunks, got %d", len(chunks), len(encodedChunks))
	}

	// Decode and reassemble
	reassembled := make([]byte, 0)
	for i, encoded := range encodedChunks {
		chunk, err := DecodePhotoChunk(encoded)
		if err != nil {
			t.Fatalf("Failed to decode chunk %d: %v", i, err)
		}

		if int(chunk.ChunkIndex) != i {
			t.Errorf("Chunk %d has wrong index: %d", i, chunk.ChunkIndex)
		}

		if chunk.TotalChunks != totalChunks {
			t.Errorf("Chunk %d has wrong total chunks: expected %d, got %d", i, totalChunks, chunk.TotalChunks)
		}

		reassembled = append(reassembled, chunk.ChunkData...)
	}

	// Verify reassembled matches original
	if string(reassembled) != string(testPhoto) {
		t.Errorf("Reassembled photo doesn't match original")
	}
}

// TestSplitIntoChunks verifies photo splitting logic
func TestSplitIntoChunks(t *testing.T) {
	// Test with photo smaller than chunk size
	smallPhoto := []byte("small")
	smallChunks := SplitIntoChunks(smallPhoto, DefaultChunkSize)
	if len(smallChunks) != 1 {
		t.Errorf("Expected 1 chunk for small photo, got %d", len(smallChunks))
	}

	// Test with photo exactly chunk size
	exactPhoto := make([]byte, DefaultChunkSize)
	exactChunks := SplitIntoChunks(exactPhoto, DefaultChunkSize)
	if len(exactChunks) != 1 {
		t.Errorf("Expected 1 chunk for exact size photo, got %d", len(exactChunks))
	}

	// Test with photo larger than chunk size
	largePhoto := make([]byte, DefaultChunkSize*2+100)
	for i := range largePhoto {
		largePhoto[i] = byte(i % 256)
	}
	largeChunks := SplitIntoChunks(largePhoto, DefaultChunkSize)

	expectedChunks := 3 // 2 full chunks + 1 partial
	if len(largeChunks) != expectedChunks {
		t.Errorf("Expected %d chunks, got %d", expectedChunks, len(largeChunks))
	}

	// Verify reassembling works
	reassembled := make([]byte, 0)
	for _, chunk := range largeChunks {
		reassembled = append(reassembled, chunk...)
	}

	if len(reassembled) != len(largePhoto) {
		t.Errorf("Reassembled size %d doesn't match original %d", len(reassembled), len(largePhoto))
	}

	for i := range reassembled {
		if reassembled[i] != largePhoto[i] {
			t.Errorf("Reassembled byte %d doesn't match original", i)
			break
		}
	}
}

// TestPhotoTransferCoordinator verifies the state machine for photo transfers
func TestPhotoTransferCoordinator(t *testing.T) {
	coord := NewPhotoTransferCoordinator("test-device")

	deviceID := "DEVICE123"
	photoHash := "abcd1234"
	totalChunks := 5

	// Test starting a send
	coord.StartSend(deviceID, photoHash, totalChunks)
	sendState := coord.GetSendState(deviceID)

	if sendState == nil {
		t.Fatal("Expected send state to be created")
	}

	if sendState.PhotoHash != photoHash {
		t.Errorf("Expected photo hash %s, got %s", photoHash, sendState.PhotoHash)
	}

	// Update progress
	coord.UpdateSendProgress(deviceID, 3)
	sendState = coord.GetSendState(deviceID)

	if sendState.ChunksSent != 3 {
		t.Errorf("Expected 3 chunks sent, got %d", sendState.ChunksSent)
	}

	// Complete send
	coord.CompleteSend(deviceID, photoHash)
	sendState = coord.GetSendState(deviceID)

	if sendState != nil {
		t.Error("Expected send state to be cleaned up after completion")
	}

	// Test starting a receive
	coord.StartReceive(deviceID, photoHash, totalChunks)
	recvState := coord.GetReceiveState(deviceID)

	if recvState == nil {
		t.Fatal("Expected receive state to be created")
	}

	// Record received chunks
	coord.RecordReceivedChunk(deviceID, 0, []byte("chunk0"))
	coord.RecordReceivedChunk(deviceID, 1, []byte("chunk1"))

	recvState = coord.GetReceiveState(deviceID)
	if recvState.ChunksReceived != 2 {
		t.Errorf("Expected 2 chunks received, got %d", recvState.ChunksReceived)
	}

	// Test failure handling
	coord.FailReceive(deviceID, "test error")
	recvState = coord.GetReceiveState(deviceID)

	if recvState != nil {
		t.Error("Expected receive state to be cleaned up after failure")
	}
}

// TestPhotoTransferTimeout verifies that stale transfers are cleaned up
func TestPhotoTransferTimeout(t *testing.T) {
	coord := NewPhotoTransferCoordinator("test-device")

	deviceID := "DEVICE123"
	photoHash := "abcd1234"

	// Start a send that will timeout
	coord.StartSend(deviceID, photoHash, 10)

	// Manually set the start time to be old (note: fields are private, so we'll just
	// start a send and immediately test cleanup with short timeout)
	time.Sleep(10 * time.Millisecond)

	// Run cleanup with very short timeout (1ms) - since we waited 10ms, it should clean up
	coord.CleanupStaleTransfers(1 * time.Millisecond)

	// Verify state was cleaned up
	sendState := coord.GetSendState(deviceID)
	if sendState != nil {
		t.Error("Expected stale send state to be cleaned up after timeout")
	}
}

// MockDevice implements the Device interface for testing
type MockDevice struct {
	hardwareUUID      string
	deviceID          string
	photoHash         string
	uuidToDeviceIDMap map[string]string
	photoCoordinator  *PhotoTransferCoordinator
	cacheManager      *DeviceCacheManager
	connManager       *MockConnManager
}

func (m *MockDevice) GetHardwareUUID() string            { return m.hardwareUUID }
func (m *MockDevice) GetDeviceID() string                { return m.deviceID }
func (m *MockDevice) GetDeviceUUID() string              { return m.hardwareUUID }
func (m *MockDevice) GetDeviceName() string              { return "TestDevice" }
func (m *MockDevice) GetPhotoHash() string               { return m.photoHash }
func (m *MockDevice) GetPhotoData() []byte               { return nil }
func (m *MockDevice) GetPlatform() string                { return "test" }
func (m *MockDevice) GetMutex() interface{}              { return nil }
func (m *MockDevice) GetUUIDToDeviceIDMap() map[string]string { return m.uuidToDeviceIDMap }
func (m *MockDevice) GetPhotoCoordinator() *PhotoTransferCoordinator { return m.photoCoordinator }
func (m *MockDevice) GetCacheManager() *DeviceCacheManager { return m.cacheManager }
func (m *MockDevice) GetConnManager() *ConnectionManager {
	// Return nil for tests - we're not testing full connection manager integration
	return nil
}
func (m *MockDevice) GetLocalProfile() *LocalProfile { return &LocalProfile{} }
func (m *MockDevice) DisconnectFromDevice(uuid string) error { return nil }
func (m *MockDevice) GetMeshView() *MeshView { return nil }

// MockConnManager implements connection manager interface for testing
type MockConnManager struct {
	sendFunc func(uuid, charUUID string, data []byte) error
}

func (m *MockConnManager) SendToDevice(uuid, charUUID string, data []byte) error {
	if m.sendFunc != nil {
		return m.sendFunc(uuid, charUUID, data)
	}
	return nil
}

func (m *MockConnManager) IsConnected(uuid string) bool { return true }
func (m *MockConnManager) RegisterCentralConnection(uuid string, conn interface{}) {}
func (m *MockConnManager) UnregisterCentralConnection(uuid string) {}
func (m *MockConnManager) RegisterPeripheralConnection(uuid string) {}
func (m *MockConnManager) UnregisterPeripheralConnection(uuid string) {}
func (m *MockConnManager) IsConnectedAsCentral(uuid string) bool { return true }
func (m *MockConnManager) GetAllConnectedUUIDs() []string { return []string{} }
