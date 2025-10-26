package phone

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/user/auraphone-blue/logger"
)

// Photo transfer protocol constants
const (
	DefaultChunkSize = 4096 // 4KB chunks (larger than old 502 byte chunks for efficiency)

	// Metadata packet magic bytes
	MetadataSize   = 14
	MagicByte0     = 0xDE
	MagicByte1     = 0xAD
	MagicByte2     = 0xBE
	MagicByte3     = 0xEF

	// Acknowledgment magic bytes
	AckMagicByte0 = 0xAC
	AckMagicByte1 = 0xCE
	AckMagicByte2 = 0x55
	AckMagicByte3 = 0xED

	// Retransmit request magic bytes
	RetransMagicByte0 = 0x7E
	RetransMagicByte1 = 0x7F
	RetransMagicByte2 = 0x12
	RetransMagicByte3 = 0x34

	ChunkHeaderSize = 8

	// Exponential backoff parameters
	InitialRetryDelay = 1 * time.Second  // First retry after 1s
	MaxRetryDelay     = 60 * time.Second // Cap at 60s
	MaxRetryAttempts  = 10               // Give up after 10 retries
	TransferTimeout   = 30 * time.Second // Mark transfer as stale after 30s inactivity
)

// SendState tracks an in-progress photo send operation
type SendState struct {
	DeviceID       string
	PhotoHash      string
	StartTime      time.Time
	LastActivity   time.Time
	ChunksSent     int
	TotalChunks    int
	SentChunks     map[int]bool  // Track which chunks have been sent
	RetryAttempts  int           // Number of times we've retried this transfer
	NextRetryTime  time.Time     // When we can retry again (for exponential backoff)
}

// ReceiveState tracks an in-progress photo receive operation
type ReceiveState struct {
	DeviceID        string
	PhotoHash       string
	StartTime       time.Time
	LastActivity    time.Time
	ChunksReceived  int
	TotalChunks     int
	ReceivedChunks  map[int][]byte  // Map of chunk index to chunk data
	MissingChunks   []int           // List of chunks we're still waiting for
}

// PhotoTransferState persists to disk to survive restarts
type PhotoTransferState struct {
	CompletedSends    map[string]string `json:"completed_sends"`    // deviceID -> hash we sent to them
	CompletedReceives map[string]string `json:"completed_receives"` // deviceID -> hash we received from them
	LastUpdated       time.Time         `json:"last_updated"`
}

// PhotoTransferCoordinator manages all photo transfer state in one place
// This is the single source of truth for photo transfer status
type PhotoTransferCoordinator struct {
	mu                sync.Mutex
	hardwareUUID      string
	statePath         string

	// Persistent state (saved to disk)
	completedSends    map[string]string  // deviceID -> hash of photo we successfully sent to them
	completedReceives map[string]string  // deviceID -> hash of photo we successfully received from them

	// Transient state (only in memory)
	inProgressSends   map[string]*SendState    // deviceID -> current send state
	inProgressReceives map[string]*ReceiveState // deviceID -> current receive state

	// Background monitoring
	stopMonitor       chan struct{}
	monitorWg         sync.WaitGroup
}

// NewPhotoTransferCoordinator creates a new coordinator and loads persistent state from disk
func NewPhotoTransferCoordinator(hardwareUUID string) *PhotoTransferCoordinator {
	devicePath := filepath.Join("data", hardwareUUID, "cache")
	statePath := filepath.Join(devicePath, "photo_transfer_state.json")

	coordinator := &PhotoTransferCoordinator{
		hardwareUUID:       hardwareUUID,
		statePath:          statePath,
		completedSends:     make(map[string]string),
		completedReceives:  make(map[string]string),
		inProgressSends:    make(map[string]*SendState),
		inProgressReceives: make(map[string]*ReceiveState),
		stopMonitor:        make(chan struct{}),
	}

	// Load persistent state from disk
	coordinator.loadState()

	// Start background timeout monitor
	coordinator.startTimeoutMonitor()

	return coordinator
}

