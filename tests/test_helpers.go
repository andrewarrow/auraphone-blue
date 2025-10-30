package main

import (
	"testing"
	"time"

	"github.com/user/auraphone-blue/android"
	"github.com/user/auraphone-blue/iphone"
	"github.com/user/auraphone-blue/phone"
)

// setupTestDevices creates fresh iPhone and Android devices.
// Returns the initialized devices ready to be started.
// Note: util.SetRandom() must be called at the start of each test to create isolated temp directory.
func setupTestDevices(t *testing.T, iphoneUUID, androidUUID string) (*iphone.IPhone, *android.Android) {
	// Create devices with default settings
	ip := iphone.NewIPhone(iphoneUUID)
	droid := android.NewAndroid(androidUUID)

	// Verify default first names
	if ip.GetFirstName() != "iPhone" {
		t.Errorf("Expected iPhone first name to be 'iPhone', got '%s'", ip.GetFirstName())
	}
	if droid.GetFirstName() != "Android" {
		t.Errorf("Expected Android first name to be 'Android', got '%s'", droid.GetFirstName())
	}

	return ip, droid
}

// startAndWaitForHandshake starts both devices and waits for discovery, connection, and initial handshake.
// BLE discovery takes ~100-500ms, connection takes ~30-100ms, handshake is immediate.
func startAndWaitForHandshake(ip *iphone.IPhone, droid *android.Android) {
	ip.Start()
	droid.Start()
	time.Sleep(3 * time.Second)
}

// verifyBasicHandshake verifies that both devices discovered each other and exchanged handshakes
// with default first names ("iPhone" and "Android"). Returns the base36 device IDs for further testing.
// This is the baseline verification that should pass in all tests.
func verifyBasicHandshake(t *testing.T, ip *iphone.IPhone, droid *android.Android, iphoneUUID, androidUUID string) (iphoneDeviceID, androidDeviceID string) {
	// ========================================
	// Verify iPhone side
	// ========================================

	// 1. Verify iPhone discovered Android
	discoveredDevices := ip.GetDiscovered()
	discoveredFromIPhone, foundAndroid := discoveredDevices[androidUUID]

	if !foundAndroid {
		t.Fatalf("iPhone did not discover Android")
	}

	t.Logf("✅ iPhone discovered Android: %s", discoveredFromIPhone.Name)

	// 2. Verify iPhone received handshake from Android
	handshakes := ip.GetHandshaked()
	handshakeFromAndroid, hasHandshake := handshakes[androidUUID]

	if !hasHandshake {
		t.Fatalf("iPhone did not receive handshake from Android")
	}

	// 3. Verify handshake data from Android is correct (default name)
	// Note: HardwareUUID is no longer in handshake (never sent over BLE)
	// It's only used as the map key (peripheralUUID for routing)
	if handshakeFromAndroid.DeviceID == "" {
		t.Errorf("Android device ID is empty")
	}
	if handshakeFromAndroid.FirstName != "Android" {
		t.Errorf("Expected first name 'Android', got '%s'", handshakeFromAndroid.FirstName)
	}

	androidDeviceID = handshakeFromAndroid.DeviceID

	t.Logf("✅ iPhone received handshake from Android:")
	t.Logf("   Peripheral UUID (routing key): %s", androidUUID[:8])
	t.Logf("   Device ID (identity): %s", androidDeviceID)
	t.Logf("   First Name: %s", handshakeFromAndroid.FirstName)

	// ========================================
	// Verify Android side
	// ========================================

	// 1. Verify Android discovered iPhone
	discoveredDevicesAndroid := droid.GetDiscovered()
	discoveredFromAndroid, foundIPhone := discoveredDevicesAndroid[iphoneUUID]

	if !foundIPhone {
		t.Fatalf("Android did not discover iPhone")
	}

	t.Logf("✅ Android discovered iPhone: %s", discoveredFromAndroid.Name)

	// 2. Verify Android received handshake from iPhone
	handshakesAndroid := droid.GetHandshaked()
	handshakeFromIPhone, hasHandshakeFromIPhone := handshakesAndroid[iphoneUUID]

	if !hasHandshakeFromIPhone {
		t.Fatalf("Android did not receive handshake from iPhone")
	}

	// 3. Verify handshake data from iPhone is correct (default name)
	// Note: HardwareUUID is no longer in handshake (never sent over BLE)
	// It's only used as the map key (peripheralUUID for routing)
	if handshakeFromIPhone.DeviceID == "" {
		t.Errorf("iPhone device ID is empty")
	}
	if handshakeFromIPhone.FirstName != "iPhone" {
		t.Errorf("Expected first name 'iPhone', got '%s'", handshakeFromIPhone.FirstName)
	}

	iphoneDeviceID = handshakeFromIPhone.DeviceID

	t.Logf("✅ Android received handshake from iPhone:")
	t.Logf("   Peripheral UUID (routing key): %s", iphoneUUID[:8])
	t.Logf("   Device ID (identity): %s", iphoneDeviceID)
	t.Logf("   First Name: %s", handshakeFromIPhone.FirstName)

	t.Logf("✅ Baseline handshake verification complete")

	return iphoneDeviceID, androidDeviceID
}

// verifyProfileReceived verifies that a device received and persisted a profile update from a peer.
// localDeviceUUID: The hardware UUID of the device that received the profile
// remoteDeviceID: The base36 device ID of the peer whose profile was received
// expectedFirstName: The first name we expect to see in the received profile
// expectedVersion: The profile version we expect
func verifyProfileReceived(t *testing.T, localDeviceUUID, remoteDeviceID, expectedFirstName string, expectedVersion int32) {
	cacheManager := phone.NewDeviceCacheManager(localDeviceUUID)
	metadata, err := cacheManager.LoadDeviceMetadata(remoteDeviceID)
	if err != nil {
		t.Fatalf("Device %s failed to load profile for %s: %v", localDeviceUUID[:8], remoteDeviceID, err)
	}

	if metadata.FirstName != expectedFirstName {
		t.Errorf("Device %s expected to receive first name '%s' from %s, got '%s'",
			localDeviceUUID[:8], expectedFirstName, remoteDeviceID, metadata.FirstName)
	}
	if metadata.ProfileVersion != expectedVersion {
		t.Errorf("Device %s expected profile version %d from %s, got %d",
			localDeviceUUID[:8], expectedVersion, remoteDeviceID, metadata.ProfileVersion)
	}

	t.Logf("✅ Device %s received profile from %s:", localDeviceUUID[:8], remoteDeviceID)
	t.Logf("   First Name: %s", metadata.FirstName)
	t.Logf("   Profile Version: %d", metadata.ProfileVersion)
}

// cleanupDevices stops both devices and performs any necessary cleanup.
func cleanupDevices(ip *iphone.IPhone, droid *android.Android) {
	ip.Stop()
	droid.Stop()
}
