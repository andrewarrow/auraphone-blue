package phone

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// PhotoCache manages cached photos for a device
type PhotoCache struct {
	deviceUUID string
	cacheDir   string
	mu         sync.RWMutex
}

// PhotoMetadata stores metadata about a cached photo
type PhotoMetadata struct {
	PhotoHash  string `json:"photo_hash"`  // SHA-256 hash (hex)
	SourceUUID string `json:"source_uuid"` // Hardware UUID of source device
	DeviceID   string `json:"device_id"`   // Device ID of source (if known)
	Size       int64  `json:"size"`        // File size in bytes
}

// NewPhotoCache creates a new photo cache manager
func NewPhotoCache(deviceUUID string) *PhotoCache {
	cacheDir := filepath.Join(GetDataDir(), deviceUUID, "photos")
	return &PhotoCache{
		deviceUUID: deviceUUID,
		cacheDir:   cacheDir,
	}
}

// LoadPhoto reads photo data from cache or source path
// If photoPath is absolute, reads from that path
// If photoHash is provided, reads from cache
func (pc *PhotoCache) LoadPhoto(photoPath string, photoHash string) ([]byte, string, error) {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	// If photoHash is provided, try to load from cache
	if photoHash != "" {
		cachedPath := filepath.Join(pc.cacheDir, photoHash+".jpg")
		data, err := os.ReadFile(cachedPath)
		if err == nil {
			return data, photoHash, nil
		}
	}

	// Load from source path
	if photoPath == "" {
		return nil, "", fmt.Errorf("no photo path or hash provided")
	}

	data, err := os.ReadFile(photoPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read photo: %w", err)
	}

	// Calculate hash
	hash := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hash[:])

	return data, hashStr, nil
}

// SavePhoto saves photo data to cache
func (pc *PhotoCache) SavePhoto(data []byte, sourceUUID string, deviceID string) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("empty photo data")
	}

	pc.mu.Lock()
	defer pc.mu.Unlock()

	// Calculate hash
	hash := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hash[:])

	// Ensure cache directory exists
	if err := os.MkdirAll(pc.cacheDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Save photo file
	photoPath := filepath.Join(pc.cacheDir, hashStr+".jpg")
	if err := os.WriteFile(photoPath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write photo: %w", err)
	}

	// Save metadata
	metadata := PhotoMetadata{
		PhotoHash:  hashStr,
		SourceUUID: sourceUUID,
		DeviceID:   deviceID,
		Size:       int64(len(data)),
	}

	metadataPath := filepath.Join(pc.cacheDir, hashStr+".json")
	metadataJSON, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := os.WriteFile(metadataPath, metadataJSON, 0644); err != nil {
		return "", fmt.Errorf("failed to write metadata: %w", err)
	}

	return hashStr, nil
}

// GetPhoto retrieves photo data from cache by hash
func (pc *PhotoCache) GetPhoto(photoHash string) ([]byte, error) {
	if photoHash == "" {
		return nil, fmt.Errorf("empty photo hash")
	}

	pc.mu.RLock()
	defer pc.mu.RUnlock()

	photoPath := filepath.Join(pc.cacheDir, photoHash+".jpg")
	data, err := os.ReadFile(photoPath)
	if err != nil {
		return nil, fmt.Errorf("photo not in cache: %w", err)
	}

	return data, nil
}

// HasPhoto checks if photo is cached
func (pc *PhotoCache) HasPhoto(photoHash string) bool {
	if photoHash == "" {
		return false
	}

	pc.mu.RLock()
	defer pc.mu.RUnlock()

	photoPath := filepath.Join(pc.cacheDir, photoHash+".jpg")
	_, err := os.Stat(photoPath)
	return err == nil
}

// GetPhotoMetadata retrieves metadata for a cached photo
func (pc *PhotoCache) GetPhotoMetadata(photoHash string) (*PhotoMetadata, error) {
	if photoHash == "" {
		return nil, fmt.Errorf("empty photo hash")
	}

	pc.mu.RLock()
	defer pc.mu.RUnlock()

	metadataPath := filepath.Join(pc.cacheDir, photoHash+".json")
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil, fmt.Errorf("metadata not found: %w", err)
	}

	var metadata PhotoMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	return &metadata, nil
}

// HashPhoto calculates SHA-256 hash of photo file
func HashPhoto(photoPath string) (string, error) {
	file, err := os.Open(photoPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}

	hash := hasher.Sum(nil)
	return hex.EncodeToString(hash), nil
}

// PhotoChunker manages chunking large photos for BLE transfer
type PhotoChunker struct {
	ChunkSize int // Bytes per chunk (default: 512 for BLE 5.0)
}

// NewPhotoChunker creates a new photo chunker with default chunk size
func NewPhotoChunker() *PhotoChunker {
	return &PhotoChunker{
		ChunkSize: 512, // BLE 5.0 max MTU - safe default
	}
}

// ChunkPhoto splits photo data into chunks for transmission
func (pc *PhotoChunker) ChunkPhoto(data []byte) [][]byte {
	if len(data) == 0 {
		return [][]byte{}
	}

	numChunks := (len(data) + pc.ChunkSize - 1) / pc.ChunkSize
	chunks := make([][]byte, 0, numChunks)

	for i := 0; i < len(data); i += pc.ChunkSize {
		end := i + pc.ChunkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := make([]byte, end-i)
		copy(chunk, data[i:end])
		chunks = append(chunks, chunk)
	}

	return chunks
}

// PhotoTransferState tracks the state of an in-progress photo transfer
type PhotoTransferState struct {
	PhotoHash    string   // Hash of photo being transferred
	TotalChunks  int      // Total number of chunks
	ReceivedData []byte   // Accumulated data
	ChunksRecv   int      // Number of chunks received
	SourceUUID   string   // Hardware UUID of sender
	DeviceID     string   // Device ID of sender (if known)
	mu           sync.Mutex
}

// NewPhotoTransferState creates a new transfer state
func NewPhotoTransferState(photoHash string, totalChunks int, sourceUUID string, deviceID string) *PhotoTransferState {
	return &PhotoTransferState{
		PhotoHash:    photoHash,
		TotalChunks:  totalChunks,
		ReceivedData: make([]byte, 0),
		ChunksRecv:   0,
		SourceUUID:   sourceUUID,
		DeviceID:     deviceID,
	}
}

// AddChunk adds a chunk to the transfer state
func (pts *PhotoTransferState) AddChunk(data []byte) {
	pts.mu.Lock()
	defer pts.mu.Unlock()

	pts.ReceivedData = append(pts.ReceivedData, data...)
	pts.ChunksRecv++
}

// IsComplete checks if all chunks have been received
func (pts *PhotoTransferState) IsComplete() bool {
	pts.mu.Lock()
	defer pts.mu.Unlock()

	return pts.ChunksRecv >= pts.TotalChunks
}

// GetProgress returns transfer progress (0.0 to 1.0)
func (pts *PhotoTransferState) GetProgress() float64 {
	pts.mu.Lock()
	defer pts.mu.Unlock()

	if pts.TotalChunks == 0 {
		return 0.0
	}
	return float64(pts.ChunksRecv) / float64(pts.TotalChunks)
}

// GetData returns the accumulated photo data
func (pts *PhotoTransferState) GetData() []byte {
	pts.mu.Lock()
	defer pts.mu.Unlock()

	return pts.ReceivedData
}
