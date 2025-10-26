package phone

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/proto"
	proto2 "google.golang.org/protobuf/proto"
)

// HandleProfileMessage receives profile data from either Central or Peripheral mode
func (ph *ProfileHandler) HandleProfileMessage(senderUUID string, data []byte) {
	device := ph.device
	prefix := fmt.Sprintf("%s %s", device.GetHardwareUUID()[:8], device.GetPlatform())

	// Unmarshal profile message
	profileMsg := &proto.ProfileMessage{}
	if err := proto2.Unmarshal(data, profileMsg); err != nil {
		logger.Error(prefix, "❌ Failed to unmarshal profile from %s: %v", senderUUID[:8], err)
		return
	}

	deviceID := profileMsg.DeviceId
	logger.Info(prefix, "📝 Received profile from %s (deviceID: %s)", senderUUID[:8], deviceID[:8])

	// Save device metadata
	cacheManager := device.GetCacheManager()
	metadata, _ := cacheManager.LoadDeviceMetadata(deviceID)
	if metadata == nil {
		metadata = &DeviceMetadata{
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

	if err := cacheManager.SaveDeviceMetadata(metadata); err != nil {
		logger.Error(prefix, "❌ Failed to save metadata for %s: %v", deviceID[:8], err)
		return
	}

	// Update receivedProfileVersion
	// Note: ProfileMessage doesn't have version field, we'd need to add it
	// For now, mark as received in mesh view
	meshView := device.GetMeshView()
	meshView.MarkProfileReceived(deviceID, 1) // TODO: Use actual version

	logger.Debug(prefix, "✅ Saved profile for %s", deviceID[:8])

	// Note: Triggering discovery callback is platform-specific
	// iOS and Android will handle this in their own code after calling this handler
}

// HandleProfileRequest handles a request for our profile
func (ph *ProfileHandler) HandleProfileRequest(senderUUID string, req *proto.ProfileRequestMessage) {
	device := ph.device
	prefix := fmt.Sprintf("%s %s", device.GetHardwareUUID()[:8], device.GetPlatform())

	// Check if they're requesting OUR profile
	if req.TargetDeviceId != device.GetDeviceID() {
		logger.Debug(prefix, "⏭️  Profile request for %s, not us", req.TargetDeviceId[:8])
		return
	}

	logger.Info(prefix, "📝 Sending our profile to %s (they want v%d)", senderUUID[:8], req.ExpectedVersion)

	if err := ph.SendProfileToDevice(senderUUID); err != nil {
		logger.Error(prefix, "❌ Failed to send profile: %v", err)
	}
}

// SendProfileToDevice sends our profile in response to a request
func (ph *ProfileHandler) SendProfileToDevice(targetUUID string) error {
	device := ph.device
	prefix := fmt.Sprintf("%s %s", device.GetHardwareUUID()[:8], device.GetPlatform())

	localProfile := device.GetLocalProfile()
	profileMsg := &proto.ProfileMessage{
		DeviceId: device.GetDeviceID(),
		LastName: localProfile.LastName,
		Tagline:  localProfile.Tagline,
		Insta:    localProfile.Insta,
		Linkedin: localProfile.LinkedIn,
		Youtube:  localProfile.YouTube,
		Tiktok:   localProfile.TikTok,
		Gmail:    localProfile.Gmail,
		Imessage: localProfile.IMessage,
		Whatsapp: localProfile.WhatsApp,
		Signal:   localProfile.Signal,
		Telegram: localProfile.Telegram,
	}

	data, err := proto2.Marshal(profileMsg)
	if err != nil {
		return fmt.Errorf("failed to marshal profile: %w", err)
	}

	connManager := device.GetConnManager()
	if err := connManager.SendToDevice(targetUUID, AuraProfileCharUUID, data); err != nil {
		return fmt.Errorf("failed to send profile: %w", err)
	}

	logger.Debug(prefix, "📤 Sent profile to %s (v%d)", targetUUID[:8], localProfile.ProfileVersion)
	return nil
}
