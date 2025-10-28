package phone

import (
	"os"
	"path/filepath"
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
func GetDataDir() string {
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
	FirstName string
	LastName  string
	Tagline   string
	Insta     string
	LinkedIn  string
	YouTube   string
	TikTok    string
	Gmail     string
	IMessage  string
	WhatsApp  string
	Signal    string
	Telegram  string
}

// LoadDeviceMetadata loads metadata for a device (stub for now)
func (m *DeviceCacheManager) LoadDeviceMetadata(deviceID string) (*DeviceMetadata, error) {
	// For now, return empty metadata (will be implemented when we add handshake/profile exchange)
	return &DeviceMetadata{}, nil
}
