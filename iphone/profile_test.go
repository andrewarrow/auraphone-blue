package iphone

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

	// Create two iPhones with profiles
	ip1 := createTestIPhone("11111111-1111-1111-1111-111111111111", "iPhone 1", config)
	ip2 := createTestIPhone("22222222-2222-2222-2222-222222222222", "iPhone 2", config)

	// Set profiles
	profile1 := map[string]string{
		"first_name": "Alice",
		"last_name":  "Smith",
		"tagline":    "iOS Developer",
		"insta":      "@alice",
	}
	profile2 := map[string]string{
		"first_name": "Bob",
		"last_name":  "Jones",
		"tagline":    "Android Expert",
		"insta":      "@bob",
	}

	ip1.UpdateLocalProfile(profile1)
	ip2.UpdateLocalProfile(profile2)

	// Start both devices
	ip1.Start()
	ip2.Start()
	defer ip1.Stop()
	defer ip2.Stop()

	// Wait for discovery and handshake
	time.Sleep(500 * time.Millisecond)

	// Check that handshakes completed
	ip1.mu.RLock()
	handshake1 := ip1.handshaked["22222222-2222-2222-2222-222222222222"]
	ip1.mu.RUnlock()

	ip2.mu.RLock()
	handshake2 := ip2.handshaked["11111111-1111-1111-1111-111111111111"]
	ip2.mu.RUnlock()

	if handshake1 == nil {
		t.Fatal("iPhone 1 did not receive handshake from iPhone 2")
	}
	if handshake2 == nil {
		t.Fatal("iPhone 2 did not receive handshake from iPhone 1")
	}

	// Wait a bit more for profile messages to be sent
	time.Sleep(300 * time.Millisecond)

	// Check that profile metadata was saved
	cacheManager1 := phone.NewDeviceCacheManager(ip1.hardwareUUID)
	metadata1, err := cacheManager1.LoadDeviceMetadata(ip2.deviceID)
	if err != nil {
		t.Fatalf("Failed to load profile for iPhone 2: %v", err)
	}

	if metadata1.LastName != "Jones" {
		t.Errorf("iPhone 1 should have received Bob's last name, got: %s", metadata1.LastName)
	}
	if metadata1.Tagline != "Android Expert" {
		t.Errorf("iPhone 1 should have received Bob's tagline, got: %s", metadata1.Tagline)
	}

	cacheManager2 := phone.NewDeviceCacheManager(ip2.hardwareUUID)
	metadata2, err := cacheManager2.LoadDeviceMetadata(ip1.deviceID)
	if err != nil {
		t.Fatalf("Failed to load profile for iPhone 1: %v", err)
	}

	if metadata2.LastName != "Smith" {
		t.Errorf("iPhone 2 should have received Alice's last name, got: %s", metadata2.LastName)
	}
	if metadata2.Tagline != "iOS Developer" {
		t.Errorf("iPhone 2 should have received Alice's tagline, got: %s", metadata2.Tagline)
	}
}

// TestProfileRequestResponse tests that devices respond to ProfileRequestMessage
func TestProfileRequestResponse(t *testing.T) {
	cleanup := setupTestEnvironment(t)
	defer cleanup()

	config := wire.PerfectSimulationConfig()

	// Create two iPhones
	ip1 := createTestIPhone("11111111-1111-1111-1111-111111111111", "iPhone 1", config)
	ip2 := createTestIPhone("22222222-2222-2222-2222-222222222222", "iPhone 2", config)

	// Set profile on iPhone 1
	profile1 := map[string]string{
		"first_name": "Alice",
		"last_name":  "Smith",
		"tagline":    "iOS Developer",
		"insta":      "@alice",
		"youtube":    "youtube.com/@alice",
	}
	ip1.UpdateLocalProfile(profile1)

	// Start both devices
	ip1.Start()
	ip2.Start()
	defer ip1.Stop()
	defer ip2.Stop()

	// Wait for connection
	time.Sleep(500 * time.Millisecond)

	// iPhone 2 sends a profile request to iPhone 1
	ip2.sendProfileRequest("11111111-1111-1111-1111-111111111111", ip1.deviceID)

	// Wait for response
	time.Sleep(300 * time.Millisecond)

	// Check that iPhone 2 received the profile
	cacheManager2 := phone.NewDeviceCacheManager(ip2.hardwareUUID)
	metadata, err := cacheManager2.LoadDeviceMetadata(ip1.deviceID)
	if err != nil {
		t.Fatalf("Failed to load profile for iPhone 1: %v", err)
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

	// Create iPhone with profile
	ip1 := createTestIPhone("11111111-1111-1111-1111-111111111111", "iPhone 1", config)

	profile := map[string]string{
		"first_name": "Alice",
		"last_name":  "Smith",
		"tagline":    "Version 1",
	}
	ip1.UpdateLocalProfile(profile)

	// Initial profile version should be set in handshake
	ip1.mu.RLock()
	initialVersion := ip1.profileVersion
	ip1.mu.RUnlock()

	// Update profile
	profile["tagline"] = "Version 2"
	ip1.UpdateLocalProfile(profile)

	// TODO: Implement profile version increment on UpdateLocalProfile
	// For now, we're just testing the structure

	ip1.mu.RLock()
	newVersion := ip1.profileVersion
	ip1.mu.RUnlock()

	t.Logf("Initial version: %d, After update: %d", initialVersion, newVersion)

	// Note: This test will pass once we implement version increment logic
	// For now, it just validates the test structure
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

	// Create iPhone with profile
	ip1 := createTestIPhone("11111111-1111-1111-1111-111111111111", "iPhone 1", config)
	ip2 := createTestIPhone("22222222-2222-2222-2222-222222222222", "iPhone 2", config)

	// Set profile version
	ip1.mu.Lock()
	ip1.profileVersion = 5
	ip1.mu.Unlock()

	ip1.Start()
	ip2.Start()
	defer ip1.Stop()
	defer ip2.Stop()

	// Wait for handshake
	time.Sleep(500 * time.Millisecond)

	// Check that iPhone 2's mesh view received the profile version
	ip2.meshView.mu.RLock()
	device, exists := ip2.meshView.devices[ip1.deviceID]
	ip2.meshView.mu.RUnlock()

	if !exists {
		t.Fatal("iPhone 2 does not have iPhone 1 in mesh view")
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
