package android

import (
	"fmt"

	"github.com/user/auraphone-blue/kotlin"
	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	pb "github.com/user/auraphone-blue/proto"
	"google.golang.org/protobuf/proto"
)

// ============================================================================
// Handshake Protocol
// ============================================================================

func (a *Android) sendHandshake(peerUUID string, gatt *kotlin.BluetoothGatt) {
	a.mu.RLock()
	photoHashBytes := []byte{}
	if a.photoHash != "" {
		// Convert hex string to bytes
		for i := 0; i < len(a.photoHash); i += 2 {
			var b byte
			fmt.Sscanf(a.photoHash[i:i+2], "%02x", &b)
			photoHashBytes = append(photoHashBytes, b)
		}
	}
	profileVersion := a.profileVersion
	firstName := a.firstName  // Capture firstName while holding lock!
	deviceID := a.deviceID    // Capture deviceID while holding lock!
	a.mu.RUnlock()

	logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "🤝 sendHandshake: firstName='%s', deviceID='%s' to %s", firstName, deviceID, shortHash(peerUUID))

	// Use protobuf HandshakeMessage
	pbHandshake := &pb.HandshakeMessage{
		DeviceId:        deviceID,       // Use captured value
		FirstName:       firstName,      // Use captured value
		ProtocolVersion: 1,
		TxPhotoHash:     photoHashBytes,  // Photo hash we're offering to send
		ProfileVersion:  profileVersion, // Current profile version
	}

	data, err := proto.Marshal(pbHandshake)
	if err != nil {
		logger.Error(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "Failed to marshal handshake: %v", err)
		return
	}

	// Write to peer's AuraProtocolCharUUID
	char := gatt.GetCharacteristic(phone.AuraServiceUUID, phone.AuraProtocolCharUUID)
	if char == nil {
		logger.Error(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Failed to get protocol characteristic")
		return
	}

	char.Value = data
	gatt.WriteCharacteristic(char)

	logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🤝 Sent handshake to %s (photo: %s)", shortHash(peerUUID), shortHash(a.photoHash))
}

// sendHandshakeViaWire sends handshake directly via wire (when we don't have a GATT connection)
// This is used when we're acting as Peripheral for this connection
func (a *Android) sendHandshakeViaWire(peerUUID string) {
	a.mu.RLock()
	photoHashBytes := []byte{}
	if a.photoHash != "" {
		// Convert hex string to bytes
		for i := 0; i < len(a.photoHash); i += 2 {
			var b byte
			fmt.Sscanf(a.photoHash[i:i+2], "%02x", &b)
			photoHashBytes = append(photoHashBytes, b)
		}
	}
	profileVersion := a.profileVersion
	firstName := a.firstName  // Capture firstName while holding lock!
	deviceID := a.deviceID    // Capture deviceID while holding lock!
	a.mu.RUnlock()

	logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "🤝 sendHandshake: firstName='%s', deviceID='%s' to %s", firstName, deviceID, shortHash(peerUUID))

	// Use protobuf HandshakeMessage
	pbHandshake := &pb.HandshakeMessage{
		DeviceId:        deviceID,       // Use captured value
		FirstName:       firstName,      // Use captured value
		ProtocolVersion: 1,
		TxPhotoHash:     photoHashBytes,  // Photo hash we're offering to send
		ProfileVersion:  profileVersion, // Current profile version
	}

	data, err := proto.Marshal(pbHandshake)
	if err != nil {
		logger.Error(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "Failed to marshal handshake: %v", err)
		return
	}

	// Send notification via wire (Peripherals send notifications, not writes)
	// This is the realistic BLE behavior: when we're Peripheral, we can only respond via notifications
	err = a.wire.NotifyCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
	if err != nil {
		logger.Error(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Failed to send handshake notification to %s: %v", shortHash(peerUUID), err)
	} else {
		logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🤝 Sent handshake via notification to %s (photo: %s)", shortHash(peerUUID), shortHash(a.photoHash))
	}
}

