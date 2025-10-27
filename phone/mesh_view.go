package phone

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/user/auraphone-blue/logger"
	pb "github.com/user/auraphone-blue/proto"
)

// MeshDeviceState represents what we know about a single device in the mesh
type MeshDeviceState struct {
	DeviceID         string    `json:"device_id"`
	PhotoHash        string    `json:"photo_hash"`         // hex-encoded SHA256
	LastSeenTime     time.Time `json:"last_seen_time"`     // when we last saw this state
	FirstName        string    `json:"first_name"`         // cached name
	HavePhoto        bool      `json:"have_photo"`         // do we have their photo cached locally?
	PhotoRequestSent bool      `json:"photo_request_sent"` // have we requested this photo?
	ProfileVersion   int32     `json:"profile_version"`    // profile version number
}

// MeshView manages the gossip protocol mesh view
// Tracks which devices exist and what photos they have
// This is SHARED CODE used by both iPhone and Android
type MeshView struct {
	mu sync.RWMutex

	// Our identity
	ourDeviceID     string
	ourHardwareUUID string

	// Mesh state: deviceID -> device state
	devices map[string]*MeshDeviceState

	// Connection state: which devices we're actually connected to right now
	connectedDevices map[string]bool // deviceID -> is connected

	// Gossip state
	gossipRound    int32
	lastGossipTime time.Time
	gossipInterval time.Duration

	// Photo cache reference (to check if we have photos)
	photoCache *PhotoCache

	// Identity manager reference (to look up hardware UUIDs)
	identityManager *IdentityManager

	// Persistence
	dataDir string

	// Gossip audit logging
	gossipAuditFile *os.File
	auditMutex      sync.Mutex
}

// NewMeshView creates a new mesh view manager
func NewMeshView(ourDeviceID, ourHardwareUUID, dataDir string, photoCache *PhotoCache) *MeshView {
	mv := &MeshView{
		ourDeviceID:      ourHardwareUUID,
		ourHardwareUUID:  ourHardwareUUID,
		devices:          make(map[string]*MeshDeviceState),
		connectedDevices: make(map[string]bool),
		gossipInterval:   5 * time.Second,
		photoCache:       photoCache,
		dataDir:          dataDir,
	}

	// Try to load persisted state
	mv.loadFromDisk()

	// Initialize gossip audit log
	mv.initGossipAuditLog()

	return mv
}

// SetIdentityManager sets the identity manager reference for looking up hardware UUIDs
func (mv *MeshView) SetIdentityManager(im *IdentityManager) {
	mv.mu.Lock()
	defer mv.mu.Unlock()
	mv.identityManager = im
}

// MarkDeviceConnected marks a device as currently connected
func (mv *MeshView) MarkDeviceConnected(deviceID string) {
	mv.mu.Lock()
	defer mv.mu.Unlock()
	mv.connectedDevices[deviceID] = true
}

// MarkDeviceDisconnected marks a device as disconnected
func (mv *MeshView) MarkDeviceDisconnected(deviceID string) {
	mv.mu.Lock()
	defer mv.mu.Unlock()
	delete(mv.connectedDevices, deviceID)

	// Reset request flags so we retry incomplete transfers after reconnect
	if device, exists := mv.devices[deviceID]; exists {
		device.PhotoRequestSent = false
	}
}

// IsDeviceConnected checks if a device is currently connected
func (mv *MeshView) IsDeviceConnected(deviceID string) bool {
	mv.mu.RLock()
	defer mv.mu.RUnlock()
	return mv.connectedDevices[deviceID]
}

// GetConnectedDevices returns only devices we're currently connected to
func (mv *MeshView) GetConnectedDevices() []*MeshDeviceState {
	mv.mu.RLock()
	defer mv.mu.RUnlock()

	connected := []*MeshDeviceState{}
	for deviceID := range mv.connectedDevices {
		if device, exists := mv.devices[deviceID]; exists {
			// Make a copy
			deviceCopy := *device
			connected = append(connected, &deviceCopy)
		}
	}
	return connected
}

