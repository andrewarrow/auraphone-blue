package swift

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain sets up a temporary data directory for all tests
// This prevents test pollution in ~/.auraphone-blue-data/
func TestMain(m *testing.M) {
	// Create a temporary directory for test data in /tmp to avoid long socket paths
	// Unix domain sockets have a max path length (~104 chars on macOS)
	tempDir, err := os.MkdirTemp("/tmp", "auratest-*")
	if err != nil {
		panic(err)
	}

	// Set environment variable to use temp directory instead of ~/.auraphone-blue-data/
	os.Setenv("AURAPHONE_BLUE_DIR", tempDir)

	// Run all tests
	code := m.Run()

	// Cleanup: remove temporary directory
	os.RemoveAll(tempDir)

	os.Exit(code)
}

// GetTestDataDir returns the test data directory path (for debugging)
func GetTestDataDir() string {
	return os.Getenv("AURAPHONE_BLUE_DIR")
}

// cleanupTestDevice is a helper function that can be called with t.Cleanup()
// to ensure device directories are removed after individual tests
func cleanupTestDevice(t *testing.T, deviceUUID string) {
	t.Cleanup(func() {
		dataDir := os.Getenv("AURAPHONE_BLUE_DIR")
		if dataDir == "" {
			return
		}
		deviceDir := filepath.Join(dataDir, deviceUUID)
		os.RemoveAll(deviceDir)
	})
}
