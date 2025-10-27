package phone

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/user/auraphone-blue/proto"
)

// MeshDeviceState represents what we know about a single device in the mesh
type MeshDeviceState struct {
	DeviceID           string    `json:"device_id"`
	PhotoHash          string    `json:"photo_hash"`           // hex-encoded SHA256
	LastSeenTime       time.Time `json:"last_seen_time"`       // when we last saw this state
	FirstName          string    `json:"first_name"`           // cached name
	HavePhoto          bool      `json:"have_photo"`           // do we have their photo cached locally?
	PhotoRequestSent   bool      `json:"photo_request_sent"`   // have we requested this photo?
	ProfileVersion     int32     `json:"profile_version"`      // profile version number
	ProfileSummaryHash string    `json:"profile_summary_hash"` // hex-encoded SHA256 of all profile fields
	HaveProfile        bool      `json:"have_profile"`         // do we have their profile cached locally?
	ProfileRequestSent bool      `json:"profile_request_sent"` // have we requested this profile?
}

// MeshView manages the gossip protocol mesh view
// Tracks which devices exist and what photos they have
type MeshView struct {
	mu sync.RWMutex

	// Our identity
	ourDeviceID     string
	ourHardwareUUID string

	// Mesh state: deviceID -> device state
	devices map[string]*MeshDeviceState

	// Connection state: which devices we're actually connected to right now
	connectedNeighbors map[string]bool // deviceID -> is connected

	// Neighbor management
	maxNeighbors     int
	currentNeighbors []string // device IDs of current neighbors

	// Gossip state
	gossipRound    int32
	lastGossipTime time.Time
	gossipInterval time.Duration

	// Photo cache reference (to check if we have photos)
	cacheManager *DeviceCacheManager

	// Identity manager reference (to look up hardware UUIDs)
	identityManager *IdentityManager

	// Persistence
	dataDir string

	// Random source for neighbor selection
	rng *rand.Rand
}

