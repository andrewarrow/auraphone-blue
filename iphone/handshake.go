package iphone

import (
	"encoding/json"
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	pb "github.com/user/auraphone-blue/proto"
	"google.golang.org/protobuf/proto"
)

// ============================================================================
// Handshake Protocol (Step 7)
// ============================================================================

func (ip *IPhone) sendHandshake(peerUUID string) {
	ip.mu.RLock()
	photoHashBytes := []byte{}
	if ip.photoHash != "" {
		// Convert hex string to bytes
		for i := 0; i < len(ip.photoHash); i += 2 {
			var b byte
			fmt.Sscanf(ip.photoHash[i:i+2], "%02x", &b)
			photoHashBytes = append(photoHashBytes, b)
		}
	}
	profileVersion := ip.profileVersion
	peripheral := ip.connectedPeers[peerUUID]
	ip.mu.RUnlock()

	// Use protobuf HandshakeMessage
	pbHandshake := &pb.HandshakeMessage{
		DeviceId:        ip.deviceID,
		FirstName:       ip.firstName,
		ProtocolVersion: 1,
		TxPhotoHash:     photoHashBytes,  // Photo hash we're offering to send
		ProfileVersion:  profileVersion, // Current profile version
	}

	data, err := proto.Marshal(pbHandshake)
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to marshal handshake: %v", err)
		return
	}

	// Determine if we're acting as Central or Peripheral for this connection
	// If we have a CBPeripheral for this peer, we're Central. Otherwise, we're Peripheral.
	var sendErr error
	if peripheral != nil {
		// We're Central - write to characteristic
		sendErr = ip.wire.WriteCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
	} else {
		// We're Peripheral - send notification (realistic BLE behavior)
		sendErr = ip.wire.NotifyCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
	}

	if sendErr != nil {
		logger.Error(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Failed to send handshake to %s: %v", shortHash(peerUUID), sendErr)
	} else {
		logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "🤝 Sent handshake to %s (photo: %s)", shortHash(peerUUID), shortHash(ip.photoHash))
	}
}

