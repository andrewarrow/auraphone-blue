package wire

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// DeviceRole represents the BLE role(s) a device can perform
type DeviceRole int

const (
	RoleCentralOnly     DeviceRole = 1 << 0 // Can scan and connect (rarely used alone)
	RolePeripheralOnly  DeviceRole = 1 << 1 // Can advertise and accept connections
	RoleDual            DeviceRole = RoleCentralOnly | RolePeripheralOnly // Both roles (iOS and Android)
)

// Platform represents the device platform for role negotiation
type Platform string

const (
	PlatformIOS     Platform = "ios"
	PlatformAndroid Platform = "android"
	PlatformGeneric Platform = "generic"
)

// Wire manages the filesystem-based communication between fake devices
type Wire struct {
	localUUID  string
	basePath   string
	role       DeviceRole
	platform   Platform
	deviceName string  // Device name for role negotiation
	simulator  *Simulator
	mtu        int // Current MTU for this connection
	connState  ConnectionState
	distance   float64 // Simulated distance in meters for RSSI calculation
}

// NewWire creates a new wire instance for a device with default dual role
func NewWire(deviceUUID string) *Wire {
	return NewWireWithPlatform(deviceUUID, PlatformGeneric, "", nil)
}

// NewWireWithRole creates a wire with specific role and simulation config (deprecated, use NewWireWithPlatform)
func NewWireWithRole(deviceUUID string, role DeviceRole, config *SimulationConfig) *Wire {
	if config == nil {
		config = DefaultSimulationConfig()
	}

	return &Wire{
		localUUID:  deviceUUID,
		basePath:   "data/",
		role:       role,
		platform:   PlatformGeneric,
		deviceName: deviceUUID,
		simulator:  NewSimulator(config),
		mtu:        config.DefaultMTU,
		connState:  StateDisconnected,
		distance:   1.0, // Default: 1 meter distance
	}
}

// NewWireWithPlatform creates a wire with platform-specific behavior
func NewWireWithPlatform(deviceUUID string, platform Platform, deviceName string, config *SimulationConfig) *Wire {
	if config == nil {
		config = DefaultSimulationConfig()
	}

	if deviceName == "" {
		deviceName = deviceUUID
	}

	return &Wire{
		localUUID:  deviceUUID,
		basePath:   "data/",
		role:       RoleDual, // Both iOS and Android support dual role
		platform:   platform,
		deviceName: deviceName,
		simulator:  NewSimulator(config),
		mtu:        config.DefaultMTU,
		connState:  StateDisconnected,
		distance:   1.0,
	}
}

// SetDistance sets the simulated distance for RSSI calculation
func (w *Wire) SetDistance(meters float64) {
	w.distance = meters
}

// GetRSSI returns simulated RSSI based on current distance
func (w *Wire) GetRSSI() int {
	return w.simulator.GenerateRSSI(w.distance)
}

// GetRole returns the device's BLE role
func (w *Wire) GetRole() DeviceRole {
	return w.role
}

// GetPlatform returns the device's platform
func (w *Wire) GetPlatform() Platform {
	return w.platform
}

// GetDeviceName returns the device's name used for role negotiation
func (w *Wire) GetDeviceName() string {
	return w.deviceName
}

// CanScan returns true if device can scan (Central role)
func (w *Wire) CanScan() bool {
	return w.role&RoleCentralOnly != 0
}

// CanAdvertise returns true if device can advertise (Peripheral role)
func (w *Wire) CanAdvertise() bool {
	return w.role&RolePeripheralOnly != 0
}

