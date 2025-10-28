package android

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/wire"
	pb "github.com/user/auraphone-blue/proto"
	"google.golang.org/protobuf/proto"
)

// TestProfileMessageSentAfterHandshake tests that ProfileMessage is sent after handshake completes
func TestProfileMessageSentAfterHandshake(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	config := wire.PerfectSimulationConfig() // Deterministic for tests

	// Create two Android devices with profiles
	a1 := createTestAndroid("11111111-1111-1111-1111-111111111111", "Android 1", config)
	a2 := createTestAndroid("22222222-2222-2222-2222-222222222222", "Android 2", config)

	// Set profiles
	profile1 := map[string]string{
		"first_name": "Alice",
		"last_name":  "Smith",
		"tagline":    "Android Developer",
		"insta":      "@alice",
	}
	profile2 := map[string]string{
		"first_name": "Bob",
		"last_name":  "Jones",
		"tagline":    "iOS Expert",
		"insta":      "@bob",
	}

	a1.UpdateLocalProfile(profile1)
	a2.UpdateLocalProfile(profile2)

	// Start both devices
	a1.Start()
	a2.Start()
	defer a1.Stop()
	defer a2.Stop()

	// Wait for discovery and handshake
	time.Sleep(500 * time.Millisecond)

	// Check that handshakes completed
	a1.mu.RLock()
	handshake1 := a1.handshaked["22222222-2222-2222-2222-222222222222"]
	a1.mu.RUnlock()

	a2.mu.RLock()
	handshake2 := a2.handshaked["11111111-1111-1111-1111-111111111111"]
	a2.mu.RUnlock()

	if handshake1 == nil {
		t.Fatal("Android 1 did not receive handshake from Android 2")
	}
	if handshake2 == nil {
		t.Fatal("Android 2 did not receive handshake from Android 1")
	}

	// Wait a bit more for profile messages to be sent
	time.Sleep(300 * time.Millisecond)

	// Check that profile metadata was saved
	cacheManager1 := phone.NewDeviceCacheManager(a1.hardwareUUID)
	metadata1, err := cacheManager1.LoadDeviceMetadata(a2.deviceID)
	if err != nil {
		t.Fatalf("Failed to load profile for Android 2: %v", err)
	}

	if metadata1.LastName != "Jones" {
		t.Errorf("Android 1 should have received Bob's last name, got: %s", metadata1.LastName)
	}
	if metadata1.Tagline != "iOS Expert" {
		t.Errorf("Android 1 should have received Bob's tagline, got: %s", metadata1.Tagline)
	}

	cacheManager2 := phone.NewDeviceCacheManager(a2.hardwareUUID)
	metadata2, err := cacheManager2.LoadDeviceMetadata(a1.deviceID)
	if err != nil {
		t.Fatalf("Failed to load profile for Android 1: %v", err)
	}

	if metadata2.LastName != "Smith" {
		t.Errorf("Android 2 should have received Alice's last name, got: %s", metadata2.LastName)
	}
	if metadata2.Tagline != "Android Developer" {
		t.Errorf("Android 2 should have received Alice's tagline, got: %s", metadata2.Tagline)
	}
}

// TestProfileRequestResponse tests that devices respond to ProfileRequestMessage
func TestProfileRequestResponse(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	config := wire.PerfectSimulationConfig()

	// Create two Android devices
	a1 := createTestAndroid("11111111-1111-1111-1111-111111111111", "Android 1", config)
	a2 := createTestAndroid("22222222-2222-2222-2222-222222222222", "Android 2", config)

	// Set profile on Android 1
	profile1 := map[string]string{
		"first_name": "Alice",
		"last_name":  "Smith",
		"tagline":    "Android Developer",
		"insta":      "@alice",
		"youtube":    "youtube.com/@alice",
	}
	a1.UpdateLocalProfile(profile1)

	// Start both devices
	a1.Start()
	a2.Start()
	defer a1.Stop()
	defer a2.Stop()

	// Wait for connection
	time.Sleep(500 * time.Millisecond)

	// Android 2 sends a profile request to Android 1
	a2.sendProfileRequest("11111111-1111-1111-1111-111111111111", a1.deviceID)

	// Wait for response
	time.Sleep(300 * time.Millisecond)

	// Check that Android 2 received the profile
	cacheManager2 := phone.NewDeviceCacheManager(a2.hardwareUUID)
	metadata, err := cacheManager2.LoadDeviceMetadata(a1.deviceID)
	if err != nil {
		t.Fatalf("Failed to load profile for Android 1: %v", err)
	}

	if metadata.LastName != "Smith" {
		t.Errorf("Expected last name 'Smith', got: %s", metadata.LastName)
	}
	if metadata.YouTube != "youtube.com/@alice" {
		t.Errorf("Expected YouTube 'youtube.com/@alice', got: %s", metadata.YouTube)
	}
}

