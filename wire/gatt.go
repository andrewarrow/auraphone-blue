package wire

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/util"
)

// ReadGATTTable reads GATT table from peer's filesystem
// Real BLE: this simulates service discovery over the connection
func (w *Wire) ReadGATTTable(peerUUID string) (*GATTTable, error) {
	// Simulate service discovery delay (real BLE takes 100-500ms)
	randomDelay(MinServiceDiscoveryDelay, MaxServiceDiscoveryDelay)

	deviceDir := util.GetDeviceCacheDir(peerUUID)
	gattPath := filepath.Join(deviceDir, "gatt.json")

	// Read from file (simulates GATT service discovery)
	data, err := os.ReadFile(gattPath)
	if err != nil {
		// Return empty GATT table if not found
		return &GATTTable{
			Services: []GATTService{},
		}, nil
	}

	var table GATTTable
	if err := json.Unmarshal(data, &table); err != nil {
		return nil, fmt.Errorf("failed to parse gatt.json: %w", err)
	}

	return &table, nil
}

// WriteGATTTable writes GATT table to device's filesystem
// Real BLE: this publishes our GATT database for peers to discover
func (w *Wire) WriteGATTTable(table *GATTTable) error {
	deviceDir := util.GetDeviceCacheDir(w.hardwareUUID)
	if err := os.MkdirAll(deviceDir, 0755); err != nil {
		return fmt.Errorf("failed to create device directory: %w", err)
	}

	gattPath := filepath.Join(deviceDir, "gatt.json")
	data, err := json.MarshalIndent(table, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal GATT table: %w", err)
	}

	if err := os.WriteFile(gattPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write gatt.json: %w", err)
	}

	return nil
}

// WriteCharacteristic writes to a characteristic (stub for old API)
// TODO Step 5: Implement via SendGATTMessage
func (w *Wire) WriteCharacteristic(peerUUID, serviceUUID, charUUID string, data []byte) error {
	msg := &GATTMessage{
		Type:               "gatt_request",
		Operation:          "write",
		ServiceUUID:        serviceUUID,
		CharacteristicUUID: charUUID,
		Data:               data,
	}
	return w.SendGATTMessage(peerUUID, msg)
}

// WriteCharacteristicNoResponse writes without waiting for response (stub for old API)
// TODO Step 5: Implement via SendGATTMessage
func (w *Wire) WriteCharacteristicNoResponse(peerUUID, serviceUUID, charUUID string, data []byte) error {
	// Same as WriteCharacteristic for now
	return w.WriteCharacteristic(peerUUID, serviceUUID, charUUID, data)
}

// WriteDescriptor writes to a descriptor (e.g., CCCD for enabling notifications)
// This function looks up the descriptor handle from the discovery cache
// Real BLE: CCCD descriptors are always at characteristic_handle + 1
func (w *Wire) WriteDescriptor(peerUUID, serviceUUID, charUUID, descriptorUUID string, data []byte) error {
	// For CCCD (0x2902), use characteristic handle + 1 (standard BLE)
	// Real Android and iOS behavior: CCCD is always at char handle + 1
	if descriptorUUID == "00002902-0000-1000-8000-00805f9b34fb" ||
	   descriptorUUID == "2902" {
		// CCCD write - use characteristic handle + 1
		charHandle, err := w.uuidToHandle(peerUUID, serviceUUID, charUUID)
		if err != nil {
			return fmt.Errorf("failed to resolve characteristic handle for CCCD write: %w", err)
		}
		cccdHandle := charHandle + 1

		msg := &GATTMessage{
			Type:               "gatt_request",
			Operation:          "write_descriptor",
			ServiceUUID:        serviceUUID,
			CharacteristicUUID: charUUID,
			DescriptorHandle:   cccdHandle,
			Data:               data,
		}
		return w.SendGATTMessage(peerUUID, msg)
	}

	// For other descriptors, we'd need to implement descriptor handle lookup
	return fmt.Errorf("descriptor write for non-CCCD descriptors not yet implemented")
}

// ReadCharacteristic reads from a characteristic
// Response will come via gatt_response message type to the peer's message handler
func (w *Wire) ReadCharacteristic(peerUUID, serviceUUID, charUUID string) error {
	msg := &GATTMessage{
		Type:               "gatt_request",
		Operation:          "read",
		ServiceUUID:        serviceUUID,
		CharacteristicUUID: charUUID,
	}
	return w.SendGATTMessage(peerUUID, msg)
}

// NotifyCharacteristic sends a notification (stub for old API)
// TODO Step 6: Implement via SendGATTMessage notification
func (w *Wire) NotifyCharacteristic(peerUUID, serviceUUID, charUUID string, data []byte) error {
	logger.Debug(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "📤 NotifyCharacteristic to %s: svc=%s, char=%s, len=%d",
		shortHash(peerUUID), shortHash(serviceUUID), shortHash(charUUID), len(data))
	msg := &GATTMessage{
		Type:               "gatt_notification",
		Operation:          "notify",
		ServiceUUID:        serviceUUID,
		CharacteristicUUID: charUUID,
		Data:               data,
	}
	err := w.SendGATTMessage(peerUUID, msg)
	if err != nil {
		logger.Warn(fmt.Sprintf("%s Wire", shortHash(w.hardwareUUID)), "❌ NotifyCharacteristic failed: %v", err)
	}
	return err
}

// ReadAndConsumeCharacteristicMessagesFromInbox reads messages (stub for old API)
// TODO Step 5: Remove inbox polling pattern, use SetGATTMessageHandler instead
func (w *Wire) ReadAndConsumeCharacteristicMessagesFromInbox(deviceUUID string) ([]*CharacteristicMessage, error) {
	// Return empty list - new architecture uses callbacks, not polling
	return []*CharacteristicMessage{}, nil
}
