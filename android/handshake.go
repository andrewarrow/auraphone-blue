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
	a.mu.RUnlock()

	// Use protobuf HandshakeMessage
	pbHandshake := &pb.HandshakeMessage{
		DeviceId:        a.deviceID,
		FirstName:       a.firstName,
		ProtocolVersion: 1,
		TxPhotoHash:     photoHashBytes, // Photo hash we're offering to send
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

func (a *Android) handleProtocolMessage(peerUUID string, data []byte) {
	// Try to parse as HandshakeMessage first
	var pbHandshake pb.HandshakeMessage
	err := proto.Unmarshal(data, &pbHandshake)
	if err == nil && pbHandshake.DeviceId != "" {
		// It's a handshake
		a.handleHandshake(peerUUID, &pbHandshake)
		return
	}

	// Try to parse as GossipMessage
	var pbGossip pb.GossipMessage
	err = proto.Unmarshal(data, &pbGossip)
	if err == nil && pbGossip.SenderDeviceId != "" {
		// It's a gossip message
		a.handleGossipMessage(peerUUID, &pbGossip)
		return
	}

	logger.Warn(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "⚠️  Failed to parse protocol message from %s", shortHash(peerUUID))
}

func (a *Android) handleHandshake(peerUUID string, pbHandshake *pb.HandshakeMessage) {
	// Convert photo hash bytes to hex string
	photoHashHex := ""
	if len(pbHandshake.TxPhotoHash) > 0 {
		photoHashHex = fmt.Sprintf("%x", pbHandshake.TxPhotoHash)
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

	a.mu.Lock()
	alreadyHandshaked := a.handshaked[peerUUID] != nil

	// Mark handshake complete (store as JSON struct for compatibility)
	a.handshaked[peerUUID] = &HandshakeMessage{
		HardwareUUID: peerUUID,
		DeviceID:     pbHandshake.DeviceId,
		DeviceName:   fmt.Sprintf("Android (%s)", pbHandshake.FirstName),
		FirstName:    pbHandshake.FirstName,
	}

	// Update discovered device with DeviceID, name, and photo hash
	if device, exists := a.discovered[peerUUID]; exists {
		// Detect platform from device name if possible (will be more accurate with future protocol changes)
		platformName := device.Name
		if platformName == "" || platformName == "Unknown Device" {
			platformName = fmt.Sprintf("Device (%s)", pbHandshake.FirstName)
		}

		device.DeviceID = pbHandshake.DeviceId
		device.Name = platformName
		device.PhotoHash = photoHashHex
		a.discovered[peerUUID] = device

		// Notify GUI
		if a.callback != nil {
			a.callback(device)
		}
	}

	// Get GATT connection for this peer
	gatt := a.connectedGatts[peerUUID]
	a.mu.Unlock()

	// Send our handshake back if we haven't already
	// This ensures bidirectional handshake completion
	if !alreadyHandshaked && gatt != nil {
		logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "🤝 Sending handshake back to %s", shortHash(peerUUID))
		a.sendHandshake(peerUUID, gatt)
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
}
