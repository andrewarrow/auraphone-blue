package iphone

import (
	"testing"
	"time"
)

// TestTwoIPhonesHandshake verifies that two iPhones can discover each other, connect, and exchange handshakes
func TestTwoIPhonesHandshake(t *testing.T) {
	// Create two iPhones
	iphone1 := NewIPhone("11111111-1111-1111-1111-111111111111", "DEVICE01", "Alice")
	iphone2 := NewIPhone("22222222-2222-2222-2222-222222222222", "DEVICE02", "Bob")

	// Start both iPhones
	iphone1.Start()
	iphone2.Start()

	// Wait for discovery, connection, and handshake
	time.Sleep(3 * time.Second)

	// Verify both devices discovered each other
	iphone1.mu.RLock()
	device2, found1 := iphone1.discovered[iphone2.hardwareUUID]
	iphone1.mu.RUnlock()

	if !found1 {
		t.Fatalf("iPhone 1 did not discover iPhone 2")
	}

	iphone2.mu.RLock()
	device1, found2 := iphone2.discovered[iphone1.hardwareUUID]
	iphone2.mu.RUnlock()

	if !found2 {
		t.Fatalf("iPhone 2 did not discover iPhone 1")
	}

	t.Logf("✅ Discovery successful")
	t.Logf("   iPhone 1 sees: %s", device2.Name)
	t.Logf("   iPhone 2 sees: %s", device1.Name)

	// Verify handshakes were exchanged
	iphone1.mu.RLock()
	handshake2, hasHandshake1 := iphone1.handshaked[iphone2.hardwareUUID]
	iphone1.mu.RUnlock()

	if !hasHandshake1 {
		t.Fatalf("iPhone 1 did not receive handshake from iPhone 2")
	}

	iphone2.mu.RLock()
	handshake1, hasHandshake2 := iphone2.handshaked[iphone1.hardwareUUID]
	iphone2.mu.RUnlock()

	if !hasHandshake2 {
		t.Fatalf("iPhone 2 did not receive handshake from iPhone 1")
	}

	t.Logf("✅ Handshake successful")
	t.Logf("   iPhone 1 received: %s (ID: %s)", handshake2.FirstName, handshake2.DeviceID)
	t.Logf("   iPhone 2 received: %s (ID: %s)", handshake1.FirstName, handshake1.DeviceID)

	// Verify handshake data is correct
	if handshake1.FirstName != "Alice" {
		t.Errorf("Expected Alice, got %s", handshake1.FirstName)
	}
	if handshake1.DeviceID != "DEVICE01" {
		t.Errorf("Expected DEVICE01, got %s", handshake1.DeviceID)
	}

	if handshake2.FirstName != "Bob" {
		t.Errorf("Expected Bob, got %s", handshake2.FirstName)
	}
	if handshake2.DeviceID != "DEVICE02" {
		t.Errorf("Expected DEVICE02, got %s", handshake2.DeviceID)
	}

	// Cleanup
	iphone1.Stop()
	iphone2.Stop()

	t.Logf("✅ Test passed: Two iPhones successfully handshaked")
}

// TestRoleNegotiation verifies that only one device initiates connection (no collision)
func TestRoleNegotiation(t *testing.T) {
	// Device with LARGER UUID should initiate connection
	// UUID1 = "aaaaaaaa..." > UUID2 = "11111111..."
	// So device1 should be Central, device2 should be Peripheral

	iphone1 := NewIPhone("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "DEVICEAA", "Alice")
	iphone2 := NewIPhone("11111111-1111-1111-1111-111111111111", "DEVICE11", "Bob")

	iphone1.Start()
	iphone2.Start()

	time.Sleep(3 * time.Second)

	// Check connection roles
	role1, hasConn1 := iphone1.wire.GetConnectionRole(iphone2.hardwareUUID)
	role2, hasConn2 := iphone2.wire.GetConnectionRole(iphone1.hardwareUUID)

	if !hasConn1 || !hasConn2 {
		t.Fatalf("Connection not established")
	}

	// iPhone1 (larger UUID) should be Central (initiated connection)
	if role1 != "central" {
		t.Errorf("iPhone1 should be Central, got %s", role1)
	}

	// iPhone2 (smaller UUID) should be Peripheral (received connection)
	if role2 != "peripheral" {
		t.Errorf("iPhone2 should be Peripheral, got %s", role2)
	}

	t.Logf("✅ Role negotiation correct:")
	t.Logf("   iPhone1 (UUID: aaa...) is %s", role1)
	t.Logf("   iPhone2 (UUID: 111...) is %s", role2)

	iphone1.Stop()
	iphone2.Stop()
}

// TestThreeIPhonesHandshake verifies that three devices can all handshake with each other
func TestThreeIPhonesHandshake(t *testing.T) {
	iphone1 := NewIPhone("11111111-1111-1111-1111-111111111111", "DEVICE01", "Alice")
	iphone2 := NewIPhone("22222222-2222-2222-2222-222222222222", "DEVICE02", "Bob")
	iphone3 := NewIPhone("33333333-3333-3333-3333-333333333333", "DEVICE03", "Charlie")

	iphone1.Start()
	iphone2.Start()
	iphone3.Start()

	// Wait longer for 3-way discovery and handshakes
	time.Sleep(5 * time.Second)

	// Verify all connections established
	checkHandshake := func(from, to *IPhone, expectedName string) {
		from.mu.RLock()
		handshake, exists := from.handshaked[to.hardwareUUID]
		from.mu.RUnlock()

		if !exists {
			t.Errorf("%s did not receive handshake from %s", from.firstName, to.firstName)
			return
		}

		if handshake.FirstName != expectedName {
			t.Errorf("Expected %s, got %s", expectedName, handshake.FirstName)
		}
	}

	checkHandshake(iphone1, iphone2, "Bob")
	checkHandshake(iphone1, iphone3, "Charlie")
	checkHandshake(iphone2, iphone1, "Alice")
	checkHandshake(iphone2, iphone3, "Charlie")
	checkHandshake(iphone3, iphone1, "Alice")
	checkHandshake(iphone3, iphone2, "Bob")

	t.Logf("✅ Three-way handshake successful")
	t.Logf("   All devices connected and exchanged handshakes")

	iphone1.Stop()
	iphone2.Stop()
	iphone3.Stop()
}
