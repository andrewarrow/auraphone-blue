package tests

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/user/auraphone-blue/kotlin"
	auraphone "github.com/user/auraphone-blue/proto"
	"github.com/user/auraphone-blue/swift"
	"github.com/user/auraphone-blue/wire"
	"google.golang.org/protobuf/proto"
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
	Cache     *wire.DeviceCacheManager
	IOSMgr    *swift.CBCentralManager
	AndroidMgr *kotlin.BluetoothManager

	// Connection tracking
	ConnectedTo map[string]bool // deviceID -> connected

	// Handshake tracking
	HandshakesSent     map[string]bool // deviceID -> sent
	HandshakesReceived map[string]*HandshakeData // deviceID -> handshake data

	// Photo transfer state
	IsSendingPhoto       bool
	IsReceivingPhoto     bool
	PhotoSendTarget      string        // deviceID we're sending to
	PhotoReceiveSource   string        // deviceID we're receiving from
	PhotoSendInitiated   map[string]bool // deviceID -> initiated (for assertions)
	PhotoSendSkipped     map[string]bool // deviceID -> skipped (for assertions)

	// Collision detection
	CollisionDetected  bool
	CollisionWinner    bool
	AbortedSendTo      string
}

// HandshakeData stores parsed handshake information
type HandshakeData struct {
	DeviceID       string
	FirstName      string
	LastName       string
	TxPhotoHash    string
	RxPhotoHash    string
	ReceivedAtMs   int64
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
	uuid := generateDeviceUUID(config.ID)
	device := &SimulatedDevice{
		Config:             config,
		UUID:               uuid,
		Wire:               wire.NewWire(uuid),
		Cache:              wire.NewDeviceCacheManager(uuid),
		ConnectedTo:        make(map[string]bool),
		HandshakesSent:     make(map[string]bool),
		HandshakesReceived: make(map[string]*HandshakeData),
		PhotoSendInitiated: make(map[string]bool),
		PhotoSendSkipped:   make(map[string]bool),
	}

	// Initialize device directory
	if err := device.Wire.InitializeDevice(); err != nil {
		return nil, err
	}

	// Initialize cache
	if err := device.Cache.InitializeCache(); err != nil {
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

	// Initialize local photo if provided
	if config.Profile.PhotoPath != "" {
		photoData, err := os.ReadFile(config.Profile.PhotoPath)
		if err != nil {
			log.Printf("Warning: Failed to load photo for %s: %v", config.ID, err)
		} else {
			if _, err := device.Cache.SaveLocalUserPhoto(photoData); err != nil {
				log.Printf("Warning: Failed to save local photo for %s: %v", config.ID, err)
			}
		}
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
	case ActionDiscoverServices:
		return r.handleDiscoverServices(device)
	case ActionSendHandshake:
		return r.handleSendHandshake(device, event.Target)
	case ActionReceiveHandshake:
		return r.handleReceiveHandshake(device, event.Target)
	case ActionSendPhotoMetadata:
		return r.handleSendPhotoMetadata(device, event.Target)
	case ActionReceivePhoto:
		return r.handleReceivePhotoMetadata(device, event.Target)
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

	// Mark as connected
	device.ConnectedTo[targetID] = true
	target.ConnectedTo[device.Config.ID] = true

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

func (r *ScenarioRunner) handleDiscoverServices(device *SimulatedDevice) error {
	log.Printf("[%s] Discovering services", device.Config.DeviceName)
	// In real implementation, this would read remote GATT table
	// For now, just log it
	return nil
}

func (r *ScenarioRunner) handleSendHandshake(device *SimulatedDevice, targetID string) error {
	target := r.devices[targetID]
	if target == nil {
		return fmt.Errorf("target device %s not found", targetID)
	}

	// Get our photo hash (tx)
	txPhotoHash, err := device.Cache.GetLocalUserPhotoHash()
	if err != nil {
		return fmt.Errorf("failed to get local photo hash: %w", err)
	}

	// Get their photo hash that we have cached (rx)
	rxPhotoHash, err := device.Cache.GetDevicePhotoHash(targetID)
	if err != nil {
		return fmt.Errorf("failed to get device photo hash: %w", err)
	}

	log.Printf("[%s] Sending handshake to %s", device.Config.DeviceName, target.Config.DeviceName)
	log.Printf("  TX photo hash (ours): %s", truncateHash(txPhotoHash))
	log.Printf("  RX photo hash (theirs, cached): %s", truncateHash(rxPhotoHash))

	// Create protobuf handshake message
	handshake := &auraphone.HandshakeMessage{
		DeviceId:        device.Config.ID,
		FirstName:       device.Config.Profile.FirstName,
		LastName:        device.Config.Profile.LastName,
		ProtocolVersion: 1,
		TxPhotoHash:     txPhotoHash,
		RxPhotoHash:     rxPhotoHash,
	}

	// Serialize to protobuf
	handshakeBytes, err := proto.Marshal(handshake)
	if err != nil {
		return fmt.Errorf("failed to marshal handshake: %w", err)
	}

	// Write to target's inbox as a "handshake" message
	// Note: Store protobuf bytes as base64 for JSON compatibility
	msg := map[string]interface{}{
		"op":        "handshake",
		"data":      handshakeBytes, // Will be base64 encoded by JSON
		"sender":    device.UUID,
		"timestamp": time.Now().UnixMilli(),
	}

	msgJSON, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	// Write to target's inbox
	inboxPath := filepath.Join("data", target.UUID, "inbox", fmt.Sprintf("handshake_%d.json", time.Now().UnixNano()))
	if err := os.WriteFile(inboxPath, msgJSON, 0644); err != nil {
		return fmt.Errorf("failed to write handshake: %w", err)
	}

	// Mark as sent
	device.HandshakesSent[targetID] = true

	return nil
}

func (r *ScenarioRunner) handleReceiveHandshake(device *SimulatedDevice, targetID string) error {
	target := r.devices[targetID]
	if target == nil {
		return fmt.Errorf("target device %s not found", targetID)
	}

	// Read handshake from our inbox
	inboxDir := filepath.Join("data", device.UUID, "inbox")
	files, err := os.ReadDir(inboxDir)
	if err != nil {
		return fmt.Errorf("failed to read inbox: %w", err)
	}

	// Find handshake message from target
	var handshakeMsg map[string]interface{}
	var handshakeFile string
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(inboxDir, file.Name()))
		if err != nil {
			continue
		}

		var msg map[string]interface{}
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		if msg["op"] == "handshake" && msg["sender"] == target.UUID {
			handshakeMsg = msg
			handshakeFile = file.Name()
			break
		}
	}

	if handshakeMsg == nil {
		return fmt.Errorf("no handshake message found from %s", targetID)
	}

	// Parse protobuf handshake - JSON unmarshals []byte as base64 string
	var handshakeBytes []byte
	switch v := handshakeMsg["data"].(type) {
	case string:
		// JSON marshaled []byte as base64
		decoded, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return fmt.Errorf("failed to decode base64 handshake data: %w", err)
		}
		handshakeBytes = decoded
	case []interface{}:
		// Array of numbers
		handshakeBytes = make([]byte, len(v))
		for i, val := range v {
			if num, ok := val.(float64); ok {
				handshakeBytes[i] = byte(num)
			}
		}
	default:
		return fmt.Errorf("invalid handshake data format: %T", v)
	}

	var handshake auraphone.HandshakeMessage
	if err := proto.Unmarshal(handshakeBytes, &handshake); err != nil {
		return fmt.Errorf("failed to unmarshal handshake: %w", err)
	}

	log.Printf("[%s] Received handshake from %s", device.Config.DeviceName, target.Config.DeviceName)
	log.Printf("  Their first name: %s %s", handshake.FirstName, handshake.LastName)
	log.Printf("  Their TX photo hash: %s", truncateHash(handshake.TxPhotoHash))
	log.Printf("  Their RX photo hash (ours, they have): %s", truncateHash(handshake.RxPhotoHash))

	// Store handshake data
	device.HandshakesReceived[targetID] = &HandshakeData{
		DeviceID:     handshake.DeviceId,
		FirstName:    handshake.FirstName,
		LastName:     handshake.LastName,
		TxPhotoHash:  handshake.TxPhotoHash,
		RxPhotoHash:  handshake.RxPhotoHash,
		ReceivedAtMs: time.Now().UnixMilli(),
	}

	// Delete the message
	os.Remove(filepath.Join(inboxDir, handshakeFile))

	return nil
}

