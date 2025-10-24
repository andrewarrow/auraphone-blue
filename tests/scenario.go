package tests

import (
	"encoding/json"
	"os"
	"time"
)

// Scenario defines a complete BLE interaction test case
type Scenario struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Devices     []DeviceConfig    `json:"devices"`
	Timeline    []TimelineEvent   `json:"timeline"`
	Assertions  []Assertion       `json:"assertions"`
}

// DeviceConfig defines a test device configuration
type DeviceConfig struct {
	ID           string        `json:"id"`
	Platform     string        `json:"platform"` // "ios" or "android"
	DeviceName   string        `json:"device_name"`
	Profile      ProfileData   `json:"profile"`
	Advertising  AdvertisingConfig `json:"advertising,omitempty"`
}

// ProfileData represents user profile information
type ProfileData struct {
	FirstName     string `json:"first_name"`
	LastName      string `json:"last_name,omitempty"`
	Tagline       string `json:"tagline,omitempty"`
	PhotoHash     string `json:"photo_hash,omitempty"`
	PhotoPath     string `json:"photo_path,omitempty"` // Path to actual photo file for testing
}

// AdvertisingConfig defines advertising behavior
type AdvertisingConfig struct {
	ServiceUUIDs     []string `json:"service_uuids"`
	Interval         int      `json:"interval_ms"` // Advertising interval in ms
	TxPower          int      `json:"tx_power"`
	ManufacturerData []byte   `json:"manufacturer_data,omitempty"`
}

// TimelineEvent represents an action at a specific time
type TimelineEvent struct {
	TimeMs  int               `json:"time_ms"`
	Action  string            `json:"action"`
	Device  string            `json:"device"`
	Target  string            `json:"target,omitempty"`
	Data    map[string]interface{} `json:"data,omitempty"`
	Comment string            `json:"comment,omitempty"`
}

// Action types
const (
	ActionStartAdvertising = "start_advertising"
	ActionStopAdvertising  = "stop_advertising"
	ActionStartScanning    = "start_scanning"
	ActionStopScanning     = "stop_scanning"
	ActionDiscover         = "discover"
	ActionConnect          = "connect"
	ActionDisconnect       = "disconnect"
	ActionDiscoverServices = "discover_services"
	ActionSendHandshake    = "send_handshake"
	ActionReceiveHandshake = "receive_handshake"
	ActionSendPhotoMetadata = "send_photo_metadata"
	ActionSendPhotoChunk   = "send_photo_chunk"
	ActionReceivePhoto     = "receive_photo"
	ActionUpdateProfile    = "update_profile"
	ActionGoOffline        = "go_offline"    // Simulate device out of range
	ActionGoOnline         = "go_online"     // Device comes back in range
	ActionToggleBluetooth  = "toggle_bluetooth" // Bluetooth off/on
	ActionSimulateTimeout  = "simulate_timeout" // Simulate write timeout
	ActionSimulateCRCError = "simulate_crc_error"
)

// Assertion defines an expected outcome
type Assertion struct {
	Type    string            `json:"type"`
	Device  string            `json:"device,omitempty"`
	From    string            `json:"from,omitempty"`
	To      string            `json:"to,omitempty"`
	Data    map[string]interface{} `json:"data,omitempty"`
	Reason  string            `json:"reason,omitempty"`
	Comment string            `json:"comment,omitempty"`
}

// Assertion types
const (
	AssertionConnected           = "connected"
	AssertionDisconnected        = "disconnected"
	AssertionHandshakeReceived   = "handshake_received"
	AssertionPhotoTransferStarted = "photo_transfer_started"
	AssertionPhotoTransferCompleted = "photo_transfer_completed"
	AssertionPhotoTransferFailed = "photo_transfer_failed"
	AssertionCollisionDetected   = "collision_detected"
	AssertionCollisionWinner     = "collision_winner"
	AssertionCollisionLoser      = "collision_loser"
	AssertionStaleHandshake      = "stale_handshake"
	AssertionReconnected         = "reconnected"
	AssertionChunkRetry          = "chunk_retry"
	AssertionTransferAborted     = "transfer_aborted"
)

// LoadScenario loads a scenario from a JSON file
func LoadScenario(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var scenario Scenario
	if err := json.Unmarshal(data, &scenario); err != nil {
		return nil, err
	}

	return &scenario, nil
}

// SaveScenario saves a scenario to a JSON file
func (s *Scenario) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// GetEvent returns the event at the specified time
func (s *Scenario) GetEvent(timeMs int) *TimelineEvent {
	for i, event := range s.Timeline {
		if event.TimeMs == timeMs {
			return &s.Timeline[i]
		}
	}
	return nil
}

// GetEventsInRange returns all events within a time range
func (s *Scenario) GetEventsInRange(startMs, endMs int) []TimelineEvent {
	var events []TimelineEvent
	for _, event := range s.Timeline {
		if event.TimeMs >= startMs && event.TimeMs <= endMs {
			events = append(events, event)
		}
	}
	return events
}

// GetDeviceByID returns a device config by ID
func (s *Scenario) GetDeviceByID(id string) *DeviceConfig {
	for i, device := range s.Devices {
		if device.ID == id {
			return &s.Devices[i]
		}
	}
	return nil
}

// Duration returns the total duration of the scenario
func (s *Scenario) Duration() time.Duration {
	if len(s.Timeline) == 0 {
		return 0
	}
	maxTime := 0
	for _, event := range s.Timeline {
		if event.TimeMs > maxTime {
			maxTime = event.TimeMs
		}
	}
	return time.Duration(maxTime) * time.Millisecond
}

// Validate checks if the scenario is valid
func (s *Scenario) Validate() []string {
	var errors []string

	// Check that all referenced devices exist
	deviceIDs := make(map[string]bool)
	for _, device := range s.Devices {
		deviceIDs[device.ID] = true
	}

	for _, event := range s.Timeline {
		if event.Device != "" && !deviceIDs[event.Device] {
			errors = append(errors, "Event references unknown device: "+event.Device)
		}
		if event.Target != "" && !deviceIDs[event.Target] {
			errors = append(errors, "Event references unknown target: "+event.Target)
		}
	}

	for _, assertion := range s.Assertions {
		if assertion.Device != "" && !deviceIDs[assertion.Device] {
			errors = append(errors, "Assertion references unknown device: "+assertion.Device)
		}
		if assertion.From != "" && !deviceIDs[assertion.From] {
			errors = append(errors, "Assertion references unknown 'from' device: "+assertion.From)
		}
		if assertion.To != "" && !deviceIDs[assertion.To] {
			errors = append(errors, "Assertion references unknown 'to' device: "+assertion.To)
		}
	}

	return errors
}
