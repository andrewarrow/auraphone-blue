package swift

import (
	"fmt"

	"github.com/user/auraphone-blue/wire"
)

type CBCentralManagerDelegate interface {
	DidUpdateState(central CBCentralManager)
	DidDiscoverPeripheral(central CBCentralManager, peripheral CBPeripheral, advertisementData map[string]interface{}, rssi float64)
	DidConnectPeripheral(central CBCentralManager, peripheral CBPeripheral)
	DidFailToConnectPeripheral(central CBCentralManager, peripheral CBPeripheral, err error)
}

type CBCentralManager struct {
	Delegate CBCentralManagerDelegate
	State    string
	uuid     string
	wire     *wire.Wire
	stopChan chan struct{}
}

func NewCBCentralManager(delegate CBCentralManagerDelegate, uuid string) *CBCentralManager {
	return &CBCentralManager{
		Delegate: delegate,
		State:    "poweredOn",
		uuid:     uuid,
		wire:     wire.NewWire(uuid),
	}
}

func (c *CBCentralManager) ScanForPeripherals(withServices []string, options map[string]interface{}) {
	fmt.Println("Scanning for peripherals...")

	c.stopChan = c.wire.StartDiscovery(func(deviceUUID string) {
		c.Delegate.DidDiscoverPeripheral(*c, CBPeripheral{
			Name: "Android Test Device",
			UUID: deviceUUID,
		}, nil, -50)
	})
}

func (c *CBCentralManager) StopScan() {
	if c.stopChan != nil {
		close(c.stopChan)
		c.stopChan = nil
	}
}

func (c *CBCentralManager) Connect(peripheral *CBPeripheral, options map[string]interface{}) {
	fmt.Printf("Connecting to peripheral %s...\n", peripheral.UUID)

	// Set up the peripheral's wire connection
	peripheral.wire = c.wire
	peripheral.remoteUUID = peripheral.UUID

	// Simulate successful connection
	c.Delegate.DidConnectPeripheral(*c, *peripheral)
}
