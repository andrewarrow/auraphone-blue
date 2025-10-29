package swift

import (
	"fmt"
	"sync"
	"time"

	"github.com/user/auraphone-blue/wire"
)

// CBCharacteristicWriteType matches iOS CoreBluetooth write types
type CBCharacteristicWriteType int

const (
	CBCharacteristicWriteWithResponse    CBCharacteristicWriteType = 0 // Wait for ACK (default)
	CBCharacteristicWriteWithoutResponse CBCharacteristicWriteType = 1 // Fire and forget (fast)
)

// CCCD UUID constant (Client Characteristic Configuration Descriptor)
// This is the standard BLE descriptor UUID for enabling notifications/indications
const (
	CBUUID_CCCD = "00002902-0000-1000-8000-00805f9b34fb"
)

// CCCD enable/disable values (matches iOS CoreBluetooth)
var (
	CBCCCDEnableNotificationValue  = []byte{0x01, 0x00} // Enable notifications
	CBCCCDEnableIndicationValue    = []byte{0x02, 0x00} // Enable indications
	CBCCCDDisableNotificationValue = []byte{0x00, 0x00} // Disable notifications/indications
)

// CBDescriptor represents a BLE descriptor (matches iOS CoreBluetooth CBDescriptor)
type CBDescriptor struct {
	UUID           string
	Value          []byte
	Characteristic *CBCharacteristic // Parent characteristic
}

