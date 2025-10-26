package kotlin

import (
	"time"

	"github.com/user/auraphone-blue/wire"
)

// BluetoothGattDescriptor represents a BLE descriptor
type BluetoothGattDescriptor struct {
	UUID        string
	Value       []byte
	Permissions int // PERMISSION_READ, PERMISSION_WRITE, etc.
}

// Write type constants (matches Android BLE API)
const (
	WRITE_TYPE_DEFAULT     = 0x02 // Write with response (wait for ACK)
	WRITE_TYPE_NO_RESPONSE = 0x01 // Write without response (fire and forget)
	WRITE_TYPE_SIGNED      = 0x04 // Signed write (rarely used)
)

// BluetoothGattCharacteristic represents a BLE characteristic
type BluetoothGattCharacteristic struct {
	UUID        string
	Properties  int // Bitmask of properties
	Service     *BluetoothGattService
	Value       []byte
	WriteType   int // WRITE_TYPE_DEFAULT or WRITE_TYPE_NO_RESPONSE
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
	data           []byte // Copy of data to prevent race condition
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
	autoConnect              bool // Android autoConnect flag for auto-reconnect
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
					WriteType:  WRITE_TYPE_DEFAULT, // Default to withResponse
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
					var err error
					if r.characteristic.WriteType == WRITE_TYPE_NO_RESPONSE {
						// Fire and forget - don't wait for ACK
						// Use copied data to avoid race condition
						err = g.wire.WriteCharacteristicNoResponse(g.remoteUUID, r.characteristic.Service.UUID, r.characteristic.UUID, r.data)
						// Callback comes immediately (doesn't wait for delivery)
						if g.callback != nil {
							if err != nil {
								g.callback.OnCharacteristicWrite(g, r.characteristic, 1) // GATT_FAILURE = 1
							} else {
								g.callback.OnCharacteristicWrite(g, r.characteristic, 0) // GATT_SUCCESS = 0
							}
						}
					} else {
						// With response - wait for ACK
						// Use copied data to avoid race condition
						err = g.wire.WriteCharacteristic(g.remoteUUID, r.characteristic.Service.UUID, r.characteristic.UUID, r.data)
						if err != nil {
							if g.callback != nil {
								g.callback.OnCharacteristicWrite(g, r.characteristic, 1) // GATT_FAILURE = 1
							}
							return
						}

						if g.callback != nil {
							g.callback.OnCharacteristicWrite(g, r.characteristic, 0) // GATT_SUCCESS = 0
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
		// Copy data to prevent race condition with incoming notifications
		dataCopy := make([]byte, len(characteristic.Value))
		copy(dataCopy, characteristic.Value)

		select {
		case g.writeQueue <- writeRequest{characteristic: characteristic, data: dataCopy}:
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
	// Copy data to prevent race condition with incoming notifications
	dataCopy := make([]byte, len(characteristic.Value))
	copy(dataCopy, characteristic.Value)

	var err error
	if characteristic.WriteType == WRITE_TYPE_NO_RESPONSE {
		err = g.wire.WriteCharacteristicNoResponse(g.remoteUUID, characteristic.Service.UUID, characteristic.UUID, dataCopy)
	} else {
		err = g.wire.WriteCharacteristic(g.remoteUUID, characteristic.Service.UUID, characteristic.UUID, dataCopy)
	}

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
	if characteristic == nil || characteristic.Service == nil {
		return false
	}

	if g.notifyingCharacteristics == nil {
		g.notifyingCharacteristics = make(map[string]bool)
	}

	g.notifyingCharacteristics[characteristic.UUID] = enable

	// Send subscribe/unsubscribe message to peripheral
	// In real Android, this would also write to the CCCD descriptor
	var err error
	if enable {
		err = g.wire.SubscribeCharacteristic(g.remoteUUID, characteristic.Service.UUID, characteristic.UUID)
	} else {
		err = g.wire.UnsubscribeCharacteristic(g.remoteUUID, characteristic.Service.UUID, characteristic.UUID)
	}

	return err == nil
}

func (g *BluetoothGatt) StartListening() {
	if g.wire == nil {
		return
	}

	g.stopChan = make(chan struct{})

	go func() {
		// Polling interval (25ms) balances responsiveness with filesystem stability
		// Real BLE uses hardware interrupts, but filesystem polling is an intentional
		// simplification for portability. This interval reduces filesystem pressure.
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-g.stopChan:
				return
			case <-ticker.C:
				// Central mode: read from central_inbox (notifications from peripherals)
				messages, err := g.wire.ReadAndConsumeCharacteristicMessagesFromInbox("central_inbox")
				if err != nil {
					continue
				}

				// Process each notification - messages are already consumed (deleted) by wire layer
				for _, msg := range messages {
					// IMPORTANT: Only process messages from the device we're connected to
					// Messages from other devices are already deleted but ignored
					if msg.SenderUUID != g.GetRemoteUUID() {
						continue // Skip messages not from our connected peripheral
					}

					// Find the characteristic this message is for
					char := g.GetCharacteristic(msg.ServiceUUID, msg.CharUUID)
					if char != nil {
						// Deliver the message data
						// - For "write" operations from remote, we receive the data
						// - For "notify" operations, only deliver if notifications are enabled
						shouldDeliver := false
						if msg.Operation == "write" || msg.Operation == "write_no_response" {
							// Always deliver incoming writes (remote wrote to our characteristic)
							shouldDeliver = true
						} else if msg.Operation == "notify" || msg.Operation == "indicate" {
							// Only deliver notifications/indications if we subscribed
							shouldDeliver = g.notifyingCharacteristics != nil && g.notifyingCharacteristics[char.UUID]
						}

						if shouldDeliver {
							// Create a copy of data to prevent race conditions
							dataCopy := make([]byte, len(msg.Data))
							copy(dataCopy, msg.Data)
							char.Value = dataCopy

							if g.callback != nil {
								// Deliver callback
								g.callback.OnCharacteristicChanged(g, char)
							}
						}
					}
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

// Disconnect disconnects from the remote GATT server
// Matches Android's BluetoothGatt.disconnect() - stops connection but doesn't release resources
func (g *BluetoothGatt) Disconnect() {
	if g.wire != nil {
		// Stop listening and write queue before disconnecting
		g.StopListening()
		g.StopWriteQueue()

		// Disconnect from remote device
		g.wire.Disconnect(g.remoteUUID)
	}
}

// Close releases all resources associated with this GATT connection
// Matches Android's BluetoothGatt.close() - releases resources
func (g *BluetoothGatt) Close() {
	g.Disconnect()
	// Additional cleanup would go here if needed
}

// attemptReconnect implements Android's autoConnect=true behavior
// Retries connection in background until it succeeds (matches real Android behavior)
func (g *BluetoothGatt) attemptReconnect() {
	if !g.autoConnect {
		return // Only retry if autoConnect is enabled
	}

	// Wait before retrying (Android uses longer delays than iOS, typically 30s)
	time.Sleep(5 * time.Second)

	// Try to reconnect
	// STATE_CONNECTING = 1
	if g.callback != nil {
		g.callback.OnConnectionStateChange(g, 0, 1)
	}

	err := g.wire.Connect(g.remoteUUID)
	if err != nil {
		// Failed again - notify disconnected and keep retrying
		if g.callback != nil {
			g.callback.OnConnectionStateChange(g, 1, 0) // status=1 (GATT_FAILURE)
		}

		// Keep retrying (Android autoConnect behavior)
		go g.attemptReconnect()
		return
	}

	// Success! Notify connected
	if g.callback != nil {
		g.callback.OnConnectionStateChange(g, 0, STATE_CONNECTED)
	}
}
