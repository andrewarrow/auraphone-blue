package phone

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/user/auraphone-blue/logger"
	auraphone "github.com/user/auraphone-blue/proto"
)

// sendPhotoChunk sends a single chunk to a device
func (ph *PhotoHandler) sendPhotoChunk(targetUUID, targetDeviceID string, chunkIndex int, photoData []byte, photoHash [32]byte, totalChunks int) error {
	device := ph.device
	prefix := fmt.Sprintf("%s %s", device.GetHardwareUUID()[:8], device.GetPlatform())
	coordinator := device.GetPhotoCoordinator()

	// Calculate chunk boundaries
	chunkStart := chunkIndex * DefaultChunkSize
	chunkEnd := chunkStart + DefaultChunkSize
	if chunkEnd > len(photoData) {
		chunkEnd = len(photoData)
	}
	chunkData := photoData[chunkStart:chunkEnd]

	// Create chunk message
	chunk, err := CreatePhotoChunk(
		device.GetDeviceID(),
		targetDeviceID,
		photoHash[:],
		int32(chunkIndex),
		int32(totalChunks),
		chunkData,
	)
	if err != nil {
		return fmt.Errorf("failed to create chunk: %w", err)
	}

	// Encode chunk
	encodedChunk, err := EncodePhotoChunk(chunk)
	if err != nil {
		return fmt.Errorf("failed to encode chunk: %w", err)
	}

	// Send via photo characteristic
	connManager := device.GetConnManager()
	if err := connManager.SendToDevice(targetUUID, AuraPhotoCharUUID, encodedChunk); err != nil {
		return fmt.Errorf("failed to send chunk: %w", err)
	}

	// Record chunk as sent in coordinator
	coordinator.RecordChunkSent(targetDeviceID, chunkIndex)

	logger.Debug(prefix, "📤 Sent chunk %d/%d to %s (%d bytes)",
		chunkIndex+1, totalChunks, targetDeviceID[:8], len(encodedChunk))

	return nil
}

// HandlePhotoRequest handles a request for a photo
func (ph *PhotoHandler) HandlePhotoRequest(senderUUID string, req *auraphone.PhotoRequestMessage) {
	device := ph.device
	prefix := fmt.Sprintf("%s %s", device.GetHardwareUUID()[:8], device.GetPlatform())

	// Check if they're requesting OUR photo
	if req.TargetDeviceId != device.GetDeviceID() {
		logger.Debug(prefix, "⏭️  Photo request for %s, not us", req.TargetDeviceId[:8])
		return
	}

	// Log photo request received event
	timelineLogger := device.GetPhotoCoordinator().GetTimelineLogger()
	if timelineLogger != nil {
		timelineLogger.LogPhotoRequestReceived(req.RequesterDeviceId, senderUUID, string(req.PhotoHash))
	}

	logger.Info(prefix, "📸 Sending our photo to %s in response to request", req.RequesterDeviceId[:8])

	// Load our photo from cache (stored as my_photo.jpg by SetProfilePhoto)
	photoPath := filepath.Join(GetDeviceCacheDir(device.GetHardwareUUID()), "my_photo.jpg")
	photoData, err := os.ReadFile(photoPath)
	if err != nil {
		logger.Warn(prefix, "❌ Failed to read our photo: %v", err)
		return
	}

	// Calculate photo hash
	hash := sha256.Sum256(photoData)

	// Calculate total chunks
	totalChunks := (len(photoData) + DefaultChunkSize - 1) / DefaultChunkSize

	logger.Debug(prefix, "📤 Sending photo to %s (%d bytes, %d chunks)",
		req.RequesterDeviceId[:8], len(photoData), totalChunks)

	// Get photo coordinator
	coordinator := device.GetPhotoCoordinator()
	coordinator.StartSend(req.RequesterDeviceId, string(hash[:]), totalChunks)

	// Send all chunks
	for i := 0; i < totalChunks; i++ {
		if err := ph.sendPhotoChunk(senderUUID, req.RequesterDeviceId, i, photoData, hash, totalChunks); err != nil {
			logger.Warn(prefix, "❌ Failed to send chunk %d: %v", i, err)
			coordinator.FailSend(req.RequesterDeviceId, fmt.Sprintf("failed to send chunk: %v", err))
			return
		}
		coordinator.UpdateSendProgress(req.RequesterDeviceId, i+1)
	}

	logger.Info(prefix, "✅ All %d chunks sent to %s, waiting for ACKs", totalChunks, req.RequesterDeviceId[:8])

	// Don't mark as complete yet - wait for ACKs
	// The message_router will call CompleteSend when it receives the completion ACK
}

