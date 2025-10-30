package testreport

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DeviceInfo holds information about a test device
type DeviceInfo struct {
	HardwareUUID   string
	DeviceID       string
	Platform       string
	PhotoHash      string // Full 64-char hash
	PhotoHashShort string // First 8 chars for display
	FirstName      string // From advertising or mesh_view
	LastName       string // From own profile
}

// PhotoMatrix tracks which devices have which photos
type PhotoMatrix map[string]map[string]bool // fromDeviceID -> toDeviceID -> hasPhoto

// TestIssue tracks problems found during testing
type TestIssue struct {
	Severity    string // "ERROR" or "WARNING"
	FromDevice  string
	ToDevice    string
	PhotoHash   string
	Description string
	Timeline    []string
}

// DebugStats holds debug statistics for a device
type DebugStats struct {
	DeviceID string

	// Connection stats
	ConnectionEvents     int
	ConnectionErrors     int
	CBLifecycleEvents    int // iOS-specific

	// BLE Protocol stats
	ATTPacketsRx         int
	ATTPacketsTx         int
	L2CAPPacketsRx       int
	L2CAPPacketsTx       int
	GATTOperations       int

	// GATT operation breakdown
	GATTReads            int
	GATTWrites           int
	GATTNotifications    int

	// Gossip stats
	GossipAuditEvents    int

	// Socket health
	SocketStatus         string
	SocketErrors         int
	SocketUptime         int
}

// Generate creates a test report from the data directory
func Generate(dataDir string) error {
	if dataDir == "" {
		dataDir = os.ExpandEnv("$HOME/.auraphone-blue-data")
	}

	timestamp := time.Now().Format("2006-01-02_15-04-05")
	reportPath := filepath.Join(dataDir, fmt.Sprintf("test_report_%s.md", timestamp))

	fmt.Printf("Generating test report from: %s\n", dataDir)
	fmt.Printf("Report will be written to: %s\n", reportPath)

	// Discover all devices
	devices, err := discoverDevices(dataDir)
	if err != nil {
		return fmt.Errorf("error discovering devices: %w", err)
	}

	if len(devices) == 0 {
		return fmt.Errorf("no devices found in %s", dataDir)
	}

	fmt.Printf("Found %d devices\n", len(devices))

	// Build photo matrix
	matrix := buildPhotoMatrix(devices, dataDir)

	// Collect debug stats
	debugStats := make(map[string]DebugStats)
	for _, device := range devices {
		debugStats[device.DeviceID] = collectDebugStats(device, dataDir)
	}

	// Detect issues
	issues := detectIssues(devices, matrix, dataDir)

	// Generate report
	report := generateReport(timestamp, devices, matrix, issues, debugStats, dataDir)

	// Write report
	if err := os.WriteFile(reportPath, []byte(report), 0644); err != nil {
		return fmt.Errorf("error writing report: %w", err)
	}

	fmt.Printf("✅ Report written to: %s\n", reportPath)

	// Print summary to console
	if len(issues) > 0 {
		fmt.Printf("\n❌ Found %d issues:\n", len(issues))
		for _, issue := range issues {
			fmt.Printf("  [%s] %s\n", issue.Severity, issue.Description)
		}
	} else {
		fmt.Printf("\n✅ All tests passed!\n")
	}

	return nil
}

