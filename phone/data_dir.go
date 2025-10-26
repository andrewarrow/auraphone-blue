package phone

import (
	"os"
	"path/filepath"
	"sync"
)

var (
	dataDir     string
	dataDirOnce sync.Once
)

// GetDataDir returns the centralized data directory for auraphone-blue
// It uses ~/.auraphone-blue-data for all persistent storage
// This ensures consistent storage location regardless of where tests/binaries are run
func GetDataDir() string {
	dataDirOnce.Do(func() {
		// Get user's home directory
		homeDir, err := os.UserHomeDir()
		if err != nil {
			// Fallback to ./data if we can't get home dir (should be rare)
			dataDir = "data"
			return
		}

		// Use ~/.auraphone-blue-data for all persistent storage
		dataDir = filepath.Join(homeDir, ".auraphone-blue-data")

		// Ensure the directory exists
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			// Fallback to ./data if we can't create the directory
			dataDir = "data"
		}
	})

	return dataDir
}

// GetDeviceDir returns the device-specific data directory
// Example: ~/.auraphone-blue-data/{deviceUUID}/
func GetDeviceDir(deviceUUID string) string {
	return filepath.Join(GetDataDir(), deviceUUID)
}

// GetDeviceCacheDir returns the device-specific cache directory
// Example: ~/.auraphone-blue-data/{deviceUUID}/cache/
func GetDeviceCacheDir(deviceUUID string) string {
	return filepath.Join(GetDeviceDir(deviceUUID), "cache")
}
