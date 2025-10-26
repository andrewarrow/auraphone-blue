package android

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/proto"
)

// Photo transfer handling (simplified - uses photoCoordinator)

// handlePhotoRequest handles a request for a photo
func (a *Android) handlePhotoRequest(senderUUID string, req *proto.PhotoRequestMessage) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	// Check if they're requesting OUR photo
	if req.TargetDeviceId != a.deviceID {
		logger.Debug(prefix, "⏭️  Photo request for %s, not us", req.TargetDeviceId[:8])
		return
	}

	logger.Info(prefix, "📸 Sending our photo to %s", senderUUID[:8])

	// TODO: Implement photo sending using photoCoordinator
	// This will load our cached photo, chunk it, and send via appropriate mode
}

// handlePhotoChunk receives photo chunk data from either Central or Peripheral mode
func (a *Android) handlePhotoChunk(senderUUID string, data []byte) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	// Get deviceID for this sender
	a.mu.RLock()
	_, exists := a.remoteUUIDToDeviceID[senderUUID]
	a.mu.RUnlock()

	if !exists {
		logger.Warn(prefix, "⚠️  Received photo chunk from unknown device %s", senderUUID[:8])
		return
	}

	logger.Debug(prefix, "📥 Photo chunk from %s (%d bytes)", senderUUID[:8], len(data))

	// TODO: Implement photo chunk handling using photoCoordinator
	// This will reassemble chunks, verify CRC, save to cache, and trigger discovery callback
}
