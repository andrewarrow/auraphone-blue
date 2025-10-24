package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/user/auraphone-blue/tests"
)

// LogParser parses iOS and Android BLE logs into test scenarios
type LogParser struct {
	iosLog     string
	androidLog string
	devices    map[string]*tests.DeviceConfig
	timeline   []tests.TimelineEvent
	assertions []tests.Assertion
	startTime  int
}

func main() {
	iosLog := flag.String("ios", "", "Path to iOS log file")
	androidLog := flag.String("android", "", "Path to Android log file")
	output := flag.String("output", "scenario.json", "Output scenario file")
	flag.Parse()

	parser := &LogParser{
		iosLog:     *iosLog,
		androidLog: *androidLog,
		devices:    make(map[string]*tests.DeviceConfig),
		timeline:   []tests.TimelineEvent{},
		assertions: []tests.Assertion{},
	}

	if err := parser.Parse(); err != nil {
		log.Fatalf("Failed to parse logs: %v", err)
	}

	scenario := &tests.Scenario{
		Name:        "Scenario from logs",
		Description: "Auto-generated from iOS and Android logs",
		Devices:     []tests.DeviceConfig{},
		Timeline:    parser.timeline,
		Assertions:  parser.assertions,
	}

	// Convert devices map to slice
	for _, device := range parser.devices {
		scenario.Devices = append(scenario.Devices, *device)
	}

	if err := scenario.Save(*output); err != nil {
		log.Fatalf("Failed to save scenario: %v", err)
	}

	fmt.Printf("✓ Scenario saved to %s\n", *output)
	fmt.Printf("  Devices: %d\n", len(scenario.Devices))
	fmt.Printf("  Events: %d\n", len(scenario.Timeline))
	fmt.Printf("  Assertions: %d\n", len(scenario.Assertions))
}

// Parse parses the log files
func (p *LogParser) Parse() error {
	if p.iosLog != "" {
		if err := p.parseIOSLog(p.iosLog); err != nil {
			return fmt.Errorf("failed to parse iOS log: %w", err)
		}
	}

	if p.androidLog != "" {
		if err := p.parseAndroidLog(p.androidLog); err != nil {
			return fmt.Errorf("failed to parse Android log: %w", err)
		}
	}

	return nil
}

// parseIOSLog parses iOS log file
func (p *LogParser) parseIOSLog(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		p.parseIOSLine(line, lineNum)
	}

	return scanner.Err()
}

// parseIOSLine parses a single iOS log line
func (p *LogParser) parseIOSLine(line string, lineNum int) {
	// Extract timestamp if present (simplified)
	// Real implementation would parse actual timestamps

	// Pattern: [SCAN] Discovered device: <device_name>
	if strings.Contains(line, "[SCAN] Discovered device:") {
		re := regexp.MustCompile(`Discovered device:\s+(.+)`)
		matches := re.FindStringSubmatch(line)
		if len(matches) > 1 {
			deviceName := strings.TrimSpace(matches[1])
			p.addDevice(deviceName, "unknown") // Platform detected later
			p.addEvent(lineNum*100, "ios-device", tests.ActionDiscover, deviceName, nil)
		}
	}

	// Pattern: [PHOTO-COLLISION] Win/Loss
	if strings.Contains(line, "[PHOTO-COLLISION]") {
		if strings.Contains(line, "Win") {
			p.addAssertion(tests.AssertionCollisionWinner, "ios-device", "Detected in logs")
		} else if strings.Contains(line, "Loss") {
			p.addAssertion(tests.AssertionCollisionLoser, "ios-device", "Detected in logs")
		}
	}

	// Pattern: [STALE-HANDSHAKE]
	if strings.Contains(line, "[STALE-HANDSHAKE]") {
		p.addAssertion(tests.AssertionStaleHandshake, "ios-device", "Detected in logs")
		p.addEvent(lineNum*100, "ios-device", tests.ActionConnect, "", nil)
	}

	// Pattern: [COLLISION-RETRY]
	if strings.Contains(line, "[COLLISION-RETRY]") {
		re := regexp.MustCompile(`Retrying send to (?:peripheral|central):\s+(.+)`)
		matches := re.FindStringSubmatch(line)
		if len(matches) > 1 {
			targetDevice := strings.TrimSpace(matches[1])
			p.addEvent(lineNum*100, "ios-device", tests.ActionSendPhotoMetadata, targetDevice, nil)
		}
	}

	// Pattern: Connected to <device>
	if strings.Contains(line, "Connected to") {
		re := regexp.MustCompile(`Connected to\s+(.+)`)
		matches := re.FindStringSubmatch(line)
		if len(matches) > 1 {
			deviceName := strings.TrimSpace(matches[1])
			p.addAssertion(tests.AssertionConnected, "ios-device", "Connected to "+deviceName)
		}
	}

	// Pattern: Sending handshake
	if strings.Contains(line, "[HANDSHAKE]") && strings.Contains(line, "Sending") {
		p.addEvent(lineNum*100, "ios-device", tests.ActionSendHandshake, "", nil)
	}

	// Pattern: Received handshake
	if strings.Contains(line, "[HANDSHAKE]") && (strings.Contains(line, "Received") || strings.Contains(line, "from")) {
		p.addAssertion(tests.AssertionHandshakeReceived, "ios-device", "Detected in logs")
	}

	// Pattern: Photo transfer completed
	if strings.Contains(line, "[PHOTO]") && strings.Contains(line, "Completed") {
		p.addAssertion(tests.AssertionPhotoTransferCompleted, "ios-device", "Detected in logs")
	}
}

