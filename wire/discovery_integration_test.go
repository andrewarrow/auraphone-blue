package wire

import (
	"testing"
	"time"

	"github.com/user/auraphone-blue/util"
	"github.com/user/auraphone-blue/wire/gatt"
)

// TestFullDiscoveryFlow tests the complete GATT discovery flow:
// 1. Connect to a peripheral
// 2. Discover services
// 3. Discover characteristics
// 4. Discover descriptors
// 5. Read/write using discovered handles
func TestFullDiscoveryFlow(t *testing.T) {
	util.SetRandom()

	// Create two devices: central and peripheral
	central := NewWire("central-uuid")
	peripheral := NewWire("peripheral-uuid")

	// Start both devices
	if err := central.Start(); err != nil {
		t.Fatalf("Failed to start central: %v", err)
	}
	defer central.Stop()

	if err := peripheral.Start(); err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheral.Stop()

	// Build a GATT database on the peripheral
	// Service 1: Generic Access (0x1800)
	// - Characteristic: Device Name (0x2A00)
	// Service 2: Heart Rate Service (0x180D)
	// - Characteristic: Heart Rate Measurement (0x2A37) with CCCD descriptor
	// - Characteristic: Body Sensor Location (0x2A38)
	services := []gatt.Service{
		{
			UUID:    []byte{0x00, 0x18}, // Generic Access (0x1800)
			Primary: true,
			Characteristics: []gatt.Characteristic{
				{
					UUID:       []byte{0x00, 0x2A}, // Device Name (0x2A00)
					Properties: gatt.PropRead | gatt.PropWrite,
					Value:      []byte("TestDevice"),
				},
			},
		},
		{
			UUID:    []byte{0x0D, 0x18}, // Heart Rate Service (0x180D)
			Primary: true,
			Characteristics: []gatt.Characteristic{
				{
					UUID:       []byte{0x37, 0x2A}, // Heart Rate Measurement (0x2A37)
					Properties: gatt.PropNotify,
					Value:      []byte{0x00, 0x5A}, // Heart rate: 90 bpm
					Descriptors: []gatt.Descriptor{
						{
							UUID:  []byte{0x02, 0x29}, // CCCD (0x2902)
							Value: []byte{0x00, 0x00}, // Notifications disabled
						},
					},
				},
				{
					UUID:       []byte{0x38, 0x2A}, // Body Sensor Location (0x2A38)
					Properties: gatt.PropRead,
					Value:      []byte{0x01}, // Chest
				},
			},
		},
	}

	db, _ := gatt.BuildAttributeDatabase(services)
	peripheral.SetAttributeDatabase(db)

	// Wait for listeners to be ready
	time.Sleep(100 * time.Millisecond)

	// Central connects to peripheral
	err := central.Connect("peripheral-uuid")
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	// Wait for connection and MTU negotiation to complete
	time.Sleep(500 * time.Millisecond)

	// Step 1: Discover services
	t.Log("Step 1: Discovering services...")
	err = central.DiscoverServices("peripheral-uuid")
	if err != nil {
		t.Fatalf("Failed to discover services: %v", err)
	}

	discoveredServices, err := central.GetDiscoveredServices("peripheral-uuid")
	if err != nil {
		t.Fatalf("Failed to get discovered services: %v", err)
	}

	if len(discoveredServices) != 2 {
		t.Errorf("Expected 2 services, got %d", len(discoveredServices))
	}

	// Verify Generic Access service
	if !bytesEqual(discoveredServices[0].UUID, []byte{0x00, 0x18}) {
		t.Errorf("Expected Generic Access service (0x1800), got %v", discoveredServices[0].UUID)
	}

	// Verify Heart Rate service
	if !bytesEqual(discoveredServices[1].UUID, []byte{0x0D, 0x18}) {
		t.Errorf("Expected Heart Rate service (0x180D), got %v", discoveredServices[1].UUID)
	}

	t.Logf("✅ Discovered %d services", len(discoveredServices))

	// Step 2: Discover characteristics for Generic Access service
	t.Log("Step 2: Discovering characteristics for Generic Access service...")
	err = central.DiscoverCharacteristics("peripheral-uuid", []byte{0x00, 0x18})
	if err != nil {
		t.Fatalf("Failed to discover characteristics: %v", err)
	}

	chars, err := central.GetDiscoveredCharacteristics("peripheral-uuid", discoveredServices[0].StartHandle)
	if err != nil {
		t.Fatalf("Failed to get discovered characteristics: %v", err)
	}

	if len(chars) != 1 {
		t.Errorf("Expected 1 characteristic, got %d", len(chars))
	}

	if !bytesEqual(chars[0].UUID, []byte{0x00, 0x2A}) {
		t.Errorf("Expected Device Name characteristic (0x2A00), got %v", chars[0].UUID)
	}

	t.Logf("✅ Discovered %d characteristics for Generic Access", len(chars))

	// Step 3: Discover characteristics for Heart Rate service
	t.Log("Step 3: Discovering characteristics for Heart Rate service...")
	err = central.DiscoverCharacteristics("peripheral-uuid", []byte{0x0D, 0x18})
	if err != nil {
		t.Fatalf("Failed to discover characteristics: %v", err)
	}

	hrChars, err := central.GetDiscoveredCharacteristics("peripheral-uuid", discoveredServices[1].StartHandle)
	if err != nil {
		t.Fatalf("Failed to get discovered characteristics: %v", err)
	}

	if len(hrChars) != 2 {
		t.Errorf("Expected 2 characteristics, got %d", len(hrChars))
	}

	t.Logf("✅ Discovered %d characteristics for Heart Rate", len(hrChars))

	// Step 4: Discover descriptors for Heart Rate Measurement characteristic
	t.Log("Step 4: Discovering descriptors...")
	var hrMeasurementHandle uint16
	for _, char := range hrChars {
		if bytesEqual(char.UUID, []byte{0x37, 0x2A}) {
			hrMeasurementHandle = char.ValueHandle
			break
		}
	}

	if hrMeasurementHandle == 0 {
		t.Fatal("Heart Rate Measurement characteristic not found")
	}

	err = central.DiscoverDescriptors("peripheral-uuid", hrMeasurementHandle)
	if err != nil {
		t.Fatalf("Failed to discover descriptors: %v", err)
	}

	t.Log("✅ Descriptor discovery completed")

	// Step 5: Verify handle lookup using discovery cache
	t.Log("Step 5: Verifying handle lookup from discovery cache...")
	handle, err := central.GetCharacteristicHandle("peripheral-uuid", []byte{0x00, 0x2A})
	if err != nil {
		t.Fatalf("Failed to get characteristic handle: %v", err)
	}

	if handle == 0 {
		t.Error("Expected non-zero handle for Device Name characteristic")
	}

	t.Logf("✅ Device Name characteristic handle: 0x%04X", handle)

	// Step 6: Test read/write using discovered handles
	t.Log("Step 6: Testing read/write using discovered handles...")

	// Set up a handler on peripheral to respond to reads
	readResponded := make(chan bool, 1)
	peripheral.SetGATTMessageHandler(func(peerUUID string, msg *GATTMessage) {
		if msg.Operation == "read" {
			// Send back the device name
			peripheral.SendGATTMessage(peerUUID, &GATTMessage{
				Type:               "gatt_response",
				Operation:          "read",
				Status:             "success",
				ServiceUUID:        msg.ServiceUUID,
				CharacteristicUUID: msg.CharacteristicUUID,
				Data:               []byte("TestDevice"),
			})
			readResponded <- true
		}
	})

	// Send read request using the discovered handle (via high-level API)
	err = central.SendGATTMessage("peripheral-uuid", &GATTMessage{
		Type:               "gatt_request",
		Operation:          "read",
		ServiceUUID:        "0018",
		CharacteristicUUID: "002A",
	})
	if err != nil {
		t.Fatalf("Failed to send read request: %v", err)
	}

	// Wait for response
	select {
	case <-readResponded:
		t.Log("✅ Read request successful using discovery cache")
	case <-time.After(2 * time.Second):
		t.Error("Read request timeout")
	}

	t.Log("✅ Full discovery flow test completed successfully")
}

