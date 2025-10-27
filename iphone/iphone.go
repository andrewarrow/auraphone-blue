package iphone

import (
	"fmt"
	"sync"
	"time"

	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/wire"
)

// IPhone implements the Phone interface for iOS devices
type IPhone struct {
	hardwareUUID string
	deviceName   string
	wire         *wire.Wire
	discovered   map[string]phone.DiscoveredDevice // UUID -> device
	mu           sync.RWMutex
	callback     phone.DeviceDiscoveryCallback
	stopScanning chan struct{}
	profilePhoto string                 // Path to current profile photo
	profile      map[string]string      // Local profile data
}

// NewIPhone creates a new iPhone instance
func NewIPhone(hardwareUUID string) *IPhone {
	// Generate device name from UUID
	deviceName := fmt.Sprintf("iPhone (%s)", hardwareUUID[:8])

	return &IPhone{
		hardwareUUID: hardwareUUID,
		deviceName:   deviceName,
		discovered:   make(map[string]phone.DiscoveredDevice),
		profile:      make(map[string]string),
	}
}

// Start initializes the iPhone and begins advertising/scanning
func (ip *IPhone) Start() {
	// Create wire
	ip.wire = wire.NewWire(ip.hardwareUUID)

	// Start listening on socket
	err := ip.wire.Start()
	if err != nil {
		panic(err) // In minimal version, just panic on errors
	}

	// Start scanning for other devices
	ip.startScanning()
}

// Stop shuts down the iPhone
func (ip *IPhone) Stop() {
	// Stop scanning
	if ip.stopScanning != nil {
		close(ip.stopScanning)
		ip.stopScanning = nil
	}

	// Stop wire
	if ip.wire != nil {
		ip.wire.Stop()
	}
}

// SetDiscoveryCallback sets the callback for device discovery
func (ip *IPhone) SetDiscoveryCallback(callback phone.DeviceDiscoveryCallback) {
	ip.mu.Lock()
	ip.callback = callback
	ip.mu.Unlock()
}

// GetDeviceUUID returns this device's hardware UUID
func (ip *IPhone) GetDeviceUUID() string {
	return ip.hardwareUUID
}

// GetDeviceName returns this device's name
func (ip *IPhone) GetDeviceName() string {
	return ip.deviceName
}

// startScanning periodically scans for available devices
func (ip *IPhone) startScanning() {
	ip.stopScanning = make(chan struct{})

	go func() {
		// Initial scan immediately
		ip.scanOnce()

		// Then scan every 1 second (realistic BLE advertising interval)
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ip.stopScanning:
				return
			case <-ticker.C:
				ip.scanOnce()
			}
		}
	}()
}

// scanOnce performs a single scan for available devices
func (ip *IPhone) scanOnce() {
	// List available devices via socket scanning
	devices := ip.wire.ListAvailableDevices()

	if len(devices) > 0 {
		fmt.Printf("[%s] Scan found %d devices\n", ip.hardwareUUID[:8], len(devices))
	}

	for _, deviceUUID := range devices {
		// Check if we've already discovered this device
		ip.mu.RLock()
		_, exists := ip.discovered[deviceUUID]
		ip.mu.RUnlock()

		if exists {
			continue // Already discovered
		}

		// Create discovered device
		// For now, we don't know the device name until we connect/handshake
		// So just use a placeholder
		device := phone.DiscoveredDevice{
			HardwareUUID: deviceUUID,
			Name:         "Unknown Device",
			RSSI:         -45.0, // Simulated good signal strength
		}

		// Store in discovered map
		ip.mu.Lock()
		ip.discovered[deviceUUID] = device
		callback := ip.callback
		ip.mu.Unlock()

		// Call callback
		if callback != nil {
			callback(device)
		}
	}
}

// GetPlatform returns the platform type
func (ip *IPhone) GetPlatform() string {
	return "iOS"
}

// SetProfilePhoto sets the profile photo for this device
func (ip *IPhone) SetProfilePhoto(photoPath string) error {
	ip.mu.Lock()
	defer ip.mu.Unlock()
	ip.profilePhoto = photoPath
	// TODO: In future, broadcast photo hash to other devices
	return nil
}

// GetLocalProfileMap returns the local profile data
func (ip *IPhone) GetLocalProfileMap() map[string]string {
	ip.mu.RLock()
	defer ip.mu.RUnlock()

	// Return a copy to avoid race conditions
	result := make(map[string]string)
	for k, v := range ip.profile {
		result[k] = v
	}
	return result
}

// UpdateLocalProfile updates the local profile data
func (ip *IPhone) UpdateLocalProfile(profile map[string]string) error {
	ip.mu.Lock()
	defer ip.mu.Unlock()

	// Update profile fields
	for k, v := range profile {
		ip.profile[k] = v
	}
	// TODO: In future, broadcast profile changes to connected devices
	return nil
}
