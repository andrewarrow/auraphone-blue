package gatt

import (
	"encoding/binary"
	"testing"

	"github.com/user/auraphone-blue/util"
)

func TestParseReadByGroupTypeResponse(t *testing.T) {
	util.SetRandom()

	t.Run("parse 16-bit service UUIDs", func(t *testing.T) {
		// Response with two services with 16-bit UUIDs
		// Length = 6 (2 + 2 + 2)
		data := []byte{
			0x06,       // Length
			0x01, 0x00, // Start handle = 0x0001
			0x05, 0x00, // End handle = 0x0005
			0x00, 0x18, // UUID = 0x1800 (Generic Access)
			0x06, 0x00, // Start handle = 0x0006
			0x0A, 0x00, // End handle = 0x000A
			0x01, 0x18, // UUID = 0x1801 (Generic Attribute)
		}

		services, err := ParseReadByGroupTypeResponse(data)
		if err != nil {
			t.Fatalf("ParseReadByGroupTypeResponse failed: %v", err)
		}

		if len(services) != 2 {
			t.Fatalf("expected 2 services, got %d", len(services))
		}

		// Check first service
		if services[0].StartHandle != 0x0001 {
			t.Errorf("service 0 start handle = 0x%04X, want 0x0001", services[0].StartHandle)
		}
		if services[0].EndHandle != 0x0005 {
			t.Errorf("service 0 end handle = 0x%04X, want 0x0005", services[0].EndHandle)
		}
		expectedUUID := []byte{0x00, 0x18}
		if !bytesEqual(services[0].UUID, expectedUUID) {
			t.Errorf("service 0 UUID = %x, want %x", services[0].UUID, expectedUUID)
		}

		// Check second service
		if services[1].StartHandle != 0x0006 {
			t.Errorf("service 1 start handle = 0x%04X, want 0x0006", services[1].StartHandle)
		}
		if services[1].EndHandle != 0x000A {
			t.Errorf("service 1 end handle = 0x%04X, want 0x000A", services[1].EndHandle)
		}
		expectedUUID2 := []byte{0x01, 0x18}
		if !bytesEqual(services[1].UUID, expectedUUID2) {
			t.Errorf("service 1 UUID = %x, want %x", services[1].UUID, expectedUUID2)
		}
	})

	t.Run("parse 128-bit service UUIDs", func(t *testing.T) {
		// Response with one service with 128-bit UUID
		// Length = 20 (2 + 2 + 16)
		customUUID := []byte{
			0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
			0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10,
		}

		data := []byte{0x14} // Length = 20
		data = append(data, 0x10, 0x00) // Start handle = 0x0010
		data = append(data, 0x20, 0x00) // End handle = 0x0020
		data = append(data, customUUID...) // 128-bit UUID

		services, err := ParseReadByGroupTypeResponse(data)
		if err != nil {
			t.Fatalf("ParseReadByGroupTypeResponse failed: %v", err)
		}

		if len(services) != 1 {
			t.Fatalf("expected 1 service, got %d", len(services))
		}

		if services[0].StartHandle != 0x0010 {
			t.Errorf("start handle = 0x%04X, want 0x0010", services[0].StartHandle)
		}
		if services[0].EndHandle != 0x0020 {
			t.Errorf("end handle = 0x%04X, want 0x0020", services[0].EndHandle)
		}
		if !bytesEqual(services[0].UUID, customUUID) {
			t.Errorf("UUID = %x, want %x", services[0].UUID, customUUID)
		}
	})

	t.Run("invalid length", func(t *testing.T) {
		data := []byte{0x05} // Invalid length (not 6 or 20)
		_, err := ParseReadByGroupTypeResponse(data)
		if err == nil {
			t.Error("expected error for invalid length, got nil")
		}
	})

	t.Run("incomplete data", func(t *testing.T) {
		data := []byte{
			0x06,       // Length = 6
			0x01, 0x00, // Start handle
			0x05, 0x00, // End handle
			0x00,       // Incomplete UUID
		}
		_, err := ParseReadByGroupTypeResponse(data)
		if err == nil {
			t.Error("expected error for incomplete data, got nil")
		}
	})
}