// loadState loads persistent state from disk
func (c *PhotoTransferCoordinator) loadState() {
	data, err := os.ReadFile(c.statePath)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Warn(c.hardwareUUID[:8], "Failed to load photo transfer state: %v", err)
		}
		return
	}

	var state PhotoTransferState
	if err := json.Unmarshal(data, &state); err != nil {
		logger.Warn(c.hardwareUUID[:8], "Failed to parse photo transfer state: %v", err)
		return
	}

	c.completedSends = state.CompletedSends
	c.completedReceives = state.CompletedReceives

	logger.Debug(c.hardwareUUID[:8], "📋 Loaded photo transfer state: %d completed sends, %d completed receives",
		len(c.completedSends), len(c.completedReceives))
}

// saveState persists state to disk
func (c *PhotoTransferCoordinator) saveState() error {
	state := PhotoTransferState{
		CompletedSends:    c.completedSends,
		CompletedReceives: c.completedReceives,
		LastUpdated:       time.Now(),
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(c.statePath), 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	if err := os.WriteFile(c.statePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write state: %w", err)
	}

	return nil
}

// ShouldSendPhoto returns true if we should send our photo to this device
// Checks if they already have the current version
func (c *PhotoTransferCoordinator) ShouldSendPhoto(deviceID string, ourPhotoHash string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	// No photo to send
	if ourPhotoHash == "" {
		return false
	}

	// Already sending to this device
	if _, inProgress := c.inProgressSends[deviceID]; inProgress {
		return false
	}

	// Check if we already successfully sent this version to them
	if completedHash, exists := c.completedSends[deviceID]; exists && completedHash == ourPhotoHash {
		return false
	}

	// Need to send
	return true
}

// ShouldReceivePhoto returns true if we should receive a photo from this device
// Checks if we already have the version they're offering
func (c *PhotoTransferCoordinator) ShouldReceivePhoto(deviceID string, theirPhotoHash string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	// No photo being offered
	if theirPhotoHash == "" {
		return false
	}

	// Already receiving from this device
	if _, inProgress := c.inProgressReceives[deviceID]; inProgress {
		return false
	}

	// Check if we already successfully received this version from them
	if completedHash, exists := c.completedReceives[deviceID]; exists && completedHash == theirPhotoHash {
		return false
	}

	// Need to receive
	return true
}

// StartSend marks a photo send as in-progress
func (c *PhotoTransferCoordinator) StartSend(deviceID string, photoHash string, totalChunks int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	c.inProgressSends[deviceID] = &SendState{
		DeviceID:      deviceID,
		PhotoHash:     photoHash,
		StartTime:     now,
		LastActivity:  now,
		ChunksSent:    0,
		TotalChunks:   totalChunks,
		SentChunks:    make(map[int]bool),
		RetryAttempts: 0,
		NextRetryTime: now,
	}

	logger.Debug(c.hardwareUUID[:8], "📤 Started photo send to %s (hash: %s, chunks: %d)",
		deviceID, photoHash[:8], totalChunks)
}

// UpdateSendProgress updates the last activity time and chunk count for a send
func (c *PhotoTransferCoordinator) UpdateSendProgress(deviceID string, chunksSent int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if state, exists := c.inProgressSends[deviceID]; exists {
		state.LastActivity = time.Now()
		state.ChunksSent = chunksSent
	}
}

// CompleteSend marks a photo send as successfully completed
func (c *PhotoTransferCoordinator) CompleteSend(deviceID string, photoHash string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Remove from in-progress
	delete(c.inProgressSends, deviceID)

	// Mark as completed
	c.completedSends[deviceID] = photoHash

	// Persist to disk
	if err := c.saveState(); err != nil {
		logger.Warn(c.hardwareUUID[:8], "Failed to save photo transfer state: %v", err)
	}

	logger.Info(c.hardwareUUID[:8], "✅ Photo send to %s completed successfully (hash: %s)",
		deviceID, photoHash[:8])
}

// FailSend marks a photo send as failed and cleans up state
func (c *PhotoTransferCoordinator) FailSend(deviceID string, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.inProgressSends, deviceID)

	logger.Warn(c.hardwareUUID[:8], "❌ Photo send to %s failed: %s", deviceID, reason)
}

