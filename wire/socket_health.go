package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/user/auraphone-blue/phone"
)

// SocketHealthMonitor tracks socket statistics in-memory and persists snapshots periodically
type SocketHealthMonitor struct {
	deviceUUID string
	mu         sync.RWMutex

	// In-memory statistics
	peripheralSocket *SocketStats
	centralSocket    *SocketStats

	// Persistence
	lastSnapshot time.Time
	snapshotFile string

	// Control
	stopChan chan struct{}
	wg       sync.WaitGroup
}

// SocketStats tracks statistics for a single socket (peripheral or central)
type SocketStats struct {
	Path            string               `json:"path"`
	CreatedAt       int64                `json:"created_at"`        // Nanoseconds since epoch
	UptimeSeconds   int                  `json:"uptime_seconds"`
	Connections     []*ConnectionStats   `json:"connections"`
	TotalErrors     int                  `json:"total_errors"`
	Status          string               `json:"status"` // "healthy", "error", "closed"
}

// ConnectionStats tracks statistics for a single connection on a socket
type ConnectionStats struct {
	RemoteUUID       string `json:"remote_uuid"`
	ConnectedAt      int64  `json:"connected_at"`       // Nanoseconds since epoch
	MessagesReceived int    `json:"messages_received"`
	MessagesSent     int    `json:"messages_sent"`
	LastActivity     int64  `json:"last_activity"`      // Nanoseconds since epoch
	Errors           int    `json:"errors"`
	LastError        string `json:"last_error,omitempty"`
}

// SocketHealthSnapshot is the JSON structure written to disk every 5 seconds
type SocketHealthSnapshot struct {
	Timestamp        int64        `json:"timestamp"` // Nanoseconds since epoch
	PeripheralSocket *SocketStats `json:"peripheral_socket,omitempty"`
	CentralSocket    *SocketStats `json:"central_socket,omitempty"`
}

// NewSocketHealthMonitor creates a new socket health monitor
func NewSocketHealthMonitor(deviceUUID string) *SocketHealthMonitor {
	dataDir := phone.GetDeviceCacheDir(deviceUUID)
	snapshotFile := filepath.Join(dataDir, "socket_health.json")

	return &SocketHealthMonitor{
		deviceUUID:   deviceUUID,
		snapshotFile: snapshotFile,
		stopChan:     make(chan struct{}),
	}
}

// InitializeSocket registers a socket for monitoring
func (m *SocketHealthMonitor) InitializeSocket(socketType, path string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	stats := &SocketStats{
		Path:        path,
		CreatedAt:   time.Now().UnixNano(),
		Connections: []*ConnectionStats{},
		Status:      "healthy",
	}

	if socketType == "peripheral" {
		m.peripheralSocket = stats
	} else if socketType == "central" {
		m.centralSocket = stats
	}
}

// RecordConnection registers a new connection on a socket
func (m *SocketHealthMonitor) RecordConnection(socketType, remoteUUID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	connStats := &ConnectionStats{
		RemoteUUID:   remoteUUID,
		ConnectedAt:  time.Now().UnixNano(),
		LastActivity: time.Now().UnixNano(),
	}

	if socketType == "peripheral" && m.peripheralSocket != nil {
		m.peripheralSocket.Connections = append(m.peripheralSocket.Connections, connStats)
	} else if socketType == "central" && m.centralSocket != nil {
		m.centralSocket.Connections = append(m.centralSocket.Connections, connStats)
	}
}

// RecordMessageSent increments the sent message counter for a connection
func (m *SocketHealthMonitor) RecordMessageSent(socketType, remoteUUID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var socket *SocketStats
	if socketType == "peripheral" {
		socket = m.peripheralSocket
	} else if socketType == "central" {
		socket = m.centralSocket
	}

	if socket == nil {
		return
	}

	for _, conn := range socket.Connections {
		if conn.RemoteUUID == remoteUUID {
			conn.MessagesSent++
			conn.LastActivity = time.Now().UnixNano()
			break
		}
	}
}

