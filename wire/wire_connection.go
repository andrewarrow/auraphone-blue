package wire

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"time"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/util"
	"github.com/user/auraphone-blue/wire/att"
	"github.com/user/auraphone-blue/wire/gatt"
	"github.com/user/auraphone-blue/wire/l2cap"
)

// acceptConnections handles incoming connections
func (w *Wire) acceptConnections() {
	defer w.wg.Done()
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
		w.wg.Add(1)
		go w.handleIncomingConnection(conn)
	}
}

// handleIncomingConnection processes a new incoming connection (we become Peripheral)
// REALISTIC BLE: Connection establishment is a bidirectional radio exchange. The delay
// is already represented by the central's sleep during Connect(). Once the socket is
// accepted, the link-layer handshake is complete and both sides have the connection.
func (w *Wire) handleIncomingConnection(conn net.Conn) {
	defer w.wg.Done()
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

	// Log connection accepted
	w.connectionEventLog.LogConnectionAccepted("peripheral", peerUUID, "")

	// Check if already connected
	w.mu.Lock()
	if _, exists := w.connections[peerUUID]; exists {
		w.mu.Unlock()
		conn.Close()
		return
	}

	// Store connection with Peripheral role (they initiated)
	defaultParams := l2cap.DefaultConnectionParameters()
	connection := &Connection{
		conn:                 conn,
		remoteUUID:           peerUUID,
		role:                 RolePeripheral,
		mtu:                  DefaultMTU,                            // Start with default MTU
		mtuExchangeCompleted: true,                                  // Peripheral is ready immediately (doesn't initiate MTU exchange)
		fragmenter:           att.NewFragmenter(),                   // Initialize fragmenter for long writes
		requestTracker:       att.NewRequestTracker(0),              // Initialize request tracker with default 30s timeout
		params:               defaultParams,                         // Start with default connection parameters
		paramsUpdatedAt:      time.Now(),
		discoveryCache:       gatt.NewDiscoveryCache(),              // Initialize discovery cache for client-side discovery
		cccdManager:          gatt.NewCCCDManager(),                 // Initialize CCCD subscription manager
		eventScheduler:       NewConnectionEventScheduler(RolePeripheral, defaultParams.IntervalMaxMs()), // Initialize connection event scheduler
	}
	w.connections[peerUUID] = connection
	w.mu.Unlock()

	// Log connection established and record in health monitor
	w.connectionEventLog.LogConnectionEstablished("peripheral", peerUUID, "", "")
	w.socketHealthMonitor.RecordConnection("peripheral", peerUUID)

	// Notify callback
	w.callbackMu.RLock()
	connectCb := w.connectCallback
	w.callbackMu.RUnlock()
	if connectCb != nil {
		connectCb(peerUUID, RolePeripheral)
	}

	// Log read loop started
	w.connectionEventLog.LogReadLoopStarted("peripheral", peerUUID, "")

	// Start reading messages from this connection
	stopChan := make(chan struct{})
	w.stopMu.Lock()
	w.stopReading[peerUUID] = stopChan
	w.stopMu.Unlock()

	w.wg.Add(1)
	go w.readMessages(peerUUID, connection, stopChan)
}