// StartReceive marks a photo receive as in-progress
func (c *PhotoTransferCoordinator) StartReceive(deviceID string, photoHash string, totalChunks int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	c.inProgressReceives[deviceID] = &ReceiveState{
		DeviceID:        deviceID,
		PhotoHash:       photoHash,
		StartTime:       now,
		LastActivity:    now,
		ChunksReceived:  0,
		TotalChunks:     totalChunks,
		ReceivedChunks:  make(map[int][]byte),
		MissingChunks:   make([]int, 0),
	}

	logger.Debug(c.hardwareUUID[:8], "📥 Started photo receive from %s (hash: %s, chunks: %d)",
		deviceID, photoHash[:8], totalChunks)
}

// UpdateReceiveProgress updates the last activity time and chunk count for a receive
func (c *PhotoTransferCoordinator) UpdateReceiveProgress(deviceID string, chunksReceived int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if state, exists := c.inProgressReceives[deviceID]; exists {
		state.LastActivity = time.Now()
		state.ChunksReceived = chunksReceived
	}
}

// RecordReceivedChunk stores a received chunk and updates missing chunks list
func (c *PhotoTransferCoordinator) RecordReceivedChunk(deviceID string, chunkIndex int, chunkData []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if state, exists := c.inProgressReceives[deviceID]; exists {
		state.ReceivedChunks[chunkIndex] = chunkData
		state.LastActivity = time.Now()
		state.ChunksReceived = len(state.ReceivedChunks)

		// Update missing chunks list
		state.MissingChunks = []int{}
		for i := 0; i < state.TotalChunks; i++ {
			if _, hasChunk := state.ReceivedChunks[i]; !hasChunk {
				state.MissingChunks = append(state.MissingChunks, i)
			}
		}
	}
}

// CompleteReceive marks a photo receive as successfully completed
func (c *PhotoTransferCoordinator) CompleteReceive(deviceID string, photoHash string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Remove from in-progress
	delete(c.inProgressReceives, deviceID)

	// Mark as completed
	c.completedReceives[deviceID] = photoHash

	// Persist to disk
	if err := c.saveState(); err != nil {
		logger.Warn(c.hardwareUUID[:8], "Failed to save photo transfer state: %v", err)
	}

	logger.Info(c.hardwareUUID[:8], "✅ Photo receive from %s completed successfully (hash: %s)",
		deviceID, photoHash[:8])
}

// FailReceive marks a photo receive as failed and cleans up state
func (c *PhotoTransferCoordinator) FailReceive(deviceID string, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.inProgressReceives, deviceID)

	logger.Warn(c.hardwareUUID[:8], "❌ Photo receive from %s failed: %s", deviceID, reason)
}

// CleanupStaleTransfers removes transfers that have been idle for too long
// Returns the number of transfers cleaned up
func (c *PhotoTransferCoordinator) CleanupStaleTransfers(timeout time.Duration) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	cleaned := 0

	// Clean up stale sends
	for deviceID, state := range c.inProgressSends {
		if now.Sub(state.LastActivity) > timeout {
			logger.Warn(c.hardwareUUID[:8],
				"🧹 Cleaning up stale photo send to %s (idle for %v, sent %d/%d chunks)",
				deviceID, now.Sub(state.LastActivity), state.ChunksSent, state.TotalChunks)
			delete(c.inProgressSends, deviceID)
			cleaned++
		}
	}

	// Clean up stale receives
	for deviceID, state := range c.inProgressReceives {
		if now.Sub(state.LastActivity) > timeout {
			logger.Warn(c.hardwareUUID[:8],
				"🧹 Cleaning up stale photo receive from %s (idle for %v, received %d/%d chunks)",
				deviceID, now.Sub(state.LastActivity), state.ChunksReceived, state.TotalChunks)
			delete(c.inProgressReceives, deviceID)
			cleaned++
		}
	}

	return cleaned
}

// GetReceiveState returns the current receive state for a device (or nil if not receiving)
func (c *PhotoTransferCoordinator) GetReceiveState(deviceID string) *ReceiveState {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.inProgressReceives[deviceID]
}

// GetSendState returns the current send state for a device (or nil if not sending)
func (c *PhotoTransferCoordinator) GetSendState(deviceID string) *SendState {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.inProgressSends[deviceID]
}

