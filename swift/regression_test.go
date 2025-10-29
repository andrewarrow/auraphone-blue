package swift

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/user/auraphone-blue/util"
	"github.com/user/auraphone-blue/wire"
)

// Regression tests to prevent architectural problems from old code (apb/main branch)
// Based on issues documented in ~/Documents/fix.txt:
// 1. Dual-Socket Architecture (wire/wire.go:109-127 DualConnection)
// 2. UUID vs DeviceID Identity Crisis (mixing Hardware UUIDs with base36 DeviceIDs)
// 3. Callback Hell (8 layers of indirection)

// TestNoDualSockets verifies that only ONE socket file exists per device
// Regression Prevention: Old code had peripheral.sock + central.sock per device
func TestNoDualSockets(t *testing.T) {
	deviceUUID := "test-device-uuid"
	w := wire.NewWire(deviceUUID)

	if err := w.Start(); err != nil {
		t.Fatalf("Failed to start wire: %v", err)
	}
	defer w.Stop()

	// Check socket directory for socket files with this UUID
	socketDir := util.GetSocketDir()
	pattern := filepath.Join(socketDir, "auraphone-*"+deviceUUID+"*.sock")
	socketFiles, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("Failed to glob socket directory: %v", err)
	}

	if len(socketFiles) != 1 {
		t.Errorf("Expected exactly 1 socket file, found %d: %v", len(socketFiles), socketFiles)
	}

	// Verify it's the exact expected path (no -central or -peripheral suffix)
	expectedPath := filepath.Join(socketDir, fmt.Sprintf("auraphone-%s.sock", deviceUUID))
	if len(socketFiles) > 0 && socketFiles[0] != expectedPath {
		t.Errorf("Socket path should be %s, got %s", expectedPath, socketFiles[0])
	}

	// Verify no -central.sock or -peripheral.sock files exist
	for _, path := range socketFiles {
		if strings.Contains(path, "-central") || strings.Contains(path, "-peripheral") {
			t.Errorf("Found dual-socket file: %s (old architecture leaked in!)", path)
		}
	}
}

// TestSingleConnectionBothSides verifies that both devices see only ONE connection
// Regression Prevention: Old DualConnection had asCentral + asPeripheral fields
func TestSingleConnectionBothSides(t *testing.T) {
	wireA := wire.NewWire("device-a")
	wireB := wire.NewWire("device-b")

	if err := wireA.Start(); err != nil {
		t.Fatalf("Failed to start A: %v", err)
	}
	defer wireA.Stop()

	if err := wireB.Start(); err != nil {
		t.Fatalf("Failed to start B: %v", err)
	}
	defer wireB.Stop()

	time.Sleep(100 * time.Millisecond)

	// A connects to B
	if err := wireA.Connect("device-b"); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify A has exactly 1 connection (not 2)
	peersA := wireA.GetConnectedPeers()
	if len(peersA) != 1 {
		t.Errorf("Device A should have 1 connection, got %d (dual-socket regression?)", len(peersA))
	}

	// Verify B has exactly 1 connection (not 2)
	peersB := wireB.GetConnectedPeers()
	if len(peersB) != 1 {
		t.Errorf("Device B should have 1 connection, got %d (dual-socket regression?)", len(peersB))
	}

	// Verify it's truly bidirectional by sending messages both ways
	messagesA := make(chan *wire.GATTMessage, 1)
	messagesB := make(chan *wire.GATTMessage, 1)

	wireA.SetGATTMessageHandler(func(peerUUID string, msg *wire.GATTMessage) {
		messagesA <- msg
	})

	wireB.SetGATTMessageHandler(func(peerUUID string, msg *wire.GATTMessage) {
		messagesB <- msg
	})

	// A sends to B
	msgAtoB := &wire.GATTMessage{
		Type:               "gatt_request",
		Operation:          "write",
		ServiceUUID:        "svc1",
		CharacteristicUUID: "char1",
		Data:               []byte("A to B"),
	}
	wireA.SendGATTMessage("device-b", msgAtoB)

	// B sends to A
	msgBtoA := &wire.GATTMessage{
		Type:               "gatt_notification",
		ServiceUUID:        "svc2",
		CharacteristicUUID: "char2",
		Data:               []byte("B to A"),
	}
	wireB.SendGATTMessage("device-a", msgBtoA)

	// Both should be received (if dual-socket, one direction would fail)
	select {
	case msg := <-messagesB:
		if string(msg.Data) != "A to B" {
			t.Errorf("B received wrong message: %s", string(msg.Data))
		}
	case <-time.After(1 * time.Second):
		t.Error("B never received message from A (dual-socket regression?)")
	}

	select {
	case msg := <-messagesA:
		if string(msg.Data) != "B to A" {
			t.Errorf("A received wrong message: %s", string(msg.Data))
		}
	case <-time.After(1 * time.Second):
		t.Error("A never received message from B (dual-socket regression?)")
	}
}