func discoverDevices(dataDir string) ([]DeviceInfo, error) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil, err
	}

	var devices []DeviceInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		hardwareUUID := entry.Name()
		// Skip non-UUID directories
		if !strings.Contains(hardwareUUID, "-") {
			continue
		}

		deviceInfo := DeviceInfo{
			HardwareUUID: hardwareUUID,
		}

		// Load device ID - try identity_mappings.json first, fall back to cache/device_id.json
		identityPath := filepath.Join(dataDir, hardwareUUID, "identity_mappings.json")
		if data, err := os.ReadFile(identityPath); err == nil {
			var idMap struct {
				OurDeviceID string `json:"our_device_id"`
			}
			if json.Unmarshal(data, &idMap) == nil {
				deviceInfo.DeviceID = idMap.OurDeviceID
			}
		}

		// Fall back to cache/device_id.json if identity_mappings.json doesn't exist
		if deviceInfo.DeviceID == "" {
			cachePath := filepath.Join(dataDir, hardwareUUID, "cache", "device_id.json")
			if data, err := os.ReadFile(cachePath); err == nil {
				var cacheID struct {
					DeviceID string `json:"device_id"`
				}
				if json.Unmarshal(data, &cacheID) == nil {
					deviceInfo.DeviceID = cacheID.DeviceID
				}
			}
		}

		// Get platform from advertising data
		advPath := filepath.Join(dataDir, hardwareUUID, "advertising.json")
		if data, err := os.ReadFile(advPath); err == nil {
			var adv struct {
				DeviceName string `json:"device_name"`
			}
			if json.Unmarshal(data, &adv) == nil {
				if strings.Contains(adv.DeviceName, "iOS") {
					deviceInfo.Platform = "iOS"
				} else if strings.Contains(adv.DeviceName, "Android") {
					deviceInfo.Platform = "Android"
				}
			}
		}

		// Get own photo hash by finding photo metadata that matches this device
		photosDir := filepath.Join(dataDir, hardwareUUID, "photos")
		if entries, err := os.ReadDir(photosDir); err == nil {
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".json") {
					metaPath := filepath.Join(photosDir, entry.Name())
					if data, err := os.ReadFile(metaPath); err == nil {
						var meta struct {
							DeviceID string `json:"device_id"`
						}
						if json.Unmarshal(data, &meta) == nil && meta.DeviceID == deviceInfo.DeviceID {
							// This is their own photo
							photoHash := strings.TrimSuffix(entry.Name(), ".json")
							deviceInfo.PhotoHash = photoHash
							if len(photoHash) >= 8 {
								deviceInfo.PhotoHashShort = photoHash[:8]
							} else {
								deviceInfo.PhotoHashShort = photoHash
							}
							break
						}
					}
				}
			}
		}

		// Load first_name from mesh_view.json (gossip protocol)
		meshViewPath := filepath.Join(dataDir, hardwareUUID, "cache", "mesh_view.json")
		if data, err := os.ReadFile(meshViewPath); err == nil {
			var meshView struct {
				Devices map[string]struct {
					FirstName string `json:"first_name"`
				} `json:"devices"`
			}
			if json.Unmarshal(data, &meshView) == nil {
				// Look for ourselves in the mesh view (shouldn't be there, but check)
				// Actually, mesh_view only contains OTHER devices, not ourselves
				// So we need to get our own first_name from our own profile
			}
		}

		// Load own profile (first_name and last_name)
		profilePath := filepath.Join(dataDir, hardwareUUID, "cache", "profiles", deviceInfo.DeviceID+".json")
		if data, err := os.ReadFile(profilePath); err == nil {
			var profile struct {
				FirstName string `json:"FirstName"`
				LastName  string `json:"LastName"`
			}
			if json.Unmarshal(data, &profile) == nil {
				deviceInfo.FirstName = profile.FirstName
				deviceInfo.LastName = profile.LastName
			}
		}

		// If we couldn't load from profile, try to parse from test setup
		// (test harness often sets first_name in profile map)
		if deviceInfo.FirstName == "" {
			// Try to extract from identity_mappings or other sources
			// For now, leave empty - the report will show this as missing
		}

		devices = append(devices, deviceInfo)
	}

	return devices, nil
}

func buildPhotoMatrix(devices []DeviceInfo, dataDir string) PhotoMatrix {
	matrix := make(PhotoMatrix)

	for _, fromDevice := range devices {
		if fromDevice.PhotoHash == "" {
			continue
		}

		matrix[fromDevice.DeviceID] = make(map[string]bool)

		for _, toDevice := range devices {
			if fromDevice.HardwareUUID == toDevice.HardwareUUID {
				// Own photo
				matrix[fromDevice.DeviceID][toDevice.DeviceID] = true
				continue
			}

			// Check if toDevice has fromDevice's photo
			photosDir := filepath.Join(dataDir, toDevice.HardwareUUID, "photos")
			photoPath := filepath.Join(photosDir, fromDevice.PhotoHash+".jpg")
			_, err := os.Stat(photoPath)
			matrix[fromDevice.DeviceID][toDevice.DeviceID] = (err == nil)
		}
	}

	return matrix
}

