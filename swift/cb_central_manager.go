package swift

import (
	"fmt"

	"github.com/user/auraphone-blue/wire"
)

type CBCentralManagerDelegate interface {
	DidUpdateState(central CBCentralManager)
	DidDiscoverPeripheral(central CBCentralManager, peripheral CBPeripheral, advertisementData map[string]interface{}, rssi float64)
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