// CBCharacteristic represents a BLE characteristic
type CBCharacteristic struct {
	UUID        string
	Properties  []string // "read", "write", "notify", "indicate", etc.
	Service     *CBService
	Value       []byte
	Descriptors []*CBDescriptor // Descriptors for this characteristic
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
	DidDiscoverDescriptorsForCharacteristic(peripheral *CBPeripheral, characteristic *CBCharacteristic, err error)
	DidWriteValueForCharacteristic(peripheral *CBPeripheral, characteristic *CBCharacteristic, err error)
	DidWriteValueForDescriptor(peripheral *CBPeripheral, descriptor *CBDescriptor, err error)
	DidUpdateValueForCharacteristic(peripheral *CBPeripheral, characteristic *CBCharacteristic, err error)
	DidUpdateValueForDescriptor(peripheral *CBPeripheral, descriptor *CBDescriptor, err error)
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
	State                    CBPeripheralState // REALISTIC: Track connection state
	Services                 []*CBService
	wire                     *wire.Wire
	remoteUUID               string
	stopChan                 chan struct{}
	notifyingCharacteristics map[string]bool // characteristic UUID -> is notifying
	writeQueue               chan writeRequest
	writeQueueStop           chan struct{}
	mu                       sync.RWMutex // Protect state changes
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
					UUID:        wireChar.UUID,
					Properties:  wireChar.Properties,
					Service:     service,
					Value:       nil,
					Descriptors: make([]*CBDescriptor, 0),
				}

				// CRITICAL: If characteristic has notify or indicate properties,
				// automatically add CCCD descriptor (0x2902)
				// This matches real BLE - every notifiable characteristic has a CCCD
				hasNotify := false
				hasIndicate := false
				for _, prop := range wireChar.Properties {
					if prop == "notify" {
						hasNotify = true
					}
					if prop == "indicate" {
						hasIndicate = true
					}
				}

				if hasNotify || hasIndicate {
					cccdDescriptor := &CBDescriptor{
						UUID:           CBUUID_CCCD,
						Value:          []byte{0x00, 0x00}, // Disabled by default
						Characteristic: char,
					}
					char.Descriptors = append(char.Descriptors, cccdDescriptor)
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
	// REALISTIC iOS BEHAVIOR: Characteristic discovery is asynchronous, even if services are already known
	// Real iOS takes 20-100ms to discover characteristics
	go func() {
		// Simulate realistic discovery delay
		time.Sleep(50 * time.Millisecond)

		// Characteristics are already discovered with services in this implementation
		// Just filter by requested UUIDs if specified
		if len(characteristicUUIDs) > 0 && service != nil {
			filteredChars := make([]*CBCharacteristic, 0)
			for _, char := range service.Characteristics {
				for _, uuid := range characteristicUUIDs {
					if char.UUID == uuid {
						filteredChars = append(filteredChars, char)
						break
					}
				}
			}
			service.Characteristics = filteredChars
		}

		if p.Delegate != nil {
			p.Delegate.DidDiscoverCharacteristics(p, service, nil)
		}
	}()
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

// DiscoverDescriptors discovers descriptors for a characteristic
// This matches real iOS CoreBluetooth API: peripheral.discoverDescriptors(for:)
func (p *CBPeripheral) DiscoverDescriptors(characteristic *CBCharacteristic) {
	if p.wire == nil {
		if p.Delegate != nil {
			p.Delegate.DidDiscoverDescriptorsForCharacteristic(p, characteristic, fmt.Errorf("peripheral not connected"))
		}
		return
	}

	// REALISTIC iOS BEHAVIOR: Descriptor discovery is asynchronous
	// Real iOS takes 20-100ms to discover descriptors
	go func() {
		// Simulate realistic discovery delay
		time.Sleep(50 * time.Millisecond)

		// Descriptors are already discovered with characteristics in this implementation
		// (we auto-generate CCCD descriptors for notifiable characteristics)
		// Just deliver the callback
		if p.Delegate != nil {
			p.Delegate.DidDiscoverDescriptorsForCharacteristic(p, characteristic, nil)
		}
	}()
}

// WriteValueForDescriptor writes a value to a descriptor
// This matches real iOS CoreBluetooth API: peripheral.writeValue(_:for:)
func (p *CBPeripheral) WriteValueForDescriptor(data []byte, descriptor *CBDescriptor) error {
	if p.wire == nil {
		return fmt.Errorf("peripheral not connected")
	}
	if descriptor == nil || descriptor.Characteristic == nil || descriptor.Characteristic.Service == nil {
		return fmt.Errorf("invalid descriptor")
	}

	// Write descriptor value via wire layer
	err := p.wire.WriteCharacteristic(
		p.remoteUUID,
		descriptor.Characteristic.Service.UUID,
		descriptor.UUID, // Descriptor UUID (e.g., 0x2902 for CCCD)
		data,
	)

	// Update local descriptor value if successful
	if err == nil {
		valueCopy := make([]byte, len(data))
		copy(valueCopy, data)
		descriptor.Value = valueCopy
	}

	// Deliver callback
	if p.Delegate != nil {
		p.Delegate.DidWriteValueForDescriptor(p, descriptor, err)
	}

	return err
}

// ReadValueForDescriptor reads a value from a descriptor
// This matches real iOS CoreBluetooth API: peripheral.readValue(for:)
func (p *CBPeripheral) ReadValueForDescriptor(descriptor *CBDescriptor) error {
	if p.wire == nil {
		return fmt.Errorf("peripheral not connected")
	}
	if descriptor == nil || descriptor.Characteristic == nil || descriptor.Characteristic.Service == nil {
		return fmt.Errorf("invalid descriptor")
	}

	// Read descriptor value via wire layer
	// Note: wire layer doesn't distinguish between characteristic and descriptor reads
	// Both use ReadCharacteristic with the appropriate UUID
	return p.wire.ReadCharacteristic(
		p.remoteUUID,
		descriptor.Characteristic.Service.UUID,
		descriptor.UUID,
	)
}

// SetNotifyValue enables or disables notifications for a characteristic
// This matches real iOS CoreBluetooth API: peripheral.setNotifyValue(_:for:)
// REALISTIC BLE BEHAVIOR: This writes to the CCCD descriptor (0x2902) to enable/disable notifications
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

	// Update local tracking
	p.notifyingCharacteristics[characteristic.UUID] = enabled

	// REALISTIC BLE: Find the CCCD descriptor (0x2902) and write to it
	// Real iOS CoreBluetooth does this automatically when you call setNotifyValue
	var cccdDescriptor *CBDescriptor
	for _, descriptor := range characteristic.Descriptors {
		if descriptor.UUID == CBUUID_CCCD {
			cccdDescriptor = descriptor
			break
		}
	}

	if cccdDescriptor == nil {
		return fmt.Errorf("characteristic does not have CCCD descriptor (not notifiable)")
	}

	// Determine the value to write (0x01 0x00 for notify, 0x00 0x00 for disable)
	var cccdValue []byte
	if enabled {
		// Check if characteristic has indicate property
		hasIndicate := false
		for _, prop := range characteristic.Properties {
			if prop == "indicate" {
				hasIndicate = true
				break
			}
		}

		if hasIndicate {
			cccdValue = CBCCCDEnableIndicationValue // 0x02 0x00
		} else {
			cccdValue = CBCCCDEnableNotificationValue // 0x01 0x00
		}
	} else {
		cccdValue = CBCCCDDisableNotificationValue // 0x00 0x00
	}

	// Write to CCCD descriptor (this is the realistic BLE way!)
	// Real iOS sends this as a descriptor write operation
	err := p.wire.WriteCharacteristic(p.remoteUUID, characteristic.Service.UUID, CBUUID_CCCD, cccdValue)

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

	// Handle gatt_response for read operations
	if msg.Type == "gatt_response" && msg.Operation == "read_response" {
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

// NewCBPeripheralFromConnection creates a CBPeripheral for an existing connection
// Used when a Central connects to us (we're Peripheral) and we need to create
// a reverse peripheral object to make requests back to them
func NewCBPeripheralFromConnection(peerUUID string, name string, w *wire.Wire) *CBPeripheral {
	return &CBPeripheral{
		Name:                     name,
		UUID:                     peerUUID,
		Services:                 []*CBService{}, // Will be discovered on-demand
		wire:                     w,
		remoteUUID:               peerUUID,
		notifyingCharacteristics: make(map[string]bool),
	}
}
