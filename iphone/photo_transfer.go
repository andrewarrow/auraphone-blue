package iphone

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	pb "github.com/user/auraphone-blue/proto"
	"github.com/user/auraphone-blue/swift"
	"google.golang.org/protobuf/proto"
)

// ============================================================================
// Photo Transfer Logic
// ============================================================================

// handlePhotoRequest handles incoming photo request from a Peripheral (when we're Central)
// MULTI-HOP: Serves ANY cached photo, not just our own
func (ip *IPhone) handlePhotoRequest(peerUUID string, photoReq *pb.PhotoRequestMessage) {
	photoHashHex := fmt.Sprintf("%x", photoReq.PhotoHash)
	logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📥 Received photo request from %s (hash: %s, target: %s)",
		shortHash(peerUUID), shortHash(photoHashHex), shortHash(photoReq.TargetDeviceId))

	// Check if we have this photo cached (MULTI-HOP: could be ours or someone else's)
	if !ip.photoCache.HasPhoto(photoHashHex) {
		logger.Warn(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)),
			"⚠️  Don't have requested photo %s in cache",
			shortHash(photoHashHex))
		return
	}

	// Load the photo from cache
	photoData, err := ip.photoCache.GetPhoto(photoHashHex)
	if err != nil || len(photoData) == 0 {
		logger.Error(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)),
			"Failed to load photo %s from cache: %v", shortHash(photoHashHex), err)
		return
	}

	// Send the cached photo as Central via notification
	// MULTI-HOP: We're acting as a relay node for this photo
	ip.mu.RLock()
	ourPhotoHash := ip.photoHash
	ip.mu.RUnlock()

	if photoHashHex == ourPhotoHash {
		logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📤 Sending OUR photo to %s", shortHash(peerUUID))
	} else {
		logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "🔄 Multi-hop: Relaying photo %s to %s (for device %s)",
			shortHash(photoHashHex), shortHash(peerUUID), shortHash(photoReq.TargetDeviceId))
	}

	go ip.sendPhotoChunksWithData(peerUUID, photoData, photoHashHex, photoReq.TargetDeviceId)
}

// sendPhotoRequest sends a photo request notification to a Central (when we're Peripheral)
func (ip *IPhone) sendPhotoRequest(peerUUID string, photoHash string, targetDeviceID string) {
	// Build PhotoRequestMessage
	photoHashBytes := []byte{}
	for i := 0; i < len(photoHash); i += 2 {
		var b byte
		fmt.Sscanf(photoHash[i:i+2], "%02x", &b)
		photoHashBytes = append(photoHashBytes, b)
	}

	photoReq := &pb.PhotoRequestMessage{
		RequesterDeviceId: ip.deviceID,
		TargetDeviceId:    targetDeviceID,
		PhotoHash:         photoHashBytes,
	}

	data, err := proto.Marshal(photoReq)
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Failed to marshal photo request: %v", err)
		return
	}

	// Send as notification (Peripheral can send notifications to Central)
	err = ip.wire.NotifyCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraProtocolCharUUID, data)
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Failed to send photo request to %s: %v", shortHash(peerUUID), err)
	} else {
		logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📤 Sent photo request to %s (hash: %s)", shortHash(peerUUID), shortHash(photoHash))
	}
}

