package wire

import "time"

// ConnectionRole represents the role in a specific connection
type ConnectionRole string

const (
	RoleCentral    ConnectionRole = "central"    // We initiated connection
	RolePeripheral ConnectionRole = "peripheral" // They initiated connection
)

// BLE timing constants for realistic behavior
const (
	// Connection establishment takes time in real BLE
	MinConnectionDelay = 30 * time.Millisecond
	MaxConnectionDelay = 100 * time.Millisecond

	// Connection interval affects message delivery latency
	MinConnectionInterval = 8 * time.Millisecond  // 7.5ms rounded up
	MaxConnectionInterval = 50 * time.Millisecond // Using 50ms as typical

	// Service discovery is not instant
	MinServiceDiscoveryDelay = 100 * time.Millisecond
	MaxServiceDiscoveryDelay = 500 * time.Millisecond

	// MTU limits - real BLE has small default MTU
	DefaultMTU = 23  // BLE 4.0 default: 23 bytes total, 20 bytes data + 3 byte header
	MaxMTU     = 512 // iOS/Android can negotiate up to 512
)
