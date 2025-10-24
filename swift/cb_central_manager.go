
package swift

import (
	"fmt"
	"os"
)

type CBCentralManagerDelegate interface {
	DidUpdateState(central CBCentralManager)
	DidDiscoverPeripheral(central CBCentralManager, peripheral CBPeripheral, advertisementData map[string]interface{}, rssi float64)
}

type CBCentralManager struct {
	Delegate CBCentralManagerDelegate
	State    string
	uuid     string
}

func NewCBCentralManager(delegate CBCentralManagerDelegate) *CBCentralManager {
	return &CBCentralManager{
		Delegate: delegate,
		State:    "unknown",
		uuid:     "uninitialized-central-manager-uuid",
	}
}

func (c *CBCentralManager) ScanForPeripherals(withServices []string, options map[string]interface{}) {
	fmt.Println("Scanning for peripherals...")
}
