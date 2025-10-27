package phone

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/proto"
	protobuf "google.golang.org/protobuf/proto"
)

// MessageRouter handles incoming protocol messages and routes to appropriate handlers
type MessageRouter struct {
	meshView         *MeshView
	cacheManager     *DeviceCacheManager
	photoCoordinator *PhotoTransferCoordinator
	requestQueue     *RequestQueue
	identityManager  *IdentityManager

	// Device context for logging
	deviceHardwareUUID string
	devicePlatform     string

	// Callbacks for device-specific actions
	onPhotoNeeded    func(deviceID, photoHash string) error
	onProfileNeeded  func(deviceID string, version int32) error
	onPhotoRequest   func(senderUUID string, req *proto.PhotoRequestMessage)
	onProfileRequest func(senderUUID string, req *proto.ProfileRequestMessage)

	// Callback to store hardware UUID → device ID mapping
	onDeviceIDDiscovered func(hardwareUUID, deviceID string)

	// Callback to check if a device is currently connected
	isConnectedFunc func(hardwareUUID string) bool
}

// NewMessageRouter creates a new message router
func NewMessageRouter(
	meshView *MeshView,
	cacheManager *DeviceCacheManager,
	photoCoordinator *PhotoTransferCoordinator,
	hardwareUUID string,
	dataDir string,
) *MessageRouter {
	rq := NewRequestQueue(hardwareUUID, dataDir)
	rq.LoadFromDisk()

	return &MessageRouter{
		meshView:         meshView,
		cacheManager:     cacheManager,
		photoCoordinator: photoCoordinator,
		requestQueue:     rq,
	}
}

// SetCallbacks configures the callback functions
func (mr *MessageRouter) SetCallbacks(
	onPhotoNeeded func(deviceID, photoHash string) error,
	onProfileNeeded func(deviceID string, version int32) error,
	onPhotoRequest func(senderUUID string, req *proto.PhotoRequestMessage),
	onProfileRequest func(senderUUID string, req *proto.ProfileRequestMessage),
) {
	mr.onPhotoNeeded = onPhotoNeeded
	mr.onProfileNeeded = onProfileNeeded
	mr.onPhotoRequest = onPhotoRequest
	mr.onProfileRequest = onProfileRequest
}

// SetDeviceIDDiscoveredCallback sets the callback for when a device ID is discovered
func (mr *MessageRouter) SetDeviceIDDiscoveredCallback(callback func(hardwareUUID, deviceID string)) {
	mr.onDeviceIDDiscovered = callback
}

// SetIsConnectedCallback sets the callback to check if a device is connected
func (mr *MessageRouter) SetIsConnectedCallback(callback func(hardwareUUID string) bool) {
	mr.isConnectedFunc = callback
}

// SetIdentityManager sets the identity manager for connection state tracking
func (mr *MessageRouter) SetIdentityManager(im *IdentityManager) {
	mr.identityManager = im
}

// SetDeviceContext sets device info for logging
func (mr *MessageRouter) SetDeviceContext(hardwareUUID, platform string) {
	mr.deviceHardwareUUID = hardwareUUID
	mr.devicePlatform = platform
}

// HandleProtocolMessage handles incoming protocol messages (gossip and requests)
func (mr *MessageRouter) HandleProtocolMessage(senderUUID string, data []byte) error {
	// Try GossipMessage first (most common)
	gossip := &proto.GossipMessage{}
	if err := protobuf.Unmarshal(data, gossip); err == nil && gossip.SenderDeviceId != "" {
		return mr.handleGossipMessage(senderUUID, gossip)
	}

	// Try PhotoRequestMessage
	photoReq := &proto.PhotoRequestMessage{}
	if err := protobuf.Unmarshal(data, photoReq); err == nil && photoReq.RequesterDeviceId != "" {
		return mr.handlePhotoRequest(senderUUID, photoReq)
	}

	// Try ProfileRequestMessage
	profileReq := &proto.ProfileRequestMessage{}
	if err := protobuf.Unmarshal(data, profileReq); err == nil && profileReq.RequesterDeviceId != "" {
		return mr.handleProfileRequest(senderUUID, profileReq)
	}

	return fmt.Errorf("unknown protocol message type")
}