// handleProtocolMessage routes incoming protocol messages (handshake, gossip, profile, profile request)
func (ip *IPhone) handleProtocolMessage(peerUUID string, data []byte) {
	logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔍 handleProtocolMessage: %d bytes from %s", len(data), shortHash(peerUUID))

	// Try to parse as GossipMessage first (has MeshView field)
	var gossipMsg pb.GossipMessage
	if proto.Unmarshal(data, &gossipMsg) == nil && len(gossipMsg.MeshView) > 0 {
		logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "✅ Identified as GossipMessage (mesh_view has %d devices)", len(gossipMsg.MeshView))
		ip.handleGossipMessage(peerUUID, data)
		return
	}

	// Try to parse as HandshakeMessage (has ProtocolVersion field)
	// Check this BEFORE ProfileMessage to avoid field number collision
	var pbHandshake pb.HandshakeMessage
	if proto.Unmarshal(data, &pbHandshake) == nil && pbHandshake.DeviceId != "" && pbHandshake.ProtocolVersion > 0 {
		logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "✅ Identified as HandshakeMessage (protocol_version=%d)", pbHandshake.ProtocolVersion)
		ip.handleHandshakeProto(&pbHandshake, peerUUID)
		return
	}

	// Try to parse as PhotoRequestMessage (has RequesterDeviceId and PhotoHash)
	// PhotoRequestMessage: requester_device_id(1), target_device_id(2), photo_hash(3) [bytes]
	// ProfileRequestMessage: requester_device_id(1), target_device_id(2), expected_version(3) [int32]
	// The presence of PhotoHash (bytes field 3) is the discriminator
	var photoReq pb.PhotoRequestMessage
	if proto.Unmarshal(data, &photoReq) == nil && photoReq.RequesterDeviceId != "" && len(photoReq.PhotoHash) > 0 {
		logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "✅ Identified as PhotoRequestMessage")
		ip.handlePhotoRequest(peerUUID, &photoReq)
		return
	}

	// Try to parse as ProfileRequestMessage (has RequesterDeviceId and ExpectedVersion)
	// MUST check this BEFORE ProfileMessage because fields 1 and 2 overlap!
	// ProfileMessage: device_id(1), first_name(2), last_name(3), ..., profile_version(15)
	// ProfileRequestMessage: requester_device_id(1), target_device_id(2), expected_version(3)
	// Discriminator: ProfileRequestMessage has only 3 fields, ProfileMessage has 15 fields
	// So we check that profile_version (field 15) is NOT set when parsing as ProfileRequestMessage
	var profileReq pb.ProfileRequestMessage
	if proto.Unmarshal(data, &profileReq) == nil && profileReq.RequesterDeviceId != "" && profileReq.TargetDeviceId != "" {
		// Also try parsing as ProfileMessage to check if it's actually a profile (has field 15)
		var testProfile pb.ProfileMessage
		if proto.Unmarshal(data, &testProfile) == nil && testProfile.ProfileVersion > 0 {
			// It's actually a ProfileMessage, not a ProfileRequestMessage
			logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔀 Looks like ProfileRequestMessage but has profile_version=%d, treating as ProfileMessage", testProfile.ProfileVersion)
			// Fall through to ProfileMessage check below
		} else {
			// It's a real ProfileRequestMessage
			logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "✅ Identified as ProfileRequestMessage (target=%s, version=%d)", profileReq.TargetDeviceId[:8], profileReq.ExpectedVersion)
			ip.handleProfileRequest(peerUUID, &profileReq)
			return
		}
	}

	// Try to parse as ProfileMessage (has first_name, phone_number, or tagline fields)
	// Check this LAST because it's the most ambiguous (many string fields that could match other messages)
	// Requires at least one profile field (FirstName, LastName, PhoneNumber, Tagline, or Insta) to be non-empty
	var profileMsg pb.ProfileMessage
	parseErr := proto.Unmarshal(data, &profileMsg)
	hasDeviceID := profileMsg.DeviceId != ""
	hasProfileFields := profileMsg.FirstName != "" || profileMsg.PhoneNumber != "" || profileMsg.Tagline != "" || profileMsg.Insta != "" || profileMsg.LastName != ""

	logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]),
		"🔍 ProfileMessage check: parseErr=%v, deviceID=%s, firstName=%s, lastName=%s, tagline=%s, version=%d, hasProfileFields=%v",
		parseErr, profileMsg.DeviceId[:min(8, len(profileMsg.DeviceId))], profileMsg.FirstName, profileMsg.LastName,
		profileMsg.Tagline[:min(20, len(profileMsg.Tagline))], profileMsg.ProfileVersion, hasProfileFields)

	if parseErr == nil && hasDeviceID && hasProfileFields {
		logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "✅ Identified as ProfileMessage (device=%s, version=%d)", profileMsg.DeviceId[:8], profileMsg.ProfileVersion)
		ip.handleProfileMessage(peerUUID, &profileMsg)
		return
	}

	logger.Warn(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "⚠️  Failed to parse protocol message from %s (%d bytes)", shortHash(peerUUID), len(data))
}

func (ip *IPhone) handleHandshake(peerUUID string, data []byte) {
	// Try to parse as protobuf first
	var pbHandshake pb.HandshakeMessage
	err := proto.Unmarshal(data, &pbHandshake)
	if err != nil {
		// Fall back to JSON for backward compatibility
		var jsonHandshake HandshakeMessage
		err = json.Unmarshal(data, &jsonHandshake)
		if err != nil {
			logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to unmarshal handshake: %v", err)
			return
		}
		// Convert JSON to protobuf format
		pbHandshake.DeviceId = jsonHandshake.DeviceID
		pbHandshake.FirstName = jsonHandshake.FirstName
	}

	ip.handleHandshakeProto(&pbHandshake, peerUUID)
}

