package gatt

import (
	"testing"

	"github.com/user/auraphone-blue/util"
)

func TestBuildSimpleService(t *testing.T) {
	util.SetRandom()

	// Create a simple service with one characteristic
	services := []Service{
		{
			UUID:    UUID16(0x1800), // Generic Access Service
			Primary: true,
			Characteristics: []Characteristic{
				{
					UUID:       UUID16(0x2A00), // Device Name
					Properties: PropRead,
					Value:      []byte("Test Device"),
				},
			},
		},
	}

	db, infos := BuildAttributeDatabase(services)

	// Verify database was populated
	if db.Count() == 0 {
		t.Fatal("Database is empty after building service")
	}

	// Verify service info
	info := infos[0]
	if info.ServiceHandle == 0 {
		t.Error("Service handle is 0")
	}

	if info.StartHandle == 0 {
		t.Error("Start handle is 0")
	}

	if info.EndHandle == 0 {
		t.Error("End handle is 0")
	}

	if info.StartHandle > info.EndHandle {
		t.Errorf("Start handle (0x%04X) > end handle (0x%04X)", info.StartHandle, info.EndHandle)
	}

	// Verify characteristic handle was stored
	charUUID := UUID16(0x2A00)
	handle, err := FindCharacteristicHandle(db, info, charUUID)
	if err != nil {
		t.Fatalf("Failed to find characteristic handle: %v", err)
	}

	// Verify we can read the characteristic value
	attr, err := db.GetAttribute(handle)
	if err != nil {
		t.Fatalf("Failed to get attribute: %v", err)
	}

	if string(attr.Value) != "Test Device" {
		t.Errorf("Characteristic value = %s, want 'Test Device'", string(attr.Value))
	}
}

func TestBuildServiceWithMultipleCharacteristics(t *testing.T) {
	util.SetRandom()

	services := []Service{
		{
			UUID:    UUID16(0x180F), // Battery Service
			Primary: true,
			Characteristics: []Characteristic{
				{
					UUID:       UUID16(0x2A19), // Battery Level
					Properties: PropRead | PropNotify,
					Value:      []byte{100}, // 100%
				},
				{
					UUID:       UUID16(0x2A1B), // Battery Power State
					Properties: PropRead,
					Value:      []byte{0x01, 0x00, 0x00, 0x00},
				},
			},
		},
	}

	db, infos := BuildAttributeDatabase(services)

	info := infos[0]

	// Verify both characteristics are present
	batteryLevel, err := FindCharacteristicHandle(db, info, UUID16(0x2A19))
	if err != nil {
		t.Fatalf("Failed to find Battery Level characteristic: %v", err)
	}

	batteryPowerState, err := FindCharacteristicHandle(db, info, UUID16(0x2A1B))
	if err != nil {
		t.Fatalf("Failed to find Battery Power State characteristic: %v", err)
	}

	// Verify values
	attr1, _ := db.GetAttribute(batteryLevel)
	if len(attr1.Value) != 1 || attr1.Value[0] != 100 {
		t.Errorf("Battery Level = %v, want [100]", attr1.Value)
	}

	attr2, _ := db.GetAttribute(batteryPowerState)
	if len(attr2.Value) != 4 {
		t.Errorf("Battery Power State length = %d, want 4", len(attr2.Value))
	}
}

