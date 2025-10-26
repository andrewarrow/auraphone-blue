package phone

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/proto"
)

// HandlePhotoRequest handles a request for a photo
func (ph *PhotoHandler) HandlePhotoRequest(senderUUID string, req *proto.PhotoRequestMessage) {
	device := ph.device
	prefix := fmt.Sprintf("%s %s", device.GetHardwareUUID()[:8], device.GetPlatform())

	// Check if they're requesting OUR photo
	if req.TargetDeviceId != device.GetDeviceID() {
		logger.Debug(prefix, "⏭️  Photo request for %s, not us", req.TargetDeviceId[:8])
		return
	}

	logger.Info(prefix, "📸 Sending our photo to %s", senderUUID[:8])

	// TODO: Implement photo sending using photoCoordinator
	// This will load our cached photo, chunk it, and send via appropriate mode
}

// HandlePhotoChunk receives photo chunk data from either Central or Peripheral mode
func (ph *PhotoHandler) HandlePhotoChunk(senderUUID string, data []byte) {
	device := ph.device
	prefix := fmt.Sprintf("%s %s", device.GetHardwareUUID()[:8], device.GetPlatform())

	// Get deviceID for this sender
	mutex := device.GetMutex()
	mutex.RLock()
	uuidToDeviceID := device.GetUUIDToDeviceIDMap()
	_, exists := uuidToDeviceID[senderUUID]
	mutex.RUnlock()

	if !exists {
		logger.Warn(prefix, "⚠️  Received photo chunk from unknown device %s", senderUUID[:8])
		return
	}

	logger.Debug(prefix, "📥 Photo chunk from %s (%d bytes)", senderUUID[:8], len(data))

	// TODO: Implement photo chunk handling using photoCoordinator
	// This will reassemble chunks, verify CRC, save to cache, and trigger discovery callback
}