func (ip *IPhone) handleHandshakeProto(pbHandshake *pb.HandshakeMessage, peerUUID string) {

	// Convert photo hash bytes to hex string
	photoHashHex := ""
	if len(pbHandshake.TxPhotoHash) > 0 {
		photoHashHex = fmt.Sprintf("%x", pbHandshake.TxPhotoHash)
	}

	logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "🤝 Received handshake from %s: %s (ID: %s, photo: %s)",
		shortHash(peerUUID), pbHandshake.FirstName, pbHandshake.DeviceId, shortHash(photoHashHex))

	// CRITICAL: Register the hardware UUID ↔ device ID mapping in IdentityManager
	// This is THE ONLY place where we learn about other devices' DeviceIDs
	ip.identityManager.RegisterDevice(peerUUID, pbHandshake.DeviceId)

	// Persist mappings to disk
	if err := ip.identityManager.SaveToDisk(); err != nil {
		logger.Warn(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to save identity mappings: %v", err)
	}

	// Update mesh view with peer's device state
	ip.meshView.UpdateDevice(pbHandshake.DeviceId, photoHashHex, pbHandshake.FirstName, pbHandshake.ProfileVersion)
	ip.meshView.MarkDeviceConnected(pbHandshake.DeviceId)

	// Persist mesh view to disk after handshake
	if err := ip.meshView.SaveToDisk(); err != nil {
		logger.Warn(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to save mesh view: %v", err)
	}

	ip.mu.Lock()
	alreadyHandshaked := ip.handshaked[peerUUID] != nil

	// Mark handshake complete (store as JSON struct for compatibility)
	ip.handshaked[peerUUID] = &HandshakeMessage{
		HardwareUUID: peerUUID,
		DeviceID:     pbHandshake.DeviceId,
		DeviceName:   pbHandshake.FirstName,
		FirstName:    pbHandshake.FirstName,
	}

	// Update discovered device with DeviceID, name, and photo hash
	if device, exists := ip.discovered[peerUUID]; exists {
		device.DeviceID = pbHandshake.DeviceId
		device.Name = pbHandshake.FirstName
		device.PhotoHash = photoHashHex
		ip.discovered[peerUUID] = device

		// Notify GUI
		if ip.callback != nil {
			ip.callback(device)
		}
	}
	ip.mu.Unlock()

	// Send our handshake back if we haven't already
	// This ensures bidirectional handshake completion
	if !alreadyHandshaked {
		logger.Debug(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "🤝 Sending handshake back to %s", shortHash(peerUUID))
		ip.sendHandshake(peerUUID)

		// Send ProfileMessage after handshake completes
		ip.sendProfileMessage(peerUUID)
	}

	// Check if we need to start a photo transfer
	// Conditions:
	// 1. They have a photo (photoHashHex != "")
	// 2. We don't have it cached yet
	// 3. We're not already transferring from this peer (prevents duplicate subscriptions)
	if photoHashHex != "" && !ip.photoCache.HasPhoto(photoHashHex) {
		ip.mu.Lock()
		existingTransfer, transferInProgress := ip.photoTransfers[peerUUID]
		ip.mu.Unlock()

		if transferInProgress {
			// Check if it's for the same photo
			if existingTransfer.PhotoHash == photoHashHex {
				logger.Debug(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)),
					"📸 Photo transfer already in progress from %s (hash: %s)",
					shortHash(peerUUID), shortHash(photoHashHex))
				return // Don't start duplicate transfer
			} else {
				// Different photo - old transfer might be stale, allow new one
				logger.Warn(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)),
					"📸 Replacing stale photo transfer from %s (old: %s, new: %s)",
					shortHash(peerUUID), shortHash(existingTransfer.PhotoHash), shortHash(photoHashHex))
			}
		}

		logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📸 Starting photo transfer from %s (hash: %s)",
			shortHash(peerUUID), shortHash(photoHashHex))
		go ip.requestAndReceivePhoto(peerUUID, photoHashHex, pbHandshake.DeviceId)
	}

	// Check if peer has newer profile version
	// Load our cached profile version for this device
	cacheManager := phone.NewDeviceCacheManager(ip.hardwareUUID)
	metadata, _ := cacheManager.LoadDeviceMetadata(pbHandshake.DeviceId)

	cachedProfileVersion := int32(0)
	if metadata != nil {
		cachedProfileVersion = metadata.ProfileVersion
	}

	if pbHandshake.ProfileVersion > cachedProfileVersion {
		logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)),
			"📋 Peer %s has newer profile v%d (we have v%d), requesting update",
			shortHash(peerUUID), pbHandshake.ProfileVersion, cachedProfileVersion)
		go ip.requestProfileUpdate(peerUUID, pbHandshake.DeviceId, pbHandshake.ProfileVersion)
	}
}