// RecordMessageReceived increments the received message counter for a connection
func (m *SocketHealthMonitor) RecordMessageReceived(socketType, remoteUUID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var socket *SocketStats
	if socketType == "peripheral" {
		socket = m.peripheralSocket
	} else if socketType == "central" {
		socket = m.centralSocket
	}

	if socket == nil {
		return
	}

	for _, conn := range socket.Connections {
		if conn.RemoteUUID == remoteUUID {
			conn.MessagesReceived++
			conn.LastActivity = time.Now().UnixNano()
			break
		}
	}
}

// RecordError logs an error for a connection
func (m *SocketHealthMonitor) RecordError(socketType, remoteUUID, errorMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var socket *SocketStats
	if socketType == "peripheral" {
		socket = m.peripheralSocket
	} else if socketType == "central" {
		socket = m.centralSocket
	}

	if socket == nil {
		return
	}

	socket.TotalErrors++
	socket.Status = "error"

	for _, conn := range socket.Connections {
		if conn.RemoteUUID == remoteUUID {
			conn.Errors++
			conn.LastError = errorMsg
			break
		}
	}
}

// RemoveConnection removes a connection from tracking (when disconnected)
func (m *SocketHealthMonitor) RemoveConnection(socketType, remoteUUID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var socket *SocketStats
	if socketType == "peripheral" {
		socket = m.peripheralSocket
	} else if socketType == "central" {
		socket = m.centralSocket
	}

	if socket == nil {
		return
	}

	// Filter out the disconnected connection
	newConns := []*ConnectionStats{}
	for _, conn := range socket.Connections {
		if conn.RemoteUUID != remoteUUID {
			newConns = append(newConns, conn)
		}
	}
	socket.Connections = newConns
}

// MarkSocketClosed marks a socket as closed
func (m *SocketHealthMonitor) MarkSocketClosed(socketType string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if socketType == "peripheral" && m.peripheralSocket != nil {
		m.peripheralSocket.Status = "closed"
	} else if socketType == "central" && m.centralSocket != nil {
		m.centralSocket.Status = "closed"
	}
}

// StartPeriodicSnapshots starts the background goroutine that writes snapshots every 5 seconds
func (m *SocketHealthMonitor) StartPeriodicSnapshots() {
	m.wg.Add(1)
	go m.snapshotLoop()
}

// snapshotLoop runs in background and writes snapshots every 5 seconds
func (m *SocketHealthMonitor) snapshotLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopChan:
			// Write final snapshot before exiting
			m.writeSnapshot()
			return
		case <-ticker.C:
			m.writeSnapshot()
		}
	}
}

// writeSnapshot writes the current state to disk
func (m *SocketHealthMonitor) writeSnapshot() {
	m.mu.RLock()

	// Update uptime for sockets
	now := time.Now()
	if m.peripheralSocket != nil && m.peripheralSocket.CreatedAt > 0 {
		m.peripheralSocket.UptimeSeconds = int(now.Sub(time.Unix(0, m.peripheralSocket.CreatedAt)).Seconds())
	}
	if m.centralSocket != nil && m.centralSocket.CreatedAt > 0 {
		m.centralSocket.UptimeSeconds = int(now.Sub(time.Unix(0, m.centralSocket.CreatedAt)).Seconds())
	}

	snapshot := &SocketHealthSnapshot{
		Timestamp:        now.UnixNano(),
		PeripheralSocket: m.peripheralSocket,
		CentralSocket:    m.centralSocket,
	}

	m.mu.RUnlock()

	// Marshal to JSON
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return // Silently fail
	}

	// Write atomically (temp file + rename)
	tempPath := m.snapshotFile + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return
	}

	os.Rename(tempPath, m.snapshotFile)
}

// Stop stops the snapshot loop and writes final snapshot
func (m *SocketHealthMonitor) Stop() {
	close(m.stopChan)
	m.wg.Wait()
}
