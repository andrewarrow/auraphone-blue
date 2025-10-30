package kotlin

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/util"
	"github.com/user/auraphone-blue/wire"
)

// TestBluetoothGatt_OperationQueue tests that GATT operations are queued and executed serially
// This is CRITICAL Android BLE behavior - operations MUST be serialized, not rejected
func TestBluetoothGatt_OperationQueue(t *testing.T) {
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

	// Create peripheral with GATT server (REALISTIC: peripheral must advertise services)
	callback2 := &testGattServerCallback{}
	gattServer := NewBluetoothGattServer("peripheral-uuid", callback2, "Test Device", w2)

	service := &BluetoothGattService{
		UUID: phone.AuraServiceUUID,
		Type: SERVICE_TYPE_PRIMARY,
		Characteristics: []*BluetoothGattCharacteristic{
			{
				UUID:       phone.AuraProtocolCharUUID,
				Properties: PROPERTY_WRITE,
			},
		},
	}
	gattServer.AddService(service)

	// Connect devices
	if err := w1.Connect("peripheral-uuid"); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Track completed operations
	var completedOps atomic.Int32
	writeCompleted := make(chan int, 10)
	servicesDiscovered := make(chan bool, 1)
	callback := &testGattCallback{
		onServicesDiscovered: func(gatt *BluetoothGatt, status int) {
			servicesDiscovered <- (status == GATT_SUCCESS)
		},
		onCharacteristicWrite: func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int) {
			if status == GATT_SUCCESS {
				// Simulate slow callback processing (50ms)
				time.Sleep(50 * time.Millisecond)
				completed := completedOps.Add(1)
				writeCompleted <- int(completed)
			}
		},
	}

	device := &BluetoothDevice{
		Address: "peripheral-uuid",
		Name:    "Test Peripheral",
	}
	device.SetWire(w1)

	gatt := device.ConnectGatt(nil, false, callback)

	// REALISTIC: Must discover services before writing (real Android BLE requirement)
	gatt.DiscoverServices()

	// Wait for discovery
	select {
	case success := <-servicesDiscovered:
		if !success {
			t.Fatal("Service discovery failed")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Timeout waiting for service discovery")
	}

	// Get characteristic reference from discovered services
	char := gatt.GetCharacteristic(phone.AuraServiceUUID, phone.AuraProtocolCharUUID)
	if char == nil {
		t.Fatal("Failed to get characteristic after discovery")
	}

	// Rapidly queue 5 write operations (no delay between calls)
	// OLD BEHAVIOR: Would reject 4 of them (only 1 succeeds)
	// NEW BEHAVIOR: All 5 should be queued and executed serially
	t.Logf("Queueing 5 write operations rapidly...")
	successCount := 0
	for i := 0; i < 5; i++ {
		if gatt.WriteCharacteristic(char) {
			successCount++
		}
		// No delay - fire rapidly to test queueing
	}

	// All operations should be accepted (queued)
	if successCount != 5 {
		t.Errorf("Expected all 5 writes to be queued, but only %d were accepted", successCount)
	} else {
		t.Logf("✅ All 5 operations queued (not rejected)")
	}

	// Wait for all operations to complete
	// They should complete serially (one at a time)
	receivedOrder := []int{}
	for i := 0; i < 5; i++ {
		select {
		case completed := <-writeCompleted:
			receivedOrder = append(receivedOrder, completed)
			t.Logf("   Operation %d completed", completed)
		case <-time.After(5 * time.Second):
			t.Fatalf("Timeout waiting for operation %d (only %d completed)", i+1, len(receivedOrder))
		}
	}

	// Verify all operations completed
	if len(receivedOrder) != 5 {
		t.Errorf("Expected 5 completed operations, got %d", len(receivedOrder))
	}

	// Verify they completed in order (1, 2, 3, 4, 5)
	for i, order := range receivedOrder {
		expected := i + 1
		if order != expected {
			t.Errorf("Operation completed out of order: expected %d, got %d", expected, order)
		}
	}

	t.Logf("✅ All 5 operations completed serially in correct order")
}

