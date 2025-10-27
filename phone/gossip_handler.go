package phone

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/proto"
	proto2 "google.golang.org/protobuf/proto"
)

// GossipLoop periodically sends gossip messages to neighbors and prunes non-neighbors
func (gh *GossipHandler) GossipLoop() {
	ticker := time.NewTicker(gh.gossipInterval)
	defer ticker.Stop()

	// Prune connections every 30 seconds (6x the gossip interval)
	pruneCount := 0

	for {
		select {
		case <-ticker.C:
			gh.SendGossipToNeighbors()

			// Prune non-neighbor connections periodically
			pruneCount++
			if pruneCount >= 6 {
				gh.PruneNonNeighborConnections()
				pruneCount = 0
			}
		case <-gh.staleCheckDone:
			return
		}
	}
}

// SendGossipToNeighbors broadcasts our mesh view to all connected neighbors
func (gh *GossipHandler) SendGossipToNeighbors() {
	device := gh.device
	prefix := fmt.Sprintf("%s %s", device.GetHardwareUUID()[:8], device.GetPlatform())

	// Select neighbors using mesh view (deterministic, max 3)
	meshView := device.GetMeshView()
	neighbors := meshView.SelectRandomNeighbors()

	// Map deviceIDs to hardware UUIDs
	mutex := device.GetMutex()
	mutex.RLock()
	deviceIDToUUID := make(map[string]string)
	uuidToDeviceID := device.GetUUIDToDeviceIDMap()
	for uuid, deviceID := range uuidToDeviceID {
		deviceIDToUUID[deviceID] = uuid
	}
	mutex.RUnlock()

	// Calculate profile summary hash
	profileSummaryHashBytes := CalculateProfileSummaryHash(device.GetLocalProfile())
	profileSummaryHashHex := HashBytesToString(profileSummaryHashBytes)

	// Build gossip message with our current state
	localProfile := device.GetLocalProfile()
	gossip := meshView.BuildGossipMessage(
		device.GetPhotoHash(),
		localProfile.FirstName,
		localProfile.ProfileVersion,
		profileSummaryHashHex,
	)

	data, err := proto2.Marshal(gossip)
	if err != nil {
		logger.Error(prefix, "❌ Failed to marshal gossip: %v", err)
		return
	}

	logger.Debug(prefix, "📢 Broadcasting gossip (round %d) to %d neighbors", gossip.GossipRound, len(neighbors))

	// Send to neighbors that are currently connected
	connManager := device.GetConnManager()
	sentCount := 0
	for _, neighborDeviceID := range neighbors {
		neighborUUID, exists := deviceIDToUUID[neighborDeviceID]
		if !exists {
			continue // Don't know hardware UUID yet
		}

		if !connManager.IsConnected(neighborUUID) {
			continue // Not currently connected
		}

		if err := connManager.SendToDevice(neighborUUID, AuraProtocolCharUUID, data); err != nil {
			logger.Warn(prefix, "⚠️  Failed to send gossip to %s: %v", neighborUUID[:8], err)
		} else {
			// Log gossip sent to audit log
			meshView.LogGossipSent(neighborDeviceID, neighborUUID, len(gossip.MeshView), fmt.Sprintf("/tmp/auraphone-%s.sock", neighborUUID))
			sentCount++
		}
	}

	logger.Debug(prefix, "📢 Sent gossip to %d/%d neighbors", sentCount, len(neighbors))

	// Save mesh view periodically
	meshView.SaveToDisk()
}

// SendGossipToDevice sends gossip to a specific device (used after connection)
func (gh *GossipHandler) SendGossipToDevice(remoteUUID string) {
	device := gh.device
	prefix := fmt.Sprintf("%s %s", device.GetHardwareUUID()[:8], device.GetPlatform())

	profileSummaryHashBytes := CalculateProfileSummaryHash(device.GetLocalProfile())
	profileSummaryHashHex := HashBytesToString(profileSummaryHashBytes)

	meshView := device.GetMeshView()
	localProfile := device.GetLocalProfile()
	gossip := meshView.BuildGossipMessage(
		device.GetPhotoHash(),
		localProfile.FirstName,
		localProfile.ProfileVersion,
		profileSummaryHashHex,
	)

	data, err := proto2.Marshal(gossip)
	if err != nil {
		logger.Error(prefix, "❌ Failed to marshal gossip: %v", err)
		return
	}

	connManager := device.GetConnManager()
	if err := connManager.SendToDevice(remoteUUID, AuraProtocolCharUUID, data); err != nil {
		logger.Warn(prefix, "⚠️  Failed to send initial gossip to %s: %v", remoteUUID[:8], err)
	} else {
		// Log gossip sent to audit log
		// Get device ID for this UUID
		mutex := device.GetMutex()
		mutex.RLock()
		uuidToDeviceID := device.GetUUIDToDeviceIDMap()
		toDeviceID := uuidToDeviceID[remoteUUID]
		mutex.RUnlock()

		meshView.LogGossipSent(toDeviceID, remoteUUID, len(gossip.MeshView), fmt.Sprintf("/tmp/auraphone-%s.sock", remoteUUID))
		logger.Debug(prefix, "📤 Sent initial gossip to %s", remoteUUID[:8])
	}
}

// CalculateProfileSummaryHash computes SHA-256 hash of all profile fields
func CalculateProfileSummaryHash(profile *LocalProfile) []byte {
	h := sha256.New()
	h.Write([]byte(profile.LastName))
	h.Write([]byte(profile.Tagline))
	h.Write([]byte(profile.Insta))
	h.Write([]byte(profile.LinkedIn))
	h.Write([]byte(profile.YouTube))
	h.Write([]byte(profile.TikTok))
	h.Write([]byte(profile.Gmail))
	h.Write([]byte(profile.IMessage))
	h.Write([]byte(profile.WhatsApp))
	h.Write([]byte(profile.Signal))
	h.Write([]byte(profile.Telegram))
	return h.Sum(nil)
}