// handleGossipMessage processes incoming gossip messages
func (mr *MessageRouter) handleGossipMessage(senderUUID string, gossip *proto.GossipMessage) error {
	prefix := fmt.Sprintf("%s %s", mr.deviceHardwareUUID[:8], mr.devicePlatform)
	logger.Debug(prefix, "📨 Received gossip from %s (deviceID=%s, meshViewSize=%d)",
		senderUUID[:8], gossip.SenderDeviceId[:8], len(gossip.MeshView))

	// Store the mapping: hardware UUID → device ID
	// This allows us to send requests to devices we learn about via gossip
	if mr.onDeviceIDDiscovered != nil && gossip.SenderDeviceId != "" {
		logger.Debug(prefix, "🔑 Calling deviceID discovered callback: %s → %s", senderUUID[:8], gossip.SenderDeviceId[:8])
		mr.onDeviceIDDiscovered(senderUUID, gossip.SenderDeviceId)
		logger.Debug(prefix, "✅ Callback completed")
	} else {
		if mr.onDeviceIDDiscovered == nil {
			logger.Warn(prefix, "⚠️  Callback is nil! Cannot store mapping for %s", senderUUID[:8])
		}
		if gossip.SenderDeviceId == "" {
			logger.Warn(prefix, "⚠️  SenderDeviceId is empty! Cannot store mapping")
		}
	}

	// Merge gossip into mesh view
	newDiscoveries := mr.meshView.MergeGossip(gossip)
	logger.Debug(prefix, "📊 Merged gossip: %d new discoveries, total mesh size=%d",
		len(newDiscoveries), len(mr.meshView.GetAllDevices()))

	// Log new discoveries (caller can handle logging with proper prefix)
	_ = newDiscoveries

	// Check for missing photos
	// NEW (Week 3): GetMissingPhotos() now only returns connected devices, so we can send directly
	if mr.onPhotoNeeded != nil {
		missingPhotos := mr.meshView.GetMissingPhotos()
		for _, device := range missingPhotos {
			// We know these devices are connected, safe to send directly
			err := mr.onPhotoNeeded(device.DeviceID, device.PhotoHash)
			if err == nil {
				mr.meshView.MarkPhotoRequested(device.DeviceID)
			} else {
				logger.Warn(prefix, "Failed to send photo request for %s: %v", device.DeviceID[:8], err)
			}
		}
	}

	// Check for missing/updated profiles
	// NEW (Week 3): GetMissingProfiles() now only returns connected devices, so we can send directly
	if mr.onProfileNeeded != nil {
		missingProfiles := mr.meshView.GetMissingProfiles()
		for _, device := range missingProfiles {
			// We know these devices are connected, safe to send directly
			err := mr.onProfileNeeded(device.DeviceID, device.ProfileVersion)
			if err == nil {
				mr.meshView.MarkProfileRequested(device.DeviceID)
			} else {
				logger.Warn(prefix, "Failed to send profile request for %s: %v", device.DeviceID[:8], err)
			}
		}
	}

	return nil
}

// handlePhotoRequest processes incoming photo request messages
func (mr *MessageRouter) handlePhotoRequest(senderUUID string, req *proto.PhotoRequestMessage) error {
	if mr.onPhotoRequest != nil {
		mr.onPhotoRequest(senderUUID, req)
	}
	return nil
}

// handleProfileRequest processes incoming profile request messages
func (mr *MessageRouter) handleProfileRequest(senderUUID string, req *proto.ProfileRequestMessage) error {
	if mr.onProfileRequest != nil {
		mr.onProfileRequest(senderUUID, req)
	}
	return nil
}

// isConnected checks if a device is currently connected
func (mr *MessageRouter) isConnected(hardwareUUID string) bool {
	if mr.isConnectedFunc != nil {
		return mr.isConnectedFunc(hardwareUUID)
	}
	return false
}

// FlushQueueForConnection processes all pending requests for a newly connected device
// This replaces RetryMissingRequestsForConnection with a cleaner approach
func (mr *MessageRouter) FlushQueueForConnection(hardwareUUID string) {
	prefix := fmt.Sprintf("%s %s", mr.deviceHardwareUUID[:8], mr.devicePlatform)

	pendingRequests := mr.requestQueue.DequeueForConnection(hardwareUUID)

	if len(pendingRequests) > 0 {
		logger.Debug(prefix, "📤 Flushing %d pending requests for newly connected %s",
			len(pendingRequests), hardwareUUID[:8])
	}

	for _, req := range pendingRequests {
		switch req.Type {
		case RequestTypePhoto:
			err := mr.onPhotoNeeded(req.DeviceID, req.PhotoHash)
			if err == nil {
				mr.meshView.MarkPhotoRequested(req.DeviceID)
				logger.Debug(prefix, "✅ Sent queued photo request for %s", req.DeviceID[:8])
			} else {
				// Re-queue with incremented attempt count
				req.Attempts++
				if req.Attempts < 5 {
					mr.requestQueue.Enqueue(req)
					logger.Debug(prefix, "🔄 Re-queued photo request for %s (attempt %d)", req.DeviceID[:8], req.Attempts)
				} else {
					logger.Warn(prefix, "❌ Giving up on photo request for %s after %d attempts",
						req.DeviceID[:8], req.Attempts)
				}
			}

		case RequestTypeProfile:
			err := mr.onProfileNeeded(req.DeviceID, req.ProfileVersion)
			if err == nil {
				mr.meshView.MarkProfileRequested(req.DeviceID)
				logger.Debug(prefix, "✅ Sent queued profile request for %s", req.DeviceID[:8])
			} else {
				// Re-queue with incremented attempt count
				req.Attempts++
				if req.Attempts < 5 {
					mr.requestQueue.Enqueue(req)
					logger.Debug(prefix, "🔄 Re-queued profile request for %s (attempt %d)", req.DeviceID[:8], req.Attempts)
				} else {
					logger.Warn(prefix, "❌ Giving up on profile request for %s after %d attempts",
						req.DeviceID[:8], req.Attempts)
				}
			}
		}
	}

	// Save queue state after flushing
	if err := mr.requestQueue.SaveToDisk(); err != nil {
		logger.Warn(prefix, "Failed to save request queue: %v", err)
	}
}

// RetryMissingRequestsForConnection is deprecated - use FlushQueueForConnection instead
// Kept for backward compatibility during migration
func (mr *MessageRouter) RetryMissingRequestsForConnection(hardwareUUID string) {
	mr.FlushQueueForConnection(hardwareUUID)
}
