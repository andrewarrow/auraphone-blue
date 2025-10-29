package wire

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/wire/gatt"
)

// uuidToHandle converts a service+characteristic UUID pair to a handle
// This ONLY uses the discovery cache - discovery must be performed first
// Returns an error if the characteristic is not found in the discovery cache
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
	// Parse as hex string (e.g., "2A00" -> []byte{0x00, 0x2A})
	charUUIDBytes := stringToUUIDBytes(charUUID)
	handle, err := cache.GetCharacteristicHandle(charUUIDBytes)
	if err != nil {
		return 0, fmt.Errorf("characteristic %s not found in discovery cache, run DiscoverServices/DiscoverCharacteristics first: %w", charUUID, err)
	}

	logger.Debug(shortHash(w.hardwareUUID)+" Wire",
		"   UUID->Handle (from discovery cache): char=%s -> 0x%04X",
		shortHash(charUUID), handle)
	return handle, nil
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
