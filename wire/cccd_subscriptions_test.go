package wire

import (
	"testing"
	"time"

	"github.com/user/auraphone-blue/util"
	"github.com/user/auraphone-blue/wire/att"
	"github.com/user/auraphone-blue/wire/gatt"
)

// TestCCCDSubscribeUnsubscribe tests the full CCCD subscription flow
func TestCCCDSubscribeUnsubscribe(t *testing.T) {
	util.SetRandom()

	// Create peripheral with a notifiable characteristic
	peripheral := NewWire("peripheral-cccd-1")
	err := peripheral.Start()
	if err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheral.Stop()

	// Build GATT database with a notifiable characteristic
	services := []gatt.Service{
		{
			UUID:    gatt.UUID16(0x1800), // Generic Access
			Primary: true,
			Characteristics: []gatt.Characteristic{
				{
					UUID:       gatt.UUID16(0x2A00), // Device Name
					Properties: gatt.PropRead | gatt.PropNotify,
					Value:      []byte("TestDevice"),
				},
			},
		},
	}
	db, serviceInfos := gatt.BuildAttributeDatabase(services)
	peripheral.attributeDB = db

	// Get the characteristic value handle
	charHandle := serviceInfos[0].CharHandles["002a"]
	if charHandle == 0 {
		t.Fatal("Failed to get characteristic handle")
	}

	// CCCD should be at charHandle + 1
	cccdHandle := charHandle + 1

	// Create central
	central := NewWire("central-cccd-1")
	err = central.Start()
	if err != nil {
		t.Fatalf("Failed to start central: %v", err)
	}
	defer central.Stop()

	// Connect central to peripheral
	err = central.Connect("peripheral-cccd-1")
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	// Wait for MTU negotiation to complete
	err = central.WaitForMTUNegotiation("peripheral-cccd-1", 200*time.Millisecond)
	if err != nil {
		t.Fatalf("Failed to wait for MTU negotiation: %v", err)
	}

	// Verify initial subscription state (should be disabled)
	if peripheral.IsSubscribedToNotifications("central-cccd-1", charHandle) {
		t.Error("Expected notifications to be disabled initially")
	}

	// Central enables notifications by writing to CCCD
	cccdValue := gatt.EncodeCCCDValue(true, false) // Enable notifications
	writeReq := &att.WriteRequest{
		Handle: cccdHandle,
		Value:  cccdValue,
	}

	// Send CCCD write
	err = central.sendATTPacket("peripheral-cccd-1", writeReq)
	if err != nil {
		t.Fatalf("Failed to write CCCD: %v", err)
	}

	// Wait for write response and processing
	time.Sleep(50 * time.Millisecond)

	// Verify subscription is enabled
	if !peripheral.IsSubscribedToNotifications("central-cccd-1", charHandle) {
		t.Error("Expected notifications to be enabled after CCCD write")
	}

	// Central disables notifications
	cccdValue = gatt.EncodeCCCDValue(false, false) // Disable notifications
	writeReq = &att.WriteRequest{
		Handle: cccdHandle,
		Value:  cccdValue,
	}

	err = central.sendATTPacket("peripheral-cccd-1", writeReq)
	if err != nil {
		t.Fatalf("Failed to write CCCD: %v", err)
	}

	// Wait for write response and processing
	time.Sleep(50 * time.Millisecond)

	// Verify subscription is disabled
	if peripheral.IsSubscribedToNotifications("central-cccd-1", charHandle) {
		t.Error("Expected notifications to be disabled after CCCD write")
	}
}

