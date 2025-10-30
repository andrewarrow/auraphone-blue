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
	profileVersion := a.profileVersion
	a.mu.RUnlock()

	// Don't send ProfileMessage if profile is empty (no last_name set)
	// This prevents sending messages that can't be discriminated from HandshakeMessage
	if profile["last_name"] == "" {
		logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Skipping profile send to %s (profile not set)", shortHash(peerUUID))
		return
	}

	// Build ProfileMessage from map
	profileMsg := &pb.ProfileMessage{
		DeviceId:       deviceID,
		FirstName:      profile["first_name"],
		LastName:       profile["last_name"],
		PhoneNumber:    profile["phone_number"],
		Tagline:        profile["tagline"],
		Insta:          profile["insta"],
		Linkedin:       profile["linkedin"],
		Youtube:        profile["youtube"],
		Tiktok:         profile["tiktok"],
		Gmail:          profile["gmail"],
		Imessage:       profile["imessage"],
		Whatsapp:       profile["whatsapp"],
		Signal:         profile["signal"],
		Telegram:       profile["telegram"],
		ProfileVersion: profileVersion,
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

	logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "📋 Received profile v%d from %s (ID: %s, name: %s %s)",
		profileMsg.ProfileVersion, shortHash(peerUUID), profileMsg.DeviceId, profileMsg.FirstName, taglinePreview)

	// Store profile in DeviceCacheManager
	metadata := &phone.DeviceMetadata{
		FirstName:      profileMsg.FirstName,
		LastName:       profileMsg.LastName,
		Tagline:        profileMsg.Tagline,
		Insta:          profileMsg.Insta,
		LinkedIn:       profileMsg.Linkedin,
		YouTube:        profileMsg.Youtube,
		TikTok:         profileMsg.Tiktok,
		Gmail:          profileMsg.Gmail,
		IMessage:       profileMsg.Imessage,
		WhatsApp:       profileMsg.Whatsapp,
		Signal:         profileMsg.Signal,
		Telegram:       profileMsg.Telegram,
		ProfileVersion: profileMsg.ProfileVersion,
	}

	// Save to disk
	cacheManager := phone.NewDeviceCacheManager(a.hardwareUUID)
	if err := cacheManager.SaveDeviceMetadata(profileMsg.DeviceId, metadata); err != nil {
		logger.Warn(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Failed to save profile for %s: %v", profileMsg.DeviceId, err)
	}

	// Update mesh view with new profile info
	photoHashHex := "" // We don't have photo hash from profile message, leave empty to preserve existing
	if a.meshView != nil {
		a.meshView.UpdateDevice(profileMsg.DeviceId, photoHashHex, profileMsg.FirstName, profileMsg.ProfileVersion)
		// Persist mesh view to disk
		if err := a.meshView.SaveToDisk(); err != nil {
			logger.Warn(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Failed to save mesh view: %v", err)
		}
	}

	// Notify GUI of profile update
	a.mu.RLock()
	callback := a.callback
	if device, exists := a.discovered[peerUUID]; exists {
		// Update the device name with the new first_name from profile
		device.Name = profileMsg.FirstName
		a.discovered[peerUUID] = device

		if callback != nil {
			// Trigger GUI refresh with updated device info
			callbackDevice := device
			a.mu.RUnlock()
			callback(callbackDevice)
			return
		}
	}
	a.mu.RUnlock()
}

// handleProfileRequest sends a profile when another device requests it
// MULTI-HOP: Serves ANY cached profile, not just our own
func (a *Android) handleProfileRequest(peerUUID string, req *pb.ProfileRequestMessage) {
	logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)),
		"📋 Received profile request from %s for device %s (version: %d)",
		shortHash(peerUUID), shortHash(req.TargetDeviceId), req.ExpectedVersion)

	// Check if they're requesting our profile
	a.mu.RLock()
	ourDeviceID := a.deviceID
	a.mu.RUnlock()

	if req.TargetDeviceId == ourDeviceID {
		// They want our profile - send it
		logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)),
			"📋 Sending OUR profile to %s", shortHash(peerUUID))
		a.sendProfileMessage(peerUUID)
		return
	}

	// MULTI-HOP: They want someone else's profile - check if we have it cached
	cacheManager := phone.NewDeviceCacheManager(a.hardwareUUID)
	cachedProfile, err := cacheManager.LoadDeviceMetadata(req.TargetDeviceId)
	if err != nil || cachedProfile == nil {
		logger.Warn(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)),
			"⚠️  Don't have cached profile for %s", shortHash(req.TargetDeviceId))
		return
	}

	// Check if our cached version is sufficient
	if cachedProfile.ProfileVersion < req.ExpectedVersion {
		logger.Warn(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)),
			"⚠️  Have profile v%d for %s but they want v%d",
			cachedProfile.ProfileVersion, shortHash(req.TargetDeviceId), req.ExpectedVersion)
		return
	}

	// Send the cached profile (MULTI-HOP relay)
	logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)),
		"🔄 Multi-hop: Relaying profile v%d for %s to %s",
		cachedProfile.ProfileVersion, shortHash(req.TargetDeviceId), shortHash(peerUUID))

	a.sendCachedProfile(peerUUID, req.TargetDeviceId, cachedProfile)
}

// sendCachedProfile sends a cached profile (for multi-hop relay)
func (a *Android) sendCachedProfile(peerUUID string, targetDeviceID string, metadata *phone.DeviceMetadata) {
	// Build ProfileMessage from cached metadata
	profileMsg := &pb.ProfileMessage{
		DeviceId:       targetDeviceID,
		FirstName:      metadata.FirstName,
		LastName:       metadata.LastName,
		PhoneNumber:    "",
		Tagline:        metadata.Tagline,
		Insta:          metadata.Insta,
		Linkedin:       metadata.LinkedIn,
		Youtube:        metadata.YouTube,
		Tiktok:         metadata.TikTok,
		Gmail:          metadata.Gmail,
		Imessage:       metadata.IMessage,
		Whatsapp:       metadata.WhatsApp,
		Signal:         metadata.Signal,
		Telegram:       metadata.Telegram,
		ProfileVersion: metadata.ProfileVersion,
	}

	data, err := proto.Marshal(profileMsg)
	if err != nil {
		logger.Error(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Failed to marshal cached profile: %v", err)
		return
	}

	// Determine if we're acting as Central or Peripheral for this connection
	a.mu.RLock()
	gatt, isCentral := a.connectedGatts[peerUUID]
	a.mu.RUnlock()

	// Send profile via appropriate method based on our role
	var err2 error
	if isCentral && gatt != nil {
		// We're Central - write to characteristic
		char := gatt.GetCharacteristic(phone.AuraServiceUUID, phone.AuraProtocolCharUUID)
		if char != nil {
			char.Value = data
			gatt.WriteCharacteristic(char)
		}
	} else {
		// We're Peripheral - send notification
		err2 = a.wire.NotifyCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
	}

	if err2 != nil {
		logger.Error(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Failed to send cached profile to %s: %v", shortHash(peerUUID), err2)
	} else {
		logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "📤 Sent cached profile for %s to %s (%d bytes)",
			shortHash(targetDeviceID), shortHash(peerUUID), len(data))
	}
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
