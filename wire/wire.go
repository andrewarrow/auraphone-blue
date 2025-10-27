package wire

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
)

// Wire handles Unix domain socket communication with BLE realism
// Single socket per device at /tmp/auraphone-{hardwareUUID}.sock
type Wire struct {
	hardwareUUID string
	socketPath   string
	listener     net.Listener
	connections  map[string]net.Conn // peer UUID -> connection
	mu           sync.RWMutex

	// Message handler for incoming data
	messageHandler func(peerUUID string, data []byte)
	handlerMu      sync.RWMutex

	// Stop channels
	stopListening chan struct{}
	stopReading   map[string]chan struct{}
	stopMu        sync.RWMutex
}

// NewWire creates a new Wire instance
func NewWire(hardwareUUID string) *Wire {
	return &Wire{
		hardwareUUID: hardwareUUID,
		socketPath:   fmt.Sprintf("/tmp/auraphone-%s.sock", hardwareUUID),
		connections:  make(map[string]net.Conn),
		stopReading:  make(map[string]chan struct{}),
	}
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

	// Accept incoming connections
	go w.acceptConnections()

	return nil
}

// Stop stops the wire and cleans up resources
func (w *Wire) Stop() {
	// Stop accepting new connections
	if w.stopListening != nil {
		close(w.stopListening)
	}

	// Close listener
	if w.listener != nil {
		w.listener.Close()
	}

	// Close all connections
	w.mu.Lock()
	for uuid, conn := range w.connections {
		// Stop reading goroutine
		w.stopMu.Lock()
		if stopChan, exists := w.stopReading[uuid]; exists {
			close(stopChan)
			delete(w.stopReading, uuid)
		}
		w.stopMu.Unlock()

		conn.Close()
	}
	w.connections = make(map[string]net.Conn)
	w.mu.Unlock()

	// Clean up socket file
	os.Remove(w.socketPath)
}

// acceptConnections handles incoming connections
func (w *Wire) acceptConnections() {
	for {
		select {
		case <-w.stopListening:
			return
		default:
		}

		conn, err := w.listener.Accept()
		if err != nil {
			// Check if we're stopping
			select {
			case <-w.stopListening:
				return
			default:
			}
			continue
		}

		// Read handshake: 4-byte UUID length + UUID bytes
		go w.handleIncomingConnection(conn)
	}
}

// handleIncomingConnection processes a new incoming connection
func (w *Wire) handleIncomingConnection(conn net.Conn) {
	// Read UUID length (4 bytes)
	var uuidLen uint32
	err := binary.Read(conn, binary.BigEndian, &uuidLen)
	if err != nil {
		conn.Close()
		return
	}

	// Read UUID
	uuidBytes := make([]byte, uuidLen)
	_, err = io.ReadFull(conn, uuidBytes)
	if err != nil {
		conn.Close()
		return
	}

	peerUUID := string(uuidBytes)

	// Store connection
	w.mu.Lock()
	w.connections[peerUUID] = conn
	w.mu.Unlock()

	// Start reading messages from this connection
	stopChan := make(chan struct{})
	w.stopMu.Lock()
	w.stopReading[peerUUID] = stopChan
	w.stopMu.Unlock()

	w.readMessages(peerUUID, conn, stopChan)
}

// Connect establishes a connection to a peer
func (w *Wire) Connect(peerUUID string) error {
	// Check if already connected
	w.mu.RLock()
	_, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if exists {
		return nil // Already connected
	}

	// Connect to peer's socket
	peerSocketPath := fmt.Sprintf("/tmp/auraphone-%s.sock", peerUUID)
	conn, err := net.Dial("unix", peerSocketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", peerUUID, err)
	}

	// Send handshake: our UUID
	uuidBytes := []byte(w.hardwareUUID)
	uuidLen := uint32(len(uuidBytes))

	err = binary.Write(conn, binary.BigEndian, uuidLen)
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to send handshake: %w", err)
	}

	_, err = conn.Write(uuidBytes)
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to send handshake: %w", err)
	}

	// Store connection
	w.mu.Lock()
	w.connections[peerUUID] = conn
	w.mu.Unlock()

	// Start reading messages from this connection
	stopChan := make(chan struct{})
	w.stopMu.Lock()
	w.stopReading[peerUUID] = stopChan
	w.stopMu.Unlock()

	go w.readMessages(peerUUID, conn, stopChan)

	return nil
}

// readMessages continuously reads messages from a connection
func (w *Wire) readMessages(peerUUID string, conn net.Conn, stopChan chan struct{}) {
	defer func() {
		// Clean up on exit
		w.mu.Lock()
		delete(w.connections, peerUUID)
		w.mu.Unlock()

		w.stopMu.Lock()
		delete(w.stopReading, peerUUID)
		w.stopMu.Unlock()

		conn.Close()
	}()

	for {
		select {
		case <-stopChan:
			return
		default:
		}

		// Read message length (4 bytes)
		var msgLen uint32
		err := binary.Read(conn, binary.BigEndian, &msgLen)
		if err != nil {
			return // Connection closed or error
		}

		// Read message data
		msgData := make([]byte, msgLen)
		_, err = io.ReadFull(conn, msgData)
		if err != nil {
			return // Connection closed or error
		}

		// Call message handler
		w.handlerMu.RLock()
		handler := w.messageHandler
		w.handlerMu.RUnlock()

		if handler != nil {
			handler(peerUUID, msgData)
		}
	}
}

// SendMessage sends a message to a peer
func (w *Wire) SendMessage(peerUUID string, data []byte) error {
	w.mu.RLock()
	conn, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if !exists {
		return fmt.Errorf("not connected to %s", peerUUID)
	}

	// Send length-prefixed message
	msgLen := uint32(len(data))

	// Write length
	err := binary.Write(conn, binary.BigEndian, msgLen)
	if err != nil {
		return fmt.Errorf("failed to send message length: %w", err)
	}

	// Write data
	_, err = conn.Write(data)
	if err != nil {
		return fmt.Errorf("failed to send message data: %w", err)
	}

	return nil
}

// SetMessageHandler sets the callback for incoming messages
func (w *Wire) SetMessageHandler(handler func(peerUUID string, data []byte)) {
	w.handlerMu.Lock()
	w.messageHandler = handler
	w.handlerMu.Unlock()
}

// ListAvailableDevices scans /tmp for .sock files and returns hardware UUIDs
func (w *Wire) ListAvailableDevices() []string {
	devices := make([]string, 0)

	// Scan /tmp for auraphone-*.sock files
	matches, err := filepath.Glob("/tmp/auraphone-*.sock")
	if err != nil {
		return devices
	}

	for _, path := range matches {
		// Extract UUID from filename
		filename := filepath.Base(path)
		// Format: auraphone-{UUID}.sock
		if len(filename) < len("auraphone-") || filepath.Ext(filename) != ".sock" {
			continue
		}

		uuid := filename[len("auraphone-") : len(filename)-len(".sock")]

		// Don't include ourselves
		if uuid != w.hardwareUUID {
			devices = append(devices, uuid)
		}
	}

	return devices
}

// GetHardwareUUID returns this device's hardware UUID
func (w *Wire) GetHardwareUUID() string {
	return w.hardwareUUID
}

// IsConnected checks if we're connected to a peer
func (w *Wire) IsConnected(peerUUID string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	_, exists := w.connections[peerUUID]
	return exists
}
