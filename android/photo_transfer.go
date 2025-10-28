package android

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	pb "github.com/user/auraphone-blue/proto"
	"google.golang.org/protobuf/proto"
)

// ============================================================================
// Photo Transfer Logic
// ============================================================================

// requestAndReceivePhoto subscribes to photo characteristic to receive photo chunks
func (a *Android) requestAndReceivePhoto(peerUUID string, photoHash string, deviceID string) {
	// Reserve transfer slot immediately to prevent duplicate subscriptions
	// We don't know total chunks yet, but we mark the transfer as in-progress
	a.mu.Lock()
	a.photoTransfers[peerUUID] = phone.NewPhotoTransferState(photoHash, 0, peerUUID, deviceID)
	a.mu.Unlock()

	// Clean up transfer state if we exit early due to errors
	defer func() {
		if r := recover(); r != nil {
			a.mu.Lock()
			delete(a.photoTransfers, peerUUID)
			a.mu.Unlock()
			panic(r)
		}
	}()

	// Find GATT connection for this peer
	a.mu.RLock()
	gatt, exists := a.connectedGatts[peerUUID]
	a.mu.RUnlock()

	if !exists {
		logger.Warn(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Cannot request photo: not connected to %s", shortHash(peerUUID))
		a.mu.Lock()
		delete(a.photoTransfers, peerUUID)
		a.mu.Unlock()
		return
	}

	// Check if services are discovered
	services := gatt.GetServices()
	if len(services) == 0 {
		logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Services not discovered yet on %s, waiting...", shortHash(peerUUID))
		// Services will be discovered via OnServicesDiscovered callback, which already subscribes
		return
	}

	// Find photo characteristic
	photoChar := gatt.GetCharacteristic(phone.AuraServiceUUID, phone.AuraPhotoCharUUID)
	if photoChar == nil {
		logger.Warn(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Cannot request photo: characteristic not found on %s", shortHash(peerUUID))
		a.mu.Lock()
		delete(a.photoTransfers, peerUUID)
		a.mu.Unlock()
		return
	}

	// Subscribe to photo notifications (this triggers the sender to start sending chunks)
	gatt.SetCharacteristicNotification(photoChar, true)

	logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "📸 Subscribed to photo characteristic from %s", shortHash(peerUUID))
}

// sendPhotoChunks sends photo chunks to a peer who subscribed
func (a *Android) sendPhotoChunks(peerUUID string) {
	a.mu.RLock()
	photoData := a.photoData
	photoHash := a.photoHash
	deviceID := a.deviceID
	a.mu.RUnlock()

	if len(photoData) == 0 {
		logger.Warn(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "No photo to send to %s", shortHash(peerUUID))
		return
	}

	// Chunk the photo
	chunks := a.photoChunker.ChunkPhoto(photoData)
	totalChunks := len(chunks)

	logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "📤 Sending %d photo chunks to %s (hash: %s)",
		totalChunks, shortHash(peerUUID), shortHash(photoHash))

	// Convert photo hash hex to bytes
	photoHashBytes := []byte{}
	for i := 0; i < len(photoHash); i += 2 {
		var b byte
		fmt.Sscanf(photoHash[i:i+2], "%02x", &b)
		photoHashBytes = append(photoHashBytes, b)
	}

	// Send each chunk
	for i, chunk := range chunks {
		chunkMsg := &pb.PhotoChunkMessage{
			SenderDeviceId: deviceID,
			TargetDeviceId: "", // Will be filled by receiver
			PhotoHash:      photoHashBytes,
			ChunkIndex:     int32(i),
			TotalChunks:    int32(totalChunks),
			ChunkData:      chunk,
		}

		data, err := proto.Marshal(chunkMsg)
		if err != nil {
			logger.Error(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "Failed to marshal chunk %d: %v", i, err)
			continue
		}

		// Send via notification
		err = a.wire.NotifyCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraPhotoCharUUID, data)
		if err != nil {
			logger.Error(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Failed to send chunk %d to %s: %v", i, shortHash(peerUUID), err)
		} else {
			logger.Trace(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "📤 Sent chunk %d/%d to %s (%d bytes)",
				i+1, totalChunks, shortHash(peerUUID), len(chunk))
		}
	}

	logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "✅ Finished sending photo to %s", shortHash(peerUUID))
}

func (a *Android) handlePhotoChunk(peerUUID string, data []byte) {
	// Parse as PhotoChunkMessage
	var chunkMsg pb.PhotoChunkMessage
	err := proto.Unmarshal(data, &chunkMsg)
	if err != nil {
		logger.Error(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "Failed to unmarshal photo chunk: %v", err)
		return
	}

	photoHashHex := fmt.Sprintf("%x", chunkMsg.PhotoHash)

	logger.Trace(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "📥 Received chunk %d/%d from %s (hash: %s, %d bytes)",
		chunkMsg.ChunkIndex+1, chunkMsg.TotalChunks, shortHash(peerUUID), shortHash(photoHashHex), len(chunkMsg.ChunkData))

	// Get or create transfer state
	a.mu.Lock()
	transfer, exists := a.photoTransfers[peerUUID]
	if !exists {
		transfer = phone.NewPhotoTransferState(photoHashHex, int(chunkMsg.TotalChunks), peerUUID, chunkMsg.SenderDeviceId)
		a.photoTransfers[peerUUID] = transfer
	} else if transfer.TotalChunks == 0 {
		// Update total chunks if this was pre-created by requestAndReceivePhoto
		transfer.TotalChunks = int(chunkMsg.TotalChunks)
	}
	a.mu.Unlock()

	// Add chunk to transfer state
	transfer.AddChunk(chunkMsg.ChunkData)

	// Check if transfer is complete
	if transfer.IsComplete() {
		logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "✅ Photo transfer complete from %s (%d bytes)",
			shortHash(peerUUID), len(transfer.GetData()))

		// Save photo to cache
		photoData := transfer.GetData()
		savedHash, err := a.photoCache.SavePhoto(photoData, peerUUID, chunkMsg.SenderDeviceId)
		if err != nil {
			logger.Error(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "Failed to save photo: %v", err)
		} else {
			logger.Info(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "💾 Saved photo to cache (hash: %s)", shortHash(savedHash))

			// Mark photo as received in mesh view
			a.meshView.MarkPhotoReceived(chunkMsg.SenderDeviceId)

			// Update discovered device with photo data
			a.mu.Lock()
			if device, exists := a.discovered[peerUUID]; exists {
				device.PhotoData = photoData
				device.PhotoHash = photoHashHex
				a.discovered[peerUUID] = device

				// Notify GUI
				if a.callback != nil {
					a.callback(device)
				}
			}

			// Clean up transfer state
			delete(a.photoTransfers, peerUUID)
			a.mu.Unlock()
		}
	}
}

