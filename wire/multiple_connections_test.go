package wire

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/user/auraphone-blue/util"
	"github.com/user/auraphone-blue/wire/gatt"
)

// TestMultipleConnectionsToSamePeripheral tests that multiple centrals can
// establish independent connections to the same peripheral.
// Each connection should have isolated state (MTU, discovery cache, request tracking).
func TestMultipleConnectionsToSamePeripheral(t *testing.T) {
	util.SetRandom()

	// Create peripheral with services
	peripheral := NewWire("peripheral-multi")

	// Add GATT services
	db, _ := gatt.BuildAttributeDatabase([]gatt.Service{
		{
			UUID:    []byte{0x00, 0x18}, // Generic Access
			Primary: true,
			Characteristics: []gatt.Characteristic{
				{
					UUID:       []byte{0x00, 0x2A}, // Device Name
					Properties: gatt.PropRead,
					Value:      []byte("TestDevice"),
				},
			},
		},
	})
	peripheral.SetAttributeDatabase(db)

	if err := peripheral.Start(); err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheral.Stop()

	// Create first central connection
	central1 := NewWire("central-1")
	if err := central1.Start(); err != nil {
		t.Fatalf("Failed to start central1: %v", err)
	}
	defer central1.Stop()

	// Create second central connection
	central2 := NewWire("central-2")
	if err := central2.Start(); err != nil {
		t.Fatalf("Failed to start central2: %v", err)
	}
	defer central2.Stop()

	time.Sleep(100 * time.Millisecond) // Wait for listeners

	// Both centrals connect to the same peripheral
	if err := central1.Connect("peripheral-multi"); err != nil {
		t.Fatalf("Failed to connect central1: %v", err)
	}

	if err := central2.Connect("peripheral-multi"); err != nil {
		t.Fatalf("Failed to connect central2: %v", err)
	}

	time.Sleep(100 * time.Millisecond) // Wait for connections

	// Verify peripheral has 2 connections
	peripheral.mu.RLock()
	numConnections := len(peripheral.connections)
	peripheral.mu.RUnlock()

	if numConnections != 2 {
		t.Errorf("Expected 2 connections to peripheral, got %d", numConnections)
	}

	// Verify both centrals are connected
	if !central1.IsConnected("peripheral-multi") {
		t.Error("Central1 should be connected to peripheral")
	}

	if !central2.IsConnected("peripheral-multi") {
		t.Error("Central2 should be connected to peripheral")
	}

	// Test that both connections can operate independently
	// Both centrals discover services simultaneously
	err1 := central1.DiscoverServices("peripheral-multi")
	err2 := central2.DiscoverServices("peripheral-multi")

	if err1 != nil {
		t.Errorf("Central1 discovery failed: %v", err1)
	}
	if err2 != nil {
		t.Errorf("Central2 discovery failed: %v", err2)
	}

	time.Sleep(200 * time.Millisecond) // Wait for discovery

	// Verify both have discovered services
	services1, getErr1 := central1.GetDiscoveredServices("peripheral-multi")
	if getErr1 != nil {
		t.Errorf("Central1 failed to get discovered services: %v", getErr1)
	}

	services2, getErr2 := central2.GetDiscoveredServices("peripheral-multi")
	if getErr2 != nil {
		t.Errorf("Central2 failed to get discovered services: %v", getErr2)
	}

	if len(services1) != 1 {
		t.Errorf("Central1: expected 1 service, got %d", len(services1))
	}

	if len(services2) != 1 {
		t.Errorf("Central2: expected 1 service, got %d", len(services2))
	}
}

