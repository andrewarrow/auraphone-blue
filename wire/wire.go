package wire

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// AdvertisingData represents BLE advertising packet data
type AdvertisingData struct {
	DeviceName       string   `json:"device_name,omitempty"`
	ServiceUUIDs     []string `json:"service_uuids,omitempty"`
	ManufacturerData []byte   `json:"manufacturer_data,omitempty"`
	TxPowerLevel     *int     `json:"tx_power_level,omitempty"`
	IsConnectable    bool     `json:"is_connectable"`
}

// DeviceInfo represents a discovered device on the wire
type DeviceInfo struct {
	UUID string
	Name string
	Rssi int
}

// GATTDescriptor represents a BLE descriptor
type GATTDescriptor struct {
	UUID string `json:"uuid"`
	Type string `json:"type,omitempty"` // e.g., "CCCD" for Client Characteristic Configuration
}

// GATTCharacteristic represents a BLE characteristic
type GATTCharacteristic struct {
	UUID        string           `json:"uuid"`
	Properties  []string         `json:"properties"`  // "read", "write", "notify", "indicate", etc.
	Descriptors []GATTDescriptor `json:"descriptors,omitempty"`
	Value       []byte           `json:"-"` // Current value (not serialized in gatt.json)
}

// GATTService represents a BLE service
type GATTService struct {
	UUID            string               `json:"uuid"`
	Type            string               `json:"type"` // "primary" or "secondary"
	Characteristics []GATTCharacteristic `json:"characteristics"`
}

// GATTTable represents a device's complete GATT database
type GATTTable struct {
	Services []GATTService `json:"services"`
}

// CharacteristicMessage represents a read/write/notify operation on a characteristic
type CharacteristicMessage struct {
	Operation      string `json:"op"`             // "write", "read", "notify", "indicate"
	ServiceUUID    string `json:"service"`
	CharUUID       string `json:"characteristic"`
	Data           []byte `json:"data"`
	Timestamp      int64  `json:"timestamp"`
	SenderUUID     string `json:"sender,omitempty"`
}

// Wire manages the filesystem-based communication between fake devices
type Wire struct {
	localUUID string
	basePath  string
}

// NewWire creates a new wire instance for a device
func NewWire(deviceUUID string) *Wire {
	return &Wire{
		localUUID: deviceUUID,
		basePath:  "data/",
	}
}

// SetBasePath sets the base directory for device communication
func (w *Wire) SetBasePath(path string) {
	w.basePath = path
}

// InitializeDevice creates the inbox, outbox, and history directories for this device
func (w *Wire) InitializeDevice() error {
	devicePath := filepath.Join(w.basePath, w.localUUID)
	inboxPath := filepath.Join(devicePath, "inbox")
	outboxPath := filepath.Join(devicePath, "outbox")
	inboxHistoryPath := filepath.Join(devicePath, "inbox_history")
	outboxHistoryPath := filepath.Join(devicePath, "outbox_history")

	if err := os.MkdirAll(inboxPath, 0755); err != nil {
		return fmt.Errorf("failed to create inbox: %w", err)
	}
	if err := os.MkdirAll(outboxPath, 0755); err != nil {
		return fmt.Errorf("failed to create outbox: %w", err)
	}
	if err := os.MkdirAll(inboxHistoryPath, 0755); err != nil {
		return fmt.Errorf("failed to create inbox_history: %w", err)
	}
	if err := os.MkdirAll(outboxHistoryPath, 0755); err != nil {
		return fmt.Errorf("failed to create outbox_history: %w", err)
	}

	return nil
}

// DiscoverDevices scans for other devices (UUID directories) in the base path
func (w *Wire) DiscoverDevices() ([]string, error) {
	files, err := os.ReadDir(w.basePath)
	if err != nil {
		return nil, err
	}

	var devices []string
	for _, file := range files {
		if file.IsDir() {
			deviceName := file.Name()
			// Check if it's a valid UUID and not our own device
			if _, err := uuid.Parse(deviceName); err == nil {
				if deviceName != w.localUUID {
					devices = append(devices, deviceName)
				}
			}
		}
	}

	return devices, nil
}

