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
}

// NewMessageRouter creates a new message router
func NewMessageRouter(
	meshView *MeshView,
	cacheManager *DeviceCacheManager,
	photoCoordinator *PhotoTransferCoordinator,
) *MessageRouter {
	return &MessageRouter{
		meshView:         meshView,
		cacheManager:     cacheManager,
		photoCoordinator: photoCoordinator,
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
	if mr.onPhotoNeeded != nil {
		missingPhotos := mr.meshView.GetMissingPhotos()
		for _, device := range missingPhotos {
			err := mr.onPhotoNeeded(device.DeviceID, device.PhotoHash)
			if err == nil {
				// Only mark as requested if send succeeded
				mr.meshView.MarkPhotoRequested(device.DeviceID)
			}
		}
	}

	// Check for missing/updated profiles
	if mr.onProfileNeeded != nil {
		missingProfiles := mr.meshView.GetMissingProfiles()
		for _, device := range missingProfiles {
			err := mr.onProfileNeeded(device.DeviceID, device.ProfileVersion)
			if err == nil {
				// Only mark as requested if send succeeded
				mr.meshView.MarkProfileRequested(device.DeviceID)
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
