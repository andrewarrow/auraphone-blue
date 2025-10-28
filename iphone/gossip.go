package iphone

import (
	"fmt"
	"time"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	pb "github.com/user/auraphone-blue/proto"
	"google.golang.org/protobuf/proto"
)

// ============================================================================
// Gossip Protocol Implementation
// ============================================================================

// startGossipTimer starts periodic gossip broadcasts
func (ip *IPhone) startGossipTimer() {
	// Send gossip every 5 seconds to all connected devices
	ip.gossipTicker = time.NewTicker(5 * time.Second)

	go func() {
		for {
			select {
			case <-ip.gossipTicker.C:
				ip.sendGossipToConnected()
			case <-ip.stopGossip:
				return
			}
		}
	}()

	logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📡 Started gossip timer (interval: 5s)")
}

// sendGossipToConnected sends gossip to all currently connected devices
func (ip *IPhone) sendGossipToConnected() {
	if !ip.meshView.ShouldGossip() {
		return // Not time yet
	}

	// Get list of all connected peers (includes both Central and Peripheral connections)
	connectedPeerUUIDs := ip.wire.GetConnectedPeers()
	if len(connectedPeerUUIDs) == 0 {
		return // No one to gossip with
	}

	// Build gossip message with our current mesh view
	ip.mu.RLock()
	photoHash := ip.photoHash
	profileVersion := ip.profileVersion
	ip.mu.RUnlock()

	gossipMsg := ip.meshView.BuildGossipMessage(photoHash, profileVersion)

	// Marshal gossip message once
	data, err := proto.Marshal(gossipMsg)
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Failed to marshal gossip: %v", err)
		return
	}

	// Send to all connected peers using appropriate method based on role
	sentCount := 0
	for _, peerUUID := range connectedPeerUUIDs {
		// Determine our role in this connection
		role, exists := ip.wire.GetConnectionRole(peerUUID)
		if !exists {
			continue
		}

		var sendErr error
		if role == "central" {
			// We're Central - write to characteristic
			sendErr = ip.wire.WriteCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
		} else {
			// We're Peripheral - send notification
			sendErr = ip.wire.NotifyCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
		}

		if sendErr == nil {
			sentCount++
			// Log successful gossip send for audit trail
			if deviceID, exists := ip.identityManager.GetDeviceID(peerUUID); exists {
				ip.meshView.LogGossipSent(deviceID, len(gossipMsg.MeshView))
			}
		} else {
			logger.Debug(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)),
				"Failed to send gossip to %s (role: %s): %v", shortHash(peerUUID), role, sendErr)
		}
	}

	if sentCount > 0 {
		logger.Debug(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)),
			"📡 Sent gossip to %d peers (%d devices in mesh)",
			sentCount, len(gossipMsg.MeshView))
	}
}

// handleGossipMessage processes incoming gossip from a peer
func (ip *IPhone) handleGossipMessage(peerUUID string, data []byte) {
	var gossipMsg pb.GossipMessage
	err := proto.Unmarshal(data, &gossipMsg)
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Failed to unmarshal gossip: %v", err)
		return
	}

	// Update what data this neighbor has (for multi-hop routing)
	ip.meshView.UpdateNeighborData(gossipMsg.SenderDeviceId, peerUUID, &gossipMsg)

	// Merge gossip into our mesh view
	newDevices := ip.meshView.MergeGossip(&gossipMsg)

	if len(newDevices) > 0 {
		logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)),
			"🌐 Discovered %d new devices via gossip from %s",
			len(newDevices), shortHash(gossipMsg.SenderDeviceId))
	}

	// Persist mesh view to disk after gossip merge
	if err := ip.meshView.SaveToDisk(); err != nil {
		logger.Warn(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Failed to save mesh view: %v", err)
	}

	// Check for photos we need to request (MULTI-HOP ROUTING)
	missingPhotos := ip.meshView.GetMissingPhotos()
	for _, device := range missingPhotos {
		// Find ANY connected neighbor who has this photo
		neighborsWithPhoto := ip.meshView.FindNeighborsWithPhoto(device.PhotoHash)

		if len(neighborsWithPhoto) > 0 {
			// Request from the first neighbor who has it (could be owner or relay)
			neighborUUID := neighborsWithPhoto[0]
			logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)),
				"📸 Multi-hop: Requesting photo %s from neighbor %s (for device %s)",
				shortHash(device.PhotoHash), shortHash(neighborUUID), shortHash(device.DeviceID))

			ip.meshView.MarkPhotoRequested(device.DeviceID)
			go ip.requestAndReceivePhoto(neighborUUID, device.PhotoHash, device.DeviceID)
		} else {
			// Fallback: Try direct connection to owner if available
			if peerUUID, exists := ip.identityManager.GetHardwareUUID(device.DeviceID); exists {
				if ip.meshView.IsDeviceConnected(device.DeviceID) {
					logger.Debug(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)),
						"📸 Direct: Requesting photo %s from owner %s",
						shortHash(device.PhotoHash), shortHash(device.DeviceID))

					ip.meshView.MarkPhotoRequested(device.DeviceID)
					go ip.requestAndReceivePhoto(peerUUID, device.PhotoHash, device.DeviceID)
				}
			}
		}
	}

	// Check for profiles we need to update (MULTI-HOP ROUTING)
	outdatedProfiles := ip.meshView.GetDevicesWithOutdatedProfiles()
	for _, device := range outdatedProfiles {
		// Find ANY connected neighbor who has this profile version
		neighborsWithProfile := ip.meshView.FindNeighborsWithProfile(device.DeviceID, device.ProfileVersion)

		if len(neighborsWithProfile) > 0 {
			// Request from the first neighbor who has it (could be owner or relay)
			neighborUUID := neighborsWithProfile[0]
			logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)),
				"📋 Multi-hop: Requesting profile v%d for %s from neighbor %s",
				device.ProfileVersion, shortHash(device.DeviceID), shortHash(neighborUUID))

			go ip.sendProfileRequest(neighborUUID, device.DeviceID)
		} else {
			// Fallback: Try direct connection to owner if available
			if peerUUID, exists := ip.identityManager.GetHardwareUUID(device.DeviceID); exists {
				if ip.meshView.IsDeviceConnected(device.DeviceID) {
					logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)),
						"📋 Direct: Requesting profile v%d from owner %s",
						device.ProfileVersion, shortHash(device.DeviceID))

					go ip.sendProfileRequest(peerUUID, device.DeviceID)
				}
			}
		}
	}
}
