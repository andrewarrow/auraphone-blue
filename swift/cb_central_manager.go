package swift

import (
	"sync"
	"time"

	"github.com/user/auraphone-blue/wire"
)

type CBCentralManagerDelegate interface {
	DidUpdateCentralState(central CBCentralManager)
	DidDiscoverPeripheral(central CBCentralManager, peripheral CBPeripheral, advertisementData map[string]interface{}, rssi float64)
	DidConnectPeripheral(central CBCentralManager, peripheral CBPeripheral)
	DidFailToConnectPeripheral(central CBCentralManager, peripheral CBPeripheral, err error)
	DidDisconnectPeripheral(central CBCentralManager, peripheral CBPeripheral, err error)
}

type CBCentralManager struct {
	Delegate            CBCentralManagerDelegate
	State               CBManagerState // Use enum instead of string
	uuid                string
	wire                *wire.Wire
	mu                  sync.RWMutex
	stopChan            chan struct{}
	pendingPeripherals  map[string]*CBPeripheral // UUID -> peripheral (for auto-reconnect and message routing)
	autoReconnectActive bool                     // Whether auto-reconnect is enabled
	discoveredDevices   map[string]bool          // Track devices already reported to delegate (per scan session)
	connectingDevices   map[string]bool          // Track devices with connection in progress (prevents duplicate Connect calls)
	lifecycleLog        *CBConnectionLifecycleLogger // Debug logging for connection lifecycle
	connectedPeripherals map[string]bool         // Track peripherals we've connected to (for RetrievePeripheralsByIdentifiers)
}

func NewCBCentralManager(delegate CBCentralManagerDelegate, uuid string, sharedWire *wire.Wire) *CBCentralManager {
	cm := &CBCentralManager{
		Delegate:             delegate,
		State:                CBManagerStateUnknown, // REALISTIC: Start in unknown state, not poweredOn
		uuid:                 uuid,
		wire:                 sharedWire,
		pendingPeripherals:   make(map[string]*CBPeripheral),
		autoReconnectActive:  true,                  // iOS auto-reconnect is always active
		discoveredDevices:    make(map[string]bool), // Initialize discovery tracking
		connectingDevices:    make(map[string]bool), // Initialize connection tracking
		lifecycleLog:         NewCBConnectionLifecycleLogger(uuid, true), // Enable connection lifecycle logging
		connectedPeripherals: make(map[string]bool), // Track connection history
	}

	// REALISTIC iOS BEHAVIOR: Bluetooth initialization takes time (50-200ms)
	// Notify delegate of state transition after initialization delay
	go func() {
		// Simulate BLE stack initialization delay
		time.Sleep(100 * time.Millisecond)

		cm.mu.Lock()
		cm.State = CBManagerStatePoweredOn
		cm.mu.Unlock()

		// CRITICAL: Always call DidUpdateCentralState when state changes
		// Real iOS apps rely on this callback to know when they can start scanning
		if delegate != nil {
			delegate.DidUpdateCentralState(*cm)
		}
	}()

	// Set up disconnect callback
	sharedWire.SetDisconnectCallback(func(deviceUUID string) {
		// Connection was randomly dropped
		cm.mu.RLock()
		var peripheralCopy CBPeripheral
		if peripheral, exists := cm.pendingPeripherals[deviceUUID]; exists {
			// REALISTIC iOS BEHAVIOR: Set peripheral state to disconnected
			peripheral.mu.Lock()
			peripheral.State = CBPeripheralStateDisconnected
			peripheral.mu.Unlock()

			peripheralCopy = *peripheral
		} else {
			peripheralCopy = CBPeripheral{
				UUID:  deviceUUID,
				State: CBPeripheralStateDisconnected,
			}
		}
		autoReconnect := cm.autoReconnectActive
		cm.mu.RUnlock()

		if delegate != nil {
			delegate.DidDisconnectPeripheral(*cm, peripheralCopy, nil) // nil error = clean disconnect (not an error, just interference/distance)
		}

		// iOS auto-reconnect: if this peripheral was in pendingPeripherals, try to reconnect
		if autoReconnect {
			cm.mu.RLock()
			peripheral, exists := cm.pendingPeripherals[deviceUUID]
			cm.mu.RUnlock()

			if exists {
				// Automatically retry connection in background (matches real iOS behavior)
				go cm.attemptReconnect(peripheral)
			}
		}
	})

	return cm
}

