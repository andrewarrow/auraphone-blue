package kotlin

import (
	"sync"
	"time"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/wire"
)

// CCCD UUID constant (Client Characteristic Configuration Descriptor)
const (
	CCCD_UUID = "00002902-0000-1000-8000-00805f9b34fb"
)

// CCCD enable/disable values (matches Android BluetoothGattDescriptor)
var (
	ENABLE_NOTIFICATION_VALUE   = []byte{0x01, 0x00} // Enable notifications
	ENABLE_INDICATION_VALUE     = []byte{0x02, 0x00} // Enable indications
	DISABLE_NOTIFICATION_VALUE  = []byte{0x00, 0x00} // Disable notifications/indications
)

// BluetoothGattDescriptor represents a BLE descriptor
type BluetoothGattDescriptor struct {
	UUID           string
	Value          []byte
	Permissions    int                             // PERMISSION_READ, PERMISSION_WRITE, etc.
	Characteristic *BluetoothGattCharacteristic // Parent characteristic
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
	OnDescriptorWrite(gatt *BluetoothGatt, descriptor *BluetoothGattDescriptor, status int)
	OnDescriptorRead(gatt *BluetoothGatt, descriptor *BluetoothGattDescriptor, status int)
}

// gattOperation represents a queued GATT operation
type gattOperation struct {
	operationType string      // "read", "write", "writeDescriptor", "readDescriptor"
	execute       func() error // The actual operation to execute
}

type BluetoothGatt struct {
	callback                 BluetoothGattCallback
	wire                     *wire.Wire
	remoteUUID               string
	services                 []*BluetoothGattService
	notifyingCharacteristics map[string]bool // characteristic UUID -> is notifying
	autoConnect              bool            // Android autoConnect flag for auto-reconnect

	// CRITICAL: Android BLE requires operation serialization via queue
	// Real Android queues operations internally and processes them one at a time
	operationQueue           chan *gattOperation // Queue for GATT operations
	queueDone                chan struct{}       // Signal to stop queue processor
	operationQueueStarted    bool
	operationQueueMutex      sync.Mutex
}

// startOperationQueue starts the operation queue processor if not already started
// This ensures all GATT operations are serialized (one at a time)
func (g *BluetoothGatt) startOperationQueue() {
	g.operationQueueMutex.Lock()
	defer g.operationQueueMutex.Unlock()

	if g.operationQueueStarted {
		return
	}

	// Create queue channel (buffered to allow queueing multiple operations)
	// Real Android can queue multiple operations, but they execute one at a time
	g.operationQueue = make(chan *gattOperation, 100)
	g.queueDone = make(chan struct{})
	g.operationQueueStarted = true

	// Start queue processor goroutine
	go g.processOperationQueue()

	logger.Debug("BluetoothGatt", "✅ Operation queue started for connection to %s", g.remoteUUID[:8])
}

// processOperationQueue processes queued operations one at a time
// This matches real Android BLE behavior where only one operation can be in progress
func (g *BluetoothGatt) processOperationQueue() {
	for {
		select {
		case op := <-g.operationQueue:
			// Execute the operation (blocks until complete)
			// This serialization is the key to matching Android BLE behavior
			err := op.execute()
			if err != nil {
				logger.Debug("BluetoothGatt", "Operation failed: %v", err)
			}
		case <-g.queueDone:
			// Stop processing
			return
		}
	}
}

