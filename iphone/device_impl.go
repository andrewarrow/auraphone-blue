package iphone

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
)

// ============================================================================
// Phone Interface Implementation
// ============================================================================

func (ip *IPhone) SetDiscoveryCallback(callback phone.DeviceDiscoveryCallback) {
	ip.mu.Lock()
	ip.callback = callback
	ip.mu.Unlock()
}

func (ip *IPhone) GetDeviceUUID() string {
	return ip.hardwareUUID
}

func (ip *IPhone) GetDeviceID() string {
	return ip.deviceID
}

func (ip *IPhone) GetDeviceName() string {
	return ip.deviceName
}

func (ip *IPhone) GetPlatform() string {
	return "iOS"
}

func (ip *IPhone) SetProfilePhoto(photoPath string) error {
	ip.mu.Lock()
	defer ip.mu.Unlock()

	// Load photo and calculate hash
	photoData, photoHash, err := ip.photoCache.LoadPhoto(photoPath, "")
	if err != nil {
		return fmt.Errorf("failed to load photo: %w", err)
	}

	// Save to cache
	_, err = ip.photoCache.SavePhoto(photoData, ip.hardwareUUID, ip.deviceID)
	if err != nil {
		return fmt.Errorf("failed to cache photo: %w", err)
	}

	ip.profilePhoto = photoPath
	ip.photoHash = photoHash
	ip.photoData = photoData

	logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "📸 Set profile photo: %s (hash: %s)", photoPath, shortHash(photoHash))

	// TODO: Broadcast updated photo hash to connected devices
	// For now, new connections will get it in handshake

	return nil
}

func (ip *IPhone) GetLocalProfileMap() map[string]string {
	ip.mu.RLock()
	defer ip.mu.RUnlock()

	result := make(map[string]string)
	for k, v := range ip.profile {
		result[k] = v
	}
	return result
}

func (ip *IPhone) UpdateLocalProfile(profile map[string]string) error {
	ip.mu.Lock()
	defer ip.mu.Unlock()

	for k, v := range profile {
		ip.profile[k] = v
	}
	// TODO: Step 8 - broadcast profile changes
	return nil
}
