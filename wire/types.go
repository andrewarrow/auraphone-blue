package wire

import (
	"net"
	"sync"
	"time"
)

// AdvertisingData represents BLE advertising packet data (stub for old API compatibility)
// TODO Step 4: Implement full advertising protocol
type AdvertisingData struct {
	DeviceName       string
	ServiceUUIDs     []string
	ManufacturerData []byte
	TxPowerLevel     *int
	IsConnectable    bool
}

// GATTTable represents a device's complete GATT database (stub for old API compatibility)
// TODO Step 4: Implement GATT service discovery
type GATTTable struct {
	Services []GATTService
}

// GATTService represents a BLE service (stub for old API compatibility)
type GATTService struct {
	UUID            string
	Type            string
	Characteristics []GATTCharacteristic
}

// GATTCharacteristic represents a BLE characteristic (stub for old API compatibility)
type GATTCharacteristic struct {
	UUID       string
	Properties []string
	Value      []byte
}

// CharacteristicMessage is deprecated, use GATTMessage instead
// Kept for backward compatibility with older code
type CharacteristicMessage = GATTMessage

// Connection represents a single bidirectional BLE connection
type Connection struct {
	conn              net.Conn
	remoteUUID        string
	role              ConnectionRole // Our role in this connection
	sendMutex         sync.Mutex     // Protects writes to this connection
	mtu               int            // Current negotiated MTU (starts at DefaultMTU)
	fragmenter        interface{}    // ATT fragmenter for long writes (type *att.Fragmenter, avoiding import cycle)
	requestTracker    interface{}    // ATT request tracker (type *att.RequestTracker, avoiding import cycle)
	params            interface{}    // Connection parameters (type *l2cap.ConnectionParameters, avoiding import cycle)
	paramsUpdatedAt   time.Time      // When parameters were last updated
	discoveryCache    interface{}    // GATT discovery cache (type *gatt.DiscoveryCache, avoiding import cycle)
	cccdManager       interface{}    // CCCD subscription manager (type *gatt.CCCDManager, avoiding import cycle)
	eventScheduler    *ConnectionEventScheduler // Simulates discrete BLE connection events
	eventSchedulerMux sync.Mutex     // Protects event scheduler access
}

// GATTMessage represents a GATT operation over the wire
type GATTMessage struct {
	Type               string `json:"type"`                         // "gatt_request", "gatt_response", "gatt_notification"
	RequestID          string `json:"request_id,omitempty"`         // For request/response matching
	Operation          string `json:"operation,omitempty"`          // "read", "write", "subscribe", "unsubscribe"
	ServiceUUID        string `json:"service_uuid"`
	CharacteristicUUID string `json:"characteristic_uuid"`
	CharUUID           string `json:"char_uuid,omitempty"`          // Alias for CharacteristicUUID (old API compat)
	Data               []byte `json:"data,omitempty"`
	Status             string `json:"status,omitempty"`             // "success", "error"
	SenderUUID         string `json:"sender_uuid,omitempty"`        // Who sent this message
}

// SimulatorStub is a stub for the old Simulator type
type SimulatorStub struct {
	ServiceDiscoveryDelay time.Duration
}

// ConnectionEventScheduler simulates discrete BLE connection events
// In real BLE:
// - Central schedules connection events at regular intervals
// - Both devices exchange data during event windows (~150-200μs)
// - Peripheral can ONLY send during these scheduled events
// - Central controls the timing
type ConnectionEventScheduler struct {
	role            ConnectionRole
	connectionStart time.Time
	intervalMs      float64 // Connection interval in milliseconds
	nextEventTime   time.Time
	mu              sync.Mutex
}

// NewConnectionEventScheduler creates a new connection event scheduler
func NewConnectionEventScheduler(role ConnectionRole, intervalMs float64) *ConnectionEventScheduler {
	now := time.Now()
	return &ConnectionEventScheduler{
		role:            role,
		connectionStart: now,
		intervalMs:      intervalMs,
		nextEventTime:   now, // First event is immediate
	}
}

// WaitForNextEvent blocks until the next connection event (Peripheral only)
// Central can send immediately as it controls the timing
// Returns immediately if called by Central
func (ces *ConnectionEventScheduler) WaitForNextEvent() {
	ces.mu.Lock()
	defer ces.mu.Unlock()

	// Central controls timing and can send anytime
	if ces.role == RoleCentral {
		return
	}

	// Peripheral must wait for next scheduled connection event
	now := time.Now()
	if now.Before(ces.nextEventTime) {
		waitTime := ces.nextEventTime.Sub(now)
		ces.mu.Unlock()
		time.Sleep(waitTime)
		ces.mu.Lock()
	}

	// Advance to next event
	ces.nextEventTime = ces.nextEventTime.Add(time.Duration(ces.intervalMs * float64(time.Millisecond)))
}

// UpdateInterval updates the connection interval (typically after parameter negotiation)
func (ces *ConnectionEventScheduler) UpdateInterval(intervalMs float64) {
	ces.mu.Lock()
	defer ces.mu.Unlock()
	ces.intervalMs = intervalMs
}
