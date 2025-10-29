package gatt

import (
	"encoding/binary"
	"fmt"
)

// DiscoveredService represents a discovered GATT service
type DiscoveredService struct {
	UUID        []byte // Service UUID
	StartHandle uint16 // First handle in the service
	EndHandle   uint16 // Last handle in the service
}

// DiscoveredCharacteristic represents a discovered GATT characteristic
type DiscoveredCharacteristic struct {
	UUID            []byte // Characteristic UUID
	Properties      uint8  // Characteristic properties (read, write, notify, etc.)
	ValueHandle     uint16 // Handle for read/write operations
	DeclarationHandle uint16 // Handle of the characteristic declaration
}

// DiscoveredDescriptor represents a discovered descriptor
type DiscoveredDescriptor struct {
	UUID   []byte // Descriptor UUID
	Handle uint16 // Descriptor handle
}

// DiscoveryCache stores the results of GATT discovery for a connection
type DiscoveryCache struct {
	Services        []DiscoveredService
	Characteristics map[uint16][]DiscoveredCharacteristic // Service start handle -> characteristics
	Descriptors     map[uint16][]DiscoveredDescriptor     // Characteristic value handle -> descriptors
	CharHandleMap   map[string]uint16                     // UUID string -> value handle
}

// NewDiscoveryCache creates a new empty discovery cache
func NewDiscoveryCache() *DiscoveryCache {
	return &DiscoveryCache{
		Services:        []DiscoveredService{},
		Characteristics: make(map[uint16][]DiscoveredCharacteristic),
		Descriptors:     make(map[uint16][]DiscoveredDescriptor),
		CharHandleMap:   make(map[string]uint16),
	}
}

// AddService adds a discovered service to the cache
func (dc *DiscoveryCache) AddService(service DiscoveredService) {
	dc.Services = append(dc.Services, service)
}

// AddCharacteristic adds a discovered characteristic to the cache
func (dc *DiscoveryCache) AddCharacteristic(serviceStartHandle uint16, char DiscoveredCharacteristic) {
	dc.Characteristics[serviceStartHandle] = append(dc.Characteristics[serviceStartHandle], char)

	// Add to handle map for quick lookup by UUID
	uuidStr := uuidToString(char.UUID)
	dc.CharHandleMap[uuidStr] = char.ValueHandle
}

// AddDescriptor adds a discovered descriptor to the cache
func (dc *DiscoveryCache) AddDescriptor(charValueHandle uint16, desc DiscoveredDescriptor) {
	dc.Descriptors[charValueHandle] = append(dc.Descriptors[charValueHandle], desc)
}

// FindCharacteristicByUUID finds a characteristic by its UUID
func (dc *DiscoveryCache) FindCharacteristicByUUID(uuid []byte) (*DiscoveredCharacteristic, error) {
	uuidStr := uuidToString(uuid)
	handle, ok := dc.CharHandleMap[uuidStr]
	if !ok {
		return nil, fmt.Errorf("gatt: characteristic %s not found", uuidStr)
	}

	// Find the full characteristic info
	for _, chars := range dc.Characteristics {
		for _, char := range chars {
			if char.ValueHandle == handle {
				return &char, nil
			}
		}
	}

	return nil, fmt.Errorf("gatt: characteristic handle 0x%04X not found in cache", handle)
}

// GetCharacteristicHandle returns the value handle for a characteristic UUID
func (dc *DiscoveryCache) GetCharacteristicHandle(uuid []byte) (uint16, error) {
	uuidStr := uuidToString(uuid)
	handle, ok := dc.CharHandleMap[uuidStr]
	if !ok {
		return 0, fmt.Errorf("gatt: characteristic %s not found", uuidStr)
	}
	return handle, nil
}

// GetDescriptorHandle returns the handle for a descriptor UUID
// Searches all descriptors across all characteristics
func (dc *DiscoveryCache) GetDescriptorHandle(uuid []byte) (uint16, error) {
	uuidStr := uuidToString(uuid)
	// Search through all descriptors
	for _, descriptors := range dc.Descriptors {
		for _, desc := range descriptors {
			if uuidToString(desc.UUID) == uuidStr {
				return desc.Handle, nil
			}
		}
	}
	return 0, fmt.Errorf("gatt: descriptor %s not found", uuidStr)
}

