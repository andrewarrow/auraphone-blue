package wire

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/user/auraphone-blue/util"
	"github.com/user/auraphone-blue/wire/debug"
	"github.com/user/auraphone-blue/wire/gatt"
)

// Real BLE behavior: advertising data and GATT tables are stored per-device
// and discovered via filesystem scanning (simulates over-the-air discovery)
// No global registries - each device reads/writes its own files

// Wire handles Unix domain socket communication with BLE realism
// Single socket per device at {dataDir}/sockets/auraphone-{hardwareUUID}.sock
type Wire struct {
	hardwareUUID string
	socketPath   string
	listener     net.Listener
	connections  map[string]*Connection // peer UUID -> single connection
	mu           sync.RWMutex

	// Message handler for incoming GATT messages
	gattHandler func(peerUUID string, msg *GATTMessage)
	handlerMu   sync.RWMutex

	// Connection callbacks
	connectCallback    func(peerUUID string, role ConnectionRole)
	disconnectCallback func(peerUUID string)
	callbackMu         sync.RWMutex

	// Stop channels
	stopListening chan struct{}
	stopReading   map[string]chan struct{}
	stopMu        sync.RWMutex

	// Audit logging
	connectionEventLog  *ConnectionEventLogger
	socketHealthMonitor *SocketHealthMonitor

	// Debug logging (binary protocol packets)
	debugLogger *debug.DebugLogger

	// GATT attribute database (server-side)
	attributeDB *gatt.AttributeDatabase
	dbMu        sync.RWMutex
}

// NewWire creates a new Wire instance
func NewWire(hardwareUUID string) *Wire {
	socketDir := util.GetSocketDir()
	w := &Wire{
		hardwareUUID: hardwareUUID,
		socketPath:   filepath.Join(socketDir, fmt.Sprintf("auraphone-%s.sock", hardwareUUID)),
		connections:  make(map[string]*Connection),
		stopReading:  make(map[string]chan struct{}),
	}

	// Initialize audit loggers
	w.connectionEventLog = NewConnectionEventLogger(hardwareUUID, true)
	w.socketHealthMonitor = NewSocketHealthMonitor(hardwareUUID)

	// Initialize debug logger (enabled by default, check env var or can be disabled)
	debugEnabled := os.Getenv("WIRE_DEBUG") != "0" // Enabled unless explicitly disabled
	w.debugLogger = debug.NewDebugLogger(hardwareUUID, debugEnabled)

	// Initialize GATT attribute database
	w.attributeDB = gatt.NewAttributeDatabase()

	return w
}

// Start begins listening on the Unix domain socket
func (w *Wire) Start() error {
	// Clean up any existing socket file
	os.Remove(w.socketPath)

	// Create listener
	listener, err := net.Listen("unix", w.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", w.socketPath, err)
	}

	w.listener = listener
	w.stopListening = make(chan struct{})

	// Log socket creation and initialize health monitoring
	w.connectionEventLog.LogSocketCreated("peripheral", w.socketPath)
	w.socketHealthMonitor.InitializeSocket("peripheral", w.socketPath)
	w.socketHealthMonitor.StartPeriodicSnapshots()

	// Accept incoming connections
	go w.acceptConnections()

	return nil
}

// Stop stops the wire and cleans up resources (idempotent - safe to call multiple times)
func (w *Wire) Stop() {
	// Stop accepting new connections (check if already stopped)
	w.mu.Lock()
	if w.stopListening != nil {
		select {
		case <-w.stopListening:
			// Already stopped
			w.mu.Unlock()
			return
		default:
			close(w.stopListening)
		}
	}
	w.mu.Unlock()

	// Close listener
	if w.listener != nil {
		w.listener.Close()
	}

	// Close all connections
	w.mu.Lock()
	for uuid, connection := range w.connections {
		// Stop reading goroutine
		w.stopMu.Lock()
		if stopChan, exists := w.stopReading[uuid]; exists {
			select {
			case <-stopChan:
				// Already closed
			default:
				close(stopChan)
			}
			delete(w.stopReading, uuid)
		}
		w.stopMu.Unlock()

		connection.conn.Close()
	}
	w.connections = make(map[string]*Connection)
	w.mu.Unlock()

	// Stop health monitor and log socket closure
	if w.socketHealthMonitor != nil {
		w.socketHealthMonitor.MarkSocketClosed("peripheral")
		w.socketHealthMonitor.Stop()
	}
	w.connectionEventLog.LogSocketClosed("peripheral", "", "", "shutdown")

	// Clean up socket file
	os.Remove(w.socketPath)
}