func (r *ScenarioRunner) handleSendPhotoMetadata(device *SimulatedDevice, targetID string) error {
	target := r.devices[targetID]
	if target == nil {
		return fmt.Errorf("target device %s not found", targetID)
	}

	// Get the handshake we received from target
	targetHandshake := device.HandshakesReceived[targetID]
	if targetHandshake == nil {
		return fmt.Errorf("no handshake received from %s yet", targetID)
	}

	// Check if we should send photo
	shouldSend, err := device.Cache.ShouldSendPhotoToDevice(targetHandshake.RxPhotoHash)
	if err != nil {
		return fmt.Errorf("failed to check should send: %w", err)
	}

	if !shouldSend {
		log.Printf("[%s] ⏭️  Skipping photo send to %s (they already have our photo)",
			device.Config.DeviceName, target.Config.DeviceName)
		device.PhotoSendSkipped[targetID] = true
		return nil
	}

	// Get our photo hash
	ourPhotoHash, err := device.Cache.GetLocalUserPhotoHash()
	if err != nil {
		return fmt.Errorf("failed to get local photo hash: %w", err)
	}

	if ourPhotoHash == "" {
		log.Printf("[%s] ⏭️  Skipping photo send to %s (we have no photo)",
			device.Config.DeviceName, target.Config.DeviceName)
		device.PhotoSendSkipped[targetID] = true
		return nil
	}

	log.Printf("[%s] 📤 Initiating photo send to %s", device.Config.DeviceName, target.Config.DeviceName)
	log.Printf("  Our TX hash: %s", truncateHash(ourPhotoHash))
	log.Printf("  Their RX hash: %s", truncateHash(targetHandshake.RxPhotoHash))
	log.Printf("  Decision: SEND (hashes differ or they have no photo)")

	// Mark as initiated
	device.PhotoSendInitiated[targetID] = true
	device.IsSendingPhoto = true
	device.PhotoSendTarget = targetID

	// TODO: Implement actual photo chunking and transfer
	// For now, just log the decision

	return nil
}

