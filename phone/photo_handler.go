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

// HandlePhotoRequest handles a request for a photo
func (ph *PhotoHandler) HandlePhotoRequest(senderUUID string, req *auraphone.PhotoRequestMessage) {
	device := ph.device
	prefix := fmt.Sprintf("%s %s", device.GetHardwareUUID()[:8], device.GetPlatform())

	// Check if they're requesting OUR photo
	if req.TargetDeviceId != device.GetDeviceID() {
		logger.Debug(prefix, "⏭️  Photo request for %s, not us", req.TargetDeviceId[:8])
		return
	}

	logger.Info(prefix, "📸 Sending our photo to %s in response to request", req.RequesterDeviceId[:8])

	// Load our photo from cache (stored as my_photo.jpg by SetProfilePhoto)
	cacheManager := device.GetCacheManager()
	photoPath := filepath.Join(GetDeviceCacheDir(device.GetHardwareUUID()), "my_photo.jpg")
	photoData, err := os.ReadFile(photoPath)
	if err != nil {
		logger.Warn(prefix, "❌ Failed to read our photo: %v", err)
		return
	}

	// Calculate photo hash
	hash := sha256.Sum256(photoData)

	// Split into chunks
	chunks := SplitIntoChunks(photoData, DefaultChunkSize)
	totalChunks := int32(len(chunks))

	logger.Debug(prefix, "📤 Sending photo to %s (%d bytes, %d chunks)",
		req.RequesterDeviceId[:8], len(photoData), totalChunks)

	// Get photo coordinator
	coordinator := device.GetPhotoCoordinator()
	coordinator.StartSend(req.RequesterDeviceId, string(hash[:]), int(totalChunks))

	// Send chunks via connection manager
	connManager := device.GetConnManager()
	for i, chunkData := range chunks {
		chunk, err := CreatePhotoChunk(
			device.GetDeviceID(),
			req.RequesterDeviceId,
			hash[:],
			int32(i),
			totalChunks,
			chunkData,
		)
		if err != nil {
			logger.Warn(prefix, "❌ Failed to create chunk %d: %v", i, err)
			coordinator.FailSend(req.RequesterDeviceId, fmt.Sprintf("failed to create chunk: %v", err))
			return
		}

		encodedChunk, err := EncodePhotoChunk(chunk)
		if err != nil {
			logger.Warn(prefix, "❌ Failed to encode chunk %d: %v", i, err)
			coordinator.FailSend(req.RequesterDeviceId, fmt.Sprintf("failed to encode chunk: %v", err))
			return
		}

		// Send via photo characteristic
		if err := connManager.SendToDevice(senderUUID, AuraPhotoCharUUID, encodedChunk); err != nil {
			logger.Warn(prefix, "❌ Failed to send chunk %d: %v", i, err)
			coordinator.FailSend(req.RequesterDeviceId, fmt.Sprintf("failed to send chunk: %v", err))
			return
		}

		coordinator.UpdateSendProgress(req.RequesterDeviceId, i+1)
	}

	// Mark send as complete
	coordinator.CompleteSend(req.RequesterDeviceId, string(hash[:]))
	cacheManager.MarkPhotoSentToDevice(req.RequesterDeviceId, string(hash[:]))
}

// HandlePhotoChunk receives photo chunk data from either Central or Peripheral mode
func (ph *PhotoHandler) HandlePhotoChunk(senderUUID string, data []byte) {
	device := ph.device
	prefix := fmt.Sprintf("%s %s", device.GetHardwareUUID()[:8], device.GetPlatform())

	// Get deviceID for this sender
	mutex := device.GetMutex()
	mutex.RLock()
	uuidToDeviceID := device.GetUUIDToDeviceIDMap()
	deviceID, exists := uuidToDeviceID[senderUUID]
	mutex.RUnlock()

	if !exists {
		logger.Warn(prefix, "⚠️  Received photo chunk from unknown device %s", senderUUID[:8])
		return
	}

	// Decode protobuf chunk
	chunk, err := DecodePhotoChunk(data)
	if err != nil {
		logger.Warn(prefix, "❌ Failed to decode photo chunk from %s: %v", senderUUID[:8], err)
		return
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

	// Check if transfer is complete
	recvState = coordinator.GetReceiveState(deviceID)
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
