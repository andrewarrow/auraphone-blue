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
// Kept for backward compatibility with old swift code
type CharacteristicMessage = GATTMessage

// Connection represents a single bidirectional BLE connection
type Connection struct {
	conn       net.Conn
	remoteUUID string
	role       ConnectionRole // Our role in this connection
	sendMutex  sync.Mutex     // Protects writes to this connection
	mtu        int            // Current negotiated MTU (starts at DefaultMTU)
	fragmenter interface{}    // ATT fragmenter for long writes (type *att.Fragmenter, avoiding import cycle)
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
