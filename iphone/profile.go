package iphone

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	pb "github.com/user/auraphone-blue/proto"
	"google.golang.org/protobuf/proto"
)

// ============================================================================
// Profile Exchange Protocol
// ============================================================================

// sendProfileMessage sends our complete profile to a peer
func (ip *IPhone) sendProfileMessage(peerUUID string) {
	ip.mu.RLock()
	profile := make(map[string]string)
	for k, v := range ip.profile {
		profile[k] = v
	}
	deviceID := ip.deviceID
	ip.mu.RUnlock()

	// Don't send ProfileMessage if profile is empty (no last_name set)
	// This prevents sending messages that can't be discriminated from HandshakeMessage
	if profile["last_name"] == "" {
		logger.Debug(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Skipping profile send to %s (profile not set)", shortHash(peerUUID))
		return
	}

	// Build ProfileMessage from map
	profileMsg := &pb.ProfileMessage{
		DeviceId:    deviceID,
		LastName:    profile["last_name"],
		PhoneNumber: profile["phone_number"],
		Tagline:     profile["tagline"],
		Insta:       profile["insta"],
		Linkedin:    profile["linkedin"],
		Youtube:     profile["youtube"],
		Tiktok:      profile["tiktok"],
		Gmail:       profile["gmail"],
		Imessage:    profile["imessage"],
		Whatsapp:    profile["whatsapp"],
		Signal:      profile["signal"],
		Telegram:    profile["telegram"],
	}

	data, err := proto.Marshal(profileMsg)
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Failed to marshal profile message: %v", err)
		return
	}

	// Determine if we're acting as Central or Peripheral for this connection
	ip.mu.RLock()
	peripheral := ip.connectedPeers[peerUUID]
	ip.mu.RUnlock()

	// Send profile via appropriate method based on our role
	var err2 error
	if peripheral != nil {
		// We're Central - write to characteristic
		err2 = ip.wire.WriteCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
	} else {
		// We're Peripheral - send notification (realistic BLE behavior)
		err2 = ip.wire.NotifyCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
	}

	if err2 != nil {
		logger.Error(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Failed to send profile to %s: %v", shortHash(peerUUID), err2)
	} else {
		logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📋 Sent profile to %s", shortHash(peerUUID))
	}
}

// handleProfileMessage receives and stores a profile from a peer
func (ip *IPhone) handleProfileMessage(peerUUID string, profileMsg *pb.ProfileMessage) {
	logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📋 Received profile from %s (ID: %s, name: %s %s)",
		shortHash(peerUUID), profileMsg.DeviceId, profileMsg.LastName, profileMsg.Tagline[:min(20, len(profileMsg.Tagline))])

	// Store profile in DeviceCacheManager
	metadata := &phone.DeviceMetadata{
		LastName: profileMsg.LastName,
		Tagline:  profileMsg.Tagline,
		Insta:    profileMsg.Insta,
		LinkedIn: profileMsg.Linkedin,
		YouTube:  profileMsg.Youtube,
		TikTok:   profileMsg.Tiktok,
		Gmail:    profileMsg.Gmail,
		IMessage: profileMsg.Imessage,
		WhatsApp: profileMsg.Whatsapp,
		Signal:   profileMsg.Signal,
		Telegram: profileMsg.Telegram,
	}

	// Save to disk
	cacheManager := phone.NewDeviceCacheManager(ip.hardwareUUID)
	if err := cacheManager.SaveDeviceMetadata(profileMsg.DeviceId, metadata); err != nil {
		logger.Warn(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Failed to save profile for %s: %v", profileMsg.DeviceId, err)
	}

	// TODO: Notify GUI of profile update
}

// handleProfileRequest sends our profile when another device requests it
func (ip *IPhone) handleProfileRequest(peerUUID string, req *pb.ProfileRequestMessage) {
	// Check if they're requesting our profile
	ip.mu.RLock()
	ourDeviceID := ip.deviceID
	ip.mu.RUnlock()

	if req.TargetDeviceId != ourDeviceID {
		logger.Warn(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)),
			"Received profile request for %s but we are %s",
			req.TargetDeviceId, ourDeviceID)
		return
	}

	logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)),
		"📋 Received profile request from %s - sending profile",
		shortHash(peerUUID))

	// Send our profile
	ip.sendProfileMessage(peerUUID)
}

// sendProfileRequest requests a profile from a device
func (ip *IPhone) sendProfileRequest(peerUUID string, targetDeviceID string) {
	ip.mu.RLock()
	ourDeviceID := ip.deviceID
	ip.mu.RUnlock()

	req := &pb.ProfileRequestMessage{
		RequesterDeviceId: ourDeviceID,
		TargetDeviceId:    targetDeviceID,
		ExpectedVersion:   0, // We'll request the latest version
	}

	data, err := proto.Marshal(req)
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Failed to marshal profile request: %v", err)
		return
	}

	// Write to peer's AuraProtocolCharUUID
	err = ip.wire.WriteCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Failed to send profile request to %s: %v", shortHash(peerUUID), err)
	} else {
		logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📋 Sent profile request to %s for device %s",
			shortHash(peerUUID), targetDeviceID[:8])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
