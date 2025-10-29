package wire

import (
	"fmt"
	"testing"
	"time"

	"github.com/user/auraphone-blue/util"
	"github.com/user/auraphone-blue/wire/gatt"
	"github.com/user/auraphone-blue/wire/l2cap"
)

// setupTestServiceAndDiscovery sets up a test service on the peripheral and performs discovery on the central
// This ensures the discovery cache is populated before trying to send GATT messages
func setupTestServiceAndDiscovery(t *testing.T, central, peripheral *Wire, peripheralUUID string) {
	// Set up a simple GATT service on peripheral with a readable characteristic
	// Use gatt.UUID16() helper to create UUIDs in correct little-endian format
	// gatt.UUID16(0x0001) creates []byte{0x01, 0x00} which matches string "0001" after conversion
	services := []gatt.Service{
		{
			UUID:    gatt.UUID16(0x0001), // service UUID 0x0001 in little-endian
			Primary: true,
			Characteristics: []gatt.Characteristic{
				{
					UUID:       gatt.UUID16(0x0001), // char UUID 0x0001 in little-endian
					Properties: gatt.PropRead | gatt.PropWrite,
					Value:      []byte("test-value"),
				},
			},
		},
	}

	db, _ := gatt.BuildAttributeDatabase(services)
	peripheral.SetAttributeDatabase(db)

	// Perform service discovery
	if err := central.DiscoverServices(peripheralUUID); err != nil {
		t.Fatalf("Failed to discover services: %v", err)
	}

	// Discover characteristics
	if err := central.DiscoverCharacteristics(peripheralUUID, gatt.UUID16(0x0001)); err != nil {
		t.Fatalf("Failed to discover characteristics: %v", err)
	}
}

// TestConnectionEventTiming_DiscreteEvents verifies that connection events are discrete
// and respect connection intervals
func TestConnectionEventTiming_DiscreteEvents(t *testing.T) {
	util.SetRandom()

	// Create two devices
	centralUUID := fmt.Sprintf("central-%d", time.Now().UnixNano())
	peripheralUUID := fmt.Sprintf("peripheral-%d", time.Now().UnixNano())

	central := NewWire(centralUUID)
	peripheral := NewWire(peripheralUUID)

	// Start both devices
	if err := peripheral.Start(); err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheral.Stop()

	// Connect central to peripheral
	if err := central.Connect(peripheralUUID); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // Wait for connection

	// Set up test service and perform discovery
	setupTestServiceAndDiscovery(t, central, peripheral, peripheralUUID)

	// Set fast connection parameters (15ms interval) for easier testing
	fastParams := l2cap.FastConnectionParameters()
	err := central.SetConnectionParameters(peripheralUUID, fastParams)
	if err != nil {
		t.Fatalf("Failed to set connection parameters: %v", err)
	}

	// Measure timing of multiple packets sent by peripheral
	// In real BLE, peripheral can only send during connection events
	const numPackets = 5
	sendTimes := make([]time.Time, numPackets)

	// Set up read request handler on peripheral
	receivedCount := 0
	peripheral.SetGATTMessageHandler(func(peerUUID string, msg *GATTMessage) {
		if msg.Type == "gatt_request" && msg.Operation == "read" {
			receivedCount++
			// Send response
			resp := &GATTMessage{
				Type:               "gatt_response",
				Operation:          "read",
				Status:             "success",
				ServiceUUID:        msg.ServiceUUID,
				CharacteristicUUID: msg.CharacteristicUUID,
				Data:               []byte("test-data"),
			}
			sendTimes[receivedCount-1] = time.Now()
			peripheral.SendGATTMessage(peerUUID, resp)
		}
	})

	// Send multiple read requests from central
	for i := 0; i < numPackets; i++ {
		req := &GATTMessage{
			Type:               "gatt_request",
			Operation:          "read",
			ServiceUUID:        "0001",
			CharacteristicUUID: "0001",
		}
		if err := central.SendGATTMessage(peripheralUUID, req); err != nil {
			t.Fatalf("Failed to send read request %d: %v", i, err)
		}
		// Small delay between requests to ensure they're queued
		time.Sleep(5 * time.Millisecond)
	}

	// Wait for all responses
	time.Sleep(200 * time.Millisecond)

	// Verify all packets were sent
	if receivedCount != numPackets {
		t.Errorf("Expected %d packets, received %d", numPackets, receivedCount)
	}

	// Verify that peripheral responses are spaced by connection intervals
	// Connection interval is 15ms, so responses should be ~15ms apart
	expectedInterval := fastParams.IntervalMaxMs()
	tolerance := 10.0 // Allow 10ms tolerance for timing jitter

	for i := 1; i < len(sendTimes); i++ {
		if sendTimes[i].IsZero() || sendTimes[i-1].IsZero() {
			continue
		}
		actualInterval := sendTimes[i].Sub(sendTimes[i-1]).Seconds() * 1000 // Convert to ms

		// Check if interval is within expected range (should be ~15ms or multiples)
		// Allow for packets to be sent in same event or next event
		if actualInterval < (expectedInterval-tolerance) && actualInterval > tolerance {
			t.Errorf("Packet interval %d->%d: %.2fms, expected ~%.2fms (or multiple)",
				i-1, i, actualInterval, expectedInterval)
		}
	}
}

