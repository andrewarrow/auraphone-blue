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

// handlePhotoRequest handles incoming photo request from a Peripheral (when we're Central)
func (a *Android) handlePhotoRequest(peerUUID string, photoReq *pb.PhotoRequestMessage) {
	photoHashHex := fmt.Sprintf("%x", photoReq.PhotoHash)
	logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "📥 Received photo request from %s (hash: %s)",
		shortHash(peerUUID), shortHash(photoHashHex))

	// Verify they're requesting OUR photo
	a.mu.RLock()
	ourPhotoHash := a.photoHash
	a.mu.RUnlock()

	if photoHashHex != ourPhotoHash {
		logger.Warn(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)),
			"⚠️  Photo request mismatch: they want %s, we have %s",
			shortHash(photoHashHex), shortHash(ourPhotoHash))
		return
	}

	// Send our photo as Central via notification (Peripheral requested it)
	// In real BLE, when Central receives a request notification from Peripheral,
	// Central can send data back via characteristic writes OR notifications
	logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "📤 Sending photo to %s in response to request", shortHash(peerUUID))
	go a.sendPhotoChunks(peerUUID)
}

// sendPhotoRequest sends a photo request notification to a Central (when we're Peripheral)
func (a *Android) sendPhotoRequest(peerUUID string, photoHash string, targetDeviceID string) {
	// Build PhotoRequestMessage
	photoHashBytes := []byte{}
	for i := 0; i < len(photoHash); i += 2 {
		var b byte
		fmt.Sscanf(photoHash[i:i+2], "%02x", &b)
		photoHashBytes = append(photoHashBytes, b)
	}

	photoReq := &pb.PhotoRequestMessage{
		RequesterDeviceId: a.deviceID,
		TargetDeviceId:    targetDeviceID,
		PhotoHash:         photoHashBytes,
	}

	data, err := proto.Marshal(photoReq)
	if err != nil {
		logger.Error(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Failed to marshal photo request: %v", err)
		return
	}

	// Send as notification (Peripheral can send notifications to Central)
	err = a.wire.NotifyCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
	if err != nil {
		logger.Error(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "Failed to send photo request to %s: %v", shortHash(peerUUID), err)
	} else {
		logger.Info(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "📤 Sent photo request to %s (hash: %s)", shortHash(peerUUID), shortHash(photoHash))
	}
}

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

	// Determine our role for this connection
	a.mu.RLock()
	gatt, exists := a.connectedGatts[peerUUID]
	a.mu.RUnlock()

	if !exists {
		// We're Peripheral - send photo request via notification (realistic BLE behavior)
		// In real BLE, Peripherals can't initiate GATT reads, but they CAN send notifications
		// to request data from the Central
		logger.Debug(fmt.Sprintf("%s Android", shortHash(a.hardwareUUID)), "📸 Requesting photo from %s via notification (we're Peripheral)", shortHash(peerUUID))
		a.sendPhotoRequest(peerUUID, photoHash, deviceID)
		// Photo will arrive via notification when Central responds
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

	// Determine our role for this connection to use the correct BLE method
	a.mu.RLock()
	_, isCentral := a.connectedGatts[peerUUID]
	a.mu.RUnlock()

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

		// Use correct BLE method based on our role:
		// - Central writes to Peripheral's characteristics
		// - Peripheral sends notifications to Central
		if isCentral {
			// We're Central - write to their characteristic
			err = a.wire.WriteCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraPhotoCharUUID, data)
		} else {
			// We're Peripheral - send notification
			err = a.wire.NotifyCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraPhotoCharUUID, data)
		}

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

