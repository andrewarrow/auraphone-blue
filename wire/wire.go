package wire

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/user/auraphone-blue/logger"
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
	localUUID            string
	basePath             string
	role                 DeviceRole
	platform             Platform
	deviceName           string  // Device name for role negotiation
	simulator            *Simulator
	mtu                  int // Current MTU for this connection
	connectionStates     map[string]ConnectionState // Per-device connection states
	distance             float64 // Simulated distance in meters for RSSI calculation
	monitorStopChans     map[string]chan struct{} // Per-device stop channels for connection monitors
	disconnectCallback   func(deviceUUID string) // Callback when connection drops
	connMutex            sync.RWMutex // Protects connectionStates and monitorStopChans
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
		localUUID:        deviceUUID,
		basePath:         "data/",
		role:             role,
		platform:         PlatformGeneric,
		deviceName:       deviceUUID,
		simulator:        NewSimulator(config),
		mtu:              config.DefaultMTU,
		connectionStates: make(map[string]ConnectionState),
		monitorStopChans: make(map[string]chan struct{}),
		distance:         1.0, // Default: 1 meter distance
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
		localUUID:        deviceUUID,
		basePath:         "data/",
		role:             RoleDual, // Both iOS and Android support dual role
		platform:         platform,
		deviceName:       deviceName,
		simulator:        NewSimulator(config),
		mtu:              config.DefaultMTU,
		connectionStates: make(map[string]ConnectionState),
		monitorStopChans: make(map[string]chan struct{}),
		distance:         1.0,
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
	// First check if file exists and is readable (avoids race with concurrent writes)
	if stat, err := os.Stat(completeFile); err == nil && stat.Size() > 0 {
		if data, err := os.ReadFile(completeFile); err == nil {
			return data, nil
		}
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
// Only returns messages that have a .ready marker indicating complete transfer
func (w *Wire) ListInbox() ([]string, error) {
	inboxPath := filepath.Join(w.basePath, w.localUUID, "inbox")
	files, err := os.ReadDir(inboxPath)
	if err != nil {
		return nil, err
	}

	// First pass: collect all .ready markers
	readyMessages := make(map[string]bool)
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		name := file.Name()
		if strings.HasSuffix(name, ".ready") {
			// Remove .ready suffix to get the base message filename
			baseName := strings.TrimSuffix(name, ".ready")
			readyMessages[baseName] = true
		}
	}

	// Second pass: collect message files that have .ready markers
	seen := make(map[string]bool)
	var filenames []string

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		name := file.Name()

		// Skip temporary files (being written)
		if strings.HasSuffix(name, ".tmp") {
			continue
		}

		// Skip .ready marker files themselves
		if strings.HasSuffix(name, ".ready") {
			continue
		}

		// Strip .partN suffix if present
		baseName := name
		if strings.Contains(name, ".part") {
			parts := strings.Split(name, ".part")
			baseName = parts[0]
		}

		// Only include messages that have a .ready marker
		if !readyMessages[baseName] {
			continue
		}

		// Deduplicate (multiple .part files become one logical message)
		if !seen[baseName] {
			filenames = append(filenames, baseName)
			seen[baseName] = true
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
// Now supports multiple simultaneous connections
func (w *Wire) Connect(targetUUID string) error {
	w.connMutex.Lock()
	currentState := w.connectionStates[targetUUID]
	if currentState != StateDisconnected && currentState != 0 {
		w.connMutex.Unlock()
		logger.Debug(fmt.Sprintf("%s wire", w.localUUID[:8]), "🔌 Connect attempt to %s BLOCKED (current state: %d)", targetUUID[:8], currentState)
		return fmt.Errorf("already connected or connecting to %s", targetUUID[:8])
	}
	w.connectionStates[targetUUID] = StateConnecting
	w.connMutex.Unlock()

	logger.Debug(fmt.Sprintf("%s wire", w.localUUID[:8]), "🔌 Connecting to %s (delay: simulated)", targetUUID[:8])

	// Simulate connection delay
	delay := w.simulator.ConnectionDelay()
	time.Sleep(delay)

	// Check if connection succeeds
	if !w.simulator.ShouldConnectionSucceed() {
		w.connMutex.Lock()
		w.connectionStates[targetUUID] = StateDisconnected
		w.connMutex.Unlock()
		logger.Warn(fmt.Sprintf("%s wire", w.localUUID[:8]), "❌ Connection to %s FAILED (simulated interference)", targetUUID[:8])
		return fmt.Errorf("connection failed (timeout or interference)")
	}

	w.connMutex.Lock()
	w.connectionStates[targetUUID] = StateConnected
	w.connMutex.Unlock()

	logger.Info(fmt.Sprintf("%s wire", w.localUUID[:8]), "✅ Connected to %s at wire level", targetUUID[:8])

	// Start connection monitoring for random disconnects
	w.startConnectionMonitoring(targetUUID)

	return nil
}

// SetDisconnectCallback sets the callback for when connection drops
func (w *Wire) SetDisconnectCallback(callback func(deviceUUID string)) {
	w.disconnectCallback = callback
}

// startConnectionMonitoring monitors connection health and randomly disconnects
// Simulates real BLE behavior: interference, distance, battery, etc.
// Now monitors per-device connection
func (w *Wire) startConnectionMonitoring(targetUUID string) {
	w.connMutex.Lock()
	if w.monitorStopChans[targetUUID] != nil {
		// Already monitoring this connection
		w.connMutex.Unlock()
		return
	}

	stopChan := make(chan struct{})
	w.monitorStopChans[targetUUID] = stopChan
	w.connMutex.Unlock()

	go func() {
		interval := time.Duration(w.simulator.config.ConnectionMonitorInterval) * time.Millisecond
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				return
			case <-ticker.C:
				// Check if connection should randomly drop
				w.connMutex.Lock()
				if w.connectionStates[targetUUID] == StateConnected && w.simulator.ShouldRandomlyDisconnect() {
					// Force disconnect
					w.connectionStates[targetUUID] = StateDisconnected

					// Notify via callback
					callback := w.disconnectCallback
					w.connMutex.Unlock()

					if callback != nil {
						callback(targetUUID)
					}

					// Stop monitoring
					w.connMutex.Lock()
					if ch, exists := w.monitorStopChans[targetUUID]; exists {
						close(ch)
						delete(w.monitorStopChans, targetUUID)
					}
					w.connMutex.Unlock()
					return
				}
				w.connMutex.Unlock()
			}
		}
	}()
}

