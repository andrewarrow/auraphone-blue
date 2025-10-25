
package kotlin

import (
	"fmt"
	"time"

	"github.com/user/auraphone-blue/wire"
)

// BluetoothGattDescriptor represents a BLE descriptor
type BluetoothGattDescriptor struct {
	UUID  string
	Value []byte
}

// BluetoothGattCharacteristic represents a BLE characteristic
type BluetoothGattCharacteristic struct {
	UUID        string
	Properties  int // Bitmask of properties
	Service     *BluetoothGattService
	Value       []byte
	Descriptors []*BluetoothGattDescriptor
}

// Property constants (Android uses bitmask)
const (
	PROPERTY_READ              = 0x02
	PROPERTY_WRITE             = 0x08
	PROPERTY_WRITE_NO_RESPONSE = 0x04
	PROPERTY_NOTIFY            = 0x10
	PROPERTY_INDICATE          = 0x20
)

// BluetoothGattService represents a BLE service
type BluetoothGattService struct {
	UUID            string
	Type            int // SERVICE_TYPE_PRIMARY = 0, SERVICE_TYPE_SECONDARY = 1
	Characteristics []*BluetoothGattCharacteristic
}

const (
	SERVICE_TYPE_PRIMARY   = 0
	SERVICE_TYPE_SECONDARY = 1
)

type BluetoothGattCallback interface {
	OnConnectionStateChange(gatt *BluetoothGatt, status int, newState int)
	OnServicesDiscovered(gatt *BluetoothGatt, status int)
	OnCharacteristicWrite(gatt *BluetoothGatt, characteristic *BluetoothGattCharacteristic, status int)
	OnCharacteristicRead(gatt *BluetoothGatt, characteristic *BluetoothGattCharacteristic, status int)
	OnCharacteristicChanged(gatt *BluetoothGatt, characteristic *BluetoothGattCharacteristic)
}

type writeRequest struct {
	characteristic *BluetoothGattCharacteristic
}

type BluetoothGatt struct {
	callback                 BluetoothGattCallback
	wire                     *wire.Wire
	remoteUUID               string
	services                 []*BluetoothGattService
	stopChan                 chan struct{}
	notifyingCharacteristics map[string]bool // characteristic UUID -> is notifying
	writeQueue               chan writeRequest
	writeQueueStop           chan struct{}
}

func (g *BluetoothGatt) DiscoverServices() bool {
	if g.wire == nil {
		if g.callback != nil {
			g.callback.OnServicesDiscovered(g, 1) // GATT_FAILURE = 1
		}
		return false
	}

	// Service discovery is async in real Android - run in goroutine with realistic delay
	go func() {
		// Simulate realistic discovery delay (50-500ms)
		delay := g.wire.GetSimulator().ServiceDiscoveryDelay()
		time.Sleep(delay)

		// Read GATT table from remote device
		gattTable, err := g.wire.ReadGATTTable(g.remoteUUID)
		if err != nil {
			if g.callback != nil {
				g.callback.OnServicesDiscovered(g, 1) // GATT_FAILURE = 1
			}
			return
		}

		// Convert wire.GATTService to BluetoothGattService
		g.services = make([]*BluetoothGattService, 0)
		for _, wireService := range gattTable.Services {
			serviceType := SERVICE_TYPE_PRIMARY
			if wireService.Type == "secondary" {
				serviceType = SERVICE_TYPE_SECONDARY
			}

			service := &BluetoothGattService{
				UUID:            wireService.UUID,
				Type:            serviceType,
				Characteristics: make([]*BluetoothGattCharacteristic, 0),
			}

			// Convert characteristics
			for _, wireChar := range wireService.Characteristics {
				// Convert property strings to bitmask
				properties := 0
				for _, prop := range wireChar.Properties {
					switch prop {
					case "read":
						properties |= PROPERTY_READ
					case "write":
						properties |= PROPERTY_WRITE
					case "write_no_response":
						properties |= PROPERTY_WRITE_NO_RESPONSE
					case "notify":
						properties |= PROPERTY_NOTIFY
					case "indicate":
						properties |= PROPERTY_INDICATE
					}
				}

				char := &BluetoothGattCharacteristic{
					UUID:       wireChar.UUID,
					Properties: properties,
					Service:    service,
					Value:      nil,
				}
				service.Characteristics = append(service.Characteristics, char)
			}

			g.services = append(g.services, service)
		}

		if g.callback != nil {
			g.callback.OnServicesDiscovered(g, 0) // GATT_SUCCESS = 0
		}
	}()

	return true
}

func (g *BluetoothGatt) GetServices() []*BluetoothGattService {
	return g.services
}

func (g *BluetoothGatt) GetService(uuid string) *BluetoothGattService {
	for _, service := range g.services {
		if service.UUID == uuid {
			return service
		}
	}
	return nil
}

