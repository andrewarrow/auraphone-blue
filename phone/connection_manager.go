package phone

import (
	"fmt"
	"sync"

	"github.com/user/auraphone-blue/logger"
)

// ConnectionManager tracks dual-role connections and provides unified send interface
// Manages both Central mode (we connect to others) and Peripheral mode (others connect to us)
type ConnectionManager struct {
	hardwareUUID string

	// Connection tracking
	// Key: remoteUUID (hardware UUID of remote device)
	// Value: connection object (platform-specific: CBPeripheral for iOS, BluetoothGatt for Android)
	centralConnections    map[string]interface{} // Devices we connected to (we are Central)
	peripheralConnections map[string]bool        // Devices that connected to us (we are Peripheral)

	// Role-specific senders
	// These functions are set by the platform-specific code (iOS/Android)
	sendViaCentral    func(remoteUUID, charUUID string, data []byte) error
	sendViaPeripheral func(remoteUUID, charUUID string, data []byte) error

	mu sync.RWMutex
}

// NewConnectionManager creates a new connection manager
func NewConnectionManager(hardwareUUID string) *ConnectionManager {
	return &ConnectionManager{
		hardwareUUID:          hardwareUUID,
		centralConnections:    make(map[string]interface{}),
		peripheralConnections: make(map[string]bool),
	}
}

// SetSendFunctions configures the platform-specific send functions
func (cm *ConnectionManager) SetSendFunctions(
	sendViaCentral func(remoteUUID, charUUID string, data []byte) error,
	sendViaPeripheral func(remoteUUID, charUUID string, data []byte) error,
) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.sendViaCentral = sendViaCentral
	cm.sendViaPeripheral = sendViaPeripheral
}

// RegisterCentralConnection registers a connection where we acted as Central
func (cm *ConnectionManager) RegisterCentralConnection(remoteUUID string, conn interface{}) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.centralConnections[remoteUUID] = conn
}

// UnregisterCentralConnection removes a Central mode connection
func (cm *ConnectionManager) UnregisterCentralConnection(remoteUUID string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	delete(cm.centralConnections, remoteUUID)
}

// RegisterPeripheralConnection registers a connection where we acted as Peripheral
func (cm *ConnectionManager) RegisterPeripheralConnection(remoteUUID string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.peripheralConnections[remoteUUID] = true
}

// UnregisterPeripheralConnection removes a Peripheral mode connection
func (cm *ConnectionManager) UnregisterPeripheralConnection(remoteUUID string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	delete(cm.peripheralConnections, remoteUUID)
}

// SendToDevice sends data to a device via the appropriate role (Central or Peripheral)
// Automatically determines which path to use based on connection tracking
func (cm *ConnectionManager) SendToDevice(remoteUUID, charUUID string, data []byte) error {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// Check if we're connected as Central
	if _, exists := cm.centralConnections[remoteUUID]; exists {
		if cm.sendViaCentral == nil {
			return fmt.Errorf("sendViaCentral function not set")
		}
		logger.Debug(fmt.Sprintf("%s ConnMgr", cm.hardwareUUID[:8]), "📡 Sending to %s via CENTRAL mode (char=%s, %d bytes)", remoteUUID[:8], charUUID[len(charUUID)-4:], len(data))
		return cm.sendViaCentral(remoteUUID, charUUID, data)
	}

	// Check if we're connected as Peripheral
	if cm.peripheralConnections[remoteUUID] {
		if cm.sendViaPeripheral == nil {
			return fmt.Errorf("sendViaPeripheral function not set")
		}
		logger.Debug(fmt.Sprintf("%s ConnMgr", cm.hardwareUUID[:8]), "📡 Sending to %s via PERIPHERAL mode (char=%s, %d bytes)", remoteUUID[:8], charUUID[len(charUUID)-4:], len(data))
		return cm.sendViaPeripheral(remoteUUID, charUUID, data)
	}

	logger.Warn(fmt.Sprintf("%s ConnMgr", cm.hardwareUUID[:8]), "⚠️  Not connected to %s! Central=%v, Peripheral=%v",
		remoteUUID[:8], len(cm.centralConnections), len(cm.peripheralConnections))
	return fmt.Errorf("not connected to %s", remoteUUID[:8])
}

// GetAllConnectedUUIDs returns all currently connected device UUIDs (both Central and Peripheral)
func (cm *ConnectionManager) GetAllConnectedUUIDs() []string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	uuids := []string{}

	// Add Central connections
	for uuid := range cm.centralConnections {
		uuids = append(uuids, uuid)
	}

	// Add Peripheral connections (avoiding duplicates)
	for uuid := range cm.peripheralConnections {
		// Check if already in list (shouldn't happen, but be defensive)
		found := false
		for _, existingUUID := range uuids {
			if existingUUID == uuid {
				found = true
				break
			}
		}
		if !found {
			uuids = append(uuids, uuid)
		}
	}

	return uuids
}

// IsConnected checks if we're connected to a device (in either role)
func (cm *ConnectionManager) IsConnected(remoteUUID string) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	_, inCentral := cm.centralConnections[remoteUUID]
	inPeripheral := cm.peripheralConnections[remoteUUID]

	return inCentral || inPeripheral
}

// GetCentralConnection retrieves the connection object for a Central mode connection
// Returns nil if not connected as Central
func (cm *ConnectionManager) GetCentralConnection(remoteUUID string) interface{} {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	return cm.centralConnections[remoteUUID]
}

// IsConnectedAsCentral checks if we're connected to this device as Central
func (cm *ConnectionManager) IsConnectedAsCentral(remoteUUID string) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	_, exists := cm.centralConnections[remoteUUID]
	return exists
}

// IsConnectedAsPeripheral checks if we're connected to this device as Peripheral
func (cm *ConnectionManager) IsConnectedAsPeripheral(remoteUUID string) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	return cm.peripheralConnections[remoteUUID]
}

// GetConnectionCount returns the total number of connections (Central + Peripheral)
func (cm *ConnectionManager) GetConnectionCount() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// Count unique connections (avoid double-counting if somehow connected both ways)
	uniqueUUIDs := make(map[string]bool)

	for uuid := range cm.centralConnections {
		uniqueUUIDs[uuid] = true
	}

	for uuid := range cm.peripheralConnections {
		uniqueUUIDs[uuid] = true
	}

	return len(uniqueUUIDs)
}