// HasService checks if a service UUID has been discovered
func (dc *DiscoveryCache) HasService(uuid []byte) bool {
	for _, service := range dc.Services {
		if bytesEqual(service.UUID, uuid) {
			return true
		}
	}
	return false
}

// ParseReadByGroupTypeResponse parses a Read By Group Type Response (service discovery)
// Response format: [Length: 1][Data: N * Length]
// Each data entry: [StartHandle: 2][EndHandle: 2][UUID: 2 or 16]
func ParseReadByGroupTypeResponse(data []byte) ([]DiscoveredService, error) {
	if len(data) < 1 {
		return nil, fmt.Errorf("gatt: invalid Read By Group Type Response, too short")
	}

	length := int(data[0])
	if length != 6 && length != 20 { // 6 = 2+2+2 (16-bit UUID), 20 = 2+2+16 (128-bit UUID)
		return nil, fmt.Errorf("gatt: invalid attribute data length %d", length)
	}

	data = data[1:] // Skip length byte
	services := []DiscoveredService{}

	for len(data) >= length {
		entry := data[:length]

		startHandle := binary.LittleEndian.Uint16(entry[0:2])
		endHandle := binary.LittleEndian.Uint16(entry[2:4])
		uuid := make([]byte, length-4)
		copy(uuid, entry[4:])

		services = append(services, DiscoveredService{
			UUID:        uuid,
			StartHandle: startHandle,
			EndHandle:   endHandle,
		})

		data = data[length:]
	}

	if len(data) > 0 {
		return nil, fmt.Errorf("gatt: incomplete service data, %d bytes remaining", len(data))
	}

	return services, nil
}

// ParseReadByTypeResponse parses a Read By Type Response (characteristic discovery)
// Response format: [Length: 1][Data: N * Length]
// Each data entry: [Handle: 2][Properties: 1][ValueHandle: 2][UUID: 2 or 16]
func ParseReadByTypeResponse(data []byte) ([]DiscoveredCharacteristic, error) {
	if len(data) < 1 {
		return nil, fmt.Errorf("gatt: invalid Read By Type Response, too short")
	}

	length := int(data[0])
	if length != 7 && length != 21 { // 7 = 2+1+2+2 (16-bit UUID), 21 = 2+1+2+16 (128-bit UUID)
		return nil, fmt.Errorf("gatt: invalid attribute data length %d", length)
	}

	data = data[1:] // Skip length byte
	characteristics := []DiscoveredCharacteristic{}

	for len(data) >= length {
		entry := data[:length]

		declarationHandle := binary.LittleEndian.Uint16(entry[0:2])
		properties := entry[2]
		valueHandle := binary.LittleEndian.Uint16(entry[3:5])
		uuid := make([]byte, length-5)
		copy(uuid, entry[5:])

		characteristics = append(characteristics, DiscoveredCharacteristic{
			UUID:              uuid,
			Properties:        properties,
			ValueHandle:       valueHandle,
			DeclarationHandle: declarationHandle,
		})

		data = data[length:]
	}

	if len(data) > 0 {
		return nil, fmt.Errorf("gatt: incomplete characteristic data, %d bytes remaining", len(data))
	}

	return characteristics, nil
}

// ParseFindInformationResponse parses a Find Information Response (descriptor discovery)
// Response format: [Format: 1][Data: N * (2 or 18 bytes)]
// Format 0x01: 16-bit UUIDs, each entry is [Handle: 2][UUID: 2]
// Format 0x02: 128-bit UUIDs, each entry is [Handle: 2][UUID: 16]
func ParseFindInformationResponse(data []byte) ([]DiscoveredDescriptor, error) {
	if len(data) < 1 {
		return nil, fmt.Errorf("gatt: invalid Find Information Response, too short")
	}

	format := data[0]
	data = data[1:] // Skip format byte

	var entrySize int
	var uuidSize int

	switch format {
	case 0x01: // 16-bit UUIDs
		entrySize = 4  // 2 (handle) + 2 (UUID)
		uuidSize = 2
	case 0x02: // 128-bit UUIDs
		entrySize = 18 // 2 (handle) + 16 (UUID)
		uuidSize = 16
	default:
		return nil, fmt.Errorf("gatt: invalid Find Information format 0x%02X", format)
	}

	descriptors := []DiscoveredDescriptor{}

	for len(data) >= entrySize {
		entry := data[:entrySize]

		handle := binary.LittleEndian.Uint16(entry[0:2])
		uuid := make([]byte, uuidSize)
		copy(uuid, entry[2:2+uuidSize])

		descriptors = append(descriptors, DiscoveredDescriptor{
			UUID:   uuid,
			Handle: handle,
		})

		data = data[entrySize:]
	}

	if len(data) > 0 {
		return nil, fmt.Errorf("gatt: incomplete descriptor data, %d bytes remaining", len(data))
	}

	return descriptors, nil
}

