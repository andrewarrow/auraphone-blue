package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/user/auraphone-blue/android"
	"github.com/user/auraphone-blue/iphone"
	"github.com/user/auraphone-blue/phone"
)

// testFirstNameExchange is the shared test logic for custom first name exchange.
// It verifies:
// 1. Basic handshake with default names (same as integration_test.go baseline)
// 2. Custom first name updates via ProfileMessage
// 3. Bidirectional profile receipt and persistence
func testFirstNameExchange(t *testing.T, iphoneUUID, androidUUID string) {
	// Clean up data directories from previous runs to ensure fresh state
	dataDir := phone.GetDataDir()
	iphoneDataDir := filepath.Join(dataDir, iphoneUUID)
	androidDataDir := filepath.Join(dataDir, androidUUID)

	os.RemoveAll(iphoneDataDir)
	os.RemoveAll(androidDataDir)

	t.Logf("🧹 Cleaned up test data directories")

	// Create one iPhone with default settings
	ip := iphone.NewIPhone(iphoneUUID)

	// Create one Android with default settings
	droid := android.NewAndroid(androidUUID)

	// Verify default first names
	if ip.GetFirstName() != "iPhone" {
		t.Errorf("Expected iPhone first name to be 'iPhone', got '%s'", ip.GetFirstName())
	}
	if droid.GetFirstName() != "Android" {
		t.Errorf("Expected Android first name to be 'Android', got '%s'", droid.GetFirstName())
	}

	// Start both devices (begins advertising and scanning)
	ip.Start()
	droid.Start()

	// Wait for discovery, connection, and handshake
	// BLE discovery takes ~100-500ms, connection takes ~30-100ms, handshake is immediate
	time.Sleep(3 * time.Second)

	// ========================================
	// PART 1: Verify baseline handshake with default names
	// (Same assertions as integration_test.go)
	// ========================================

	// Verify iPhone discovered Android
	discoveredDevices := ip.GetDiscovered()
	discoveredFromIPhone, foundAndroid := discoveredDevices[androidUUID]

	if !foundAndroid {
		t.Fatalf("iPhone did not discover Android")
	}

	t.Logf("✅ iPhone discovered Android: %s", discoveredFromIPhone.Name)

	// Verify iPhone received handshake from Android
	handshakes := ip.GetHandshaked()
	handshakeFromAndroid, hasHandshake := handshakes[androidUUID]

	if !hasHandshake {
		t.Fatalf("iPhone did not receive handshake from Android")
	}

	// Verify handshake data from Android is correct (default name)
	if handshakeFromAndroid.HardwareUUID != androidUUID {
		t.Errorf("Expected hardware UUID %s, got %s", androidUUID, handshakeFromAndroid.HardwareUUID)
	}
	if handshakeFromAndroid.DeviceID == "" {
		t.Errorf("Android device ID is empty")
	}
	if handshakeFromAndroid.FirstName != "Android" {
		t.Errorf("Expected first name 'Android', got '%s'", handshakeFromAndroid.FirstName)
	}

	androidDeviceID := handshakeFromAndroid.DeviceID

	t.Logf("✅ iPhone received handshake from Android:")
	t.Logf("   Hardware UUID: %s", handshakeFromAndroid.HardwareUUID[:8])
	t.Logf("   Device ID: %s", androidDeviceID)
	t.Logf("   First Name: %s", handshakeFromAndroid.FirstName)

	// Verify Android discovered iPhone
	discoveredDevicesAndroid := droid.GetDiscovered()
	discoveredFromAndroid, foundIPhone := discoveredDevicesAndroid[iphoneUUID]

	if !foundIPhone {
		t.Fatalf("Android did not discover iPhone")
	}

	t.Logf("✅ Android discovered iPhone: %s", discoveredFromAndroid.Name)

	// Verify Android received handshake from iPhone
	handshakesAndroid := droid.GetHandshaked()
	handshakeFromIPhone, hasHandshakeFromIPhone := handshakesAndroid[iphoneUUID]

	if !hasHandshakeFromIPhone {
		t.Fatalf("Android did not receive handshake from iPhone")
	}

	// Verify handshake data from iPhone is correct (default name)
	if handshakeFromIPhone.HardwareUUID != iphoneUUID {
		t.Errorf("Expected hardware UUID %s, got %s", iphoneUUID, handshakeFromIPhone.HardwareUUID)
	}
	if handshakeFromIPhone.DeviceID == "" {
		t.Errorf("iPhone device ID is empty")
	}
	if handshakeFromIPhone.FirstName != "iPhone" {
		t.Errorf("Expected first name 'iPhone', got '%s'", handshakeFromIPhone.FirstName)
	}

	iphoneDeviceID := handshakeFromIPhone.DeviceID

	t.Logf("✅ Android received handshake from iPhone:")
	t.Logf("   Hardware UUID: %s", handshakeFromIPhone.HardwareUUID[:8])
	t.Logf("   Device ID: %s", iphoneDeviceID)
	t.Logf("   First Name: %s", handshakeFromIPhone.FirstName)

	t.Logf("✅ Baseline handshake verification complete (same as integration_test.go)")

	// ========================================
	// PART 2: Update first names and verify ProfileMessage exchange
	// ========================================

	// iPhone sets first name to "bob"
	err := ip.UpdateLocalProfile(map[string]string{"first_name": "bob"})
	if err != nil {
		t.Fatalf("Failed to update iPhone profile: %v", err)
	}

	// Android sets first name to "sue"
	err = droid.UpdateLocalProfile(map[string]string{"first_name": "sue"})
	if err != nil {
		t.Fatalf("Failed to update Android profile: %v", err)
	}

	t.Logf("📝 Updated profiles: iPhone → 'bob', Android → 'sue'")

	// Wait for ProfileMessage broadcast and receipt
	// UpdateLocalProfile triggers broadcastProfileUpdate() which sends ProfileMessage to all connected peers
	time.Sleep(2 * time.Second)

	// ========================================
	// PART 3: Verify profile data was received and persisted
	// ========================================

	// Verify iPhone received Android's profile ("sue")
	iphoneCacheManager := phone.NewDeviceCacheManager(iphoneUUID)
	androidProfileFromIPhone, err := iphoneCacheManager.LoadDeviceMetadata(androidDeviceID)
	if err != nil {
		t.Fatalf("iPhone failed to load Android's profile: %v", err)
	}

	if androidProfileFromIPhone.FirstName != "sue" {
		t.Errorf("iPhone expected to receive first name 'sue' from Android, got '%s'", androidProfileFromIPhone.FirstName)
	}
	if androidProfileFromIPhone.ProfileVersion != 1 {
		t.Errorf("Expected Android profileVersion 1, got %d", androidProfileFromIPhone.ProfileVersion)
	}

	t.Logf("✅ iPhone received Android's profile update:")
	t.Logf("   First Name: %s", androidProfileFromIPhone.FirstName)
	t.Logf("   Profile Version: %d", androidProfileFromIPhone.ProfileVersion)

	// Verify Android received iPhone's profile ("bob")
	androidCacheManager := phone.NewDeviceCacheManager(androidUUID)
	iphoneProfileFromAndroid, err := androidCacheManager.LoadDeviceMetadata(iphoneDeviceID)
	if err != nil {
		t.Fatalf("Android failed to load iPhone's profile: %v", err)
	}

	if iphoneProfileFromAndroid.FirstName != "bob" {
		t.Errorf("Android expected to receive first name 'bob' from iPhone, got '%s'", iphoneProfileFromAndroid.FirstName)
	}
	if iphoneProfileFromAndroid.ProfileVersion != 1 {
		t.Errorf("Expected iPhone profileVersion 1, got %d", iphoneProfileFromAndroid.ProfileVersion)
	}

	t.Logf("✅ Android received iPhone's profile update:")
	t.Logf("   First Name: %s", iphoneProfileFromAndroid.FirstName)
	t.Logf("   Profile Version: %d", iphoneProfileFromAndroid.ProfileVersion)

	// ========================================
	// Final summary
	// ========================================

	t.Logf("✅ Test complete:")
	t.Logf("   - Baseline handshake with default names: ✓")
	t.Logf("   - Custom first name updates: ✓")
	t.Logf("   - Bidirectional ProfileMessage exchange: ✓")
	t.Logf("   - Profile persistence to disk: ✓")

	// Cleanup
	ip.Stop()
	droid.Stop()

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