func TestNotifyCharacteristicAddsCCCD(t *testing.T) {
	util.SetRandom()

	services := []Service{
		{
			UUID:    UUID16(0x1234),
			Primary: true,
			Characteristics: []Characteristic{
				{
					UUID:       UUID16(0x5678),
					Properties: PropRead | PropNotify,
					Value:      []byte{0x00},
				},
			},
		},
	}

	db, infos := BuildAttributeDatabase(services)

	info := infos[0]

	// A characteristic with notify should have:
	// 1. Service declaration (1 handle)
	// 2. Characteristic declaration (1 handle)
	// 3. Characteristic value (1 handle)
	// 4. CCCD descriptor (1 handle)
	// Total: 4 handles

	expectedHandles := 4
	actualHandles := db.Count()

	if actualHandles != expectedHandles {
		t.Errorf("Database has %d handles, want %d", actualHandles, expectedHandles)
	}

	// Find the CCCD (should be after the characteristic value)
	charHandle, _ := FindCharacteristicHandle(db, info, UUID16(0x5678))
	cccdHandle := charHandle + 1

	cccd, err := db.GetAttribute(cccdHandle)
	if err != nil {
		t.Fatalf("Failed to get CCCD: %v", err)
	}

	// Verify it's a CCCD
	if len(cccd.Type) != 2 || cccd.Type[0] != 0x02 || cccd.Type[1] != 0x29 {
		t.Errorf("Expected CCCD UUID (0x2902), got %v", cccd.Type)
	}

	// Verify initial value is 0x0000 (notifications disabled)
	if len(cccd.Value) != 2 || cccd.Value[0] != 0x00 || cccd.Value[1] != 0x00 {
		t.Errorf("CCCD initial value = %v, want [0x00, 0x00]", cccd.Value)
	}
}

func TestMultipleServices(t *testing.T) {
	util.SetRandom()

	services := []Service{
		{
			UUID:    UUID16(0x1800), // Generic Access
			Primary: true,
			Characteristics: []Characteristic{
				{
					UUID:       UUID16(0x2A00),
					Properties: PropRead,
					Value:      []byte("Device A"),
				},
			},
		},
		{
			UUID:    UUID16(0x180F), // Battery Service
			Primary: true,
			Characteristics: []Characteristic{
				{
					UUID:       UUID16(0x2A19),
					Properties: PropRead,
					Value:      []byte{75},
				},
			},
		},
	}

	db, infos := BuildAttributeDatabase(services)

	// Verify we have two services
	if len(infos) != 2 {
		t.Fatalf("Service infos count = %d, want 2", len(infos))
	}

	// Verify handles are sequential and don't overlap
	info1 := infos[0]
	info2 := infos[1]

	if info1.EndHandle >= info2.StartHandle {
		t.Errorf("Services overlap: service 1 ends at 0x%04X, service 2 starts at 0x%04X",
			info1.EndHandle, info2.StartHandle)
	}

	// Verify both services are accessible
	char1, err := FindCharacteristicHandle(db, info1, UUID16(0x2A00))
	if err != nil {
		t.Fatalf("Failed to find characteristic in service 1: %v", err)
	}

	char2, err := FindCharacteristicHandle(db, info2, UUID16(0x2A19))
	if err != nil {
		t.Fatalf("Failed to find characteristic in service 2: %v", err)
	}

	attr1, _ := db.GetAttribute(char1)
	attr2, _ := db.GetAttribute(char2)

	if string(attr1.Value) != "Device A" {
		t.Errorf("Service 1 char value = %s, want 'Device A'", string(attr1.Value))
	}

	if len(attr2.Value) != 1 || attr2.Value[0] != 75 {
		t.Errorf("Service 2 char value = %v, want [75]", attr2.Value)
	}
}