// HandlePhotoChunk receives photo chunk data from either Central or Peripheral mode
func (ph *PhotoHandler) HandlePhotoChunk(senderUUID string, data []byte) {
	device := ph.device
	prefix := fmt.Sprintf("%s %s", device.GetHardwareUUID()[:8], device.GetPlatform())

	// Decode protobuf chunk FIRST to get the sender's device ID
	chunk, err := DecodePhotoChunk(data)
	if err != nil {
		logger.Warn(prefix, "❌ Failed to decode photo chunk from %s: %v", senderUUID[:8], err)
		return
	}

	// Use device ID from the chunk itself (more reliable than UUID→DeviceID map)
	deviceID := chunk.SenderDeviceId

	logger.Debug(prefix, "🔍 Looking up deviceID for sender: %s (from chunk: %s)", senderUUID[:8], deviceID[:8])

	// Verify the UUID→DeviceID mapping exists and update if needed
	mutex := device.GetMutex()
	mutex.RLock()
	uuidToDeviceID := device.GetUUIDToDeviceIDMap()
	existingDeviceID, exists := uuidToDeviceID[senderUUID]

	// Debug: log the entire map
	logger.Debug(prefix, "📋 Current UUID→DeviceID map has %d entries:", len(uuidToDeviceID))
	for uuid, devID := range uuidToDeviceID {
		logger.Debug(prefix, "   - %s → %s", uuid[:8], devID[:8])
	}
	mutex.RUnlock()

	if !exists {
		// Mapping doesn't exist yet - add it from the chunk data
		logger.Debug(prefix, "📝 Adding UUID→DeviceID mapping: %s → %s (from photo chunk)", senderUUID[:8], deviceID[:8])
		mutex.Lock()
		uuidToDeviceID[senderUUID] = deviceID
		mutex.Unlock()
	} else if existingDeviceID != deviceID {
		logger.Warn(prefix, "⚠️  Device ID mismatch for %s: map has %s, chunk has %s",
			senderUUID[:8], existingDeviceID[:8], deviceID[:8])
		// Use the device ID from the chunk (it's more current)
		deviceID = existingDeviceID
	}

	logger.Debug(prefix, "📥 Photo chunk %d/%d from %s (%d bytes)",
		chunk.ChunkIndex+1, chunk.TotalChunks, deviceID[:8], len(chunk.ChunkData))

	coordinator := device.GetPhotoCoordinator()

	// Start receive if this is the first chunk
	recvState := coordinator.GetReceiveState(deviceID)
	if recvState == nil {
		// Convert binary photo hash to hex string for storage
		photoHashHex := hex.EncodeToString(chunk.PhotoHash)
		coordinator.StartReceive(deviceID, photoHashHex, int(chunk.TotalChunks))
	}

	// Record this chunk
	coordinator.RecordReceivedChunk(deviceID, int(chunk.ChunkIndex), chunk.ChunkData)

	// Send ACK for this chunk
	ph.sendChunkAck(senderUUID, deviceID, chunk)

	// Check if transfer is complete (safely handle if state was cleaned up)
	recvState = coordinator.GetReceiveState(deviceID)
	if recvState == nil {
		logger.Warn(prefix, "⚠️  Receive state disappeared for %s after recording chunk %d (possibly cleaned up)",
			deviceID[:8], chunk.ChunkIndex)
		return
	}

	logger.Debug(prefix, "📊 Progress: %d/%d chunks received from %s",
		recvState.ChunksReceived, recvState.TotalChunks, deviceID[:8])

	if recvState != nil && recvState.ChunksReceived == recvState.TotalChunks {
		logger.Info(prefix, "✅ Received all %d chunks from %s, assembling photo",
			recvState.TotalChunks, deviceID[:8])

		// Reassemble photo
		photoData := make([]byte, 0)
		for i := 0; i < recvState.TotalChunks; i++ {
			chunkData, exists := recvState.ReceivedChunks[i]
			if !exists {
				logger.Warn(prefix, "❌ Missing chunk %d during assembly", i)
				coordinator.FailReceive(deviceID, fmt.Sprintf("missing chunk %d", i))
				return
			}
			photoData = append(photoData, chunkData...)
		}

		// Verify hash
		calculatedHash := sha256.Sum256(photoData)
		calculatedHashHex := hex.EncodeToString(calculatedHash[:])
		if calculatedHashHex != recvState.PhotoHash {
			logger.Warn(prefix, "❌ Photo hash mismatch from %s (expected %s, got %s)",
				deviceID[:8], recvState.PhotoHash[:8], calculatedHashHex[:8])
			coordinator.FailReceive(deviceID, "photo hash mismatch")
			return
		}

		// Mark receive as complete
		coordinator.CompleteReceive(deviceID, recvState.PhotoHash)

		// Save photo to cache manager (creates photos/{hash}.jpg and updates metadata)
		cacheManager := device.GetCacheManager()
		if err := cacheManager.SaveDevicePhoto(deviceID, photoData, recvState.PhotoHash); err != nil {
			logger.Warn(prefix, "⚠️  Failed to save photo to cache: %v", err)
			coordinator.FailReceive(deviceID, fmt.Sprintf("failed to save photo: %v", err))
			return
		}

		logger.Info(prefix, "🎉 Photo from %s saved successfully (%d bytes)", deviceID[:8], len(photoData))

		// Trigger discovery callback to notify GUI that photo is now available
		logger.Debug(prefix, "🔍 BEFORE TriggerDiscoveryUpdate: senderUUID=%s, deviceID=%s, photoHash=%s, photoDataLen=%d",
			senderUUID[:8], deviceID[:8], recvState.PhotoHash[:8], len(photoData))
		device.TriggerDiscoveryUpdate(senderUUID, deviceID, recvState.PhotoHash, photoData)
		logger.Debug(prefix, "✅ AFTER TriggerDiscoveryUpdate called for %s with photo %s", deviceID[:8], recvState.PhotoHash[:8])
	}
}

