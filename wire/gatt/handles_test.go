package gatt

import (
	"testing"

	"github.com/user/auraphone-blue/util"
)

func TestAttributeDatabaseBasics(t *testing.T) {
	util.SetRandom()

	db := NewAttributeDatabase()

	// Add a few attributes
	handle1 := db.AddAttribute(UUIDPrimaryService, []byte{0x01, 0x18}, PermReadable)
	handle2 := db.AddAttribute(UUIDCharacteristic, []byte{0x00}, PermReadable)
	handle3 := db.AddAttribute(UUID16(0x2A00), []byte("Device Name"), PermReadable|PermWritable)

	// Verify handles are sequential starting from 1
	if handle1 != 0x0001 {
		t.Errorf("First handle = 0x%04X, want 0x0001", handle1)
	}
	if handle2 != 0x0002 {
		t.Errorf("Second handle = 0x%04X, want 0x0002", handle2)
	}
	if handle3 != 0x0003 {
		t.Errorf("Third handle = 0x%04X, want 0x0003", handle3)
	}

	// Verify count
	if db.Count() != 3 {
		t.Errorf("Count = %d, want 3", db.Count())
	}
}

func TestGetAttribute(t *testing.T) {
	util.SetRandom()

	db := NewAttributeDatabase()

	// Add an attribute
	value := []byte("Test Value")
	handle := db.AddAttribute(UUID16(0x2A00), value, PermReadable)

	// Retrieve it
	attr, err := db.GetAttribute(handle)
	if err != nil {
		t.Fatalf("GetAttribute failed: %v", err)
	}

	if attr.Handle != handle {
		t.Errorf("Handle = 0x%04X, want 0x%04X", attr.Handle, handle)
	}

	if string(attr.Value) != string(value) {
		t.Errorf("Value = %s, want %s", string(attr.Value), string(value))
	}

	if attr.Permissions != PermReadable {
		t.Errorf("Permissions = 0x%02X, want 0x%02X", attr.Permissions, PermReadable)
	}

	// Try to get non-existent handle
	_, err = db.GetAttribute(0x9999)
	if err == nil {
		t.Error("GetAttribute should fail for invalid handle")
	}
}

func TestSetAttributeValue(t *testing.T) {
	util.SetRandom()

	db := NewAttributeDatabase()

	// Add an attribute
	handle := db.AddAttribute(UUID16(0x2A00), []byte("Initial"), PermWritable)

	// Update its value
	newValue := []byte("Updated")
	err := db.SetAttributeValue(handle, newValue)
	if err != nil {
		t.Fatalf("SetAttributeValue failed: %v", err)
	}

	// Verify it was updated
	attr, err := db.GetAttribute(handle)
	if err != nil {
		t.Fatalf("GetAttribute failed: %v", err)
	}

	if string(attr.Value) != string(newValue) {
		t.Errorf("Value = %s, want %s", string(attr.Value), string(newValue))
	}

	// Try to set value on non-existent handle
	err = db.SetAttributeValue(0x9999, []byte("test"))
	if err == nil {
		t.Error("SetAttributeValue should fail for invalid handle")
	}
}

func TestFindAttributesByType(t *testing.T) {
	util.SetRandom()

	db := NewAttributeDatabase()

	// Add various attributes
	svc1 := db.AddAttribute(UUIDPrimaryService, []byte{0x01, 0x18}, PermReadable)
	char1 := db.AddAttribute(UUIDCharacteristic, []byte{0x00}, PermReadable)
	val1 := db.AddAttribute(UUID16(0x2A00), []byte("Name1"), PermReadable)
	svc2 := db.AddAttribute(UUIDPrimaryService, []byte{0x0D, 0x18}, PermReadable)
	char2 := db.AddAttribute(UUIDCharacteristic, []byte{0x00}, PermReadable)
	val2 := db.AddAttribute(UUID16(0x2A01), []byte("Name2"), PermReadable)

	// Find all primary services
	services := db.FindAttributesByType(0x0001, 0xFFFF, UUIDPrimaryService)
	if len(services) != 2 {
		t.Errorf("Found %d services, want 2", len(services))
	}

	if services[0] != svc1 || services[1] != svc2 {
		t.Errorf("Service handles = %v, want [%d, %d]", services, svc1, svc2)
	}

	// Find all characteristics
	chars := db.FindAttributesByType(0x0001, 0xFFFF, UUIDCharacteristic)
	if len(chars) != 2 {
		t.Errorf("Found %d characteristics, want 2", len(chars))
	}

	if chars[0] != char1 || chars[1] != char2 {
		t.Errorf("Characteristic handles = %v, want [%d, %d]", chars, char1, char2)
	}

	// Find within a limited range
	charsLimited := db.FindAttributesByType(0x0001, 0x0003, UUIDCharacteristic)
	if len(charsLimited) != 1 {
		t.Errorf("Found %d characteristics in range, want 1", len(charsLimited))
	}

	// Find non-existent type
	none := db.FindAttributesByType(0x0001, 0xFFFF, UUID16(0x9999))
	if len(none) != 0 {
		t.Errorf("Found %d attributes of non-existent type, want 0", len(none))
	}

	_ = val1
	_ = val2
}