// GetStats returns statistics about photo transfers
func (c *PhotoTransferCoordinator) GetStats() (completedSends, completedReceives, inProgressSends, inProgressReceives int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.completedSends), len(c.completedReceives), len(c.inProgressSends), len(c.inProgressReceives)
}

// InvalidateSend clears the completed send record for a device (used when our photo changes)
func (c *PhotoTransferCoordinator) InvalidateSend(deviceID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.completedSends, deviceID)

	// Persist to disk
	if err := c.saveState(); err != nil {
		logger.Warn(c.hardwareUUID[:8], "Failed to save photo transfer state: %v", err)
	}

	logger.Debug(c.hardwareUUID[:8], "🔄 Invalidated send record for %s (our photo changed)", deviceID)
}

// InvalidateAllSends clears all completed send records (used when our photo changes)
func (c *PhotoTransferCoordinator) InvalidateAllSends() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.completedSends = make(map[string]string)

	// Persist to disk
	if err := c.saveState(); err != nil {
		logger.Warn(c.hardwareUUID[:8], "Failed to save photo transfer state: %v", err)
	}

	logger.Debug(c.hardwareUUID[:8], "🔄 Invalidated all send records (our photo changed)")
}

// CalculateRetryDelay calculates exponential backoff delay for a transfer
// Returns the delay duration and whether we should give up (exceeded max retries)
func (c *PhotoTransferCoordinator) CalculateRetryDelay(retryAttempts int) (time.Duration, bool) {
	if retryAttempts >= MaxRetryAttempts {
		return 0, true // Give up
	}

	// Exponential backoff: 1s, 2s, 4s, 8s, 16s, 32s, 60s (capped)
	delay := InitialRetryDelay * time.Duration(1<<uint(retryAttempts))
	if delay > MaxRetryDelay {
		delay = MaxRetryDelay
	}

	return delay, false
}

// CanRetry checks if a transfer can be retried (based on backoff timing)
func (c *PhotoTransferCoordinator) CanRetry(deviceID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	state, exists := c.inProgressSends[deviceID]
	if !exists {
		return true // No send in progress, can start new one
	}

	return time.Now().After(state.NextRetryTime)
}

// IncrementRetryAttempt increments the retry counter and calculates next retry time
// Returns false if max retries exceeded
func (c *PhotoTransferCoordinator) IncrementRetryAttempt(deviceID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	state, exists := c.inProgressSends[deviceID]
	if !exists {
		return false
	}

	state.RetryAttempts++
	delay, giveUp := c.CalculateRetryDelay(state.RetryAttempts)

	if giveUp {
		logger.Warn(c.hardwareUUID[:8],
			"❌ Photo send to %s exceeded max retries (%d attempts), giving up",
			deviceID, state.RetryAttempts)
		delete(c.inProgressSends, deviceID)
		return false
	}

	state.NextRetryTime = time.Now().Add(delay)
	logger.Debug(c.hardwareUUID[:8],
		"🔄 Photo send to %s will retry in %v (attempt %d/%d)",
		deviceID, delay, state.RetryAttempts, MaxRetryAttempts)

	return true
}

// CleanupDisconnectedDevice cleans up all transfer state for a disconnected device
func (c *PhotoTransferCoordinator) CleanupDisconnectedDevice(deviceID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Clean up send state
	if sendState, exists := c.inProgressSends[deviceID]; exists {
		logger.Debug(c.hardwareUUID[:8],
			"🧹 Cleaning up photo send to %s on disconnect (sent %d/%d chunks)",
			deviceID, sendState.ChunksSent, sendState.TotalChunks)
		delete(c.inProgressSends, deviceID)
	}

	// Clean up receive state
	if recvState, exists := c.inProgressReceives[deviceID]; exists {
		logger.Debug(c.hardwareUUID[:8],
			"🧹 Cleaning up photo receive from %s on disconnect (received %d/%d chunks)",
			deviceID, recvState.ChunksReceived, recvState.TotalChunks)
		delete(c.inProgressReceives, deviceID)
	}
}