func TestParseReadByTypeResponse(t *testing.T) {
	util.SetRandom()

	t.Run("parse 16-bit characteristic UUIDs", func(t *testing.T) {
		// Response with two characteristics with 16-bit UUIDs
		// Length = 7 (2 + 1 + 2 + 2)
		data := []byte{
			0x07,       // Length
			0x02, 0x00, // Declaration handle = 0x0002
			0x02,       // Properties = Read
			0x03, 0x00, // Value handle = 0x0003
			0x00, 0x2A, // UUID = 0x2A00 (Device Name)
			0x04, 0x00, // Declaration handle = 0x0004
			0x02,       // Properties = Read
			0x05, 0x00, // Value handle = 0x0005
			0x01, 0x2A, // UUID = 0x2A01 (Appearance)
		}

		chars, err := ParseReadByTypeResponse(data)
		if err != nil {
			t.Fatalf("ParseReadByTypeResponse failed: %v", err)
		}

		if len(chars) != 2 {
			t.Fatalf("expected 2 characteristics, got %d", len(chars))
		}

		// Check first characteristic
		if chars[0].DeclarationHandle != 0x0002 {
			t.Errorf("char 0 declaration handle = 0x%04X, want 0x0002", chars[0].DeclarationHandle)
		}
		if chars[0].Properties != 0x02 {
			t.Errorf("char 0 properties = 0x%02X, want 0x02", chars[0].Properties)
		}
		if chars[0].ValueHandle != 0x0003 {
			t.Errorf("char 0 value handle = 0x%04X, want 0x0003", chars[0].ValueHandle)
		}
		expectedUUID := []byte{0x00, 0x2A}
		if !bytesEqual(chars[0].UUID, expectedUUID) {
			t.Errorf("char 0 UUID = %x, want %x", chars[0].UUID, expectedUUID)
		}

		// Check second characteristic
		if chars[1].ValueHandle != 0x0005 {
			t.Errorf("char 1 value handle = 0x%04X, want 0x0005", chars[1].ValueHandle)
		}
	})

	t.Run("parse 128-bit characteristic UUIDs", func(t *testing.T) {
		// Response with one characteristic with 128-bit UUID
		// Length = 21 (2 + 1 + 2 + 16)
		customUUID := []byte{
			0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x00, 0x11,
			0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99,
		}

		data := []byte{0x15} // Length = 21
		data = append(data, 0x10, 0x00) // Declaration handle = 0x0010
		data = append(data, 0x1A)       // Properties = Read | Notify | Write
		data = append(data, 0x11, 0x00) // Value handle = 0x0011
		data = append(data, customUUID...) // 128-bit UUID

		chars, err := ParseReadByTypeResponse(data)
		if err != nil {
			t.Fatalf("ParseReadByTypeResponse failed: %v", err)
		}

		if len(chars) != 1 {
			t.Fatalf("expected 1 characteristic, got %d", len(chars))
		}

		if chars[0].DeclarationHandle != 0x0010 {
			t.Errorf("declaration handle = 0x%04X, want 0x0010", chars[0].DeclarationHandle)
		}
		if chars[0].Properties != 0x1A {
			t.Errorf("properties = 0x%02X, want 0x1A", chars[0].Properties)
		}
		if chars[0].ValueHandle != 0x0011 {
			t.Errorf("value handle = 0x%04X, want 0x0011", chars[0].ValueHandle)
		}
		if !bytesEqual(chars[0].UUID, customUUID) {
			t.Errorf("UUID = %x, want %x", chars[0].UUID, customUUID)
		}
	})
}

func TestParseFindInformationResponse(t *testing.T) {
	util.SetRandom()

	t.Run("parse 16-bit descriptor UUIDs", func(t *testing.T) {
		// Format 0x01: 16-bit UUIDs
		data := []byte{
			0x01,       // Format = 16-bit UUIDs
			0x04, 0x00, // Handle = 0x0004
			0x02, 0x29, // UUID = 0x2902 (CCCD)
			0x08, 0x00, // Handle = 0x0008
			0x01, 0x29, // UUID = 0x2901 (User Description)
		}

		descriptors, err := ParseFindInformationResponse(data)
		if err != nil {
			t.Fatalf("ParseFindInformationResponse failed: %v", err)
		}

		if len(descriptors) != 2 {
			t.Fatalf("expected 2 descriptors, got %d", len(descriptors))
		}

		// Check first descriptor (CCCD)
		if descriptors[0].Handle != 0x0004 {
			t.Errorf("descriptor 0 handle = 0x%04X, want 0x0004", descriptors[0].Handle)
		}
		expectedUUID := []byte{0x02, 0x29}
		if !bytesEqual(descriptors[0].UUID, expectedUUID) {
			t.Errorf("descriptor 0 UUID = %x, want %x", descriptors[0].UUID, expectedUUID)
		}

		// Check second descriptor
		if descriptors[1].Handle != 0x0008 {
			t.Errorf("descriptor 1 handle = 0x%04X, want 0x0008", descriptors[1].Handle)
		}
	})

	t.Run("parse 128-bit descriptor UUIDs", func(t *testing.T) {
		// Format 0x02: 128-bit UUIDs
		customUUID := []byte{
			0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70, 0x80,
			0x90, 0xA0, 0xB0, 0xC0, 0xD0, 0xE0, 0xF0, 0x00,
		}

		data := []byte{0x02} // Format = 128-bit UUIDs
		data = append(data, 0x10, 0x00) // Handle = 0x0010
		data = append(data, customUUID...) // 128-bit UUID

		descriptors, err := ParseFindInformationResponse(data)
		if err != nil {
			t.Fatalf("ParseFindInformationResponse failed: %v", err)
		}

		if len(descriptors) != 1 {
			t.Fatalf("expected 1 descriptor, got %d", len(descriptors))
		}

		if descriptors[0].Handle != 0x0010 {
			t.Errorf("handle = 0x%04X, want 0x0010", descriptors[0].Handle)
		}
		if !bytesEqual(descriptors[0].UUID, customUUID) {
			t.Errorf("UUID = %x, want %x", descriptors[0].UUID, customUUID)
		}
	})

	t.Run("invalid format", func(t *testing.T) {
		data := []byte{0x03} // Invalid format (not 0x01 or 0x02)
		_, err := ParseFindInformationResponse(data)
		if err == nil {
			t.Error("expected error for invalid format, got nil")
		}
	})
}

