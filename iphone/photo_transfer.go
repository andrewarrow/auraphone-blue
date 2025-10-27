package iphone

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/swift"
	pb "github.com/user/auraphone-blue/proto"
	"google.golang.org/protobuf/proto"
)

// ============================================================================
// Photo Transfer Logic
// ============================================================================

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

	// Find peripheral for this peer
	ip.mu.RLock()
	peripheral, exists := ip.connectedPeers[peerUUID]
	ip.mu.RUnlock()

	if !exists {
		logger.Warn(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Cannot request photo: not connected to %s", shortHash(peerUUID))
		ip.mu.Lock()
		delete(ip.photoTransfers, peerUUID)
		ip.mu.Unlock()
		return
	}

	// Discover services if not already done
	if len(peripheral.Services) == 0 {
		logger.Debug(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "Discovering services on %s for photo transfer", shortHash(peerUUID))

		// Set up a delegate to handle service discovery
		peripheral.Delegate = &photoTransferDelegate{
			iphone:    ip,
			peerUUID:  peerUUID,
			photoHash: photoHash,
			deviceID:  deviceID,
		}

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
func (ip *IPhone) sendPhotoChunks(peerUUID string) {
	ip.mu.RLock()
	photoData := ip.photoData
	photoHash := ip.photoHash
	deviceID := ip.deviceID
	ip.mu.RUnlock()

	if len(photoData) == 0 {
		logger.Warn(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "No photo to send to %s", shortHash(peerUUID))
		return
	}

	// Chunk the photo
	chunks := ip.photoChunker.ChunkPhoto(photoData)
	totalChunks := len(chunks)

	logger.Info(fmt.Sprintf("%s iOS", shortHash(ip.hardwareUUID)), "📤 Sending %d photo chunks to %s (hash: %s)",
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
			logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to marshal chunk %d: %v", i, err)
			continue
		}

		// Send via notification
		err = ip.wire.NotifyCharacteristic(peerUUID, phone.AuraServiceUUID, phone.AuraPhotoCharUUID, data)
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