func TestGetHandleRange(t *testing.T) {
	util.SetRandom()

	db := NewAttributeDatabase()

	// Empty database
	first, last := db.GetHandleRange()
	if first != 0 || last != 0 {
		t.Errorf("Empty database range = [0x%04X, 0x%04X], want [0, 0]", first, last)
	}

	// Add some attributes
	db.AddAttribute(UUIDPrimaryService, []byte{0x01, 0x18}, PermReadable)
	db.AddAttribute(UUIDCharacteristic, []byte{0x00}, PermReadable)
	db.AddAttribute(UUID16(0x2A00), []byte("Test"), PermReadable)

	first, last = db.GetHandleRange()
	if first != 0x0001 || last != 0x0003 {
		t.Errorf("Handle range = [0x%04X, 0x%04X], want [0x0001, 0x0003]", first, last)
	}
}

func TestGetAllHandles(t *testing.T) {
	util.SetRandom()

	db := NewAttributeDatabase()

	// Empty database
	handles := db.GetAllHandles()
	if len(handles) != 0 {
		t.Errorf("Empty database has %d handles, want 0", len(handles))
	}

	// Add attributes
	db.AddAttribute(UUIDPrimaryService, []byte{0x01, 0x18}, PermReadable)
	db.AddAttribute(UUIDCharacteristic, []byte{0x00}, PermReadable)
	db.AddAttribute(UUID16(0x2A00), []byte("Test"), PermReadable)

	handles = db.GetAllHandles()
	if len(handles) != 3 {
		t.Errorf("Database has %d handles, want 3", len(handles))
	}

	// Verify handles are in order
	expected := []uint16{0x0001, 0x0002, 0x0003}
	for i, h := range handles {
		if h != expected[i] {
			t.Errorf("Handle[%d] = 0x%04X, want 0x%04X", i, h, expected[i])
		}
	}
}

func TestClear(t *testing.T) {
	util.SetRandom()

	db := NewAttributeDatabase()

	// Add some attributes
	db.AddAttribute(UUIDPrimaryService, []byte{0x01, 0x18}, PermReadable)
	db.AddAttribute(UUIDCharacteristic, []byte{0x00}, PermReadable)

	if db.Count() != 2 {
		t.Errorf("Count before clear = %d, want 2", db.Count())
	}

	// Clear
	db.Clear()

	if db.Count() != 0 {
		t.Errorf("Count after clear = %d, want 0", db.Count())
	}

	// Verify next handle is reset
	handle := db.AddAttribute(UUIDPrimaryService, []byte{0x01, 0x18}, PermReadable)
	if handle != 0x0001 {
		t.Errorf("First handle after clear = 0x%04X, want 0x0001", handle)
	}
}

func TestUUIDHelpers(t *testing.T) {
	util.SetRandom()

	// Test UUID16
	uuid := UUID16(0x1234)
	if len(uuid) != 2 {
		t.Errorf("UUID16 length = %d, want 2", len(uuid))
	}
	if uuid[0] != 0x34 || uuid[1] != 0x12 {
		t.Errorf("UUID16(0x1234) = %v, want [0x34, 0x12]", uuid)
	}

	if !IsUUID16(uuid) {
		t.Error("IsUUID16 should return true for 2-byte UUID")
	}

	// Test UUID128
	uuid128 := UUID128(0x1800)
	if len(uuid128) != 16 {
		t.Errorf("UUID128 length = %d, want 16", len(uuid128))
	}

	if !IsUUID128(uuid128) {
		t.Error("IsUUID128 should return true for 16-byte UUID")
	}

	// First two bytes should match short UUID
	if uuid128[0] != 0x00 || uuid128[1] != 0x18 {
		t.Errorf("UUID128 short part = [0x%02X, 0x%02X], want [0x00, 0x18]", uuid128[0], uuid128[1])
	}
}

func TestConcurrency(t *testing.T) {
	util.SetRandom()

	db := NewAttributeDatabase()

	// Add initial attributes
	for i := 0; i < 10; i++ {
		db.AddAttribute(UUID16(uint16(i)), []byte{byte(i)}, PermReadable)
	}

	// Concurrent reads
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(handle uint16) {
			for j := 0; j < 100; j++ {
				_, _ = db.GetAttribute(handle)
			}
			done <- true
		}(uint16(i + 1))
	}

	// Concurrent writes
	for i := 0; i < 10; i++ {
		go func(handle uint16) {
			for j := 0; j < 100; j++ {
				_ = db.SetAttributeValue(handle, []byte{byte(j)})
			}
			done <- true
		}(uint16(i + 1))
	}

	// Wait for all goroutines
	for i := 0; i < 20; i++ {
		<-done
	}

	// Verify database is still consistent
	if db.Count() != 10 {
		t.Errorf("Count after concurrent operations = %d, want 10", db.Count())
	}
}
