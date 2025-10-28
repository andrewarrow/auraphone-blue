package wire

import (
	"testing"
	"time"

	pb "github.com/user/auraphone-blue/proto"
	"google.golang.org/protobuf/proto"
)

// TestProfileMessageSerialization tests that ProfileMessage can be marshaled and unmarshaled
func TestProfileMessageSerialization(t *testing.T) {
	// Create a profile message
	original := &pb.ProfileMessage{
		DeviceId:    "TESTDEV1",
		LastName:    "Smith",
		PhoneNumber: "+1234567890",
		Tagline:     "Test tagline",
		Insta:       "@testuser",
		Linkedin:    "linkedin.com/in/testuser",
		Youtube:     "youtube.com/@testuser",
		Tiktok:      "@testuser",
		Gmail:       "test@gmail.com",
		Imessage:    "+1234567890",
		Whatsapp:    "+1234567890",
		Signal:      "+1234567890",
		Telegram:    "@testuser",
	}

	// Marshal to bytes
	data, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("Failed to marshal ProfileMessage: %v", err)
	}

	// Unmarshal back
	var decoded pb.ProfileMessage
	err = proto.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Failed to unmarshal ProfileMessage: %v", err)
	}

	// Verify all fields
	if decoded.DeviceId != original.DeviceId {
		t.Errorf("DeviceId mismatch: got %s, want %s", decoded.DeviceId, original.DeviceId)
	}
	if decoded.LastName != original.LastName {
		t.Errorf("LastName mismatch: got %s, want %s", decoded.LastName, original.LastName)
	}
	if decoded.Tagline != original.Tagline {
		t.Errorf("Tagline mismatch: got %s, want %s", decoded.Tagline, original.Tagline)
	}
	if decoded.Insta != original.Insta {
		t.Errorf("Insta mismatch: got %s, want %s", decoded.Insta, original.Insta)
	}
}

// TestProfileRequestMessageSerialization tests that ProfileRequestMessage can be marshaled and unmarshaled
func TestProfileRequestMessageSerialization(t *testing.T) {
	original := &pb.ProfileRequestMessage{
		RequesterDeviceId: "REQUESTER",
		TargetDeviceId:    "TARGET",
		ExpectedVersion:   5,
	}

	// Marshal to bytes
	data, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("Failed to marshal ProfileRequestMessage: %v", err)
	}

	// Unmarshal back
	var decoded pb.ProfileRequestMessage
	err = proto.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Failed to unmarshal ProfileRequestMessage: %v", err)
	}

	// Verify all fields
	if decoded.RequesterDeviceId != original.RequesterDeviceId {
		t.Errorf("RequesterDeviceId mismatch: got %s, want %s", decoded.RequesterDeviceId, original.RequesterDeviceId)
	}
	if decoded.TargetDeviceId != original.TargetDeviceId {
		t.Errorf("TargetDeviceId mismatch: got %s, want %s", decoded.TargetDeviceId, original.TargetDeviceId)
	}
	if decoded.ExpectedVersion != original.ExpectedVersion {
		t.Errorf("ExpectedVersion mismatch: got %d, want %d", decoded.ExpectedVersion, original.ExpectedVersion)
	}
}

// TestDeviceStateWithProfileVersion tests that DeviceState includes profile_version
func TestDeviceStateWithProfileVersion(t *testing.T) {
	photoHash := []byte{0x01, 0x02, 0x03, 0x04}

	original := &pb.DeviceState{
		DeviceId:          "TESTDEV1",
		PhotoHash:         photoHash,
		LastSeenTimestamp: time.Now().Unix(),
		FirstName:         "John",
		ProfileVersion:    3,
	}

	// Marshal to bytes
	data, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("Failed to marshal DeviceState: %v", err)
	}

	// Unmarshal back
	var decoded pb.DeviceState
	err = proto.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Failed to unmarshal DeviceState: %v", err)
	}

	// Verify profile version is preserved
	if decoded.ProfileVersion != original.ProfileVersion {
		t.Errorf("ProfileVersion mismatch: got %d, want %d", decoded.ProfileVersion, original.ProfileVersion)
	}
	if decoded.FirstName != original.FirstName {
		t.Errorf("FirstName mismatch: got %s, want %s", decoded.FirstName, original.FirstName)
	}
}

