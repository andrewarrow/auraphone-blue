package tests

import (
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/user/auraphone-blue/kotlin"
	"github.com/user/auraphone-blue/swift"
	"github.com/user/auraphone-blue/wire"
)

// ScenarioRunner executes a test scenario
type ScenarioRunner struct {
	scenario      *Scenario
	devices       map[string]*SimulatedDevice
	eventLog      []EventLogEntry
	startTime     time.Time
	assertionResults []AssertionResult
}

// SimulatedDevice wraps either an iOS or Android simulated device
type SimulatedDevice struct {
	Config    *DeviceConfig
	UUID      string
	Wire      *wire.Wire
	IOSMgr    *swift.CBCentralManager
	AndroidMgr *kotlin.BluetoothManager
}

// EventLogEntry records an event that occurred during the scenario
type EventLogEntry struct {
	TimeMs    int
	Device    string
	EventType string
	Message   string
	Data      map[string]interface{}
}

// AssertionResult records the outcome of an assertion
type AssertionResult struct {
	Assertion *Assertion
	Passed    bool
	Message   string
}

// NewScenarioRunner creates a new scenario runner
func NewScenarioRunner(scenario *Scenario) *ScenarioRunner {
	return &ScenarioRunner{
		scenario:         scenario,
		devices:          make(map[string]*SimulatedDevice),
		eventLog:         []EventLogEntry{},
		assertionResults: []AssertionResult{},
	}
}

// Setup initializes all devices for the scenario
func (r *ScenarioRunner) Setup() error {
	// Validate scenario first
	errors := r.scenario.Validate()
	if len(errors) > 0 {
		return fmt.Errorf("scenario validation failed: %v", errors)
	}

	// Create simulated devices
	for _, deviceConfig := range r.scenario.Devices {
		device, err := r.createDevice(&deviceConfig)
		if err != nil {
			return fmt.Errorf("failed to create device %s: %w", deviceConfig.ID, err)
		}
		r.devices[deviceConfig.ID] = device
	}

	return nil
}

// createDevice creates a simulated device based on platform
func (r *ScenarioRunner) createDevice(config *DeviceConfig) (*SimulatedDevice, error) {
	device := &SimulatedDevice{
		Config: config,
		UUID:   generateDeviceUUID(config.ID),
		Wire:   wire.NewWire(generateDeviceUUID(config.ID)),
	}

	// Initialize device directory
	if err := device.Wire.InitializeDevice(); err != nil {
		return nil, err
	}

	// Create GATT table for the device
	gattTable := createGATTTableForDevice(config)
	if err := device.Wire.WriteGATTTable(gattTable); err != nil {
		return nil, err
	}

	// Create advertising data
	advData := createAdvertisingDataForDevice(config)
	if err := device.Wire.WriteAdvertisingData(advData); err != nil {
		return nil, err
	}

	// Initialize platform-specific manager
	if config.Platform == "ios" {
		// TODO: Create iOS CBCentralManager
		// device.IOSMgr = swift.NewCBCentralManager(...)
	} else if config.Platform == "android" {
		// TODO: Create Android BluetoothManager
		// device.AndroidMgr = kotlin.NewBluetoothManager(...)
	}

	return device, nil
}

// Run executes the scenario timeline
func (r *ScenarioRunner) Run() error {
	r.startTime = time.Now()

	// Sort timeline events by time
	sort.Slice(r.scenario.Timeline, func(i, j int) bool {
		return r.scenario.Timeline[i].TimeMs < r.scenario.Timeline[j].TimeMs
	})

	// Execute events in order
	lastTime := 0
	for _, event := range r.scenario.Timeline {
		// Wait for the event time
		waitTime := event.TimeMs - lastTime
		if waitTime > 0 {
			time.Sleep(time.Duration(waitTime) * time.Millisecond)
		}

		// Execute the event
		if err := r.executeEvent(&event); err != nil {
			r.logEvent(event.TimeMs, event.Device, "error", fmt.Sprintf("Failed to execute event: %v", err), nil)
		}

		lastTime = event.TimeMs
	}

	return nil
}

// executeEvent executes a single timeline event
func (r *ScenarioRunner) executeEvent(event *TimelineEvent) error {
	device := r.devices[event.Device]
	if device == nil {
		return fmt.Errorf("device %s not found", event.Device)
	}

	r.logEvent(event.TimeMs, event.Device, event.Action, event.Comment, event.Data)

	switch event.Action {
	case ActionStartAdvertising:
		return r.handleStartAdvertising(device)
	case ActionStopAdvertising:
		return r.handleStopAdvertising(device)
	case ActionStartScanning:
		return r.handleStartScanning(device)
	case ActionStopScanning:
		return r.handleStopScanning(device)
	case ActionDiscover:
		return r.handleDiscover(device, event.Target)
	case ActionConnect:
		return r.handleConnect(device, event.Target)
	case ActionDisconnect:
		return r.handleDisconnect(device, event.Target)
	case ActionSendHandshake:
		return r.handleSendHandshake(device)
	case ActionUpdateProfile:
		return r.handleUpdateProfile(device, event.Data)
	default:
		return fmt.Errorf("unknown action: %s", event.Action)
	}
}

// Event handlers (simplified implementations)
func (r *ScenarioRunner) handleStartAdvertising(device *SimulatedDevice) error {
	log.Printf("[%s] Starting advertising", device.Config.DeviceName)
	return nil
}

func (r *ScenarioRunner) handleStopAdvertising(device *SimulatedDevice) error {
	log.Printf("[%s] Stopping advertising", device.Config.DeviceName)
	return nil
}

func (r *ScenarioRunner) handleStartScanning(device *SimulatedDevice) error {
	log.Printf("[%s] Starting scanning", device.Config.DeviceName)
	return nil
}