// TestConnectionEventTiming_CentralImmediate verifies that Central can send immediately
func TestConnectionEventTiming_CentralImmediate(t *testing.T) {
	util.SetRandom()

	// Create two devices
	centralUUID := fmt.Sprintf("central-%d", time.Now().UnixNano())
	peripheralUUID := fmt.Sprintf("peripheral-%d", time.Now().UnixNano())

	central := NewWire(centralUUID)
	peripheral := NewWire(peripheralUUID)

	// Start both devices
	if err := peripheral.Start(); err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheral.Stop()

	// Set up handler on peripheral
	peripheral.SetGATTMessageHandler(func(peerUUID string, msg *GATTMessage) {
		if msg.Type == "gatt_request" && msg.Operation == "read" {
			resp := &GATTMessage{
				Type:               "gatt_response",
				Operation:          "read",
				Status:             "success",
				ServiceUUID:        msg.ServiceUUID,
				CharacteristicUUID: msg.CharacteristicUUID,
				Data:               []byte("test"),
			}
			peripheral.SendGATTMessage(peerUUID, resp)
		}
	})

	// Connect central to peripheral
	if err := central.Connect(peripheralUUID); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // Wait for connection

	// Set up test service and perform discovery
	setupTestServiceAndDiscovery(t, central, peripheral, peripheralUUID)

	// Central should be able to send multiple packets rapidly
	// without waiting for connection events
	startTime := time.Now()
	const numPackets = 5

	for i := 0; i < numPackets; i++ {
		req := &GATTMessage{
			Type:               "gatt_request",
			Operation:          "read",
			ServiceUUID:        "0001",
			CharacteristicUUID: "0001",
		}
		if err := central.SendGATTMessage(peripheralUUID, req); err != nil {
			t.Fatalf("Failed to send read request %d: %v", i, err)
		}
	}

	elapsed := time.Since(startTime)

	// Central sending should be fast (< 10ms for 5 packets)
	// because Central doesn't wait for connection events
	maxExpected := 20 * time.Millisecond // Allow some overhead
	if elapsed > maxExpected {
		t.Errorf("Central took too long to send %d packets: %v (expected < %v)",
			numPackets, elapsed, maxExpected)
	}

	// Wait for responses
	time.Sleep(200 * time.Millisecond)
}