// requestAndReceivePhoto subscribes to photo characteristic to receive photo chunks
func (ip *IPhone) requestAndReceivePhoto(peerUUID string, photoHash string, deviceID string) {
	// Reserve transfer slot immediately to prevent duplicate subscriptions
	// We don't know total chunks yet, but we mark the transfer as in-progress
	ip.mu.Lock()
	ip.photoTransfers[peerUUID] = phone.NewPhotoTransferState(photoHash, 0, peerUUID, deviceID)
	ip.mu.Unlock()

	// Clean up transfer state if we exit early due to errors
	defer func() {
		if r := recover(); r != nil {
			ip.mu.Lock()
			delete(ip.photoTransfers, peerUUID)
			ip.mu.Unlock()
			panic(r)
		}
	}()

	// Determine our role for this connection
	ip.mu.RLock()
	peripheral, exists := ip.connectedPeers[peerUUID]
	ip.mu.RUnlock()

	if !exists {
		// We're Peripheral - send photo request via notification (realistic BLE behavior)
		// In real BLE, Peripherals can't initiate GATT reads, but they CAN send notifications
		// to request data from the Central
		logger.Debug(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📸 Requesting photo from %s via notification (we're Peripheral)", shortHash(peerUUID))
		ip.sendPhotoRequest(peerUUID, photoHash, deviceID)
		// Photo will arrive via notification when Central responds
		return
	}

	// Set up a delegate to handle photo notifications
	// This must be done BEFORE subscribing, regardless of whether services are already discovered
	peripheral.Delegate = &photoTransferDelegate{
		iphone:    ip,
		peerUUID:  peerUUID,
		photoHash: photoHash,
		deviceID:  deviceID,
	}

	// Discover services if not already done
	if len(peripheral.Services) == 0 {
		logger.Debug(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Discovering services on %s for photo transfer", shortHash(peerUUID))
		peripheral.DiscoverServices([]string{phone.AuraServiceUUID})
		// Note: Transfer state will be updated with actual chunk count when first chunk arrives
		return
	}

	// Find photo characteristic
	photoChar := peripheral.GetCharacteristic(phone.AuraServiceUUID, phone.AuraPhotoCharUUID)
	if photoChar == nil {
		logger.Warn(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Cannot request photo: characteristic not found on %s", shortHash(peerUUID))
		ip.mu.Lock()
		delete(ip.photoTransfers, peerUUID)
		ip.mu.Unlock()
		return
	}

	// Subscribe to photo notifications (this triggers the sender to start sending chunks)
	err := peripheral.SetNotifyValue(true, photoChar)
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Failed to subscribe to photo characteristic: %v", err)
		ip.mu.Lock()
		delete(ip.photoTransfers, peerUUID)
		ip.mu.Unlock()
		return
	}

	logger.Debug(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📸 Subscribed to photo characteristic from %s", shortHash(peerUUID))
}

// photoTransferDelegate methods
func (d *photoTransferDelegate) DidDiscoverServices(peripheral *swift.CBPeripheral, services []*swift.CBService, err error) {
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", d.iphone.hardwareUUID[:8]), "Failed to discover services for photo transfer: %v", err)
		return
	}

	logger.Debug(fmt.Sprintf("%s iOS", shortHash(d.iphone.hardwareUUID)), "✅ Services discovered for photo transfer from %s", shortHash(d.peerUUID))

	// Now subscribe to photo characteristic
	d.iphone.requestAndReceivePhoto(d.peerUUID, d.photoHash, d.deviceID)
}

func (d *photoTransferDelegate) DidDiscoverCharacteristics(peripheral *swift.CBPeripheral, service *swift.CBService, err error) {
	// Not needed for photo transfer
}

func (d *photoTransferDelegate) DidWriteValueForCharacteristic(peripheral *swift.CBPeripheral, characteristic *swift.CBCharacteristic, err error) {
	// Not needed for photo transfer
}

func (d *photoTransferDelegate) DidUpdateValueForCharacteristic(peripheral *swift.CBPeripheral, characteristic *swift.CBCharacteristic, err error) {
	// Handle incoming photo chunk notification
	if characteristic.UUID == phone.AuraPhotoCharUUID {
		d.iphone.handlePhotoData(d.peerUUID, characteristic.Value)
	}
}

// sendPhotoChunks sends photo chunks to a peer who subscribed
// sendPhotoChunks sends OUR photo (convenience wrapper)
func (ip *IPhone) sendPhotoChunks(peerUUID string) {
	ip.mu.RLock()
	photoData := ip.photoData
	photoHash := ip.photoHash
	deviceID := ip.deviceID
	ip.mu.RUnlock()

	ip.sendPhotoChunksWithData(peerUUID, photoData, photoHash, deviceID)
}

// sendPhotoChunksWithData sends ANY photo (for multi-hop relay)
func (ip *IPhone) sendPhotoChunksWithData(peerUUID string, photoData []byte, photoHash string, sourceDeviceID string) {
	if len(photoData) == 0 {
		logger.Warn(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "No photo to send to %s", shortHash(peerUUID))
		return
	}

	// Chunk the photo
	chunks := ip.photoChunker.ChunkPhoto(photoData)
	totalChunks := len(chunks)

	logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📤 Sending %d photo chunks to %s (hash: %s, source: %s)",
		totalChunks, shortHash(peerUUID), shortHash(photoHash), shortHash(sourceDeviceID))

	// Convert photo hash hex to bytes
	photoHashBytes := []byte{}
	for i := 0; i < len(photoHash); i += 2 {
		var b byte
		fmt.Sscanf(photoHash[i:i+2], "%02x", &b)
		photoHashBytes = append(photoHashBytes, b)
	}

	// Determine our role for this connection to use the correct BLE method
	ip.mu.RLock()
	_, isCentral := ip.connectedPeers[peerUUID]
	ip.mu.RUnlock()

	// Send each chunk
	for i, chunk := range chunks {
		chunkMsg := &pb.PhotoChunkMessage{
			SenderDeviceId: sourceDeviceID, // Original owner, not us (we might be relaying)
			TargetDeviceId: "",             // Will be filled by receiver
			PhotoHash:      photoHashBytes,
			ChunkIndex:     int32(i),
			TotalChunks:    int32(totalChunks),
			ChunkData:      chunk,
		}

		data, err := proto.Marshal(chunkMsg)
		if err != nil {
			logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to marshal chunk %d: %v", i, err)
			continue
		}

		// Use correct BLE method based on our role:
		// - Central writes to Peripheral's characteristics
		// - Peripheral sends notifications to Central
		if isCentral {
			// We're Central - write to their characteristic
			err = ip.wire.WriteCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraPhotoCharUUID, data)
		} else {
			// We're Peripheral - send notification
			err = ip.wire.NotifyCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraPhotoCharUUID, data)
		}

		if err != nil {
			logger.Error(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Failed to send chunk %d to %s: %v", i, shortHash(peerUUID), err)
		} else {
			logger.Trace(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📤 Sent chunk %d/%d to %s (%d bytes)",
				i+1, totalChunks, shortHash(peerUUID), len(chunk))
		}
	}

	logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "✅ Finished sending photo to %s", shortHash(peerUUID))
}

