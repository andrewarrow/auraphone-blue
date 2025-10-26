package android

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/proto"
	proto2 "google.golang.org/protobuf/proto"
)

// Gossip protocol implementation (PLAN.md Phase 3)

// gossipLoop periodically sends gossip messages to neighbors
func (a *Android) gossipLoop() {
	ticker := time.NewTicker(a.gossipInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			a.sendGossipToNeighbors()
		case <-a.staleCheckDone:
			return
		}
	}
}

// sendGossipToNeighbors broadcasts our mesh view to all connected neighbors
func (a *Android) sendGossipToNeighbors() {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	// Select neighbors using mesh view (deterministic, max 3)
	neighbors := a.meshView.SelectRandomNeighbors()

	// Map deviceIDs to hardware UUIDs
	a.mu.RLock()
	deviceIDToUUID := make(map[string]string)
	for uuid, deviceID := range a.remoteUUIDToDeviceID {
		deviceIDToUUID[deviceID] = uuid
	}
	a.mu.RUnlock()

	// Calculate profile summary hash
	profileSummaryHashBytes := a.calculateProfileSummaryHash()
	profileSummaryHashHex := phone.HashBytesToString(profileSummaryHashBytes)

	// Build gossip message with our current state
	gossip := a.meshView.BuildGossipMessage(
		a.photoHash,
		a.localProfile.FirstName,
		a.localProfile.ProfileVersion,
		profileSummaryHashHex,
	)

	data, err := proto2.Marshal(gossip)
	if err != nil {
		logger.Error(prefix, "❌ Failed to marshal gossip: %v", err)
		return
	}

	logger.Debug(prefix, "📢 Broadcasting gossip (round %d) to %d neighbors", gossip.GossipRound, len(neighbors))

	// Send to neighbors that are currently connected
	sentCount := 0
	for _, neighborDeviceID := range neighbors {
		neighborUUID, exists := deviceIDToUUID[neighborDeviceID]
		if !exists {
			continue // Don't know hardware UUID yet
		}

		if !a.connManager.IsConnected(neighborUUID) {
			continue // Not currently connected
		}

		if err := a.connManager.SendToDevice(neighborUUID, phone.AuraProtocolCharUUID, data); err != nil {
			logger.Warn(prefix, "⚠️  Failed to send gossip to %s: %v", neighborUUID[:8], err)
		} else {
			sentCount++
		}
	}

	logger.Debug(prefix, "📢 Sent gossip to %d/%d neighbors", sentCount, len(neighbors))

	// Save mesh view periodically
	a.meshView.SaveToDisk()
}

// sendGossipToDevice sends gossip to a specific device (used after connection)
func (a *Android) sendGossipToDevice(remoteUUID string) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	profileSummaryHashBytes := a.calculateProfileSummaryHash()
	profileSummaryHashHex := phone.HashBytesToString(profileSummaryHashBytes)

	gossip := a.meshView.BuildGossipMessage(
		a.photoHash,
		a.localProfile.FirstName,
		a.localProfile.ProfileVersion,
		profileSummaryHashHex,
	)

	data, err := proto2.Marshal(gossip)
	if err != nil {
		logger.Error(prefix, "❌ Failed to marshal gossip: %v", err)
		return
	}

	if err := a.connManager.SendToDevice(remoteUUID, phone.AuraProtocolCharUUID, data); err != nil {
		logger.Warn(prefix, "⚠️  Failed to send initial gossip to %s: %v", remoteUUID[:8], err)
	} else {
		logger.Debug(prefix, "📤 Sent initial gossip to %s", remoteUUID[:8])
	}
}

// calculateProfileSummaryHash computes SHA-256 hash of all profile fields
func (a *Android) calculateProfileSummaryHash() []byte {
	h := sha256.New()
	h.Write([]byte(a.localProfile.LastName))
	h.Write([]byte(a.localProfile.Tagline))
	h.Write([]byte(a.localProfile.Insta))
	h.Write([]byte(a.localProfile.LinkedIn))
	h.Write([]byte(a.localProfile.YouTube))
	h.Write([]byte(a.localProfile.TikTok))
	h.Write([]byte(a.localProfile.Gmail))
	h.Write([]byte(a.localProfile.IMessage))
	h.Write([]byte(a.localProfile.WhatsApp))
	h.Write([]byte(a.localProfile.Signal))
	h.Write([]byte(a.localProfile.Telegram))
	return h.Sum(nil)
}

// requestPhoto requests a photo from a device (triggered by gossip)
func (a *Android) requestPhoto(deviceID, photoHash string) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
	logger.Info(prefix, "📸 Requesting photo %s from device %s (via gossip)", phone.TruncateHash(photoHash, 8), deviceID[:8])

	// Build PhotoRequestMessage
	hashBytes, _ := hex.DecodeString(photoHash)
	req := &proto.PhotoRequestMessage{
		RequesterDeviceId: a.deviceID,
		TargetDeviceId:    deviceID,
		PhotoHash:         hashBytes,
	}

	data, err := proto2.Marshal(req)
	if err != nil {
		logger.Error(prefix, "❌ Failed to marshal photo request: %v", err)
		return
	}

	// Find hardware UUID for this deviceID
	a.mu.RLock()
	var targetUUID string
	for uuid, devID := range a.remoteUUIDToDeviceID {
		if devID == deviceID {
			targetUUID = uuid
			break
		}
	}
	a.mu.RUnlock()

	if targetUUID == "" {
		logger.Warn(prefix, "⚠️  Cannot request photo: don't know hardware UUID for device %s", deviceID[:8])
		return
	}

	// Send request
	if err := a.connManager.SendToDevice(targetUUID, phone.AuraProtocolCharUUID, data); err != nil {
		logger.Error(prefix, "❌ Failed to send photo request: %v", err)
		return
	}

	a.meshView.MarkPhotoRequested(deviceID)
	logger.Debug(prefix, "📤 Sent photo request for %s", phone.TruncateHash(photoHash, 8))
}

// requestProfile requests a profile from a device (triggered by gossip)
func (a *Android) requestProfile(deviceID string, version int32) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
	logger.Info(prefix, "📝 Requesting profile v%d from device %s (via gossip)", version, deviceID[:8])

	// Build ProfileRequestMessage
	req := &proto.ProfileRequestMessage{
		RequesterDeviceId: a.deviceID,
		TargetDeviceId:    deviceID,
		ExpectedVersion:   version,
	}

	data, err := proto2.Marshal(req)
	if err != nil {
		logger.Error(prefix, "❌ Failed to marshal profile request: %v", err)
		return
	}

	// Find hardware UUID for this deviceID
	a.mu.RLock()
	var targetUUID string
	for uuid, devID := range a.remoteUUIDToDeviceID {
		if devID == deviceID {
			targetUUID = uuid
			break
		}
	}
	a.mu.RUnlock()

	if targetUUID == "" {
		logger.Warn(prefix, "⚠️  Cannot request profile: don't know hardware UUID for device %s", deviceID[:8])
		return
	}

	// Send request
	if err := a.connManager.SendToDevice(targetUUID, phone.AuraProtocolCharUUID, data); err != nil {
		logger.Error(prefix, "❌ Failed to send profile request: %v", err)
		return
	}

	a.meshView.MarkProfileRequested(deviceID)
	logger.Debug(prefix, "📤 Sent profile request for v%d", version)
}