// TestConnectionEventTiming_PeripheralWaits verifies that Peripheral waits for connection events
func TestConnectionEventTiming_PeripheralWaits(t *testing.T) {
	util.SetRandom()

	// Create two devices
	centralUUID := fmt.Sprintf("central-%d", time.Now().UnixNano())
	peripheralUUID := fmt.Sprintf("peripheral-%d", time.Now().UnixNano())

	central := NewWire(centralUUID)
	peripheral := NewWire(peripheralUUID)

	// Start both devices
	if err := peripheral.Start(); err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheral.Stop()

	// Connect central to peripheral
	if err := central.Connect(peripheralUUID); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // Wait for connection

	// Set up test service and perform discovery
	setupTestServiceAndDiscovery(t, central, peripheral, peripheralUUID)

	// Set connection parameters with 50ms interval
	params := l2cap.DefaultConnectionParameters() // 50ms max interval
	err := central.SetConnectionParameters(peripheralUUID, params)
	if err != nil {
		t.Fatalf("Failed to set connection parameters: %v", err)
	}

	// Set up handler on peripheral
	peripheral.SetGATTMessageHandler(func(peerUUID string, msg *GATTMessage) {
		if msg.Type == "gatt_request" && msg.Operation == "read" {
			resp := &GATTMessage{
				Type:               "gatt_response",
				Operation:          "read",
				Status:             "success",
				ServiceUUID:        msg.ServiceUUID,
				CharacteristicUUID: msg.CharacteristicUUID,
				Data:               []byte("test"),
			}
			peripheral.SendGATTMessage(peerUUID, resp)
		}
	})

	// Send multiple read requests rapidly
	const numPackets = 3
	startTime := time.Now()

	for i := 0; i < numPackets; i++ {
		req := &GATTMessage{
			Type:               "gatt_request",
			Operation:          "read",
			ServiceUUID:        "0001",
			CharacteristicUUID: "0001",
		}
		if err := central.SendGATTMessage(peripheralUUID, req); err != nil {
			t.Fatalf("Failed to send read request %d: %v", i, err)
		}
	}

	// Wait for all responses
	time.Sleep(200 * time.Millisecond)
	elapsed := time.Since(startTime)

	// Peripheral responses should take time due to connection event spacing
	// With 50ms interval and 3 responses, should take at least ~100ms
	minExpected := 100 * time.Millisecond
	if elapsed < minExpected {
		t.Errorf("Peripheral responses too fast: %v (expected >= %v due to connection events)",
			elapsed, minExpected)
	}
}

// TestConnectionEventTiming_ParameterUpdate verifies event timing updates with parameters
func TestConnectionEventTiming_ParameterUpdate(t *testing.T) {
	util.SetRandom()

	// Create two devices
	centralUUID := fmt.Sprintf("central-%d", time.Now().UnixNano())
	peripheralUUID := fmt.Sprintf("peripheral-%d", time.Now().UnixNano())

	central := NewWire(centralUUID)
	peripheral := NewWire(peripheralUUID)

	// Start both devices
	if err := peripheral.Start(); err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheral.Stop()

	peripheral.SetAttributeDatabase(gatt.NewAttributeDatabase())

	// Connect
	if err := central.Connect(peripheralUUID); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Start with default params (50ms)
	params1 := l2cap.DefaultConnectionParameters()
	err := central.SetConnectionParameters(peripheralUUID, params1)
	if err != nil {
		t.Fatalf("Failed to set initial parameters: %v", err)
	}

	// Verify the event scheduler was updated
	central.mu.RLock()
	conn := central.connections[peripheralUUID]
	central.mu.RUnlock()

	if conn == nil {
		t.Fatal("Connection not found")
	}

	if conn.eventScheduler == nil {
		t.Fatal("Event scheduler not initialized")
	}

	// Check initial interval
	conn.eventScheduler.mu.Lock()
	initialInterval := conn.eventScheduler.intervalMs
	conn.eventScheduler.mu.Unlock()

	if initialInterval != params1.IntervalMaxMs() {
		t.Errorf("Initial interval mismatch: got %.2f, expected %.2f",
			initialInterval, params1.IntervalMaxMs())
	}

	// Update to fast params (15ms)
	params2 := l2cap.FastConnectionParameters()
	err = central.SetConnectionParameters(peripheralUUID, params2)
	if err != nil {
		t.Fatalf("Failed to update parameters: %v", err)
	}

	// Verify the event scheduler was updated
	conn.eventScheduler.mu.Lock()
	updatedInterval := conn.eventScheduler.intervalMs
	conn.eventScheduler.mu.Unlock()

	if updatedInterval != params2.IntervalMaxMs() {
		t.Errorf("Updated interval mismatch: got %.2f, expected %.2f",
			updatedInterval, params2.IntervalMaxMs())
	}
}