func detectIssues(devices []DeviceInfo, matrix PhotoMatrix, dataDir string) []TestIssue {
	var issues []TestIssue

	for _, fromDevice := range devices {
		if fromDevice.PhotoHash == "" {
			continue
		}

		for _, toDevice := range devices {
			if fromDevice.HardwareUUID == toDevice.HardwareUUID {
				continue // Skip self
			}

			hasPhoto := matrix[fromDevice.DeviceID][toDevice.DeviceID]
			if !hasPhoto {
				// Found a missing photo - investigate why
				issue := TestIssue{
					Severity:    "ERROR",
					FromDevice:  fromDevice.DeviceID,
					ToDevice:    toDevice.DeviceID,
					PhotoHash:   fromDevice.PhotoHashShort,
					Description: fmt.Sprintf("%s missing photo from %s", toDevice.DeviceID, fromDevice.DeviceID),
					Timeline:    investigateFailure(fromDevice, toDevice, dataDir),
				}
				issues = append(issues, issue)
			}
		}
	}

	return issues
}

func investigateFailure(fromDevice, toDevice DeviceInfo, dataDir string) []string {
	timeline := []string{}

	// Check photo timeline for clues
	timelinePath := filepath.Join(dataDir, toDevice.HardwareUUID, "photo_timeline.jsonl")
	if data, err := os.ReadFile(timelinePath); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			var event struct {
				Timestamp int64  `json:"timestamp"`
				Event     string `json:"event"`
				PhotoHash string `json:"photo_hash"`
				DeviceID  string `json:"device_id"`
				Error     string `json:"error,omitempty"`
			}
			if json.Unmarshal([]byte(line), &event) == nil {
				if event.PhotoHash == fromDevice.PhotoHashShort || event.DeviceID == fromDevice.DeviceID {
					ts := time.Unix(0, event.Timestamp).Format("15:04:05")
					desc := fmt.Sprintf("%s - %s", ts, event.Event)
					if event.Error != "" {
						desc += fmt.Sprintf(" (error: %s)", event.Error)
					}
					timeline = append(timeline, desc)
				}
			}
		}
	}

	// Check connection events
	connEventsPath := filepath.Join(dataDir, toDevice.HardwareUUID, "connection_events.jsonl")
	if data, err := os.ReadFile(connEventsPath); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			var event struct {
				Timestamp  int64  `json:"timestamp"`
				Event      string `json:"event"`
				RemoteUUID string `json:"remote_uuid,omitempty"`
				SocketType string `json:"socket_type,omitempty"`
				Error      string `json:"error,omitempty"`
			}
			if json.Unmarshal([]byte(line), &event) == nil {
				if event.RemoteUUID != "" && strings.HasPrefix(fromDevice.HardwareUUID, event.RemoteUUID[:8]) {
					ts := time.Unix(0, event.Timestamp).Format("15:04:05")
					desc := fmt.Sprintf("%s - connection %s [%s]", ts, event.Event, event.SocketType)
					if event.Error != "" {
						desc += fmt.Sprintf(" (%s)", event.Error)
					}
					timeline = append(timeline, desc)
				}
			}
		}
	}

	if len(timeline) == 0 {
		timeline = append(timeline, "No relevant events found in logs")
	}

	return timeline
}

