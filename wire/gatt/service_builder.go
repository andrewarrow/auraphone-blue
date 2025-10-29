package gatt

import (
	"encoding/binary"
	"fmt"
)

// Service represents a high-level GATT service definition
type Service struct {
	UUID            []byte           // Service UUID (2 or 16 bytes)
	Primary         bool             // true = primary service, false = secondary
	Characteristics []Characteristic // List of characteristics in this service
}

// Characteristic represents a high-level GATT characteristic definition
type Characteristic struct {
	UUID       []byte       // Characteristic UUID (2 or 16 bytes)
	Properties uint8        // Characteristic properties (read, write, notify, etc.)
	Value      []byte       // Initial value
	Descriptors []Descriptor // Optional descriptors (CCCD, etc.)
}

// Descriptor represents a GATT descriptor
type Descriptor struct {
	UUID  []byte // Descriptor UUID (2 or 16 bytes)
	Value []byte // Descriptor value
}

// ServiceHandleInfo stores the handle ranges for a built service
type ServiceHandleInfo struct {
	ServiceHandle    uint16 // Handle of the service declaration
	StartHandle      uint16 // First handle in the service
	EndHandle        uint16 // Last handle in the service
	CharHandles      map[string]uint16 // UUID -> characteristic value handle
}

// BuildAttributeDatabase converts high-level service definitions into an attribute database
func BuildAttributeDatabase(services []Service) (*AttributeDatabase, map[int]*ServiceHandleInfo) {
	db := NewAttributeDatabase()
	serviceInfos := make(map[int]*ServiceHandleInfo)

	for idx, service := range services {
		info := buildService(db, service)
		serviceInfos[idx] = info
	}

	return db, serviceInfos
}

// buildService adds a single service and its characteristics to the database
func buildService(db *AttributeDatabase, service Service) *ServiceHandleInfo {
	info := &ServiceHandleInfo{
		CharHandles: make(map[string]uint16),
	}

	// Add service declaration attribute
	serviceType := UUIDSecondaryService
	if service.Primary {
		serviceType = UUIDPrimaryService
	}

	info.ServiceHandle = db.AddAttribute(serviceType, service.UUID, PermReadable)
	info.StartHandle = info.ServiceHandle

	// Add each characteristic
	for _, char := range service.Characteristics {
		charInfo := buildCharacteristic(db, char)

		// Store the value handle (which is what clients use for read/write)
		uuidStr := uuidToString(char.UUID)
		info.CharHandles[uuidStr] = charInfo.ValueHandle
	}

	// Update end handle
	info.EndHandle = db.nextHandle - 1

	return info
}

// charHandleInfo stores handle information for a characteristic
type charHandleInfo struct {
	DeclarationHandle uint16 // Handle of the characteristic declaration
	ValueHandle       uint16 // Handle of the characteristic value
	DescriptorHandles []uint16 // Handles of descriptors
}

// buildCharacteristic adds a characteristic and its descriptors to the database
func buildCharacteristic(db *AttributeDatabase, char Characteristic) *charHandleInfo {
	info := &charHandleInfo{}

	// Add characteristic declaration attribute
	// Format: [Properties: 1 byte][Value Handle: 2 bytes][UUID: 2 or 16 bytes]
	declValue := make([]byte, 3+len(char.UUID))
	declValue[0] = char.Properties
	binary.LittleEndian.PutUint16(declValue[1:3], db.nextHandle+1) // Next handle will be the value
	copy(declValue[3:], char.UUID)

	info.DeclarationHandle = db.AddAttribute(UUIDCharacteristic, declValue, PermReadable)

	// Add characteristic value attribute
	permissions := determinePermissions(char.Properties)
	info.ValueHandle = db.AddAttribute(char.UUID, char.Value, permissions)

	// Add descriptors
	for _, desc := range char.Descriptors {
		descHandle := db.AddAttribute(desc.UUID, desc.Value, PermReadable|PermWritable)
		info.DescriptorHandles = append(info.DescriptorHandles, descHandle)
	}

	// Add CCCD if characteristic has notify or indicate properties
	if char.Properties&(PropNotify|PropIndicate) != 0 {
		// Add Client Characteristic Configuration Descriptor (CCCD)
		// Initial value: notifications/indications disabled (0x0000)
		cccdValue := []byte{0x00, 0x00}
		cccdHandle := db.AddAttribute(UUIDClientCharacteristicConfig, cccdValue, PermReadable|PermWritable)
		info.DescriptorHandles = append(info.DescriptorHandles, cccdHandle)
	}

	return info
}

// determinePermissions converts characteristic properties to attribute permissions
func determinePermissions(properties uint8) uint8 {
	var perms uint8

	if properties&PropRead != 0 {
		perms |= PermReadable
	}

	if properties&(PropWrite|PropWriteWithoutResponse) != 0 {
		perms |= PermWritable
	}

	return perms
}

// uuidToString converts a UUID byte slice to a string for map keys
func uuidToString(uuid []byte) string {
	return fmt.Sprintf("%x", uuid)
}

// Helper functions to create common services

// NewGenericAccessService creates the mandatory Generic Access service (0x1800)
func NewGenericAccessService(deviceName string, appearance uint16) Service {
	return Service{
		UUID:    UUID16(0x1800),
		Primary: true,
		Characteristics: []Characteristic{
			{
				UUID:       UUID16(0x2A00), // Device Name
				Properties: PropRead,
				Value:      []byte(deviceName),
			},
			{
				UUID:       UUID16(0x2A01), // Appearance
				Properties: PropRead,
				Value:      []byte{byte(appearance), byte(appearance >> 8)},
			},
		},
	}
}

// NewGenericAttributeService creates the mandatory Generic Attribute service (0x1801)
func NewGenericAttributeService() Service {
	return Service{
		UUID:    UUID16(0x1801),
		Primary: true,
		Characteristics: []Characteristic{
			{
				UUID:       UUID16(0x2A05), // Service Changed
				Properties: PropIndicate,
				Value:      []byte{0x00, 0x00, 0x00, 0x00}, // Start handle, end handle
			},
		},
	}
}

// NewCustomService creates a custom service with characteristics
func NewCustomService(serviceUUID []byte, characteristics []Characteristic) Service {
	return Service{
		UUID:            serviceUUID,
		Primary:         true,
		Characteristics: characteristics,
	}
}

// NewReadWriteCharacteristic creates a characteristic with read/write properties
func NewReadWriteCharacteristic(uuid []byte, initialValue []byte) Characteristic {
	return Characteristic{
		UUID:       uuid,
		Properties: PropRead | PropWrite,
		Value:      initialValue,
	}
}

// NewNotifyCharacteristic creates a characteristic with read/notify properties
func NewNotifyCharacteristic(uuid []byte, initialValue []byte) Characteristic {
	return Characteristic{
		UUID:       uuid,
		Properties: PropRead | PropNotify,
		Value:      initialValue,
	}
}

// NewReadOnlyCharacteristic creates a characteristic with only read property
func NewReadOnlyCharacteristic(uuid []byte, value []byte) Characteristic {
	return Characteristic{
		UUID:       uuid,
		Properties: PropRead,
		Value:      value,
	}
}

// FindCharacteristicHandle finds the value handle for a characteristic UUID in a service
func FindCharacteristicHandle(db *AttributeDatabase, serviceInfo *ServiceHandleInfo, charUUID []byte) (uint16, error) {
	uuidStr := uuidToString(charUUID)
	handle, ok := serviceInfo.CharHandles[uuidStr]
	if !ok {
		return 0, fmt.Errorf("gatt: characteristic %s not found in service", uuidStr)
	}
	return handle, nil
}