// StartWriteQueue initializes the async write queue (matches real Android BLE behavior)
func (g *BluetoothGatt) StartWriteQueue() {
	if g.writeQueue != nil {
		return // Already started
	}

	g.writeQueue = make(chan writeRequest, 10) // Buffer up to 10 writes
	g.writeQueueStop = make(chan struct{})

	go func() {
		for {
			select {
			case <-g.writeQueueStop:
				return
			case req := <-g.writeQueue:
				// Process write asynchronously
				go func(r writeRequest) {
					err := g.wire.WriteCharacteristic(g.remoteUUID, r.characteristic.Service.UUID, r.characteristic.UUID, r.characteristic.Value)
					if err != nil {
						if g.callback != nil {
							g.callback.OnCharacteristicWrite(g, r.characteristic, 1) // GATT_FAILURE = 1
						}
						return
					}

					if g.callback != nil {
						g.callback.OnCharacteristicWrite(g, r.characteristic, 0) // GATT_SUCCESS = 0
					}
				}(req)

				// Small delay between writes to simulate BLE radio constraints
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()
}

// StopWriteQueue stops the write queue
func (g *BluetoothGatt) StopWriteQueue() {
	if g.writeQueueStop != nil {
		close(g.writeQueueStop)
		g.writeQueueStop = nil
		close(g.writeQueue)
		g.writeQueue = nil
	}
}

func (g *BluetoothGatt) WriteCharacteristic(characteristic *BluetoothGattCharacteristic) bool {
	if g.wire == nil {
		return false
	}
	if characteristic == nil || characteristic.Service == nil {
		return false
	}

	// If write queue is active, queue the write (async like real Android)
	if g.writeQueue != nil {
		select {
		case g.writeQueue <- writeRequest{characteristic: characteristic}:
			// Queued successfully - callback will come later
			return true
		default:
			// Queue full - this would fail in real BLE too
			if g.callback != nil {
				g.callback.OnCharacteristicWrite(g, characteristic, 1) // GATT_FAILURE = 1
			}
			return false
		}
	}

	// Fallback: synchronous write (if queue not started)
	err := g.wire.WriteCharacteristic(g.remoteUUID, characteristic.Service.UUID, characteristic.UUID, characteristic.Value)
	if err != nil {
		if g.callback != nil {
			g.callback.OnCharacteristicWrite(g, characteristic, 1) // GATT_FAILURE = 1
		}
		return false
	}

	if g.callback != nil {
		g.callback.OnCharacteristicWrite(g, characteristic, 0) // GATT_SUCCESS = 0
	}
	return true
}

func (g *BluetoothGatt) ReadCharacteristic(characteristic *BluetoothGattCharacteristic) bool {
	if g.wire == nil {
		return false
	}
	if characteristic == nil || characteristic.Service == nil {
		return false
	}

	err := g.wire.ReadCharacteristic(g.remoteUUID, characteristic.Service.UUID, characteristic.UUID)
	if err != nil {
		if g.callback != nil {
			g.callback.OnCharacteristicRead(g, characteristic, 1) // GATT_FAILURE = 1
		}
		return false
	}

	return true
}

// GetCharacteristic finds a characteristic by UUID
func (g *BluetoothGatt) GetCharacteristic(serviceUUID, charUUID string) *BluetoothGattCharacteristic {
	for _, service := range g.services {
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

// SetCharacteristicNotification enables or disables notifications for a characteristic
// This matches real Android BLE API: gatt.setCharacteristicNotification(characteristic, enable)
func (g *BluetoothGatt) SetCharacteristicNotification(characteristic *BluetoothGattCharacteristic, enable bool) bool {
	if g.wire == nil {
		return false
	}
	if characteristic == nil {
		return false
	}

	if g.notifyingCharacteristics == nil {
		g.notifyingCharacteristics = make(map[string]bool)
	}

	g.notifyingCharacteristics[characteristic.UUID] = enable

	// In real Android, this would also write to the CCCD descriptor
	// For our simulation, we just track the subscription state
	return true
}

func (g *BluetoothGatt) StartListening() {
	if g.wire == nil {
		return
	}

	g.stopChan = make(chan struct{})

	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-g.stopChan:
				return
			case <-ticker.C:
				messages, err := g.wire.ReadCharacteristicMessages()
				if err != nil {
					continue
				}

				for _, msg := range messages {
					// Find the characteristic this message is for
					char := g.GetCharacteristic(msg.ServiceUUID, msg.CharUUID)
					if char != nil {
						// Only deliver updates if notifications are enabled for this characteristic
						// This matches real Android behavior - you must call setCharacteristicNotification(true) first
						if g.notifyingCharacteristics != nil && g.notifyingCharacteristics[char.UUID] {
							char.Value = msg.Data

							if g.callback != nil {
								g.callback.OnCharacteristicChanged(g, char)
							}
						}
					}

					// Delete message after processing
					filename := fmt.Sprintf("msg_%d.json", msg.Timestamp)
					g.wire.DeleteInboxFile(filename)
				}
			}
		}
	}()
}

func (g *BluetoothGatt) StopListening() {
	if g.stopChan != nil {
		close(g.stopChan)
		g.stopChan = nil
	}
}

func (g *BluetoothGatt) GetRemoteUUID() string {
	return g.remoteUUID
}