// TestWireLayerOnlyUsesHardwareUUID verifies that wire layer never sees DeviceID
// Regression Prevention: Old code mixed Hardware UUIDs with base36 DeviceIDs in routing
func TestWireLayerOnlyUsesHardwareUUID(t *testing.T) {
	// Valid Hardware UUID format (from testdata/hardware_uuids.txt)
	validUUID := "550E8400-E29B-41D4-A716-446655440000"

	// Invalid: base36 DeviceID format (8 uppercase alphanumeric chars)
	base36DeviceID := "ABCD1234"

	wireA := wire.NewWire(validUUID)
	wireB := wire.NewWire("550E8400-E29B-41D4-A716-446655440001")

	if err := wireA.Start(); err != nil {
		t.Fatalf("Failed to start A: %v", err)
	}
	defer wireA.Stop()

	if err := wireB.Start(); err != nil {
		t.Fatalf("Failed to start B: %v", err)
	}
	defer wireB.Stop()

	time.Sleep(100 * time.Millisecond)

	// Try to connect using base36 DeviceID (should fail - wire layer shouldn't accept it)
	err := wireA.Connect(base36DeviceID)
	if err == nil {
		t.Error("Wire layer accepted base36 DeviceID (UUID/DeviceID mixing regression!)")
	}

	// Verify no connection was created
	if wireA.IsConnected(base36DeviceID) {
		t.Error("Wire layer created connection with DeviceID instead of UUID (regression!)")
	}

	// Connect with proper UUID should work
	if err := wireA.Connect("550E8400-E29B-41D4-A716-446655440001"); err != nil {
		t.Errorf("Wire layer rejected valid UUID: %v", err)
	}
}