func collectDebugStats(device DeviceInfo, dataDir string) DebugStats {
	stats := DebugStats{
		DeviceID: device.DeviceID,
	}

	// Read connection_events.jsonl
	connEventsPath := filepath.Join(dataDir, device.HardwareUUID, "connection_events.jsonl")
	if data, err := os.ReadFile(connEventsPath); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			var event struct {
				Event string `json:"event"`
				Error string `json:"error,omitempty"`
			}
			if json.Unmarshal([]byte(line), &event) == nil {
				stats.ConnectionEvents++
				if event.Error != "" {
					stats.ConnectionErrors++
				}
			}
		}
	}

	// Read cb_connection_lifecycle.jsonl (iOS only)
	cbLifecyclePath := filepath.Join(dataDir, device.HardwareUUID, "cb_connection_lifecycle.jsonl")
	if data, err := os.ReadFile(cbLifecyclePath); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if line != "" {
				stats.CBLifecycleEvents++
			}
		}
	}

	// Read debug/att_packets.jsonl
	attPacketsPath := filepath.Join(dataDir, device.HardwareUUID, "debug", "att_packets.jsonl")
	if data, err := os.ReadFile(attPacketsPath); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			var packet struct {
				Direction string `json:"direction"`
			}
			if json.Unmarshal([]byte(line), &packet) == nil {
				if packet.Direction == "rx" {
					stats.ATTPacketsRx++
				} else if packet.Direction == "tx" {
					stats.ATTPacketsTx++
				}
			}
		}
	}

	// Read debug/l2cap_packets.jsonl
	l2capPacketsPath := filepath.Join(dataDir, device.HardwareUUID, "debug", "l2cap_packets.jsonl")
	if data, err := os.ReadFile(l2capPacketsPath); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			var packet struct {
				Direction string `json:"direction"`
			}
			if json.Unmarshal([]byte(line), &packet) == nil {
				if packet.Direction == "rx" {
					stats.L2CAPPacketsRx++
				} else if packet.Direction == "tx" {
					stats.L2CAPPacketsTx++
				}
			}
		}
	}

	// Read debug/gatt_operations.jsonl
	gattOpsPath := filepath.Join(dataDir, device.HardwareUUID, "debug", "gatt_operations.jsonl")
	if data, err := os.ReadFile(gattOpsPath); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			var op struct {
				Operation string `json:"operation"`
			}
			if json.Unmarshal([]byte(line), &op) == nil {
				stats.GATTOperations++
				switch op.Operation {
				case "read", "read_response":
					stats.GATTReads++
				case "write", "write_request":
					stats.GATTWrites++
				case "notify", "notification":
					stats.GATTNotifications++
				}
			}
		}
	}

	// Read gossip_audit.jsonl
	gossipAuditPath := filepath.Join(dataDir, device.HardwareUUID, "gossip_audit.jsonl")
	if data, err := os.ReadFile(gossipAuditPath); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if line != "" {
				stats.GossipAuditEvents++
			}
		}
	}

	// Read socket_health.json
	socketHealthPath := filepath.Join(dataDir, device.HardwareUUID, "socket_health.json")
	if data, err := os.ReadFile(socketHealthPath); err == nil {
		var health struct {
			PeripheralSocket struct {
				Status       string `json:"status"`
				TotalErrors  int    `json:"total_errors"`
				UptimeSeconds int   `json:"uptime_seconds"`
			} `json:"peripheral_socket"`
		}
		if json.Unmarshal(data, &health) == nil {
			stats.SocketStatus = health.PeripheralSocket.Status
			stats.SocketErrors = health.PeripheralSocket.TotalErrors
			stats.SocketUptime = health.PeripheralSocket.UptimeSeconds
		}
	}

	return stats
}

