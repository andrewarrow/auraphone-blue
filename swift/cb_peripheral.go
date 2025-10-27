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
		delay := p.wire.GetSimulator().ServiceDiscoveryDelay
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
						// Fire and forget - WriteCharacteristicNoResponse returns immediately
						// Transmission happens asynchronously in background
						err = p.wire.WriteCharacteristicNoResponse(p.remoteUUID, r.characteristic.Service.UUID, r.characteristic.UUID, r.data)
						// Callback fires immediately (matches real iOS BLE behavior)
						// Any transmission failures after this point are silent
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
					// Ensure Service back-reference is set (for manually created services in tests)
					if char.Service == nil {
						char.Service = service
					}
					return char
				}
			}
		}
	}
	return nil
}

// HandleGATTMessage processes incoming GATT notification/response messages (public API for CBCentralManager)
// Should be called for gatt_notification and gatt_response messages only
// Returns true if message was handled, false if it should be routed elsewhere
func (p *CBPeripheral) HandleGATTMessage(peerUUID string, msg *wire.GATTMessage) bool {
	// Only handle notifications from the peripheral we're connected to
	if msg.Type != "gatt_notification" && msg.Type != "gatt_response" {
		return false
	}

	if peerUUID != p.remoteUUID {
		return false // Not from our connected peripheral
	}

	// Find the characteristic this message is for
	char := p.GetCharacteristic(msg.ServiceUUID, msg.CharacteristicUUID)
	if char == nil {
		return false
	}

	// Handle notification/indication
	if msg.Type == "gatt_notification" {
		// Only deliver if we subscribed
		if p.notifyingCharacteristics == nil || !p.notifyingCharacteristics[char.UUID] {
			return false
		}

		// Update characteristic value
		dataCopy := make([]byte, len(msg.Data))
		copy(dataCopy, msg.Data)
		char.Value = dataCopy

		// Deliver callback
		if p.Delegate != nil {
			p.Delegate.DidUpdateValueForCharacteristic(p, char, nil)
		}
		return true
	}

	// TODO: Handle gatt_response for read operations
	return false
}

// StartListening is now a no-op
// GATT message handling is done via HandleGATTMessage() callback from iPhone/CBCentralManager layer
func (p *CBPeripheral) StartListening() {
	// No-op: New architecture uses callbacks, not polling
}

func (p *CBPeripheral) StopListening() {
	if p.stopChan != nil {
		close(p.stopChan)
		p.stopChan = nil
	}
}
