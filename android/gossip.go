package android

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
func (a *Android) startGossipTimer() {
	// Send gossip every 5 seconds to all connected devices
	a.gossipTicker = time.NewTicker(5 * time.Second)

	go func() {
		for {
			select {
			case <-a.gossipTicker.C:
				a.sendGossipToConnected()
			case <-a.stopGossip:
				return
			}
		}
	}()

	logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "📡 Started gossip timer (interval: 5s)")
}

// sendGossipToConnected sends gossip to all currently connected devices
func (a *Android) sendGossipToConnected() {
	if !a.meshView.ShouldGossip() {
		return // Not time yet
	}

	// Get list of all connected peers (includes both Central and Peripheral connections)
	connectedPeerUUIDs := a.wire.GetConnectedPeers()
	if len(connectedPeerUUIDs) == 0 {
		return // No one to gossip with
	}

	// Build gossip message with our current mesh view
	a.mu.RLock()
	photoHash := a.photoHash
	profileVersion := a.profileVersion
	a.mu.RUnlock()

	gossipMsg := a.meshView.BuildGossipMessage(photoHash, profileVersion)

	// Marshal gossip message once
	data, err := proto.Marshal(gossipMsg)
	if err != nil {
		logger.Error(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Failed to marshal gossip: %v", err)
		return
	}

	// Send to all connected peers using appropriate method based on role
	sentCount := 0
	for _, peerUUID := range connectedPeerUUIDs {
		// Determine our role in this connection
		role, exists := a.wire.GetConnectionRole(peerUUID)
		if !exists {
			continue
		}

		var sendErr error
		if role == "central" {
			// We're Central - write to characteristic
			sendErr = a.wire.WriteCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
		} else {
			// We're Peripheral - send notification
			sendErr = a.wire.NotifyCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
		}

		if sendErr == nil {
			sentCount++
			// Log successful gossip send for audit trail
			if deviceID, exists := a.identityManager.GetDeviceID(peerUUID); exists {
				a.meshView.LogGossipSent(deviceID, len(gossipMsg.MeshView))
			}
		} else {
			logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)),
				"Failed to send gossip to %s (role: %s): %v", shortHash(peerUUID), role, sendErr)
		}
	}

	if sentCount > 0 {
		logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)),
			"📡 Sent gossip to %d peers (%d devices in mesh)",
			sentCount, len(gossipMsg.MeshView))
	}
}

// handleGossipMessage processes incoming gossip from a peer
func (a *Android) handleGossipMessage(peerUUID string, gossipMsg *pb.GossipMessage) {
	// Update what data this neighbor has (for multi-hop routing)
	a.meshView.UpdateNeighborData(gossipMsg.SenderDeviceId, peerUUID, gossipMsg)

	// Merge gossip into our mesh view
	newDevices := a.meshView.MergeGossip(gossipMsg)

	if len(newDevices) > 0 {
		logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)),
			"🌐 Discovered %d new devices via gossip from %s",
			len(newDevices), shortHash(gossipMsg.SenderDeviceId))
	}

	// Persist mesh view to disk after gossip merge
	if err := a.meshView.SaveToDisk(); err != nil {
		logger.Warn(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Failed to save mesh view: %v", err)
	}

	// Check for photos we need to request (MULTI-HOP ROUTING)
	missingPhotos := a.meshView.GetMissingPhotos()
	for _, device := range missingPhotos {
		// Find ANY connected neighbor who has this photo
		neighborsWithPhoto := a.meshView.FindNeighborsWithPhoto(device.PhotoHash)

		if len(neighborsWithPhoto) > 0 {
			// Request from the first neighbor who has it (could be owner or relay)
			neighborUUID := neighborsWithPhoto[0]
			logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)),
				"📸 Multi-hop: Requesting photo %s from neighbor %s (for device %s)",
				shortHash(device.PhotoHash), shortHash(neighborUUID), shortHash(device.DeviceID))

			a.meshView.MarkPhotoRequested(device.DeviceID)
			go a.requestAndReceivePhoto(neighborUUID, device.PhotoHash, device.DeviceID)
		} else {
			// Fallback: Try direct connection to owner if available
			if peerUUID, exists := a.identityManager.GetHardwareUUID(device.DeviceID); exists {
				if a.meshView.IsDeviceConnected(device.DeviceID) {
					logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)),
						"📸 Direct: Requesting photo %s from owner %s",
						shortHash(device.PhotoHash), shortHash(device.DeviceID))

					a.meshView.MarkPhotoRequested(device.DeviceID)
					go a.requestAndReceivePhoto(peerUUID, device.PhotoHash, device.DeviceID)
				}
			}
		}
	}

	// Check for profiles we need to update (MULTI-HOP ROUTING)
	outdatedProfiles := a.meshView.GetDevicesWithOutdatedProfiles()
	for _, device := range outdatedProfiles {
		// Find ANY connected neighbor who has this profile version
		neighborsWithProfile := a.meshView.FindNeighborsWithProfile(device.DeviceID, device.ProfileVersion)

		if len(neighborsWithProfile) > 0 {
			// Request from the first neighbor who has it (could be owner or relay)
			neighborUUID := neighborsWithProfile[0]
			logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)),
				"📋 Multi-hop: Requesting profile v%d for %s from neighbor %s",
				device.ProfileVersion, shortHash(device.DeviceID), shortHash(neighborUUID))

			go a.sendProfileRequest(neighborUUID, device.DeviceID)
		} else {
			// Fallback: Try direct connection to owner if available
			if peerUUID, exists := a.identityManager.GetHardwareUUID(device.DeviceID); exists {
				if a.meshView.IsDeviceConnected(device.DeviceID) {
					logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)),
						"📋 Direct: Requesting profile v%d from owner %s",
						device.ProfileVersion, shortHash(device.DeviceID))

					go a.sendProfileRequest(peerUUID, device.DeviceID)
				}
			}
		}
	}
}
