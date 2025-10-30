package wire

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/wire/gatt"
)

// localUUIDToHandle converts a service+characteristic UUID pair to a handle using LOCAL attribute database
// This is used when acting as a PERIPHERAL (server) sending notifications/indications
// The peripheral uses its own GATT database, not the client's discovery cache
func (w *Wire) localUUIDToHandle(serviceUUID, charUUID string) (uint16, error) {
	w.dbMu.RLock()
	db := w.attributeDB
	w.dbMu.RUnlock()

	if db == nil {
		return 0, fmt.Errorf("no local attribute database, call SetAttributeDatabase first")
	}

	// Convert UUIDs to bytes for lookup
	charUUIDBytes := stringToUUIDBytes(charUUID)

	// Search attribute database for characteristic with matching UUID
	for handle := uint16(1); handle <= 0xFFFF; handle++ {
		attr, err := db.GetAttribute(handle)
		if err != nil {
			continue // Handle not found, keep searching
		}

		// Check if this is a characteristic value attribute (type 0x2803 is char declaration, next handle is value)
		// We want the VALUE handle, not the declaration handle
		// The Type field contains the characteristic UUID for value attributes
		if len(attr.Type) == len(charUUIDBytes) {
			match := true
			for i := range charUUIDBytes {
				if attr.Type[i] != charUUIDBytes[i] {
					match = false
					break
				}
			}
			if match {
				logger.Debug(shortHash(w.hardwareUUID)+" Wire",
					"   UUID->Handle (from local database): char=%s -> 0x%04X",
					shortHash(charUUID), handle)
				return handle, nil
			}
		}
	}

	return 0, fmt.Errorf("characteristic %s not found in local attribute database", charUUID)
}

// uuidToHandle converts a service+characteristic UUID pair to a handle
// This ONLY uses the discovery cache - discovery must be performed first
// Returns an error if the characteristic or descriptor is not found in the discovery cache
func (w *Wire) uuidToHandle(peerUUID, serviceUUID, charUUID string) (uint16, error) {
	// Get handle from discovery cache
	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if !exists {
		return 0, fmt.Errorf("no connection to peer %s", peerUUID)
	}

	if connection.discoveryCache == nil {
		return 0, fmt.Errorf("no discovery cache for peer %s, run DiscoverServices first", peerUUID)
	}

	cache := connection.discoveryCache.(*gatt.DiscoveryCache)

	// Convert char UUID string to bytes for lookup
	charUUIDBytes := stringToUUIDBytes(charUUID)

	// First try characteristic lookup
	handle, err := cache.GetCharacteristicHandle(charUUIDBytes)
	if err == nil {
		logger.Debug(shortHash(w.hardwareUUID)+" Wire",
			"   UUID->Handle (from discovery cache): char=%s -> 0x%04X",
			shortHash(charUUID), handle)
		return handle, nil
	}

	// If not found as characteristic, try descriptor lookup
	// This handles cases like CCCD (0x2902) which is a descriptor
	handle, descErr := cache.GetDescriptorHandle(charUUIDBytes)
	if descErr == nil {
		logger.Debug(shortHash(w.hardwareUUID)+" Wire",
			"   UUID->Handle (from discovery cache): descriptor=%s -> 0x%04X",
			shortHash(charUUID), handle)
		return handle, nil
	}

	// Not found as either characteristic or descriptor
	return 0, fmt.Errorf("characteristic %s not found in discovery cache, run DiscoverServices/DiscoverCharacteristics first: %w", charUUID, err)
}

// stringToUUIDBytes converts a UUID string to bytes
// Handles both short (16-bit) and long (128-bit) UUIDs
// This is the canonical UUID parsing logic used across all platform wrappers
func stringToUUIDBytes(uuid string) []byte {
	// Try to parse as 16-bit UUID first (4 hex chars)
	if len(uuid) == 4 {
		// Check if all characters are hex
		allHex := true
		for _, c := range uuid {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				allHex = false
				break
			}
		}
		if allHex {
			// Parse as 16-bit hex UUID
			var uuid16 uint16
			fmt.Sscanf(uuid, "%04x", &uuid16)
			// Return in little-endian format as per BLE spec
			return []byte{byte(uuid16), byte(uuid16 >> 8)}
		}
	}

	// For standard 128-bit UUIDs (36 chars with dashes: XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX)
	if len(uuid) == 36 && (uuid[8] == '-' && uuid[13] == '-' && uuid[18] == '-' && uuid[23] == '-') {
		// Check if it's a Bluetooth Base UUID (0000XXXX-0000-1000-8000-00805F9B34FB)
		// In this case, extract the 16-bit UUID (XXXX) and return as 2 bytes
		baseUUIDSuffix := "-0000-1000-8000-00805f9b34fb"
		baseUUIDSuffixUpper := "-0000-1000-8000-00805F9B34FB"
		if (len(uuid) >= 13 && uuid[13:] == baseUUIDSuffix) || (len(uuid) >= 13 && uuid[13:] == baseUUIDSuffixUpper) {
			// Extract the 16-bit UUID from positions 4-8 (e.g., "0000XXXX")
			if uuid[0:4] == "0000" {
				// This is a 16-bit UUID in standard format
				var uuid16 uint16
				fmt.Sscanf(uuid[4:8], "%04x", &uuid16)
				// Return in little-endian format as per BLE spec
				return []byte{byte(uuid16), byte(uuid16 >> 8)}
			}
		}
		// For other 128-bit UUIDs, remove dashes and fall through to hex parsing below
		// Don't just copy characters - we need to parse hex!
		// Fall through to the hex parsing logic below by not returning here
	}

	// For test/custom UUIDs or pure hex strings, try to determine the format
	// Check if it looks like a hex string (no dashes, all hex chars)
	cleaned := ""
	for _, c := range uuid {
		if c != '-' {
			cleaned += string(c)
		}
	}

	// If it's all hex characters and reasonable length, parse as hex
	allHex := true
	for _, c := range cleaned {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			allHex = false
			break
		}
	}

	if allHex && len(cleaned) >= 4 && len(cleaned)%2 == 0 {
		// Parse as hex string
		bytes := make([]byte, len(cleaned)/2)
		for i := 0; i < len(bytes); i++ {
			var b byte
			if cleaned[i*2] >= '0' && cleaned[i*2] <= '9' {
				b = (cleaned[i*2] - '0') << 4
			} else if cleaned[i*2] >= 'a' && cleaned[i*2] <= 'f' {
				b = (cleaned[i*2] - 'a' + 10) << 4
			} else if cleaned[i*2] >= 'A' && cleaned[i*2] <= 'F' {
				b = (cleaned[i*2] - 'A' + 10) << 4
			}

			if cleaned[i*2+1] >= '0' && cleaned[i*2+1] <= '9' {
				b |= cleaned[i*2+1] - '0'
			} else if cleaned[i*2+1] >= 'a' && cleaned[i*2+1] <= 'f' {
				b |= cleaned[i*2+1] - 'a' + 10
			} else if cleaned[i*2+1] >= 'A' && cleaned[i*2+1] <= 'F' {
				b |= cleaned[i*2+1] - 'A' + 10
			}

			bytes[i] = b
		}
		return bytes
	}

	// For test/custom UUIDs (like "test-char-uuid"), create a deterministic 16-byte UUID
	// by converting the string directly to bytes (ASCII values)
	// This is the canonical UUID conversion for non-standard UUIDs
	bytes := make([]byte, 16)
	for i := 0; i < len(uuid) && i < 16; i++ {
		bytes[i] = uuid[i]
	}
	// Fill remaining bytes with zero
	for i := len(uuid); i < 16; i++ {
		bytes[i] = 0
	}
	return bytes
}

