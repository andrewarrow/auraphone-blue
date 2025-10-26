package iphone

import (
	"sync"
	"time"

	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/swift"
	"github.com/user/auraphone-blue/wire"
)

// photoReceiveState tracks photo reception progress
type photoReceiveState struct {
	mu              sync.Mutex
	isReceiving     bool
	expectedSize    uint32
	expectedCRC     uint32
	expectedChunks  uint16
	receivedChunks  map[uint16][]byte
	senderDeviceID  string
	buffer          []byte
	lastChunkTime   time.Time // Time of last chunk received (for timeout detection)
	retransmitCount int       // Number of retransmit requests sent
}

// iPhone represents an iOS device with BLE capabilities
type iPhone struct {
	hardwareUUID           string                              // Bluetooth hardware UUID (never changes, from testdata/hardware_uuids.txt)
	deviceID               string                              // Logical device ID (8-char base36, cached to disk)
	deviceName             string
	wire                   *wire.Wire
	cacheManager           *phone.DeviceCacheManager           // Persistent photo storage
	photoCoordinator       *phone.PhotoTransferCoordinator     // Photo transfer state (single source of truth)
	manager                *swift.CBCentralManager
	peripheralManager      *swift.CBPeripheralManager          // Peripheral mode: GATT server + advertising
	protocolChar           *swift.CBMutableCharacteristic      // Reference to protocol characteristic for notifications
	photoChar              *swift.CBMutableCharacteristic      // Reference to photo characteristic for notifications
	profileChar            *swift.CBMutableCharacteristic      // Reference to profile characteristic for notifications
	discoveryCallback      phone.DeviceDiscoveryCallback
	photoPath              string
	photoHash              string
	photoData              []byte
	localProfile           *phone.LocalProfile                 // Our local profile data
	mu                     sync.RWMutex                        // Protects all maps below
	connectedPeripherals   map[string]*swift.CBPeripheral      // peripheral UUID -> peripheral
	peripheralToDeviceID   map[string]string                   // hardware UUID -> logical device ID
	deviceIDToPhotoHash    map[string]string                   // deviceID -> their TX photo hash
	receivedPhotoHashes    map[string]string                   // deviceID -> RX hash (photos we got from them)
	receivedProfileVersion map[string]int32                    // deviceID -> their profile version
	lastHandshakeTime      map[string]time.Time                // hardware UUID -> last handshake timestamp (connection-scoped)
	photoReceiveState      map[string]*photoReceiveState       // peripheral UUID -> receive state (central mode) - DEPRECATED: use photoCoordinator
	photoReceiveStateServer map[string]*photoReceiveState      // senderUUID -> receive state for peripheral mode - DEPRECATED: use photoCoordinator
	staleCheckDone         chan struct{}                       // Signal channel for stopping background checker

	// NEW: Gossip protocol fields (Phase 3)
	meshView         *phone.MeshView
	messageRouter    *phone.MessageRouter
	connManager      *phone.ConnectionManager
	gossipInterval   time.Duration
	lastGossipTime   time.Time
}

// iPhonePeripheralDelegate wraps iPhone to implement CBPeripheralManagerDelegate
type iPhonePeripheralDelegate struct {
	iphone *iPhone
}