// UpdateDevice updates or adds a device to the mesh view
func (mv *MeshView) UpdateDevice(deviceID, photoHashHex, firstName string, profileVersion int32) {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	now := time.Now()

	device, exists := mv.devices[deviceID]
	if !exists {
		// New device discovered
		device = &MeshDeviceState{
			DeviceID:       deviceID,
			PhotoHash:      photoHashHex,
			FirstName:      firstName,
			ProfileVersion: profileVersion,
			LastSeenTime:   now,
			HavePhoto:      mv.photoCache != nil && mv.photoCache.HasPhoto(photoHashHex),
		}
		mv.devices[deviceID] = device

		logger.Debug(fmt.Sprintf("%s GOSSIP", mv.ourDeviceID[:8]),
			"📡 New device in mesh: %s (%s, photo: %s)",
			deviceID[:8], firstName, shortHash(photoHashHex, 8))
	} else {
		// Update existing device
		photoChanged := device.PhotoHash != photoHashHex
		if photoChanged {
			logger.Debug(fmt.Sprintf("%s GOSSIP", mv.ourDeviceID[:8]),
				"📸 Device %s updated photo: %s → %s",
				deviceID[:8], shortHash(device.PhotoHash, 8), shortHash(photoHashHex, 8))
			device.PhotoRequestSent = false // Reset flag for new photo
		}

		device.PhotoHash = photoHashHex
		device.FirstName = firstName
		device.ProfileVersion = profileVersion
		device.LastSeenTime = now
		device.HavePhoto = mv.photoCache != nil && mv.photoCache.HasPhoto(photoHashHex)
	}
}

// GetMissingPhotos returns devices whose photos we don't have and haven't requested
func (mv *MeshView) GetMissingPhotos() []*MeshDeviceState {
	mv.mu.RLock()
	defer mv.mu.RUnlock()

	missing := []*MeshDeviceState{}
	for _, device := range mv.devices {
		if device.PhotoHash != "" && !device.HavePhoto && !device.PhotoRequestSent {
			// Make a copy
			deviceCopy := *device
			missing = append(missing, &deviceCopy)
		}
	}
	return missing
}

// MarkPhotoRequested marks that we've requested a photo from a device
func (mv *MeshView) MarkPhotoRequested(deviceID string) {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	if device, exists := mv.devices[deviceID]; exists {
		device.PhotoRequestSent = true
	}
}

// MarkPhotoReceived marks that we've received and cached a photo
func (mv *MeshView) MarkPhotoReceived(deviceID string) {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	if device, exists := mv.devices[deviceID]; exists {
		device.HavePhoto = true
		device.PhotoRequestSent = false
	}
}

// ShouldGossip returns true if it's time to send gossip
func (mv *MeshView) ShouldGossip() bool {
	mv.mu.RLock()
	defer mv.mu.RUnlock()

	return time.Since(mv.lastGossipTime) >= mv.gossipInterval
}

// BuildGossipMessage creates a gossip message with our complete mesh view
func (mv *MeshView) BuildGossipMessage(ourPhotoHash string) *pb.GossipMessage {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	mv.gossipRound++
	mv.lastGossipTime = time.Now()

	// Build list of device states
	deviceStates := []*pb.DeviceState{}

	// Include ourselves first
	ourPhotoHashBytes := hexToBytes(ourPhotoHash)
	deviceStates = append(deviceStates, &pb.DeviceState{
		DeviceId:          mv.ourDeviceID,
		PhotoHash:         ourPhotoHashBytes,
		LastSeenTimestamp: time.Now().Unix(),
		FirstName:         mv.ourDeviceID[:4], // Use first 4 chars of device ID
		ProfileVersion:    1,
	})

	// Add all other known devices
	for _, device := range mv.devices {
		photoHashBytes := hexToBytes(device.PhotoHash)
		deviceStates = append(deviceStates, &pb.DeviceState{
			DeviceId:          device.DeviceID,
			PhotoHash:         photoHashBytes,
			LastSeenTimestamp: device.LastSeenTime.Unix(),
			FirstName:         device.FirstName,
			ProfileVersion:    device.ProfileVersion,
		})
	}

	gossipMsg := &pb.GossipMessage{
		SenderDeviceId: mv.ourDeviceID,
		Timestamp:      time.Now().Unix(),
		MeshView:       deviceStates,
		GossipRound:    mv.gossipRound,
	}

	return gossipMsg
}

