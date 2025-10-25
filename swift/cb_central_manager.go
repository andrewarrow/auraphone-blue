package swift

import (
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
	Delegate CBCentralManagerDelegate
	State    string
	uuid     string
	wire     *wire.Wire
	stopChan chan struct{}
}

func NewCBCentralManager(delegate CBCentralManagerDelegate, uuid string) *CBCentralManager {
	w := wire.NewWire(uuid)

	cm := &CBCentralManager{
		Delegate: delegate,
		State:    "poweredOn",
		uuid:     uuid,
		wire:     w,
	}

	// Set up disconnect callback
	w.SetDisconnectCallback(func(deviceUUID string) {
		// Connection was randomly dropped
		if delegate != nil {
			delegate.DidDisconnectPeripheral(*cm, CBPeripheral{
				UUID: deviceUUID,
			}, nil) // nil error = clean disconnect (not an error, just interference/distance)
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
		rssi := float64(c.wire.GetRSSI())

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

func (c *CBCentralManager) Connect(peripheral *CBPeripheral, options map[string]interface{}) {
	// Set up the peripheral's wire connection
	peripheral.wire = c.wire
	peripheral.remoteUUID = peripheral.UUID

	// Attempt realistic connection with timing and potential failure
	go func() {
		err := c.wire.Connect(peripheral.UUID)
		if err != nil {
			// Connection failed
			c.Delegate.DidFailToConnectPeripheral(*c, *peripheral, err)
			return
		}

		// Connection succeeded
		c.Delegate.DidConnectPeripheral(*c, *peripheral)
	}()
}