// TestHandshakeWithProfileVersion tests that HandshakeMessage includes profile_version
func TestHandshakeWithProfileVersion(t *testing.T) {
	photoHash := []byte{0x01, 0x02, 0x03, 0x04}

	original := &pb.HandshakeMessage{
		DeviceId:        "TESTDEV1",
		FirstName:       "John",
		ProtocolVersion: 1,
		TxPhotoHash:     photoHash,
		ProfileVersion:  2,
	}

	// Marshal to bytes
	data, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("Failed to marshal HandshakeMessage: %v", err)
	}

	// Unmarshal back
	var decoded pb.HandshakeMessage
	err = proto.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Failed to unmarshal HandshakeMessage: %v", err)
	}

	// Verify profile version is preserved
	if decoded.ProfileVersion != original.ProfileVersion {
		t.Errorf("ProfileVersion mismatch: got %d, want %d", decoded.ProfileVersion, original.ProfileVersion)
	}
	if decoded.ProtocolVersion != original.ProtocolVersion {
		t.Errorf("ProtocolVersion mismatch: got %d, want %d", decoded.ProtocolVersion, original.ProtocolVersion)
	}
}

// TestProfileMessageDistinctFromHandshake tests that we can distinguish ProfileMessage from HandshakeMessage
func TestProfileMessageDistinctFromHandshake(t *testing.T) {
	// Create a handshake message
	handshake := &pb.HandshakeMessage{
		DeviceId:        "TESTDEV1",
		FirstName:       "John",
		ProtocolVersion: 1,
		ProfileVersion:  1,
	}

	handshakeData, err := proto.Marshal(handshake)
	if err != nil {
		t.Fatalf("Failed to marshal HandshakeMessage: %v", err)
	}

	// Create a profile message
	profile := &pb.ProfileMessage{
		DeviceId: "TESTDEV1",
		LastName: "Smith",
		Tagline:  "Test tagline",
	}

	profileData, err := proto.Marshal(profile)
	if err != nil {
		t.Fatalf("Failed to marshal ProfileMessage: %v", err)
	}

	// Try to parse handshake as profile (should fail to identify correctly)
	var wrongProfile pb.ProfileMessage
	proto.Unmarshal(handshakeData, &wrongProfile)

	// Handshake should have ProtocolVersion but profile should not have meaningful content
	var testHandshake pb.HandshakeMessage
	proto.Unmarshal(handshakeData, &testHandshake)
	if testHandshake.ProtocolVersion != 1 {
		t.Error("HandshakeMessage should have ProtocolVersion=1")
	}

	// Profile should have LastName/Tagline but handshake should not
	var testProfile pb.ProfileMessage
	proto.Unmarshal(profileData, &testProfile)
	if testProfile.LastName != "Smith" {
		t.Error("ProfileMessage should have LastName")
	}
	if testProfile.Tagline != "Test tagline" {
		t.Error("ProfileMessage should have Tagline")
	}

	// The discriminator: ProtocolVersion > 0 means handshake, LastName/Tagline/Insta != "" means profile
	var parsedHandshake pb.HandshakeMessage
	proto.Unmarshal(handshakeData, &parsedHandshake)
	isHandshake := parsedHandshake.DeviceId != "" && parsedHandshake.ProtocolVersion > 0
	if !isHandshake {
		t.Error("Should identify handshake by ProtocolVersion > 0")
	}

	var parsedProfile pb.ProfileMessage
	proto.Unmarshal(profileData, &parsedProfile)
	isProfile := parsedProfile.DeviceId != "" && (parsedProfile.LastName != "" || parsedProfile.Tagline != "" || parsedProfile.Insta != "")
	if !isProfile {
		t.Error("Should identify profile by LastName/Tagline/Insta fields")
	}
}
