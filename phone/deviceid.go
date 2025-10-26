package phone

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"
)

// DeviceIDCache stores the persistent device ID
type DeviceIDCache struct {
	DeviceID string `json:"device_id"` // 8-character base36 ID
}

// GenerateBase36ID generates an 8-character base36 ID (0-9, A-Z)
// Matches the iOS implementation from AuraPhone/bluetooth/BluetoothPhoto.swift
func GenerateBase36ID() string {
	const base36Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"

	// Use current time as seed for uniqueness
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	result := ""
	for i := 0; i < 8; i++ {
		randomIndex := rng.Intn(36)
		result += string(base36Chars[randomIndex])
	}

	return result
}

// LoadOrGenerateDeviceID loads the cached device ID or generates a new one
// The device ID is stored in cache/device_id.json and persists across app restarts
func LoadOrGenerateDeviceID(hardwareUUID string) (string, error) {
	cachePath := filepath.Join(GetDeviceCacheDir(hardwareUUID), "device_id.json")

	// Try to load existing device ID
	data, err := os.ReadFile(cachePath)
	if err == nil {
		var cache DeviceIDCache
		if err := json.Unmarshal(data, &cache); err == nil && cache.DeviceID != "" {
			return cache.DeviceID, nil
		}
	}

	// Generate new device ID
	deviceID := GenerateBase36ID()

	// Save to cache
	cache := DeviceIDCache{
		DeviceID: deviceID,
	}

	cacheData, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal device ID cache: %w", err)
	}

	// Ensure cache directory exists
	cacheDir := filepath.Dir(cachePath)
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}

	if err := os.WriteFile(cachePath, cacheData, 0644); err != nil {
		return "", fmt.Errorf("failed to save device ID cache: %w", err)
	}

	return deviceID, nil
}