// MergeGossip merges received gossip into our mesh view
// Returns list of newly discovered devices
func (mv *MeshView) MergeGossip(gossipMsg *pb.GossipMessage) []string {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	newDevices := []string{}

	for _, deviceState := range gossipMsg.MeshView {
		// Skip ourselves
		if deviceState.DeviceId == mv.ourDeviceID {
			continue
		}

		photoHashHex := bytesToHex(deviceState.PhotoHash)
		_, existed := mv.devices[deviceState.DeviceId]

		// Update or create device
		device, exists := mv.devices[deviceState.DeviceId]
		if !exists {
			device = &MeshDeviceState{
				DeviceID:       deviceState.DeviceId,
				PhotoHash:      photoHashHex,
				FirstName:      deviceState.FirstName,
				ProfileVersion: deviceState.ProfileVersion,
				LastSeenTime:   time.Unix(deviceState.LastSeenTimestamp, 0),
				HavePhoto:      mv.photoCache != nil && mv.photoCache.HasPhoto(photoHashHex),
			}
			mv.devices[deviceState.DeviceId] = device
			newDevices = append(newDevices, deviceState.DeviceId)
		} else {
			// Update if timestamp is newer
			if deviceState.LastSeenTimestamp > device.LastSeenTime.Unix() {
				device.PhotoHash = photoHashHex
				device.FirstName = deviceState.FirstName
				device.ProfileVersion = deviceState.ProfileVersion
				device.LastSeenTime = time.Unix(deviceState.LastSeenTimestamp, 0)
				device.HavePhoto = mv.photoCache != nil && mv.photoCache.HasPhoto(photoHashHex)
			}
		}

		if !existed && len(newDevices) > 0 {
			logger.Info(fmt.Sprintf("%s GOSSIP", mv.ourDeviceID[:8]),
				"🌐 Learned about device %s via gossip from %s",
				deviceState.DeviceId[:8], gossipMsg.SenderDeviceId[:8])
		}
	}

	// Log gossip audit
	mv.logGossipAudit("received", gossipMsg.SenderDeviceId, len(gossipMsg.MeshView), len(newDevices))

	return newDevices
}

// GetAllDevices returns all known devices in the mesh
func (mv *MeshView) GetAllDevices() []*MeshDeviceState {
	mv.mu.RLock()
	defer mv.mu.RUnlock()

	devices := make([]*MeshDeviceState, 0, len(mv.devices))
	for _, device := range mv.devices {
		deviceCopy := *device
		devices = append(devices, &deviceCopy)
	}
	return devices
}

// Helper functions
func hexToBytes(hexStr string) []byte {
	if hexStr == "" {
		return []byte{}
	}
	bytes, _ := hex.DecodeString(hexStr)
	return bytes
}

func bytesToHex(bytes []byte) string {
	return hex.EncodeToString(bytes)
}

func shortHash(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Persistence
func (mv *MeshView) loadFromDisk() {
	cachePath := filepath.Join(mv.dataDir, "cache", "mesh_view.json")
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return // File doesn't exist yet, that's okay
	}

	var saved struct {
		Devices map[string]*MeshDeviceState `json:"devices"`
	}

	if err := json.Unmarshal(data, &saved); err != nil {
		logger.Warn(fmt.Sprintf("%s GOSSIP", mv.ourDeviceID[:8]), "Failed to load mesh view: %v", err)
		return
	}

	mv.devices = saved.Devices
	logger.Info(fmt.Sprintf("%s GOSSIP", mv.ourDeviceID[:8]), "📂 Loaded mesh view with %d devices", len(mv.devices))
}

func (mv *MeshView) SaveToDisk() error {
	mv.mu.RLock()
	defer mv.mu.RUnlock()

	cacheDir := filepath.Join(mv.dataDir, "cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}

	saved := struct {
		Devices map[string]*MeshDeviceState `json:"devices"`
	}{
		Devices: mv.devices,
	}

	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return err
	}

	// Atomic write: write to temp file, then rename
	tempPath := filepath.Join(cacheDir, "mesh_view.json.tmp")
	finalPath := filepath.Join(cacheDir, "mesh_view.json")

	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return err
	}

	return os.Rename(tempPath, finalPath)
}

// Gossip audit logging
func (mv *MeshView) initGossipAuditLog() {
	auditPath := filepath.Join(mv.dataDir, "gossip_audit.jsonl")
	file, err := os.OpenFile(auditPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		logger.Warn(fmt.Sprintf("%s GOSSIP", mv.ourDeviceID[:8]), "Failed to open gossip audit log: %v", err)
		return
	}
	mv.gossipAuditFile = file
}

func (mv *MeshView) logGossipAudit(eventType, peerDeviceID string, totalDevices, newDevices int) {
	mv.auditMutex.Lock()
	defer mv.auditMutex.Unlock()

	if mv.gossipAuditFile == nil {
		return
	}

	audit := map[string]interface{}{
		"timestamp":      time.Now().Unix(),
		"event":          eventType,
		"peer_device_id": peerDeviceID,
		"gossip_round":   mv.gossipRound,
		"total_devices":  totalDevices,
		"new_devices":    newDevices,
	}

	data, _ := json.Marshal(audit)
	mv.gossipAuditFile.Write(append(data, '\n'))
}

// Close cleans up resources
func (mv *MeshView) Close() error {
	mv.SaveToDisk()

	if mv.gossipAuditFile != nil {
		return mv.gossipAuditFile.Close()
	}
	return nil
}