// BuildReadByGroupTypeResponse builds a Read By Group Type Response for service discovery
// This is used by the peripheral/server side to respond to service discovery requests
func BuildReadByGroupTypeResponse(services []DiscoveredService) ([]byte, error) {
	if len(services) == 0 {
		return nil, fmt.Errorf("gatt: no services to encode")
	}

	// Determine UUID size from first service
	uuidLen := len(services[0].UUID)
	if uuidLen != 2 && uuidLen != 16 {
		return nil, fmt.Errorf("gatt: invalid UUID length %d", uuidLen)
	}

	// All services must have the same UUID length
	for _, service := range services {
		if len(service.UUID) != uuidLen {
			return nil, fmt.Errorf("gatt: inconsistent UUID lengths in service list")
		}
	}

	length := 4 + uuidLen // 2 (start) + 2 (end) + UUID
	buf := make([]byte, 1+len(services)*length)
	buf[0] = byte(length)

	offset := 1
	for _, service := range services {
		binary.LittleEndian.PutUint16(buf[offset:offset+2], service.StartHandle)
		binary.LittleEndian.PutUint16(buf[offset+2:offset+4], service.EndHandle)
		copy(buf[offset+4:offset+4+uuidLen], service.UUID)
		offset += length
	}

	return buf, nil
}

// BuildReadByTypeResponse builds a Read By Type Response for characteristic discovery
// This is used by the peripheral/server side to respond to characteristic discovery requests
func BuildReadByTypeResponse(characteristics []DiscoveredCharacteristic) ([]byte, error) {
	if len(characteristics) == 0 {
		return nil, fmt.Errorf("gatt: no characteristics to encode")
	}

	// Determine UUID size from first characteristic
	uuidLen := len(characteristics[0].UUID)
	if uuidLen != 2 && uuidLen != 16 {
		return nil, fmt.Errorf("gatt: invalid UUID length %d", uuidLen)
	}

	// All characteristics must have the same UUID length
	for _, char := range characteristics {
		if len(char.UUID) != uuidLen {
			return nil, fmt.Errorf("gatt: inconsistent UUID lengths in characteristic list")
		}
	}

	length := 5 + uuidLen // 2 (decl handle) + 1 (props) + 2 (value handle) + UUID
	buf := make([]byte, 1+len(characteristics)*length)
	buf[0] = byte(length)

	offset := 1
	for _, char := range characteristics {
		binary.LittleEndian.PutUint16(buf[offset:offset+2], char.DeclarationHandle)
		buf[offset+2] = char.Properties
		binary.LittleEndian.PutUint16(buf[offset+3:offset+5], char.ValueHandle)
		copy(buf[offset+5:offset+5+uuidLen], char.UUID)
		offset += length
	}

	return buf, nil
}

// BuildFindInformationResponse builds a Find Information Response for descriptor discovery
// This is used by the peripheral/server side to respond to descriptor discovery requests
func BuildFindInformationResponse(descriptors []DiscoveredDescriptor) ([]byte, error) {
	if len(descriptors) == 0 {
		return nil, fmt.Errorf("gatt: no descriptors to encode")
	}

	// Determine UUID size from first descriptor
	uuidLen := len(descriptors[0].UUID)
	if uuidLen != 2 && uuidLen != 16 {
		return nil, fmt.Errorf("gatt: invalid UUID length %d", uuidLen)
	}

	// All descriptors must have the same UUID length
	for _, desc := range descriptors {
		if len(desc.UUID) != uuidLen {
			return nil, fmt.Errorf("gatt: inconsistent UUID lengths in descriptor list")
		}
	}

	var format uint8
	if uuidLen == 2 {
		format = 0x01
	} else {
		format = 0x02
	}

	entrySize := 2 + uuidLen // 2 (handle) + UUID
	buf := make([]byte, 1+len(descriptors)*entrySize)
	buf[0] = format

	offset := 1
	for _, desc := range descriptors {
		binary.LittleEndian.PutUint16(buf[offset:offset+2], desc.Handle)
		copy(buf[offset+2:offset+2+uuidLen], desc.UUID)
		offset += entrySize
	}

	return buf, nil
}

