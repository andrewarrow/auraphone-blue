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
	ip.mu.RUnlock()

	// Use protobuf HandshakeMessage
	pbHandshake := &pb.HandshakeMessage{
		DeviceId:        ip.deviceID,
		FirstName:       ip.firstName,
		ProtocolVersion: 1,
		TxPhotoHash:     photoHashBytes, // Photo hash we're offering to send
	}

	data, err := proto.Marshal(pbHandshake)
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to marshal handshake: %v", err)
		return
	}

	// Write to peer's AuraProtocolCharUUID
	err = ip.wire.WriteCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Failed to send handshake to %s: %v", shortHash(peerUUID), err)
	} else {
		logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "🤝 Sent handshake to %s (photo: %s)", shortHash(peerUUID), shortHash(ip.photoHash))
	}
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

	ip.mu.Lock()
	alreadyHandshaked := ip.handshaked[peerUUID] != nil

	// Mark handshake complete (store as JSON struct for compatibility)
	ip.handshaked[peerUUID] = &HandshakeMessage{
		HardwareUUID: peerUUID,
		DeviceID:     pbHandshake.DeviceId,
		DeviceName:   fmt.Sprintf("iPhone (%s)", pbHandshake.FirstName),
		FirstName:    pbHandshake.FirstName,
	}

	// Update discovered device with DeviceID, name, and photo hash
	if device, exists := ip.discovered[peerUUID]; exists {
		device.DeviceID = pbHandshake.DeviceId
		device.Name = fmt.Sprintf("iPhone (%s)", pbHandshake.FirstName)
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
}