func (a *Android) handleProtocolMessage(peerUUID string, data []byte) {
	// Try to parse as GossipMessage first (has MeshView field)
	var pbGossip pb.GossipMessage
	err := proto.Unmarshal(data, &pbGossip)
	if err == nil && len(pbGossip.MeshView) > 0 {
		// It's a gossip message
		a.handleGossipMessage(peerUUID, &pbGossip)
		return
	}

	// Try to parse as HandshakeMessage (has ProtocolVersion field)
	// Check this BEFORE ProfileMessage to avoid field number collision
	var pbHandshake pb.HandshakeMessage
	err = proto.Unmarshal(data, &pbHandshake)
	if err == nil && pbHandshake.DeviceId != "" && pbHandshake.ProtocolVersion > 0 {
		// It's a handshake
		a.handleHandshake(peerUUID, &pbHandshake)
		return
	}

	// Try to parse as PhotoRequestMessage (has RequesterDeviceId and PhotoHash)
	// PhotoRequestMessage: requester_device_id(1), target_device_id(2), photo_hash(3) [bytes]
	// ProfileRequestMessage: requester_device_id(1), target_device_id(2), expected_version(3) [int32]
	// The presence of PhotoHash (bytes field 3) is the discriminator
	var photoReq pb.PhotoRequestMessage
	if proto.Unmarshal(data, &photoReq) == nil && photoReq.RequesterDeviceId != "" && len(photoReq.PhotoHash) > 0 {
		a.handlePhotoRequest(peerUUID, &photoReq)
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
			// Fall through to ProfileMessage check below
		} else {
			// It's a real ProfileRequestMessage
			a.handleProfileRequest(peerUUID, &profileReq)
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

	if parseErr == nil && hasDeviceID && hasProfileFields {
		a.handleProfileMessage(peerUUID, &profileMsg)
		return
	}

	logger.Warn(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "⚠️  Failed to parse protocol message from %s (%d bytes)", shortHash(peerUUID), len(data))
}

func (a *Android) handleHandshake(peerUUID string, pbHandshake *pb.HandshakeMessage) {

	// Convert photo hash bytes to hex string
	photoHashHex := ""
	if len(pbHandshake.TxPhotoHash) > 0 {
		photoHashHex = fmt.Sprintf("%x", pbHandshake.TxPhotoHash)
	}

	// Quick check: if we've already completed handshake with this peer AND photo hash hasn't changed, skip redundant processing
	a.mu.RLock()
	existingHandshake := a.handshaked[peerUUID]
	existingDevice, deviceExists := a.discovered[peerUUID]
	a.mu.RUnlock()

	if existingHandshake != nil && deviceExists && existingDevice.PhotoHash == photoHashHex {
		return
	}

	logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🤝 Received handshake from %s: %s (ID: %s, photo: %s)",
		shortHash(peerUUID), pbHandshake.FirstName, pbHandshake.DeviceId, shortHash(photoHashHex))

	// CRITICAL: Register the hardware UUID ↔ device ID mapping in IdentityManager
	// This is THE ONLY place where we learn about other devices' DeviceIDs
	a.identityManager.RegisterDevice(peerUUID, pbHandshake.DeviceId)

	// Persist mappings to disk
	if err := a.identityManager.SaveToDisk(); err != nil {
		logger.Warn(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "Failed to save identity mappings: %v", err)
	}

	// Update mesh view with peer's device state
	a.meshView.UpdateDevice(pbHandshake.DeviceId, photoHashHex, pbHandshake.FirstName, pbHandshake.ProfileVersion)
	a.meshView.MarkDeviceConnected(pbHandshake.DeviceId)

	// Persist mesh view to disk after handshake
	if err := a.meshView.SaveToDisk(); err != nil {
		logger.Warn(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "Failed to save mesh view: %v", err)
	}

	a.mu.Lock()
	alreadyHandshaked := a.handshaked[peerUUID] != nil

	// Mark handshake complete (store as JSON struct for compatibility)
	// Note: peripheralUUID is the map key, so we don't need to store it in the value
	a.handshaked[peerUUID] = &HandshakeMessage{
		DeviceID:   pbHandshake.DeviceId,
		DeviceName: pbHandshake.FirstName,
		FirstName:  pbHandshake.FirstName,
	}

	// Update discovered device with DeviceID, name, and photo hash
	var callbackDevice *phone.DiscoveredDevice
	if device, exists := a.discovered[peerUUID]; exists {
		device.DeviceID = pbHandshake.DeviceId
		device.Name = pbHandshake.FirstName
		device.PhotoHash = photoHashHex
		a.discovered[peerUUID] = device

		// Prepare callback device (copy it so we can call callback outside mutex)
		callbackDevice = &device
	}

	// Get GATT connection for this peer
	gatt := a.connectedGatts[peerUUID]
	a.mu.Unlock()

	// Notify GUI (outside mutex to avoid deadlock)
	if callbackDevice != nil && a.callback != nil {
		a.callback(*callbackDevice)
	}

	// Send our handshake back if we haven't already
	// This ensures bidirectional handshake completion
	if !alreadyHandshaked {
		if gatt != nil {
			a.sendHandshake(peerUUID, gatt)
		} else if a.wire.IsConnected(peerUUID) {
			// We're connected but don't have a GATT (we're Peripheral in this connection)
			// Send handshake back directly via wire
			a.sendHandshakeViaWire(peerUUID)
		} else {
			logger.Warn(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "⚠️ Cannot send handshake back to %s - no connection", shortHash(peerUUID))
		}

		// Send ProfileMessage after handshake completes
		a.sendProfileMessage(peerUUID)
	}

	// Check if we need to start a photo transfer
	// Conditions:
	// 1. They have a photo (photoHashHex != "")
	// 2. We don't have it cached yet
	// 3. We're not already transferring from this peer (prevents duplicate subscriptions)
	if photoHashHex != "" && !a.photoCache.HasPhoto(photoHashHex) {
		a.mu.Lock()
		existingTransfer, transferInProgress := a.photoTransfers[peerUUID]
		a.mu.Unlock()

		if transferInProgress {
			// Check if it's for the same photo
			if existingTransfer.PhotoHash == photoHashHex {
				return // Don't start duplicate transfer
			} else {
				// Different photo - old transfer might be stale, allow new one
				logger.Warn(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)),
					"📸 Replacing stale photo transfer from %s (old: %s, new: %s)",
					shortHash(peerUUID), shortHash(existingTransfer.PhotoHash), shortHash(photoHashHex))
			}
		}

		logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "📸 Starting photo transfer from %s (hash: %s)",
			shortHash(peerUUID), shortHash(photoHashHex))
		go a.requestAndReceivePhoto(peerUUID, photoHashHex, pbHandshake.DeviceId)
	}

	// Check if peer has newer profile version
	// Load our cached profile version for this device
	cacheManager := phone.NewDeviceCacheManager(a.hardwareUUID)
	metadata, _ := cacheManager.LoadDeviceMetadata(pbHandshake.DeviceId)

	cachedProfileVersion := int32(0)
	if metadata != nil {
		cachedProfileVersion = metadata.ProfileVersion
	}

	if pbHandshake.ProfileVersion > cachedProfileVersion {
		logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)),
			"📋 Peer %s has newer profile v%d (we have v%d), requesting update",
			shortHash(peerUUID), pbHandshake.ProfileVersion, cachedProfileVersion)
		go a.requestProfileUpdate(peerUUID, pbHandshake.DeviceId, pbHandshake.ProfileVersion)
	}
}
