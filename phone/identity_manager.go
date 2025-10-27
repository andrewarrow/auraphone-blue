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

// IdentityManager maintains bidirectional mapping between hardware UUIDs and device IDs
// This is the ONLY place where this mapping should exist
type IdentityManager struct {
	mu              sync.RWMutex
	ourHardwareUUID string
	ourDeviceID     string

	// Bidirectional mapping
	hardwareToDevice map[string]string // hardware UUID -> device ID
	deviceToHardware map[string]string // device ID -> hardware UUID

	// Connection state (tracks which devices are currently reachable)
	connectedDevices map[string]bool // hardware UUID -> is connected

	// Persistence
	statePath string
}

// IdentityMapping represents a persisted hardware UUID <-> device ID mapping
type IdentityMapping struct {
	HardwareUUID string `json:"hardware_uuid"`
	DeviceID     string `json:"device_id"`
	LastSeen     int64  `json:"last_seen"`
}

// IdentityManagerState represents the complete persisted state
type IdentityManagerState struct {
	OurHardwareUUID string            `json:"our_hardware_uuid"`
	OurDeviceID     string            `json:"our_device_id"`
	Mappings        []IdentityMapping `json:"mappings"`
}

// truncateForLog safely truncates a string for logging (max 8 chars)
func truncateForLog(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	if s == "" {
		return "(empty)"
	}
	return s
}

// NewIdentityManager creates a new identity manager
func NewIdentityManager(ourHardwareUUID, ourDeviceID, dataDir string) *IdentityManager {
	im := &IdentityManager{
		ourHardwareUUID:  ourHardwareUUID,
		ourDeviceID:      ourDeviceID,
		hardwareToDevice: make(map[string]string),
		deviceToHardware: make(map[string]string),
		connectedDevices: make(map[string]bool),
		statePath:        filepath.Join(dataDir, "identity_mappings.json"),
	}

	// Always register ourselves
	im.hardwareToDevice[ourHardwareUUID] = ourDeviceID
	im.deviceToHardware[ourDeviceID] = ourHardwareUUID

	return im
}

// RegisterDevice adds or updates a hardware UUID <-> device ID mapping
func (im *IdentityManager) RegisterDevice(hardwareUUID, deviceID string) {
	if hardwareUUID == "" || deviceID == "" {
		return
	}

	im.mu.Lock()
	defer im.mu.Unlock()

	// Check if this is a new mapping or an update
	existingDeviceID, hadHardware := im.hardwareToDevice[hardwareUUID]
	existingHardwareUUID, hadDevice := im.deviceToHardware[deviceID]

	// Update bidirectional mapping
	im.hardwareToDevice[hardwareUUID] = deviceID
	im.deviceToHardware[deviceID] = hardwareUUID

	// Clean up old mappings if hardware UUID or device ID changed
	if hadHardware && existingDeviceID != deviceID {
		delete(im.deviceToHardware, existingDeviceID)
		logger.Debug(truncateForLog(im.ourHardwareUUID), "Identity mapping updated: hardware %s now maps to device %s (was %s)",
			truncateForLog(hardwareUUID), truncateForLog(deviceID), truncateForLog(existingDeviceID))
	}
	if hadDevice && existingHardwareUUID != hardwareUUID {
		delete(im.hardwareToDevice, existingHardwareUUID)
		logger.Debug(truncateForLog(im.ourHardwareUUID), "Identity mapping updated: device %s now maps to hardware %s (was %s)",
			truncateForLog(deviceID), truncateForLog(hardwareUUID), truncateForLog(existingHardwareUUID))
	}
}

// GetDeviceID looks up device ID from hardware UUID
func (im *IdentityManager) GetDeviceID(hardwareUUID string) (string, bool) {
	im.mu.RLock()
	defer im.mu.RUnlock()

	deviceID, ok := im.hardwareToDevice[hardwareUUID]
	return deviceID, ok
}

// GetHardwareUUID looks up hardware UUID from device ID
func (im *IdentityManager) GetHardwareUUID(deviceID string) (string, bool) {
	im.mu.RLock()
	defer im.mu.RUnlock()

	hardwareUUID, ok := im.deviceToHardware[deviceID]
	return hardwareUUID, ok
}

// MarkConnected marks a device as currently connected
func (im *IdentityManager) MarkConnected(hardwareUUID string) {
	if hardwareUUID == "" {
		return
	}

	im.mu.Lock()
	defer im.mu.Unlock()

	wasConnected := im.connectedDevices[hardwareUUID]
	im.connectedDevices[hardwareUUID] = true

	if !wasConnected {
		deviceID := im.hardwareToDevice[hardwareUUID]
		if deviceID == "" {
			deviceID = "(unknown)"
		}
		logger.Debug(truncateForLog(im.ourHardwareUUID), "Device marked as connected: hardware=%s device=%s",
			truncateForLog(hardwareUUID), truncateForLog(deviceID))
	}
}

// MarkDisconnected marks a device as disconnected
func (im *IdentityManager) MarkDisconnected(hardwareUUID string) {
	if hardwareUUID == "" {
		return
	}

	im.mu.Lock()
	defer im.mu.Unlock()

	wasConnected := im.connectedDevices[hardwareUUID]
	delete(im.connectedDevices, hardwareUUID)

	if wasConnected {
		deviceID := im.hardwareToDevice[hardwareUUID]
		if deviceID == "" {
			deviceID = "(unknown)"
		}
		logger.Debug(truncateForLog(im.ourHardwareUUID), "Device marked as disconnected: hardware=%s device=%s",
			truncateForLog(hardwareUUID), truncateForLog(deviceID))
	}
}