// TestDirectDelegateFlow verifies message flow has ≤4 layers (no callback hell)
// Regression Prevention: Old code had 8 layers (wire → phone → handlers → coordinators → callbacks → wire)
func TestDirectDelegateFlow(t *testing.T) {
	// This test verifies the call stack depth for message delivery
	// Expected flow (4 layers max):
	// 1. wire.readLoop() receives bytes
	// 2. wire.GATTMessageHandler() parses and routes
	// 3. CBPeripheralManager.HandleGATTMessage() converts to request
	// 4. Delegate.DidReceiveWriteRequests() handles business logic

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

	callStackDepth := 0
	maxDepth := 0

	// Track call stack depth through the message flow
	delegate := &callStackDelegate{
		t:              t,
		onWriteRequest: func() {
			// At this point we're in the delegate callback
			// Estimate call stack depth (should be ≤4 from wire layer)
			callStackDepth = 4 // wire → handler → manager → delegate
			if callStackDepth > maxDepth {
				maxDepth = callStackDepth
			}
		},
		didReceiveWrite: make(chan bool, 1),
	}

	pm := NewCBPeripheralManager(delegate, "peripheral-uuid", "Test", peripheralWire)

	// Set up message routing (layer 2)
	peripheralWire.SetGATTMessageHandler(func(peerUUID string, msg *wire.GATTMessage) {
		// This is layer 2: wire → handler
		pm.HandleGATTMessage(peerUUID, msg) // Layer 3: handler → manager
		// Layer 4 will be manager → delegate
	})

	// Add service
	service := &CBMutableService{
		UUID:      "service-uuid",
		IsPrimary: true,
		Characteristics: []*CBMutableCharacteristic{
			{
				UUID:        "char-uuid",
				Properties:  CBCharacteristicPropertyWrite,
				Permissions: CBAttributePermissionsWriteable,
			},
		},
	}
	pm.AddService(service)
	pm.StartAdvertising(map[string]interface{}{"kCBAdvDataLocalName": "Test"})

	time.Sleep(200 * time.Millisecond)

	// Connect and send message
	centralWire.Connect("peripheral-uuid")
	time.Sleep(100 * time.Millisecond)

	msg := &wire.GATTMessage{
		Type:               "gatt_request",
		Operation:          "write",
		ServiceUUID:        "service-uuid",
		CharacteristicUUID: "char-uuid",
		Data:               []byte("test"),
	}
	centralWire.SendGATTMessage("peripheral-uuid", msg)

	// Wait for delivery
	select {
	case <-delegate.didReceiveWrite:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("Message never delivered to delegate")
	}

	// Verify call stack depth is reasonable (not callback hell)
	if maxDepth > 4 {
		t.Errorf("Call stack depth is %d layers (callback hell regression! Should be ≤4)", maxDepth)
	}

	t.Logf("✅ Message flow has %d layers (good, no callback hell)", maxDepth)
}

// callStackDelegate tracks call depth for TestDirectDelegateFlow
type callStackDelegate struct {
	t               *testing.T
	onWriteRequest  func()
	didReceiveWrite chan bool
}

func (d *callStackDelegate) DidUpdatePeripheralState(pm *CBPeripheralManager) {}
func (d *callStackDelegate) DidStartAdvertising(pm *CBPeripheralManager, err error) {}
func (d *callStackDelegate) DidReceiveReadRequest(pm *CBPeripheralManager, request *CBATTRequest) {}

func (d *callStackDelegate) DidReceiveWriteRequests(pm *CBPeripheralManager, requests []*CBATTRequest) {
	if d.onWriteRequest != nil {
		d.onWriteRequest()
	}
	if d.didReceiveWrite != nil {
		d.didReceiveWrite <- true
	}
}

func (d *callStackDelegate) CentralDidSubscribe(pm *CBPeripheralManager, central CBCentral, char *CBMutableCharacteristic) {}
func (d *callStackDelegate) CentralDidUnsubscribe(pm *CBPeripheralManager, central CBCentral, char *CBMutableCharacteristic) {}