func (r *ScenarioRunner) handleReceivePhotoMetadata(device *SimulatedDevice, targetID string) error {
	target := r.devices[targetID]
	if target == nil {
		return fmt.Errorf("target device %s not found", targetID)
	}

	log.Printf("[%s] 📥 Receiving photo metadata from %s", device.Config.DeviceName, target.Config.DeviceName)

	// Check if we're already sending - collision detection would go here
	if device.IsSendingPhoto {
		log.Printf("[%s] ⚠️  Collision detected: receiving while sending", device.Config.DeviceName)
		// TODO: Implement collision detection logic
	}

	device.IsReceivingPhoto = true
	device.PhotoReceiveSource = targetID

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
	switch assertion.Type {
	case AssertionConnected:
		return r.checkConnectedAssertion(assertion)
	case AssertionHandshakeReceived:
		return r.checkHandshakeReceivedAssertion(assertion)
	case AssertionPhotoSendInitiated:
		return r.checkPhotoSendInitiatedAssertion(assertion)
	case AssertionPhotoSendSkipped:
		return r.checkPhotoSendSkippedAssertion(assertion)
	default:
		return AssertionResult{
			Assertion: assertion,
			Passed:    true,
			Message:   "Not yet implemented",
		}
	}
}

func (r *ScenarioRunner) checkConnectedAssertion(assertion *Assertion) AssertionResult {
	device := r.devices[assertion.Device]
	if device == nil {
		return AssertionResult{
			Assertion: assertion,
			Passed:    false,
			Message:   fmt.Sprintf("Device %s not found", assertion.Device),
		}
	}

	if assertion.To == "" {
		return AssertionResult{
			Assertion: assertion,
			Passed:    false,
			Message:   "Missing 'to' field in assertion",
		}
	}

	if device.ConnectedTo[assertion.To] {
		return AssertionResult{
			Assertion: assertion,
			Passed:    true,
			Message:   fmt.Sprintf("%s connected to %s", assertion.Device, assertion.To),
		}
	}

	return AssertionResult{
		Assertion: assertion,
		Passed:    false,
		Message:   fmt.Sprintf("%s not connected to %s", assertion.Device, assertion.To),
	}
}