func generateReport(timestamp string, devices []DeviceInfo, matrix PhotoMatrix, issues []TestIssue, debugStats map[string]DebugStats, dataDir string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# Test Report: %s\n\n", timestamp))

	// Devices section
	sb.WriteString("## Devices\n\n")
	for _, device := range devices {
		nameInfo := ""
		if device.FirstName != "" && device.LastName != "" {
			nameInfo = fmt.Sprintf(" - Name: %s %s", device.FirstName, device.LastName)
		} else if device.FirstName != "" {
			nameInfo = fmt.Sprintf(" - Name: %s (last_name missing)", device.FirstName)
		} else if device.LastName != "" {
			nameInfo = fmt.Sprintf(" - Name: (first_name missing) %s", device.LastName)
		} else {
			nameInfo = " - Name: (not set)"
		}
		sb.WriteString(fmt.Sprintf("- **%s** (%s, %s) - Photo: %s%s\n",
			device.DeviceID, device.HardwareUUID[:8], device.Platform, device.PhotoHashShort, nameInfo))
	}
	sb.WriteString("\n")

	// Name Propagation section (shows which devices know about which other devices' names)
	sb.WriteString("## Name Propagation\n\n")
	sb.WriteString("Shows which devices have received name information about others via gossip/profile messages.\n\n")

	for _, device := range devices {
		sb.WriteString(fmt.Sprintf("### Device %s (%s)\n\n", device.DeviceID, device.FirstName))

		// Check mesh_view.json for this device
		meshViewPath := filepath.Join(dataDir, device.HardwareUUID, "cache", "mesh_view.json")
		if data, err := os.ReadFile(meshViewPath); err == nil {
			var meshView struct {
				Devices map[string]struct {
					FirstName      string `json:"first_name"`
					ProfileVersion int32  `json:"profile_version"`
				} `json:"devices"`
			}
			if json.Unmarshal(data, &meshView) == nil && len(meshView.Devices) > 0 {
				sb.WriteString("**Via Gossip (mesh_view.json):**\n")
				for deviceID, info := range meshView.Devices {
					sb.WriteString(fmt.Sprintf("- %s: first_name=%s, profile_v=%d\n", deviceID, info.FirstName, info.ProfileVersion))
				}
				sb.WriteString("\n")
			} else {
				sb.WriteString("❌ No mesh_view data\n\n")
			}
		} else {
			sb.WriteString("❌ mesh_view.json not found\n\n")
		}

		// Check profiles directory
		profilesDir := filepath.Join(dataDir, device.HardwareUUID, "cache", "profiles")
		if entries, err := os.ReadDir(profilesDir); err == nil && len(entries) > 0 {
			sb.WriteString("**Via Profile Messages (cache/profiles/):**\n")
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".json") {
					profilePath := filepath.Join(profilesDir, entry.Name())
					if data, err := os.ReadFile(profilePath); err == nil {
						var profile struct {
							FirstName      string `json:"FirstName"`
							LastName       string `json:"LastName"`
							ProfileVersion int32  `json:"ProfileVersion"`
						}
						if json.Unmarshal(data, &profile) == nil {
							deviceID := strings.TrimSuffix(entry.Name(), ".json")
							sb.WriteString(fmt.Sprintf("- %s: %s %s (profile_v=%d)\n", deviceID, profile.FirstName, profile.LastName, profile.ProfileVersion))
						}
					}
				}
			}
			sb.WriteString("\n")
		} else {
			sb.WriteString("❌ No cached profiles\n\n")
		}
	}

	// Debug Statistics section
	sb.WriteString("## Debug Statistics\n\n")
	sb.WriteString("Per-device BLE protocol activity and socket health.\n\n")

	for _, device := range devices {
		stats, ok := debugStats[device.DeviceID]
		if !ok {
			continue
		}

		sb.WriteString(fmt.Sprintf("### Device %s (%s %s)\n\n", device.DeviceID, device.FirstName, device.LastName))

		// Connection stats
		sb.WriteString("**Connection Activity:**\n")
		sb.WriteString(fmt.Sprintf("- Connection events: %d\n", stats.ConnectionEvents))
		if stats.ConnectionErrors > 0 {
			sb.WriteString(fmt.Sprintf("- Connection errors: %d ⚠️\n", stats.ConnectionErrors))
		}
		if stats.CBLifecycleEvents > 0 {
			sb.WriteString(fmt.Sprintf("- CoreBluetooth lifecycle events: %d (iOS)\n", stats.CBLifecycleEvents))
		}
		sb.WriteString("\n")

		// BLE Protocol stats
		sb.WriteString("**BLE Protocol Activity:**\n")
		sb.WriteString(fmt.Sprintf("- ATT packets: %d rx / %d tx\n", stats.ATTPacketsRx, stats.ATTPacketsTx))
		sb.WriteString(fmt.Sprintf("- L2CAP packets: %d rx / %d tx\n", stats.L2CAPPacketsRx, stats.L2CAPPacketsTx))
		sb.WriteString("\n")

		// GATT operations
		sb.WriteString("**GATT Operations:**\n")
		sb.WriteString(fmt.Sprintf("- Total operations: %d\n", stats.GATTOperations))
		if stats.GATTReads > 0 {
			sb.WriteString(fmt.Sprintf("  - Reads: %d\n", stats.GATTReads))
		}
		if stats.GATTWrites > 0 {
			sb.WriteString(fmt.Sprintf("  - Writes: %d\n", stats.GATTWrites))
		}
		if stats.GATTNotifications > 0 {
			sb.WriteString(fmt.Sprintf("  - Notifications: %d\n", stats.GATTNotifications))
		}
		sb.WriteString("\n")

		// Gossip stats
		if stats.GossipAuditEvents > 0 {
			sb.WriteString("**Gossip Protocol:**\n")
			sb.WriteString(fmt.Sprintf("- Audit events: %d\n", stats.GossipAuditEvents))
			sb.WriteString("\n")
		}

		// Socket health
		sb.WriteString("**Socket Health:**\n")
		sb.WriteString(fmt.Sprintf("- Status: %s", stats.SocketStatus))
		if stats.SocketStatus == "closed" {
			sb.WriteString(" ✅\n")
		} else {
			sb.WriteString("\n")
		}
		sb.WriteString(fmt.Sprintf("- Uptime: %d seconds\n", stats.SocketUptime))
		if stats.SocketErrors > 0 {
			sb.WriteString(fmt.Sprintf("- Socket errors: %d ⚠️\n", stats.SocketErrors))
		} else {
			sb.WriteString(fmt.Sprintf("- Socket errors: %d ✅\n", stats.SocketErrors))
		}
		sb.WriteString("\n")
	}

	// Photo Matrix section
	sb.WriteString("## Photo Matrix (Expected vs Actual)\n\n")
	sb.WriteString("|              |")
	for _, device := range devices {
		sb.WriteString(fmt.Sprintf(" %s |", device.DeviceID))
	}
	sb.WriteString("\n|")
	for range devices {
		sb.WriteString("----------|")
	}
	sb.WriteString("----------|")
	sb.WriteString("\n")

	for _, fromDevice := range devices {
		sb.WriteString(fmt.Sprintf("| **%s** |", fromDevice.DeviceID))
		for _, toDevice := range devices {
			if fromDevice.HardwareUUID == toDevice.HardwareUUID {
				sb.WriteString(" ✅ Own   |")
			} else if matrix[fromDevice.DeviceID][toDevice.DeviceID] {
				sb.WriteString(" ✅ Recv  |")
			} else {
				sb.WriteString(" ❌ MISSING |")
			}
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	// Issues section
	if len(issues) > 0 {
		sb.WriteString("## Issues\n\n")

		// Group by severity
		errors := []TestIssue{}
		warnings := []TestIssue{}
		for _, issue := range issues {
			if issue.Severity == "ERROR" {
				errors = append(errors, issue)
			} else {
				warnings = append(warnings, issue)
			}
		}

		if len(errors) > 0 {
			sb.WriteString("### Errors\n\n")
			for i, issue := range errors {
				sb.WriteString(fmt.Sprintf("#### %d. %s\n", i+1, issue.Description))
				sb.WriteString(fmt.Sprintf("- **Photo hash:** %s\n", issue.PhotoHash))
				sb.WriteString(fmt.Sprintf("- **From device:** %s\n", issue.FromDevice))
				sb.WriteString(fmt.Sprintf("- **To device:** %s\n", issue.ToDevice))
				sb.WriteString("\n**Timeline:**\n")
				if len(issue.Timeline) > 0 {
					for _, event := range issue.Timeline {
						sb.WriteString(fmt.Sprintf("- %s\n", event))
					}
				} else {
					sb.WriteString("- No timeline available\n")
				}
				sb.WriteString("\n")
			}
		}

		if len(warnings) > 0 {
			sb.WriteString("### Warnings\n\n")
			for i, issue := range warnings {
				sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, issue.Description))
			}
			sb.WriteString("\n")
		}
	} else {
		sb.WriteString("## ✅ All Tests Passed!\n\n")
		sb.WriteString("All devices successfully exchanged photos.\n\n")
	}

	// Statistics section
	sb.WriteString("## Statistics\n\n")
	totalExpected := len(devices) * (len(devices) - 1) // N * (N-1) expected transfers
	totalSuccessful := 0
	for _, fromDeviceMatrix := range matrix {
		for _, hasPhoto := range fromDeviceMatrix {
			if hasPhoto {
				totalSuccessful++
			}
		}
	}
	totalSuccessful -= len(devices) // Subtract "own photo" entries

	successRate := 0.0
	if totalExpected > 0 {
		successRate = float64(totalSuccessful) / float64(totalExpected) * 100.0
	}

	sb.WriteString(fmt.Sprintf("- **Total devices:** %d\n", len(devices)))
	sb.WriteString(fmt.Sprintf("- **Expected transfers:** %d\n", totalExpected))
	sb.WriteString(fmt.Sprintf("- **Successful transfers:** %d\n", totalSuccessful))
	sb.WriteString(fmt.Sprintf("- **Success rate:** %.1f%%\n", successRate))
	sb.WriteString(fmt.Sprintf("- **Failed transfers:** %d\n", totalExpected-totalSuccessful))

	return sb.String()
}
