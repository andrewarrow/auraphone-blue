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