// IsSubscribedToNotifications checks if a peer has enabled notifications for a characteristic
func (w *Wire) IsSubscribedToNotifications(peerUUID string, charHandle uint16) bool {
	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if !exists || connection.cccdManager == nil {
		return false
	}

	cccdManager := connection.cccdManager.(*gatt.CCCDManager)
	return cccdManager.IsNotifyEnabled(charHandle)
}

// IsSubscribedToIndications checks if a peer has enabled indications for a characteristic
func (w *Wire) IsSubscribedToIndications(peerUUID string, charHandle uint16) bool {
	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if !exists || connection.cccdManager == nil {
		return false
	}

	cccdManager := connection.cccdManager.(*gatt.CCCDManager)
	return cccdManager.IsIndicateEnabled(charHandle)
}

// GetSubscribedPeers returns all peers that have subscribed to notifications/indications for a characteristic
func (w *Wire) GetSubscribedPeers(charHandle uint16) []string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	var subscribedPeers []string
	for peerUUID, connection := range w.connections {
		if connection.cccdManager != nil {
			cccdManager := connection.cccdManager.(*gatt.CCCDManager)
			if cccdManager.IsSubscribed(charHandle) {
				subscribedPeers = append(subscribedPeers, peerUUID)
			}
		}
	}

	return subscribedPeers
}

// GetDiscoveryCache returns the discovery cache for a peer connection.
//
// iOS Caching Pattern (for swift/ package):
// iOS CoreBluetooth caches GATT discovery results persistently across disconnects.
// To simulate this behavior:
//   1. Before disconnect: cache := wire.GetDiscoveryCache(peerUUID)
//   2. Store cache in swift/ wrapper's persistent map[peerUUID]*gatt.DiscoveryCache
//   3. After reconnect: wire.SetDiscoveryCache(peerUUID, cache)
//
// Android Caching Pattern (for kotlin/ package):
// Android BluetoothGatt clears cache on disconnect/close.
// Current wire/ behavior (cache destroyed on disconnect) matches Android by default.
// No additional work needed in kotlin/ wrapper.
//
// Returns nil if not connected or if cache doesn't exist.
func (w *Wire) GetDiscoveryCache(peerUUID string) *gatt.DiscoveryCache {
	w.mu.RLock()
	defer w.mu.RUnlock()

	connection, exists := w.connections[peerUUID]
	if !exists || connection.discoveryCache == nil {
		return nil
	}

	return connection.discoveryCache.(*gatt.DiscoveryCache)
}

// SetDiscoveryCache sets the discovery cache for a peer connection.
//
// This allows pre-populating discovery results (e.g., iOS persistent cache pattern).
// Must be called while connected to the peer.
//
// iOS Usage (for swift/ package):
// After establishing connection, inject previously saved cache:
//   wire.Connect(peerUUID)
//   if cachedData := swiftWrapper.persistentCache[peerUUID]; cachedData != nil {
//       wire.SetDiscoveryCache(peerUUID, cachedData)
//   }
//
// Returns error if not connected to peer.
func (w *Wire) SetDiscoveryCache(peerUUID string, cache *gatt.DiscoveryCache) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	connection, exists := w.connections[peerUUID]
	if !exists {
		return fmt.Errorf("not connected to %s", peerUUID)
	}

	connection.discoveryCache = cache
	return nil
}
