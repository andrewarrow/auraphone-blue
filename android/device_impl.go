package android

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
)

// ============================================================================
// Phone Interface Implementation
// ============================================================================

func (a *Android) GetDeviceID() string {
	return a.deviceID
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
	defer a.mu.Unlock()

	// Check if any fields actually changed
	changed := false
	for k, v := range profile {
		if a.profile[k] != v {
			changed = true
		}
		a.profile[k] = v
	}

	// Increment version if profile changed
	if changed {
		a.profileVersion++
		logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "📋 Profile updated, version now: %d", a.profileVersion)
	}

	// TODO: Step 8 - broadcast profile changes to connected peers
	return nil
}