// TestBluetoothGatt_QueueMixedOperations tests that different operation types are queued together
func TestBluetoothGatt_QueueMixedOperations(t *testing.T) {
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

	// Connect devices
	if err := w1.Connect("peripheral-uuid"); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Track completed operations in order
	completedOps := make(chan string, 10)
	callback := &testGattCallback{
		onCharacteristicWrite: func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int) {
			time.Sleep(30 * time.Millisecond) // Simulate slow operation
			completedOps <- "write"
		},
		onCharacteristicRead: func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int) {
			time.Sleep(30 * time.Millisecond) // Simulate slow operation
			completedOps <- "read"
		},
		onDescriptorWrite: func(gatt *BluetoothGatt, descriptor *BluetoothGattDescriptor, status int) {
			time.Sleep(30 * time.Millisecond) // Simulate slow operation
			completedOps <- "writeDescriptor"
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
				Properties: PROPERTY_READ | PROPERTY_WRITE | PROPERTY_NOTIFY,
				Service:    nil,
				Value:      []byte("test data"),
				WriteType:  WRITE_TYPE_DEFAULT,
			},
		},
	}
	service.Characteristics[0].Service = service

	// Add CCCD descriptor
	cccd := &BluetoothGattDescriptor{
		UUID:           CCCD_UUID,
		Value:          ENABLE_NOTIFICATION_VALUE,
		Permissions:    PERMISSION_READ | PERMISSION_WRITE,
		Characteristic: service.Characteristics[0],
	}
	service.Characteristics[0].Descriptors = append(service.Characteristics[0].Descriptors, cccd)

	gatt.services = []*BluetoothGattService{service}

	char := service.Characteristics[0]

	// Queue mixed operations rapidly
	t.Logf("Queueing mixed operations: write -> read -> writeDescriptor")
	operations := []string{"write", "read", "writeDescriptor"}

	// Write
	if !gatt.WriteCharacteristic(char) {
		t.Fatal("Write should be queued")
	}

	// Read (should queue, not reject)
	if !gatt.ReadCharacteristic(char) {
		t.Fatal("Read should be queued")
	}

	// Write descriptor (should queue, not reject)
	if !gatt.WriteDescriptor(cccd) {
		t.Fatal("WriteDescriptor should be queued")
	}

	// Wait for all operations to complete in order
	for i := 0; i < 3; i++ {
		select {
		case opType := <-completedOps:
			expected := operations[i]
			if opType != expected {
				t.Errorf("Operation %d: expected %s, got %s", i+1, expected, opType)
			}
			t.Logf("   Operation %d (%s) completed", i+1, opType)
		case <-time.After(3 * time.Second):
			t.Fatalf("Timeout waiting for operation %d", i+1)
		}
	}

	t.Logf("✅ Mixed operations queued and executed serially in correct order")
}

// TestBluetoothGatt_QueueStressTest tests queue under high load
func TestBluetoothGatt_QueueStressTest(t *testing.T) {
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

	// Connect devices
	if err := w1.Connect("peripheral-uuid"); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Track completion
	var completedCount atomic.Int32
	callback := &testGattCallback{
		onCharacteristicWrite: func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int) {
			completedCount.Add(1)
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
				Properties: PROPERTY_WRITE,
				Service:    nil,
				Value:      []byte("test data"),
				WriteType:  WRITE_TYPE_DEFAULT,
			},
		},
	}
	service.Characteristics[0].Service = service
	gatt.services = []*BluetoothGattService{service}

	char := service.Characteristics[0]

	// Queue many operations rapidly (stress test)
	const numOps = 50
	t.Logf("Queueing %d write operations...", numOps)

	queuedCount := 0
	for i := 0; i < numOps; i++ {
		if gatt.WriteCharacteristic(char) {
			queuedCount++
		}
	}

	t.Logf("✅ Queued %d/%d operations", queuedCount, numOps)

	// Wait for all operations to complete (with generous timeout)
	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			completed := completedCount.Load()
			t.Logf("   Progress: %d/%d completed", completed, queuedCount)
			if completed == int32(queuedCount) {
				t.Logf("✅ All %d operations completed successfully", queuedCount)
				return
			}
		case <-timeout:
			completed := completedCount.Load()
			t.Fatalf("Timeout: only %d/%d operations completed", completed, queuedCount)
		}
	}
}
