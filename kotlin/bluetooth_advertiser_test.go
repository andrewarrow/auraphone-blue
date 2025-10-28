package kotlin

import (
	"testing"
	"time"

	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/wire"
)

// TestBluetoothLeAdvertiser_DirectMessageDelivery tests that GATT server receives messages directly
func TestBluetoothLeAdvertiser_DirectMessageDelivery(t *testing.T) {
	w := wire.NewWire("peripheral-uuid")
	if err := w.Start(); err != nil {
		t.Fatalf("Failed to start wire: %v", err)
	}
	defer w.Stop()

	// Create GATT server
	receivedWrites := make(chan []byte, 10)
	callback := &testGattServerCallback{
		onCharacteristicWriteRequest: func(device *BluetoothDevice, requestId int, char *BluetoothGattCharacteristic, preparedWrite bool, responseNeeded bool, offset int, value []byte) {
			receivedWrites <- value
		},
	}

	gattServer := NewBluetoothGattServer("peripheral-uuid", callback, "Test Device", w)

	// Add service
	service := &BluetoothGattService{
		UUID: phone.AuraServiceUUID,
		Type: SERVICE_TYPE_PRIMARY,
		Characteristics: []*BluetoothGattCharacteristic{
			{
				UUID:       phone.AuraProtocolCharUUID,
				Properties: PROPERTY_WRITE | PROPERTY_READ,
			},
		},
	}
	gattServer.AddService(service)

	// Create advertiser
	advertiser := NewBluetoothLeAdvertiser("peripheral-uuid", "Test Device", w)
	advertiser.SetGattServer(gattServer)

	// Start advertising
	settings := &AdvertiseSettings{
		AdvertiseMode: ADVERTISE_MODE_LOW_LATENCY,
		Connectable:   true,
	}
	advertiseData := &AdvertiseData{
		ServiceUUIDs:      []string{phone.AuraServiceUUID},
		IncludeDeviceName: true,
	}

	startSuccess := make(chan bool, 1)
	advertiseCallback := &testAdvertiseCallback{
		onStartSuccess: func(settings *AdvertiseSettings) {
			startSuccess <- true
		},
	}

	advertiser.StartAdvertising(settings, advertiseData, nil, advertiseCallback)

	// Wait for advertising to start
	select {
	case <-startSuccess:
		t.Logf("✅ Advertising started")
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timeout waiting for advertising to start")
	}

	// Create a GATT message and deliver it directly (new pattern - no inbox polling!)
	msg := &wire.GATTMessage{
		Operation:   "write",
		ServiceUUID: phone.AuraServiceUUID,
		CharUUID:    phone.AuraProtocolCharUUID,
		Data:        []byte("test write from central"),
		SenderUUID:  "central-uuid",
	}

	// Deliver directly via HandleGATTMessage (no polling!)
	advertiser.HandleGATTMessage(msg)

	// Check if message was received by GATT server
	select {
	case data := <-receivedWrites:
		if string(data) != "test write from central" {
			t.Errorf("Wrong write data: %s", string(data))
		}
		t.Logf("✅ Direct message delivery to GATT server works (no inbox polling)")
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timeout waiting for write request")
	}
}

