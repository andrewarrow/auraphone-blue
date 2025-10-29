package gatt

import (
	"fmt"
	"sync"
)

// Well-known GATT UUIDs (16-bit, little-endian)
var (
	// Service UUIDs
	UUIDPrimaryService   = []byte{0x00, 0x28} // 0x2800
	UUIDSecondaryService = []byte{0x01, 0x28} // 0x2801
	UUIDInclude          = []byte{0x02, 0x28} // 0x2802
	UUIDCharacteristic   = []byte{0x03, 0x28} // 0x2803

	// Descriptor UUIDs
	UUIDCharExtProps              = []byte{0x00, 0x29} // 0x2900
	UUIDCharUserDescription       = []byte{0x01, 0x29} // 0x2901
	UUIDClientCharacteristicConfig = []byte{0x02, 0x29} // 0x2902 (CCCD)
	UUIDServerCharacteristicConfig = []byte{0x03, 0x29} // 0x2903
	UUIDCharPresentationFormat    = []byte{0x04, 0x29} // 0x2904
	UUIDCharAggregateFormat       = []byte{0x05, 0x29} // 0x2905
)

// Characteristic Properties (bitmask)
const (
	PropBroadcast              = 0x01
	PropRead                   = 0x02
	PropWriteWithoutResponse   = 0x04
	PropWrite                  = 0x08
	PropNotify                 = 0x10
	PropIndicate               = 0x20
	PropAuthenticatedSignedWrites = 0x40
	PropExtendedProperties     = 0x80
)

// Attribute permissions (not transmitted over the air, server-side only)
const (
	PermReadable  = 0x01
	PermWritable  = 0x02
	PermReadEncrypt  = 0x04
	PermWriteEncrypt = 0x08
)

// Attribute represents a single GATT attribute with a handle
type Attribute struct {
	Handle      uint16 // ATT handle (1-based, 0x0000 is reserved)
	Type        []byte // UUID (2 or 16 bytes)
	Value       []byte // Current value
	Permissions uint8  // Read/Write permissions
}

// AttributeDatabase manages the GATT attribute table with handle-based access
type AttributeDatabase struct {
	mu         sync.RWMutex
	attributes map[uint16]*Attribute // Handle -> Attribute
	nextHandle uint16                // Next available handle
}

// NewAttributeDatabase creates an empty attribute database
func NewAttributeDatabase() *AttributeDatabase {
	return &AttributeDatabase{
		attributes: make(map[uint16]*Attribute),
		nextHandle: 0x0001, // Handles start at 1
	}
}

// AddAttribute adds an attribute and assigns it a handle
func (db *AttributeDatabase) AddAttribute(attrType []byte, value []byte, permissions uint8) uint16 {
	db.mu.Lock()
	defer db.mu.Unlock()

	handle := db.nextHandle
	db.nextHandle++

	db.attributes[handle] = &Attribute{
		Handle:      handle,
		Type:        append([]byte{}, attrType...), // Copy to avoid aliasing
		Value:       append([]byte{}, value...),    // Copy to avoid aliasing
		Permissions: permissions,
	}

	return handle
}

// GetAttribute retrieves an attribute by handle
func (db *AttributeDatabase) GetAttribute(handle uint16) (*Attribute, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	attr, ok := db.attributes[handle]
	if !ok {
		return nil, fmt.Errorf("gatt: invalid handle 0x%04X", handle)
	}

	// Return a copy to prevent external modification
	return &Attribute{
		Handle:      attr.Handle,
		Type:        append([]byte{}, attr.Type...),
		Value:       append([]byte{}, attr.Value...),
		Permissions: attr.Permissions,
	}, nil
}

// SetAttributeValue updates an attribute's value
func (db *AttributeDatabase) SetAttributeValue(handle uint16, value []byte) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	attr, ok := db.attributes[handle]
	if !ok {
		return fmt.Errorf("gatt: invalid handle 0x%04X", handle)
	}

	attr.Value = append([]byte{}, value...) // Copy to avoid aliasing
	return nil
}

// FindAttributesByType returns all handles with matching type UUID in a range
func (db *AttributeDatabase) FindAttributesByType(startHandle, endHandle uint16, attrType []byte) []uint16 {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var handles []uint16

	for h := startHandle; h <= endHandle && h < db.nextHandle; h++ {
		if attr, ok := db.attributes[h]; ok {
			if bytesEqual(attr.Type, attrType) {
				handles = append(handles, h)
			}
		}
	}

	return handles
}

// GetAllHandles returns all handles in the database (sorted)
func (db *AttributeDatabase) GetAllHandles() []uint16 {
	db.mu.RLock()
	defer db.mu.RUnlock()

	handles := make([]uint16, 0, len(db.attributes))
	for h := uint16(0x0001); h < db.nextHandle; h++ {
		if _, ok := db.attributes[h]; ok {
			handles = append(handles, h)
		}
	}

	return handles
}

// GetHandleRange returns the first and last handle in the database
func (db *AttributeDatabase) GetHandleRange() (uint16, uint16) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if len(db.attributes) == 0 {
		return 0, 0
	}

	var first uint16 = 0xFFFF
	var last uint16 = 0x0000

	for h := range db.attributes {
		if h < first {
			first = h
		}
		if h > last {
			last = h
		}
	}

	return first, last
}

// Count returns the number of attributes in the database
func (db *AttributeDatabase) Count() int {
	db.mu.RLock()
	defer db.mu.RUnlock()

	return len(db.attributes)
}

// Clear removes all attributes from the database
func (db *AttributeDatabase) Clear() {
	db.mu.Lock()
	defer db.mu.Unlock()

	db.attributes = make(map[uint16]*Attribute)
	db.nextHandle = 0x0001
}

// bytesEqual compares two byte slices for equality
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// UUID16 creates a 16-bit UUID in little-endian format
func UUID16(val uint16) []byte {
	return []byte{byte(val), byte(val >> 8)}
}

// UUID128 creates a 128-bit UUID from a base UUID and a 16-bit short UUID
// Base UUID: 00000000-0000-1000-8000-00805F9B34FB
func UUID128(shortUUID uint16) []byte {
	uuid := make([]byte, 16)
	uuid[0] = byte(shortUUID)        // Low byte of 16-bit UUID
	uuid[1] = byte(shortUUID >> 8)   // High byte of 16-bit UUID
	uuid[2] = 0x00
	uuid[3] = 0x00
	uuid[4] = 0x10
	uuid[5] = 0x00
	uuid[6] = 0x80
	uuid[7] = 0x00
	uuid[8] = 0x00
	uuid[9] = 0x80
	uuid[10] = 0x5F
	uuid[11] = 0x9B
	uuid[12] = 0x34
	uuid[13] = 0xFB
	uuid[14] = 0x00
	uuid[15] = 0x00
	return uuid
}

// IsUUID16 checks if a UUID is 16-bit (2 bytes)
func IsUUID16(uuid []byte) bool {
	return len(uuid) == 2
}

// IsUUID128 checks if a UUID is 128-bit (16 bytes)
func IsUUID128(uuid []byte) bool {
	return len(uuid) == 16
}
