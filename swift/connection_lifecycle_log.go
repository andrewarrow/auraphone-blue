package swift

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/user/auraphone-blue/phone"
)

// CBConnectionEvent represents a CoreBluetooth-level connection lifecycle event
type CBConnectionEvent struct {
	Timestamp         int64             `json:"timestamp"` // nanoseconds since epoch
	Event             string            `json:"event"`     // connect_called, connect_blocked, connect_started, connect_completed, etc.
	PeripheralUUID    string            `json:"peripheral_uuid"`
	AlreadyConnecting bool              `json:"already_connecting,omitempty"`
	AlreadyConnected  bool              `json:"already_connected,omitempty"`
	ConnectionsCount  int               `json:"connections_count,omitempty"` // number of items in connectingDevices
	PendingCount      int               `json:"pending_count,omitempty"`     // number of items in pendingPeripherals
	Details           map[string]string `json:"details,omitempty"`
}

// CBConnectionLifecycleLogger logs CoreBluetooth connection lifecycle for debugging
type CBConnectionLifecycleLogger struct {
	localUUID string
	logPath   string
	mutex     sync.Mutex
	enabled   bool
}

// NewCBConnectionLifecycleLogger creates a logger for CB connection events
func NewCBConnectionLifecycleLogger(localUUID string, enabled bool) *CBConnectionLifecycleLogger {
	if !enabled {
		return &CBConnectionLifecycleLogger{enabled: false}
	}

	deviceDir := phone.GetDeviceCacheDir(localUUID)
	logPath := filepath.Join(deviceDir, "cb_connection_lifecycle.jsonl")

	return &CBConnectionLifecycleLogger{
		localUUID: localUUID,
		logPath:   logPath,
		enabled:   true,
	}
}

// Log writes a CB connection event to the JSONL file
func (log *CBConnectionLifecycleLogger) Log(event CBConnectionEvent) {
	if !log.enabled {
		return
	}

	// Set timestamp if not already set
	if event.Timestamp == 0 {
		event.Timestamp = time.Now().UnixNano()
	}

	log.mutex.Lock()
	defer log.mutex.Unlock()

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(log.logPath), 0755); err != nil {
		return
	}

	// Open file in append mode
	f, err := os.OpenFile(log.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	// Write JSON line
	encoder := json.NewEncoder(f)
	if err := encoder.Encode(event); err != nil {
		return
	}
}

// LogConnectCalled logs when Connect() is called
func (log *CBConnectionLifecycleLogger) LogConnectCalled(uuid string, alreadyConnecting, alreadyConnected bool, connectingCount, pendingCount int) {
	log.Log(CBConnectionEvent{
		Event:             "connect_called",
		PeripheralUUID:    uuid,
		AlreadyConnecting: alreadyConnecting,
		AlreadyConnected:  alreadyConnected,
		ConnectionsCount:  connectingCount,
		PendingCount:      pendingCount,
	})
}

// LogConnectBlocked logs when Connect() is blocked due to existing connection
func (log *CBConnectionLifecycleLogger) LogConnectBlocked(uuid string, reason string) {
	log.Log(CBConnectionEvent{
		Event:          "connect_blocked",
		PeripheralUUID: uuid,
		Details:        map[string]string{"reason": reason},
	})
}

// LogConnectStarted logs when Connect() actually starts a new connection attempt
func (log *CBConnectionLifecycleLogger) LogConnectStarted(uuid string) {
	log.Log(CBConnectionEvent{
		Event:          "connect_started",
		PeripheralUUID: uuid,
	})
}

// LogConnectCompleted logs when a connection succeeds
func (log *CBConnectionLifecycleLogger) LogConnectCompleted(uuid string, success bool) {
	eventType := "connect_completed"
	if !success {
		eventType = "connect_failed"
	}
	log.Log(CBConnectionEvent{
		Event:          eventType,
		PeripheralUUID: uuid,
	})
}

// LogConnectingFlagCleared logs when the connecting flag is removed
func (log *CBConnectionLifecycleLogger) LogConnectingFlagCleared(uuid string) {
	log.Log(CBConnectionEvent{
		Event:          "connecting_flag_cleared",
		PeripheralUUID: uuid,
	})
}