// parseAndroidLog parses Android log file
func (p *LogParser) parseAndroidLog(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		p.parseAndroidLine(line, lineNum)
	}

	return scanner.Err()
}

// parseAndroidLine parses a single Android log line
func (p *LogParser) parseAndroidLine(line string, lineNum int) {
	// Similar patterns to iOS but with Android-specific format

	// Pattern: [SCAN] New device discovered: <device_name>
	if strings.Contains(line, "[SCAN] New device discovered:") || strings.Contains(line, "[SCAN] Discovered device:") {
		re := regexp.MustCompile(`discovered:\s+(.+?)(?:,|$)`)
		matches := re.FindStringSubmatch(line)
		if len(matches) > 1 {
			deviceName := strings.TrimSpace(matches[1])
			p.addDevice(deviceName, "unknown")
			p.addEvent(lineNum*100, "android-device", tests.ActionDiscover, deviceName, nil)
		}
	}

	// Pattern: waiting for them to connect (we're peripheral)
	if strings.Contains(line, "waiting for them to connect") && strings.Contains(line, "peripheral") {
		p.addEvent(lineNum*100, "android-device", tests.ActionStartAdvertising, "", nil)
	}

	// Pattern: connecting as central
	if strings.Contains(line, "connecting as central") {
		re := regexp.MustCompile(`discovered:\s+(.+?),\s+connecting`)
		matches := re.FindStringSubmatch(line)
		if len(matches) > 1 {
			deviceName := strings.TrimSpace(matches[1])
			p.addEvent(lineNum*100, "android-device", tests.ActionConnect, deviceName, nil)
		}
	}

	// Pattern: [HANDSHAKE] Received handshake from central
	if strings.Contains(line, "[HANDSHAKE] Received handshake") {
		p.addAssertion(tests.AssertionHandshakeReceived, "android-device", "Detected in logs")
	}

	// Pattern: [STALE-HANDSHAKE]
	if strings.Contains(line, "[STALE-HANDSHAKE]") {
		re := regexp.MustCompile(`last:\s+([\d.]+)s ago`)
		matches := re.FindStringSubmatch(line)
		if len(matches) > 1 {
			seconds := matches[1]
			p.addAssertion(tests.AssertionStaleHandshake, "android-device", "Handshake stale: "+seconds+"s")
		}
	}

	// Pattern: [PROFILE-PHOTO] Completed sending photo
	if strings.Contains(line, "[PROFILE-PHOTO] Completed sending") {
		p.addAssertion(tests.AssertionPhotoTransferCompleted, "android-device", "Detected in logs")
	}
}

// Helper methods

func (p *LogParser) addDevice(deviceName, platform string) {
	// Create sanitized ID from device name
	id := strings.ToLower(strings.ReplaceAll(deviceName, " ", "-"))

	if _, exists := p.devices[id]; !exists {
		p.devices[id] = &tests.DeviceConfig{
			ID:         id,
			Platform:   platform,
			DeviceName: deviceName,
			Profile: tests.ProfileData{
				FirstName: extractFirstName(deviceName),
			},
		}
	}
}

func (p *LogParser) addEvent(timeMs int, deviceID, action, target string, data map[string]interface{}) {
	event := tests.TimelineEvent{
		TimeMs: timeMs - p.startTime, // Normalize to start at 0
		Action: action,
		Device: deviceID,
		Target: target,
		Data:   data,
	}
	p.timeline = append(p.timeline, event)
}

func (p *LogParser) addAssertion(assertionType, deviceID, comment string) {
	assertion := tests.Assertion{
		Type:    assertionType,
		Device:  deviceID,
		Comment: comment,
	}
	p.assertions = append(p.assertions, assertion)
}

func extractFirstName(deviceName string) string {
	// Try to extract first name from device name like "Alice's iPhone"
	if idx := strings.Index(deviceName, "'s "); idx > 0 {
		return deviceName[:idx]
	}
	// Otherwise use first word
	parts := strings.Fields(deviceName)
	if len(parts) > 0 {
		return parts[0]
	}
	return "Unknown"
}

func parseTimestamp(line string) int {
	// TODO: Parse actual timestamp from log line
	// For now, return 0 (will use line numbers)
	return 0
}
