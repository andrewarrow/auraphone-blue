package wire

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
)

// ConnectionEvent represents a lifecycle event for socket/connection debugging
type ConnectionEvent struct {
	Timestamp    int64             `json:"timestamp"`     // nanoseconds since epoch
	Event        string            `json:"event"`         // socket_created, connection_accepted, socket_error, socket_closed, etc.
	SocketType   string            `json:"socket_type"`   // "peripheral" or "central"
	RemoteUUID   string            `json:"remote_uuid,omitempty"`
	RemoteDevice string            `json:"remote_device,omitempty"` // device ID if known
	Path         string            `json:"path,omitempty"`
	Error        string            `json:"error,omitempty"`
	Context      string            `json:"context,omitempty"` // e.g., "read loop", "write", "accept"
	Details      map[string]string `json:"details,omitempty"` // additional context
}

// ConnectionEventLogger manages append-only logging of connection events
type ConnectionEventLogger struct {
	localUUID string
	logPath   string
	mutex     sync.Mutex
	enabled   bool
}

// NewConnectionEventLogger creates a new event logger for a device
func NewConnectionEventLogger(localUUID string, enabled bool) *ConnectionEventLogger {
	if !enabled {
		return &ConnectionEventLogger{enabled: false}
	}

	deviceDir := phone.GetDeviceDir(localUUID)
	logPath := filepath.Join(deviceDir, "connection_events.jsonl")

	return &ConnectionEventLogger{
		localUUID: localUUID,
		logPath:   logPath,
		enabled:   true,
	}
}

// Log writes a connection event to the JSONL file
func (cel *ConnectionEventLogger) Log(event ConnectionEvent) {
	if !cel.enabled {
		return
	}

	// Set timestamp if not already set
	if event.Timestamp == 0 {
		event.Timestamp = time.Now().UnixNano()
	}

	cel.mutex.Lock()
	defer cel.mutex.Unlock()

	// Open file in append mode
	f, err := os.OpenFile(cel.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		logger.Warn(fmt.Sprintf("%s connection_events", cel.localUUID[:8]),
			"Failed to open connection event log: %v", err)
		return
	}
	defer f.Close()

	// Marshal and write JSON line
	data, err := json.Marshal(event)
	if err != nil {
		logger.Warn(fmt.Sprintf("%s connection_events", cel.localUUID[:8]),
			"Failed to marshal connection event: %v", err)
		return
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		logger.Warn(fmt.Sprintf("%s connection_events", cel.localUUID[:8]),
			"Failed to write connection event: %v", err)
	}
}

// Helper methods for common events

func (cel *ConnectionEventLogger) LogSocketCreated(socketType, path string) {
	cel.Log(ConnectionEvent{
		Event:      "socket_created",
		SocketType: socketType,
		Path:       path,
	})
}

func (cel *ConnectionEventLogger) LogConnectionAccepted(socketType, remoteUUID, remoteDevice string) {
	cel.Log(ConnectionEvent{
		Event:        "connection_accepted",
		SocketType:   socketType,
		RemoteUUID:   remoteUUID,
		RemoteDevice: remoteDevice,
	})
}

func (cel *ConnectionEventLogger) LogConnectionEstablished(socketType, remoteUUID, remoteDevice, path string) {
	cel.Log(ConnectionEvent{
		Event:        "connection_established",
		SocketType:   socketType,
		RemoteUUID:   remoteUUID,
		RemoteDevice: remoteDevice,
		Path:         path,
	})
}

func (cel *ConnectionEventLogger) LogSocketError(socketType, remoteUUID, remoteDevice, errorMsg, context string) {
	cel.Log(ConnectionEvent{
		Event:        "socket_error",
		SocketType:   socketType,
		RemoteUUID:   remoteUUID,
		RemoteDevice: remoteDevice,
		Error:        errorMsg,
		Context:      context,
	})
}

func (cel *ConnectionEventLogger) LogSocketClosed(socketType, remoteUUID, remoteDevice, reason string) {
	cel.Log(ConnectionEvent{
		Event:        "socket_closed",
		SocketType:   socketType,
		RemoteUUID:   remoteUUID,
		RemoteDevice: remoteDevice,
		Context:      reason,
	})
}

func (cel *ConnectionEventLogger) LogConnectionTimeout(socketType, remoteUUID, remoteDevice string, durationMs int64) {
	cel.Log(ConnectionEvent{
		Event:        "connection_timeout",
		SocketType:   socketType,
		RemoteUUID:   remoteUUID,
		RemoteDevice: remoteDevice,
		Details:      map[string]string{"duration_ms": fmt.Sprintf("%d", durationMs)},
	})
}

func (cel *ConnectionEventLogger) LogMTUNegotiated(socketType, remoteUUID, remoteDevice string, mtu int) {
	cel.Log(ConnectionEvent{
		Event:        "mtu_negotiated",
		SocketType:   socketType,
		RemoteUUID:   remoteUUID,
		RemoteDevice: remoteDevice,
		Details:      map[string]string{"mtu": fmt.Sprintf("%d", mtu)},
	})
}

func (cel *ConnectionEventLogger) LogDualConnectionComplete(remoteUUID, remoteDevice string) {
	cel.Log(ConnectionEvent{
		Event:        "dual_connection_complete",
		SocketType:   "both",
		RemoteUUID:   remoteUUID,
		RemoteDevice: remoteDevice,
	})
}

func (cel *ConnectionEventLogger) LogReadLoopStarted(socketType, remoteUUID, remoteDevice string) {
	cel.Log(ConnectionEvent{
		Event:        "read_loop_started",
		SocketType:   socketType,
		RemoteUUID:   remoteUUID,
		RemoteDevice: remoteDevice,
	})
}

func (cel *ConnectionEventLogger) LogReadLoopEnded(socketType, remoteUUID, remoteDevice, reason string) {
	cel.Log(ConnectionEvent{
		Event:        "read_loop_ended",
		SocketType:   socketType,
		RemoteUUID:   remoteUUID,
		RemoteDevice: remoteDevice,
		Context:      reason,
	})
}
