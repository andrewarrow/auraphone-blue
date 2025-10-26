package iphone

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
func (ip *iPhone) gossipLoop() {
	ticker := time.NewTicker(ip.gossipInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ip.sendGossipToNeighbors()
		case <-ip.staleCheckDone:
			return
		}
	}
}

// sendGossipToNeighbors broadcasts our mesh view to all connected neighbors
func (ip *iPhone) sendGossipToNeighbors() {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])

	// Select neighbors using mesh view (deterministic, max 3)
	neighbors := ip.meshView.SelectRandomNeighbors()

	// Map deviceIDs to hardware UUIDs
	ip.mu.RLock()
	deviceIDToUUID := make(map[string]string)
	for uuid, deviceID := range ip.peripheralToDeviceID {
		deviceIDToUUID[deviceID] = uuid
	}
	ip.mu.RUnlock()

	// Calculate profile summary hash
	profileSummaryHashBytes := ip.calculateProfileSummaryHash()
	profileSummaryHashHex := phone.HashBytesToString(profileSummaryHashBytes)

	// Build gossip message with our current state
	gossip := ip.meshView.BuildGossipMessage(
		ip.photoHash,
		ip.localProfile.FirstName,
		ip.localProfile.ProfileVersion,
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

		if !ip.connManager.IsConnected(neighborUUID) {
			continue // Not currently connected
		}

		if err := ip.connManager.SendToDevice(neighborUUID, phone.AuraProtocolCharUUID, data); err != nil {
			logger.Warn(prefix, "⚠️  Failed to send gossip to %s: %v", neighborUUID[:8], err)
		} else {
			sentCount++
		}
	}

	logger.Debug(prefix, "📢 Sent gossip to %d/%d neighbors", sentCount, len(neighbors))

	// Save mesh view periodically
	ip.meshView.SaveToDisk()
}

// sendGossipToDevice sends gossip to a specific device (used after connection)
func (ip *iPhone) sendGossipToDevice(remoteUUID string) {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])

	profileSummaryHashBytes := ip.calculateProfileSummaryHash()
	profileSummaryHashHex := phone.HashBytesToString(profileSummaryHashBytes)

	gossip := ip.meshView.BuildGossipMessage(
		ip.photoHash,
		ip.localProfile.FirstName,
		ip.localProfile.ProfileVersion,
		profileSummaryHashHex,
	)

	data, err := proto2.Marshal(gossip)
	if err != nil {
		logger.Error(prefix, "❌ Failed to marshal gossip: %v", err)
		return
	}

	if err := ip.connManager.SendToDevice(remoteUUID, phone.AuraProtocolCharUUID, data); err != nil {
		logger.Warn(prefix, "⚠️  Failed to send initial gossip to %s: %v", remoteUUID[:8], err)
	} else {
		logger.Debug(prefix, "📤 Sent initial gossip to %s", remoteUUID[:8])
	}
}

// calculateProfileSummaryHash computes SHA-256 hash of all profile fields
func (ip *iPhone) calculateProfileSummaryHash() []byte {
	h := sha256.New()
	h.Write([]byte(ip.localProfile.LastName))
	h.Write([]byte(ip.localProfile.Tagline))
	h.Write([]byte(ip.localProfile.Insta))
	h.Write([]byte(ip.localProfile.LinkedIn))
	h.Write([]byte(ip.localProfile.YouTube))
	h.Write([]byte(ip.localProfile.TikTok))
	h.Write([]byte(ip.localProfile.Gmail))
	h.Write([]byte(ip.localProfile.IMessage))
	h.Write([]byte(ip.localProfile.WhatsApp))
	h.Write([]byte(ip.localProfile.Signal))
	h.Write([]byte(ip.localProfile.Telegram))
	return h.Sum(nil)
}

// requestPhoto requests a photo from a device (triggered by gossip)
func (ip *iPhone) requestPhoto(deviceID, photoHash string) {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
	logger.Info(prefix, "📸 Requesting photo %s from device %s (via gossip)", phone.TruncateHash(photoHash, 8), deviceID[:8])

	// Build PhotoRequestMessage
	hashBytes, _ := hex.DecodeString(photoHash)
	req := &proto.PhotoRequestMessage{
		RequesterDeviceId: ip.deviceID,
		TargetDeviceId:    deviceID,
		PhotoHash:         hashBytes,
	}

	data, err := proto2.Marshal(req)
	if err != nil {
		logger.Error(prefix, "❌ Failed to marshal photo request: %v", err)
		return
	}

	// Find hardware UUID for this deviceID
	ip.mu.RLock()
	var targetUUID string
	for uuid, devID := range ip.peripheralToDeviceID {
		if devID == deviceID {
			targetUUID = uuid
			break
		}
	}
	ip.mu.RUnlock()

	if targetUUID == "" {
		logger.Warn(prefix, "⚠️  Cannot request photo: don't know hardware UUID for device %s", deviceID[:8])
		return
	}

	// Send request
	if err := ip.connManager.SendToDevice(targetUUID, phone.AuraProtocolCharUUID, data); err != nil {
		logger.Error(prefix, "❌ Failed to send photo request: %v", err)
		return
	}

	ip.meshView.MarkPhotoRequested(deviceID)
	logger.Debug(prefix, "📤 Sent photo request for %s", phone.TruncateHash(photoHash, 8))
}

// requestProfile requests a profile from a device (triggered by gossip)
func (ip *iPhone) requestProfile(deviceID string, version int32) {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
	logger.Info(prefix, "📝 Requesting profile v%d from device %s (via gossip)", version, deviceID[:8])

	// Build ProfileRequestMessage
	req := &proto.ProfileRequestMessage{
		RequesterDeviceId: ip.deviceID,
		TargetDeviceId:    deviceID,
		ExpectedVersion:   version,
	}

	data, err := proto2.Marshal(req)
	if err != nil {
		logger.Error(prefix, "❌ Failed to marshal profile request: %v", err)
		return
	}

	// Find hardware UUID for this deviceID
	ip.mu.RLock()
	var targetUUID string
	for uuid, devID := range ip.peripheralToDeviceID {
		if devID == deviceID {
			targetUUID = uuid
			break
		}
	}
	ip.mu.RUnlock()

	if targetUUID == "" {
		logger.Warn(prefix, "⚠️  Cannot request profile: don't know hardware UUID for device %s", deviceID[:8])
		return
	}

	// Send request
	if err := ip.connManager.SendToDevice(targetUUID, phone.AuraProtocolCharUUID, data); err != nil {
		logger.Error(prefix, "❌ Failed to send profile request: %v", err)
		return
	}

	ip.meshView.MarkProfileRequested(deviceID)
	logger.Debug(prefix, "📤 Sent profile request for v%d", version)
}
