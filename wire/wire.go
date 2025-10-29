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