// Connect establishes a connection to a peer (we become Central)
// Returns error if already connected or if concurrent connection is detected
func (w *Wire) Connect(peerUUID string) error {
	// Check if already connected
	w.mu.RLock()
	_, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if exists {
		return fmt.Errorf("already connected to %s", peerUUID)
	}

	// Simulate connection establishment delay (real BLE takes 30-100ms)
	time.Sleep(randomDelay(MinConnectionDelay, MaxConnectionDelay))

	// Connect to peer's socket
	socketDir := util.GetSocketDir()
	peerSocketPath := filepath.Join(socketDir, fmt.Sprintf("auraphone-%s.sock", peerUUID))
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

	// Store connection with Central role (we initiated)
	defaultParams := l2cap.DefaultConnectionParameters()
	connection := &Connection{
		conn:                 conn,
		remoteUUID:           peerUUID,
		role:                 RoleCentral,
		mtu:                  DefaultMTU,                            // Start with default MTU
		mtuExchangeCompleted: false,                                 // Central will initiate MTU exchange
		fragmenter:           att.NewFragmenter(),                   // Initialize fragmenter for long writes
		requestTracker:       att.NewRequestTracker(0),              // Initialize request tracker with default 30s timeout
		params:               defaultParams,                         // Start with default connection parameters
		paramsUpdatedAt:      time.Now(),
		discoveryCache:       gatt.NewDiscoveryCache(),              // Initialize discovery cache for client-side discovery
		cccdManager:          gatt.NewCCCDManager(),                 // Initialize CCCD subscription manager
		eventScheduler:       NewConnectionEventScheduler(RoleCentral, defaultParams.IntervalMaxMs()), // Initialize connection event scheduler
	}

	w.mu.Lock()
	// Check again inside the lock to prevent race condition
	if _, exists := w.connections[peerUUID]; exists {
		w.mu.Unlock()
		conn.Close()
		return fmt.Errorf("concurrent connection detected - another goroutine already connected to %s", peerUUID)
	}
	w.connections[peerUUID] = connection
	w.mu.Unlock()

	// Log connection established and record in health monitor
	w.connectionEventLog.LogConnectionEstablished("central", peerUUID, "", peerSocketPath)
	w.socketHealthMonitor.RecordConnection("central", peerUUID)

	// Notify callback
	w.callbackMu.RLock()
	connectCb := w.connectCallback
	w.callbackMu.RUnlock()
	if connectCb != nil {
		connectCb(peerUUID, RoleCentral)
	}

	// Log read loop started
	w.connectionEventLog.LogReadLoopStarted("central", peerUUID, "")

	// Start reading messages from this connection
	stopChan := make(chan struct{})
	w.stopMu.Lock()
	w.stopReading[peerUUID] = stopChan
	w.stopMu.Unlock()

	w.wg.Add(1)
	go w.readMessages(peerUUID, connection, stopChan)

	// Initiate MTU exchange as Central (we initiated the connection)
	// In real BLE, the Central typically initiates MTU negotiation
	go func() {
		time.Sleep(10 * time.Millisecond) // Small delay to ensure read loop is running

		// Start request tracking
		w.mu.RLock()
		conn := w.connections[peerUUID]
		w.mu.RUnlock()
		if conn == nil || conn.requestTracker == nil {
			return
		}
		tracker := conn.requestTracker.(*att.RequestTracker)
		responseC, err := tracker.StartRequest(att.OpExchangeMTURequest, 0, 0)
		if err != nil {
			logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "❌ Failed to start MTU request tracking: %v", err)
			return
		}

		// Send MTU request
		mtuReq := &att.ExchangeMTURequest{
			ClientRxMTU: uint16(MaxMTU), // Request our maximum MTU
		}
		err = w.sendATTPacket(peerUUID, mtuReq)
		if err != nil {
			logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "❌ Failed to send MTU request to %s: %v", shortHash(peerUUID), err)
			tracker.FailRequest(err)
			return
		}
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "📤 MTU Request to %s: client_mtu=%d", shortHash(peerUUID), MaxMTU)

		// Wait for response (with timeout)
		select {
		case resp := <-responseC:
			if resp.Error != nil {
				logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "❌ MTU exchange with %s failed: %v", shortHash(peerUUID), resp.Error)
				// Mark MTU exchange as completed even on failure (connection continues with default MTU)
				w.mu.RLock()
				conn := w.connections[peerUUID]
				w.mu.RUnlock()
				if conn != nil {
					conn.mtuMutex.Lock()
					conn.mtuExchangeCompleted = true
					conn.mtuMutex.Unlock()
				}
			}
			// Success is already logged in handleATTPacket, and flag is set there
		}
	}()

	return nil
}

