package iphone

import (
	"sync"
	"time"

	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/swift"
	"github.com/user/auraphone-blue/wire"
)

// shortHash returns first 8 chars of a hash string, or the full string if shorter
func shortHash(s string) string {
	if len(s) >= 8 {
		return s[:8]
	}
	if s == "" {
		return "(none)"
	}
	return s
}

// HandshakeMessage is exchanged when two devices first connect
type HandshakeMessage struct {
	HardwareUUID string `json:"hardware_uuid"`
	DeviceID     string `json:"device_id"`
	DeviceName   string `json:"device_name"`
	FirstName    string `json:"first_name"`
}

// IPhone implements the Phone interface for iOS devices
type IPhone struct {
	hardwareUUID string
	deviceID     string
	deviceName   string
	firstName    string

	wire            *wire.Wire
	central         *swift.CBCentralManager
	peripheral      *swift.CBPeripheralManager
	identityManager *phone.IdentityManager // THE ONLY place for UUID ↔ DeviceID mapping
	photoCache      *phone.PhotoCache      // Photo caching and storage
	photoChunker    *phone.PhotoChunker    // Photo chunking for BLE transfer

	discovered     map[string]phone.DiscoveredDevice    // hardwareUUID -> device
	handshaked     map[string]*HandshakeMessage         // hardwareUUID -> handshake data
	connectedPeers map[string]*swift.CBPeripheral       // hardwareUUID -> peripheral object
	photoTransfers map[string]*phone.PhotoTransferState // hardwareUUID -> in-progress transfer

	// Gossip protocol (shared logic in phone/mesh_view.go)
	meshView     *phone.MeshView
	gossipTicker *time.Ticker
	stopGossip   chan struct{}

	mu             sync.RWMutex
	callback       phone.DeviceDiscoveryCallback
	profilePhoto   string
	photoHash      string            // SHA-256 hash of our current profile photo
	photoData      []byte            // Our current profile photo data
	profile        map[string]string // Profile fields (last_name, tagline, etc.)
	profileVersion int32             // Increments on any profile change
}

// photoTransferDelegate handles service discovery for photo transfers
type photoTransferDelegate struct {
	iphone    *IPhone
	peerUUID  string
	photoHash string
	deviceID  string
}
