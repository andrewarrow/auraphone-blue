package android

import (
	"testing"
	"time"
)

// TestTwoAndroidsHandshake verifies that two Android devices can discover each other, connect, and exchange handshakes
func TestTwoAndroidsHandshake(t *testing.T) {
	// Create two Android devices
	android1 := NewAndroid("11111111-1111-1111-1111-111111111111")
	android2 := NewAndroid("22222222-2222-2222-2222-222222222222")

	// Start both devices
	android1.Start()
	android2.Start()

	// Wait for discovery, connection, and handshake
	time.Sleep(3 * time.Second)

	// Verify both devices discovered each other
	android1.mu.RLock()
	device2, found1 := android1.discovered[android2.hardwareUUID]
	android1.mu.RUnlock()

	if !found1 {
		t.Fatalf("Android 1 did not discover Android 2")
	}

	android2.mu.RLock()
	device1, found2 := android2.discovered[android1.hardwareUUID]
	android2.mu.RUnlock()

	if !found2 {
		t.Fatalf("Android 2 did not discover Android 1")
	}

	t.Logf("✅ Discovery successful")
	t.Logf("   Android 1 sees: %s", device2.Name)
	t.Logf("   Android 2 sees: %s", device1.Name)

	// Verify handshakes were exchanged
	android1.mu.RLock()
	handshake2, hasHandshake1 := android1.handshaked[android2.hardwareUUID]
	android1.mu.RUnlock()

	if !hasHandshake1 {
		t.Fatalf("Android 1 did not receive handshake from Android 2")
	}

	android2.mu.RLock()
	handshake1, hasHandshake2 := android2.handshaked[android1.hardwareUUID]
	android2.mu.RUnlock()

	if !hasHandshake2 {
		t.Fatalf("Android 2 did not receive handshake from Android 1")
	}

	t.Logf("✅ Handshake successful")
	t.Logf("   Android 1 received: %s (ID: %s)", handshake2.FirstName, handshake2.DeviceID)
	t.Logf("   Android 2 received: %s (ID: %s)", handshake1.FirstName, handshake1.DeviceID)

	// Verify handshake data is correct
	if handshake1.FirstName != android1.firstName {
		t.Errorf("Expected %s, got %s", android1.firstName, handshake1.FirstName)
	}
	if handshake1.DeviceID != android1.deviceID {
		t.Errorf("Expected %s, got %s", android1.deviceID, handshake1.DeviceID)
	}

	if handshake2.FirstName != android2.firstName {
		t.Errorf("Expected %s, got %s", android2.firstName, handshake2.FirstName)
	}
	if handshake2.DeviceID != android2.deviceID {
		t.Errorf("Expected %s, got %s", android2.deviceID, handshake2.DeviceID)
	}

	// Cleanup
	android1.Stop()
	android2.Stop()

	t.Logf("✅ Test passed: Two Android devices successfully handshaked")
}

// TestRoleNegotiation verifies that only one device initiates connection (no collision)
func TestRoleNegotiation(t *testing.T) {
	// Device with LARGER UUID should initiate connection
	// UUID1 = "aaaaaaaa..." > UUID2 = "11111111..."
	// So device1 should be Central, device2 should be Peripheral

	android1 := NewAndroid("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	android2 := NewAndroid("11111111-1111-1111-1111-111111111111")

	android1.Start()
	android2.Start()

	time.Sleep(3 * time.Second)

	// Check connection roles
	role1, hasConn1 := android1.wire.GetConnectionRole(android2.hardwareUUID)
	role2, hasConn2 := android2.wire.GetConnectionRole(android1.hardwareUUID)

	if !hasConn1 || !hasConn2 {
		t.Fatalf("Connection not established")
	}

	// Android1 (larger UUID) should be Central (initiated connection)
	if role1 != "central" {
		t.Errorf("Android1 should be Central, got %s", role1)
	}

	// Android2 (smaller UUID) should be Peripheral (received connection)
	if role2 != "peripheral" {
		t.Errorf("Android2 should be Peripheral, got %s", role2)
	}

	t.Logf("✅ Role negotiation correct:")
	t.Logf("   Android1 (UUID: aaa...) is %s", role1)
	t.Logf("   Android2 (UUID: 111...) is %s", role2)

	android1.Stop()
	android2.Stop()
}

// TestThreeAndroidsHandshake verifies that three devices can all handshake with each other
func TestThreeAndroidsHandshake(t *testing.T) {
	android1 := NewAndroid("11111111-1111-1111-1111-111111111111")
	android2 := NewAndroid("22222222-2222-2222-2222-222222222222")
	android3 := NewAndroid("33333333-3333-3333-3333-333333333333")

	android1.Start()
	android2.Start()
	android3.Start()

	// Wait longer for 3-way discovery and handshakes
	time.Sleep(5 * time.Second)

	// Verify all connections established
	checkHandshake := func(from, to *Android) {
		from.mu.RLock()
		handshake, exists := from.handshaked[to.hardwareUUID]
		from.mu.RUnlock()

		if !exists {
			t.Errorf("%s did not receive handshake from %s", from.firstName, to.firstName)
			return
		}

		if handshake.FirstName != to.firstName {
			t.Errorf("Expected %s, got %s", to.firstName, handshake.FirstName)
		}
	}

	checkHandshake(android1, android2)
	checkHandshake(android1, android3)
	checkHandshake(android2, android1)
	checkHandshake(android2, android3)
	checkHandshake(android3, android1)
	checkHandshake(android3, android2)

	t.Logf("✅ Three-way handshake successful")
	t.Logf("   All devices connected and exchanged handshakes")

	android1.Stop()
	android2.Stop()
	android3.Stop()
}
