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

func (ip *IPhone) GetFirstName() string {
	ip.mu.RLock()
	defer ip.mu.RUnlock()
	return ip.firstName
}

func (ip *IPhone) GetDiscovered() map[string]phone.DiscoveredDevice {
	ip.mu.RLock()
	defer ip.mu.RUnlock()

	result := make(map[string]phone.DiscoveredDevice)
	for k, v := range ip.discovered {
		result[k] = v
	}
	return result
}

func (ip *IPhone) GetHandshaked() map[string]*HandshakeMessage {
	ip.mu.RLock()
	defer ip.mu.RUnlock()

	result := make(map[string]*HandshakeMessage)
	for k, v := range ip.handshaked {
		result[k] = v
	}
	return result
}

func (ip *IPhone) Lock() {
	ip.mu.Lock()
}

func (ip *IPhone) Unlock() {
	ip.mu.Unlock()
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

	// Check if any fields actually changed
	changed := false
	firstNameChanged := false
	for k, v := range profile {
		if ip.profile[k] != v {
			changed = true
			if k == "first_name" {
				firstNameChanged = true
			}
		}
		ip.profile[k] = v
	}

	// Update firstName field if first_name changed
	if firstNameChanged && profile["first_name"] != "" {
		ip.firstName = profile["first_name"]
		// Update mesh view so gossip messages include the new firstName
		ip.meshView.SetOurFirstName(ip.firstName)
		logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "✏️  Updated firstName to: %s", ip.firstName)
	}

	// Increment version if profile changed
	if changed {
		ip.profileVersion++
		logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "📋 Profile updated, version now: %d", ip.profileVersion)
	}

	ip.mu.Unlock()

	// Broadcast profile changes to all connected peers
	if changed {
		ip.broadcastProfileUpdate()
	}

	return nil
}