// stopConnectionMonitoring stops the connection monitor for a specific device
func (w *Wire) stopConnectionMonitoring(targetUUID string) {
	w.connMutex.Lock()
	defer w.connMutex.Unlock()

	if ch, exists := w.monitorStopChans[targetUUID]; exists {
		close(ch)
		delete(w.monitorStopChans, targetUUID)
	}
}

// Disconnect simulates BLE disconnection from a specific device
func (w *Wire) Disconnect(targetUUID string) error {
	w.connMutex.Lock()
	if w.connectionStates[targetUUID] != StateConnected {
		w.connMutex.Unlock()
		return fmt.Errorf("not connected to %s", targetUUID[:8])
	}

	w.connectionStates[targetUUID] = StateDisconnecting
	w.connMutex.Unlock()

	// Stop monitoring
	w.stopConnectionMonitoring(targetUUID)

	// Simulate disconnection delay
	delay := w.simulator.DisconnectDelay()
	time.Sleep(delay)

	w.connMutex.Lock()
	w.connectionStates[targetUUID] = StateDisconnected
	w.connMutex.Unlock()

	return nil
}

// GetConnectionState returns the connection state for a specific device
func (w *Wire) GetConnectionState(targetUUID string) ConnectionState {
	w.connMutex.RLock()
	defer w.connMutex.RUnlock()
	return w.connectionStates[targetUUID]
}