// DiscoverServicesFromDatabase discovers all services in an attribute database
// This is a helper for testing and for implementing the server-side discovery response
func DiscoverServicesFromDatabase(db *AttributeDatabase, startHandle, endHandle uint16) []DiscoveredService {
	services := []DiscoveredService{}

	// Find all primary service declarations
	serviceHandles := db.FindAttributesByType(startHandle, endHandle, UUIDPrimaryService)

	for _, handle := range serviceHandles {
		attr, err := db.GetAttribute(handle)
		if err != nil {
			continue
		}

		// Find the end handle for this service (next service handle - 1, or endHandle)
		serviceEndHandle := endHandle
		for _, nextHandle := range serviceHandles {
			if nextHandle > handle && nextHandle-1 < serviceEndHandle {
				serviceEndHandle = nextHandle - 1
				break
			}
		}

		services = append(services, DiscoveredService{
			UUID:        attr.Value, // Service UUID is stored in the value
			StartHandle: handle,
			EndHandle:   serviceEndHandle,
		})
	}

	return services
}

// DiscoverCharacteristicsFromDatabase discovers characteristics within a service
func DiscoverCharacteristicsFromDatabase(db *AttributeDatabase, startHandle, endHandle uint16) []DiscoveredCharacteristic {
	characteristics := []DiscoveredCharacteristic{}

	// Find all characteristic declarations
	charHandles := db.FindAttributesByType(startHandle, endHandle, UUIDCharacteristic)

	for _, handle := range charHandles {
		attr, err := db.GetAttribute(handle)
		if err != nil {
			continue
		}

		// Parse characteristic declaration
		// Format: [Properties: 1][ValueHandle: 2][UUID: 2 or 16]
		if len(attr.Value) < 5 {
			continue
		}

		properties := attr.Value[0]
		valueHandle := binary.LittleEndian.Uint16(attr.Value[1:3])
		uuid := attr.Value[3:]

		characteristics = append(characteristics, DiscoveredCharacteristic{
			UUID:              uuid,
			Properties:        properties,
			ValueHandle:       valueHandle,
			DeclarationHandle: handle,
		})
	}

	return characteristics
}

// DiscoverDescriptorsFromDatabase discovers descriptors for a characteristic
func DiscoverDescriptorsFromDatabase(db *AttributeDatabase, startHandle, endHandle uint16) []DiscoveredDescriptor {
	descriptors := []DiscoveredDescriptor{}

	// Get the actual database handle range to avoid iterating through empty space
	_, lastHandle := db.GetHandleRange()
	if endHandle > lastHandle {
		endHandle = lastHandle
	}

	// Track characteristic value handles to exclude them from descriptor list
	charValueHandles := make(map[uint16]bool)

	// First pass: find all characteristic declarations and their value handles
	for h := uint16(0x0001); h <= endHandle; h++ {
		attr, err := db.GetAttribute(h)
		if err != nil {
			continue
		}

		// If this is a characteristic declaration, parse it to get the value handle
		if bytesEqual(attr.Type, UUIDCharacteristic) {
			// Characteristic declaration format: [Properties: 1][ValueHandle: 2][UUID: N]
			if len(attr.Value) >= 3 {
				valueHandle := uint16(attr.Value[1]) | (uint16(attr.Value[2]) << 8)
				charValueHandles[valueHandle] = true
			}
		}
	}

	// Second pass: collect descriptors in the requested range
	// Descriptors are attributes that are NOT:
	// - Service declarations (0x2800, 0x2801)
	// - Characteristic declarations (0x2803)
	// - Characteristic values (tracked above)
	for handle := startHandle; handle <= endHandle; handle++ {
		attr, err := db.GetAttribute(handle)
		if err != nil {
			continue
		}

		// Skip service and characteristic declarations
		if bytesEqual(attr.Type, UUIDPrimaryService) ||
			bytesEqual(attr.Type, UUIDSecondaryService) ||
			bytesEqual(attr.Type, UUIDCharacteristic) {
			continue
		}

		// Skip characteristic values (they're not descriptors)
		if charValueHandles[handle] {
			continue
		}

		// This is a descriptor - include it
		descriptors = append(descriptors, DiscoveredDescriptor{
			UUID:   attr.Type,
			Handle: handle,
		})
	}

	return descriptors
}