func TestBuildReadByGroupTypeResponse(t *testing.T) {
	util.SetRandom()

	t.Run("build 16-bit service response", func(t *testing.T) {
		services := []DiscoveredService{
			{
				UUID:        []byte{0x00, 0x18},
				StartHandle: 0x0001,
				EndHandle:   0x0005,
			},
			{
				UUID:        []byte{0x01, 0x18},
				StartHandle: 0x0006,
				EndHandle:   0x000A,
			},
		}

		data, err := BuildReadByGroupTypeResponse(services)
		if err != nil {
			t.Fatalf("BuildReadByGroupTypeResponse failed: %v", err)
		}

		// Verify format: [Length: 1][Data: N * Length]
		expectedLength := byte(6) // 2 + 2 + 2 (16-bit UUID)
		if data[0] != expectedLength {
			t.Errorf("length = %d, want %d", data[0], expectedLength)
		}

		// Parse it back to verify
		parsed, err := ParseReadByGroupTypeResponse(data)
		if err != nil {
			t.Fatalf("failed to parse built response: %v", err)
		}

		if len(parsed) != 2 {
			t.Fatalf("expected 2 services after round-trip, got %d", len(parsed))
		}

		if parsed[0].StartHandle != 0x0001 || parsed[0].EndHandle != 0x0005 {
			t.Errorf("service 0 handles = 0x%04X-0x%04X, want 0x0001-0x0005",
				parsed[0].StartHandle, parsed[0].EndHandle)
		}
	})

	t.Run("build 128-bit service response", func(t *testing.T) {
		customUUID := []byte{
			0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
			0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10,
		}

		services := []DiscoveredService{
			{
				UUID:        customUUID,
				StartHandle: 0x0010,
				EndHandle:   0x0020,
			},
		}

		data, err := BuildReadByGroupTypeResponse(services)
		if err != nil {
			t.Fatalf("BuildReadByGroupTypeResponse failed: %v", err)
		}

		expectedLength := byte(20) // 2 + 2 + 16 (128-bit UUID)
		if data[0] != expectedLength {
			t.Errorf("length = %d, want %d", data[0], expectedLength)
		}

		// Parse it back
		parsed, err := ParseReadByGroupTypeResponse(data)
		if err != nil {
			t.Fatalf("failed to parse built response: %v", err)
		}

		if !bytesEqual(parsed[0].UUID, customUUID) {
			t.Errorf("UUID = %x, want %x", parsed[0].UUID, customUUID)
		}
	})
}

