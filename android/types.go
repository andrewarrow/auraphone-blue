package android

import (
	"sync"
	"time"

	"github.com/user/auraphone-blue/kotlin"
	"github.com/user/auraphone-blue/phone"
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

// Android implements the Phone interface for Android devices
type Android struct {
	hardwareUUID string
	deviceID     string
	deviceName   string
	firstName    string

	wire            *wire.Wire
	manager         *kotlin.BluetoothManager
	scanner         *kotlin.BluetoothLeScanner
	advertiser      *kotlin.BluetoothLeAdvertiser
	gattServer      *kotlin.BluetoothGattServer
	identityManager *phone.IdentityManager // THE ONLY place for UUID ↔ DeviceID mapping
	photoCache      *phone.PhotoCache      // Photo caching and storage
	photoChunker    *phone.PhotoChunker    // Photo chunking for BLE transfer

	discovered     map[string]phone.DiscoveredDevice    // hardwareUUID -> device
	handshaked     map[string]*HandshakeMessage         // hardwareUUID -> handshake data
	connectedGatts map[string]*kotlin.BluetoothGatt     // hardwareUUID -> GATT connection (central mode)
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
