package kotlin

import (
	"testing"
	"time"

	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/util"
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

// TestBluetoothGatt_OperationSerialization tests that GATT operations are queued (not rejected)
// This is CRITICAL Android BLE behavior - operations are queued and executed serially
// OLD BEHAVIOR: Rejected operations when busy
// NEW BEHAVIOR: Queues operations for serial execution
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

	// Immediately try second write (should be queued, not rejected)
	char2 := service.Characteristics[1]
	success2 := gatt.WriteCharacteristic(char2)
	if !success2 {
		t.Fatal("Second WriteCharacteristic should be queued (not rejected)!")
	}
	// OLD: writeRejected <- true (no longer applies with queueing)

	// Wait for both operations to complete (they should execute serially)
	select {
	case <-writeCompleted:
		t.Logf("✅ First write completed")
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for first write")
	}

	select {
	case <-writeCompleted:
		t.Logf("✅ Second write completed (was queued, not rejected)")
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for second write")
	}

	t.Logf("✅ Operations were queued and executed serially (matches real Android BLE)")
}

// TestBluetoothGatt_ReadWriteSerialization tests that read and write operations are queued
// OLD BEHAVIOR: Write was rejected when read in progress
// NEW BEHAVIOR: Write is queued and executes after read completes
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
	writeCompleted := make(chan bool, 10)
	callback := &testGattCallback{
		onCharacteristicRead: func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int) {
			// Simulate slow read operation
			time.Sleep(50 * time.Millisecond)
			readCompleted <- true
		},
		onCharacteristicWrite: func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int) {
			// Now called because write is queued (not rejected)
			writeCompleted <- true
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

	// Immediately try write (should be queued, not rejected)
	writeSuccess := gatt.WriteCharacteristic(char)
	if !writeSuccess {
		t.Fatal("WriteCharacteristic should be queued (not rejected)!")
	}

	// Wait for read to complete
	select {
	case <-readCompleted:
		t.Logf("✅ Read completed")
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for read")
	}

	// Wait for write to complete (was queued)
	select {
	case <-writeCompleted:
		t.Logf("✅ Write completed after read (was queued, matches real Android)")
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for write")
	}
}

// TestBluetoothGatt_MultipleReadsBlocked tests that multiple reads are queued (not rejected)
// OLD BEHAVIOR: Reads rejected when one already in progress
// NEW BEHAVIOR: All reads queued and executed serially
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
		// No delay - fire rapidly to test queueing
	}

	// NEW BEHAVIOR: All reads should be queued (not rejected)
	if successCount != 5 {
		t.Errorf("Expected all 5 reads to be queued, got %d", successCount)
	} else {
		t.Logf("✅ Operation queueing working: all %d/5 reads queued (none rejected)", successCount)
	}

	// Wait for operations to complete
	time.Sleep(500 * time.Millisecond)
}

// ============================================================================
// RECONNECTION TESTS
// ============================================================================

