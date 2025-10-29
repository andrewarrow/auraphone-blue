package wire

import (
	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/wire/gatt"
)

// uuidToHandle converts a service+characteristic UUID pair to a handle
// First tries to use the discovery cache, then falls back to hash-based mapping
func (w *Wire) uuidToHandle(peerUUID, serviceUUID, charUUID string) uint16 {
	// Try to get handle from discovery cache first
	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if exists && connection.discoveryCache != nil {
		cache := connection.discoveryCache.(*gatt.DiscoveryCache)

		// Convert char UUID string to bytes for lookup
		// Try to parse as hex string (e.g., "2A00" -> []byte{0x00, 0x2A})
		charUUIDBytes := stringToUUIDBytes(charUUID)
		if handle, err := cache.GetCharacteristicHandle(charUUIDBytes); err == nil {
			logger.Debug(shortHash(w.hardwareUUID)+" Wire",
				"   UUID->Handle (from discovery cache): char=%s -> 0x%04X",
				shortHash(charUUID), handle)
			return handle
		}
	}

	// Fall back to hash-based mapping for backward compatibility
	// In real BLE, handles are discovered via GATT service discovery
	hash := 0
	for i := 0; i < len(serviceUUID) && i < len(charUUID); i++ {
		hash = hash*31 + int(serviceUUID[i]) + int(charUUID[i])
	}
	// Map to handle range 0x0001-0xFFFF (0x0000 is reserved)
	handle := uint16((hash % 0xFFFE) + 1)
	logger.Debug(shortHash(w.hardwareUUID)+" Wire",
		"   UUID->Handle (hash-based fallback): svc=%s, char=%s -> 0x%04X",
		shortHash(serviceUUID), shortHash(charUUID), handle)
	return handle
}

// stringToUUIDBytes converts a UUID string to bytes
// Handles both short (16-bit) and long (128-bit) UUIDs
func stringToUUIDBytes(uuid string) []byte {
	// Remove any dashes or formatting
	cleaned := ""
	for _, c := range uuid {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			cleaned += string(c)
		}
	}

	// Convert hex string to bytes
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
