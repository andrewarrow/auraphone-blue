package android

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
)

// ============================================================================
// Phone Interface Implementation
// ============================================================================

func (a *Android) GetDeviceID() string {
	return a.deviceID
}

func (a *Android) GetFirstName() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.firstName
}

func (a *Android) GetDiscovered() map[string]phone.DiscoveredDevice {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := make(map[string]phone.DiscoveredDevice)
	for k, v := range a.discovered {
		result[k] = v
	}
	return result
}

func (a *Android) GetHandshaked() map[string]*HandshakeMessage {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := make(map[string]*HandshakeMessage)
	for k, v := range a.handshaked {
		result[k] = v
	}
	return result
}

func (a *Android) Lock() {
	a.mu.Lock()
}

func (a *Android) Unlock() {
	a.mu.Unlock()
}

func (a *Android) SetProfilePhoto(photoPath string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Load photo and calculate hash
	photoData, photoHash, err := a.photoCache.LoadPhoto(photoPath, "")
	if err != nil {
		return fmt.Errorf("failed to load photo: %w", err)
	}

	// Save to cache
	_, err = a.photoCache.SavePhoto(photoData, a.hardwareUUID, a.deviceID)
	if err != nil {
		return fmt.Errorf("failed to cache photo: %w", err)
	}

	a.profilePhoto = photoPath
	a.photoHash = photoHash
	a.photoData = photoData

	logger.Info(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "📸 Set profile photo: %s (hash: %s)", photoPath, shortHash(photoHash))

	// TODO: Broadcast updated photo hash to connected devices
	// For now, new connections will get it in handshake

	return nil
}

func (a *Android) GetLocalProfileMap() map[string]string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := make(map[string]string)
	for k, v := range a.profile {
		result[k] = v
	}
	return result
}

func (a *Android) UpdateLocalProfile(profile map[string]string) error {
	a.mu.Lock()

	// Check if any fields actually changed
	changed := false
	firstNameChanged := false
	for k, v := range profile {
		if a.profile[k] != v {
			changed = true
			if k == "first_name" {
				firstNameChanged = true
			}
		}
		a.profile[k] = v
	}

	// Update firstName field if first_name changed
	if firstNameChanged && profile["first_name"] != "" {
		a.firstName = profile["first_name"]
		// Update mesh view so gossip messages include the new firstName
		a.meshView.SetOurFirstName(a.firstName)
		logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "✏️  Updated firstName to: %s", a.firstName)
	}

	// Increment version if profile changed
	if changed {
		a.profileVersion++
		logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "📋 Profile updated, version now: %d", a.profileVersion)
	}

	a.mu.Unlock()

	// Broadcast profile changes to all connected peers
	if changed {
		a.broadcastProfileUpdate()
	}

	return nil
}