// IsConnected checks if a device is currently reachable by hardware UUID
func (im *IdentityManager) IsConnected(hardwareUUID string) bool {
	im.mu.RLock()
	defer im.mu.RUnlock()

	return im.connectedDevices[hardwareUUID]
}

// IsConnectedByDeviceID checks if a device is reachable by device ID
func (im *IdentityManager) IsConnectedByDeviceID(deviceID string) bool {
	im.mu.RLock()
	defer im.mu.RUnlock()

	hardwareUUID, ok := im.deviceToHardware[deviceID]
	if !ok {
		return false
	}

	return im.connectedDevices[hardwareUUID]
}

// GetAllConnectedDevices returns list of connected device IDs
func (im *IdentityManager) GetAllConnectedDevices() []string {
	im.mu.RLock()
	defer im.mu.RUnlock()

	connectedDeviceIDs := []string{}
	for hardwareUUID := range im.connectedDevices {
		if deviceID, ok := im.hardwareToDevice[hardwareUUID]; ok {
			connectedDeviceIDs = append(connectedDeviceIDs, deviceID)
		}
	}

	return connectedDeviceIDs
}

// GetAllKnownDevices returns all device IDs we've ever seen
func (im *IdentityManager) GetAllKnownDevices() []string {
	im.mu.RLock()
	defer im.mu.RUnlock()

	deviceIDs := make([]string, 0, len(im.deviceToHardware))
	for deviceID := range im.deviceToHardware {
		// Skip ourselves
		if deviceID != im.ourDeviceID {
			deviceIDs = append(deviceIDs, deviceID)
		}
	}

	return deviceIDs
}

// GetOurDeviceID returns our own device ID
func (im *IdentityManager) GetOurDeviceID() string {
	im.mu.RLock()
	defer im.mu.RUnlock()
	return im.ourDeviceID
}

// GetOurHardwareUUID returns our own hardware UUID
func (im *IdentityManager) GetOurHardwareUUID() string {
	im.mu.RLock()
	defer im.mu.RUnlock()
	return im.ourHardwareUUID
}

// SaveToDisk persists mappings to disk
func (im *IdentityManager) SaveToDisk() error {
	im.mu.RLock()
	defer im.mu.RUnlock()

	// Build list of mappings (excluding ourselves)
	mappings := []IdentityMapping{}
	now := time.Now().Unix()

	for hardwareUUID, deviceID := range im.hardwareToDevice {
		// Skip our own mapping
		if hardwareUUID == im.ourHardwareUUID {
			continue
		}

		mappings = append(mappings, IdentityMapping{
			HardwareUUID: hardwareUUID,
			DeviceID:     deviceID,
			LastSeen:     now,
		})
	}

	state := IdentityManagerState{
		OurHardwareUUID: im.ourHardwareUUID,
		OurDeviceID:     im.ourDeviceID,
		Mappings:        mappings,
	}

	// Ensure directory exists
	dir := filepath.Dir(im.statePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Write to temp file first (atomic write pattern)
	tempPath := im.statePath + ".tmp"
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tempPath, im.statePath); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	logger.Debug(truncateForLog(im.ourHardwareUUID), "Identity mappings saved: %d devices", len(mappings))
	return nil
}

// LoadFromDisk restores mappings from disk
func (im *IdentityManager) LoadFromDisk() error {
	im.mu.Lock()
	defer im.mu.Unlock()

	// Check if file exists
	if _, err := os.Stat(im.statePath); os.IsNotExist(err) {
		logger.Debug(truncateForLog(im.ourHardwareUUID), "No identity mappings file found, starting fresh")
		return nil
	}

	// Read file
	data, err := os.ReadFile(im.statePath)
	if err != nil {
		return fmt.Errorf("failed to read state file: %w", err)
	}

	// Parse JSON
	var state IdentityManagerState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("failed to unmarshal state: %w", err)
	}

	// Verify our identity matches
	if state.OurHardwareUUID != im.ourHardwareUUID {
		logger.Warn(truncateForLog(im.ourHardwareUUID), "Hardware UUID mismatch in saved state: expected=%s got=%s",
			truncateForLog(im.ourHardwareUUID), truncateForLog(state.OurHardwareUUID))
		// Don't load mappings if our identity changed
		return nil
	}

	if state.OurDeviceID != im.ourDeviceID {
		logger.Warn(truncateForLog(im.ourHardwareUUID), "Device ID mismatch in saved state: expected=%s got=%s",
			truncateForLog(im.ourDeviceID), truncateForLog(state.OurDeviceID))
		// Don't load mappings if our identity changed
		return nil
	}

	// Load mappings
	loaded := 0
	for _, mapping := range state.Mappings {
		if mapping.HardwareUUID == "" || mapping.DeviceID == "" {
			continue
		}

		// Skip if this is our own identity
		if mapping.HardwareUUID == im.ourHardwareUUID {
			continue
		}

		im.hardwareToDevice[mapping.HardwareUUID] = mapping.DeviceID
		im.deviceToHardware[mapping.DeviceID] = mapping.HardwareUUID
		loaded++
	}

	logger.Debug(truncateForLog(im.ourHardwareUUID), "Identity mappings loaded: %d devices", loaded)
	return nil
}

// GetMappingCount returns the number of known device mappings (excluding ourselves)
func (im *IdentityManager) GetMappingCount() int {
	im.mu.RLock()
	defer im.mu.RUnlock()

	// Subtract 1 for our own mapping
	count := len(im.hardwareToDevice)
	if _, hasOurMapping := im.hardwareToDevice[im.ourHardwareUUID]; hasOurMapping {
		count--
	}

	return count
}

// GetConnectedCount returns the number of currently connected devices
func (im *IdentityManager) GetConnectedCount() int {
	im.mu.RLock()
	defer im.mu.RUnlock()

	return len(im.connectedDevices)
}
