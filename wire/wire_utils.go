package wire

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/wire/gatt"
)

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
// Must match the parseUUID logic in swift/cb_peripheral_manager.go
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
		// For other 128-bit UUIDs, parse the full UUID
		// For now, simplified: just use first 16 bytes of string
		bytes := make([]byte, 16)
		for i := 0; i < 16 && i < len(uuid); i++ {
			bytes[i] = uuid[i]
		}
		return bytes
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
	// This matches the parseUUID logic in swift/cb_peripheral_manager.go
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
