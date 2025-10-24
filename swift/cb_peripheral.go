
package swift

import (
	"fmt"
	"os"
)

type CBPeripheralDelegate interface {
	DidDiscoverServices(peripheral CBPeripheral, err error)
}

type CBPeripheral struct {
	Delegate CBPeripheralDelegate
	Name     string
	UUID     string
}

func (p *CBPeripheral) DiscoverServices(serviceUUIDs []string) {
	fmt.Println("Discovering services...")
}