func TestBuildReadByTypeResponse(t *testing.T) {
	util.SetRandom()

	t.Run("build 16-bit characteristic response", func(t *testing.T) {
		chars := []DiscoveredCharacteristic{
			{
				UUID:              []byte{0x00, 0x2A},
				Properties:        0x02, // Read
				ValueHandle:       0x0003,
				DeclarationHandle: 0x0002,
			},
		}

		data, err := BuildReadByTypeResponse(chars)
		if err != nil {
			t.Fatalf("BuildReadByTypeResponse failed: %v", err)
		}

		expectedLength := byte(7) // 2 + 1 + 2 + 2 (16-bit UUID)
		if data[0] != expectedLength {
			t.Errorf("length = %d, want %d", data[0], expectedLength)
		}

		// Parse it back
		parsed, err := ParseReadByTypeResponse(data)
		if err != nil {
			t.Fatalf("failed to parse built response: %v", err)
		}

		if len(parsed) != 1 {
			t.Fatalf("expected 1 characteristic after round-trip, got %d", len(parsed))
		}

		if parsed[0].ValueHandle != 0x0003 {
			t.Errorf("value handle = 0x%04X, want 0x0003", parsed[0].ValueHandle)
		}
		if parsed[0].Properties != 0x02 {
			t.Errorf("properties = 0x%02X, want 0x02", parsed[0].Properties)
		}
	})
}

func TestBuildFindInformationResponse(t *testing.T) {
	util.SetRandom()

	t.Run("build 16-bit descriptor response", func(t *testing.T) {
		descriptors := []DiscoveredDescriptor{
			{
				UUID:   []byte{0x02, 0x29}, // CCCD
				Handle: 0x0004,
			},
		}

		data, err := BuildFindInformationResponse(descriptors)
		if err != nil {
			t.Fatalf("BuildFindInformationResponse failed: %v", err)
		}

		if data[0] != 0x01 { // Format = 16-bit UUIDs
			t.Errorf("format = 0x%02X, want 0x01", data[0])
		}

		// Parse it back
		parsed, err := ParseFindInformationResponse(data)
		if err != nil {
			t.Fatalf("failed to parse built response: %v", err)
		}

		if len(parsed) != 1 {
			t.Fatalf("expected 1 descriptor after round-trip, got %d", len(parsed))
		}

		if parsed[0].Handle != 0x0004 {
			t.Errorf("handle = 0x%04X, want 0x0004", parsed[0].Handle)
		}
	})
}

func TestDiscoveryCache(t *testing.T) {
	util.SetRandom()

	t.Run("add and find services", func(t *testing.T) {
		cache := NewDiscoveryCache()

		service := DiscoveredService{
			UUID:        []byte{0x00, 0x18},
			StartHandle: 0x0001,
			EndHandle:   0x0005,
		}

		cache.AddService(service)

		if len(cache.Services) != 1 {
			t.Fatalf("expected 1 service, got %d", len(cache.Services))
		}

		if !cache.HasService([]byte{0x00, 0x18}) {
			t.Error("HasService returned false for added service")
		}

		if cache.HasService([]byte{0xFF, 0xFF}) {
			t.Error("HasService returned true for non-existent service")
		}
	})

	t.Run("add and find characteristics", func(t *testing.T) {
		cache := NewDiscoveryCache()

		char := DiscoveredCharacteristic{
			UUID:              []byte{0x00, 0x2A},
			Properties:        0x02,
			ValueHandle:       0x0003,
			DeclarationHandle: 0x0002,
		}

		cache.AddCharacteristic(0x0001, char)

		// Find by UUID
		foundChar, err := cache.FindCharacteristicByUUID([]byte{0x00, 0x2A})
		if err != nil {
			t.Fatalf("FindCharacteristicByUUID failed: %v", err)
		}

		if foundChar.ValueHandle != 0x0003 {
			t.Errorf("value handle = 0x%04X, want 0x0003", foundChar.ValueHandle)
		}

		// Get handle directly
		handle, err := cache.GetCharacteristicHandle([]byte{0x00, 0x2A})
		if err != nil {
			t.Fatalf("GetCharacteristicHandle failed: %v", err)
		}

		if handle != 0x0003 {
			t.Errorf("handle = 0x%04X, want 0x0003", handle)
		}
	})

	t.Run("characteristic not found", func(t *testing.T) {
		cache := NewDiscoveryCache()

		_, err := cache.FindCharacteristicByUUID([]byte{0xFF, 0xFF})
		if err == nil {
			t.Error("expected error for non-existent characteristic, got nil")
		}

		_, err = cache.GetCharacteristicHandle([]byte{0xFF, 0xFF})
		if err == nil {
			t.Error("expected error for non-existent characteristic, got nil")
		}
	})
}

