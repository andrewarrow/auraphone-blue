package main

import (
	"testing"
	"time"
)

// testFirstNameExchange is the shared test logic for custom first name exchange.
// It verifies:
// 1. Basic handshake with default names (same as integration_test.go baseline)
// 2. Custom first name updates via ProfileMessage
// 3. Bidirectional profile receipt and persistence
func testFirstNameExchange(t *testing.T, iphoneUUID, androidUUID string) {
	// Setup: Clean directories and create devices
	ip, droid := setupTestDevices(t, iphoneUUID, androidUUID)
	defer cleanupDevices(ip, droid)

	// Start both devices and wait for connection
	startAndWaitForHandshake(ip, droid)

	// Verify baseline handshake with default names (returns device IDs for profile verification)
	iphoneDeviceID, androidDeviceID := verifyBasicHandshake(t, ip, droid, iphoneUUID, androidUUID)

	// ========================================
	// Update first names and verify ProfileMessage exchange
	// ========================================

	// iPhone sets first name to "bob" (profileVersion 0 → 1)
	err := ip.UpdateLocalProfile(map[string]string{"first_name": "bob"})
	if err != nil {
		t.Fatalf("Failed to update iPhone profile: %v", err)
	}

	// Android sets first name to "sue" (profileVersion 0 → 1)
	err = droid.UpdateLocalProfile(map[string]string{"first_name": "sue"})
	if err != nil {
		t.Fatalf("Failed to update Android profile: %v", err)
	}

	t.Logf("📝 Updated profiles: iPhone → 'bob', Android → 'sue'")

	// Wait for ProfileMessage broadcast and receipt
	// UpdateLocalProfile triggers broadcastProfileUpdate() which sends ProfileMessage to all connected peers
	time.Sleep(2 * time.Second)

	// ========================================
	// Verify profile data was received and persisted
	// ========================================

	// Verify iPhone received Android's profile ("sue")
	verifyProfileReceived(t, iphoneUUID, androidDeviceID, "sue", 1)

	// Verify Android received iPhone's profile ("bob")
	verifyProfileReceived(t, androidUUID, iphoneDeviceID, "bob", 1)

	// ========================================
	// Final summary
	// ========================================

	t.Logf("✅ Test complete:")
	t.Logf("   - Baseline handshake with default names: ✓")
	t.Logf("   - Custom first name updates: ✓")
	t.Logf("   - Bidirectional ProfileMessage exchange: ✓")
	t.Logf("   - Profile persistence to disk: ✓")

	t.Logf("✅ Test passed: First name exchange works correctly")
}

// TestFirstNameExchangeAndroidCentral tests first name exchange when Android acts as Central.
// Android UUID (222...) > iPhone UUID (111...), so Android initiates the connection.
func TestFirstNameExchangeAndroidCentral(t *testing.T) {
	iphoneUUID := "11111111-1111-1111-1111-111111111111"  // Lower UUID
	androidUUID := "22222222-2222-2222-2222-222222222222" // Higher UUID → acts as Central
	testFirstNameExchange(t, iphoneUUID, androidUUID)
}

// TestFirstNameExchangeAndroidPeripheral tests first name exchange when Android acts as Peripheral.
// iPhone UUID (222...) > Android UUID (111...), so iPhone initiates the connection.
func TestFirstNameExchangeAndroidPeripheral(t *testing.T) {
	androidUUID := "11111111-1111-1111-1111-111111111111" // Lower UUID
	iphoneUUID := "22222222-2222-2222-2222-222222222222"  // Higher UUID → acts as Central
	testFirstNameExchange(t, iphoneUUID, androidUUID)
}