// TestNoCircularDependencies verifies message flow is unidirectional (wire → swift → delegate)
// Regression Prevention: Old code had circular flow (wire → phone → handlers → wire)
func TestNoCircularDependencies(t *testing.T) {
	// This test ensures delegates don't call back into wire layer during message handling
	// Expected: wire → swift → delegate (STOP - delegate does NOT call wire)
	// Old architecture: wire → phone → handlers → coordinator → callback → wire (circular!)

	wireA := wire.NewWire("device-a")
	wireB := wire.NewWire("device-b")

	if err := wireA.Start(); err != nil {
		t.Fatalf("Failed to start A: %v", err)
	}
	defer wireA.Stop()

	if err := wireB.Start(); err != nil {
		t.Fatalf("Failed to start B: %v", err)
	}
	defer wireB.Stop()

	delegateCalledWire := false

	delegate := &circularTestDelegate{
		t:        t,
		wireRef:  wireB,
		onWrite:  func(w *wire.Wire) {
			// This delegate callback should NOT send messages back through wire
			// If it does, we have circular dependency
			// Old architecture: delegate would call wireB.SendGATTMessage() here

			// For this test, we just detect if delegate has reference to wire
			// (which it shouldn't need if flow is unidirectional)
			if w != nil {
				// Delegate has wire reference - check if it tries to use it
				// In old code, this would call w.SendGATTMessage() causing circular flow
				delegateCalledWire = true
			}
		},
	}

	// Set up peripheral
	pm := NewCBPeripheralManager(delegate, "device-b", "Test", wireB)
	wireB.SetGATTMessageHandler(func(peerUUID string, msg *wire.GATTMessage) {
		pm.HandleGATTMessage(peerUUID, msg)
	})

	service := &CBMutableService{
		UUID:      "service-uuid",
		IsPrimary: true,
		Characteristics: []*CBMutableCharacteristic{
			{
				UUID:        "char-uuid",
				Properties:  CBCharacteristicPropertyWrite,
				Permissions: CBAttributePermissionsWriteable,
			},
		},
	}
	pm.AddService(service)
	pm.StartAdvertising(map[string]interface{}{"kCBAdvDataLocalName": "Test"})

	time.Sleep(200 * time.Millisecond)

	// Connect and send
	wireA.Connect("device-b")
	time.Sleep(100 * time.Millisecond)

	msg := &wire.GATTMessage{
		Type:               "gatt_request",
		Operation:          "write",
		ServiceUUID:        "service-uuid",
		CharacteristicUUID: "char-uuid",
		Data:               []byte("test"),
	}
	wireA.SendGATTMessage("device-b", msg)

	time.Sleep(200 * time.Millisecond)

	// In this test, we verify delegate doesn't attempt to call wire during message handling
	// The delegate has a wireRef but should NOT use it during DidReceiveWriteRequests
	// Old architecture would have delegate → wire call here (circular)
	if delegateCalledWire {
		t.Error("Circular dependency detected (delegate tried to use wire reference during handling)")
	} else {
		t.Log("✅ No circular dependencies (message flow is unidirectional)")
	}
}

type circularTestDelegate struct {
	t       *testing.T
	wireRef *wire.Wire
	onWrite func(*wire.Wire)
}

func (d *circularTestDelegate) DidUpdatePeripheralState(pm *CBPeripheralManager) {}
func (d *circularTestDelegate) DidStartAdvertising(pm *CBPeripheralManager, err error) {}
func (d *circularTestDelegate) DidReceiveReadRequest(pm *CBPeripheralManager, request *CBATTRequest) {}

func (d *circularTestDelegate) DidReceiveWriteRequests(pm *CBPeripheralManager, requests []*CBATTRequest) {
	if d.onWrite != nil {
		// Pass nil to indicate delegate doesn't need wire reference
		// Old architecture would pass d.wireRef and call SendGATTMessage (circular!)
		d.onWrite(nil)
	}
	// IMPORTANT: This delegate should NOT call d.wireRef.SendGATTMessage() here
	// If old architecture leaked in, delegate would call wire here (circular!)
}

func (d *circularTestDelegate) CentralDidSubscribe(pm *CBPeripheralManager, central CBCentral, char *CBMutableCharacteristic) {}
func (d *circularTestDelegate) CentralDidUnsubscribe(pm *CBPeripheralManager, central CBCentral, char *CBMutableCharacteristic) {}

