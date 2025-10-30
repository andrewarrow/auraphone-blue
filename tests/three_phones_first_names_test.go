package main

import (
	"testing"
	"time"

	"github.com/user/auraphone-blue/android"
	"github.com/user/auraphone-blue/iphone"
	"github.com/user/auraphone-blue/util"
)

// TestThreePhonesFirstNameExchange tests first name exchange among 3 devices:
// - iPhone A (started first)
// - Android B (started first)
// - iPhone C (started third, after A and B are connected)
//
// Verifies that after everything settles, all 3 phones have first_names from the other 2 phones.
// Names can propagate directly or via gossip - the test only cares about the final state.
func TestThreePhonesFirstNameExchange(t *testing.T) {
	util.SetRandom()

	// UUIDs sorted to control connection roles:
	// 111... < 222... < 333...
	// So: A is lowest, B is middle, C is highest
	iphoneAUUID := "11111111-1111-1111-1111-111111111111"  // Phone A (iPhone)
	androidBUUID := "22222222-2222-2222-2222-222222222222" // Phone B (Android)
	iphoneCUUID := "33333333-3333-3333-3333-333333333333"  // Phone C (iPhone, launches third)

	// ========================================
	// Phase 1: Create and start phones A and B
	// ========================================

	t.Logf("📱 Creating devices:")
	t.Logf("   Phone A (iPhone):  %s", iphoneAUUID[:8])
	t.Logf("   Phone B (Android): %s", androidBUUID[:8])
	t.Logf("   Phone C (iPhone):  %s (will start later)", iphoneCUUID[:8])

	phoneA := iphone.NewIPhone(iphoneAUUID)
	phoneB := android.NewAndroid(androidBUUID)

	defer phoneA.Stop()
	defer phoneB.Stop()

	// Start phones A and B
	phoneA.Start()
	phoneB.Start()

	t.Logf("🚀 Started phones A and B")

	// Wait for A and B to discover each other and connect
	time.Sleep(3 * time.Second)

	// Verify A and B are connected
	verifyBasicHandshake(t, phoneA, phoneB, iphoneAUUID, androidBUUID)

	t.Logf("✅ Phones A and B are connected")

	// ========================================
	// Phase 2: Start phone C (third device) before updating profiles
	// This ensures all phones are connected before profile broadcasts
	// ========================================

	t.Logf("🚀 Starting phone C (third device)")

	phoneC := iphone.NewIPhone(iphoneCUUID)
	defer phoneC.Stop()

	phoneC.Start()

	// Wait for C to discover A and B, and for A and B to discover C
	time.Sleep(3 * time.Second)

	t.Logf("✅ Phone C has joined the network")

	// ========================================
	// Phase 3: Update all first names AFTER all phones are connected
	// This ensures profile broadcasts reach all connected peers directly
	// ========================================

	err := phoneA.UpdateLocalProfile(map[string]string{"first_name": "Alice"})
	if err != nil {
		t.Fatalf("Failed to update phone A profile: %v", err)
	}

	err = phoneB.UpdateLocalProfile(map[string]string{"first_name": "Bob"})
	if err != nil {
		t.Fatalf("Failed to update phone B profile: %v", err)
	}

	err = phoneC.UpdateLocalProfile(map[string]string{"first_name": "Charlie"})
	if err != nil {
		t.Fatalf("Failed to update phone C profile: %v", err)
	}

	t.Logf("📝 Updated all profiles: A → 'Alice', B → 'Bob', C → 'Charlie'")

	// Wait for all profiles to propagate (direct connections and/or gossip)
	// Since all phones are already connected, profiles should propagate via broadcast
	time.Sleep(3 * time.Second)

	// ========================================
	// Phase 4: Verify final state - all phones should know all other phones
	// ========================================

	t.Logf("🔍 Verifying final state: all phones should have first_names from all other phones")

	// Get device IDs for verification
	// Phone A should have handshakes from B and C
	handshakesA := phoneA.GetHandshaked()
	handshakeAtoB, hasBfromA := handshakesA[androidBUUID]
	handshakeAtoC, hasCfromA := handshakesA[iphoneCUUID]

	if !hasBfromA {
		t.Fatalf("Phone A did not receive handshake from Phone B")
	}
	if !hasCfromA {
		t.Fatalf("Phone A did not receive handshake from Phone C")
	}

	deviceIDB := handshakeAtoB.DeviceID
	deviceIDC := handshakeAtoC.DeviceID

	// Phone B should have handshakes from A and C
	handshakesB := phoneB.GetHandshaked()
	handshakeBtoA, hasAfromB := handshakesB[iphoneAUUID]
	_, hasCfromB := handshakesB[iphoneCUUID]

	if !hasAfromB {
		t.Fatalf("Phone B did not receive handshake from Phone A")
	}
	if !hasCfromB {
		t.Fatalf("Phone B did not receive handshake from Phone C")
	}

	deviceIDA := handshakeBtoA.DeviceID

	// Phone C should have handshakes from A and B
	handshakesC := phoneC.GetHandshaked()
	_, hasAfromC := handshakesC[iphoneAUUID]
	_, hasBfromC := handshakesC[androidBUUID]

	if !hasAfromC {
		t.Fatalf("Phone C did not receive handshake from Phone A")
	}
	if !hasBfromC {
		t.Fatalf("Phone C did not receive handshake from Phone B")
	}

	t.Logf("✅ All phones have handshakes from all other phones")
	t.Logf("   Device IDs: A=%s, B=%s, C=%s", deviceIDA, deviceIDB, deviceIDC)

	// Verify Phone A received profiles from B and C
	verifyProfileReceived(t, iphoneAUUID, deviceIDB, "Bob", 1)
	verifyProfileReceived(t, iphoneAUUID, deviceIDC, "Charlie", 1)

	// Verify Phone B received profiles from A and C
	verifyProfileReceived(t, androidBUUID, deviceIDA, "Alice", 1)
	verifyProfileReceived(t, androidBUUID, deviceIDC, "Charlie", 1)

	// Verify Phone C received profiles from A and B
	verifyProfileReceived(t, iphoneCUUID, deviceIDA, "Alice", 1)
	verifyProfileReceived(t, iphoneCUUID, deviceIDB, "Bob", 1)

	// ========================================
	// Final summary
	// ========================================

	t.Logf("✅ Test complete:")
	t.Logf("   - Phone A knows: Bob (from B) and Charlie (from C)")
	t.Logf("   - Phone B knows: Alice (from A) and Charlie (from C)")
	t.Logf("   - Phone C knows: Alice (from A) and Bob (from B)")
	t.Logf("   - All profiles propagated successfully (direct or via gossip)")

	t.Logf("✅ Test passed: Three-phone first name exchange works correctly")
}
