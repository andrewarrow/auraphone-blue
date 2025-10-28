package gui

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/user/auraphone-blue/phone"
)

// CleanupOldDevices removes all device directories and socket files from previous runs
func CleanupOldDevices() error {
	// Clean up data directory
	dataPath := phone.GetDataDir()

	// Check if data directory exists
	if _, err := os.Stat(dataPath); !os.IsNotExist(err) {
		// Remove all contents
		entries, err := os.ReadDir(dataPath)
		if err != nil {
			return fmt.Errorf("failed to read data directory: %w", err)
		}

		for _, entry := range entries {
			path := filepath.Join(dataPath, entry.Name())
			if err := os.RemoveAll(path); err != nil {
				fmt.Printf("Warning: failed to remove %s: %v\n", path, err)
			}
		}

		fmt.Printf("Cleaned up %d old device directories\n", len(entries))
	}

	// Clean up old socket files from /tmp
	socketMatches, err := filepath.Glob("/tmp/auraphone-*.sock")
	if err == nil && len(socketMatches) > 0 {
		for _, sockPath := range socketMatches {
			if err := os.Remove(sockPath); err != nil {
				fmt.Printf("Warning: failed to remove socket %s: %v\n", sockPath, err)
			}
		}
		fmt.Printf("Cleaned up %d old socket files from /tmp\n", len(socketMatches))
	}

	return nil
}