// enqueueOperation adds an operation to the queue for serial execution
func (g *BluetoothGatt) enqueueOperation(opType string, execute func() error) bool {
	g.startOperationQueue() // Ensure queue is started

	op := &gattOperation{
		operationType: opType,
		execute:       execute,
	}

	select {
	case g.operationQueue <- op:
		return true
	default:
		// Queue is full - this is an error condition
		logger.Warn("BluetoothGatt", "❌ Operation queue full - dropping %s operation", opType)
		return false
	}
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
		delay := g.wire.GetSimulator().ServiceDiscoveryDelay
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
				hasNotify := false
				hasIndicate := false
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
						hasNotify = true
					case "indicate":
						properties |= PROPERTY_INDICATE
						hasIndicate = true
					}
				}

				char := &BluetoothGattCharacteristic{
					UUID:        wireChar.UUID,
					Properties:  properties,
					Service:     service,
					Value:       nil,
					WriteType:   WRITE_TYPE_DEFAULT, // Default to withResponse
					Descriptors: make([]*BluetoothGattDescriptor, 0),
				}

				// CRITICAL: If characteristic has notify or indicate properties,
				// automatically add CCCD descriptor (0x2902)
				// This matches real BLE - every notifiable characteristic has a CCCD
				if hasNotify || hasIndicate {
					cccdDescriptor := &BluetoothGattDescriptor{
						UUID:           CCCD_UUID,
						Value:          []byte{0x00, 0x00}, // Disabled by default
						Permissions:    PERMISSION_READ | PERMISSION_WRITE,
						Characteristic: char,
					}
					char.Descriptors = append(char.Descriptors, cccdDescriptor)
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

func (g *BluetoothGatt) WriteCharacteristic(characteristic *BluetoothGattCharacteristic) bool {
	if g.wire == nil {
		return false
	}
	if characteristic == nil || characteristic.Service == nil {
		return false
	}

	// Copy the value before queueing to avoid race conditions
	// (caller might modify characteristic.Value after this call returns)
	valueCopy := make([]byte, len(characteristic.Value))
	copy(valueCopy, characteristic.Value)

	// CRITICAL: Enqueue operation instead of rejecting
	// Real Android queues operations and processes them serially
	return g.enqueueOperation("write", func() error {
		var err error
		if characteristic.WriteType == WRITE_TYPE_NO_RESPONSE {
			err = g.wire.WriteCharacteristicNoResponse(g.remoteUUID, characteristic.Service.UUID, characteristic.UUID, valueCopy)
		} else {
			err = g.wire.WriteCharacteristic(g.remoteUUID, characteristic.Service.UUID, characteristic.UUID, valueCopy)
		}

		// Callback after operation completes
		if g.callback != nil {
			if err != nil {
				g.callback.OnCharacteristicWrite(g, characteristic, 1) // GATT_FAILURE = 1
			} else {
				g.callback.OnCharacteristicWrite(g, characteristic, 0) // GATT_SUCCESS = 0
			}
		}

		return err
	})
}

func (g *BluetoothGatt) ReadCharacteristic(characteristic *BluetoothGattCharacteristic) bool {
	if g.wire == nil {
		return false
	}
	if characteristic == nil || characteristic.Service == nil {
		return false
	}

	// CRITICAL: Enqueue operation instead of rejecting
	// Real Android queues operations and processes them serially
	return g.enqueueOperation("read", func() error {
		err := g.wire.ReadCharacteristic(g.remoteUUID, characteristic.Service.UUID, characteristic.UUID)

		// Callback after operation completes
		if g.callback != nil {
			if err != nil {
				g.callback.OnCharacteristicRead(g, characteristic, 1) // GATT_FAILURE = 1
			} else {
				g.callback.OnCharacteristicRead(g, characteristic, 0) // GATT_SUCCESS = 0
			}
		}

		return err
	})
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

// GetDescriptor finds a descriptor within a characteristic
// This matches real Android API: characteristic.getDescriptor(uuid)
func (char *BluetoothGattCharacteristic) GetDescriptor(uuid string) *BluetoothGattDescriptor {
	for _, desc := range char.Descriptors {
		if desc.UUID == uuid {
			return desc
		}
	}
	return nil
}

// SetCharacteristicNotification enables or disables notifications for a characteristic
// This matches real Android BLE API: gatt.setCharacteristicNotification(characteristic, enable)
// IMPORTANT: In real Android, this only enables LOCAL notification tracking.
// You MUST also write to the CCCD descriptor (0x2902) to enable notifications on the peripheral.
// This is a two-step process:
//   1. gatt.setCharacteristicNotification(characteristic, true)  // Local tracking
//   2. gatt.writeDescriptor(cccdDescriptor)                      // Enable on peripheral
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

	// This ONLY updates local state - does NOT send anything to peripheral
	// Real Android requires a separate writeDescriptor() call
	g.notifyingCharacteristics[characteristic.UUID] = enable

	logger.Debug("BluetoothGatt", "🔔 SetCharacteristicNotification: char=%s, enable=%v (LOCAL ONLY - must write CCCD!)",
		characteristic.UUID[:8], enable)

	return true
}

// WriteDescriptor writes a value to a descriptor
// This matches real Android BLE API: gatt.writeDescriptor(descriptor)
// CRITICAL: This is how you enable notifications in real Android!
// After calling setCharacteristicNotification(), you must write 0x01 0x00 to CCCD (0x2902)
func (g *BluetoothGatt) WriteDescriptor(descriptor *BluetoothGattDescriptor) bool {
	if g.wire == nil {
		return false
	}
	if descriptor == nil || descriptor.Characteristic == nil || descriptor.Characteristic.Service == nil {
		return false
	}

	// Copy the value before queueing
	valueCopy := make([]byte, len(descriptor.Value))
	copy(valueCopy, descriptor.Value)

	// CRITICAL: Enqueue operation instead of rejecting
	// Real Android queues operations and processes them serially
	return g.enqueueOperation("writeDescriptor", func() error {
		// Send descriptor write as a regular characteristic write to the descriptor UUID
		// Wire layer treats descriptors as special characteristics
		err := g.wire.WriteCharacteristic(
			g.remoteUUID,
			descriptor.Characteristic.Service.UUID,
			descriptor.UUID, // Descriptor UUID (e.g., 0x2902 for CCCD)
			valueCopy,
		)

		// Callback after operation completes
		if g.callback != nil {
			if err != nil {
				g.callback.OnDescriptorWrite(g, descriptor, 1) // GATT_FAILURE
			} else {
				g.callback.OnDescriptorWrite(g, descriptor, 0) // GATT_SUCCESS
			}
		}

		return err
	})
}

// ReadDescriptor reads a value from a descriptor
// This matches real Android BLE API: gatt.readDescriptor(descriptor)
func (g *BluetoothGatt) ReadDescriptor(descriptor *BluetoothGattDescriptor) bool {
	if g.wire == nil {
		return false
	}
	if descriptor == nil || descriptor.Characteristic == nil || descriptor.Characteristic.Service == nil {
		return false
	}

	// CRITICAL: Enqueue operation instead of rejecting
	// Real Android queues operations and processes them serially
	return g.enqueueOperation("readDescriptor", func() error {
		// Send descriptor read as a regular characteristic read from the descriptor UUID
		err := g.wire.ReadCharacteristic(
			g.remoteUUID,
			descriptor.Characteristic.Service.UUID,
			descriptor.UUID, // Descriptor UUID
		)

		// Callback after operation completes
		if g.callback != nil {
			if err != nil {
				g.callback.OnDescriptorRead(g, descriptor, 1) // GATT_FAILURE
			} else {
				g.callback.OnDescriptorRead(g, descriptor, 0) // GATT_SUCCESS
			}
		}

		return err
	})
}

// HandleGATTMessage is called by android.go when a GATT message arrives for this connection
// This replaces the old inbox polling mechanism with direct message delivery
func (g *BluetoothGatt) HandleGATTMessage(msg *wire.GATTMessage) {
	// Find the characteristic this message is for
	char := g.GetCharacteristic(msg.ServiceUUID, msg.CharacteristicUUID)
	if char == nil {
		// Log when characteristic not found - helps debug subscription issues
		logger.Warn("BluetoothGatt", "⚠️  Characteristic not found: service=%s, char=%s, op=%s (remote=%s)",
			msg.ServiceUUID[:8], msg.CharacteristicUUID[:8], msg.Operation, g.remoteUUID[:8])
		logger.Debug("BluetoothGatt", "📋 Available services: %d", len(g.services))
		for _, svc := range g.services {
			logger.Debug("BluetoothGatt", "   Service: %s (%d characteristics)", svc.UUID[:8], len(svc.Characteristics))
			for _, ch := range svc.Characteristics {
				logger.Debug("BluetoothGatt", "      Char: %s", ch.UUID[:8])
			}
		}
		return
	}

	// Determine if we should deliver this message
	shouldDeliver := false
	if msg.Operation == "write" || msg.Operation == "write_no_response" {
		// Always deliver incoming writes (remote wrote to our characteristic)
		shouldDeliver = true
	} else if msg.Operation == "notify" || msg.Operation == "indicate" {
		// Only deliver notifications/indications if we subscribed
		isSubscribed := g.notifyingCharacteristics != nil && g.notifyingCharacteristics[char.UUID]
		if !isSubscribed {
			logger.Debug("BluetoothGatt", "⚠️  Notification/indication ignored - not subscribed to char %s", char.UUID[:8])
		}
		shouldDeliver = isSubscribed
	}

	if shouldDeliver {
		char.Value = msg.Data
		if g.callback != nil {
			g.callback.OnCharacteristicChanged(g, char)
		}
	}
}

func (g *BluetoothGatt) GetRemoteUUID() string {
	return g.remoteUUID
}

// Disconnect disconnects from the remote GATT server
// Matches Android's BluetoothGatt.disconnect() - stops connection but doesn't release resources
func (g *BluetoothGatt) Disconnect() {
	if g.wire != nil {
		// Disconnect from remote device
		g.wire.Disconnect(g.remoteUUID)
	}
}

// Close releases all resources associated with this GATT connection
// Matches Android's BluetoothGatt.close() - releases resources
func (g *BluetoothGatt) Close() {
	g.Disconnect()

	// Stop the operation queue processor
	g.operationQueueMutex.Lock()
	if g.operationQueueStarted {
		close(g.queueDone)
		g.operationQueueStarted = false
	}
	g.operationQueueMutex.Unlock()
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