// TestProfileVersionIncrement tests that profile version increments
func TestProfileVersionIncrement(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	config := wire.PerfectSimulationConfig()

	// Create Android with profile
	a1 := createTestAndroid("11111111-1111-1111-1111-111111111111", "Android 1", config)

	profile := map[string]string{
		"first_name": "Alice",
		"last_name":  "Smith",
		"tagline":    "Version 1",
	}
	a1.UpdateLocalProfile(profile)

	// Initial profile version
	a1.mu.RLock()
	initialVersion := a1.profileVersion
	a1.mu.RUnlock()

	if initialVersion != 2 {
		t.Errorf("Expected initial version 2 (1 + first update), got: %d", initialVersion)
	}

	// Update profile again
	profile["tagline"] = "Version 2"
	a1.UpdateLocalProfile(profile)

	a1.mu.RLock()
	newVersion := a1.profileVersion
	a1.mu.RUnlock()

	if newVersion != 3 {
		t.Errorf("Expected version 3 after second update, got: %d", newVersion)
	}

	// Update with same values (should not increment)
	a1.UpdateLocalProfile(profile)

	a1.mu.RLock()
	unchangedVersion := a1.profileVersion
	a1.mu.RUnlock()

	if unchangedVersion != 3 {
		t.Errorf("Expected version to remain 3 when no changes, got: %d", unchangedVersion)
	}
}

// TestProfileMessageParsing tests that we can correctly parse ProfileMessage
func TestProfileMessageParsing(t *testing.T) {
	// Create a profile message
	profileMsg := &pb.ProfileMessage{
		DeviceId:    "TESTDEV1",
		LastName:    "Smith",
		PhoneNumber: "+1234567890",
		Tagline:     "Test tagline",
		Insta:       "@testuser",
	}

	data, err := proto.Marshal(profileMsg)
	if err != nil {
		t.Fatalf("Failed to marshal ProfileMessage: %v", err)
	}

	// Test the discriminator logic used in handleProtocolMessage
	var parsedProfile pb.ProfileMessage
	err = proto.Unmarshal(data, &parsedProfile)
	if err != nil {
		t.Fatalf("Failed to unmarshal ProfileMessage: %v", err)
	}

	// Check that we can identify it as a profile message
	isProfile := parsedProfile.DeviceId != "" &&
		(parsedProfile.PhoneNumber != "" || parsedProfile.Tagline != "" || parsedProfile.Insta != "")

	if !isProfile {
		t.Error("Failed to identify ProfileMessage using discriminator logic")
	}

	// Verify it's NOT identified as a handshake
	var testHandshake pb.HandshakeMessage
	proto.Unmarshal(data, &testHandshake)
	isHandshake := testHandshake.DeviceId != "" && testHandshake.ProtocolVersion > 0

	if isHandshake {
		t.Error("ProfileMessage should not be identified as HandshakeMessage")
	}
}

// TestHandshakeIncludesProfileVersion tests that HandshakeMessage includes profile_version
func TestHandshakeIncludesProfileVersion(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	config := wire.PerfectSimulationConfig()

	// Create Android devices
	a1 := createTestAndroid("11111111-1111-1111-1111-111111111111", "Android 1", config)
	a2 := createTestAndroid("22222222-2222-2222-2222-222222222222", "Android 2", config)

	// Set profile version
	a1.mu.Lock()
	a1.profileVersion = 5
	a1.mu.Unlock()

	a1.Start()
	a2.Start()
	defer a1.Stop()
	defer a2.Stop()

	// Wait for handshake
	time.Sleep(500 * time.Millisecond)

	// Check that Android 2's mesh view received the profile version
	a2.meshView.mu.RLock()
	device, exists := a2.meshView.devices[a1.deviceID]
	a2.meshView.mu.RUnlock()

	if !exists {
		t.Fatal("Android 2 does not have Android 1 in mesh view")
	}

	if device.ProfileVersion != 5 {
		t.Errorf("Expected profile version 5, got: %d", device.ProfileVersion)
	}
}

// Helper function to setup test environment
func setupTestEnvironment(t *testing.T) func() {
	// Create temp data directory
	tempDir := filepath.Join(os.TempDir(), "auraphone-test", t.Name())
	os.RemoveAll(tempDir)
	os.MkdirAll(tempDir, 0755)

	// Override data directory
	originalGetDataDir := phone.GetDataDir
	phone.GetDataDir = func() string {
		return tempDir
	}

	return func() {
		phone.GetDataDir = originalGetDataDir
		os.RemoveAll(tempDir)
	}
}
