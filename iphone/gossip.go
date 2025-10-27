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

	// Get list of connected peers
	ip.mu.RLock()
	connectedPeers := make([]string, 0, len(ip.connectedPeers))
	for peerUUID := range ip.connectedPeers {
		// Get deviceID from hardware UUID
		if deviceID, exists := ip.identityManager.GetDeviceID(peerUUID); exists {
			connectedPeers = append(connectedPeers, deviceID)
		}
	}
	ip.mu.RUnlock()

	if len(connectedPeers) == 0 {
		return // No one to gossip with
	}

	// Build gossip message with our current mesh view
	ip.mu.RLock()
	photoHash := ip.photoHash
	ip.mu.RUnlock()

	gossipMsg := ip.meshView.BuildGossipMessage(photoHash)

	// Send to all connected peers
	data, err := proto.Marshal(gossipMsg)
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Failed to marshal gossip: %v", err)
		return
	}

	sentCount := 0
	for _, peerDeviceID := range connectedPeers {
		// Get hardware UUID from device ID
		if peerUUID, exists := ip.identityManager.GetHardwareUUID(peerDeviceID); exists {
			err := ip.wire.WriteCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
			if err == nil {
				sentCount++
			}
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

	// Merge gossip into our mesh view
	newDevices := ip.meshView.MergeGossip(&gossipMsg)

	if len(newDevices) > 0 {
		logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)),
			"🌐 Discovered %d new devices via gossip from %s",
			len(newDevices), shortHash(gossipMsg.SenderDeviceId))
	}

	// Check for photos we need to request
	missingPhotos := ip.meshView.GetMissingPhotos()
	for _, device := range missingPhotos {
		// Only request from directly connected devices for now
		// TODO: Multi-hop photo routing via gossip
		if peerUUID, exists := ip.identityManager.GetHardwareUUID(device.DeviceID); exists {
			if ip.meshView.IsDeviceConnected(device.DeviceID) {
				logger.Debug(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)),
					"📸 Requesting photo %s from %s (learned via gossip)",
					shortHash(device.PhotoHash), shortHash(device.DeviceID))

				ip.meshView.MarkPhotoRequested(device.DeviceID)
				go ip.requestAndReceivePhoto(peerUUID, device.PhotoHash, device.DeviceID)
			}
		}
	}
}