// TestPerConnectionDiscoveryCacheIsolation verifies that each connection
// maintains its own discovery cache independently.
func TestPerConnectionDiscoveryCacheIsolation(t *testing.T) {
	util.SetRandom()

	// Create peripheral with services
	peripheral := NewWire("peripheral-disco")

	db, _ := gatt.BuildAttributeDatabase([]gatt.Service{
		{
			UUID:    []byte{0x00, 0x18}, // Generic Access
			Primary: true,
			Characteristics: []gatt.Characteristic{
				{
					UUID:       []byte{0x00, 0x2A}, // Device Name
					Properties: gatt.PropRead,
					Value:      []byte("Test"),
				},
			},
		},
		{
			UUID:    []byte{0x0A, 0x18}, // Device Information
			Primary: true,
			Characteristics: []gatt.Characteristic{
				{
					UUID:       []byte{0x29, 0x2A}, // Manufacturer Name
					Properties: gatt.PropRead,
					Value:      []byte("Acme"),
				},
			},
		},
	})
	peripheral.SetAttributeDatabase(db)

	if err := peripheral.Start(); err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheral.Stop()

	// Create two centrals
	central1 := NewWire("central-disco-1")
	if err := central1.Start(); err != nil {
		t.Fatalf("Failed to start central1: %v", err)
	}
	defer central1.Stop()

	central2 := NewWire("central-disco-2")
	if err := central2.Start(); err != nil {
		t.Fatalf("Failed to start central2: %v", err)
	}
	defer central2.Stop()

	time.Sleep(100 * time.Millisecond)

	// Connect both
	if err := central1.Connect("peripheral-disco"); err != nil {
		t.Fatalf("Failed to connect central1: %v", err)
	}

	if err := central2.Connect("peripheral-disco"); err != nil {
		t.Fatalf("Failed to connect central2: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Central 1 discovers services
	if err := central1.DiscoverServices("peripheral-disco"); err != nil {
		t.Fatalf("Failed to discover services on central1: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Verify central1 has cache but central2 doesn't (hasn't discovered yet)
	services1, getErr1 := central1.GetDiscoveredServices("peripheral-disco")
	if getErr1 != nil {
		t.Fatalf("Failed to get services from central1: %v", getErr1)
	}

	services2, getErr2 := central2.GetDiscoveredServices("peripheral-disco")
	if getErr2 != nil {
		t.Fatalf("Failed to get services from central2: %v", getErr2)
	}

	if len(services1) != 2 {
		t.Errorf("Expected central1 to have 2 services in cache, got %d", len(services1))
	}

	if len(services2) != 0 {
		t.Errorf("Expected central2 to have 0 services in cache (not discovered yet), got %d", len(services2))
	}

	// Now central2 discovers
	if err := central2.DiscoverServices("peripheral-disco"); err != nil {
		t.Fatalf("Failed to discover services on central2: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Now both should have cache
	services2After, getErr3 := central2.GetDiscoveredServices("peripheral-disco")
	if getErr3 != nil {
		t.Fatalf("Failed to get services from central2 after discovery: %v", getErr3)
	}

	if len(services2After) != 2 {
		t.Errorf("Expected central2 to have 2 services in cache after discovery, got %d", len(services2After))
	}
}

// TestConcurrentDiscoveryOnMultipleConnections tests that multiple centrals
// can perform discovery simultaneously without interfering with each other.
func TestConcurrentDiscoveryOnMultipleConnections(t *testing.T) {
	util.SetRandom()

	// Create peripheral with services
	peripheral := NewWire("peripheral-concurrent")

	db, _ := gatt.BuildAttributeDatabase([]gatt.Service{
		{
			UUID:    []byte{0x00, 0x18}, // Generic Access
			Primary: true,
			Characteristics: []gatt.Characteristic{
				{UUID: []byte{0x00, 0x2A}, Properties: gatt.PropRead, Value: []byte("Device1")},
			},
		},
		{
			UUID:    []byte{0x0A, 0x18}, // Device Information
			Primary: true,
			Characteristics: []gatt.Characteristic{
				{UUID: []byte{0x29, 0x2A}, Properties: gatt.PropRead, Value: []byte("Mfg1")},
			},
		},
		{
			UUID:    []byte{0x0F, 0x18}, // Battery Service
			Primary: true,
			Characteristics: []gatt.Characteristic{
				{UUID: []byte{0x19, 0x2A}, Properties: gatt.PropRead, Value: []byte{100}},
			},
		},
	})
	peripheral.SetAttributeDatabase(db)

	if err := peripheral.Start(); err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheral.Stop()

	// Create 3 centrals
	numCentrals := 3
	centrals := make([]*Wire, numCentrals)

	for i := 0; i < numCentrals; i++ {
		central := NewWire(fmt.Sprintf("central-concurrent-%d", i))
		if err := central.Start(); err != nil {
			t.Fatalf("Failed to start central %d: %v", i, err)
		}
		defer central.Stop()
		centrals[i] = central
	}

	time.Sleep(100 * time.Millisecond)

	// Connect all centrals
	for i := 0; i < numCentrals; i++ {
		if err := centrals[i].Connect("peripheral-concurrent"); err != nil {
			t.Fatalf("Failed to connect central %d: %v", i, err)
		}
	}

	time.Sleep(100 * time.Millisecond)

	// All centrals discover services concurrently
	var wg sync.WaitGroup
	errors := make([]error, numCentrals)

	for i := 0; i < numCentrals; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errors[idx] = centrals[idx].DiscoverServices("peripheral-concurrent")
		}(i)
	}

	wg.Wait()

	// Check for errors
	for i, err := range errors {
		if err != nil {
			t.Errorf("Central %d error: %v", i, err)
		}
	}

	time.Sleep(300 * time.Millisecond) // Wait for all discoveries to complete

	// Verify all centrals have correct discovery cache
	for i := 0; i < numCentrals; i++ {
		services, err := centrals[i].GetDiscoveredServices("peripheral-concurrent")
		if err != nil {
			t.Errorf("Central %d: failed to get discovered services: %v", i, err)
			continue
		}

		if len(services) != 3 {
			t.Errorf("Central %d: expected 3 services, got %d", i, len(services))
		}
	}
}

// TestConnectionLimits tests behavior when approaching typical BLE connection limits.
// iOS typically supports 7-10 concurrent connections, Android varies by chipset.
func TestConnectionLimits(t *testing.T) {
	util.SetRandom()

	// Create peripheral
	peripheral := NewWire("peripheral-limits")

	db, _ := gatt.BuildAttributeDatabase([]gatt.Service{
		{
			UUID:    []byte{0x00, 0x18}, // Generic Access
			Primary: true,
			Characteristics: []gatt.Characteristic{
				{UUID: []byte{0x00, 0x2A}, Properties: gatt.PropRead, Value: []byte("Test")},
			},
		},
	})
	peripheral.SetAttributeDatabase(db)

	if err := peripheral.Start(); err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheral.Stop()

	// Try to create 10 connections (typical iOS limit)
	numConnections := 10
	centrals := make([]*Wire, numConnections)

	for i := 0; i < numConnections; i++ {
		central := NewWire(fmt.Sprintf("central-limit-%d", i))
		if err := central.Start(); err != nil {
			t.Fatalf("Failed to start central %d: %v", i, err)
		}
		defer central.Stop()
		centrals[i] = central
	}

	time.Sleep(100 * time.Millisecond)

	// Connect all centrals
	for i := 0; i < numConnections; i++ {
		if err := centrals[i].Connect("peripheral-limits"); err != nil {
			t.Fatalf("Failed to connect central %d: %v", i, err)
		}
	}

	time.Sleep(200 * time.Millisecond)

	// Verify all connections exist
	peripheral.mu.RLock()
	actualConnections := len(peripheral.connections)
	peripheral.mu.RUnlock()

	if actualConnections != numConnections {
		t.Errorf("Expected %d connections, got %d", numConnections, actualConnections)
	}

	// Verify all connections can discover services independently
	// Test by having each central discover concurrently
	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	for i := 0; i < numConnections; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			err := centrals[idx].DiscoverServices("peripheral-limits")
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()
	time.Sleep(300 * time.Millisecond) // Wait for discoveries

	if successCount != numConnections {
		t.Errorf("Expected %d successful discoveries, got %d", numConnections, successCount)
	}

	// Verify all have discovered services
	for i := 0; i < numConnections; i++ {
		services, err := centrals[i].GetDiscoveredServices("peripheral-limits")
		if err != nil {
			t.Errorf("Central %d: failed to get services: %v", i, err)
			continue
		}
		if len(services) != 1 {
			t.Errorf("Central %d: expected 1 service, got %d", i, len(services))
		}
	}
}

// TestRequestTrackerIsolation verifies that request trackers are independent
// per connection and don't interfere with each other.
func TestRequestTrackerIsolation(t *testing.T) {
	util.SetRandom()

	peripheral := NewWire("peripheral-tracker")

	db, _ := gatt.BuildAttributeDatabase([]gatt.Service{
		{
			UUID:    []byte{0x00, 0x18}, // Generic Access
			Primary: true,
			Characteristics: []gatt.Characteristic{
				{UUID: []byte{0x00, 0x2A}, Properties: gatt.PropRead, Value: []byte("Test")},
			},
		},
	})
	peripheral.SetAttributeDatabase(db)

	if err := peripheral.Start(); err != nil {
		t.Fatalf("Failed to start peripheral: %v", err)
	}
	defer peripheral.Stop()

	// Create two centrals
	central1 := NewWire("central-tracker-1")
	if err := central1.Start(); err != nil {
		t.Fatalf("Failed to start central1: %v", err)
	}
	defer central1.Stop()

	central2 := NewWire("central-tracker-2")
	if err := central2.Start(); err != nil {
		t.Fatalf("Failed to start central2: %v", err)
	}
	defer central2.Stop()

	time.Sleep(100 * time.Millisecond)

	// Connect both
	if err := central1.Connect("peripheral-tracker"); err != nil {
		t.Fatalf("Failed to connect central1: %v", err)
	}

	if err := central2.Connect("peripheral-tracker"); err != nil {
		t.Fatalf("Failed to connect central2: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Both centrals discover services multiple times concurrently
	// This tests that request tracking doesn't interfere between connections
	numRequests := 5
	var wg sync.WaitGroup
	errors1 := make([]error, numRequests)
	errors2 := make([]error, numRequests)

	for i := 0; i < numRequests; i++ {
		wg.Add(2)

		go func(idx int) {
			defer wg.Done()
			errors1[idx] = central1.DiscoverServices("peripheral-tracker")
		}(i)

		go func(idx int) {
			defer wg.Done()
			errors2[idx] = central2.DiscoverServices("peripheral-tracker")
		}(i)

		time.Sleep(20 * time.Millisecond) // Slight delay between batches
	}

	wg.Wait()
	time.Sleep(2000 * time.Millisecond) // Wait for all responses (increased for concurrent load)

	// Count successes
	success1 := 0
	for _, err := range errors1 {
		if err == nil {
			success1++
		}
	}

	success2 := 0
	for _, err := range errors2 {
		if err == nil {
			success2++
		}
	}

	if success1 != numRequests {
		t.Errorf("Central1: expected %d successful discoveries, got %d", numRequests, success1)
	}

	if success2 != numRequests {
		t.Errorf("Central2: expected %d successful discoveries, got %d", numRequests, success2)
	}
}