func TestDiscoverFromDatabase(t *testing.T) {
	util.SetRandom()

	t.Run("discover services from database", func(t *testing.T) {
		db := NewAttributeDatabase()

		// Add Generic Access Service
		db.AddAttribute(UUIDPrimaryService, []byte{0x00, 0x18}, PermReadable)
		db.AddAttribute(UUIDCharacteristic, []byte{0x02, 0x02, 0x00, 0x00, 0x2A}, PermReadable) // Char decl
		db.AddAttribute([]byte{0x00, 0x2A}, []byte("TestDevice"), PermReadable) // Char value

		// Add Generic Attribute Service
		db.AddAttribute(UUIDPrimaryService, []byte{0x01, 0x18}, PermReadable)
		db.AddAttribute(UUIDCharacteristic, []byte{0x20, 0x05, 0x00, 0x05, 0x2A}, PermReadable) // Char decl
		db.AddAttribute([]byte{0x05, 0x2A}, []byte{0x00, 0x00, 0x00, 0x00}, PermReadable|PermWritable) // Char value

		services := DiscoverServicesFromDatabase(db, 0x0001, 0xFFFF)

		if len(services) != 2 {
			t.Fatalf("expected 2 services, got %d", len(services))
		}

		// Check first service (Generic Access)
		if !bytesEqual(services[0].UUID, []byte{0x00, 0x18}) {
			t.Errorf("service 0 UUID = %x, want [00 18]", services[0].UUID)
		}
		if services[0].StartHandle != 0x0001 {
			t.Errorf("service 0 start handle = 0x%04X, want 0x0001", services[0].StartHandle)
		}
		if services[0].EndHandle != 0x0003 {
			t.Errorf("service 0 end handle = 0x%04X, want 0x0003", services[0].EndHandle)
		}

		// Check second service (Generic Attribute)
		if !bytesEqual(services[1].UUID, []byte{0x01, 0x18}) {
			t.Errorf("service 1 UUID = %x, want [01 18]", services[1].UUID)
		}
	})

	t.Run("discover characteristics from database", func(t *testing.T) {
		db := NewAttributeDatabase()

		// Add service declaration
		db.AddAttribute(UUIDPrimaryService, []byte{0x00, 0x18}, PermReadable)

		// Add characteristic: Device Name (read-only)
		charValue := make([]byte, 5)
		charValue[0] = PropRead // Properties
		binary.LittleEndian.PutUint16(charValue[1:3], 0x0003) // Value handle
		copy(charValue[3:], []byte{0x00, 0x2A}) // UUID

		db.AddAttribute(UUIDCharacteristic, charValue, PermReadable) // Handle 0x0002
		db.AddAttribute([]byte{0x00, 0x2A}, []byte("TestDevice"), PermReadable) // Handle 0x0003

		chars := DiscoverCharacteristicsFromDatabase(db, 0x0001, 0xFFFF)

		if len(chars) != 1 {
			t.Fatalf("expected 1 characteristic, got %d", len(chars))
		}

		if chars[0].DeclarationHandle != 0x0002 {
			t.Errorf("declaration handle = 0x%04X, want 0x0002", chars[0].DeclarationHandle)
		}
		if chars[0].ValueHandle != 0x0003 {
			t.Errorf("value handle = 0x%04X, want 0x0003", chars[0].ValueHandle)
		}
		if chars[0].Properties != PropRead {
			t.Errorf("properties = 0x%02X, want 0x%02X", chars[0].Properties, PropRead)
		}
		if !bytesEqual(chars[0].UUID, []byte{0x00, 0x2A}) {
			t.Errorf("UUID = %x, want [00 2A]", chars[0].UUID)
		}
	})

	t.Run("discover descriptors from database", func(t *testing.T) {
		db := NewAttributeDatabase()

		// Add service and characteristic
		db.AddAttribute(UUIDPrimaryService, []byte{0x00, 0x18}, PermReadable) // 0x0001
		db.AddAttribute(UUIDCharacteristic, []byte{0x12, 0x03, 0x00, 0x00, 0x2A}, PermReadable) // 0x0002
		db.AddAttribute([]byte{0x00, 0x2A}, []byte("Test"), PermReadable|PermWritable) // 0x0003
		db.AddAttribute(UUIDClientCharacteristicConfig, []byte{0x00, 0x00}, PermReadable|PermWritable) // 0x0004 (CCCD)

		// Discover descriptors in the characteristic range (after value handle)
		descriptors := DiscoverDescriptorsFromDatabase(db, 0x0004, 0x0004)

		if len(descriptors) != 1 {
			t.Fatalf("expected 1 descriptor, got %d", len(descriptors))
		}

		if descriptors[0].Handle != 0x0004 {
			t.Errorf("descriptor handle = 0x%04X, want 0x0004", descriptors[0].Handle)
		}
		if !bytesEqual(descriptors[0].UUID, UUIDClientCharacteristicConfig) {
			t.Errorf("descriptor UUID = %x, want %x", descriptors[0].UUID, UUIDClientCharacteristicConfig)
		}
	})
}
