package phone

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/user/auraphone-blue/util"
)

// DiscoveredDevice represents a device discovered via BLE scanning
type DiscoveredDevice struct {
	HardwareUUID string
	DeviceID     string // Base36 device ID (empty until handshake)
	Name         string
	RSSI         float64
	PhotoHash    string // SHA-256 hash of profile photo
	PhotoData    []byte // Actual photo data (nil until received)
}

// DeviceDiscoveryCallback is called when a new device is discovered
type DeviceDiscoveryCallback func(device DiscoveredDevice)

// Phone interface represents a simulated BLE-enabled phone
type Phone interface {
	Start()
	Stop()
	SetDiscoveryCallback(callback DeviceDiscoveryCallback)
	GetDeviceUUID() string
	GetDeviceName() string
	GetPlatform() string
	SetProfilePhoto(photoPath string) error
	GetLocalProfileMap() map[string]string
	UpdateLocalProfile(profile map[string]string) error
}

// GetDataDir returns the data directory path
// Deprecated: Use util.GetDataDir() instead
func GetDataDir() string {
	return util.GetDataDir()
}

// GetDeviceCacheDir returns the cache directory for a specific device
// Deprecated: Use util.GetDeviceCacheDir() instead
func GetDeviceCacheDir(deviceUUID string) string {
	return util.GetDeviceCacheDir(deviceUUID)
}

// DeviceCacheManager manages device metadata cache
type DeviceCacheManager struct {
	deviceUUID string
}

// NewDeviceCacheManager creates a new cache manager
func NewDeviceCacheManager(deviceUUID string) *DeviceCacheManager {
	return &DeviceCacheManager{deviceUUID: deviceUUID}
}

// DeviceMetadata represents cached metadata about a device
type DeviceMetadata struct {
	FirstName      string
	LastName       string
	Tagline        string
	Insta          string
	LinkedIn       string
	YouTube        string
	TikTok         string
	Gmail          string
	IMessage       string
	WhatsApp       string
	Signal         string
	Telegram       string
	ProfileVersion int32 // Profile version number, increments on any profile change
}

// LoadDeviceMetadata loads metadata for a device
func (m *DeviceCacheManager) LoadDeviceMetadata(deviceID string) (*DeviceMetadata, error) {
	metadataPath := filepath.Join(GetDeviceCacheDir(m.deviceUUID), "cache", "profiles", deviceID+".json")

	data, err := os.ReadFile(metadataPath)
	if err != nil {
		// Return empty metadata if file doesn't exist
		return &DeviceMetadata{}, nil
	}

	var metadata DeviceMetadata
	err = json.Unmarshal(data, &metadata)
	if err != nil {
		return nil, err
	}

	return &metadata, nil
}

// SaveDeviceMetadata saves metadata for a device
func (m *DeviceCacheManager) SaveDeviceMetadata(deviceID string, metadata *DeviceMetadata) error {
	profilesDir := filepath.Join(GetDeviceCacheDir(m.deviceUUID), "cache", "profiles")
	if err := os.MkdirAll(profilesDir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}

	metadataPath := filepath.Join(profilesDir, deviceID+".json")
	return os.WriteFile(metadataPath, data, 0644)
}