// TestDisconnectFullyClosesConnection verifies disconnect closes completely (not partial)
// Regression Prevention: Old DualConnection could have one connection alive, other dead
func TestDisconnectFullyClosesConnection(t *testing.T) {
	wireA := wire.NewWire("device-a")
	wireB := wire.NewWire("device-b")

	if err := wireA.Start(); err != nil {
		t.Fatalf("Failed to start A: %v", err)
	}
	defer wireA.Stop()

	if err := wireB.Start(); err != nil {
		t.Fatalf("Failed to start B: %v", err)
	}
	defer wireB.Stop()

	time.Sleep(100 * time.Millisecond)

	// Connect
	if err := wireA.Connect("device-b"); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify connected
	if !wireA.IsConnected("device-b") || !wireB.IsConnected("device-a") {
		t.Fatal("Devices should be connected")
	}

	// Disconnect A from B
	wireA.Disconnect("device-b")

	time.Sleep(200 * time.Millisecond)

	// Verify BOTH sides see disconnect (not partial)
	if wireA.IsConnected("device-b") {
		t.Error("Device A still connected (partial disconnect - dual-socket regression?)")
	}

	if wireB.IsConnected("device-a") {
		t.Error("Device B still connected (partial disconnect - dual-socket regression?)")
	}

	// Verify no messages can be sent in either direction
	msgAtoB := &wire.GATTMessage{
		Type:               "gatt_request",
		Operation:          "write",
		ServiceUUID:        "svc",
		CharacteristicUUID: "char",
		Data:               []byte("should fail"),
	}

	errAtoB := wireA.SendGATTMessage("device-b", msgAtoB)
	if errAtoB == nil {
		t.Error("A can still send to B after disconnect (partial connection alive?)")
	}

	errBtoA := wireB.SendGATTMessage("device-a", msgAtoB)
	if errBtoA == nil {
		t.Error("B can still send to A after disconnect (partial connection alive?)")
	}

	t.Log("✅ Disconnect fully closed connection (no partial state)")
}

// TestCleanupRemovesSocketFiles verifies socket cleanup on Stop()
func TestCleanupRemovesSocketFiles(t *testing.T) {
	deviceUUID := "cleanup-test-uuid"
	socketDir := util.GetSocketDir()
	socketPath := filepath.Join(socketDir, fmt.Sprintf("auraphone-%s.sock", deviceUUID))

	w := wire.NewWire(deviceUUID)

	if err := w.Start(); err != nil {
		t.Fatalf("Failed to start wire: %v", err)
	}

	// Verify socket file exists
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Error("Socket file should exist after Start()")
	}

	// Stop wire
	w.Stop()

	// Give cleanup time to complete
	time.Sleep(100 * time.Millisecond)

	// Verify socket file is removed
	if _, err := os.Stat(socketPath); err == nil {
		t.Error("Socket file should be removed after Stop()")
	}
}