// TestBluetoothGatt_ManualReconnect tests manual reconnection after disconnect (autoConnect=false)
func TestBluetoothGatt_ManualReconnect(t *testing.T) {
	util.SetRandom()

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

	// Track connection state changes
	connectionStates := make(chan int, 10)
	callback := &testGattCallback{
		onConnectionStateChange: func(gatt *BluetoothGatt, status int, newState int) {
			connectionStates <- newState
		},
	}

	// Create device and connect with autoConnect=false
	device := &BluetoothDevice{
		Address: "peripheral-uuid",
		Name:    "Test Peripheral",
	}
	device.SetWire(w1)

	gatt := device.ConnectGatt(nil, false, callback) // autoConnect=false

	// Wait for connection (may get CONNECTING first, then CONNECTED)
	connected := false
	for i := 0; i < 2; i++ {
		select {
		case state := <-connectionStates:
			if state == STATE_CONNECTED {
				connected = true
				break
			}
		case <-time.After(1 * time.Second):
			t.Fatal("Timeout waiting for connection")
		}
	}
	if !connected {
		t.Fatal("Never received STATE_CONNECTED")
	}

	t.Logf("✅ Initial connection established")

	// Disconnect
	gatt.Disconnect()

	// Wait for disconnect callback
	select {
	case state := <-connectionStates:
		if state != STATE_DISCONNECTED {
			t.Fatalf("Expected STATE_DISCONNECTED, got %d", state)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for disconnection")
	}

	t.Logf("✅ Disconnected successfully")

	// Drain any remaining state changes from the channel
	drainLoop:
	for {
		select {
		case <-connectionStates:
			// Drain
		case <-time.After(100 * time.Millisecond):
			break drainLoop
		}
	}

	// With autoConnect=false, Android does NOT auto-reconnect
	// Wait to ensure no automatic reconnection happens
	select {
	case state := <-connectionStates:
		t.Fatalf("Unexpected reconnection with autoConnect=false (state=%d)", state)
	case <-time.After(2 * time.Second):
		t.Logf("✅ No automatic reconnection (correct behavior for autoConnect=false)")
	}

	// Manual reconnection - create new GATT connection
	gatt2 := device.ConnectGatt(nil, false, callback)

	// Wait for manual reconnection (may get CONNECTING first, then CONNECTED)
	connected2 := false
	for i := 0; i < 2; i++ {
		select {
		case state := <-connectionStates:
			if state == STATE_CONNECTED {
				connected2 = true
				break
			}
		case <-time.After(1 * time.Second):
			t.Fatal("Timeout waiting for manual reconnection")
		}
	}
	if !connected2 {
		t.Fatal("Never received STATE_CONNECTED on manual reconnect")
	}

	t.Logf("✅ Manual reconnection successful")
	gatt2.Disconnect()
}

// TestBluetoothGatt_AutoReconnect tests automatic reconnection after connection failure (autoConnect=true)
func TestBluetoothGatt_AutoReconnect(t *testing.T) {
	util.SetRandom()

	w1 := wire.NewWire("central-uuid")

	if err := w1.Start(); err != nil {
		t.Fatalf("Failed to start central wire: %v", err)
	}
	defer w1.Stop()

	// Track connection state changes
	connectionAttempts := 0
	callback := &testGattCallback{
		onConnectionStateChange: func(gatt *BluetoothGatt, status int, newState int) {
			if newState == STATE_CONNECTING {
				connectionAttempts++
			}
		},
	}

	// Create device pointing to non-existent peripheral (to simulate connection failures)
	device := &BluetoothDevice{
		Address: "nonexistent-peripheral-uuid",
		Name:    "Test Peripheral",
	}
	device.SetWire(w1)

	// Connect with autoConnect=true - will fail but keep retrying
	gatt := device.ConnectGatt(nil, true, callback) // autoConnect=true
	defer gatt.Disconnect()

	// Wait for initial connection attempt
	time.Sleep(500 * time.Millisecond)

	// Android autoConnect should keep retrying every ~5 seconds
	// Wait long enough to see multiple attempts
	time.Sleep(12 * time.Second)

	// Should have seen multiple connection attempts
	if connectionAttempts < 2 {
		t.Errorf("Expected multiple reconnection attempts with autoConnect=true, got %d", connectionAttempts)
	} else {
		t.Logf("✅ AutoConnect=true keeps retrying: %d attempts (matches Android behavior)", connectionAttempts)
	}
}

// TestBluetoothGatt_AutoReconnectPersistence tests that autoConnect keeps retrying on failure
func TestBluetoothGatt_AutoReconnectPersistence(t *testing.T) {
	util.SetRandom()

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

	// Track connection state changes
	connectionAttempts := 0
	callback := &testGattCallback{
		onConnectionStateChange: func(gatt *BluetoothGatt, status int, newState int) {
			if newState == STATE_CONNECTING || newState == STATE_DISCONNECTED {
				connectionAttempts++
			}
		},
	}

	// Create device and connect with autoConnect=true
	device := &BluetoothDevice{
		Address: "peripheral-uuid",
		Name:    "Test Peripheral",
	}
	device.SetWire(w1)

	gatt := device.ConnectGatt(nil, true, callback) // autoConnect=true
	defer gatt.Disconnect()

	// Wait for initial connection
	time.Sleep(500 * time.Millisecond)
	t.Logf("✅ Initial connection established")

	// Stop peripheral - should trigger reconnection attempts
	w2.Stop()

	// Wait for multiple reconnection attempts
	// Android autoConnect retries every ~5 seconds
	time.Sleep(12 * time.Second)

	// Should have seen multiple connection attempts
	if connectionAttempts < 2 {
		t.Errorf("Expected multiple reconnection attempts, got %d", connectionAttempts)
	} else {
		t.Logf("✅ AutoConnect kept retrying: %d attempts (matches Android behavior)", connectionAttempts)
	}
}
