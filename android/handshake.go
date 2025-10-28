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
	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 sendHandshake called for peer %s", shortHash(peerUUID))

	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 Acquiring read lock")
	a.mu.RLock()
	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 Read lock acquired")
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
	a.mu.RUnlock()

	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 Marshaling handshake protobuf")

	// Use protobuf HandshakeMessage
	pbHandshake := &pb.HandshakeMessage{
		DeviceId:        a.deviceID,
		FirstName:       a.firstName,
		ProtocolVersion: 1,
		TxPhotoHash:     photoHashBytes,  // Photo hash we're offering to send
		ProfileVersion:  profileVersion, // Current profile version
	}

	data, err := proto.Marshal(pbHandshake)
	if err != nil {
		logger.Error(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "Failed to marshal handshake: %v", err)
		return
	}

	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 Getting protocol characteristic")

	// Write to peer's AuraProtocolCharUUID
	char := gatt.GetCharacteristic(phone.AuraServiceUUID, phone.AuraProtocolCharUUID)
	if char == nil {
		logger.Error(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Failed to get protocol characteristic")
		return
	}

	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 Writing characteristic")

	char.Value = data
	success := gatt.WriteCharacteristic(char)

	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 WriteCharacteristic returned: %v", success)

	logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🤝 Sent handshake to %s (photo: %s)", shortHash(peerUUID), shortHash(a.photoHash))
}

// sendHandshakeViaWire sends handshake directly via wire (when we don't have a GATT connection)
// This is used when we're acting as Peripheral for this connection
func (a *Android) sendHandshakeViaWire(peerUUID string) {
	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 sendHandshakeViaWire called for peer %s", shortHash(peerUUID))

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
	a.mu.RUnlock()

	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 Marshaling handshake protobuf")

	// Use protobuf HandshakeMessage
	pbHandshake := &pb.HandshakeMessage{
		DeviceId:        a.deviceID,
		FirstName:       a.firstName,
		ProtocolVersion: 1,
		TxPhotoHash:     photoHashBytes,  // Photo hash we're offering to send
		ProfileVersion:  profileVersion, // Current profile version
	}

	data, err := proto.Marshal(pbHandshake)
	if err != nil {
		logger.Error(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "Failed to marshal handshake: %v", err)
		return
	}

	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 Calling wire.NotifyCharacteristic (acting as Peripheral)")

	// Send notification via wire (Peripherals send notifications, not writes)
	// This is the realistic BLE behavior: when we're Peripheral, we can only respond via notifications
	err = a.wire.NotifyCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
	if err != nil {
		logger.Error(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Failed to send handshake notification to %s: %v", shortHash(peerUUID), err)
	} else {
		logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🤝 Sent handshake via notification to %s (photo: %s)", shortHash(peerUUID), shortHash(a.photoHash))
	}

	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 sendHandshakeViaWire completed for peer %s", shortHash(peerUUID))
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

	// Try to parse as ProfileMessage (has phone_number or tagline fields)
	// Check this BEFORE ProfileRequestMessage because field 1 and 2 overlap!
	// ProfileMessage: device_id(1), last_name(2), phone_number(3)...
	// ProfileRequestMessage: requester_device_id(1), target_device_id(2), expected_version(3)
	// Without checking ProfileMessage first, last_name gets misread as target_device_id
	var profileMsg pb.ProfileMessage
	if proto.Unmarshal(data, &profileMsg) == nil && profileMsg.DeviceId != "" && (profileMsg.PhoneNumber != "" || profileMsg.Tagline != "" || profileMsg.Insta != "" || profileMsg.LastName != "") {
		a.handleProfileMessage(peerUUID, &profileMsg)
		return
	}

	// Try to parse as ProfileRequestMessage (has RequesterDeviceId field)
	var profileReq pb.ProfileRequestMessage
	if proto.Unmarshal(data, &profileReq) == nil && profileReq.RequesterDeviceId != "" && profileReq.TargetDeviceId != "" {
		a.handleProfileRequest(peerUUID, &profileReq)
		return
	}

	logger.Warn(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "⚠️  Failed to parse protocol message from %s", shortHash(peerUUID))
}

func (a *Android) handleHandshake(peerUUID string, pbHandshake *pb.HandshakeMessage) {
	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 handleHandshake called for peer %s", shortHash(peerUUID))

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
		logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 Handshake already completed with %s (same photo), skipping", shortHash(peerUUID))
		return
	}

	logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🤝 Received handshake from %s: %s (ID: %s, photo: %s)",
		shortHash(peerUUID), pbHandshake.FirstName, pbHandshake.DeviceId, shortHash(photoHashHex))

	// CRITICAL: Register the hardware UUID ↔ device ID mapping in IdentityManager
	// This is THE ONLY place where we learn about other devices' DeviceIDs
	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 [STEP 1] Registering device identity")
	a.identityManager.RegisterDevice(peerUUID, pbHandshake.DeviceId)
	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 [STEP 1] Device identity registered")

	// Persist mappings to disk
	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 [STEP 2] Saving identity mappings to disk")
	if err := a.identityManager.SaveToDisk(); err != nil {
		logger.Warn(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "Failed to save identity mappings: %v", err)
	}
	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 [STEP 2] Identity mappings saved")

	// Update mesh view with peer's device state
	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 [STEP 3] Updating mesh view")
	a.meshView.UpdateDevice(pbHandshake.DeviceId, photoHashHex, pbHandshake.FirstName, pbHandshake.ProfileVersion)
	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 [STEP 3] Mesh view updated")

	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 [STEP 4] Marking device connected in mesh")
	a.meshView.MarkDeviceConnected(pbHandshake.DeviceId)
	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 [STEP 4] Device marked as connected")

	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 Acquiring mutex to update handshaked map")
	a.mu.Lock()
	alreadyHandshaked := a.handshaked[peerUUID] != nil

	// Mark handshake complete (store as JSON struct for compatibility)
	a.handshaked[peerUUID] = &HandshakeMessage{
		HardwareUUID: peerUUID,
		DeviceID:     pbHandshake.DeviceId,
		DeviceName:   pbHandshake.FirstName,
		FirstName:    pbHandshake.FirstName,
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
	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 Releasing mutex")
	a.mu.Unlock()

	// Notify GUI (outside mutex to avoid deadlock)
	if callbackDevice != nil && a.callback != nil {
		logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 Calling discovery callback")
		a.callback(*callbackDevice)
	}

	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)),
		"🔍 Checking handshake reply: alreadyHandshaked=%v, gatt=%v, connectedViaWire=%v",
		alreadyHandshaked, gatt != nil, a.wire.IsConnected(peerUUID))

	// Send our handshake back if we haven't already
	// This ensures bidirectional handshake completion
	if !alreadyHandshaked {
		if gatt != nil {
			logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🤝 Sending handshake back via GATT to %s", shortHash(peerUUID))
			a.sendHandshake(peerUUID, gatt)
		} else if a.wire.IsConnected(peerUUID) {
			// We're connected but don't have a GATT (we're Peripheral in this connection)
			// Send handshake back directly via wire
			logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🤝 Sending handshake back via wire to %s (peripheral mode)", shortHash(peerUUID))
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
				logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)),
					"📸 Photo transfer already in progress from %s (hash: %s)",
					shortHash(peerUUID), shortHash(photoHashHex))
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

	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🔍 handleHandshake completed for peer %s", shortHash(peerUUID))
}