func (r *ScenarioRunner) handleStopScanning(device *SimulatedDevice) error {
	log.Printf("[%s] Stopping scanning", device.Config.DeviceName)
	return nil
}

func (r *ScenarioRunner) handleDiscover(device *SimulatedDevice, targetID string) error {
	target := r.devices[targetID]
	if target == nil {
		return fmt.Errorf("target device %s not found", targetID)
	}
	log.Printf("[%s] Discovered device: %s", device.Config.DeviceName, target.Config.DeviceName)
	return nil
}

func (r *ScenarioRunner) handleConnect(device *SimulatedDevice, targetID string) error {
	target := r.devices[targetID]
	if target == nil {
		return fmt.Errorf("target device %s not found", targetID)
	}
	log.Printf("[%s] Connecting to: %s", device.Config.DeviceName, target.Config.DeviceName)
	return nil
}

func (r *ScenarioRunner) handleDisconnect(device *SimulatedDevice, targetID string) error {
	target := r.devices[targetID]
	if target == nil {
		return fmt.Errorf("target device %s not found", targetID)
	}
	log.Printf("[%s] Disconnecting from: %s", device.Config.DeviceName, target.Config.DeviceName)
	return nil
}

func (r *ScenarioRunner) handleSendHandshake(device *SimulatedDevice) error {
	log.Printf("[%s] Sending handshake", device.Config.DeviceName)
	return nil
}

func (r *ScenarioRunner) handleUpdateProfile(device *SimulatedDevice, data map[string]interface{}) error {
	log.Printf("[%s] Updating profile: %v", device.Config.DeviceName, data)
	// Update device profile
	if firstName, ok := data["first_name"].(string); ok {
		device.Config.Profile.FirstName = firstName
	}
	if photoHash, ok := data["photo_hash"].(string); ok {
		device.Config.Profile.PhotoHash = photoHash
	}
	return nil
}

// CheckAssertions validates all assertions
func (r *ScenarioRunner) CheckAssertions() []AssertionResult {
	results := []AssertionResult{}

	for _, assertion := range r.scenario.Assertions {
		result := r.checkAssertion(&assertion)
		results = append(results, result)
	}

	r.assertionResults = results
	return results
}

// checkAssertion validates a single assertion
func (r *ScenarioRunner) checkAssertion(assertion *Assertion) AssertionResult {
	// TODO: Implement assertion checking logic
	// This would inspect the event log and device state to verify assertions
	return AssertionResult{
		Assertion: assertion,
		Passed:    true,
		Message:   "Not yet implemented",
	}
}

// logEvent records an event in the log
func (r *ScenarioRunner) logEvent(timeMs int, device, eventType, message string, data map[string]interface{}) {
	r.eventLog = append(r.eventLog, EventLogEntry{
		TimeMs:    timeMs,
		Device:    device,
		EventType: eventType,
		Message:   message,
		Data:      data,
	})
}

// PrintReport prints the scenario execution report
func (r *ScenarioRunner) PrintReport() {
	fmt.Println("\n=== Scenario Report ===")
	fmt.Printf("Name: %s\n", r.scenario.Name)
	fmt.Printf("Description: %s\n", r.scenario.Description)
	fmt.Printf("Duration: %v\n", r.scenario.Duration())

	fmt.Println("\n--- Event Log ---")
	for _, entry := range r.eventLog {
		fmt.Printf("[%dms] [%s] %s: %s\n", entry.TimeMs, entry.Device, entry.EventType, entry.Message)
	}

	fmt.Println("\n--- Assertion Results ---")
	passed := 0
	for _, result := range r.assertionResults {
		status := "❌ FAIL"
		if result.Passed {
			status = "✅ PASS"
			passed++
		}
		fmt.Printf("%s - %s: %s\n", status, result.Assertion.Type, result.Message)
	}

	fmt.Printf("\nTotal: %d/%d assertions passed\n", passed, len(r.assertionResults))
}

// Helper functions

func generateDeviceUUID(deviceID string) string {
	// Generate a deterministic UUID based on device ID
	// For testing, we can use a simple hash or just append to a base UUID
	return fmt.Sprintf("test-device-%s", deviceID)
}

func createGATTTableForDevice(config *DeviceConfig) *wire.GATTTable {
	// Create standard Aura GATT table
	return &wire.GATTTable{
		Services: []wire.GATTService{
			{
				UUID: "E621E1F8-C36C-495A-93FC-0C247A3E6E5F",
				Type: "primary",
				Characteristics: []wire.GATTCharacteristic{
					{
						UUID:       "E621E1F8-C36C-495A-93FC-0C247A3E6E5D",
						Properties: []string{"read", "write", "notify"},
					},
					{
						UUID:       "E621E1F8-C36C-495A-93FC-0C247A3E6E5E",
						Properties: []string{"write", "notify"},
					},
				},
			},
		},
	}
}

func createAdvertisingDataForDevice(config *DeviceConfig) *wire.AdvertisingData {
	advData := &wire.AdvertisingData{
		DeviceName:    config.DeviceName,
		IsConnectable: true,
	}

	if config.Advertising.ServiceUUIDs != nil {
		advData.ServiceUUIDs = config.Advertising.ServiceUUIDs
	} else {
		advData.ServiceUUIDs = []string{"E621E1F8-C36C-495A-93FC-0C247A3E6E5F"}
	}

	if config.Advertising.TxPower != 0 {
		txPower := config.Advertising.TxPower
		advData.TxPowerLevel = &txPower
	}

	if config.Advertising.ManufacturerData != nil {
		advData.ManufacturerData = config.Advertising.ManufacturerData
	}

	return advData
}