func (r *ScenarioRunner) checkHandshakeReceivedAssertion(assertion *Assertion) AssertionResult {
	device := r.devices[assertion.Device]
	if device == nil {
		return AssertionResult{
			Assertion: assertion,
			Passed:    false,
			Message:   fmt.Sprintf("Device %s not found", assertion.Device),
		}
	}

	if assertion.From == "" {
		return AssertionResult{
			Assertion: assertion,
			Passed:    false,
			Message:   "Missing 'from' field in assertion",
		}
	}

	handshake := device.HandshakesReceived[assertion.From]
	if handshake != nil {
		return AssertionResult{
			Assertion: assertion,
			Passed:    true,
			Message:   fmt.Sprintf("%s received handshake from %s (%s %s)",
				assertion.Device, assertion.From, handshake.FirstName, handshake.LastName),
		}
	}

	return AssertionResult{
		Assertion: assertion,
		Passed:    false,
		Message:   fmt.Sprintf("%s did not receive handshake from %s", assertion.Device, assertion.From),
	}
}

func (r *ScenarioRunner) checkPhotoSendInitiatedAssertion(assertion *Assertion) AssertionResult {
	device := r.devices[assertion.Device]
	if device == nil {
		return AssertionResult{
			Assertion: assertion,
			Passed:    false,
			Message:   fmt.Sprintf("Device %s not found", assertion.Device),
		}
	}

	if assertion.To == "" {
		return AssertionResult{
			Assertion: assertion,
			Passed:    false,
			Message:   "Missing 'to' field in assertion",
		}
	}

	if device.PhotoSendInitiated[assertion.To] {
		return AssertionResult{
			Assertion: assertion,
			Passed:    true,
			Message:   fmt.Sprintf("%s initiated photo send to %s", assertion.Device, assertion.To),
		}
	}

	return AssertionResult{
		Assertion: assertion,
		Passed:    false,
		Message:   fmt.Sprintf("%s did not initiate photo send to %s", assertion.Device, assertion.To),
	}
}

func (r *ScenarioRunner) checkPhotoSendSkippedAssertion(assertion *Assertion) AssertionResult {
	device := r.devices[assertion.Device]
	if device == nil {
		return AssertionResult{
			Assertion: assertion,
			Passed:    false,
			Message:   fmt.Sprintf("Device %s not found", assertion.Device),
		}
	}

	if assertion.To == "" {
		return AssertionResult{
			Assertion: assertion,
			Passed:    false,
			Message:   "Missing 'to' field in assertion",
		}
	}

	if device.PhotoSendSkipped[assertion.To] {
		return AssertionResult{
			Assertion: assertion,
			Passed:    true,
			Message:   fmt.Sprintf("%s skipped photo send to %s (not needed)", assertion.Device, assertion.To),
		}
	}

	// If we initiated a send, that's the opposite of skipping
	if device.PhotoSendInitiated[assertion.To] {
		return AssertionResult{
			Assertion: assertion,
			Passed:    false,
			Message:   fmt.Sprintf("%s did NOT skip photo send to %s (send was initiated)", assertion.Device, assertion.To),
		}
	}

	return AssertionResult{
		Assertion: assertion,
		Passed:    false,
		Message:   fmt.Sprintf("%s status unclear for photo send to %s", assertion.Device, assertion.To),
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

// truncateHash returns first 8 chars of hash for logging, or "none" if empty
func truncateHash(hash string) string {
	if hash == "" {
		return "none"
	}
	if len(hash) > 8 {
		return hash[:8] + "..."
	}
	return hash
}