// NewMeshView creates a new mesh view manager
func NewMeshView(ourDeviceID, ourHardwareUUID, dataDir string, cacheManager *DeviceCacheManager) *MeshView {
	mv := &MeshView{
		ourDeviceID:        ourDeviceID,
		ourHardwareUUID:    ourHardwareUUID,
		devices:            make(map[string]*MeshDeviceState),
		connectedNeighbors: make(map[string]bool),
		maxNeighbors:       3,
		gossipInterval:     5 * time.Second,
		cacheManager:       cacheManager,
		dataDir:            dataDir,
		rng:                rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	// Try to load persisted state
	mv.loadFromDisk()

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
	mv.connectedNeighbors[deviceID] = true
}

// MarkDeviceDisconnected marks a device as disconnected
func (mv *MeshView) MarkDeviceDisconnected(deviceID string) {
	mv.mu.Lock()
	defer mv.mu.Unlock()
	delete(mv.connectedNeighbors, deviceID)
}

// IsDeviceConnected checks if a device is currently connected
func (mv *MeshView) IsDeviceConnected(deviceID string) bool {
	mv.mu.RLock()
	defer mv.mu.RUnlock()
	return mv.connectedNeighbors[deviceID]
}

// GetConnectedDevices returns only devices we're currently connected to
func (mv *MeshView) GetConnectedDevices() []*MeshDeviceState {
	mv.mu.RLock()
	defer mv.mu.RUnlock()

	connected := []*MeshDeviceState{}
	for deviceID := range mv.connectedNeighbors {
		if device, exists := mv.devices[deviceID]; exists {
			// Make a copy
			deviceCopy := *device
			connected = append(connected, &deviceCopy)
		}
	}
	return connected
}

// UpdateDevice updates or adds a device to the mesh view
func (mv *MeshView) UpdateDevice(deviceID, photoHashHex, firstName string, profileVersion int32, profileSummaryHashHex string) {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	now := time.Now()

	// Check if we have this photo cached
	havePhoto := false
	if mv.cacheManager != nil && photoHashHex != "" {
		photoPath := filepath.Join(mv.dataDir, "cache", "photos", photoHashHex+".jpg")
		if _, err := os.Stat(photoPath); err == nil {
			havePhoto = true
		}
	}

	// Check if we have this profile cached
	haveProfile := false
	if mv.cacheManager != nil && deviceID != "" {
		profilePath := filepath.Join(mv.dataDir, "cache", "profiles", deviceID+".json")
		if _, err := os.Stat(profilePath); err == nil {
			haveProfile = true
		}
	}

	existing, exists := mv.devices[deviceID]
	if exists {
		// Update existing device
		if photoHashHex != "" && photoHashHex != existing.PhotoHash {
			// Photo changed - reset request state
			existing.PhotoHash = photoHashHex
			existing.PhotoRequestSent = false
			existing.HavePhoto = havePhoto
		}
		if profileVersion > 0 && (profileVersion != existing.ProfileVersion || profileSummaryHashHex != existing.ProfileSummaryHash) {
			// Profile changed - reset request state
			existing.ProfileVersion = profileVersion
			existing.ProfileSummaryHash = profileSummaryHashHex
			existing.ProfileRequestSent = false
			existing.HaveProfile = haveProfile
		}
		existing.LastSeenTime = now
		existing.FirstName = firstName
	} else {
		// New device discovered
		mv.devices[deviceID] = &MeshDeviceState{
			DeviceID:           deviceID,
			PhotoHash:          photoHashHex,
			LastSeenTime:       now,
			FirstName:          firstName,
			HavePhoto:          havePhoto,
			PhotoRequestSent:   false,
			ProfileVersion:     profileVersion,
			ProfileSummaryHash: profileSummaryHashHex,
			HaveProfile:        haveProfile,
			ProfileRequestSent: false,
		}
	}
}

// MergeGossip merges gossip information from a neighbor
// Returns list of new devices or updated photo/profile hashes we learned about
func (mv *MeshView) MergeGossip(gossipMsg *proto.GossipMessage) []string {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	newDiscoveries := []string{}

	for _, deviceState := range gossipMsg.MeshView {
		deviceID := deviceState.DeviceId
		photoHashBytes := deviceState.PhotoHash
		photoHashHex := hex.EncodeToString(photoHashBytes)
		firstName := deviceState.FirstName
		lastSeenTime := time.Unix(deviceState.LastSeenTimestamp, 0)
		profileVersion := deviceState.ProfileVersion
		profileSummaryHashHex := hex.EncodeToString(deviceState.ProfileSummaryHash)

		// Skip ourselves
		if deviceID == mv.ourDeviceID {
			continue
		}

		existing, exists := mv.devices[deviceID]

		if !exists {
			// New device we didn't know about
			havePhoto := false
			if mv.cacheManager != nil && photoHashHex != "" {
				photoPath := filepath.Join(mv.dataDir, "cache", "photos", photoHashHex+".jpg")
				if _, err := os.Stat(photoPath); err == nil {
					havePhoto = true
				}
			}

			haveProfile := false
			if mv.cacheManager != nil && deviceID != "" {
				profilePath := filepath.Join(mv.dataDir, "cache", "profiles", deviceID+".json")
				if _, err := os.Stat(profilePath); err == nil {
					haveProfile = true
				}
			}

			mv.devices[deviceID] = &MeshDeviceState{
				DeviceID:           deviceID,
				PhotoHash:          photoHashHex,
				LastSeenTime:       lastSeenTime,
				FirstName:          firstName,
				HavePhoto:          havePhoto,
				PhotoRequestSent:   false,
				ProfileVersion:     profileVersion,
				ProfileSummaryHash: profileSummaryHashHex,
				HaveProfile:        haveProfile,
				ProfileRequestSent: false,
			}
			newDiscoveries = append(newDiscoveries, deviceID)
		} else {
			// Update if gossip has newer information
			updated := false
			if lastSeenTime.After(existing.LastSeenTime) {
				// Hardware UUID is no longer stored here - it's managed by IdentityManager
				if photoHashHex != "" && photoHashHex != existing.PhotoHash {
					// Photo changed
					existing.PhotoHash = photoHashHex
					existing.PhotoRequestSent = false
					existing.HavePhoto = false // Need to fetch new photo
					updated = true
				}
				if profileVersion > 0 && (profileVersion != existing.ProfileVersion || profileSummaryHashHex != existing.ProfileSummaryHash) {
					// Profile changed (version increased or hash differs)
					existing.ProfileVersion = profileVersion
					existing.ProfileSummaryHash = profileSummaryHashHex
					existing.ProfileRequestSent = false
					existing.HaveProfile = false // Need to fetch new profile
					updated = true
				}
				existing.LastSeenTime = lastSeenTime
				existing.FirstName = firstName
			}
			if updated {
				newDiscoveries = append(newDiscoveries, deviceID)
			}
		}
	}

	return newDiscoveries
}

// GetMissingPhotos returns list of devices whose photos we need to fetch
// NOW ONLY RETURNS DEVICES WE'RE CURRENTLY CONNECTED TO
func (mv *MeshView) GetMissingPhotos() []*MeshDeviceState {
	mv.mu.RLock()
	defer mv.mu.RUnlock()

	missing := []*MeshDeviceState{}

	for _, device := range mv.devices {
		// Need photo if: we don't have it, photo hash is known, and we haven't sent request yet
		if !device.HavePhoto && device.PhotoHash != "" && !device.PhotoRequestSent {
			// NEW: Only include if this device is currently connected
			// Check via identity manager for connection state
			if mv.identityManager != nil {
				if hardwareUUID, ok := mv.identityManager.GetHardwareUUID(device.DeviceID); ok {
					if mv.identityManager.IsConnected(hardwareUUID) {
						missing = append(missing, device)
					}
				}
			}
		}
	}

	return missing
}

// MarkPhotoRequested marks that we've sent a request for this device's photo
func (mv *MeshView) MarkPhotoRequested(deviceID string) {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	if device, exists := mv.devices[deviceID]; exists {
		device.PhotoRequestSent = true
	}
}

// MarkPhotoReceived marks that we've successfully received a device's photo
func (mv *MeshView) MarkPhotoReceived(deviceID, photoHashHex string) {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	if device, exists := mv.devices[deviceID]; exists {
		if device.PhotoHash == photoHashHex {
			device.HavePhoto = true
		}
	}
}

// GetMissingProfiles returns list of devices whose profiles we need to fetch
// NOW ONLY RETURNS DEVICES WE'RE CURRENTLY CONNECTED TO
func (mv *MeshView) GetMissingProfiles() []*MeshDeviceState {
	mv.mu.RLock()
	defer mv.mu.RUnlock()

	missing := []*MeshDeviceState{}

	for _, device := range mv.devices {
		// Need profile if: we don't have it, profile version is known, and we haven't sent request yet
		if !device.HaveProfile && device.ProfileVersion > 0 && !device.ProfileRequestSent {
			// NEW: Only include if this device is currently connected
			// Check via identity manager for connection state
			if mv.identityManager != nil {
				if hardwareUUID, ok := mv.identityManager.GetHardwareUUID(device.DeviceID); ok {
					if mv.identityManager.IsConnected(hardwareUUID) {
						missing = append(missing, device)
					}
				}
			}
		}
	}

	return missing
}

// MarkProfileRequested marks that we've sent a request for this device's profile
func (mv *MeshView) MarkProfileRequested(deviceID string) {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	if device, exists := mv.devices[deviceID]; exists {
		device.ProfileRequestSent = true
	}
}

// MarkProfileReceived marks that we've successfully received a device's profile
func (mv *MeshView) MarkProfileReceived(deviceID string, version int32) {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	if device, exists := mv.devices[deviceID]; exists {
		if device.ProfileVersion == version {
			device.HaveProfile = true
		}
	}
}

// SelectRandomNeighbors picks N random devices to be our gossip neighbors
// Uses consistent selection: picks devices with lowest hash(ourID + theirID)
func (mv *MeshView) SelectRandomNeighbors() []string {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	// Get all known devices except ourselves
	candidates := []string{}
	for deviceID := range mv.devices {
		if deviceID != mv.ourDeviceID {
			candidates = append(candidates, deviceID)
		}
	}

	// If we have <= maxNeighbors devices, connect to all
	if len(candidates) <= mv.maxNeighbors {
		mv.currentNeighbors = candidates
		return candidates
	}

	// Use consistent hashing to pick neighbors
	// Hash(ourID + theirID) - pick devices with lowest N hashes
	type scoredDevice struct {
		deviceID string
		score    string
	}

	scoredDevices := []scoredDevice{}
	for _, deviceID := range candidates {
		combined := mv.ourDeviceID + deviceID
		hash := sha256.Sum256([]byte(combined))
		hashHex := hex.EncodeToString(hash[:])
		scoredDevices = append(scoredDevices, scoredDevice{
			deviceID: deviceID,
			score:    hashHex,
		})
	}

	// Sort by score (lexicographically)
	// Simple bubble sort is fine for small N
	for i := 0; i < len(scoredDevices); i++ {
		for j := i + 1; j < len(scoredDevices); j++ {
			if scoredDevices[j].score < scoredDevices[i].score {
				scoredDevices[i], scoredDevices[j] = scoredDevices[j], scoredDevices[i]
			}
		}
	}

	// Take top N
	neighbors := []string{}
	for i := 0; i < mv.maxNeighbors && i < len(scoredDevices); i++ {
		neighbors = append(neighbors, scoredDevices[i].deviceID)
	}

	mv.currentNeighbors = neighbors
	return neighbors
}

// GetCurrentNeighbors returns the current neighbor list
func (mv *MeshView) GetCurrentNeighbors() []string {
	mv.mu.RLock()
	defer mv.mu.RUnlock()

	result := make([]string, len(mv.currentNeighbors))
	copy(result, mv.currentNeighbors)
	return result
}

// IsNeighbor checks if a device is one of our current neighbors
func (mv *MeshView) IsNeighbor(deviceID string) bool {
	mv.mu.RLock()
	defer mv.mu.RUnlock()

	for _, neighbor := range mv.currentNeighbors {
		if neighbor == deviceID {
			return true
		}
	}
	return false
}

// BuildGossipMessage creates a gossip message with our current mesh view
func (mv *MeshView) BuildGossipMessage(ourPhotoHashHex, ourFirstName string, ourProfileVersion int32, ourProfileSummaryHashHex string) *proto.GossipMessage {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	mv.gossipRound++
	mv.lastGossipTime = time.Now()

	// Build device states including ourselves
	meshView := []*proto.DeviceState{}

	// Add ourselves first
	ourHashBytes, _ := hex.DecodeString(ourPhotoHashHex)
	ourProfileSummaryBytes, _ := hex.DecodeString(ourProfileSummaryHashHex)
	meshView = append(meshView, &proto.DeviceState{
		DeviceId:           mv.ourDeviceID,
		HardwareUuid:       mv.ourHardwareUUID,
		PhotoHash:          ourHashBytes,
		LastSeenTimestamp:  time.Now().Unix(),
		FirstName:          ourFirstName,
		ProfileVersion:     ourProfileVersion,
		ProfileSummaryHash: ourProfileSummaryBytes,
	})

	// Add all known devices
	for _, device := range mv.devices {
		hashBytes, _ := hex.DecodeString(device.PhotoHash)
		profileSummaryBytes, _ := hex.DecodeString(device.ProfileSummaryHash)

		// Look up hardware UUID from identity manager
		hardwareUUID := ""
		if mv.identityManager != nil {
			hardwareUUID, _ = mv.identityManager.GetHardwareUUID(device.DeviceID)
		}

		meshView = append(meshView, &proto.DeviceState{
			DeviceId:           device.DeviceID,
			HardwareUuid:       hardwareUUID,
			PhotoHash:          hashBytes,
			LastSeenTimestamp:  device.LastSeenTime.Unix(),
			FirstName:          device.FirstName,
			ProfileVersion:     device.ProfileVersion,
			ProfileSummaryHash: profileSummaryBytes,
		})
	}

	return &proto.GossipMessage{
		SenderDeviceId: mv.ourDeviceID,
		Timestamp:      time.Now().Unix(),
		MeshView:       meshView,
		GossipRound:    mv.gossipRound,
	}
}

// ShouldGossipNow checks if it's time to send gossip
func (mv *MeshView) ShouldGossipNow() bool {
	mv.mu.RLock()
	defer mv.mu.RUnlock()

	return time.Since(mv.lastGossipTime) >= mv.gossipInterval
}

// GetAllDevices returns all known devices
func (mv *MeshView) GetAllDevices() []*MeshDeviceState {
	mv.mu.RLock()
	defer mv.mu.RUnlock()

	devices := make([]*MeshDeviceState, 0, len(mv.devices))
	for _, device := range mv.devices {
		// Make a copy
		deviceCopy := *device
		devices = append(devices, &deviceCopy)
	}
	return devices
}

// Persistence
// ===========

func (mv *MeshView) saveToDisk() error {
	mv.mu.RLock()
	defer mv.mu.RUnlock()

	statePath := filepath.Join(mv.dataDir, "cache", "mesh_view.json")

	// Ensure cache directory exists
	cacheDir := filepath.Join(mv.dataDir, "cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}

	// Serialize state
	data, err := json.MarshalIndent(mv.devices, "", "  ")
	if err != nil {
		return err
	}

	// Write atomically (write to temp file, then rename)
	tempPath := statePath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return err
	}

	return os.Rename(tempPath, statePath)
}

func (mv *MeshView) loadFromDisk() error {
	mv.mu.Lock()
	defer mv.mu.Unlock()

	statePath := filepath.Join(mv.dataDir, "cache", "mesh_view.json")

	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No saved state, that's fine
		}
		return err
	}

	// Deserialize
	devices := make(map[string]*MeshDeviceState)
	if err := json.Unmarshal(data, &devices); err != nil {
		return err
	}

	mv.devices = devices
	return nil
}

// SaveToDisk persists the mesh view (public method for periodic saves)
func (mv *MeshView) SaveToDisk() error {
	return mv.saveToDisk()
}
