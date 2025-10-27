package phone

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

// HardwareUUIDManager manages allocation of hardware UUIDs from testdata/hardware_uuids.txt
type HardwareUUIDManager struct {
	uuids         []string
	allocatedIdx  int
	allocatedUUIDs map[string]bool
	mu            sync.Mutex
}

var (
	globalManager     *HardwareUUIDManager
	globalManagerOnce sync.Once
)

// GetHardwareUUIDManager returns the singleton hardware UUID manager
func GetHardwareUUIDManager() *HardwareUUIDManager {
	globalManagerOnce.Do(func() {
		globalManager = &HardwareUUIDManager{
			allocatedUUIDs: make(map[string]bool),
		}
		// Load UUIDs from testdata/hardware_uuids.txt
		data, err := os.ReadFile("testdata/hardware_uuids.txt")
		if err != nil {
			panic(fmt.Sprintf("Failed to load hardware_uuids.txt: %v", err))
		}
		// Parse UUIDs (one per line)
		lines := string(data)
		for i := 0; i < len(lines); {
			// Find end of line
			end := i
			for end < len(lines) && lines[end] != '\n' {
				end++
			}
			line := lines[i:end]
			// Trim whitespace
			line = trimSpace(line)
			if len(line) > 0 {
				globalManager.uuids = append(globalManager.uuids, line)
			}
			i = end + 1
		}
		if len(globalManager.uuids) == 0 {
			panic("No hardware UUIDs found in testdata/hardware_uuids.txt")
		}
	})
	return globalManager
}

// AllocateNextUUID returns the next available UUID
func (m *HardwareUUIDManager) AllocateNextUUID() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.allocatedIdx >= len(m.uuids) {
		return "", fmt.Errorf("no more hardware UUIDs available (max: %d)", len(m.uuids))
	}

	uuid := m.uuids[m.allocatedIdx]
	m.allocatedIdx++
	m.allocatedUUIDs[uuid] = true
	return uuid, nil
}

// ReleaseUUID releases a UUID back to the pool
func (m *HardwareUUIDManager) ReleaseUUID(uuid string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.allocatedUUIDs, uuid)
}

// GetAllocatedCount returns the number of currently allocated UUIDs
func (m *HardwareUUIDManager) GetAllocatedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.allocatedUUIDs)
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

// trimSpace removes leading and trailing whitespace
func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r' || s[start] == '\n') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r' || s[end-1] == '\n') {
		end--
	}
	return s[start:end]
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
