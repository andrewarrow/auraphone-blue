package wire

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DeviceCacheManager manages persistent storage for device data and photos
type DeviceCacheManager struct {
	baseDir string // Base directory for this device's cache (e.g., "data/{uuid}/cache")
}

// DeviceMetadata stores cached information about a remote device
type DeviceMetadata struct {
	DeviceID    string `json:"device_id"`
	FirstName   string `json:"first_name,omitempty"`
	LastName    string `json:"last_name,omitempty"`
	PhoneNumber string `json:"phone_number,omitempty"`
	PhotoHash   string `json:"photo_hash,omitempty"` // Hash of their photo we have cached
	LastUpdated int64  `json:"last_updated"`
}

// LocalUserMetadata stores metadata about our local photo
type LocalUserMetadata struct {
	PhotoHash      string            `json:"photo_hash"`
	LastUpdated    int64             `json:"last_updated"`
	SentVersions   map[string]string `json:"sent_versions,omitempty"` // deviceID -> photoHash we sent
}

// NewDeviceCacheManager creates a new cache manager for a device
func NewDeviceCacheManager(deviceUUID string) *DeviceCacheManager {
	baseDir := filepath.Join("data", deviceUUID, "cache")
	return &DeviceCacheManager{
		baseDir: baseDir,
	}
}

// InitializeCache creates the cache directory structure
func (m *DeviceCacheManager) InitializeCache() error {
	// Create base cache directory
	if err := os.MkdirAll(m.baseDir, 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Create photos subdirectory
	photosDir := filepath.Join(m.baseDir, "Photos")
	if err := os.MkdirAll(photosDir, 0755); err != nil {
		return fmt.Errorf("failed to create photos directory: %w", err)
	}

	return nil
}

// CalculatePhotoHash computes SHA-256 hash of photo data
func (m *DeviceCacheManager) CalculatePhotoHash(photoData []byte) string {
	hash := sha256.Sum256(photoData)
	return hex.EncodeToString(hash[:])
}

// GetLocalUserPhotoHash returns the hash of our current profile photo
func (m *DeviceCacheManager) GetLocalUserPhotoHash() (string, error) {
	metadataPath := filepath.Join(m.baseDir, "local_user_metadata.json")

	data, err := os.ReadFile(metadataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // No photo set yet
		}
		return "", err
	}

	var metadata LocalUserMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return "", err
	}

	return metadata.PhotoHash, nil
}

// SaveLocalUserPhoto saves our profile photo and returns its hash
func (m *DeviceCacheManager) SaveLocalUserPhoto(photoData []byte) (string, error) {
	if err := m.InitializeCache(); err != nil {
		return "", err
	}

	// Calculate hash
	photoHash := m.CalculatePhotoHash(photoData)

	// Save photo file named by hash
	photoPath := filepath.Join(m.baseDir, "Photos", fmt.Sprintf("%s.jpg", photoHash))
	if err := os.WriteFile(photoPath, photoData, 0644); err != nil {
		return "", fmt.Errorf("failed to save photo: %w", err)
	}

	// Also save as local_user.jpg for convenience
	localPhotoPath := filepath.Join(m.baseDir, "Photos", "local_user.jpg")
	if err := os.WriteFile(localPhotoPath, photoData, 0644); err != nil {
		return "", fmt.Errorf("failed to save local photo: %w", err)
	}

	// Load existing metadata to preserve sent versions
	existingMetadata, _ := m.loadLocalUserMetadata()
	sentVersions := make(map[string]string)
	if existingMetadata != nil {
		sentVersions = existingMetadata.SentVersions
	}

	// Save metadata
	metadata := LocalUserMetadata{
		PhotoHash:    photoHash,
		LastUpdated:  getCurrentTimestamp(),
		SentVersions: sentVersions,
	}

	metadataPath := filepath.Join(m.baseDir, "local_user_metadata.json")
	metadataJSON, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(metadataPath, metadataJSON, 0644); err != nil {
		return "", fmt.Errorf("failed to save metadata: %w", err)
	}

	return photoHash, nil
}

// LoadLocalUserPhoto loads our profile photo
func (m *DeviceCacheManager) LoadLocalUserPhoto() ([]byte, error) {
	photoPath := filepath.Join(m.baseDir, "Photos", "local_user.jpg")
	data, err := os.ReadFile(photoPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No photo set
		}
		return nil, err
	}
	return data, nil
}

// GetDevicePhotoHash returns the hash of a remote device's photo that we have cached
func (m *DeviceCacheManager) GetDevicePhotoHash(deviceID string) (string, error) {
	metadataPath := filepath.Join(m.baseDir, fmt.Sprintf("%s_metadata.json", deviceID))

	data, err := os.ReadFile(metadataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // Never met this device
		}
		return "", err
	}

	var metadata DeviceMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return "", err
	}

	return metadata.PhotoHash, nil
}

