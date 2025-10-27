package swift

import (
	"time"

	"github.com/user/auraphone-blue/wire"
)

type CBCentralManagerDelegate interface {
	DidUpdateState(central CBCentralManager)
	DidDiscoverPeripheral(central CBCentralManager, peripheral CBPeripheral, advertisementData map[string]interface{}, rssi float64)
	DidConnectPeripheral(central CBCentralManager, peripheral CBPeripheral)
	DidFailToConnectPeripheral(central CBCentralManager, peripheral CBPeripheral, err error)
	DidDisconnectPeripheral(central CBCentralManager, peripheral CBPeripheral, err error)
}

type CBCentralManager struct {
	Delegate            CBCentralManagerDelegate
	State               string
	uuid                string
	wire                *wire.Wire
	stopChan            chan struct{}
	pendingPeripherals  map[string]*CBPeripheral // UUIDs of peripherals to auto-reconnect
	autoReconnectActive bool                     // Whether auto-reconnect is enabled
}

func NewCBCentralManager(delegate CBCentralManagerDelegate, uuid string, sharedWire *wire.Wire) *CBCentralManager {
	cm := &CBCentralManager{
		Delegate:            delegate,
		State:               "poweredOn",
		uuid:                uuid,
		wire:                sharedWire,
		pendingPeripherals:  make(map[string]*CBPeripheral),
		autoReconnectActive: true, // iOS auto-reconnect is always active
	}

	// Set up disconnect callback
	sharedWire.SetDisconnectCallback(func(deviceUUID string) {
		// Connection was randomly dropped
		if delegate != nil {
			delegate.DidDisconnectPeripheral(*cm, CBPeripheral{
				UUID: deviceUUID,
			}, nil) // nil error = clean disconnect (not an error, just interference/distance)
		}

		// iOS auto-reconnect: if this peripheral was in pendingPeripherals, try to reconnect
		if cm.autoReconnectActive {
			if peripheral, exists := cm.pendingPeripherals[deviceUUID]; exists {
				// Automatically retry connection in background (matches real iOS behavior)
				go cm.attemptReconnect(peripheral)
			}
		}
	})

	return cm
}

func (c *CBCentralManager) ScanForPeripherals(withServices []string, options map[string]interface{}) {
	c.stopChan = c.wire.StartDiscovery(func(deviceUUID string) {
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

		c.Delegate.DidDiscoverPeripheral(*c, CBPeripheral{
			Name: deviceName,
			UUID: deviceUUID,
		}, advertisementData, rssi)
	})
}

func (c *CBCentralManager) StopScan() {
	if c.stopChan != nil {
		close(c.stopChan)
		c.stopChan = nil
	}
}

// RetrievePeripheralsByIdentifiers retrieves known peripherals by their identifiers
// Matches: centralManager.retrievePeripherals(withIdentifiers:)
// This allows connecting to peripherals without scanning (critical for iOS-to-iOS connections)
// In real iOS, this returns peripherals that the system knows about (previously connected or discovered)
// In our simulator, we check if the device exists in the wire layer (socket file exists)
func (c *CBCentralManager) RetrievePeripheralsByIdentifiers(identifiers []string) []*CBPeripheral {
	peripherals := make([]*CBPeripheral, 0, len(identifiers))

	for _, uuid := range identifiers {
		// Check if this device exists (has a socket file or is known to wire layer)
		if c.wire.DeviceExists(uuid) {
			// Read advertising data to get device name if available
			advData, err := c.wire.ReadAdvertisingData(uuid)
			deviceName := "Unknown Device"
			if err == nil && advData.DeviceName != "" {
				deviceName = advData.DeviceName
			}

			peripheral := &CBPeripheral{
				UUID: uuid,
				Name: deviceName,
				wire: c.wire,
				remoteUUID: uuid,
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

	// Set up the peripheral's wire connection
	peripheral.wire = c.wire
	peripheral.remoteUUID = peripheral.UUID

	// iOS remembers this peripheral for auto-reconnect
	c.pendingPeripherals[peripheral.UUID] = peripheral

	// Attempt realistic connection with timing and potential failure
	go func() {
		err := c.wire.Connect(peripheral.UUID)
		if err != nil {
			// Connection failed
			c.Delegate.DidFailToConnectPeripheral(*c, *peripheral, err)

			// iOS auto-reconnect: retry connection in background
			if c.autoReconnectActive {
				go c.attemptReconnect(peripheral)
			}
			return
		}

		// Connection succeeded
		c.Delegate.DidConnectPeripheral(*c, *peripheral)
	}()
}

// attemptReconnect implements iOS's auto-reconnect behavior
// iOS will keep retrying connection in the background until it succeeds
func (c *CBCentralManager) attemptReconnect(peripheral *CBPeripheral) {
	// Wait before retrying (real iOS uses exponential backoff, we'll use fixed 2s delay)
	time.Sleep(2 * time.Second)

	// Check if still in pending list (app might have cancelled)
	if _, exists := c.pendingPeripherals[peripheral.UUID]; !exists {
		return
	}

	// Try to reconnect
	err := c.wire.Connect(peripheral.UUID)
	if err != nil {
		// Failed again, keep retrying (iOS behavior)
		go c.attemptReconnect(peripheral)
		return
	}

	// Success! Notify delegate
	if c.Delegate != nil {
		c.Delegate.DidConnectPeripheral(*c, *peripheral)
	}
}

// CancelPeripheralConnection cancels a connection or pending connection
// Stops auto-reconnect for this peripheral (matches real iOS API)
func (c *CBCentralManager) CancelPeripheralConnection(peripheral *CBPeripheral) {
	// Remove from pending peripherals to stop auto-reconnect
	delete(c.pendingPeripherals, peripheral.UUID)

	// Disconnect if currently connected
	c.wire.Disconnect(peripheral.UUID)
}