// handleToUUIDs converts a characteristic value handle to service + characteristic UUIDs
// by looking up in the local attribute database. Returns empty strings if not found.
// This is used when receiving ATT packets to convert handles back to UUIDs for higher layers.
func (w *Wire) handleToUUIDs(handle uint16) (serviceUUID, charUUID string) {
	w.dbMu.RLock()
	db := w.attributeDB
	w.dbMu.RUnlock()

	if db == nil {
		return "", ""
	}

	// Get the attribute at this handle
	attr, err := db.GetAttribute(handle)
	if err != nil {
		return "", ""
	}

	// The Type field contains the characteristic UUID for value attributes
	charUUID = bytesToUUIDString(attr.Type)

	// Find the service by searching backwards for service declaration
	// Service declarations have type 0x2800 (primary) or 0x2801 (secondary)
	primaryServiceType := []byte{0x00, 0x28} // 0x2800 in little-endian
	secondaryServiceType := []byte{0x01, 0x28} // 0x2801 in little-endian

	// Search backwards from current handle to find the service declaration
	for h := handle; h >= 1; h-- {
		attr, err := db.GetAttribute(h)
		if err != nil {
			continue
		}

		// Check if this is a service declaration
		if len(attr.Type) == 2 &&
			((attr.Type[0] == primaryServiceType[0] && attr.Type[1] == primaryServiceType[1]) ||
				(attr.Type[0] == secondaryServiceType[0] && attr.Type[1] == secondaryServiceType[1])) {
			// Found service declaration - the Value field contains the service UUID
			serviceUUID = bytesToUUIDString(attr.Value)
			return serviceUUID, charUUID
		}
	}

	// If no service found, return just the characteristic UUID
	return "", charUUID
}

// bytesToUUIDString converts UUID bytes to a string representation
// This matches the string format used in test UUIDs
func bytesToUUIDString(uuid []byte) string {
	// For standard BLE UUIDs (2 bytes = 16-bit)
	if len(uuid) == 2 {
		// 16-bit UUID - return as hex string
		return fmt.Sprintf("%04x", uint16(uuid[0])|uint16(uuid[1])<<8)
	}

	// For 16-byte UUIDs, check if it's an ASCII string (common in tests)
	if len(uuid) == 16 {
		// Check if this looks like an ASCII string UUID
		allASCII := true
		endIndex := len(uuid)

		// Find the end of the string (first null byte or end of buffer)
		for i := 0; i < len(uuid); i++ {
			if uuid[i] == 0 {
				endIndex = i
				break
			}
			if uuid[i] < 32 || uuid[i] > 126 {
				// Not printable ASCII
				allASCII = false
				break
			}
		}

		// If it's all ASCII, return the string (without null bytes)
		if allASCII && endIndex > 0 {
			return string(uuid[:endIndex])
		}
	}

	// For 128-bit UUIDs or other formats, return as hex
	return fmt.Sprintf("%x", uuid)
}

// GetHardwareUUID returns this device's hardware UUID
func (w *Wire) GetHardwareUUID() string {
	return w.hardwareUUID
}

// SetConnectCallback sets the callback for when a connection is established
func (w *Wire) SetConnectCallback(callback func(peerUUID string, role ConnectionRole)) {
	w.callbackMu.Lock()
	w.connectCallback = callback
	w.callbackMu.Unlock()
}

// SetDisconnectCallback sets the callback for when a connection is lost
func (w *Wire) SetDisconnectCallback(callback func(peerUUID string)) {
	w.callbackMu.Lock()
	w.disconnectCallback = callback
	w.callbackMu.Unlock()
}