func (ip *IPhone) handlePhotoData(peerUUID string, data []byte) {
	// Parse as PhotoChunkMessage
	var chunkMsg pb.PhotoChunkMessage
	err := proto.Unmarshal(data, &chunkMsg)
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to unmarshal photo chunk: %v", err)
		return
	}

	photoHashHex := fmt.Sprintf("%x", chunkMsg.PhotoHash)

	logger.Trace(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📥 Received chunk %d/%d from %s (hash: %s, %d bytes)",
		chunkMsg.ChunkIndex+1, chunkMsg.TotalChunks, shortHash(peerUUID), shortHash(photoHashHex), len(chunkMsg.ChunkData))

	// Get or create transfer state
	ip.mu.Lock()
	transfer, exists := ip.photoTransfers[peerUUID]
	if !exists {
		transfer = phone.NewPhotoTransferState(photoHashHex, int(chunkMsg.TotalChunks), peerUUID, chunkMsg.SenderDeviceId)
		ip.photoTransfers[peerUUID] = transfer
	} else if transfer.TotalChunks == 0 {
		// Update total chunks if this was pre-created by requestAndReceivePhoto
		transfer.TotalChunks = int(chunkMsg.TotalChunks)
	}
	ip.mu.Unlock()

	// Add chunk to transfer state
	transfer.AddChunk(chunkMsg.ChunkData)

	// Check if transfer is complete
	if transfer.IsComplete() {
		logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "✅ Photo transfer complete from %s (%d bytes)",
			shortHash(peerUUID), len(transfer.GetData()))

		// Save photo to cache
		photoData := transfer.GetData()
		savedHash, err := ip.photoCache.SavePhoto(photoData, peerUUID, chunkMsg.SenderDeviceId)
		if err != nil {
			logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to save photo: %v", err)
		} else {
			logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "💾 Saved photo to cache (hash: %s)", shortHash(savedHash))

			// Mark photo as received in mesh view
			ip.meshView.MarkPhotoReceived(chunkMsg.SenderDeviceId)

			// Update discovered device with photo data
			ip.mu.Lock()
			if device, exists := ip.discovered[peerUUID]; exists {
				device.PhotoData = photoData
				device.PhotoHash = photoHashHex
				ip.discovered[peerUUID] = device

				// Notify GUI
				if ip.callback != nil {
					ip.callback(device)
				}
			}

			// Clean up transfer state
			delete(ip.photoTransfers, peerUUID)
			ip.mu.Unlock()
		}
	}
}