// startTimeoutMonitor starts a background goroutine that periodically checks for stalled transfers
func (c *PhotoTransferCoordinator) startTimeoutMonitor() {
	c.monitorWg.Add(1)
	go func() {
		defer c.monitorWg.Done()

		ticker := time.NewTicker(10 * time.Second) // Check every 10 seconds
		defer ticker.Stop()

		for {
			select {
			case <-c.stopMonitor:
				return
			case <-ticker.C:
				// Clean up transfers that have been idle for too long
				cleaned := c.CleanupStaleTransfers(TransferTimeout)
				if cleaned > 0 {
					logger.Debug(c.hardwareUUID[:8],
						"🧹 Timeout monitor cleaned up %d stale transfers", cleaned)
				}
			}
		}
	}()

	logger.Debug(c.hardwareUUID[:8], "🔍 Started photo transfer timeout monitor")
}

// Shutdown stops the background timeout monitor and waits for it to finish
func (c *PhotoTransferCoordinator) Shutdown() {
	close(c.stopMonitor)
	c.monitorWg.Wait()
	logger.Debug(c.hardwareUUID[:8], "🛑 Photo transfer coordinator shutdown complete")
}

// ==============================================================================
// Photo Transfer Protocol - Encoding/Decoding Functions
// ==============================================================================

// MetadataPacket represents the initial photo transfer metadata
type MetadataPacket struct {
	TotalSize   uint32
	TotalCRC    uint32
	TotalChunks uint16
}

// ChunkPacket represents a single photo data chunk
type ChunkPacket struct {
	Index uint16
	Size  uint16
	CRC   uint32
	Data  []byte
}

// CalculateCRC32 computes CRC32 checksum of data
func CalculateCRC32(data []byte) uint32 {
	return crc32.ChecksumIEEE(data)
}

// EncodeMetadata creates a metadata packet with optional first chunk data
func EncodeMetadata(totalSize uint32, totalCRC uint32, totalChunks uint16, firstChunkData []byte) []byte {
	packet := make([]byte, MetadataSize+len(firstChunkData))

	// Magic bytes
	packet[0] = MagicByte0
	packet[1] = MagicByte1
	packet[2] = MagicByte2
	packet[3] = MagicByte3

	// Total size (little-endian)
	binary.LittleEndian.PutUint32(packet[4:8], totalSize)

	// Total CRC (little-endian)
	binary.LittleEndian.PutUint32(packet[8:12], totalCRC)

	// Total chunks (little-endian)
	binary.LittleEndian.PutUint16(packet[12:14], totalChunks)

	// Optional first chunk data
	if len(firstChunkData) > 0 {
		copy(packet[14:], firstChunkData)
	}

	return packet
}

// DecodeMetadata parses a metadata packet
func DecodeMetadata(data []byte) (*MetadataPacket, []byte, error) {
	if len(data) < MetadataSize {
		return nil, nil, fmt.Errorf("data too short for metadata: %d bytes", len(data))
	}

	// Check magic bytes
	if data[0] != MagicByte0 || data[1] != MagicByte1 ||
		data[2] != MagicByte2 || data[3] != MagicByte3 {
		return nil, nil, fmt.Errorf("invalid magic bytes")
	}

	meta := &MetadataPacket{
		TotalSize:   binary.LittleEndian.Uint32(data[4:8]),
		TotalCRC:    binary.LittleEndian.Uint32(data[8:12]),
		TotalChunks: binary.LittleEndian.Uint16(data[12:14]),
	}

	// Return remaining data as part of first chunk
	remainingData := []byte{}
	if len(data) > MetadataSize {
		remainingData = data[MetadataSize:]
	}

	return meta, remainingData, nil
}

// EncodeChunk creates a chunk packet
func EncodeChunk(index uint16, data []byte) []byte {
	chunkSize := uint16(len(data))
	chunkCRC := CalculateCRC32(data)

	packet := make([]byte, ChunkHeaderSize+len(data))

	// Chunk index (little-endian)
	binary.LittleEndian.PutUint16(packet[0:2], index)

	// Chunk size (little-endian)
	binary.LittleEndian.PutUint16(packet[2:4], chunkSize)

	// Chunk CRC (little-endian)
	binary.LittleEndian.PutUint32(packet[4:8], chunkCRC)

	// Chunk data
	copy(packet[8:], data)

	return packet
}