// StartDiscovery continuously scans for devices and calls the callback when found
func (w *Wire) StartDiscovery(callback func(deviceUUID string)) chan struct{} {
	stopChan := make(chan struct{})

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				return
			case <-ticker.C:
				devices, err := w.DiscoverDevices()
				if err != nil {
					continue
				}

				for _, deviceUUID := range devices {
					callback(deviceUUID)
				}
			}
		}
	}()

	return stopChan
}

// WriteData writes binary data to this device's outbox for another device to read
func (w *Wire) WriteData(data []byte, filename string) error {
	outboxPath := filepath.Join(w.basePath, w.localUUID, "outbox")
	filePath := filepath.Join(outboxPath, filename)

	return os.WriteFile(filePath, data, 0644)
}

// ReadData reads binary data from this device's inbox
func (w *Wire) ReadData(filename string) ([]byte, error) {
	inboxPath := filepath.Join(w.basePath, w.localUUID, "inbox")
	filePath := filepath.Join(inboxPath, filename)

	return os.ReadFile(filePath)
}

// ListInbox lists all files in this device's inbox
func (w *Wire) ListInbox() ([]string, error) {
	inboxPath := filepath.Join(w.basePath, w.localUUID, "inbox")
	files, err := os.ReadDir(inboxPath)
	if err != nil {
		return nil, err
	}

	var filenames []string
	for _, file := range files {
		if !file.IsDir() {
			filenames = append(filenames, file.Name())
		}
	}

	return filenames, nil
}

// ListOutbox lists all files in this device's outbox
func (w *Wire) ListOutbox() ([]string, error) {
	outboxPath := filepath.Join(w.basePath, w.localUUID, "outbox")
	files, err := os.ReadDir(outboxPath)
	if err != nil {
		return nil, err
	}

	var filenames []string
	for _, file := range files {
		if !file.IsDir() {
			filenames = append(filenames, file.Name())
		}
	}

	return filenames, nil
}

// SendToDevice writes data to the target device's inbox
// and copies it to this device's outbox_history for record keeping
func (w *Wire) SendToDevice(targetUUID string, data []byte, filename string) error {
	targetInboxPath := filepath.Join(w.basePath, targetUUID, "inbox")
	filePath := filepath.Join(targetInboxPath, filename)

	// Write to target's inbox
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return err
	}

	// Copy to our outbox_history
	historyPath := filepath.Join(w.basePath, w.localUUID, "outbox_history")
	historyFilePath := filepath.Join(historyPath, filename)
	if err := os.WriteFile(historyFilePath, data, 0644); err != nil {
		// Don't fail the send if history write fails, just log it
		fmt.Printf("Warning: failed to copy to outbox_history: %v\n", err)
	}

	return nil
}

// DeleteInboxFile removes a file from this device's inbox (after processing)
// and copies it to inbox_history for record keeping
func (w *Wire) DeleteInboxFile(filename string) error {
	inboxPath := filepath.Join(w.basePath, w.localUUID, "inbox")
	filePath := filepath.Join(inboxPath, filename)

	// Read the file content before deleting
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read inbox file: %w", err)
	}

	// Copy to inbox_history
	historyPath := filepath.Join(w.basePath, w.localUUID, "inbox_history")
	historyFilePath := filepath.Join(historyPath, filename)
	if err := os.WriteFile(historyFilePath, data, 0644); err != nil {
		return fmt.Errorf("failed to copy to inbox_history: %w", err)
	}

	// Delete the original file
	return os.Remove(filePath)
}

// DeleteOutboxFile removes a file from this device's outbox
func (w *Wire) DeleteOutboxFile(filename string) error {
	outboxPath := filepath.Join(w.basePath, w.localUUID, "outbox")
	filePath := filepath.Join(outboxPath, filename)

	return os.Remove(filePath)
}

// WriteGATTTable writes the GATT table to this device's gatt.json file
func (w *Wire) WriteGATTTable(table *GATTTable) error {
	devicePath := filepath.Join(w.basePath, w.localUUID)
	gattPath := filepath.Join(devicePath, "gatt.json")

	data, err := json.MarshalIndent(table, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal GATT table: %w", err)
	}

	return os.WriteFile(gattPath, data, 0644)
}

