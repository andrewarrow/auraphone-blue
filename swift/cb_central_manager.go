package swift

import (
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
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

func NewCBCentralManager(delegate CBCentralManagerDelegate, uuid string) *CBCentralManager {
	return &CBCentralManager{
		Delegate: delegate,
		State:    "poweredOn",
		uuid:     uuid,
	}
}

func (c *CBCentralManager) ScanForPeripherals(withServices []string, options map[string]interface{}) {
	fmt.Println("Scanning for peripherals...")
	go func() {
		for {
			files, err := os.ReadDir(".")
			if err != nil {
				continue
			}

			for _, file := range files {
				if file.IsDir() {
					deviceName := file.Name()
					if _, err := uuid.Parse(deviceName); err == nil {
						if deviceName != c.uuid {
							c.Delegate.DidDiscoverPeripheral(*c, CBPeripheral{Name: "Android Test Device", UUID: deviceName}, nil, -50)
						}
					}
				}
			}
			time.Sleep(1 * time.Second)
		}
	}()
}
