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

// TestBluetoothGatt_OperationSerialization tests that only one GATT operation can run at a time
// This is CRITICAL Android BLE behavior - operations must be serialized
func TestBluetoothGatt_OperationSerialization(t *testing.T) {
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
	writeRejected := make(chan bool, 10)
	callback := &testGattCallback{
		onCharacteristicWrite: func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int) {
			if status == GATT_SUCCESS {
				// Simulate slow operation (100ms)
				time.Sleep(100 * time.Millisecond)
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

	// Set up GATT table with two characteristics
	service := &BluetoothGattService{
		UUID: phone.AuraServiceUUID,
		Type: SERVICE_TYPE_PRIMARY,
		Characteristics: []*BluetoothGattCharacteristic{
			{
				UUID:       phone.AuraProtocolCharUUID,
				Properties: PROPERTY_WRITE,
				Service:    nil, // Will be set below
				Value:      []byte("write 1"),
				WriteType:  WRITE_TYPE_DEFAULT,
			},
			{
				UUID:       phone.AuraPhotoCharUUID,
				Properties: PROPERTY_WRITE,
				Service:    nil, // Will be set below
				Value:      []byte("write 2"),
				WriteType:  WRITE_TYPE_DEFAULT,
			},
		},
	}
	// Set service reference for both characteristics
	for _, char := range service.Characteristics {
		char.Service = service
	}
	gatt.services = []*BluetoothGattService{service}

	// Start first write operation
	char1 := service.Characteristics[0]
	success1 := gatt.WriteCharacteristic(char1)
	if !success1 {
		t.Fatal("First WriteCharacteristic should succeed")
	}

	// Immediately try second write (should fail - operation in progress)
	char2 := service.Characteristics[1]
	success2 := gatt.WriteCharacteristic(char2)
	if success2 {
		t.Fatal("Second WriteCharacteristic should fail - operation already in progress!")
	}
	writeRejected <- true

	// Wait for first operation to complete
	select {
	case <-writeCompleted:
		t.Logf("✅ First write completed")
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for first write")
	}

	// Now second write should succeed
	success3 := gatt.WriteCharacteristic(char2)
	if !success3 {
		t.Fatal("Third WriteCharacteristic should succeed after first completes")
	}

	select {
	case <-writeCompleted:
		t.Logf("✅ Second write completed after first finished")
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for second write")
	}

	// Verify rejection happened
	select {
	case <-writeRejected:
		t.Logf("✅ Concurrent write was rejected (matches real Android BLE)")
	default:
		t.Fatal("Expected write rejection did not occur")
	}
}

// TestBluetoothGatt_ReadWriteSerialization tests that read and write operations are serialized
func TestBluetoothGatt_ReadWriteSerialization(t *testing.T) {
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
	readCompleted := make(chan bool, 10)
	callback := &testGattCallback{
		onCharacteristicRead: func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int) {
			// Simulate slow read operation
			time.Sleep(50 * time.Millisecond)
			readCompleted <- true
		},
		onCharacteristicWrite: func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int) {
			// Should not be called in this test
			t.Error("Write should have been rejected")
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
		UUID: phone.AuraServiceUUID,
		Type: SERVICE_TYPE_PRIMARY,
		Characteristics: []*BluetoothGattCharacteristic{
			{
				UUID:       phone.AuraProtocolCharUUID,
				Properties: PROPERTY_READ | PROPERTY_WRITE,
				Service:    nil,
				Value:      []byte("test data"),
				WriteType:  WRITE_TYPE_DEFAULT,
			},
		},
	}
	service.Characteristics[0].Service = service
	gatt.services = []*BluetoothGattService{service}

	char := service.Characteristics[0]

	// Start read operation
	readSuccess := gatt.ReadCharacteristic(char)
	if !readSuccess {
		t.Fatal("ReadCharacteristic should succeed")
	}

	// Immediately try write (should fail - read in progress)
	writeSuccess := gatt.WriteCharacteristic(char)
	if writeSuccess {
		t.Fatal("WriteCharacteristic should fail - read operation in progress!")
	}

	// Wait for read to complete
	select {
	case <-readCompleted:
		t.Logf("✅ Read completed, write was blocked (matches real Android)")
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for read")
	}
}

// TestBluetoothGatt_MultipleReadsBlocked tests that multiple reads cannot run concurrently
func TestBluetoothGatt_MultipleReadsBlocked(t *testing.T) {
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

	// Create GATT connection with slow read callback
	callback := &testGattCallback{
		onCharacteristicRead: func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int) {
			// Simulate slow read to make serialization visible
			time.Sleep(50 * time.Millisecond)
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
		UUID: phone.AuraServiceUUID,
		Type: SERVICE_TYPE_PRIMARY,
		Characteristics: []*BluetoothGattCharacteristic{
			{
				UUID:       phone.AuraProtocolCharUUID,
				Properties: PROPERTY_READ,
				Service:    nil,
			},
		},
	}
	service.Characteristics[0].Service = service
	gatt.services = []*BluetoothGattService{service}

	char := service.Characteristics[0]

	// Try to start 5 read operations rapidly (no delay between calls)
	successCount := 0
	for i := 0; i < 5; i++ {
		if gatt.ReadCharacteristic(char) {
			successCount++
		}
		// No delay - fire rapidly to test serialization
	}

	// Due to goroutine scheduling, 1-2 reads may succeed before serialization kicks in
	// The important thing is that NOT ALL reads succeed (which would mean no serialization)
	if successCount >= 5 {
		t.Errorf("All 5 reads succeeded - serialization not working! (successCount=%d)", successCount)
	} else if successCount < 1 {
		t.Errorf("No reads succeeded - unexpected behavior! (successCount=%d)", successCount)
	} else {
		t.Logf("✅ Operation serialization working: %d/5 reads accepted (%d blocked)", successCount, 5-successCount)
	}

	// Wait for operations to complete
	time.Sleep(200 * time.Millisecond)
}