// ShouldActAsCentral determines if this device should initiate connection to target
// Implements platform-specific role negotiation:
// - iOS always acts as Central (initiates connections)
// - Android acts as Central when connecting to another Android with smaller device name
// - Android acts as Peripheral when connecting to iOS (for compatibility)
func (w *Wire) ShouldActAsCentral(targetWire *Wire) bool {
	// iOS always acts as Central
	if w.platform == PlatformIOS {
		return true
	}

	// Android connecting to iOS: act as Peripheral (iOS will connect to us)
	if w.platform == PlatformAndroid && targetWire.platform == PlatformIOS {
		return false
	}

	// Android-to-Android: use lexicographic device name comparison
	// Device with LARGER name acts as Central
	if w.platform == PlatformAndroid && targetWire.platform == PlatformAndroid {
		return w.deviceName > targetWire.deviceName
	}

	// Generic/unknown platforms: both can act as Central
	return true
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

// StartDiscovery continuously scans for devices with realistic timing
// Devices are discovered after a random delay (100ms-1s) to simulate advertising intervals
func (w *Wire) StartDiscovery(callback func(deviceUUID string)) chan struct{} {
	stopChan := make(chan struct{})

	if !w.CanScan() {
		// Device cannot scan (e.g., Android peripheral-only mode)
		// Return a closed channel immediately
		close(stopChan)
		return stopChan
	}

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		discoveredDevices := make(map[string]bool)

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
					// Check if device can be discovered (must be advertising)
					targetWire := NewWire(deviceUUID)
					targetWire.SetBasePath(w.basePath)

					// Skip if not first discovery (already seen)
					if discoveredDevices[deviceUUID] {
						// Still call callback for periodic re-discovery
						callback(deviceUUID)
						continue
					}

					// Simulate realistic discovery delay
					delay := w.simulator.DiscoveryDelay()
					time.Sleep(delay)

					discoveredDevices[deviceUUID] = true
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

// ReadAndReassemble reads a message, handling fragmented .part files
// This simulates how real BLE stacks reassemble fragmented packets transparently
func (w *Wire) ReadAndReassemble(filename string) ([]byte, error) {
	inboxPath := filepath.Join(w.basePath, w.localUUID, "inbox")

	// Try reading the complete file first (no fragmentation)
	completeFile := filepath.Join(inboxPath, filename)
	if data, err := os.ReadFile(completeFile); err == nil {
		return data, nil
	}

	// File doesn't exist as single file, look for .part files
	var fragments [][]byte
	partIndex := 0

	for {
		partFile := filepath.Join(inboxPath, fmt.Sprintf("%s.part%d", filename, partIndex))
		data, err := os.ReadFile(partFile)
		if err != nil {
			if partIndex == 0 {
				// No fragments found either
				return nil, fmt.Errorf("file not found: %s", filename)
			}
			// No more fragments
			break
		}
		fragments = append(fragments, data)
		partIndex++
	}

	// Reassemble fragments
	var complete []byte
	for _, frag := range fragments {
		complete = append(complete, frag...)
	}

	return complete, nil
}

// ListInbox lists all logical messages in this device's inbox
// Returns base filenames without .part suffixes (simulates BLE stack behavior)
func (w *Wire) ListInbox() ([]string, error) {
	inboxPath := filepath.Join(w.basePath, w.localUUID, "inbox")
	files, err := os.ReadDir(inboxPath)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var filenames []string

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		name := file.Name()

		// Strip .partN suffix if present
		if strings.Contains(name, ".part") {
			parts := strings.Split(name, ".part")
			name = parts[0]
		}

		// Deduplicate (multiple .part files become one logical message)
		if !seen[name] {
			filenames = append(filenames, name)
			seen[name] = true
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

// Connect simulates BLE connection with realistic timing and failures
// Returns error if connection fails (simulates ~1.6% failure rate)
func (w *Wire) Connect(targetUUID string) error {
	if w.connState != StateDisconnected {
		return fmt.Errorf("already connected or connecting")
	}

	w.connState = StateConnecting

	// Simulate connection delay
	delay := w.simulator.ConnectionDelay()
	time.Sleep(delay)

	// Check if connection succeeds
	if !w.simulator.ShouldConnectionSucceed() {
		w.connState = StateDisconnected
		return fmt.Errorf("connection failed (timeout or interference)")
	}

	w.connState = StateConnected
	return nil
}

// Disconnect simulates BLE disconnection
func (w *Wire) Disconnect() error {
	if w.connState != StateConnected {
		return fmt.Errorf("not connected")
	}

	w.connState = StateDisconnecting

	// Simulate disconnection delay
	delay := w.simulator.DisconnectDelay()
	time.Sleep(delay)

	w.connState = StateDisconnected
	return nil
}

// GetConnectionState returns the current connection state
func (w *Wire) GetConnectionState() ConnectionState {
	return w.connState
}

// SendToDevice writes data to the target device's inbox with realistic packet loss and retries
// Simulates ~1.5% packet loss with automatic retries (3 attempts)
// Overall success rate: ~98.4%
func (w *Wire) SendToDevice(targetUUID string, data []byte, filename string) error {
	targetInboxPath := filepath.Join(w.basePath, targetUUID, "inbox")

	// Fragment data if it exceeds MTU
	fragments := w.simulator.FragmentData(data, w.mtu)

	// Send each fragment with retry logic
	for i, fragment := range fragments {
		fragmentFilename := filename
		if len(fragments) > 1 {
			fragmentFilename = fmt.Sprintf("%s.part%d", filename, i)
		}

		var lastErr error
		for attempt := 0; attempt <= w.simulator.config.MaxRetries; attempt++ {
			// Simulate packet loss
			if attempt > 0 {
				// This is a retry, add delay
				time.Sleep(time.Duration(w.simulator.config.RetryDelay) * time.Millisecond)
			}

			if !w.simulator.ShouldPacketSucceed() && attempt < w.simulator.config.MaxRetries {
				// Packet lost, will retry
				lastErr = fmt.Errorf("packet loss (attempt %d/%d)", attempt+1, w.simulator.config.MaxRetries+1)
				continue
			}

			// Write to target's inbox
			fragmentPath := filepath.Join(targetInboxPath, fragmentFilename)
			if err := os.WriteFile(fragmentPath, fragment, 0644); err != nil {
				lastErr = err
				continue
			}

			// Success
			lastErr = nil
			break
		}

		if lastErr != nil {
			return fmt.Errorf("failed to send fragment %d after %d retries: %w", i, w.simulator.config.MaxRetries, lastErr)
		}
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
// and copies the complete reassembled message to inbox_history for record keeping
// Handles both complete files and fragmented .part files
func (w *Wire) DeleteInboxFile(filename string) error {
	inboxPath := filepath.Join(w.basePath, w.localUUID, "inbox")

	// Read and reassemble (handles .part files transparently)
	data, err := w.ReadAndReassemble(filename)
	if err != nil {
		return fmt.Errorf("failed to read inbox file: %w", err)
	}

	// Copy complete message to inbox_history
	historyPath := filepath.Join(w.basePath, w.localUUID, "inbox_history")
	historyFilePath := filepath.Join(historyPath, filename)
	if err := os.WriteFile(historyFilePath, data, 0644); err != nil {
		return fmt.Errorf("failed to copy to inbox_history: %w", err)
	}

	// Delete the original file(s)
	// Try deleting complete file first
	completeFile := filepath.Join(inboxPath, filename)
	if err := os.Remove(completeFile); err == nil {
		return nil // Complete file deleted
	}

	// Delete all .part files
	partIndex := 0
	for {
		partFile := filepath.Join(inboxPath, fmt.Sprintf("%s.part%d", filename, partIndex))
		if err := os.Remove(partFile); err != nil {
			break // No more parts
		}
		partIndex++
	}

	return nil
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