// TestBluetoothGattServer_NotifyCharacteristicChanged tests sending notifications
func TestBluetoothGattServer_NotifyCharacteristicChanged(t *testing.T) {
	w1 := wire.NewWire("peripheral-uuid")
	w2 := wire.NewWire("central-uuid")

	if err := w1.Start(); err != nil {
		t.Fatalf("Failed to start peripheral wire: %v", err)
	}
	defer w1.Stop()

	if err := w2.Start(); err != nil {
		t.Fatalf("Failed to start central wire: %v", err)
	}
	defer w2.Stop()

	// Connect central to peripheral
	if err := w2.Connect("peripheral-uuid"); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Create GATT server
	callback := &testGattServerCallback{}
	gattServer := NewBluetoothGattServer("peripheral-uuid", callback, "Test Device", w1)

	// Add service
	service := &BluetoothGattService{
		UUID: phone.AuraServiceUUID,
		Type: SERVICE_TYPE_PRIMARY,
		Characteristics: []*BluetoothGattCharacteristic{
			{
				UUID:       phone.AuraProtocolCharUUID,
				Properties: PROPERTY_NOTIFY,
			},
		},
	}
	gattServer.AddService(service)

	// Get characteristic
	char := gattServer.GetCharacteristic(phone.AuraServiceUUID, phone.AuraProtocolCharUUID)
	if char == nil {
		t.Fatal("Failed to get characteristic")
	}

	// Set notification data
	char.Value = []byte("notification data")

	// Send notification to central
	device := &BluetoothDevice{
		Address: "central-uuid",
		Name:    "Central Device",
	}

	success := gattServer.NotifyCharacteristicChanged(device, char, false)
	if !success {
		t.Error("NotifyCharacteristicChanged failed")
	}

	t.Logf("✅ GATT server can send notifications")
}

// testAdvertiseCallback is a test implementation of AdvertiseCallback
type testAdvertiseCallback struct {
	onStartSuccess func(settings *AdvertiseSettings)
	onStartFailure func(errorCode int)
}

func (c *testAdvertiseCallback) OnStartSuccess(settings *AdvertiseSettings) {
	if c.onStartSuccess != nil {
		c.onStartSuccess(settings)
	}
}

func (c *testAdvertiseCallback) OnStartFailure(errorCode int) {
	if c.onStartFailure != nil {
		c.onStartFailure(errorCode)
	}
}

// testGattServerCallback is a test implementation of BluetoothGattServerCallback
type testGattServerCallback struct {
	onConnectionStateChange      func(device *BluetoothDevice, status int, newState int)
	onCharacteristicReadRequest  func(device *BluetoothDevice, requestId int, offset int, char *BluetoothGattCharacteristic)
	onCharacteristicWriteRequest func(device *BluetoothDevice, requestId int, char *BluetoothGattCharacteristic, preparedWrite bool, responseNeeded bool, offset int, value []byte)
	onDescriptorReadRequest      func(device *BluetoothDevice, requestId int, offset int, descriptor *BluetoothGattDescriptor)
	onDescriptorWriteRequest     func(device *BluetoothDevice, requestId int, descriptor *BluetoothGattDescriptor, preparedWrite bool, responseNeeded bool, offset int, value []byte)
}

func (c *testGattServerCallback) OnConnectionStateChange(device *BluetoothDevice, status int, newState int) {
	if c.onConnectionStateChange != nil {
		c.onConnectionStateChange(device, status, newState)
	}
}

func (c *testGattServerCallback) OnCharacteristicReadRequest(device *BluetoothDevice, requestId int, offset int, char *BluetoothGattCharacteristic) {
	if c.onCharacteristicReadRequest != nil {
		c.onCharacteristicReadRequest(device, requestId, offset, char)
	}
}

func (c *testGattServerCallback) OnCharacteristicWriteRequest(device *BluetoothDevice, requestId int, char *BluetoothGattCharacteristic, preparedWrite bool, responseNeeded bool, offset int, value []byte) {
	if c.onCharacteristicWriteRequest != nil {
		c.onCharacteristicWriteRequest(device, requestId, char, preparedWrite, responseNeeded, offset, value)
	}
}

func (c *testGattServerCallback) OnDescriptorReadRequest(device *BluetoothDevice, requestId int, offset int, descriptor *BluetoothGattDescriptor) {
	if c.onDescriptorReadRequest != nil {
		c.onDescriptorReadRequest(device, requestId, offset, descriptor)
	}
}

func (c *testGattServerCallback) OnDescriptorWriteRequest(device *BluetoothDevice, requestId int, descriptor *BluetoothGattDescriptor, preparedWrite bool, responseNeeded bool, offset int, value []byte) {
	if c.onDescriptorWriteRequest != nil {
		c.onDescriptorWriteRequest(device, requestId, descriptor, preparedWrite, responseNeeded, offset, value)
	}
}
