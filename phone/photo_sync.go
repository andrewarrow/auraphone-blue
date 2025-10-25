package phone

// PhotoSyncManager handles photo synchronization logic between devices
// This is application-level business logic, not part of the BLE transport layer
type PhotoSyncManager struct {
	cache *DeviceCacheManager
}

// NewPhotoSyncManager creates a new photo sync manager
func NewPhotoSyncManager(cache *DeviceCacheManager) *PhotoSyncManager {
	return &PhotoSyncManager{
		cache: cache,
	}
}

// ShouldSendPhotoToDevice determines if we need to send our photo to a remote device
// based on what version they have cached (their rx_photo_hash from handshake)
func (m *PhotoSyncManager) ShouldSendPhotoToDevice(remoteRxPhotoHash string) (bool, error) {
	// Get our current photo hash
	ourPhotoHash, err := m.cache.GetLocalUserPhotoHash()
	if err != nil {
		return false, err
	}

	// No photo to send
	if ourPhotoHash == "" {
		return false, nil
	}

	// Remote has no photo from us, or has a different version
	if remoteRxPhotoHash == "" || remoteRxPhotoHash != ourPhotoHash {
		return true, nil
	}

	// Remote already has our current photo
	return false, nil
}

// ShouldReceivePhotoFromDevice determines if we need to receive a photo from a remote device
// based on what we have cached vs what they're offering (their tx_photo_hash from handshake)
func (m *PhotoSyncManager) ShouldReceivePhotoFromDevice(deviceID string, remoteTxPhotoHash string) (bool, error) {
	// Remote has no photo to send
	if remoteTxPhotoHash == "" {
		return false, nil
	}

	// Get what we have cached from them
	cachedHash, err := m.cache.GetDevicePhotoHash(deviceID)
	if err != nil {
		return false, err
	}

	// We don't have their photo, or they have a new version
	if cachedHash == "" || cachedHash != remoteTxPhotoHash {
		return true, nil
	}

	// We already have their current photo
	return false, nil
}

// MarkPhotoSentToDevice records which version of our photo we sent to a device
func (m *PhotoSyncManager) MarkPhotoSentToDevice(deviceID string, photoHash string) error {
	return m.cache.MarkPhotoSentToDevice(deviceID, photoHash)
}

// GetPhotoVersionSentToDevice returns the hash of the photo version we sent to a device
func (m *PhotoSyncManager) GetPhotoVersionSentToDevice(deviceID string) (string, error) {
	return m.cache.GetPhotoVersionSentToDevice(deviceID)
}