// ReadGATTTable reads the GATT table from a device's gatt.json file
func (w *Wire) ReadGATTTable(deviceUUID string) (*GATTTable, error) {
	devicePath := filepath.Join(w.basePath, deviceUUID)
	gattPath := filepath.Join(devicePath, "gatt.json")

	data, err := os.ReadFile(gattPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read GATT table: %w", err)
	}

	var table GATTTable
	if err := json.Unmarshal(data, &table); err != nil {
		return nil, fmt.Errorf("failed to unmarshal GATT table: %w", err)
	}

	return &table, nil
}

// WriteAdvertisingData writes the advertising data to this device's advertising.json file
func (w *Wire) WriteAdvertisingData(advData *AdvertisingData) error {
	devicePath := filepath.Join(w.basePath, w.localUUID)
	advPath := filepath.Join(devicePath, "advertising.json")

	data, err := json.MarshalIndent(advData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal advertising data: %w", err)
	}

	return os.WriteFile(advPath, data, 0644)
}

// ReadAdvertisingData reads the advertising data from a device's advertising.json file
func (w *Wire) ReadAdvertisingData(deviceUUID string) (*AdvertisingData, error) {
	devicePath := filepath.Join(w.basePath, deviceUUID)
	advPath := filepath.Join(devicePath, "advertising.json")

	data, err := os.ReadFile(advPath)
	if err != nil {
		// If advertising.json doesn't exist, return default advertising data
		if os.IsNotExist(err) {
			return &AdvertisingData{
				IsConnectable: true,
			}, nil
		}
		return nil, fmt.Errorf("failed to read advertising data: %w", err)
	}

	var advData AdvertisingData
	if err := json.Unmarshal(data, &advData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal advertising data: %w", err)
	}

	return &advData, nil
}

// WriteCharacteristic sends a characteristic operation message to target device
func (w *Wire) WriteCharacteristic(targetUUID, serviceUUID, charUUID string, data []byte) error {
	msg := CharacteristicMessage{
		Operation:   "write",
		ServiceUUID: serviceUUID,
		CharUUID:    charUUID,
		Data:        data,
		Timestamp:   time.Now().UnixNano(),
		SenderUUID:  w.localUUID,
	}

	return w.sendCharacteristicMessage(targetUUID, &msg)
}

// ReadCharacteristic sends a read request to target device
func (w *Wire) ReadCharacteristic(targetUUID, serviceUUID, charUUID string) error {
	msg := CharacteristicMessage{
		Operation:   "read",
		ServiceUUID: serviceUUID,
		CharUUID:    charUUID,
		Timestamp:   time.Now().UnixNano(),
		SenderUUID:  w.localUUID,
	}

	return w.sendCharacteristicMessage(targetUUID, &msg)
}

// NotifyCharacteristic sends a notification to target device
func (w *Wire) NotifyCharacteristic(targetUUID, serviceUUID, charUUID string, data []byte) error {
	msg := CharacteristicMessage{
		Operation:   "notify",
		ServiceUUID: serviceUUID,
		CharUUID:    charUUID,
		Data:        data,
		Timestamp:   time.Now().UnixNano(),
		SenderUUID:  w.localUUID,
	}

	return w.sendCharacteristicMessage(targetUUID, &msg)
}

// sendCharacteristicMessage writes a characteristic message to target's inbox as JSON
func (w *Wire) sendCharacteristicMessage(targetUUID string, msg *CharacteristicMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	filename := fmt.Sprintf("msg_%d.json", msg.Timestamp)
	return w.SendToDevice(targetUUID, data, filename)
}

// ReadCharacteristicMessages reads all characteristic messages from inbox
func (w *Wire) ReadCharacteristicMessages() ([]*CharacteristicMessage, error) {
	files, err := w.ListInbox()
	if err != nil {
		return nil, err
	}

	var messages []*CharacteristicMessage
	for _, filename := range files {
		data, err := w.ReadData(filename)
		if err != nil {
			continue
		}

		var msg CharacteristicMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			// Not a characteristic message, skip
			continue
		}

		messages = append(messages, &msg)
	}

	return messages, nil
}