func (c *CBCentralManager) ScanForPeripherals(withServices []string, options map[string]interface{}) {
	// REALISTIC iOS BEHAVIOR: Reset discovered devices at the start of a new scan session
	// In real iOS, starting a new scan session clears the "already reported" state
	c.mu.Lock()
	c.discoveredDevices = make(map[string]bool)
	c.mu.Unlock()

	stopChan := c.wire.StartDiscovery(func(deviceUUID string) {
		// REALISTIC iOS BEHAVIOR: Check if we've already reported this device to delegate
		// Real iOS CoreBluetooth only calls didDiscoverPeripheral once per device per scan session
		c.mu.Lock()
		if c.discoveredDevices[deviceUUID] {
			c.mu.Unlock()
			return // Already reported this peripheral
		}
		c.discoveredDevices[deviceUUID] = true
		c.mu.Unlock()

		// Read advertising data from the discovered device
		advData, err := c.wire.ReadAdvertisingData(deviceUUID)
		if err != nil {
			// Fall back to empty advertising data if not available
			advData = &wire.AdvertisingData{
				IsConnectable: true,
			}
		}

		// Service UUID filtering (matches real iOS CoreBluetooth behavior)
		// If withServices is specified, only report devices advertising those services
		if len(withServices) > 0 {
			found := false
			for _, requestedService := range withServices {
				for _, advertisedService := range advData.ServiceUUIDs {
					if requestedService == advertisedService {
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			if !found {
				// Device doesn't advertise the requested services, skip it
				return
			}
		}

		// Build advertisement data map matching iOS CoreBluetooth format
		advertisementData := make(map[string]interface{})

		if advData.DeviceName != "" {
			advertisementData["kCBAdvDataLocalName"] = advData.DeviceName
		}

		if len(advData.ServiceUUIDs) > 0 {
			advertisementData["kCBAdvDataServiceUUIDs"] = advData.ServiceUUIDs
		}

		if advData.ManufacturerData != nil && len(advData.ManufacturerData) > 0 {
			advertisementData["kCBAdvDataManufacturerData"] = advData.ManufacturerData
		}

		if advData.TxPowerLevel != nil {
			advertisementData["kCBAdvDataTxPowerLevel"] = *advData.TxPowerLevel
		}

		advertisementData["kCBAdvDataIsConnectable"] = advData.IsConnectable

		// Use device name from advertising data if available, otherwise use placeholder
		deviceName := advData.DeviceName
		if deviceName == "" {
			deviceName = "Unknown Device"
		}

		// Get realistic RSSI from wire layer
		rssi := float64(c.wire.GetRSSI(deviceUUID))

		// Make a copy of the manager struct to avoid races when dereferencing
		c.mu.RLock()
		managerCopy := CBCentralManager{
			Delegate: c.Delegate,
			State:    c.State,
			uuid:     c.uuid,
			wire:     c.wire,
		}
		c.mu.RUnlock()

		c.Delegate.DidDiscoverPeripheral(managerCopy, CBPeripheral{
			Name: deviceName,
			UUID: deviceUUID,
		}, advertisementData, rssi)
	})

	c.mu.Lock()
	c.stopChan = stopChan
	c.mu.Unlock()
}

func (c *CBCentralManager) StopScan() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stopChan != nil {
		close(c.stopChan)
		c.stopChan = nil
	}

	// REALISTIC iOS BEHAVIOR: Clear discovered devices when scan stops
	// This ensures that if scanning is restarted, devices can be discovered again
	c.discoveredDevices = make(map[string]bool)
}

// RetrievePeripheralsByIdentifiers retrieves known peripherals by their identifiers
// Matches: centralManager.retrievePeripherals(withIdentifiers:)
// REALISTIC iOS BEHAVIOR: Returns peripherals the system knows about:
// - Peripherals currently connected to this app
// - Peripherals previously connected to this app (iOS remembers them)
// - Does NOT return peripherals you've never connected to, even if they're nearby
func (c *CBCentralManager) RetrievePeripheralsByIdentifiers(identifiers []string) []*CBPeripheral {
	peripherals := make([]*CBPeripheral, 0, len(identifiers))

	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, uuid := range identifiers {
		// REALISTIC: Only return peripherals we've connected to before
		if !c.connectedPeripherals[uuid] {
			continue
		}

		// Check if we have a pending peripheral (currently connected or remembered for auto-reconnect)
		if existingPeripheral, exists := c.pendingPeripherals[uuid]; exists {
			peripherals = append(peripherals, existingPeripheral)
			continue
		}

		// Create new peripheral object for previously connected device
		if c.wire.DeviceExists(uuid) {
			// Read advertising data to get device name if available
			advData, err := c.wire.ReadAdvertisingData(uuid)
			deviceName := "Unknown Device"
			if err == nil && advData.DeviceName != "" {
				deviceName = advData.DeviceName
			}

			// Determine current state
			state := CBPeripheralStateDisconnected
			if c.wire.IsConnected(uuid) {
				state = CBPeripheralStateConnected
			}

			peripheral := &CBPeripheral{
				UUID:       uuid,
				Name:       deviceName,
				State:      state,
				wire:       c.wire,
				remoteUUID: uuid,
				notifyingCharacteristics: make(map[string]bool),
			}
			peripherals = append(peripherals, peripheral)
		}
	}

	return peripherals
}

// ShouldInitiateConnection determines if this iOS device should initiate connection to target
// Simple Role Policy: Use hardware UUID comparison regardless of platform
// Device with LARGER UUID acts as Central (initiates connection)
func (c *CBCentralManager) ShouldInitiateConnection(targetUUID string) bool {
	// Use hardware UUID comparison for all devices
	// Device with LARGER UUID initiates the connection (deterministic collision avoidance)
	return c.uuid > targetUUID
}

func (c *CBCentralManager) Connect(peripheral *CBPeripheral, options map[string]interface{}) {
	// Role Policy: Apps should call ShouldInitiateConnection() before calling Connect()
	// to avoid simultaneous connection attempts with dual-role devices.

	// REALISTIC iOS BEHAVIOR: Check if connection in progress or already connected
	// Real iOS ignores Connect() calls when a connection attempt is already ongoing or connected
	c.mu.Lock()
	alreadyConnecting := c.connectingDevices[peripheral.UUID]
	alreadyConnected := c.wire.IsConnected(peripheral.UUID)
	connectingCount := len(c.connectingDevices)
	pendingCount := len(c.pendingPeripherals)

	// Log the Connect() call with current state
	c.lifecycleLog.LogConnectCalled(peripheral.UUID, alreadyConnecting, alreadyConnected, connectingCount, pendingCount)

	if alreadyConnecting || alreadyConnected {
		c.mu.Unlock()
		// Log why we're blocking this connection attempt
		reason := "already_connecting"
		if alreadyConnected {
			reason = "already_connected"
		}
		c.lifecycleLog.LogConnectBlocked(peripheral.UUID, reason)
		return // Already connecting or connected, do nothing (realistic iOS behavior)
	}
	// Mark as connecting BEFORE spawning goroutine to prevent race
	c.connectingDevices[peripheral.UUID] = true
	c.lifecycleLog.LogConnectStarted(peripheral.UUID)
	c.mu.Unlock()

	// Set up the peripheral's wire connection
	peripheral.wire = c.wire
	peripheral.remoteUUID = peripheral.UUID

	// REALISTIC iOS BEHAVIOR: Set peripheral state to connecting
	peripheral.mu.Lock()
	peripheral.State = CBPeripheralStateConnecting
	peripheral.mu.Unlock()

	// iOS remembers this peripheral for auto-reconnect
	c.mu.Lock()
	c.pendingPeripherals[peripheral.UUID] = peripheral
	c.mu.Unlock()

	// Attempt realistic connection with timing and potential failure
	go func() {
		err := c.wire.Connect(peripheral.UUID)

		// Clear connecting flag when done (success or failure)
		c.mu.Lock()
		delete(c.connectingDevices, peripheral.UUID)
		c.mu.Unlock()
		c.lifecycleLog.LogConnectingFlagCleared(peripheral.UUID)

		if err != nil {
			// Connection failed - set peripheral state to disconnected
			peripheral.mu.Lock()
			peripheral.State = CBPeripheralStateDisconnected
			peripheral.mu.Unlock()

			// Pass copy to delegate
			c.lifecycleLog.LogConnectCompleted(peripheral.UUID, false)
			peripheralCopy := *peripheral
			c.Delegate.DidFailToConnectPeripheral(*c, peripheralCopy, err)

			// iOS auto-reconnect: retry connection in background
			c.mu.RLock()
			autoReconnect := c.autoReconnectActive
			c.mu.RUnlock()

			if autoReconnect {
				go c.attemptReconnect(peripheral)
			}
			return
		}

		// Connection succeeded
		// REALISTIC iOS BEHAVIOR: Set peripheral state to connected
		peripheral.mu.Lock()
		peripheral.State = CBPeripheralStateConnected
		peripheral.mu.Unlock()

		// Track that we've connected to this peripheral (for RetrievePeripheralsByIdentifiers)
		c.mu.Lock()
		c.connectedPeripherals[peripheral.UUID] = true
		c.mu.Unlock()

		// Pass copy to delegate
		c.lifecycleLog.LogConnectCompleted(peripheral.UUID, true)
		peripheralCopy := *peripheral
		c.Delegate.DidConnectPeripheral(*c, peripheralCopy)
	}()
}

// attemptReconnect implements iOS's auto-reconnect behavior
// iOS will keep retrying connection in the background until it succeeds
func (c *CBCentralManager) attemptReconnect(peripheral *CBPeripheral) {
	// Wait before retrying (real iOS uses exponential backoff, we'll use fixed 2s delay)
	time.Sleep(2 * time.Second)

	// Check if still in pending list (app might have cancelled)
	c.mu.RLock()
	_, exists := c.pendingPeripherals[peripheral.UUID]
	c.mu.RUnlock()

	if !exists {
		return
	}

	// Try to reconnect
	err := c.wire.Connect(peripheral.UUID)
	if err != nil {
		// Failed again, keep retrying (iOS behavior)
		go c.attemptReconnect(peripheral)
		return
	}

	// Success! Notify delegate with copy
	if c.Delegate != nil {
		peripheralCopy := *peripheral
		c.Delegate.DidConnectPeripheral(*c, peripheralCopy)
	}
}

// CancelPeripheralConnection cancels a connection or pending connection
// Stops auto-reconnect for this peripheral (matches real iOS API)
func (c *CBCentralManager) CancelPeripheralConnection(peripheral *CBPeripheral) {
	// Remove from pending peripherals to stop auto-reconnect
	c.mu.Lock()
	delete(c.pendingPeripherals, peripheral.UUID)
	c.mu.Unlock()

	// Disconnect if currently connected
	c.wire.Disconnect(peripheral.UUID)
}

// RegisterReversePeripheral registers a peripheral object created when a Central connects to us
// This is needed for bidirectional communication: when we're acting as Peripheral in the BLE connection
// but we create a CBPeripheral object to make requests back to the Central
// This ensures notifications from the remote Central are routed to this peripheral's delegate
func (c *CBCentralManager) RegisterReversePeripheral(peripheral *CBPeripheral) {
	// Add to pending peripherals so HandleGATTMessage can route notifications to it
	c.mu.Lock()
	c.pendingPeripherals[peripheral.UUID] = peripheral
	c.mu.Unlock()
}

// HandleGATTMessage processes incoming GATT messages and routes them to the appropriate peripheral
// Should be called from iPhone layer for gatt_notification and gatt_response messages
// Returns true if message was handled, false otherwise
func (c *CBCentralManager) HandleGATTMessage(peerUUID string, msg *wire.GATTMessage) bool {
	// Only handle notification/response messages (these are for centrals)
	if msg.Type != "gatt_notification" && msg.Type != "gatt_response" {
		return false
	}

	// Find the peripheral for this UUID
	c.mu.RLock()
	peripheral, exists := c.pendingPeripherals[peerUUID]
	c.mu.RUnlock()
	if !exists {
		return false // Not connected to this peripheral
	}

	// Route to peripheral's handler
	return peripheral.HandleGATTMessage(peerUUID, msg)
}