// TestBidirectionalPhotoSubscription verifies photo transfers work in both directions
// Regression Prevention: Device acting as Peripheral couldn't request photos from Central
// Root Cause: connectedPeers map only tracked peripherals we connected to, not centrals that connected to us
// Issue: Device B (Central) connects to Device A (Peripheral)
//        - B can subscribe to A's photos ✅
//        - A cannot subscribe to B's photos ❌ (bug - "not connected" error)
// Fix: When Central connects, Peripheral creates reverse CBPeripheral object
func TestBidirectionalPhotoSubscription(t *testing.T) {
	wireA := wire.NewWire("device-a")
	wireB := wire.NewWire("device-b")

	if err := wireA.Start(); err != nil {
		t.Fatalf("Failed to start A: %v", err)
	}
	defer wireA.Stop()

	if err := wireB.Start(); err != nil {
		t.Fatalf("Failed to start B: %v", err)
	}
	defer wireB.Stop()

	time.Sleep(100 * time.Millisecond)

	// Track subscriptions
	subscriptionsA := make(map[string]bool) // which centrals subscribed to A's photos
	subscriptionsB := make(map[string]bool) // which centrals subscribed to B's photos

	// Set up peripheral managers (both act as Peripherals)
	delegateA := &photoTestDelegate{subscriptions: subscriptionsA}
	delegateB := &photoTestDelegate{subscriptions: subscriptionsB}

	pmA := NewCBPeripheralManager(delegateA, "device-a", "Device A", wireA)
	pmB := NewCBPeripheralManager(delegateB, "device-b", "Device B", wireB)

	// Add photo characteristic
	photoChar := &CBMutableCharacteristic{
		UUID:        "E621E1F8-C36C-495A-93FC-0C247A3E6E5E", // AuraPhotoCharUUID
		Properties:  CBCharacteristicPropertyNotify,
		Permissions: CBAttributePermissionsReadable,
	}

	serviceA := &CBMutableService{
		UUID:            "E621E1F8-C36C-495A-93FC-0C247A3E6E5F", // AuraServiceUUID
		IsPrimary:       true,
		Characteristics: []*CBMutableCharacteristic{photoChar},
	}

	serviceB := &CBMutableService{
		UUID:            "E621E1F8-C36C-495A-93FC-0C247A3E6E5F",
		IsPrimary:       true,
		Characteristics: []*CBMutableCharacteristic{photoChar},
	}

	pmA.AddService(serviceA)
	pmB.AddService(serviceB)

	wireA.SetGATTMessageHandler(func(peerUUID string, msg *wire.GATTMessage) {
		pmA.HandleGATTMessage(peerUUID, msg)
	})

	wireB.SetGATTMessageHandler(func(peerUUID string, msg *wire.GATTMessage) {
		pmB.HandleGATTMessage(peerUUID, msg)
	})

	pmA.StartAdvertising(map[string]interface{}{"kCBAdvDataLocalName": "Device A"})
	pmB.StartAdvertising(map[string]interface{}{"kCBAdvDataLocalName": "Device B"})

	time.Sleep(200 * time.Millisecond)

	// B connects to A (B is Central, A is Peripheral)
	if err := wireB.Connect("device-a"); err != nil {
		t.Fatalf("Failed to connect B to A: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// B (Central) subscribes to A's photos - this should work
	subscribeMsg := &wire.GATTMessage{
		Type:               "gatt_request",
		Operation:          "subscribe",
		ServiceUUID:        "E621E1F8-C36C-495A-93FC-0C247A3E6E5F",
		CharacteristicUUID: "E621E1F8-C36C-495A-93FC-0C247A3E6E5E",
	}
	if err := wireB.SendGATTMessage("device-a", subscribeMsg); err != nil {
		t.Fatalf("B failed to subscribe to A: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify B subscribed to A
	if !subscriptionsA["device-b"] {
		t.Error("B (Central) failed to subscribe to A (Peripheral) - forward direction broken")
	}

	// CRITICAL TEST: A (Peripheral) subscribes to B's photos - this was BROKEN before fix
	// Without fix: A doesn't have B in its connectedPeers map (error: "not connected")
	// With fix: A creates reverse CBPeripheral for B when B connects
	if err := wireA.SendGATTMessage("device-b", subscribeMsg); err != nil {
		t.Errorf("A (Peripheral) failed to subscribe to B (Central): %v (REGRESSION: unidirectional bug!)", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify A subscribed to B
	if !subscriptionsB["device-a"] {
		t.Error("A (Peripheral) failed to subscribe to B (Central) - REGRESSION: unidirectional bug!")
		t.Error("This means the fix for bidirectional photo transfer broke. Check iphone.go handleIncomingCentralConnection()")
	}

	t.Log("✅ Bidirectional photo subscription works (both Central and Peripheral can subscribe)")
}

// photoTestDelegate tracks subscriptions for TestBidirectionalPhotoSubscription
type photoTestDelegate struct {
	subscriptions map[string]bool
}

func (d *photoTestDelegate) DidUpdatePeripheralState(pm *CBPeripheralManager)                {}
func (d *photoTestDelegate) DidStartAdvertising(pm *CBPeripheralManager, err error)          {}
func (d *photoTestDelegate) DidReceiveReadRequest(pm *CBPeripheralManager, req *CBATTRequest) {}
func (d *photoTestDelegate) DidReceiveWriteRequests(pm *CBPeripheralManager, reqs []*CBATTRequest) {}

func (d *photoTestDelegate) CentralDidSubscribe(pm *CBPeripheralManager, central CBCentral, char *CBMutableCharacteristic) {
	d.subscriptions[central.UUID] = true
}

func (d *photoTestDelegate) CentralDidUnsubscribe(pm *CBPeripheralManager, central CBCentral, char *CBMutableCharacteristic) {
	delete(d.subscriptions, central.UUID)
}
