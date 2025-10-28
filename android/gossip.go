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

	// Get list of connected peers
	a.mu.RLock()
	connectedPeers := make([]string, 0, len(a.connectedGatts))
	for peerUUID := range a.connectedGatts {
		// Get deviceID from hardware UUID
		if deviceID, exists := a.identityManager.GetDeviceID(peerUUID); exists {
			connectedPeers = append(connectedPeers, deviceID)
		}
	}
	a.mu.RUnlock()

	if len(connectedPeers) == 0 {
		return // No one to gossip with
	}

	// Build gossip message with our current mesh view
	a.mu.RLock()
	photoHash := a.photoHash
	profileVersion := a.profileVersion
	a.mu.RUnlock()

	gossipMsg := a.meshView.BuildGossipMessage(photoHash, profileVersion)

	// Send to all connected peers
	data, err := proto.Marshal(gossipMsg)
	if err != nil {
		logger.Error(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Failed to marshal gossip: %v", err)
		return
	}

	sentCount := 0
	for _, peerDeviceID := range connectedPeers {
		// Get hardware UUID from device ID
		if peerUUID, exists := a.identityManager.GetHardwareUUID(peerDeviceID); exists {
			err := a.wire.WriteCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
			if err == nil {
				sentCount++
			}
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
	// Merge gossip into our mesh view
	newDevices := a.meshView.MergeGossip(gossipMsg)

	if len(newDevices) > 0 {
		logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)),
			"🌐 Discovered %d new devices via gossip from %s",
			len(newDevices), shortHash(gossipMsg.SenderDeviceId))
	}

	// Check for photos we need to request
	missingPhotos := a.meshView.GetMissingPhotos()
	for _, device := range missingPhotos {
		// Only request from directly connected devices for now
		// TODO: Multi-hop photo routing via gossip
		if peerUUID, exists := a.identityManager.GetHardwareUUID(device.DeviceID); exists {
			if a.meshView.IsDeviceConnected(device.DeviceID) {
				logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)),
					"📸 Requesting photo %s from %s (learned via gossip)",
					shortHash(device.PhotoHash), shortHash(device.DeviceID))

				a.meshView.MarkPhotoRequested(device.DeviceID)
				go a.requestAndReceivePhoto(peerUUID, device.PhotoHash, device.DeviceID)
			}
		}
	}

	// Check for profiles we need to update
	outdatedProfiles := a.meshView.GetDevicesWithOutdatedProfiles()
	for _, device := range outdatedProfiles {
		// Only request from directly connected devices
		if peerUUID, exists := a.identityManager.GetHardwareUUID(device.DeviceID); exists {
			if a.meshView.IsDeviceConnected(device.DeviceID) {
				logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)),
					"📋 Requesting updated profile v%d for %s (learned via gossip)",
					device.ProfileVersion, shortHash(device.DeviceID))

				go a.sendProfileRequest(peerUUID, device.DeviceID)
			}
		}
	}
}