// RequestPhoto requests a photo from a device (triggered by gossip)
func (gh *GossipHandler) RequestPhoto(deviceID, photoHash string) error {
	device := gh.device
	prefix := fmt.Sprintf("%s %s", device.GetHardwareUUID()[:8], device.GetPlatform())
	logger.Info(prefix, "📸 Requesting photo %s from device %s (via gossip)", TruncateHash(photoHash, 8), deviceID[:8])

	// Build PhotoRequestMessage
	hashBytes, _ := hex.DecodeString(photoHash)
	req := &proto.PhotoRequestMessage{
		RequesterDeviceId: device.GetDeviceID(),
		TargetDeviceId:    deviceID,
		PhotoHash:         hashBytes,
	}

	data, err := proto2.Marshal(req)
	if err != nil {
		logger.Error(prefix, "❌ Failed to marshal photo request: %v", err)
		return fmt.Errorf("failed to marshal photo request: %w", err)
	}

	// Get hardware UUID from identity manager
	identityManager := device.GetIdentityManager()
	targetUUID, ok := identityManager.GetHardwareUUID(deviceID)

	if !ok || targetUUID == "" {
		logger.Warn(prefix, "⚠️  Cannot request photo: don't know hardware UUID for device %s", deviceID[:8])
		return fmt.Errorf("hardware UUID not known for device %s", deviceID)
	}

	// Send request
	connManager := device.GetConnManager()
	if err := connManager.SendToDevice(targetUUID, AuraProtocolCharUUID, data); err != nil {
		logger.Error(prefix, "❌ Failed to send photo request: %v", err)
		return fmt.Errorf("failed to send photo request: %w", err)
	}

	logger.Debug(prefix, "📤 Sent photo request for %s", TruncateHash(photoHash, 8))
	return nil
}

// RequestProfile requests a profile from a device (triggered by gossip)
func (gh *GossipHandler) RequestProfile(deviceID string, version int32) error {
	device := gh.device
	prefix := fmt.Sprintf("%s %s", device.GetHardwareUUID()[:8], device.GetPlatform())
	logger.Info(prefix, "📝 Requesting profile v%d from device %s (via gossip)", version, deviceID[:8])

	// Build ProfileRequestMessage
	req := &proto.ProfileRequestMessage{
		RequesterDeviceId: device.GetDeviceID(),
		TargetDeviceId:    deviceID,
		ExpectedVersion:   version,
	}

	data, err := proto2.Marshal(req)
	if err != nil {
		logger.Error(prefix, "❌ Failed to marshal profile request: %v", err)
		return fmt.Errorf("failed to marshal profile request: %w", err)
	}

	// Get hardware UUID from identity manager
	identityManager := device.GetIdentityManager()
	targetUUID, ok := identityManager.GetHardwareUUID(deviceID)

	if !ok || targetUUID == "" {
		logger.Warn(prefix, "⚠️  Cannot request profile: don't know hardware UUID for device %s", deviceID[:8])
		return fmt.Errorf("hardware UUID not known for device %s", deviceID)
	}

	// Send request
	connManager := device.GetConnManager()
	if err := connManager.SendToDevice(targetUUID, AuraProtocolCharUUID, data); err != nil {
		logger.Error(prefix, "❌ Failed to send profile request: %v", err)
		return fmt.Errorf("failed to send profile request: %w", err)
	}

	logger.Debug(prefix, "📤 Sent profile request for v%d", version)
	return nil
}

// PruneNonNeighborConnections disconnects from devices that are not our neighbors
// This achieves O(log N) connections instead of O(N²) full mesh
func (gh *GossipHandler) PruneNonNeighborConnections() {
	device := gh.device
	prefix := fmt.Sprintf("%s %s", device.GetHardwareUUID()[:8], device.GetPlatform())

	// Get current neighbors (deterministic selection, max 3)
	meshView := device.GetMeshView()
	neighbors := meshView.GetCurrentNeighbors()
	neighborMap := make(map[string]bool)
	for _, deviceID := range neighbors {
		neighborMap[deviceID] = true
	}

	// Get all connected hardware UUIDs
	connManager := device.GetConnManager()
	connectedUUIDs := connManager.GetAllConnectedUUIDs()

	mutex := device.GetMutex()
	mutex.RLock()
	// Build reverse map for quick lookup
	uuidToDeviceID := make(map[string]string)
	for uuid, deviceID := range device.GetUUIDToDeviceIDMap() {
		uuidToDeviceID[uuid] = deviceID
	}
	mutex.RUnlock()

	for _, uuid := range connectedUUIDs {
		deviceID := uuidToDeviceID[uuid]
		if deviceID == "" {
			// Don't know deviceID yet, keep connection (will prune later)
			continue
		}

		// Check if this is a neighbor
		if !neighborMap[deviceID] {
			// Only disconnect if we are the Central (we initiated the connection)
			if connManager.IsConnectedAsCentral(uuid) {
				logger.Info(prefix, "✂️  Disconnecting from %s (not a neighbor, deviceID: %s)", uuid[:8], deviceID[:8])

				// Platform-specific disconnect (iOS uses CBCentralManager, Android uses BluetoothGatt)
				if err := device.DisconnectFromDevice(uuid); err != nil {
					logger.Warn(prefix, "⚠️  Failed to disconnect from %s: %v", uuid[:8], err)
				}
			}
			// If they connected to us as Peripheral, we can't force disconnect
			// (that's up to them when they prune their neighbors)
		}
	}
}
