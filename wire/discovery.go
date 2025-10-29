package wire

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/user/auraphone-blue/util"
)

// StartDiscovery starts scanning for devices (stub for old API)
// TODO Step 4: Implement proper discovery with callbacks
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
				// Use our new ListAvailableDevices
				devices := w.ListAvailableDevices()
				for _, deviceUUID := range devices {
					callback(deviceUUID)
				}
			}
		}
	}()

	return stopChan
}

// ListAvailableDevices scans socket directory for .sock files and returns hardware UUIDs
func (w *Wire) ListAvailableDevices() []string {
	devices := make([]string, 0)

	// Scan socket directory for auraphone-*.sock files
	socketDir := util.GetSocketDir()
	pattern := filepath.Join(socketDir, "auraphone-*.sock")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return devices
	}

	for _, path := range matches {
		// Extract UUID from filename
		filename := filepath.Base(path)
		// Format: auraphone-{UUID}.sock
		if len(filename) < len("auraphone-") || filepath.Ext(filename) != ".sock" {
			continue
		}

		uuid := filename[len("auraphone-") : len(filename)-len(".sock")]

		// Don't include ourselves
		if uuid != w.hardwareUUID {
			devices = append(devices, uuid)
		}
	}

	return devices
}

// ReadAdvertisingData reads advertising data for a device from filesystem
// Real BLE: this simulates discovering advertising packets "over the air"
func (w *Wire) ReadAdvertisingData(deviceUUID string) (*AdvertisingData, error) {
	deviceDir := util.GetDeviceCacheDir(deviceUUID)
	advPath := filepath.Join(deviceDir, "advertising.json")

	// Read from file (simulates discovering advertising packet)
	data, err := os.ReadFile(advPath)
	if err != nil {
		// Return default advertising data if not found
		return &AdvertisingData{
			DeviceName:    fmt.Sprintf("Device-%s", shortHash(deviceUUID)),
			ServiceUUIDs:  []string{},
			IsConnectable: true,
		}, nil
	}

	var advData AdvertisingData
	if err := json.Unmarshal(data, &advData); err != nil {
		return nil, fmt.Errorf("failed to parse advertising.json: %w", err)
	}

	return &advData, nil
}

// WriteAdvertisingData writes advertising data to device's filesystem
// Real BLE: this sets what we broadcast in advertising packets
func (w *Wire) WriteAdvertisingData(data *AdvertisingData) error {
	deviceDir := util.GetDeviceCacheDir(w.hardwareUUID)
	if err := os.MkdirAll(deviceDir, 0755); err != nil {
		return fmt.Errorf("failed to create device directory: %w", err)
	}

	advPath := filepath.Join(deviceDir, "advertising.json")
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal advertising data: %w", err)
	}

	if err := os.WriteFile(advPath, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write advertising.json: %w", err)
	}

	return nil
}

// GetRSSI returns simulated RSSI for a device (stub for old API)
// TODO Step 4: Implement realistic RSSI simulation
func (w *Wire) GetRSSI(deviceUUID string) float64 {
	return -45.0 // Good signal strength
}

// DeviceExists checks if a device exists (stub for old API)
// TODO Step 4: Implement proper device discovery state tracking
func (w *Wire) DeviceExists(deviceUUID string) bool {
	// Check if device socket exists
	socketDir := util.GetSocketDir()
	socketPath := filepath.Join(socketDir, fmt.Sprintf("auraphone-%s.sock", deviceUUID))
	_, err := os.Stat(socketPath)
	return err == nil
}

// GetSimulator returns a stub simulator (stub for old API)
// TODO Step 4: Remove simulator dependency from swift layer
func (w *Wire) GetSimulator() *SimulatorStub {
	return &SimulatorStub{
		ServiceDiscoveryDelay: 100 * time.Millisecond,
	}
}
