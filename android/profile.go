package android

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
func (a *Android) sendProfileMessage(peerUUID string) {
	a.mu.RLock()
	profile := make(map[string]string)
	for k, v := range a.profile {
		profile[k] = v
	}
	deviceID := a.deviceID
	a.mu.RUnlock()

	// Don't send ProfileMessage if profile is empty (no last_name set)
	// This prevents sending messages that can't be discriminated from HandshakeMessage
	if profile["last_name"] == "" {
		logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Skipping profile send to %s (profile not set)", shortHash(peerUUID))
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
		logger.Error(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Failed to marshal profile message: %v", err)
		return
	}

	// Determine if we're acting as Central or Peripheral for this connection
	a.mu.RLock()
	gatt := a.connectedGatts[peerUUID]
	a.mu.RUnlock()

	// Send profile via appropriate method based on our role
	var err2 error
	if gatt != nil {
		// We're Central - write to characteristic
		err2 = a.wire.WriteCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
	} else {
		// We're Peripheral - send notification
		err2 = a.wire.NotifyCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
	}

	if err2 != nil {
		logger.Error(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Failed to send profile to %s: %v", shortHash(peerUUID), err2)
	} else {
		logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "📋 Sent profile to %s", shortHash(peerUUID))
	}
}

// handleProfileMessage receives and stores a profile from a peer
func (a *Android) handleProfileMessage(peerUUID string, profileMsg *pb.ProfileMessage) {
	taglinePreview := profileMsg.Tagline
	if len(taglinePreview) > 20 {
		taglinePreview = taglinePreview[:20]
	}

	logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "📋 Received profile from %s (ID: %s, name: %s %s)",
		shortHash(peerUUID), profileMsg.DeviceId, profileMsg.LastName, taglinePreview)

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
	cacheManager := phone.NewDeviceCacheManager(a.hardwareUUID)
	if err := cacheManager.SaveDeviceMetadata(profileMsg.DeviceId, metadata); err != nil {
		logger.Warn(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Failed to save profile for %s: %v", profileMsg.DeviceId, err)
	}

	// TODO: Notify GUI of profile update
}

// handleProfileRequest sends our profile when another device requests it
func (a *Android) handleProfileRequest(peerUUID string, req *pb.ProfileRequestMessage) {
	// Check if they're requesting our profile
	a.mu.RLock()
	ourDeviceID := a.deviceID
	a.mu.RUnlock()

	if req.TargetDeviceId != ourDeviceID {
		logger.Warn(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)),
			"Received profile request for %s but we are %s",
			req.TargetDeviceId, ourDeviceID)
		return
	}

	logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)),
		"📋 Received profile request from %s - sending profile",
		shortHash(peerUUID))

	// Send our profile
	a.sendProfileMessage(peerUUID)
}

// sendProfileRequest requests a profile from a device
func (a *Android) sendProfileRequest(peerUUID string, targetDeviceID string) {
	a.mu.RLock()
	ourDeviceID := a.deviceID
	a.mu.RUnlock()

	req := &pb.ProfileRequestMessage{
		RequesterDeviceId: ourDeviceID,
		TargetDeviceId:    targetDeviceID,
		ExpectedVersion:   0, // We'll request the latest version
	}

	data, err := proto.Marshal(req)
	if err != nil {
		logger.Error(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Failed to marshal profile request: %v", err)
		return
	}

	// Write to peer's AuraProtocolCharUUID
	err = a.wire.WriteCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
	if err != nil {
		logger.Error(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Failed to send profile request to %s: %v", shortHash(peerUUID), err)
	} else {
		logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "📋 Sent profile request to %s for device %s",
			shortHash(peerUUID), targetDeviceID[:8])
	}
}
