package kotlin

import (
	"testing"
	"time"

	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/wire"
)

// TestBluetoothGatt_DirectMessageDelivery tests the new HandleGATTMessage pattern
func TestBluetoothGatt_DirectMessageDelivery(t *testing.T) {
	w1 := wire.NewWire("central-uuid")
	w2 := wire.NewWire("peripheral-uuid")

	if err := w1.Start(); err != nil {
		t.Fatalf("Failed to start central wire: %v", err)
	}
	defer w1.Stop()

	if err := w2.Start(); err != nil {
		t.Fatalf("Failed to start peripheral wire: %v", err)
	}
	defer w2.Stop()

	// Create GATT connection
	receivedMessages := make(chan *BluetoothGattCharacteristic, 10)
	callback := &testGattCallback{
		onCharacteristicChanged: func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic) {
			receivedMessages <- char
		},
	}

	device := &BluetoothDevice{
		Address: "peripheral-uuid",
		Name:    "Test Peripheral",
	}
	device.SetWire(w1)

	gatt := device.ConnectGatt(nil, false, callback)

	// Wait for connection
	time.Sleep(200 * time.Millisecond)

	// Manually simulate service discovery (normally done via DiscoverServices)
	service := &BluetoothGattService{
		UUID:            phone.AuraServiceUUID,
		Type:            SERVICE_TYPE_PRIMARY,
		Characteristics: []*BluetoothGattCharacteristic{},
	}
	char := &BluetoothGattCharacteristic{
		UUID:       phone.AuraProtocolCharUUID,
		Properties: PROPERTY_READ | PROPERTY_WRITE | PROPERTY_NOTIFY,
		Service:    service,
	}
	service.Characteristics = append(service.Characteristics, char)
	gatt.services = []*BluetoothGattService{service}
	gatt.notifyingCharacteristics = make(map[string]bool)
	gatt.notifyingCharacteristics[phone.AuraProtocolCharUUID] = true

	// Create a GATT message and deliver it directly (new pattern)
	msg := &wire.GATTMessage{
		Operation:          "notify",
		ServiceUUID:        phone.AuraServiceUUID,
		CharacteristicUUID: phone.AuraProtocolCharUUID,
		Data:               []byte("test notification"),
		SenderUUID:         "peripheral-uuid",
	}

	// Deliver directly via HandleGATTMessage (no inbox polling!)
	gatt.HandleGATTMessage(msg)

	// Check if message was received
	select {
	case receivedChar := <-receivedMessages:
		if string(receivedChar.Value) != "test notification" {
			t.Errorf("Wrong notification data: %s", string(receivedChar.Value))
		}
		t.Logf("✅ Direct message delivery works (no inbox polling)")
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timeout waiting for notification")
	}
}

// TestBluetoothGatt_WriteCharacteristic tests async write without queue
func TestBluetoothGatt_WriteCharacteristic(t *testing.T) {
	w1 := wire.NewWire("central-uuid")
	w2 := wire.NewWire("peripheral-uuid")

	if err := w1.Start(); err != nil {
		t.Fatalf("Failed to start central wire: %v", err)
	}
	defer w1.Stop()

	if err := w2.Start(); err != nil {
		t.Fatalf("Failed to start peripheral wire: %v", err)
	}
	defer w2.Stop()

	// Connect devices
	if err := w1.Connect("peripheral-uuid"); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Create GATT connection
	writeCompleted := make(chan bool, 10)
	callback := &testGattCallback{
		onCharacteristicWrite: func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int) {
			if status == GATT_SUCCESS {
				writeCompleted <- true
			}
		},
	}

	device := &BluetoothDevice{
		Address: "peripheral-uuid",
		Name:    "Test Peripheral",
	}
	device.SetWire(w1)

	gatt := device.ConnectGatt(nil, false, callback)

	// Set up GATT table
	service := &BluetoothGattService{
		UUID:            phone.AuraServiceUUID,
		Type:            SERVICE_TYPE_PRIMARY,
		Characteristics: []*BluetoothGattCharacteristic{},
	}
	char := &BluetoothGattCharacteristic{
		UUID:       phone.AuraProtocolCharUUID,
		Properties: PROPERTY_WRITE,
		Service:    service,
		Value:      []byte("test write data"),
		WriteType:  WRITE_TYPE_DEFAULT,
	}
	service.Characteristics = append(service.Characteristics, char)
	gatt.services = []*BluetoothGattService{service}

	// Write characteristic (new async pattern without write queue)
	success := gatt.WriteCharacteristic(char)
	if !success {
		t.Fatal("WriteCharacteristic returned false")
	}

	// Wait for write callback
	select {
	case <-writeCompleted:
		t.Logf("✅ Async write works (no write queue complexity)")
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for write callback")
	}
}

// testGattCallback is a test implementation of BluetoothGattCallback
type testGattCallback struct {
	onConnectionStateChange func(gatt *BluetoothGatt, status int, newState int)
	onServicesDiscovered    func(gatt *BluetoothGatt, status int)
	onCharacteristicRead    func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int)
	onCharacteristicWrite   func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int)
	onCharacteristicChanged func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic)
}

func (c *testGattCallback) OnConnectionStateChange(gatt *BluetoothGatt, status int, newState int) {
	if c.onConnectionStateChange != nil {
		c.onConnectionStateChange(gatt, status, newState)
	}
}

func (c *testGattCallback) OnServicesDiscovered(gatt *BluetoothGatt, status int) {
	if c.onServicesDiscovered != nil {
		c.onServicesDiscovered(gatt, status)
	}
}

func (c *testGattCallback) OnCharacteristicRead(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int) {
	if c.onCharacteristicRead != nil {
		c.onCharacteristicRead(gatt, char, status)
	}
}

func (c *testGattCallback) OnCharacteristicWrite(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int) {
	if c.onCharacteristicWrite != nil {
		c.onCharacteristicWrite(gatt, char, status)
	}
}

func (c *testGattCallback) OnCharacteristicChanged(gatt *BluetoothGatt, char *BluetoothGattCharacteristic) {
	if c.onCharacteristicChanged != nil {
		c.onCharacteristicChanged(gatt, char)
	}
}
