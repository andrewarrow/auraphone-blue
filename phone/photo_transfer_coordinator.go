package phone

import (
	"encoding/json"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/user/auraphone-blue/logger"
	auraphone "github.com/user/auraphone-blue/proto"
	"google.golang.org/protobuf/proto"
)

// Photo transfer protocol constants
const (
	DefaultChunkSize = 4096 // 4KB chunks (fits comfortably within BLE MTU after protobuf overhead)

	// Exponential backoff parameters
	InitialRetryDelay = 1 * time.Second  // First retry after 1s
	MaxRetryDelay     = 60 * time.Second // Cap at 60s
	MaxRetryAttempts  = 10               // Give up after 10 retries
	TransferTimeout   = 30 * time.Second // Mark transfer as stale after 30s inactivity

	// ACK/Retry protocol parameters
	ChunkAckTimeout      = 5 * time.Second  // How long to wait for ACK before retry
	MaxChunkRetries      = 5                // Maximum retry attempts per chunk
	TransferStallTimeout = 60 * time.Second // When to consider transfer stalled
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
	Paused         bool          // Transfer paused due to disconnect

	// Per-chunk ACK tracking
	ChunkAckReceived map[int]bool      // Which chunks have been ACK'd
	ChunkRetryCount  map[int]int       // How many times each chunk was sent
	ChunkLastSent    map[int]time.Time // When each chunk was last sent
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
	Paused          bool            // Transfer paused due to disconnect
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
	timelineLogger    *PhotoTimelineLogger

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
	devicePath := GetDeviceCacheDir(hardwareUUID)
	statePath := filepath.Join(devicePath, "photo_transfer_state.json")

	coordinator := &PhotoTransferCoordinator{
		hardwareUUID:       hardwareUUID,
		statePath:          statePath,
		timelineLogger:     NewPhotoTimelineLogger(hardwareUUID, true),
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

	// Already sending to this device (but allow if paused - will resume)
	if sendState, inProgress := c.inProgressSends[deviceID]; inProgress && !sendState.Paused {
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

	// Already receiving from this device (but allow if paused - will resume)
	if recvState, inProgress := c.inProgressReceives[deviceID]; inProgress && !recvState.Paused {
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

		// Initialize per-chunk tracking
		ChunkAckReceived: make(map[int]bool),
		ChunkRetryCount:  make(map[int]int),
		ChunkLastSent:    make(map[int]time.Time),
	}

	logger.Debug(c.hardwareUUID[:8], "📤 Started photo send to %s (hash: %s, chunks: %d)",
		deviceID, photoHash[:8], totalChunks)

	c.timelineLogger.LogSendStarted(deviceID, "", photoHash[:8], totalChunks)
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

	// Calculate duration before we remove from in-progress
	var durationMs int64
	if state, exists := c.inProgressSends[deviceID]; exists {
		durationMs = time.Since(state.StartTime).Milliseconds()
	}

	// Remove from in-progress
	delete(c.inProgressSends, deviceID)

	// Mark as completed
	c.completedSends[deviceID] = photoHash

	c.mu.Unlock()

	// Persist to disk
	if err := c.saveState(); err != nil {
		logger.Warn(c.hardwareUUID[:8], "Failed to save photo transfer state: %v", err)
	}

	logger.Info(c.hardwareUUID[:8], "✅ Photo send to %s completed successfully (hash: %s)",
		deviceID, photoHash[:8])

	c.timelineLogger.LogSendComplete(deviceID, "", photoHash[:8], 0, durationMs)
}

// FailSend marks a photo send as failed and cleans up state
func (c *PhotoTransferCoordinator) FailSend(deviceID string, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.inProgressSends, deviceID)

	logger.Warn(c.hardwareUUID[:8], "❌ Photo send to %s failed: %s", deviceID, reason)
}

// RecordChunkSent marks a chunk as sent and records the timestamp
func (c *PhotoTransferCoordinator) RecordChunkSent(deviceID string, chunkIndex int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if state, exists := c.inProgressSends[deviceID]; exists {
		state.ChunkLastSent[chunkIndex] = time.Now()
		state.ChunkRetryCount[chunkIndex]++
		state.LastActivity = time.Now()

		// Log chunk sent
		c.timelineLogger.LogChunkSent(deviceID, "", state.PhotoHash[:8], chunkIndex, state.TotalChunks, 0)

		// Log retry if this is not the first attempt
		if state.ChunkRetryCount[chunkIndex] > 1 {
			c.timelineLogger.LogChunkRetry(deviceID, "", state.PhotoHash[:8], chunkIndex, state.ChunkRetryCount[chunkIndex])
		}
	}
}

// MarkChunkAcked marks a chunk as acknowledged
func (c *PhotoTransferCoordinator) MarkChunkAcked(deviceID string, chunkIndex int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if state, exists := c.inProgressSends[deviceID]; exists {
		state.ChunkAckReceived[chunkIndex] = true
		state.LastActivity = time.Now()

		c.timelineLogger.LogChunkAckReceived(deviceID, "", state.PhotoHash[:8], chunkIndex)
	}
}

// GetMissingAcks returns chunks that need to be retried (not ACKed and past timeout)
func (c *PhotoTransferCoordinator) GetMissingAcks(deviceID string) []int {
	c.mu.Lock()
	defer c.mu.Unlock()

	state, exists := c.inProgressSends[deviceID]
	if !exists {
		return nil
	}

	now := time.Now()
	missingAcks := []int{}

	for i := 0; i < state.TotalChunks; i++ {
		// Skip if already ACKed
		if state.ChunkAckReceived[i] {
			continue
		}

		// Check if chunk was sent
		lastSent, wasSent := state.ChunkLastSent[i]
		if !wasSent {
			// Chunk was never sent (shouldn't happen, but handle it)
			continue
		}

		// Check if timeout has passed
		if now.Sub(lastSent) > ChunkAckTimeout {
			// Check retry count
			if state.ChunkRetryCount[i] < MaxChunkRetries {
				missingAcks = append(missingAcks, i)
			}
		}
	}

	return missingAcks
}

// GetSendState returns the current send state for a device (for external inspection)
func (c *PhotoTransferCoordinator) GetSendState(deviceID string) *SendState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inProgressSends[deviceID]
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

	c.timelineLogger.LogReceiveStarted(deviceID, "", photoHash[:8], totalChunks)
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

		c.timelineLogger.LogChunkReceived(deviceID, "", state.PhotoHash[:8], chunkIndex, state.TotalChunks, len(chunkData))
	}
}