// sendChunkAck sends an acknowledgment for a received chunk
// This includes information about missing chunks to trigger retries
func (ph *PhotoHandler) sendChunkAck(senderUUID, senderDeviceID string, chunk *auraphone.PhotoChunkMessage) {
	device := ph.device
	prefix := fmt.Sprintf("%s %s", device.GetHardwareUUID()[:8], device.GetPlatform())
	coordinator := device.GetPhotoCoordinator()
	recvState := coordinator.GetReceiveState(senderDeviceID)

	if recvState == nil {
		logger.Warn(prefix, "⚠️  Cannot send ACK: no receive state for %s", senderDeviceID[:8])
		return
	}

	// Build list of missing chunks
	missingChunks := []int32{}
	for i := 0; i < int(chunk.TotalChunks); i++ {
		if _, exists := recvState.ReceivedChunks[i]; !exists {
			missingChunks = append(missingChunks, int32(i))
		}
	}

	transferComplete := recvState.ChunksReceived == recvState.TotalChunks

	// Create ACK message
	ack, err := CreatePhotoChunkAck(
		device.GetDeviceID(),
		senderDeviceID,
		chunk.PhotoHash,
		int32(chunk.ChunkIndex), // Last chunk we just received
		missingChunks,
		transferComplete,
	)
	if err != nil {
		logger.Warn(prefix, "❌ Failed to create chunk ACK: %v", err)
		return
	}

	// Encode ACK
	ackData, err := EncodePhotoChunkAck(ack)
	if err != nil {
		logger.Warn(prefix, "❌ Failed to encode chunk ACK: %v", err)
		return
	}

	// Send via protocol characteristic (not photo characteristic)
	connManager := device.GetConnManager()
	if err := connManager.SendToDevice(senderUUID, AuraProtocolCharUUID, ackData); err != nil {
		logger.Warn(prefix, "❌ Failed to send chunk ACK to %s: %v", senderDeviceID[:8], err)
		return
	}

	if transferComplete {
		logger.Debug(prefix, "✅ Sent completion ACK to %s for chunk %d/%d",
			senderDeviceID[:8], chunk.ChunkIndex+1, chunk.TotalChunks)
	} else {
		logger.Debug(prefix, "📤 Sent ACK to %s for chunk %d/%d (%d missing)",
			senderDeviceID[:8], chunk.ChunkIndex+1, chunk.TotalChunks, len(missingChunks))
	}
}

// RetryMissingChunks retries sending chunks that haven't been ACK'd
func (ph *PhotoHandler) RetryMissingChunks(targetUUID, targetDeviceID string, chunkIndices []int32) error {
	device := ph.device
	prefix := fmt.Sprintf("%s %s", device.GetHardwareUUID()[:8], device.GetPlatform())

	// Load our photo from cache
	photoPath := filepath.Join(GetDeviceCacheDir(device.GetHardwareUUID()), "my_photo.jpg")
	photoData, err := os.ReadFile(photoPath)
	if err != nil {
		return fmt.Errorf("failed to read photo: %w", err)
	}

	hash := sha256.Sum256(photoData)
	totalChunks := (len(photoData) + DefaultChunkSize - 1) / DefaultChunkSize

	logger.Info(prefix, "🔄 Retrying %d missing chunks to %s", len(chunkIndices), targetDeviceID[:8])

	// Retry each missing chunk
	for _, chunkIndex := range chunkIndices {
		if err := ph.sendPhotoChunk(targetUUID, targetDeviceID, int(chunkIndex), photoData, hash, totalChunks); err != nil {
			logger.Warn(prefix, "❌ Failed to retry chunk %d: %v", chunkIndex, err)
			return err
		}
		logger.Debug(prefix, "✅ Retried chunk %d to %s", chunkIndex+1, targetDeviceID[:8])
	}

	return nil
}