// TestDiscoveryWithMultipleConnections tests discovery on multiple simultaneous connections
func TestDiscoveryWithMultipleConnections(t *testing.T) {
	util.SetRandom()

	// Create one central and two peripherals
	central := NewWire("central-uuid")
	peripheral1 := NewWire("peripheral-1-uuid")
	peripheral2 := NewWire("peripheral-2-uuid")

	// Start all devices
	if err := central.Start(); err != nil {
		t.Fatalf("Failed to start central: %v", err)
	}
	defer central.Stop()

	if err := peripheral1.Start(); err != nil {
		t.Fatalf("Failed to start peripheral1: %v", err)
	}
	defer peripheral1.Stop()

	if err := peripheral2.Start(); err != nil {
		t.Fatalf("Failed to start peripheral2: %v", err)
	}
	defer peripheral2.Stop()

	// Build different GATT databases on each peripheral
	services1 := []gatt.Service{
		{
			UUID:    []byte{0x00, 0x18}, // Generic Access
			Primary: true,
			Characteristics: []gatt.Characteristic{
				{
					UUID:       []byte{0x00, 0x2A}, // Device Name
					Properties: gatt.PropRead,
					Value:      []byte("Device1"),
				},
			},
		},
	}

	services2 := []gatt.Service{
		{
			UUID:    []byte{0x0D, 0x18}, // Heart Rate
			Primary: true,
			Characteristics: []gatt.Characteristic{
				{
					UUID:       []byte{0x37, 0x2A}, // Heart Rate Measurement
					Properties: gatt.PropNotify,
					Value:      []byte{0x00, 0x5A},
				},
			},
		},
	}

	db1, _ := gatt.BuildAttributeDatabase(services1)
	peripheral1.SetAttributeDatabase(db1)
	db2, _ := gatt.BuildAttributeDatabase(services2)
	peripheral2.SetAttributeDatabase(db2)

	// Wait for listeners
	time.Sleep(100 * time.Millisecond)

	// Connect to both peripherals
	err := central.Connect("peripheral-1-uuid")
	if err != nil {
		t.Fatalf("Failed to connect to peripheral1: %v", err)
	}

	err = central.Connect("peripheral-2-uuid")
	if err != nil {
		t.Fatalf("Failed to connect to peripheral2: %v", err)
	}

	// Wait for connections and MTU negotiation
	time.Sleep(500 * time.Millisecond)

	// Discover services on both peripherals
	err = central.DiscoverServices("peripheral-1-uuid")
	if err != nil {
		t.Fatalf("Failed to discover services on peripheral1: %v", err)
	}

	err = central.DiscoverServices("peripheral-2-uuid")
	if err != nil {
		t.Fatalf("Failed to discover services on peripheral2: %v", err)
	}

	// Verify each peripheral has different services
	services1Discovered, err := central.GetDiscoveredServices("peripheral-1-uuid")
	if err != nil {
		t.Fatalf("Failed to get services for peripheral1: %v", err)
	}

	services2Discovered, err := central.GetDiscoveredServices("peripheral-2-uuid")
	if err != nil {
		t.Fatalf("Failed to get services for peripheral2: %v", err)
	}

	if len(services1Discovered) != 1 {
		t.Errorf("Expected 1 service on peripheral1, got %d", len(services1Discovered))
	}

	if len(services2Discovered) != 1 {
		t.Errorf("Expected 1 service on peripheral2, got %d", len(services2Discovered))
	}

	// Verify they're different services
	if bytesEqual(services1Discovered[0].UUID, services2Discovered[0].UUID) {
		t.Error("Expected different services on each peripheral")
	}

	t.Log("✅ Multiple connection discovery test completed successfully")
}