// Disconnect closes connection to a peer (stub for old API)
// TODO Step 4: Implement graceful disconnect
func (w *Wire) Disconnect(peerUUID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	connection, exists := w.connections[peerUUID]
	if !exists {
		return fmt.Errorf("not connected to %s", peerUUID)
	}

	// In real BLE, disconnect behavior differs by role:
	// - Central can force immediate disconnect
	// - Peripheral can request disconnect, but Central controls timing
	// For simulation, both can disconnect, but we log the semantic difference
	if connection.role == RolePeripheral {
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"🔌 Peripheral requesting disconnect from %s (in real BLE, Central controls timing)",
			shortHash(peerUUID))
	} else {
		logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)),
			"🔌 Central disconnecting from %s (immediate)",
			shortHash(peerUUID))
	}

	// Cancel any pending ATT requests
	if connection.requestTracker != nil {
		tracker := connection.requestTracker.(*att.RequestTracker)
		tracker.CancelPending()
	}

	// Stop reading goroutine
	w.stopMu.Lock()
	if stopChan, exists := w.stopReading[peerUUID]; exists {
		select {
		case <-stopChan:
			// Already closed
		default:
			close(stopChan)
		}
		delete(w.stopReading, peerUUID)
	}
	w.stopMu.Unlock()

	// Close connection
	connection.conn.Close()
	delete(w.connections, peerUUID)

	// Trigger disconnect callback after cleanup
	w.callbackMu.RLock()
	callback := w.disconnectCallback
	w.callbackMu.RUnlock()

	if callback != nil {
		// Call callback asynchronously to avoid blocking and potential deadlocks
		go callback(peerUUID)
	}

	return nil
}

// IsConnected checks if we're connected to a peer
func (w *Wire) IsConnected(peerUUID string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	_, exists := w.connections[peerUUID]
	return exists
}

// GetConnectionRole returns our role in the connection with the peer
func (w *Wire) GetConnectionRole(peerUUID string) (ConnectionRole, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	connection, exists := w.connections[peerUUID]
	if !exists {
		return "", false
	}
	return connection.role, true
}

// GetConnectedPeers returns a list of all connected peer UUIDs
func (w *Wire) GetConnectedPeers() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	peers := make([]string, 0, len(w.connections))
	for uuid := range w.connections {
		peers = append(peers, uuid)
	}
	return peers
}

// WaitForMTUNegotiation waits for MTU negotiation to complete for a connection.
// This should be called after Connect() to ensure MTU exchange has finished
// before sending other ATT requests.
// Returns error if not connected or if timeout expires.
func (w *Wire) WaitForMTUNegotiation(peerUUID string, timeout time.Duration) error {
	if timeout == 0 {
		timeout = 5 * time.Second // Default timeout
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		w.mu.RLock()
		connection, exists := w.connections[peerUUID]
		w.mu.RUnlock()

		if !exists {
			return fmt.Errorf("not connected to %s", peerUUID)
		}

		// Check if MTU exchange has completed
		// For Peripheral role: Always true (set during connection initialization)
		// For Central role: Set to true when MTU exchange completes (or fails)
		connection.mtuMutex.RLock()
		completed := connection.mtuExchangeCompleted
		connection.mtuMutex.RUnlock()

		if completed {
			return nil
		}

		// Sleep briefly before checking again
		time.Sleep(5 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for MTU negotiation with %s", peerUUID)
}

// GetMTU returns the negotiated MTU for a connection
// Returns DefaultMTU if not connected or if MTU exchange hasn't completed yet
// REALISTIC BLE: MTU determines maximum data payload per ATT operation
func (w *Wire) GetMTU(peerUUID string) int {
	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if !exists {
		return DefaultMTU
	}

	connection.mtuMutex.RLock()
	mtu := connection.mtu
	connection.mtuMutex.RUnlock()

	return mtu
}
