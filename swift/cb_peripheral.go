package swift

import (
	"fmt"
	"time"

	"github.com/user/auraphone-blue/wire"
)

// CBCharacteristicWriteType matches iOS CoreBluetooth write types
type CBCharacteristicWriteType int

const (
	CBCharacteristicWriteWithResponse    CBCharacteristicWriteType = 0 // Wait for ACK (default)
	CBCharacteristicWriteWithoutResponse CBCharacteristicWriteType = 1 // Fire and forget (fast)
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

type writeRequest struct {
	data           []byte
	characteristic *CBCharacteristic
	writeType      CBCharacteristicWriteType
}

type CBPeripheral struct {
	Delegate                 CBPeripheralDelegate
	Name                     string
	UUID                     string
	Services                 []*CBService
	wire                     *wire.Wire
	remoteUUID               string
	stopChan                 chan struct{}
	notifyingCharacteristics map[string]bool // characteristic UUID -> is notifying
	writeQueue               chan writeRequest
	writeQueueStop           chan struct{}
}

func (p *CBPeripheral) DiscoverServices(serviceUUIDs []string) {
	if p.wire == nil {
		if p.Delegate != nil {
			p.Delegate.DidDiscoverServices(p, nil, fmt.Errorf("peripheral not connected"))
		}
		return
	}

	// Service discovery is async in real iOS - run in goroutine with realistic delay
	go func() {
		// Simulate realistic discovery delay (50-500ms)
		delay := p.wire.GetSimulator().ServiceDiscoveryDelay()
		time.Sleep(delay)

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
	}()
}

func (p *CBPeripheral) DiscoverCharacteristics(characteristicUUIDs []string, service *CBService) {
	// Characteristics are already discovered with services in this implementation
	if p.Delegate != nil {
		p.Delegate.DidDiscoverCharacteristics(p, service, nil)
	}
}

// StartWriteQueue initializes the async write queue (matches real BLE behavior)
func (p *CBPeripheral) StartWriteQueue() {
	if p.writeQueue != nil {
		return // Already started
	}

	p.writeQueue = make(chan writeRequest, 10) // Buffer up to 10 writes
	p.writeQueueStop = make(chan struct{})

	go func() {
		for {
			select {
			case <-p.writeQueueStop:
				return
			case req := <-p.writeQueue:
				// Process write asynchronously
				go func(r writeRequest) {
					var err error
					if r.writeType == CBCharacteristicWriteWithoutResponse {
						// Fire and forget - don't wait for ACK
						err = p.wire.WriteCharacteristicNoResponse(p.remoteUUID, r.characteristic.Service.UUID, r.characteristic.UUID, r.data)
						// Callback comes immediately (doesn't wait for delivery)
						if p.Delegate != nil {
							p.Delegate.DidWriteValueForCharacteristic(p, r.characteristic, err)
						}
					} else {
						// With response - wait for ACK
						err = p.wire.WriteCharacteristic(p.remoteUUID, r.characteristic.Service.UUID, r.characteristic.UUID, r.data)
						if err != nil {
							if p.Delegate != nil {
								p.Delegate.DidWriteValueForCharacteristic(p, r.characteristic, err)
							}
							return
						}

						if p.Delegate != nil {
							p.Delegate.DidWriteValueForCharacteristic(p, r.characteristic, nil)
						}
					}
				}(req)

				// Small delay between writes to simulate BLE radio constraints
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()
}

// StopWriteQueue stops the write queue
func (p *CBPeripheral) StopWriteQueue() {
	if p.writeQueueStop != nil {
		close(p.writeQueueStop)
		p.writeQueueStop = nil
		close(p.writeQueue)
		p.writeQueue = nil
	}
}

// WriteValue writes data to characteristic with specified write type (matches real iOS API)
func (p *CBPeripheral) WriteValue(data []byte, characteristic *CBCharacteristic, writeType CBCharacteristicWriteType) error {
	if p.wire == nil {
		return fmt.Errorf("peripheral not connected")
	}
	if characteristic == nil || characteristic.Service == nil {
		return fmt.Errorf("invalid characteristic")
	}

	// If write queue is active, queue the write (async like real iOS)
	if p.writeQueue != nil {
		select {
		case p.writeQueue <- writeRequest{data: data, characteristic: characteristic, writeType: writeType}:
			// Queued successfully - callback will come later
			return nil
		default:
			// Queue full - this would fail in real BLE too
			return fmt.Errorf("write queue full")
		}
	}

	// Fallback: synchronous write (if queue not started)
	var err error
	if writeType == CBCharacteristicWriteWithoutResponse {
		err = p.wire.WriteCharacteristicNoResponse(p.remoteUUID, characteristic.Service.UUID, characteristic.UUID, data)
	} else {
		err = p.wire.WriteCharacteristic(p.remoteUUID, characteristic.Service.UUID, characteristic.UUID, data)
	}

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
	if characteristic == nil || characteristic.Service == nil {
		return fmt.Errorf("invalid characteristic")
	}

	if p.notifyingCharacteristics == nil {
		p.notifyingCharacteristics = make(map[string]bool)
	}

	p.notifyingCharacteristics[characteristic.UUID] = enabled

	// Send subscribe/unsubscribe message to peripheral
	// In real iOS, this would write to the CCCD descriptor
	var err error
	if enabled {
		err = p.wire.SubscribeCharacteristic(p.remoteUUID, characteristic.Service.UUID, characteristic.UUID)
	} else {
		err = p.wire.UnsubscribeCharacteristic(p.remoteUUID, characteristic.Service.UUID, characteristic.UUID)
	}

	return err
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
