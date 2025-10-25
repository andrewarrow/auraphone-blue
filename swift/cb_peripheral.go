package swift

import (
	"fmt"
	"time"

	"github.com/user/auraphone-blue/wire"
)

// CBCharacteristic represents a BLE characteristic
type CBCharacteristic struct {
	UUID       string
	Properties []string // "read", "write", "notify", "indicate", etc.
	Service    *CBService
	Value      []byte
}

// CBService represents a BLE service
type CBService struct {
	UUID            string
	IsPrimary       bool
	Characteristics []*CBCharacteristic
}

type CBPeripheralDelegate interface {
	DidDiscoverServices(peripheral *CBPeripheral, services []*CBService, err error)
	DidDiscoverCharacteristics(peripheral *CBPeripheral, service *CBService, err error)
	DidWriteValueForCharacteristic(peripheral *CBPeripheral, characteristic *CBCharacteristic, err error)
	DidUpdateValueForCharacteristic(peripheral *CBPeripheral, characteristic *CBCharacteristic, err error)
}

type CBPeripheral struct {
	Delegate              CBPeripheralDelegate
	Name                  string
	UUID                  string
	Services              []*CBService
	wire                  *wire.Wire
	remoteUUID            string
	stopChan              chan struct{}
	notifyingCharacteristics map[string]bool // characteristic UUID -> is notifying
}

func (p *CBPeripheral) DiscoverServices(serviceUUIDs []string) {
	if p.wire == nil {
		if p.Delegate != nil {
			p.Delegate.DidDiscoverServices(p, nil, fmt.Errorf("peripheral not connected"))
		}
		return
	}

	// Read GATT table from remote device
	gattTable, err := p.wire.ReadGATTTable(p.remoteUUID)
	if err != nil {
		if p.Delegate != nil {
			p.Delegate.DidDiscoverServices(p, nil, err)
		}
		return
	}

	// Convert wire.GATTService to CBService
	p.Services = make([]*CBService, 0)
	for _, wireService := range gattTable.Services {
		// Filter by requested UUIDs if specified
		if len(serviceUUIDs) > 0 {
			found := false
			for _, uuid := range serviceUUIDs {
				if wireService.UUID == uuid {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		service := &CBService{
			UUID:            wireService.UUID,
			IsPrimary:       wireService.Type == "primary",
			Characteristics: make([]*CBCharacteristic, 0),
		}

		// Convert characteristics
		for _, wireChar := range wireService.Characteristics {
			char := &CBCharacteristic{
				UUID:       wireChar.UUID,
				Properties: wireChar.Properties,
				Service:    service,
				Value:      nil,
			}
			service.Characteristics = append(service.Characteristics, char)
		}

		p.Services = append(p.Services, service)
	}

	if p.Delegate != nil {
		p.Delegate.DidDiscoverServices(p, p.Services, nil)
	}
}

func (p *CBPeripheral) DiscoverCharacteristics(characteristicUUIDs []string, service *CBService) {
	// Characteristics are already discovered with services in this implementation
	if p.Delegate != nil {
		p.Delegate.DidDiscoverCharacteristics(p, service, nil)
	}
}

func (p *CBPeripheral) WriteValue(data []byte, characteristic *CBCharacteristic) error {
	if p.wire == nil {
		return fmt.Errorf("peripheral not connected")
	}
	if characteristic == nil || characteristic.Service == nil {
		return fmt.Errorf("invalid characteristic")
	}

	err := p.wire.WriteCharacteristic(p.remoteUUID, characteristic.Service.UUID, characteristic.UUID, data)
	if err != nil {
		if p.Delegate != nil {
			p.Delegate.DidWriteValueForCharacteristic(p, characteristic, err)
		}
		return err
	}

	if p.Delegate != nil {
		p.Delegate.DidWriteValueForCharacteristic(p, characteristic, nil)
	}
	return nil
}

func (p *CBPeripheral) ReadValue(characteristic *CBCharacteristic) error {
	if p.wire == nil {
		return fmt.Errorf("peripheral not connected")
	}
	if characteristic == nil || characteristic.Service == nil {
		return fmt.Errorf("invalid characteristic")
	}

	return p.wire.ReadCharacteristic(p.remoteUUID, characteristic.Service.UUID, characteristic.UUID)
}

// SetNotifyValue enables or disables notifications for a characteristic
// This matches real iOS CoreBluetooth API: peripheral.setNotifyValue(_:for:)
func (p *CBPeripheral) SetNotifyValue(enabled bool, characteristic *CBCharacteristic) error {
	if p.wire == nil {
		return fmt.Errorf("peripheral not connected")
	}
	if characteristic == nil {
		return fmt.Errorf("invalid characteristic")
	}

	if p.notifyingCharacteristics == nil {
		p.notifyingCharacteristics = make(map[string]bool)
	}

	p.notifyingCharacteristics[characteristic.UUID] = enabled

	// In real iOS, this would write to the CCCD descriptor
	// For our simulation, we just track the subscription state
	return nil
}

// GetCharacteristic finds a characteristic by UUID within the peripheral's services
func (p *CBPeripheral) GetCharacteristic(serviceUUID, charUUID string) *CBCharacteristic {
	for _, service := range p.Services {
		if service.UUID == serviceUUID {
			for _, char := range service.Characteristics {
				if char.UUID == charUUID {
					return char
				}
			}
		}
	}
	return nil
}

func (p *CBPeripheral) StartListening() {
	if p.wire == nil {
		return
	}

	p.stopChan = make(chan struct{})

	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-p.stopChan:
				return
			case <-ticker.C:
				messages, err := p.wire.ReadCharacteristicMessages()
				if err != nil {
					continue
				}

				for _, msg := range messages {
					// Find the characteristic this message is for
					char := p.GetCharacteristic(msg.ServiceUUID, msg.CharUUID)
					if char != nil {
						// Only deliver updates if notifications are enabled for this characteristic
						// This matches real iOS behavior - you must call setNotifyValue(true) first
						if p.notifyingCharacteristics != nil && p.notifyingCharacteristics[char.UUID] {
							char.Value = msg.Data

							if p.Delegate != nil {
								p.Delegate.DidUpdateValueForCharacteristic(p, char, nil)
							}
						}
					}

					// Delete message after processing
					filename := fmt.Sprintf("msg_%d.json", msg.Timestamp)
					p.wire.DeleteInboxFile(filename)
				}
			}
		}
	}()
}

func (p *CBPeripheral) StopListening() {
	if p.stopChan != nil {
		close(p.stopChan)
		p.stopChan = nil
	}
}