// DecodeChunk parses a chunk packet
func DecodeChunk(data []byte) (*ChunkPacket, int, error) {
	if len(data) < ChunkHeaderSize {
		return nil, 0, fmt.Errorf("data too short for chunk header: %d bytes", len(data))
	}

	chunk := &ChunkPacket{
		Index: binary.LittleEndian.Uint16(data[0:2]),
		Size:  binary.LittleEndian.Uint16(data[2:4]),
		CRC:   binary.LittleEndian.Uint32(data[4:8]),
	}

	totalChunkSize := ChunkHeaderSize + int(chunk.Size)

	if len(data) < totalChunkSize {
		return nil, 0, fmt.Errorf("data too short for chunk: have %d, need %d", len(data), totalChunkSize)
	}

	chunk.Data = data[8:totalChunkSize]

	// Verify CRC
	calculatedCRC := CalculateCRC32(chunk.Data)
	if calculatedCRC != chunk.CRC {
		return nil, 0, fmt.Errorf("chunk CRC mismatch: expected %08X, got %08X", chunk.CRC, calculatedCRC)
	}

	return chunk, totalChunkSize, nil
}

// SplitIntoChunks splits photo data into chunks
func SplitIntoChunks(data []byte, chunkSize int) [][]byte {
	var chunks [][]byte

	for offset := 0; offset < len(data); offset += chunkSize {
		end := offset + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunks = append(chunks, data[offset:end])
	}

	return chunks
}

// EncodeAck creates a transfer completion acknowledgment
// Format: [AC CE 55 ED] [CRC32:4bytes]
func EncodeAck(totalCRC uint32) []byte {
	packet := make([]byte, 8)
	packet[0] = AckMagicByte0
	packet[1] = AckMagicByte1
	packet[2] = AckMagicByte2
	packet[3] = AckMagicByte3
	binary.LittleEndian.PutUint32(packet[4:8], totalCRC)
	return packet
}

// DecodeAck parses an acknowledgment packet
func DecodeAck(data []byte) (uint32, error) {
	if len(data) < 8 {
		return 0, fmt.Errorf("data too short for ack: %d bytes", len(data))
	}
	if data[0] != AckMagicByte0 || data[1] != AckMagicByte1 ||
		data[2] != AckMagicByte2 || data[3] != AckMagicByte3 {
		return 0, fmt.Errorf("invalid ack magic bytes")
	}
	crc := binary.LittleEndian.Uint32(data[4:8])
	return crc, nil
}

// EncodeRetransmitRequest creates a request for missing chunks
// Format: [7E 7F 12 34] [ChunkCount:2bytes] [ChunkIndex:2bytes] [ChunkIndex:2bytes] ...
func EncodeRetransmitRequest(missingChunks []uint16) []byte {
	packet := make([]byte, 6+len(missingChunks)*2)
	packet[0] = RetransMagicByte0
	packet[1] = RetransMagicByte1
	packet[2] = RetransMagicByte2
	packet[3] = RetransMagicByte3
	binary.LittleEndian.PutUint16(packet[4:6], uint16(len(missingChunks)))

	for i, chunkIdx := range missingChunks {
		offset := 6 + i*2
		binary.LittleEndian.PutUint16(packet[offset:offset+2], chunkIdx)
	}
	return packet
}

// DecodeRetransmitRequest parses a retransmit request packet
func DecodeRetransmitRequest(data []byte) ([]uint16, error) {
	if len(data) < 6 {
		return nil, fmt.Errorf("data too short for retransmit request: %d bytes", len(data))
	}
	if data[0] != RetransMagicByte0 || data[1] != RetransMagicByte1 ||
		data[2] != RetransMagicByte2 || data[3] != RetransMagicByte3 {
		return nil, fmt.Errorf("invalid retransmit request magic bytes")
	}

	chunkCount := binary.LittleEndian.Uint16(data[4:6])
	expectedLen := 6 + int(chunkCount)*2
	if len(data) < expectedLen {
		return nil, fmt.Errorf("data too short for chunk indices: have %d, need %d", len(data), expectedLen)
	}

	missingChunks := make([]uint16, chunkCount)
	for i := 0; i < int(chunkCount); i++ {
		offset := 6 + i*2
		missingChunks[i] = binary.LittleEndian.Uint16(data[offset : offset+2])
	}

	return missingChunks, nil
}
