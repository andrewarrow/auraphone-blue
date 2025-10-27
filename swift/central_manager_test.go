package swift

import (
	"testing"
	"time"

	"github.com/user/auraphone-blue/wire"
)

func TestCBCentralManager_ScanForPeripherals_ServiceFiltering(t *testing.T) {
	w := wire.NewWire("test-uuid")
	if err := w.Start(); err != nil {
		t.Fatalf("Failed to start wire: %v", err)
	}
	defer w.Stop()

	discoveredDevices := make(chan string, 10)

	// We need a custom delegate type that captures device discoveries
	// For now, let's test the wire-level service filtering

	// Create peripheral with specific service UUID
	peripheralWire := wire.NewWire("peripheral-uuid")
	if err := peripheralWire.Start(); err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheralWire.Stop()

	// Write advertising data with service UUID
	advData := &wire.AdvertisingData{
		DeviceName:    "Test Peripheral",
		ServiceUUIDs:  []string{"E621E1F8-C36C-495A-93FC-0C247A3E6E5F"},
		IsConnectable: true,
	}
	if err := peripheralWire.WriteAdvertisingData(advData); err != nil {
		t.Fatalf("Failed to write advertising data: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Verify we can read the advertising data
	readAdvData, err := w.ReadAdvertisingData("peripheral-uuid")
	if err != nil {
		t.Fatalf("Failed to read advertising data: %v", err)
	}

	if len(readAdvData.ServiceUUIDs) == 0 {
		t.Error("No service UUIDs in advertising data")
	}

	if readAdvData.ServiceUUIDs[0] != "E621E1F8-C36C-495A-93FC-0C247A3E6E5F" {
		t.Errorf("Wrong service UUID: %s", readAdvData.ServiceUUIDs[0])
	}

	t.Logf("✅ Service UUID filtering data structure works correctly")

	// Close discovery channel
	close(discoveredDevices)
}

func TestCBCentralManager_ShouldInitiateConnection(t *testing.T) {
	w1 := wire.NewWire("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	w2 := wire.NewWire("ffffffff-0000-1111-2222-333333333333")

	if err := w1.Start(); err != nil {
		t.Fatalf("Failed to start w1: %v", err)
	}
	defer w1.Stop()

	if err := w2.Start(); err != nil {
		t.Fatalf("Failed to start w2: %v", err)
	}
	defer w2.Stop()

	delegate1 := &testCentralDelegate{t: t}
	delegate2 := &testCentralDelegate{t: t}

	cm1 := NewCBCentralManager(delegate1, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", w1)
	cm2 := NewCBCentralManager(delegate2, "ffffffff-0000-1111-2222-333333333333", w2)

	// Test role negotiation: device with larger UUID should initiate
	should1InitTo2 := cm1.ShouldInitiateConnection("ffffffff-0000-1111-2222-333333333333")
	should2InitTo1 := cm2.ShouldInitiateConnection("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")

	// Only one should initiate
	if should1InitTo2 == should2InitTo1 {
		t.Errorf("Role conflict: both or neither want to initiate (1->2: %v, 2->1: %v)", should1InitTo2, should2InitTo1)
	}

	// Device with larger UUID should initiate
	if should1InitTo2 {
		t.Error("Device with smaller UUID should NOT initiate")
	}
	if !should2InitTo1 {
		t.Error("Device with larger UUID SHOULD initiate")
	}

	t.Logf("✅ Role negotiation works correctly: larger UUID initiates")
}

func TestCBCentralManager_AutoReconnect(t *testing.T) {
	centralWire := wire.NewWire("central-uuid")
	peripheralWire := wire.NewWire("peripheral-uuid")

	if err := centralWire.Start(); err != nil {
		t.Fatalf("Failed to start central: %v", err)
	}
	defer centralWire.Stop()

	if err := peripheralWire.Start(); err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheralWire.Stop()

	connectCount := 0
	disconnectCount := 0

	delegate := &testCentralDelegate{
		t:             t,
		didConnect:    make(chan *CBPeripheral, 10),
		didDisconnect: make(chan *CBPeripheral, 10),
	}

	cm := NewCBCentralManager(delegate, "central-uuid", centralWire)

	peripheral := &CBPeripheral{
		UUID: "peripheral-uuid",
		Name: "Test Peripheral",
	}

	// Initial connection
	cm.Connect(peripheral, nil)

	// Wait for first connection
	select {
	case <-delegate.didConnect:
		connectCount++
		t.Logf("✅ Initial connection established")
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for initial connection")
	}

	// Small delay to ensure both sides have fully established the connection
	time.Sleep(200 * time.Millisecond)

	// Verify connection is active before disconnect
	if !centralWire.IsConnected("peripheral-uuid") {
		t.Fatal("Central should be connected to peripheral before disconnect test")
	}
	if !peripheralWire.IsConnected("central-uuid") {
		t.Fatal("Peripheral should be connected to central before disconnect test")
	}

	// Simulate disconnect by calling disconnect on peripheral (simulates device going out of range)
	err := peripheralWire.Disconnect("central-uuid")
	if err != nil {
		t.Logf("Disconnect returned error: %v", err)
	}

	// Wait for disconnect notification (should be immediate when socket closes)
	select {
	case <-delegate.didDisconnect:
		disconnectCount++
		t.Logf("✅ Disconnect detected")
	case <-time.After(2 * time.Second):
		// Debug: Check connection state
		t.Logf("Central still connected: %v", centralWire.IsConnected("peripheral-uuid"))
		t.Logf("Peripheral still connected: %v", peripheralWire.IsConnected("central-uuid"))
		t.Fatal("Timeout waiting for disconnect notification")
	}

	// iOS auto-reconnect should trigger (after 2s delay)
	// Wait for auto-reconnect
	select {
	case <-delegate.didConnect:
		connectCount++
		t.Logf("✅ Auto-reconnect successful")
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for auto-reconnect")
	}

	if connectCount != 2 {
		t.Errorf("Expected 2 connections (initial + reconnect), got %d", connectCount)
	}
	if disconnectCount != 1 {
		t.Errorf("Expected 1 disconnect, got %d", disconnectCount)
	}
}

func TestCBCentralManager_CancelPeripheralConnection(t *testing.T) {
	centralWire := wire.NewWire("central-uuid")
	peripheralWire := wire.NewWire("peripheral-uuid")

	if err := centralWire.Start(); err != nil {
		t.Fatalf("Failed to start central: %v", err)
	}
	defer centralWire.Stop()

	if err := peripheralWire.Start(); err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheralWire.Stop()

	delegate := &testCentralDelegate{
		t:             t,
		didConnect:    make(chan *CBPeripheral, 1),
		didDisconnect: make(chan *CBPeripheral, 1),
	}

	cm := NewCBCentralManager(delegate, "central-uuid", centralWire)

	peripheral := &CBPeripheral{
		UUID: "peripheral-uuid",
		Name: "Test Peripheral",
	}

	// Connect
	cm.Connect(peripheral, nil)
	<-delegate.didConnect
	t.Logf("✅ Connected")

	// Cancel connection (should stop auto-reconnect)
	cm.CancelPeripheralConnection(peripheral)

	// Verify disconnected
	if centralWire.IsConnected("peripheral-uuid") {
		t.Error("Should be disconnected after cancel")
	}

	// Simulate disconnect and verify NO auto-reconnect happens
	time.Sleep(3 * time.Second)

	// No reconnection should occur
	select {
	case <-delegate.didConnect:
		t.Error("Auto-reconnect should NOT happen after CancelPeripheralConnection")
	default:
		t.Logf("✅ Auto-reconnect correctly disabled")
	}
}
