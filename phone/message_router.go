package phone

import (
	"fmt"

	"github.com/user/auraphone-blue/proto"
	protobuf "google.golang.org/protobuf/proto"
)

// MessageRouter handles incoming protocol messages and routes to appropriate handlers
type MessageRouter struct {
	meshView         *MeshView
	cacheManager     *DeviceCacheManager
	photoCoordinator *PhotoTransferCoordinator

	// Callbacks for device-specific actions
	onPhotoNeeded    func(deviceID, photoHash string)
	onProfileNeeded  func(deviceID string, version int32)
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
	onPhotoNeeded func(deviceID, photoHash string),
	onProfileNeeded func(deviceID string, version int32),
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
	// Store the mapping: hardware UUID → device ID
	// This allows us to send requests to devices we learn about via gossip
	if mr.onDeviceIDDiscovered != nil && gossip.SenderDeviceId != "" {
		mr.onDeviceIDDiscovered(senderUUID, gossip.SenderDeviceId)
	}

	// Merge gossip into mesh view
	newDiscoveries := mr.meshView.MergeGossip(gossip)

	// Log new discoveries (caller can handle logging with proper prefix)
	_ = newDiscoveries

	// Check for missing photos
	if mr.onPhotoNeeded != nil {
		missingPhotos := mr.meshView.GetMissingPhotos()
		for _, device := range missingPhotos {
			mr.onPhotoNeeded(device.DeviceID, device.PhotoHash)
			mr.meshView.MarkPhotoRequested(device.DeviceID)
		}
	}

	// Check for missing/updated profiles
	if mr.onProfileNeeded != nil {
		missingProfiles := mr.meshView.GetMissingProfiles()
		for _, device := range missingProfiles {
			mr.onProfileNeeded(device.DeviceID, device.ProfileVersion)
			mr.meshView.MarkProfileRequested(device.DeviceID)
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
