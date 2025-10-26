package phone

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/user/auraphone-blue/logger"
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
	Tagline     string `json:"tagline,omitempty"`
	Insta       string `json:"insta,omitempty"`
	LinkedIn    string `json:"linkedin,omitempty"`
	YouTube     string `json:"youtube,omitempty"`
	TikTok      string `json:"tiktok,omitempty"`
	Gmail       string `json:"gmail,omitempty"`
	IMessage    string `json:"imessage,omitempty"`
	WhatsApp    string `json:"whatsapp,omitempty"`
	Signal      string `json:"signal,omitempty"`
	Telegram    string `json:"telegram,omitempty"`
	PhotoHash   string `json:"photo_hash,omitempty"` // Hash of their photo we have cached
	LastUpdated int64  `json:"last_updated"`
}

// LocalUserMetadata stores metadata about our local photo
type LocalUserMetadata struct {
	PhotoHash    string            `json:"photo_hash"`
	LastUpdated  int64             `json:"last_updated"`
	SentVersions map[string]string `json:"sent_versions,omitempty"` // deviceID -> photoHash we sent
}

// NewDeviceCacheManager creates a new cache manager for a device
func NewDeviceCacheManager(deviceUUID string) *DeviceCacheManager {
	baseDir := GetDeviceCacheDir(deviceUUID)
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
	photosDir := filepath.Join(m.baseDir, "photos")
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
	photoPath := filepath.Join(m.baseDir, "photos", fmt.Sprintf("%s.jpg", photoHash))
	if err := os.WriteFile(photoPath, photoData, 0644); err != nil {
		return "", fmt.Errorf("failed to save photo: %w", err)
	}

	// Also save as local_user.jpg for convenience
	localPhotoPath := filepath.Join(m.baseDir, "photos", "local_user.jpg")
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
	photoPath := filepath.Join(m.baseDir, "photos", "local_user.jpg")
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
	prefix := "cache"
	logger.Debug(prefix, "📸 [CACHE-SAVE] SaveDevicePhoto called: deviceID=%s, photoSize=%d, photoHash=%s, baseDir=%s",
		deviceID[:8], len(photoData), photoHash[:8], m.baseDir)

	if err := m.InitializeCache(); err != nil {
		logger.Error(prefix, "❌ InitializeCache failed: %v", err)
		return err
	}

	// If hash not provided, calculate it
	if photoHash == "" {
		photoHash = m.CalculatePhotoHash(photoData)
		logger.Debug(prefix, "   └─ Calculated hash: %s", photoHash[:8])
	}

	// Save photo file named by hash
	photoPath := filepath.Join(m.baseDir, "photos", fmt.Sprintf("%s.jpg", photoHash))
	logger.Debug(prefix, "📸 [CACHE-WRITE] Writing photo file: %s (size=%d)", photoPath, len(photoData))

	if err := os.WriteFile(photoPath, photoData, 0644); err != nil {
		logger.Error(prefix, "❌ [CACHE-ERROR] Failed to write photo file: %v", err)
		return fmt.Errorf("failed to save device photo: %w", err)
	}

	// Verify the file was written
	if stat, err := os.Stat(photoPath); err == nil {
		logger.Debug(prefix, "✅ [CACHE-VERIFY] Photo file written and verified: %s (%d bytes on disk)", photoPath, stat.Size())
		logger.Info(prefix, "✅ Photo file written successfully: %s (%d bytes)", photoPath, stat.Size())
	} else {
		logger.Error(prefix, "❌ [CACHE-ERROR] Photo file verification failed: %v", err)
	}

	// Load existing metadata or create new
	var metadata DeviceMetadata
	metadataPath := filepath.Join(m.baseDir, fmt.Sprintf("%s_metadata.json", deviceID))
	logger.Debug(prefix, "   └─ Metadata path: %s", metadataPath)

	existingData, err := os.ReadFile(metadataPath)
	if err == nil {
		json.Unmarshal(existingData, &metadata)
		logger.Debug(prefix, "   └─ Loaded existing metadata")
	} else {
		logger.Debug(prefix, "   └─ No existing metadata, creating new")
	}

	// Update photo hash
	metadata.DeviceID = deviceID
	metadata.PhotoHash = photoHash
	metadata.LastUpdated = getCurrentTimestamp()

	logger.Debug(prefix, "📸 [CACHE-METADATA] Updated metadata: deviceID=%s, photoHash=%s", deviceID[:8], photoHash[:8])

	// Save metadata
	metadataJSON, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		logger.Error(prefix, "❌ [CACHE-ERROR] Failed to marshal metadata: %v", err)
		return err
	}

	logger.Debug(prefix, "📸 [CACHE-WRITE] Writing metadata file: %s", metadataPath)
	if err := os.WriteFile(metadataPath, metadataJSON, 0644); err != nil {
		logger.Error(prefix, "❌ [CACHE-ERROR] Failed to write metadata: %v", err)
		return fmt.Errorf("failed to save device metadata: %w", err)
	}

	logger.Debug(prefix, "✅ [CACHE-SUCCESS] SaveDevicePhoto complete: deviceID=%s, photoHash=%s, photoPath=%s, metadataPath=%s",
		deviceID[:8], photoHash[:8], photoPath, metadataPath)
	logger.Info(prefix, "✅ Metadata saved: %s", metadataPath)

	return nil
}

// LoadDevicePhoto loads a remote device's cached photo
func (m *DeviceCacheManager) LoadDevicePhoto(deviceID string) ([]byte, error) {
	prefix := "cache"
	logger.Debug(prefix, "📸 [CACHE-LOAD] LoadDevicePhoto called for deviceID=%s", deviceID[:8])

	// First get the hash from metadata
	photoHash, err := m.GetDevicePhotoHash(deviceID)
	if err != nil {
		logger.Error(prefix, "❌ [CACHE-ERROR] Failed to get photo hash for %s: %v", deviceID[:8], err)
		return nil, err
	}
	if photoHash == "" {
		logger.Debug(prefix, "📸 [CACHE-LOAD] No photo hash in metadata for %s", deviceID[:8])
		return nil, nil // No photo cached
	}

	logger.Debug(prefix, "📸 [CACHE-LOAD] Photo hash from metadata: %s", photoHash[:8])

	// Load photo by hash
	photoPath := filepath.Join(m.baseDir, "photos", fmt.Sprintf("%s.jpg", photoHash))
	logger.Debug(prefix, "📸 [CACHE-READ] Loading photo from: %s", photoPath)

	// Check if file exists
	if stat, err := os.Stat(photoPath); err != nil {
		if os.IsNotExist(err) {
			logger.Warn(prefix, "⚠️  Photo file not found: %s", photoPath)
			return nil, nil
		}
		logger.Error(prefix, "❌ Error stating photo file: %v", err)
	} else {
		logger.Debug(prefix, "   └─ Photo file exists: %s (%d bytes)", photoPath, stat.Size())
	}

	data, err := os.ReadFile(photoPath)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Warn(prefix, "⚠️  [CACHE-ERROR] Photo file disappeared during read: %s", photoPath)
			return nil, nil
		}
		logger.Error(prefix, "❌ [CACHE-ERROR] Failed to read photo file: %v", err)
		return nil, err
	}

	logger.Debug(prefix, "✅ [CACHE-SUCCESS] Successfully loaded photo for deviceID=%s: %d bytes from %s",
		deviceID[:8], len(data), photoPath)
	logger.Info(prefix, "✅ Successfully loaded photo for %s: %d bytes", deviceID[:8], len(data))
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