// GetSimulator returns the simulator for accessing timing/delay functions
func (w *Wire) GetSimulator() *Simulator {
	return w.simulator
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

			// Write to target's inbox using atomic write (temp file + sync + rename)
			// This prevents readers from seeing incomplete files during write
			fragmentPath := filepath.Join(targetInboxPath, fragmentFilename)
			tempPath := fragmentPath + ".tmp"

			// Write to temp file and sync to disk before renaming
			f, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			if err != nil {
				lastErr = err
				continue
			}
			if _, err := f.Write(fragment); err != nil {
				f.Close()
				os.Remove(tempPath)
				lastErr = err
				continue
			}
			// Sync to ensure data is on disk before rename
			if err := f.Sync(); err != nil {
				f.Close()
				os.Remove(tempPath)
				lastErr = err
				continue
			}
			if err := f.Close(); err != nil {
				os.Remove(tempPath)
				lastErr = err
				continue
			}

			// Now rename atomically
			if err := os.Rename(tempPath, fragmentPath); err != nil {
				os.Remove(tempPath) // Clean up temp file
				lastErr = err
				continue
			}

			// Sync the directory to ensure the rename is visible
			// This is critical on some filesystems (APFS, ext4, etc.)
			dir, err := os.Open(targetInboxPath)
			if err == nil {
				dir.Sync()
				dir.Close()
			}

			// Success
			lastErr = nil
			break
		}

		if lastErr != nil {
			return fmt.Errorf("failed to send fragment %d after %d retries: %w", i, w.simulator.config.MaxRetries, lastErr)
		}
	}

	// Write .ready marker file to signal that all fragments are complete
	// This prevents readers from processing incomplete transfers
	readyPath := filepath.Join(targetInboxPath, filename+".ready")
	readyTempPath := readyPath + ".tmp"
	if f, err := os.OpenFile(readyTempPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644); err == nil {
		f.Sync()
		f.Close()
		if err := os.Rename(readyTempPath, readyPath); err != nil {
			os.Remove(readyTempPath)
			return fmt.Errorf("failed to write ready marker: %w", err)
		}
		// Sync directory to make marker visible
		if dir, err := os.Open(targetInboxPath); err == nil {
			dir.Sync()
			dir.Close()
		}
	} else {
		return fmt.Errorf("failed to create ready marker: %w", err)
	}

	// Copy to our outbox_history using atomic write with sync
	historyPath := filepath.Join(w.basePath, w.localUUID, "outbox_history")
	historyFilePath := filepath.Join(historyPath, filename)
	tempHistoryPath := historyFilePath + ".tmp"
	if f, err := os.OpenFile(tempHistoryPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644); err == nil {
		if _, writeErr := f.Write(data); writeErr == nil {
			if syncErr := f.Sync(); syncErr == nil {
				f.Close()
				if err := os.Rename(tempHistoryPath, historyFilePath); err != nil {
					os.Remove(tempHistoryPath)
					fmt.Printf("Warning: failed to copy to outbox_history: %v\n", err)
				}
			} else {
				f.Close()
				os.Remove(tempHistoryPath)
				fmt.Printf("Warning: failed to sync temp file for outbox_history: %v\n", syncErr)
			}
		} else {
			f.Close()
			os.Remove(tempHistoryPath)
			fmt.Printf("Warning: failed to write temp file for outbox_history: %v\n", writeErr)
		}
	} else {
		fmt.Printf("Warning: failed to open temp file for outbox_history: %v\n", err)
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

	// Copy complete message to inbox_history using atomic write with sync
	historyPath := filepath.Join(w.basePath, w.localUUID, "inbox_history")
	historyFilePath := filepath.Join(historyPath, filename)
	tempHistoryPath := historyFilePath + ".tmp"
	f, err := os.OpenFile(tempHistoryPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open temp file for inbox_history: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tempHistoryPath)
		return fmt.Errorf("failed to write temp file for inbox_history: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tempHistoryPath)
		return fmt.Errorf("failed to sync temp file for inbox_history: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tempHistoryPath)
		return fmt.Errorf("failed to close temp file for inbox_history: %w", err)
	}
	if err := os.Rename(tempHistoryPath, historyFilePath); err != nil {
		os.Remove(tempHistoryPath) // Clean up temp file
		return fmt.Errorf("failed to copy to inbox_history: %w", err)
	}

	// Delete the .ready marker file first
	readyFile := filepath.Join(inboxPath, filename+".ready")
	os.Remove(readyFile) // Ignore errors, marker might not exist

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

	logger.TraceJSON(fmt.Sprintf("%s %s", w.localUUID[:8], w.platform), "📡 TX Advertising Data", advData)

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

	logger.TraceJSON(fmt.Sprintf("%s %s", w.localUUID[:8], w.platform), fmt.Sprintf("📡 RX Advertising Data (from %s)", deviceUUID[:8]), &advData)

	return &advData, nil
}

// WriteCharacteristic sends a characteristic write WITH response (waits for ACK)
func (w *Wire) WriteCharacteristic(targetUUID, serviceUUID, charUUID string, data []byte) error {
	msg := CharacteristicMessage{
		Operation:   "write",
		ServiceUUID: serviceUUID,
		CharUUID:    charUUID,
		Data:        data,
		Timestamp:   time.Now().UnixNano(),
		SenderUUID:  w.localUUID,
	}

	logger.TraceJSON(fmt.Sprintf("%s %s", w.localUUID[:8], w.platform),
		fmt.Sprintf("📤 TX Write WITH Response (to %s, svc=%s, char=%s, %d bytes)",
			targetUUID[:8], serviceUUID[len(serviceUUID)-4:], charUUID[len(charUUID)-4:], len(data)), &msg)

	return w.sendCharacteristicMessage(targetUUID, &msg)
}

// WriteCharacteristicNoResponse sends a characteristic write WITHOUT response (fire and forget)
// Does not wait for ACK - faster but no delivery guarantee (matches real BLE)
// Returns immediately like real BLE, but transmission happens asynchronously in background
// IMPORTANT: Callback fires immediately on successful queue (NOT after transmission)
// Transmission failures happen silently in background (matches real BLE behavior)
func (w *Wire) WriteCharacteristicNoResponse(targetUUID, serviceUUID, charUUID string, data []byte) error {
	msg := CharacteristicMessage{
		Operation:   "write_no_response",
		ServiceUUID: serviceUUID,
		CharUUID:    charUUID,
		Data:        data,
		Timestamp:   time.Now().UnixNano(),
		SenderUUID:  w.localUUID,
	}

	logger.TraceJSON(fmt.Sprintf("%s %s", w.localUUID[:8], w.platform),
		fmt.Sprintf("📤 TX Write NO Response (to %s, svc=%s, char=%s, %d bytes)",
			targetUUID[:8], serviceUUID[len(serviceUUID)-4:], charUUID[len(charUUID)-4:], len(data)), &msg)

	// Transmit asynchronously - failures are silent (matches real BLE)
	go func() {
		if err := w.sendCharacteristicMessage(targetUUID, &msg); err != nil {
			// Real BLE: transmission failures are completely silent to the app
			// App has no way to know if the packet was lost
			logger.Trace(fmt.Sprintf("%s %s", w.localUUID[:8], w.platform),
				"📉 Write NO Response transmission failed silently (realistic BLE behavior): %v", err)
		}
	}()

	return nil // Returns immediately (app callback can fire now)
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

// NotifyCharacteristic sends a notification to target device with realistic delivery
// Notifications may be dropped (~1%), arrive out-of-order, or be delayed (5-20ms)
func (w *Wire) NotifyCharacteristic(targetUUID, serviceUUID, charUUID string, data []byte) error {
	msg := CharacteristicMessage{
		Operation:   "notify",
		ServiceUUID: serviceUUID,
		CharUUID:    charUUID,
		Data:        data,
		Timestamp:   time.Now().UnixNano(),
		SenderUUID:  w.localUUID,
	}

	// Simulate notification drops (1% under load)
	if w.simulator.ShouldNotificationDrop() {
		logger.Trace(fmt.Sprintf("%s %s", w.localUUID[:8], w.platform),
			"📉 Notification DROPPED (realistic BLE behavior, %d bytes lost)", len(data))
		return nil // Silently dropped (matches real BLE)
	}

	// Simulate realistic notification latency (5-20ms)
	if w.simulator.EnableNotificationReordering() {
		delay := w.simulator.NotificationDeliveryDelay()
		go func() {
			time.Sleep(delay)
			w.sendCharacteristicMessage(targetUUID, &msg)
		}()
		return nil // Non-blocking like real BLE
	}

	// Synchronous delivery (deterministic mode)
	return w.sendCharacteristicMessage(targetUUID, &msg)
}

// SubscribeCharacteristic sends a subscription request to target device
func (w *Wire) SubscribeCharacteristic(targetUUID, serviceUUID, charUUID string) error {
	msg := CharacteristicMessage{
		Operation:   "subscribe",
		ServiceUUID: serviceUUID,
		CharUUID:    charUUID,
		Timestamp:   time.Now().UnixNano(),
		SenderUUID:  w.localUUID,
	}

	logger.TraceJSON(fmt.Sprintf("%s %s", w.localUUID[:8], w.platform),
		fmt.Sprintf("📤 TX Subscribe (to %s, svc=%s, char=%s)",
			targetUUID[:8], serviceUUID[len(serviceUUID)-4:], charUUID[len(charUUID)-4:]), &msg)

	return w.sendCharacteristicMessage(targetUUID, &msg)
}

// UnsubscribeCharacteristic sends an unsubscription request to target device
func (w *Wire) UnsubscribeCharacteristic(targetUUID, serviceUUID, charUUID string) error {
	msg := CharacteristicMessage{
		Operation:   "unsubscribe",
		ServiceUUID: serviceUUID,
		CharUUID:    charUUID,
		Timestamp:   time.Now().UnixNano(),
		SenderUUID:  w.localUUID,
	}

	logger.TraceJSON(fmt.Sprintf("%s %s", w.localUUID[:8], w.platform),
		fmt.Sprintf("📤 TX Unsubscribe (to %s, svc=%s, char=%s)",
			targetUUID[:8], serviceUUID[len(serviceUUID)-4:], charUUID[len(charUUID)-4:]), &msg)

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

	// Log if we have pending messages
	if len(files) > 0 {
		logger.Trace(fmt.Sprintf("%s %s", w.localUUID[:8], w.platform),
			"📬 Found %d message(s) in inbox", len(files))
	}

	var messages []*CharacteristicMessage
	for _, filename := range files {
		data, err := w.ReadAndReassemble(filename)
		if err != nil {
			logger.Trace(fmt.Sprintf("%s %s", w.localUUID[:8], w.platform),
				"⚠️  Failed to reassemble message %s: %v", filename, err)
			continue
		}

		var msg CharacteristicMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			// Not a characteristic message, skip
			logger.Trace(fmt.Sprintf("%s %s", w.localUUID[:8], w.platform),
				"⚠️  Failed to parse message %s: %v", filename, err)
			continue
		}

		logger.TraceJSON(fmt.Sprintf("%s %s", w.localUUID[:8], w.platform),
			fmt.Sprintf("📥 RX %s Characteristic (from %s, svc=%s, char=%s, %d bytes)",
				msg.Operation, msg.SenderUUID[:8],
				msg.ServiceUUID[len(msg.ServiceUUID)-4:],
				msg.CharUUID[len(msg.CharUUID)-4:], len(msg.Data)), &msg)

		messages = append(messages, &msg)
	}

	return messages, nil
}
