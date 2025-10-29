package util

import (
	"os"
	"path/filepath"
)

// GetDataDir returns the data directory path
func GetDataDir() string {
	if envDir := os.Getenv("AURAPHONE_BLUE_DIR"); envDir != "" {
		return envDir
	}

	home, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	return filepath.Join(home, ".auraphone-blue-data")
}

// GetDeviceCacheDir returns the cache directory for a specific device
func GetDeviceCacheDir(deviceUUID string) string {
	return filepath.Join(GetDataDir(), deviceUUID)
}

// GetSocketDir returns the directory where Unix domain sockets are stored
func GetSocketDir() string {
	socketDir := filepath.Join(GetDataDir(), "sockets")
	// Ensure the directory exists
	if err := os.MkdirAll(socketDir, 0755); err != nil {
		panic(err)
	}
	return socketDir
}
