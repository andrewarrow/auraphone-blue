package phone

import (
	"sync"
	"time"

	"github.com/user/auraphone-blue/proto"
)

// DeviceCommon defines the interface that both iPhone and Android must implement
// This allows shared code in phone/ to work with both platforms
type DeviceCommon interface {
	// Identity
	GetHardwareUUID() string
	GetDeviceID() string
	GetDeviceName() string
	GetPlatform() string // "iOS" or "Android"

	// Photo management
	GetPhotoHash() string
	GetPhotoData() []byte

	// Profile management
	GetLocalProfile() *LocalProfile

	// Connection management
	GetConnManager() *ConnectionManager
	GetMeshView() *MeshView
	GetCacheManager() *DeviceCacheManager
	GetPhotoCoordinator() *PhotoTransferCoordinator
	GetIdentityManager() *IdentityManager

	// UUID mapping (platform-specific map name: peripheralToDeviceID vs remoteUUIDToDeviceID)
	GetUUIDToDeviceIDMap() map[string]string
	GetMutex() *sync.RWMutex

	// Platform-specific disconnect (iOS uses CBCentralManager, Android uses BluetoothGatt)
	DisconnectFromDevice(hardwareUUID string) error

	// Discovery callback - triggers GUI update when device info changes
	TriggerDiscoveryUpdate(hardwareUUID, deviceID, photoHash string, photoData []byte)
}

// GossipHandler handles gossip protocol logic (shared between iOS and Android)
type GossipHandler struct {
	device         DeviceCommon
	gossipInterval time.Duration
	staleCheckDone chan struct{}
}

// NewGossipHandler creates a new gossip handler
func NewGossipHandler(device DeviceCommon, gossipInterval time.Duration, staleCheckDone chan struct{}) *GossipHandler {
	return &GossipHandler{
		device:         device,
		gossipInterval: gossipInterval,
		staleCheckDone: staleCheckDone,
	}
}

// PhotoHandler handles photo transfer logic (shared between iOS and Android)
type PhotoHandler struct {
	device DeviceCommon
}

// NewPhotoHandler creates a new photo handler
func NewPhotoHandler(device DeviceCommon) *PhotoHandler {
	return &PhotoHandler{
		device: device,
	}
}

// ProfileHandler handles profile exchange logic (shared between iOS and Android)
type ProfileHandler struct {
	device DeviceCommon
}

// NewProfileHandler creates a new profile handler
func NewProfileHandler(device DeviceCommon) *ProfileHandler {
	return &ProfileHandler{
		device: device,
	}
}

// ProfileRequestCallback is called when we need to request a profile
type ProfileRequestCallback func(deviceID string, version int32)

// PhotoRequestCallback is called when we need to request a photo
type PhotoRequestCallback func(deviceID, photoHash string)

// HandlePhotoRequestCallback is called when we receive a photo request
type HandlePhotoRequestCallback func(senderUUID string, req *proto.PhotoRequestMessage)

// HandleProfileRequestCallback is called when we receive a profile request
type HandleProfileRequestCallback func(senderUUID string, req *proto.ProfileRequestMessage)
