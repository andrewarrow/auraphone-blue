package android

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/proto"
	proto2 "google.golang.org/protobuf/proto"
)

// Profile exchange handling

// handleProfileMessage receives profile data from either Central or Peripheral mode
func (a *Android) handleProfileMessage(senderUUID string, data []byte) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	// Unmarshal profile message
	profileMsg := &proto.ProfileMessage{}
	if err := proto2.Unmarshal(data, profileMsg); err != nil {
		logger.Error(prefix, "❌ Failed to unmarshal profile from %s: %v", senderUUID[:8], err)
		return
	}

	deviceID := profileMsg.DeviceId
	logger.Info(prefix, "📝 Received profile from %s (deviceID: %s)", senderUUID[:8], deviceID[:8])

	// Save device metadata
	metadata, _ := a.cacheManager.LoadDeviceMetadata(deviceID)
	if metadata == nil {
		metadata = &phone.DeviceMetadata{
			DeviceID: deviceID,
		}
	}

	// Note: FirstName comes from gossip, not ProfileMessage
	metadata.LastName = profileMsg.LastName
	metadata.Tagline = profileMsg.Tagline
	metadata.Insta = profileMsg.Insta
	metadata.LinkedIn = profileMsg.Linkedin
	metadata.YouTube = profileMsg.Youtube
	metadata.TikTok = profileMsg.Tiktok
	metadata.Gmail = profileMsg.Gmail
	metadata.IMessage = profileMsg.Imessage
	metadata.WhatsApp = profileMsg.Whatsapp
	metadata.Signal = profileMsg.Signal
	metadata.Telegram = profileMsg.Telegram

	if err := a.cacheManager.SaveDeviceMetadata(metadata); err != nil {
		logger.Error(prefix, "❌ Failed to save metadata for %s: %v", deviceID[:8], err)
		return
	}

	// Update receivedProfileVersion
	// Note: ProfileMessage doesn't have version field, we'd need to add it
	// For now, mark as received in mesh view
	a.meshView.MarkProfileReceived(deviceID, 1) // TODO: Use actual version

	logger.Debug(prefix, "✅ Saved profile for %s", deviceID[:8])

	// Trigger discovery callback to update GUI
	if a.discoveryCallback != nil {
		name := deviceID[:8]
		if metadata.FirstName != "" {
			name = metadata.FirstName
		}

		a.discoveryCallback(phone.DiscoveredDevice{
			DeviceID:     deviceID,
			HardwareUUID: senderUUID,
			Name:         name,
			RSSI:         -50,
			Platform:     "unknown",
			PhotoHash:    "",
		})
	}
}

// handleProfileRequest handles a request for our profile
func (a *Android) handleProfileRequest(senderUUID string, req *proto.ProfileRequestMessage) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	// Check if they're requesting OUR profile
	if req.TargetDeviceId != a.deviceID {
		logger.Debug(prefix, "⏭️  Profile request for %s, not us", req.TargetDeviceId[:8])
		return
	}

	logger.Info(prefix, "📝 Sending our profile to %s (they want v%d)", senderUUID[:8], req.ExpectedVersion)

	if err := a.sendProfileToDevice(senderUUID); err != nil {
		logger.Error(prefix, "❌ Failed to send profile: %v", err)
	}
}

// sendProfileToDevice sends our profile in response to a request
func (a *Android) sendProfileToDevice(targetUUID string) error {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	profileMsg := &proto.ProfileMessage{
		DeviceId:    a.deviceID,
		LastName:    a.localProfile.LastName,
		Tagline:     a.localProfile.Tagline,
		Insta:       a.localProfile.Insta,
		Linkedin:    a.localProfile.LinkedIn,
		Youtube:     a.localProfile.YouTube,
		Tiktok:      a.localProfile.TikTok,
		Gmail:       a.localProfile.Gmail,
		Imessage:    a.localProfile.IMessage,
		Whatsapp:    a.localProfile.WhatsApp,
		Signal:      a.localProfile.Signal,
		Telegram:    a.localProfile.Telegram,
	}

	data, err := proto2.Marshal(profileMsg)
	if err != nil {
		return fmt.Errorf("failed to marshal profile: %w", err)
	}

	if err := a.connManager.SendToDevice(targetUUID, phone.AuraProfileCharUUID, data); err != nil {
		return fmt.Errorf("failed to send profile: %w", err)
	}

	logger.Debug(prefix, "📤 Sent profile to %s (v%d)", targetUUID[:8], a.localProfile.ProfileVersion)
	return nil
}