func TestHelperFunctions(t *testing.T) {
	util.SetRandom()

	// Test NewGenericAccessService
	gas := NewGenericAccessService("MyDevice", 0x0000)
	if len(gas.UUID) != 2 || gas.UUID[0] != 0x00 || gas.UUID[1] != 0x18 {
		t.Errorf("Generic Access Service UUID = %v, want [0x00, 0x18]", gas.UUID)
	}

	if !gas.Primary {
		t.Error("Generic Access Service should be primary")
	}

	if len(gas.Characteristics) != 2 {
		t.Errorf("Generic Access Service has %d characteristics, want 2", len(gas.Characteristics))
	}

	// Test NewGenericAttributeService
	gatts := NewGenericAttributeService()
	if len(gatts.UUID) != 2 || gatts.UUID[0] != 0x01 || gatts.UUID[1] != 0x18 {
		t.Errorf("Generic Attribute Service UUID = %v, want [0x01, 0x18]", gatts.UUID)
	}

	// Test NewReadWriteCharacteristic
	char := NewReadWriteCharacteristic(UUID16(0x1234), []byte{0xAA})
	if char.Properties != (PropRead | PropWrite) {
		t.Errorf("Read/Write characteristic properties = 0x%02X, want 0x%02X", char.Properties, PropRead|PropWrite)
	}

	// Test NewNotifyCharacteristic
	notifyChar := NewNotifyCharacteristic(UUID16(0x5678), []byte{0xBB})
	if notifyChar.Properties != (PropRead | PropNotify) {
		t.Errorf("Notify characteristic properties = 0x%02X, want 0x%02X", notifyChar.Properties, PropRead|PropNotify)
	}

	// Test NewReadOnlyCharacteristic
	readOnlyChar := NewReadOnlyCharacteristic(UUID16(0x9ABC), []byte{0xCC})
	if readOnlyChar.Properties != PropRead {
		t.Errorf("Read-only characteristic properties = 0x%02X, want 0x%02X", readOnlyChar.Properties, PropRead)
	}
}

func TestCharacteristicWithDescriptors(t *testing.T) {
	util.SetRandom()

	services := []Service{
		{
			UUID:    UUID16(0x1234),
			Primary: true,
			Characteristics: []Characteristic{
				{
					UUID:       UUID16(0x5678),
					Properties: PropRead,
					Value:      []byte("Test"),
					Descriptors: []Descriptor{
						{
							UUID:  UUIDCharUserDescription,
							Value: []byte("User Description"),
						},
					},
				},
			},
		},
	}

	db, _ := BuildAttributeDatabase(services)

	// Should have:
	// 1. Service declaration
	// 2. Characteristic declaration
	// 3. Characteristic value
	// 4. User Description descriptor
	// Total: 4 handles

	if db.Count() != 4 {
		t.Errorf("Database has %d handles, want 4", db.Count())
	}
}

func TestWriteCharacteristicValue(t *testing.T) {
	util.SetRandom()

	services := []Service{
		{
			UUID:    UUID16(0x1800),
			Primary: true,
			Characteristics: []Characteristic{
				{
					UUID:       UUID16(0x2A00),
					Properties: PropRead | PropWrite,
					Value:      []byte("Initial"),
				},
			},
		},
	}

	db, infos := BuildAttributeDatabase(services)

	// Find the characteristic
	handle, err := FindCharacteristicHandle(db, infos[0], UUID16(0x2A00))
	if err != nil {
		t.Fatalf("Failed to find characteristic: %v", err)
	}

	// Write a new value
	newValue := []byte("Updated")
	err = db.SetAttributeValue(handle, newValue)
	if err != nil {
		t.Fatalf("Failed to set attribute value: %v", err)
	}

	// Read it back
	attr, err := db.GetAttribute(handle)
	if err != nil {
		t.Fatalf("Failed to get attribute: %v", err)
	}

	if string(attr.Value) != "Updated" {
		t.Errorf("Value after update = %s, want 'Updated'", string(attr.Value))
	}
}

func TestSecondaryService(t *testing.T) {
	util.SetRandom()

	services := []Service{
		{
			UUID:    UUID16(0x1234),
			Primary: false, // Secondary service
			Characteristics: []Characteristic{
				{
					UUID:       UUID16(0x5678),
					Properties: PropRead,
					Value:      []byte{0x00},
				},
			},
		},
	}

	db, infos := BuildAttributeDatabase(services)

	// Get the service declaration
	serviceAttr, err := db.GetAttribute(infos[0].ServiceHandle)
	if err != nil {
		t.Fatalf("Failed to get service declaration: %v", err)
	}

	// Verify it's a secondary service (UUID 0x2801)
	if len(serviceAttr.Type) != 2 || serviceAttr.Type[0] != 0x01 || serviceAttr.Type[1] != 0x28 {
		t.Errorf("Expected Secondary Service UUID (0x2801), got %v", serviceAttr.Type)
	}
}
