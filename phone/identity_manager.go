package phone

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// IdentityManager maintains bidirectional mapping between peripheral UUIDs and device IDs
// This is the ONLY place where this mapping should exist
//
// IMPORTANT: Two different types of "UUID" are used:
// 1. ourHardwareUUID = Simulator instance ID (only for data paths, never sent over BLE)
// 2. Peer peripheral UUIDs = CBPeripheral.identifier (for BLE routing, dictionary keys)
//
// CRITICAL RULES:
// 1. Wire layer ALWAYS uses peripheral UUIDs (never DeviceIDs)
// 2. Application layer uses DeviceIDs for display/storage/role policy
// 3. When sending: DeviceID → lookup peripheral UUID → send via wire
// 4. When receiving: peripheral UUID → lookup DeviceID → display to user
type IdentityManager struct {
	mu              sync.RWMutex
	ourHardwareUUID string // Simulator instance ID (for data directory paths only)
	ourDeviceID     string // Our Base36 device ID

	// Bidirectional mapping (THE ONLY PLACE this exists)
	hardwareToDevice map[string]string // peripheral UUID (CBPeripheral.identifier) -> Base36 device ID
	deviceToHardware map[string]string // Base36 device ID -> peripheral UUID (CBPeripheral.identifier)

	// Connection state (tracks which devices are currently reachable)
	connectedDevices map[string]bool // peripheral UUID -> is connected

	// Persistence
	statePath string
}

// IdentityMapping represents a persisted hardware UUID <-> device ID mapping
type IdentityMapping struct {
	HardwareUUID string `json:"hardware_uuid"`
	DeviceID     string `json:"device_id"`
}

// IdentityManagerState represents the complete persisted state
type IdentityManagerState struct {
	OurHardwareUUID string            `json:"our_hardware_uuid"`
	OurDeviceID     string            `json:"our_device_id"`
	Mappings        []IdentityMapping `json:"mappings"`
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

// RegisterDevice adds or updates a peripheral UUID <-> device ID mapping
// Called after handshake when we learn a peer's device ID
//
// Parameters:
//   - hardwareUUID: CBPeripheral.identifier for this peer (used for BLE routing only)
//   - deviceID: Base36 device ID from handshake (used for identity/storage/role policy)
//
// Handles two real-world scenarios:
// 1. iOS UUID rotation: Same DeviceID reconnects with new peripheral UUID
//    - Remove old peripheralUUID → DeviceID mapping before adding new one
// 2. Android device reuse: Same peripheral UUID reconnects with new DeviceID (factory reset/new owner)
//    - Remove old DeviceID → peripheralUUID mapping before adding new one
func (im *IdentityManager) RegisterDevice(hardwareUUID, deviceID string) {
	if hardwareUUID == "" || deviceID == "" {
		return
	}

	im.mu.Lock()
	defer im.mu.Unlock()

	// Check if this hardwareUUID was previously mapped to a DIFFERENT deviceID
	// (Android device reuse: same hardware, new owner/factory reset)
	if oldDeviceID, exists := im.hardwareToDevice[hardwareUUID]; exists && oldDeviceID != deviceID {
		// Remove stale reverse mapping
		delete(im.deviceToHardware, oldDeviceID)
	}

	// Check if this deviceID was previously mapped to a DIFFERENT hardwareUUID
	// (iOS privacy: same device, rotated BLE UUID)
	if oldHardwareUUID, exists := im.deviceToHardware[deviceID]; exists && oldHardwareUUID != hardwareUUID {
		// Remove stale reverse mapping
		delete(im.hardwareToDevice, oldHardwareUUID)

		// Also clean up connection state for the old UUID (it's no longer valid)
		delete(im.connectedDevices, oldHardwareUUID)
	}

	// Now add/update the bidirectional mapping
	im.hardwareToDevice[hardwareUUID] = deviceID
	im.deviceToHardware[deviceID] = hardwareUUID
}

// GetDeviceID looks up device ID from hardware UUID
// Returns ("", false) if not yet handshaked
func (im *IdentityManager) GetDeviceID(hardwareUUID string) (string, bool) {
	im.mu.RLock()
	defer im.mu.RUnlock()

	deviceID, ok := im.hardwareToDevice[hardwareUUID]
	return deviceID, ok
}

// GetHardwareUUID looks up hardware UUID from device ID
// CRITICAL: Use this before sending messages via wire!
func (im *IdentityManager) GetHardwareUUID(deviceID string) (string, bool) {
	im.mu.RLock()
	defer im.mu.RUnlock()

	hardwareUUID, ok := im.deviceToHardware[deviceID]
	return hardwareUUID, ok
}

// MarkConnected marks a device as currently connected (by hardware UUID)
func (im *IdentityManager) MarkConnected(hardwareUUID string) {
	if hardwareUUID == "" {
		return
	}

	im.mu.Lock()
	defer im.mu.Unlock()

	im.connectedDevices[hardwareUUID] = true
}

// MarkDisconnected marks a device as disconnected (by hardware UUID)
func (im *IdentityManager) MarkDisconnected(hardwareUUID string) {
	if hardwareUUID == "" {
		return
	}

	im.mu.Lock()
	defer im.mu.Unlock()

	delete(im.connectedDevices, hardwareUUID)
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

// SaveToDisk persists mappings to disk (atomic write)
func (im *IdentityManager) SaveToDisk() error {
	im.mu.RLock()
	defer im.mu.RUnlock()

	// Build list of mappings (excluding ourselves)
	mappings := []IdentityMapping{}

	for hardwareUUID, deviceID := range im.hardwareToDevice {
		// Skip our own mapping
		if hardwareUUID == im.ourHardwareUUID {
			continue
		}

		mappings = append(mappings, IdentityMapping{
			HardwareUUID: hardwareUUID,
			DeviceID:     deviceID,
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

	// Atomic write pattern: temp file + rename
	tempPath := im.statePath + ".tmp"
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tempPath, im.statePath); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// LoadFromDisk restores mappings from disk
func (im *IdentityManager) LoadFromDisk() error {
	im.mu.Lock()
	defer im.mu.Unlock()

	// Check if file exists
	if _, err := os.Stat(im.statePath); os.IsNotExist(err) {
		return nil // No saved state, starting fresh
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
		// Hardware UUID changed - don't load old mappings
		return nil
	}

	if state.OurDeviceID != im.ourDeviceID {
		// Device ID changed - don't load old mappings
		return nil
	}

	// Load mappings
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
	}

	return nil
}
