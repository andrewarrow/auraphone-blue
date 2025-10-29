package kotlin

import (
	"os"
	"testing"
)

// setupTestEnv creates a temporary directory for test data and sets AURAPHONE_BLUE_DIR
// Returns the temp dir path and a cleanup function
// The cleanup is automatically registered with t.Cleanup() to run AFTER all defers
func setupTestEnv(t *testing.T) (string, func()) {
	// Use /tmp directly with short random name to avoid socket path length limits
	tmpDir, err := os.MkdirTemp("/tmp", "apb-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Save original env var
	originalDir := os.Getenv("AURAPHONE_BLUE_DIR")

	// Set to temp dir
	os.Setenv("AURAPHONE_BLUE_DIR", tmpDir)

	// Create cleanup function
	cleanup := func() {
		// Restore original env var
		if originalDir == "" {
			os.Unsetenv("AURAPHONE_BLUE_DIR")
		} else {
			os.Setenv("AURAPHONE_BLUE_DIR", originalDir)
		}
		// Remove temp directory (best effort - may fail if sockets still open)
		os.RemoveAll(tmpDir)
	}

	// Register cleanup to run AFTER all defers (ensures wires are stopped first)
	t.Cleanup(cleanup)

	// Also return cleanup for manual use if needed
	return tmpDir, cleanup
}

// testGattCallback is a test implementation of BluetoothGattCallback
// Shared across all test files
type testGattCallback struct {
	onConnectionStateChange func(gatt *BluetoothGatt, status int, newState int)
	onServicesDiscovered    func(gatt *BluetoothGatt, status int)
	onCharacteristicRead    func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int)
	onCharacteristicWrite   func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int)
	onCharacteristicChanged func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic)
	onDescriptorRead        func(gatt *BluetoothGatt, descriptor *BluetoothGattDescriptor, status int)
	onDescriptorWrite       func(gatt *BluetoothGatt, descriptor *BluetoothGattDescriptor, status int)
}

func (c *testGattCallback) OnConnectionStateChange(gatt *BluetoothGatt, status int, newState int) {
	if c.onConnectionStateChange != nil {
		c.onConnectionStateChange(gatt, status, newState)
	}
}

func (c *testGattCallback) OnServicesDiscovered(gatt *BluetoothGatt, status int) {
	if c.onServicesDiscovered != nil {
		c.onServicesDiscovered(gatt, status)
	}
}

func (c *testGattCallback) OnCharacteristicRead(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int) {
	if c.onCharacteristicRead != nil {
		c.onCharacteristicRead(gatt, char, status)
	}
}

func (c *testGattCallback) OnCharacteristicWrite(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int) {
	if c.onCharacteristicWrite != nil {
		c.onCharacteristicWrite(gatt, char, status)
	}
}

func (c *testGattCallback) OnCharacteristicChanged(gatt *BluetoothGatt, char *BluetoothGattCharacteristic) {
	if c.onCharacteristicChanged != nil {
		c.onCharacteristicChanged(gatt, char)
	}
}

func (c *testGattCallback) OnDescriptorRead(gatt *BluetoothGatt, descriptor *BluetoothGattDescriptor, status int) {
	if c.onDescriptorRead != nil {
		c.onDescriptorRead(gatt, descriptor, status)
	}
}

func (c *testGattCallback) OnDescriptorWrite(gatt *BluetoothGatt, descriptor *BluetoothGattDescriptor, status int) {
	if c.onDescriptorWrite != nil {
		c.onDescriptorWrite(gatt, descriptor, status)
	}
}