// CompleteReceive marks a photo receive as successfully completed
func (c *PhotoTransferCoordinator) CompleteReceive(deviceID string, photoHash string) {
	c.mu.Lock()

	// Calculate duration before we remove from in-progress
	var durationMs int64
	if state, exists := c.inProgressReceives[deviceID]; exists {
		durationMs = time.Since(state.StartTime).Milliseconds()
	}

	// Remove from in-progress
	delete(c.inProgressReceives, deviceID)

	// Mark as completed
	c.completedReceives[deviceID] = photoHash

	c.mu.Unlock()

	// Persist to disk
	if err := c.saveState(); err != nil {
		logger.Warn(c.hardwareUUID[:8], "Failed to save photo transfer state: %v", err)
	}

	logger.Info(c.hardwareUUID[:8], "✅ Photo receive from %s completed successfully (hash: %s)",
		deviceID, photoHash[:8])

	c.timelineLogger.LogReceiveComplete(deviceID, "", photoHash[:8], 0, durationMs)
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

	// Pause send state (don't delete - we'll resume on reconnect)
	if sendState, exists := c.inProgressSends[deviceID]; exists {
		sendState.Paused = true
		logger.Info(c.hardwareUUID[:8],
			"⏸️  Paused photo send to %s on disconnect (sent %d/%d chunks)",
			deviceID, sendState.ChunksSent, sendState.TotalChunks)
	}

	// Pause receive state (don't delete - we'll resume on reconnect)
	if recvState, exists := c.inProgressReceives[deviceID]; exists {
		recvState.Paused = true
		logger.Info(c.hardwareUUID[:8],
			"⏸️  Paused photo receive from %s on disconnect (received %d/%d chunks)",
			deviceID, recvState.ChunksReceived, recvState.TotalChunks)
	}
}

