package kotlin

import (
	"testing"
	"time"

	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/wire"
)

// TestMessageRequeueWithMultipleConnections reproduces the bug where notifications
// are consumed by the wrong GATT connection and lost.
//
// Scenario:
// - Central device has 3 active connections (to Device A, B, and C)
// - Each connection has its own BluetoothGatt polling the shared messageQueue
// - When Device B sends a notification, it might be consumed by Device A's or C's goroutine
// - Without requeuing, the message is lost and Device B's goroutine never sees it
//
// This test verifies that messages are correctly requeued when consumed by the wrong connection.
func TestMessageRequeueWithMultipleConnections(t *testing.T) {
	phone.CleanupDataDir()

	config := wire.PerfectSimulationConfig()

	// Create 4 devices (simulating the real bug scenario)
	// deviceCentral connects to all 3 peripherals
	deviceCentralUUID := "FFFF0000-0000-0000-0000-000000000000" // Largest UUID = Central
	deviceAUUID := "AAAA0000-0000-0000-0000-000000000000"       // Peripheral A
	deviceBUUID := "BBBB0000-0000-0000-0000-000000000000"       // Peripheral B
	deviceCUUID := "CCCC0000-0000-0000-0000-000000000000"       // Peripheral C

	wireCentral := wire.NewWireWithPlatform(deviceCentralUUID, wire.PlatformAndroid, "Central Device", config)
	wireA := wire.NewWireWithPlatform(deviceAUUID, wire.PlatformAndroid, "Peripheral A", config)
	wireB := wire.NewWireWithPlatform(deviceBUUID, wire.PlatformAndroid, "Peripheral B", config)
	wireC := wire.NewWireWithPlatform(deviceCUUID, wire.PlatformAndroid, "Peripheral C", config)

	defer wireCentral.Cleanup()
	defer wireA.Cleanup()
	defer wireB.Cleanup()
	defer wireC.Cleanup()

	// Initialize all devices
	if err := wireCentral.InitializeDevice(); err != nil {
		t.Fatalf("Failed to initialize central: %v", err)
	}
	if err := wireA.InitializeDevice(); err != nil {
		t.Fatalf("Failed to initialize device A: %v", err)
	}
	if err := wireB.InitializeDevice(); err != nil {
		t.Fatalf("Failed to initialize device B: %v", err)
	}
	if err := wireC.InitializeDevice(); err != nil {
		t.Fatalf("Failed to initialize device C: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Central connects to all 3 peripherals
	if err := wireCentral.Connect(deviceAUUID); err != nil {
		t.Fatalf("Failed to connect to device A: %v", err)
	}
	if err := wireCentral.Connect(deviceBUUID); err != nil {
		t.Fatalf("Failed to connect to device B: %v", err)
	}
	if err := wireCentral.Connect(deviceCUUID); err != nil {
		t.Fatalf("Failed to connect to device C: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Create 3 BluetoothGatt objects (one for each connection)
	serviceUUID := phone.AuraServiceUUID
	charUUID := phone.AuraProtocolCharUUID

	// Subscribe from all connections
	if err := wireCentral.SubscribeCharacteristic(deviceAUUID, serviceUUID, charUUID); err != nil {
		t.Fatalf("Failed to subscribe to device A: %v", err)
	}
	if err := wireCentral.SubscribeCharacteristic(deviceBUUID, serviceUUID, charUUID); err != nil {
		t.Fatalf("Failed to subscribe to device B: %v", err)
	}
	if err := wireCentral.SubscribeCharacteristic(deviceCUUID, serviceUUID, charUUID); err != nil {
		t.Fatalf("Failed to subscribe to device C: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Create 3 GATT clients simulating real scenario
	gattA := &BluetoothGatt{
		wire:       wireCentral,
		remoteUUID: deviceAUUID,
	}
	gattB := &BluetoothGatt{
		wire:       wireCentral,
		remoteUUID: deviceBUUID,
	}
	gattC := &BluetoothGatt{
		wire:       wireCentral,
		remoteUUID: deviceCUUID,
	}

	// Track which GATT received which message
	receivedByA := make(chan string, 10)
	receivedByB := make(chan string, 10)
	receivedByC := make(chan string, 10)

	// Set up callbacks for each GATT
	gattA.callback = &MockGattCallback{
		onCharacteristicChanged: func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic) {
			receivedByA <- string(char.Value)
		},
	}
	gattB.callback = &MockGattCallback{
		onCharacteristicChanged: func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic) {
			receivedByB <- string(char.Value)
		},
	}
	gattC.callback = &MockGattCallback{
		onCharacteristicChanged: func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic) {
			receivedByC <- string(char.Value)
		},
	}

	// Add service/characteristics to each GATT
	for i, gatt := range []*BluetoothGatt{gattA, gattB, gattC} {
		service := &BluetoothGattService{
			UUID:            serviceUUID,
			Characteristics: []*BluetoothGattCharacteristic{},
		}
		char := &BluetoothGattCharacteristic{
			UUID:       charUUID,
			Properties: PROPERTY_READ | PROPERTY_WRITE | PROPERTY_NOTIFY,
			Service:    service,
		}
		service.Characteristics = append(service.Characteristics, char)
		gatt.services = []*BluetoothGattService{service}

		// Manually enable notifications (wire subscription already done above)
		gatt.notifyingCharacteristics = make(map[string]bool)
		gatt.notifyingCharacteristics[charUUID] = true

		// Verify the characteristic is accessible
		foundChar := gatt.GetCharacteristic(serviceUUID, charUUID)
		if foundChar == nil {
			t.Fatalf("Failed to get characteristic for GATT %d", i)
		}
		t.Logf("GATT %d configured: remoteUUID=%s, char=%s, notifying=%v",
			i, gatt.remoteUUID[:8], foundChar.UUID[:8], gatt.notifyingCharacteristics[charUUID])
	}

	// Start listening on all 3 GATTs concurrently (reproducing the race condition)
	gattA.StartListening()
	gattB.StartListening()
	gattC.StartListening()

	time.Sleep(50 * time.Millisecond)

	// Each peripheral sends a notification
	notifA := []byte("notification from device A")
	notifB := []byte("notification from device B")
	notifC := []byte("notification from device C")

	if err := wireA.NotifyCharacteristic(deviceCentralUUID, serviceUUID, charUUID, notifA); err != nil {
		t.Fatalf("Failed to send notification from A: %v", err)
	}
	if err := wireB.NotifyCharacteristic(deviceCentralUUID, serviceUUID, charUUID, notifB); err != nil {
		t.Fatalf("Failed to send notification from B: %v", err)
	}
	if err := wireC.NotifyCharacteristic(deviceCentralUUID, serviceUUID, charUUID, notifC); err != nil {
		t.Fatalf("Failed to send notification from C: %v", err)
	}

	// Wait for messages to be processed
	time.Sleep(500 * time.Millisecond)

	// Check message queue status for debugging
	t.Logf("Checking if notifications were delivered...")

	// Verify each GATT received ONLY its own notification
	select {
	case msg := <-receivedByA:
		if msg != string(notifA) {
			t.Errorf("GATT A received wrong message: got '%s', want '%s'", msg, string(notifA))
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("GATT A did not receive notification from device A (message was lost due to race condition)")
	}

	select {
	case msg := <-receivedByB:
		if msg != string(notifB) {
			t.Errorf("GATT B received wrong message: got '%s', want '%s'", msg, string(notifB))
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("GATT B did not receive notification from device B (message was lost due to race condition)")
	}

	select {
	case msg := <-receivedByC:
		if msg != string(notifC) {
			t.Errorf("GATT C received wrong message: got '%s', want '%s'", msg, string(notifC))
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("GATT C did not receive notification from device C (message was lost due to race condition)")
	}

	// Verify no GATT received extra messages
	select {
	case msg := <-receivedByA:
		t.Errorf("GATT A received unexpected extra message: '%s'", msg)
	default:
		// Good - no extra messages
	}

	select {
	case msg := <-receivedByB:
		t.Errorf("GATT B received unexpected extra message: '%s'", msg)
	default:
		// Good - no extra messages
	}

	select {
	case msg := <-receivedByC:
		t.Errorf("GATT C received unexpected extra message: '%s'", msg)
	default:
		// Good - no extra messages
	}

	// Stop listening
	gattA.StopListening()
	gattB.StopListening()
	gattC.StopListening()
}

// MockGattCallback implements BluetoothGattCallback for testing
type MockGattCallback struct {
	onConnectionStateChange func(gatt *BluetoothGatt, status int, newState int)
	onServicesDiscovered    func(gatt *BluetoothGatt, status int)
	onCharacteristicRead    func(gatt *BluetoothGatt, characteristic *BluetoothGattCharacteristic, status int)
	onCharacteristicWrite   func(gatt *BluetoothGatt, characteristic *BluetoothGattCharacteristic, status int)
	onCharacteristicChanged func(gatt *BluetoothGatt, characteristic *BluetoothGattCharacteristic)
}

func (m *MockGattCallback) OnConnectionStateChange(gatt *BluetoothGatt, status int, newState int) {
	if m.onConnectionStateChange != nil {
		m.onConnectionStateChange(gatt, status, newState)
	}
}

func (m *MockGattCallback) OnServicesDiscovered(gatt *BluetoothGatt, status int) {
	if m.onServicesDiscovered != nil {
		m.onServicesDiscovered(gatt, status)
	}
}

func (m *MockGattCallback) OnCharacteristicRead(gatt *BluetoothGatt, characteristic *BluetoothGattCharacteristic, status int) {
	if m.onCharacteristicRead != nil {
		m.onCharacteristicRead(gatt, characteristic, status)
	}
}

func (m *MockGattCallback) OnCharacteristicWrite(gatt *BluetoothGatt, characteristic *BluetoothGattCharacteristic, status int) {
	if m.onCharacteristicWrite != nil {
		m.onCharacteristicWrite(gatt, characteristic, status)
	}
}

func (m *MockGattCallback) OnCharacteristicChanged(gatt *BluetoothGatt, characteristic *BluetoothGattCharacteristic) {
	if m.onCharacteristicChanged != nil {
		m.onCharacteristicChanged(gatt, characteristic)
	}
}