// SaveDevicePhoto saves a remote device's photo
func (m *DeviceCacheManager) SaveDevicePhoto(deviceID string, photoData []byte, photoHash string) error {
	if err := m.InitializeCache(); err != nil {
		return err
	}

	// If hash not provided, calculate it
	if photoHash == "" {
		photoHash = m.CalculatePhotoHash(photoData)
	}

	// Save photo file named by hash
	photoPath := filepath.Join(m.baseDir, "Photos", fmt.Sprintf("%s.jpg", photoHash))
	if err := os.WriteFile(photoPath, photoData, 0644); err != nil {
		return fmt.Errorf("failed to save device photo: %w", err)
	}

	// Load existing metadata or create new
	var metadata DeviceMetadata
	metadataPath := filepath.Join(m.baseDir, fmt.Sprintf("%s_metadata.json", deviceID))
	existingData, err := os.ReadFile(metadataPath)
	if err == nil {
		json.Unmarshal(existingData, &metadata)
	}

	// Update photo hash
	metadata.DeviceID = deviceID
	metadata.PhotoHash = photoHash
	metadata.LastUpdated = getCurrentTimestamp()

	// Save metadata
	metadataJSON, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(metadataPath, metadataJSON, 0644); err != nil {
		return fmt.Errorf("failed to save device metadata: %w", err)
	}

	return nil
}

// LoadDevicePhoto loads a remote device's cached photo
func (m *DeviceCacheManager) LoadDevicePhoto(deviceID string) ([]byte, error) {
	// First get the hash from metadata
	photoHash, err := m.GetDevicePhotoHash(deviceID)
	if err != nil {
		return nil, err
	}
	if photoHash == "" {
		return nil, nil // No photo cached
	}

	// Load photo by hash
	photoPath := filepath.Join(m.baseDir, "Photos", fmt.Sprintf("%s.jpg", photoHash))
	data, err := os.ReadFile(photoPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	return data, nil
}

// MarkPhotoSentToDevice records which version of our photo we sent to a device
func (m *DeviceCacheManager) MarkPhotoSentToDevice(deviceID string, photoHash string) error {
	metadata, err := m.loadLocalUserMetadata()
	if err != nil {
		return err
	}
	if metadata == nil {
		metadata = &LocalUserMetadata{
			SentVersions: make(map[string]string),
		}
	}
	if metadata.SentVersions == nil {
		metadata.SentVersions = make(map[string]string)
	}

	metadata.SentVersions[deviceID] = photoHash

	// Save updated metadata
	metadataPath := filepath.Join(m.baseDir, "local_user_metadata.json")
	metadataJSON, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(metadataPath, metadataJSON, 0644)
}

// GetPhotoVersionSentToDevice returns the hash of the photo version we sent to a device
func (m *DeviceCacheManager) GetPhotoVersionSentToDevice(deviceID string) (string, error) {
	metadata, err := m.loadLocalUserMetadata()
	if err != nil {
		return "", err
	}
	if metadata == nil || metadata.SentVersions == nil {
		return "", nil
	}

	return metadata.SentVersions[deviceID], nil
}

// ShouldSendPhotoToDevice determines if we need to send our photo to a remote device
// based on what version they have cached (their rx_photo_hash)
func (m *DeviceCacheManager) ShouldSendPhotoToDevice(remoteRxPhotoHash string) (bool, error) {
	// Get our current photo hash
	ourPhotoHash, err := m.GetLocalUserPhotoHash()
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
// based on what we have cached vs what they're offering (their tx_photo_hash)
func (m *DeviceCacheManager) ShouldReceivePhotoFromDevice(deviceID string, remoteTxPhotoHash string) (bool, error) {
	// Remote has no photo to send
	if remoteTxPhotoHash == "" {
		return false, nil
	}

	// Get what we have cached from them
	cachedHash, err := m.GetDevicePhotoHash(deviceID)
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

// SaveDeviceMetadata saves metadata about a remote device
func (m *DeviceCacheManager) SaveDeviceMetadata(metadata *DeviceMetadata) error {
	if err := m.InitializeCache(); err != nil {
		return err
	}

	metadata.LastUpdated = getCurrentTimestamp()

	metadataPath := filepath.Join(m.baseDir, fmt.Sprintf("%s_metadata.json", metadata.DeviceID))
	metadataJSON, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(metadataPath, metadataJSON, 0644)
}

// LoadDeviceMetadata loads metadata about a remote device
func (m *DeviceCacheManager) LoadDeviceMetadata(deviceID string) (*DeviceMetadata, error) {
	metadataPath := filepath.Join(m.baseDir, fmt.Sprintf("%s_metadata.json", deviceID))

	data, err := os.ReadFile(metadataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var metadata DeviceMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, err
	}

	return &metadata, nil
}

// Helper to load local user metadata
func (m *DeviceCacheManager) loadLocalUserMetadata() (*LocalUserMetadata, error) {
	metadataPath := filepath.Join(m.baseDir, "local_user_metadata.json")

	data, err := os.ReadFile(metadataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var metadata LocalUserMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, err
	}

	return &metadata, nil
}

// Helper to get current timestamp in milliseconds
func getCurrentTimestamp() int64 {
	return time.Now().UnixMilli()
}
