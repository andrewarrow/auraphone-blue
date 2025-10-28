package android

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	pb "github.com/user/auraphone-blue/proto"
	"google.golang.org/protobuf/proto"
)

// broadcastProfileUpdate sends profile update to all connected peers
func (a *Android) broadcastProfileUpdate() {
	a.mu.RLock()
	deviceID := a.deviceID
	profile := make(map[string]string)
	for k, v := range a.profile {
		profile[k] = v
	}
	a.mu.RUnlock()

	// Get ALL connected peers (both Central and Peripheral connections)
	peerUUIDs := a.wire.GetConnectedPeers()

	if len(peerUUIDs) == 0 {
		logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "📋 No connected peers to broadcast profile update")
		return
	}

	logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "📤 Broadcasting profile update to %d peers", len(peerUUIDs))

	// Send profile to each connected peer
	for _, peerUUID := range peerUUIDs {
		go a.sendProfileUpdate(peerUUID, deviceID, profile)
	}
}

// sendProfileUpdate sends profile data to a specific peer
func (a *Android) sendProfileUpdate(peerUUID string, deviceID string, profile map[string]string) {
	a.mu.RLock()
	profileVersion := a.profileVersion
	a.mu.RUnlock()

	// Build ProfileMessage
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
		// We're Peripheral - send notification (realistic BLE behavior)
		err2 = a.wire.NotifyCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
	}

	if err2 != nil {
		logger.Error(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Failed to send profile update to %s: %v", shortHash(peerUUID), err2)
	} else {
		logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "📤 Sent profile update to %s (%d bytes)", shortHash(peerUUID), len(data))
	}
}

// requestProfileUpdate requests profile data from a peer with newer version
func (a *Android) requestProfileUpdate(peerUUID string, targetDeviceID string, expectedVersion int32) {
	// Build ProfileRequestMessage
	profileReq := &pb.ProfileRequestMessage{
		RequesterDeviceId: a.deviceID,
		TargetDeviceId:    targetDeviceID,
		ExpectedVersion:   expectedVersion,
	}

	data, err := proto.Marshal(profileReq)
	if err != nil {
		logger.Error(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Failed to marshal profile request: %v", err)
		return
	}

	logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "📥 Requesting profile v%d from %s", expectedVersion, shortHash(peerUUID))

	// Send via protocol characteristic (like photo requests)
	a.mu.RLock()
	gatt, exists := a.connectedGatts[peerUUID]
	a.mu.RUnlock()

	if !exists {
		logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Peer %s disconnected before profile request", shortHash(peerUUID))
		return
	}

	// Find protocol characteristic
	char := gatt.GetCharacteristic(phone.AuraServiceUUID, phone.AuraProtocolCharUUID)
	if char == nil {
		logger.Warn(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Protocol characteristic not found for peer %s", shortHash(peerUUID))
		return
	}

	// Write request
	char.Value = data
	gatt.WriteCharacteristic(char)
	logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "📥 Sent profile request to %s", shortHash(peerUUID))
}
