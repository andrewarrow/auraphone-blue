package phone

import (
	"crypto/sha256"
	"sync"
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
func (m *MockDevice) GetMutex() *sync.RWMutex           { return &sync.RWMutex{} }
func (m *MockDevice) GetUUIDToDeviceIDMap() map[string]string { return m.uuidToDeviceIDMap }
func (m *MockDevice) GetPhotoCoordinator() *PhotoTransferCoordinator { return m.photoCoordinator }
func (m *MockDevice) GetCacheManager() *DeviceCacheManager { return m.cacheManager }
func (m *MockDevice) GetConnManager() *ConnectionManager {
	// Create a real connection manager for testing
	cm := NewConnectionManager(m.hardwareUUID)

	// Set up send functions using our mock
	if m.connManager != nil && m.connManager.sendFunc != nil {
		cm.SetSendFunctions(m.connManager.sendFunc, m.connManager.sendFunc)
	} else {
		// Default no-op send function if none provided
		noopSend := func(uuid, charUUID string, data []byte) error { return nil }
		cm.SetSendFunctions(noopSend, noopSend)
	}

	// Register connections for all devices in our UUID map
	// This ensures SendToDevice can find a connection path
	for senderUUID := range m.uuidToDeviceIDMap {
		cm.RegisterCentralConnection(senderUUID, struct{}{})
	}

	return cm
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
func (m *MockDevice) TriggerDiscoveryUpdate(hardwareUUID, deviceID, photoHash string, photoData []byte) {}
func (m *MockDevice) GetIdentityManager() *IdentityManager { return nil }

// TestHandlePhotoChunkRaceCondition tests the race condition where the coordinator
// cleans up the receive state between RecordReceivedChunk and GetReceiveState
func TestHandlePhotoChunkRaceCondition(t *testing.T) {
	// Create a test coordinator
	coord := NewPhotoTransferCoordinator("test-uuid-12345678")

	// Create a mock device
	mockDevice := &MockDevice{
		hardwareUUID:      "test-uuid-12345678",
		deviceID:          "TESTDEV1",
		photoHash:         "testhash",
		uuidToDeviceIDMap: map[string]string{"sender-uuid": "SENDER01"},
		photoCoordinator:  coord,
		cacheManager:      nil,
		connManager:       &MockConnManager{},
	}

	// Create a photo handler
	handler := NewPhotoHandler(mockDevice)

	// Create a test photo chunk
	testPhoto := []byte("test photo data")
	photoHash := sha256.Sum256(testPhoto)
	senderUUID := "sender-uuid"
	deviceID := "SENDER01"

	// Create chunk message
	chunk, err := CreatePhotoChunk(
		deviceID,
		mockDevice.deviceID,
		photoHash[:],
		0, // first chunk
		1, // only one chunk
		testPhoto,
	)
	if err != nil {
		t.Fatalf("Failed to create chunk: %v", err)
	}

	encodedChunk, err := EncodePhotoChunk(chunk)
	if err != nil {
		t.Fatalf("Failed to encode chunk: %v", err)
	}

	// Start the receive
	coord.StartReceive(deviceID, "testhash", 1)

	// Simulate the race condition by cleaning up the state in a goroutine
	// right after HandlePhotoChunk records the chunk
	go func() {
		time.Sleep(1 * time.Millisecond)
		// Force cleanup to simulate the race condition
		coord.FailReceive(deviceID, "simulated cleanup")
	}()

	// Call HandlePhotoChunk - this should NOT crash even if state is cleaned up
	// The bug would cause a nil pointer dereference at line 183
	handler.HandlePhotoChunk(senderUUID, encodedChunk)

	// If we get here without crashing, the bug is fixed
	// The function should gracefully handle the nil state and return early
}

// TestHandlePhotoChunkNormalFlow tests the normal case where no race occurs
func TestHandlePhotoChunkNormalFlow(t *testing.T) {
	// Create a test coordinator
	coord := NewPhotoTransferCoordinator("test-uuid-87654321")

	// Create a mock device with a cache manager
	cacheDir := t.TempDir()
	cacheMgr := &DeviceCacheManager{
		baseDir: cacheDir,
	}

	mockDevice := &MockDevice{
		hardwareUUID:      "test-uuid-87654321",
		deviceID:          "TESTDEV2",
		photoHash:         "testhash2",
		uuidToDeviceIDMap: map[string]string{"sender-uuid-2": "SENDER02"},
		photoCoordinator:  coord,
		cacheManager:      cacheMgr,
		connManager:       &MockConnManager{},
	}

	// Create a photo handler
	handler := NewPhotoHandler(mockDevice)

	// Create a test photo chunk
	testPhoto := []byte("another test photo")
	photoHash := sha256.Sum256(testPhoto)
	senderUUID := "sender-uuid-2"
	deviceID := "SENDER02"

	// Create single-chunk message
	chunk, err := CreatePhotoChunk(
		deviceID,
		mockDevice.deviceID,
		photoHash[:],
		0, // first and only chunk
		1, // total chunks
		testPhoto,
	)
	if err != nil {
		t.Fatalf("Failed to create chunk: %v", err)
	}

	encodedChunk, err := EncodePhotoChunk(chunk)
	if err != nil {
		t.Fatalf("Failed to encode chunk: %v", err)
	}

	// Handle the chunk - should complete successfully
	handler.HandlePhotoChunk(senderUUID, encodedChunk)

	// Verify the receive was completed (state should be cleaned up)
	recvState := coord.GetReceiveState(deviceID)
	if recvState != nil {
		t.Errorf("Expected receive state to be cleaned up after successful completion, but it still exists")
	}

	// Verify it was marked as completed
	_, _, _, inProgressReceives := coord.GetStats()
	if inProgressReceives != 0 {
		t.Errorf("Expected 0 in-progress receives, got %d", inProgressReceives)
	}
}
