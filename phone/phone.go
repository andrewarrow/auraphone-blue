package phone

// DiscoveredDevice represents a device discovered via BLE
type DiscoveredDevice struct {
	DeviceID     string
	HardwareUUID string // Hardware UUID (used as stable key before handshake)
	Name         string
	RSSI         float64
	Platform     string
	PhotoHash    string // SHA256 hash of profile photo
	PhotoData    []byte // JPEG photo data
}

// DeviceDiscoveryCallback is called when a new device is discovered
type DeviceDiscoveryCallback func(device DiscoveredDevice)

// Phone represents a BLE-enabled phone device
type Phone interface {
	// Start begins BLE advertising and scanning
	Start()

	// Stop stops BLE operations and cleans up resources
	Stop()

	// SetDiscoveryCallback sets the callback for when devices are discovered
	SetDiscoveryCallback(callback DeviceDiscoveryCallback)

	// GetDeviceUUID returns the device's UUID
	GetDeviceUUID() string

	// GetDeviceName returns the device's name
	GetDeviceName() string

	// GetPlatform returns the platform type ("iOS" or "Android")
	GetPlatform() string

	// SetProfilePhoto sets the profile photo and broadcasts the hash
	SetProfilePhoto(photoPath string) error

	// GetProfilePhotoHash returns the hash of the current profile photo
	GetProfilePhotoHash() string

	// GetLocalProfile returns the local profile (first_name, etc.) as a map
	GetLocalProfile() map[string]string

	// UpdateLocalProfile updates the local profile fields
	UpdateLocalProfile(profile map[string]string) error
}