// TestCCCDIndications tests indication subscriptions
func TestCCCDIndications(t *testing.T) {
	util.SetRandom()

	// Create peripheral with an indicatable characteristic
	peripheral := NewWire("peripheral-cccd-2")
	err := peripheral.Start()
	if err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheral.Stop()

	// Build GATT database with an indicatable characteristic
	services := []gatt.Service{
		{
			UUID:    gatt.UUID16(0x1801), // Generic Attribute
			Primary: true,
			Characteristics: []gatt.Characteristic{
				{
					UUID:       gatt.UUID16(0x2A05), // Service Changed
					Properties: gatt.PropIndicate,
					Value:      []byte{0x00, 0x00, 0x00, 0x00},
				},
			},
		},
	}
	db, serviceInfos := gatt.BuildAttributeDatabase(services)
	peripheral.attributeDB = db

	// Get the characteristic value handle
	charHandle := serviceInfos[0].CharHandles["052a"]
	cccdHandle := charHandle + 1

	// Create central
	central := NewWire("central-cccd-2")
	err = central.Start()
	if err != nil {
		t.Fatalf("Failed to start central: %v", err)
	}
	defer central.Stop()

	// Connect
	err = central.Connect("peripheral-cccd-2")
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	// Wait for MTU negotiation to complete
	err = central.WaitForMTUNegotiation("peripheral-cccd-2", 200*time.Millisecond)
	if err != nil {
		t.Fatalf("Failed to wait for MTU negotiation: %v", err)
	}

	// Enable indications
	cccdValue := gatt.EncodeCCCDValue(false, true) // Enable indications
	writeReq := &att.WriteRequest{
		Handle: cccdHandle,
		Value:  cccdValue,
	}

	err = central.sendATTPacket("peripheral-cccd-2", writeReq)
	if err != nil {
		t.Fatalf("Failed to write CCCD: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Verify indications are enabled
	if !peripheral.IsSubscribedToIndications("central-cccd-2", charHandle) {
		t.Error("Expected indications to be enabled")
	}
	if peripheral.IsSubscribedToNotifications("central-cccd-2", charHandle) {
		t.Error("Expected notifications to be disabled")
	}
}

// TestCCCDBothEnabled tests enabling both notifications and indications
func TestCCCDBothEnabled(t *testing.T) {
	util.SetRandom()

	// Create peripheral
	peripheral := NewWire("peripheral-cccd-3")
	err := peripheral.Start()
	if err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheral.Stop()

	// Build GATT database with both notify and indicate
	services := []gatt.Service{
		{
			UUID:    gatt.UUID16(0x1800),
			Primary: true,
			Characteristics: []gatt.Characteristic{
				{
					UUID:       gatt.UUID16(0x2A00),
					Properties: gatt.PropRead | gatt.PropNotify | gatt.PropIndicate,
					Value:      []byte("TestDevice"),
				},
			},
		},
	}
	db, serviceInfos := gatt.BuildAttributeDatabase(services)
	peripheral.attributeDB = db

	charHandle := serviceInfos[0].CharHandles["002a"]
	cccdHandle := charHandle + 1

	// Create central
	central := NewWire("central-cccd-3")
	err = central.Start()
	if err != nil {
		t.Fatalf("Failed to start central: %v", err)
	}
	defer central.Stop()

	// Connect
	err = central.Connect("peripheral-cccd-3")
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	// Wait for MTU negotiation to complete
	err = central.WaitForMTUNegotiation("peripheral-cccd-3", 200*time.Millisecond)
	if err != nil {
		t.Fatalf("Failed to wait for MTU negotiation: %v", err)
	}

	// Enable both notifications and indications
	cccdValue := gatt.EncodeCCCDValue(true, true) // Both enabled
	writeReq := &att.WriteRequest{
		Handle: cccdHandle,
		Value:  cccdValue,
	}

	err = central.sendATTPacket("peripheral-cccd-3", writeReq)
	if err != nil {
		t.Fatalf("Failed to write CCCD: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Verify both are enabled
	if !peripheral.IsSubscribedToNotifications("central-cccd-3", charHandle) {
		t.Error("Expected notifications to be enabled")
	}
	if !peripheral.IsSubscribedToIndications("central-cccd-3", charHandle) {
		t.Error("Expected indications to be enabled")
	}
}

// TestCCCDMultipleConnections tests CCCD state isolation across connections
func TestCCCDMultipleConnections(t *testing.T) {
	util.SetRandom()

	// Create peripheral
	peripheral := NewWire("peripheral-cccd-4")
	err := peripheral.Start()
	if err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheral.Stop()

	// Build GATT database
	services := []gatt.Service{
		{
			UUID:    gatt.UUID16(0x1800),
			Primary: true,
			Characteristics: []gatt.Characteristic{
				{
					UUID:       gatt.UUID16(0x2A00),
					Properties: gatt.PropRead | gatt.PropNotify,
					Value:      []byte("TestDevice"),
				},
			},
		},
	}
	db, serviceInfos := gatt.BuildAttributeDatabase(services)
	peripheral.attributeDB = db

	charHandle := serviceInfos[0].CharHandles["002a"]
	cccdHandle := charHandle + 1

	// Create two centrals
	central1 := NewWire("central-cccd-4a")
	err = central1.Start()
	if err != nil {
		t.Fatalf("Failed to start central1: %v", err)
	}
	defer central1.Stop()

	central2 := NewWire("central-cccd-4b")
	err = central2.Start()
	if err != nil {
		t.Fatalf("Failed to start central2: %v", err)
	}
	defer central2.Stop()

	// Connect both centrals
	err = central1.Connect("peripheral-cccd-4")
	if err != nil {
		t.Fatalf("Failed to connect central1: %v", err)
	}

	err = central2.Connect("peripheral-cccd-4")
	if err != nil {
		t.Fatalf("Failed to connect central2: %v", err)
	}

	// Wait for MTU negotiation to complete for both centrals
	err = central1.WaitForMTUNegotiation("peripheral-cccd-4", 200*time.Millisecond)
	if err != nil {
		t.Fatalf("Failed to wait for central1 MTU negotiation: %v", err)
	}

	err = central2.WaitForMTUNegotiation("peripheral-cccd-4", 200*time.Millisecond)
	if err != nil {
		t.Fatalf("Failed to wait for central2 MTU negotiation: %v", err)
	}

	// Central1 enables notifications
	cccdValue := gatt.EncodeCCCDValue(true, false)
	writeReq := &att.WriteRequest{
		Handle: cccdHandle,
		Value:  cccdValue,
	}
	err = central1.sendATTPacket("peripheral-cccd-4", writeReq)
	if err != nil {
		t.Fatalf("Failed to write CCCD from central1: %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	// Verify: central1 is subscribed, central2 is not
	if !peripheral.IsSubscribedToNotifications("central-cccd-4a", charHandle) {
		t.Error("Expected central1 to be subscribed")
	}
	if peripheral.IsSubscribedToNotifications("central-cccd-4b", charHandle) {
		t.Error("Expected central2 to NOT be subscribed")
	}

	// Central2 enables notifications
	err = central2.sendATTPacket("peripheral-cccd-4", writeReq)
	if err != nil {
		t.Fatalf("Failed to write CCCD from central2: %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	// Verify: both are subscribed
	if !peripheral.IsSubscribedToNotifications("central-cccd-4a", charHandle) {
		t.Error("Expected central1 to be subscribed")
	}
	if !peripheral.IsSubscribedToNotifications("central-cccd-4b", charHandle) {
		t.Error("Expected central2 to be subscribed")
	}

	// Central1 disables notifications
	cccdValue = gatt.EncodeCCCDValue(false, false)
	writeReq.Value = cccdValue
	err = central1.sendATTPacket("peripheral-cccd-4", writeReq)
	if err != nil {
		t.Fatalf("Failed to write CCCD from central1: %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	// Verify: only central2 is subscribed
	if peripheral.IsSubscribedToNotifications("central-cccd-4a", charHandle) {
		t.Error("Expected central1 to NOT be subscribed")
	}
	if !peripheral.IsSubscribedToNotifications("central-cccd-4b", charHandle) {
		t.Error("Expected central2 to be subscribed")
	}
}

// TestCCCDGetSubscribedPeers tests retrieving all subscribed peers
func TestCCCDGetSubscribedPeers(t *testing.T) {
	util.SetRandom()

	// Create peripheral
	peripheral := NewWire("peripheral-cccd-5")
	err := peripheral.Start()
	if err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheral.Stop()

	// Build GATT database
	services := []gatt.Service{
		{
			UUID:    gatt.UUID16(0x1800),
			Primary: true,
			Characteristics: []gatt.Characteristic{
				{
					UUID:       gatt.UUID16(0x2A00),
					Properties: gatt.PropRead | gatt.PropNotify,
					Value:      []byte("TestDevice"),
				},
			},
		},
	}
	db, serviceInfos := gatt.BuildAttributeDatabase(services)
	peripheral.attributeDB = db

	charHandle := serviceInfos[0].CharHandles["002a"]
	cccdHandle := charHandle + 1

	// Initially no subscribers
	subscribers := peripheral.GetSubscribedPeers(charHandle)
	if len(subscribers) != 0 {
		t.Errorf("Expected 0 subscribers initially, got %d", len(subscribers))
	}

	// Create three centrals
	centrals := []*Wire{}
	centralUUIDs := []string{"central-cccd-5a", "central-cccd-5b", "central-cccd-5c"}
	for i, uuid := range centralUUIDs {
		central := NewWire(uuid)
		err = central.Start()
		if err != nil {
			t.Fatalf("Failed to start central %d: %v", i, err)
		}
		defer central.Stop()

		err = central.Connect("peripheral-cccd-5")
		if err != nil {
			t.Fatalf("Failed to connect central %d: %v", i, err)
		}

		// Wait for MTU negotiation to complete
		err = central.WaitForMTUNegotiation("peripheral-cccd-5", 200*time.Millisecond)
		if err != nil {
			t.Fatalf("Failed to wait for central %d MTU negotiation: %v", i, err)
		}

		centrals = append(centrals, central)
	}

	// All centrals enable notifications
	cccdValue := gatt.EncodeCCCDValue(true, false)
	for _, central := range centrals {
		writeReq := &att.WriteRequest{
			Handle: cccdHandle,
			Value:  cccdValue,
		}
		err = central.sendATTPacket("peripheral-cccd-5", writeReq)
		if err != nil {
			t.Fatalf("Failed to write CCCD: %v", err)
		}
	}

	time.Sleep(50 * time.Millisecond)

	// Verify all are subscribed
	subscribers = peripheral.GetSubscribedPeers(charHandle)
	if len(subscribers) != 3 {
		t.Errorf("Expected 3 subscribers, got %d", len(subscribers))
	}
}

// TestCCCDInvalidValue tests error handling for invalid CCCD values
func TestCCCDInvalidValue(t *testing.T) {
	util.SetRandom()

	// Create peripheral
	peripheral := NewWire("peripheral-cccd-6")
	err := peripheral.Start()
	if err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheral.Stop()

	// Build GATT database
	services := []gatt.Service{
		{
			UUID:    gatt.UUID16(0x1800),
			Primary: true,
			Characteristics: []gatt.Characteristic{
				{
					UUID:       gatt.UUID16(0x2A00),
					Properties: gatt.PropRead | gatt.PropNotify,
					Value:      []byte("TestDevice"),
				},
			},
		},
	}
	db, serviceInfos := gatt.BuildAttributeDatabase(services)
	peripheral.attributeDB = db

	charHandle := serviceInfos[0].CharHandles["002a"]
	cccdHandle := charHandle + 1

	// Create central
	central := NewWire("central-cccd-6")
	err = central.Start()
	if err != nil {
		t.Fatalf("Failed to start central: %v", err)
	}
	defer central.Stop()

	// Connect
	err = central.Connect("peripheral-cccd-6")
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Try to write invalid CCCD value (wrong length)
	writeReq := &att.WriteRequest{
		Handle: cccdHandle,
		Value:  []byte{0x01}, // Only 1 byte instead of 2
	}

	err = central.sendATTPacket("peripheral-cccd-6", writeReq)
	if err != nil {
		t.Fatalf("Failed to send CCCD write: %v", err)
	}

	// Wait for error response
	time.Sleep(20 * time.Millisecond)

	// Verify subscription is not enabled
	if peripheral.IsSubscribedToNotifications("central-cccd-6", charHandle) {
		t.Error("Expected subscription to fail with invalid CCCD value")
	}
}
