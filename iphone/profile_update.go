package iphone

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	pb "github.com/user/auraphone-blue/proto"
	"google.golang.org/protobuf/proto"
)

// broadcastProfileUpdate sends profile update to all connected peers
func (ip *IPhone) broadcastProfileUpdate() {
	ip.mu.RLock()
	deviceID := ip.deviceID
	profile := make(map[string]string)
	for k, v := range ip.profile {
		profile[k] = v
	}
	ip.mu.RUnlock()

	// Get ALL connected peers (both Central and Peripheral connections)
	peerUUIDs := ip.wire.GetConnectedPeers()

	if len(peerUUIDs) == 0 {
		logger.Debug(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📋 No connected peers to broadcast profile update")
		return
	}

	logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📤 Broadcasting profile update to %d peers", len(peerUUIDs))

	// Send profile to each connected peer
	for _, peerUUID := range peerUUIDs {
		go ip.sendProfileUpdate(peerUUID, deviceID, profile)
	}
}

// sendProfileUpdate sends profile data to a specific peer
func (ip *IPhone) sendProfileUpdate(peerUUID string, deviceID string, profile map[string]string) {
	ip.mu.RLock()
	profileVersion := ip.profileVersion
	ip.mu.RUnlock()

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
		logger.Error(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Failed to send profile update to %s: %v", shortHash(peerUUID), err2)
	} else {
		logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📤 Sent profile update to %s (%d bytes)", shortHash(peerUUID), len(data))
	}
}

// requestProfileUpdate requests profile data from a peer with newer version
func (ip *IPhone) requestProfileUpdate(peerUUID string, targetDeviceID string, expectedVersion int32) {
	// Build ProfileRequestMessage
	profileReq := &pb.ProfileRequestMessage{
		RequesterDeviceId: ip.deviceID,
		TargetDeviceId:    targetDeviceID,
		ExpectedVersion:   expectedVersion,
	}

	data, err := proto.Marshal(profileReq)
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Failed to marshal profile request: %v", err)
		return
	}

	logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📥 Requesting profile v%d from %s", expectedVersion, shortHash(peerUUID))

	// Send via protocol characteristic (like photo requests)
	ip.mu.RLock()
	peripheral, exists := ip.connectedPeers[peerUUID]
	ip.mu.RUnlock()

	if !exists {
		logger.Debug(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Peer %s disconnected before profile request", shortHash(peerUUID))
		return
	}

	// Find protocol characteristic
	for _, service := range peripheral.Services {
		if service.UUID == phone.AuraServiceUUID {
			for _, char := range service.Characteristics {
				if char.UUID == phone.AuraProtocolCharUUID {
					// Write request
					peripheral.WriteValue(data, char, 0) // CBCharacteristicWriteWithResponse
					logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📥 Sent profile request to %s", shortHash(peerUUID))
					return
				}
			}
		}
	}

	logger.Warn(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Protocol characteristic not found for peer %s", shortHash(peerUUID))
}
