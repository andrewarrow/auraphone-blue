package phone

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/user/auraphone-blue/logger"
)

// SendState tracks an in-progress photo send operation
type SendState struct {
	DeviceID      string
	PhotoHash     string
	StartTime     time.Time
	LastActivity  time.Time
	ChunksSent    int
	TotalChunks   int
}

// ReceiveState tracks an in-progress photo receive operation
type ReceiveState struct {
	DeviceID       string
	PhotoHash      string
	StartTime      time.Time
	LastActivity   time.Time
	ChunksReceived int
	TotalChunks    int
	Buffer         []byte
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
	}

	// Load persistent state from disk
	coordinator.loadState()

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
		DeviceID:     deviceID,
		PhotoHash:    photoHash,
		StartTime:    now,
		LastActivity: now,
		ChunksSent:   0,
		TotalChunks:  totalChunks,
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
		DeviceID:       deviceID,
		PhotoHash:      photoHash,
		StartTime:      now,
		LastActivity:   now,
		ChunksReceived: 0,
		TotalChunks:    totalChunks,
		Buffer:         make([]byte, 0),
	}

	logger.Debug(c.hardwareUUID[:8], "📥 Started photo receive from %s (hash: %s, chunks: %d)",
		deviceID, photoHash[:8], totalChunks)
}

// UpdateReceiveProgress updates the last activity time, chunk count, and buffer for a receive
func (c *PhotoTransferCoordinator) UpdateReceiveProgress(deviceID string, chunksReceived int, buffer []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if state, exists := c.inProgressReceives[deviceID]; exists {
		state.LastActivity = time.Now()
		state.ChunksReceived = chunksReceived
		state.Buffer = buffer
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