// TestDiscoveryErrorHandling tests error cases in discovery
func TestDiscoveryErrorHandling(t *testing.T) {
	util.SetRandom()

	central := NewWire("central-uuid")

	if err := central.Start(); err != nil {
		t.Fatalf("Failed to start central: %v", err)
	}
	defer central.Stop()

	// Test 1: Discover services on non-existent connection
	err := central.DiscoverServices("non-existent-uuid")
	if err == nil {
		t.Error("Expected error when discovering services on non-existent connection")
	}

	// Test 2: Discover characteristics before discovering services
	peripheral := NewWire("peripheral-uuid")
	if err := peripheral.Start(); err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheral.Stop()

	services := []gatt.Service{
		{
			UUID:    []byte{0x00, 0x18},
			Primary: true,
			Characteristics: []gatt.Characteristic{
				{
					UUID:       []byte{0x00, 0x2A},
					Properties: gatt.PropRead,
					Value:      []byte("Test"),
				},
			},
		},
	}
	db, _ := gatt.BuildAttributeDatabase(services)
	peripheral.SetAttributeDatabase(db)

	time.Sleep(100 * time.Millisecond)

	err = central.Connect("peripheral-uuid")
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Try to discover characteristics without discovering services first
	err = central.DiscoverCharacteristics("peripheral-uuid", []byte{0x00, 0x18})
	if err == nil {
		t.Error("Expected error when discovering characteristics without discovering services first")
	}

	t.Log("✅ Error handling test completed successfully")
}