// ResumeTransfersOnReconnect resumes any paused transfers when a device reconnects
func (c *PhotoTransferCoordinator) ResumeTransfersOnReconnect(deviceID string) (hasPausedSend bool, hasPausedReceive bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Resume send state
	if sendState, exists := c.inProgressSends[deviceID]; exists && sendState.Paused {
		sendState.Paused = false
		sendState.LastActivity = time.Now()

		// Reset chunk send times to trigger immediate retry of un-ACKed chunks
		now := time.Now().Add(-ChunkAckTimeout - 1*time.Second) // Set to past timeout
		for chunkIdx := range sendState.ChunkLastSent {
			if !sendState.ChunkAckReceived[chunkIdx] {
				sendState.ChunkLastSent[chunkIdx] = now
			}
		}

		hasPausedSend = true
		unackedCount := 0
		for i := 0; i < sendState.TotalChunks; i++ {
			if !sendState.ChunkAckReceived[i] {
				unackedCount++
			}
		}
		logger.Info(c.hardwareUUID[:8],
			"▶️  Resumed photo send to %s on reconnect (%d/%d chunks ACKed, will retry %d)",
			deviceID, sendState.TotalChunks-unackedCount, sendState.TotalChunks, unackedCount)
	}

	// Resume receive state
	if recvState, exists := c.inProgressReceives[deviceID]; exists && recvState.Paused {
		recvState.Paused = false
		recvState.LastActivity = time.Now()
		hasPausedReceive = true
		logger.Info(c.hardwareUUID[:8],
			"▶️  Resumed photo receive from %s on reconnect (received %d/%d chunks)",
			deviceID, recvState.ChunksReceived, recvState.TotalChunks)
	}

	return hasPausedSend, hasPausedReceive
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
// Photo Transfer Protocol - Protobuf-based Encoding/Decoding
// ==============================================================================

// CalculateCRC32 computes CRC32 checksum of data
func CalculateCRC32(data []byte) uint32 {
	return crc32.ChecksumIEEE(data)
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

// CreatePhotoChunk creates a protobuf PhotoChunkMessage
func CreatePhotoChunk(senderDeviceID, targetDeviceID string, photoHash []byte, chunkIndex, totalChunks int32, chunkData []byte) (*auraphone.PhotoChunkMessage, error) {
	chunkCRC := CalculateCRC32(chunkData)

	chunk := &auraphone.PhotoChunkMessage{
		SenderDeviceId: senderDeviceID,
		TargetDeviceId: targetDeviceID,
		PhotoHash:      photoHash,
		ChunkIndex:     chunkIndex,
		TotalChunks:    totalChunks,
		ChunkData:      chunkData,
		Timestamp:      time.Now().UnixNano(),
		ChunkCrc:       chunkCRC,
	}

	return chunk, nil
}

// EncodePhotoChunk marshals a PhotoChunkMessage to bytes
func EncodePhotoChunk(chunk *auraphone.PhotoChunkMessage) ([]byte, error) {
	return proto.Marshal(chunk)
}

// DecodePhotoChunk unmarshals bytes to PhotoChunkMessage and verifies CRC
func DecodePhotoChunk(data []byte) (*auraphone.PhotoChunkMessage, error) {
	var chunk auraphone.PhotoChunkMessage
	if err := proto.Unmarshal(data, &chunk); err != nil {
		return nil, fmt.Errorf("failed to unmarshal photo chunk: %w", err)
	}

	// Verify CRC
	calculatedCRC := CalculateCRC32(chunk.ChunkData)
	if calculatedCRC != chunk.ChunkCrc {
		return nil, fmt.Errorf("chunk CRC mismatch: expected %08X, got %08X", chunk.ChunkCrc, calculatedCRC)
	}

	return &chunk, nil
}

// CreatePhotoChunkAck creates a protobuf PhotoChunkAck
func CreatePhotoChunkAck(receiverDeviceID, senderDeviceID string, photoHash []byte, lastChunkReceived int32, missingChunks []int32, transferComplete bool) (*auraphone.PhotoChunkAck, error) {
	ack := &auraphone.PhotoChunkAck{
		ReceiverDeviceId:  receiverDeviceID,
		SenderDeviceId:    senderDeviceID,
		PhotoHash:         photoHash,
		LastChunkReceived: lastChunkReceived,
		MissingChunks:     missingChunks,
		TransferComplete:  transferComplete,
		Timestamp:         time.Now().UnixNano(),
	}

	return ack, nil
}

// EncodePhotoChunkAck marshals a PhotoChunkAck to bytes
func EncodePhotoChunkAck(ack *auraphone.PhotoChunkAck) ([]byte, error) {
	return proto.Marshal(ack)
}

// DecodePhotoChunkAck unmarshals bytes to PhotoChunkAck
func DecodePhotoChunkAck(data []byte) (*auraphone.PhotoChunkAck, error) {
	var ack auraphone.PhotoChunkAck
	if err := proto.Unmarshal(data, &ack); err != nil {
		return nil, fmt.Errorf("failed to unmarshal photo chunk ack: %w", err)
	}

	return &ack, nil
}
