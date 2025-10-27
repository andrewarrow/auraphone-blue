package phone

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RequestType identifies the kind of pending request
type RequestType string

const (
	RequestTypePhoto   RequestType = "photo"
	RequestTypeProfile RequestType = "profile"
)

// PendingRequest represents a request waiting for a connection
type PendingRequest struct {
	DeviceID       string      `json:"device_id"`        // Target device (base36 ID)
	HardwareUUID   string      `json:"hardware_uuid"`    // Target hardware UUID (if known)
	Type           RequestType `json:"type"`
	PhotoHash      string      `json:"photo_hash"`       // For photo requests
	ProfileVersion int32       `json:"profile_version"`  // For profile requests
	CreatedAt      time.Time   `json:"created_at"`
	Attempts       int         `json:"attempts"`
}

// RequestQueue manages pending requests that can't be sent immediately
type RequestQueue struct {
	mu             sync.RWMutex
	hardwareUUID   string

	// Pending requests by device ID
	byDeviceID     map[string][]*PendingRequest

	// Pending requests by hardware UUID (for fast lookup on connection)
	byHardwareUUID map[string][]*PendingRequest

	// Persistent storage
	statePath      string
}

// requestQueueState is the JSON structure for persistence
type requestQueueState struct {
	Requests []*PendingRequest `json:"requests"`
}

// NewRequestQueue creates a new request queue
func NewRequestQueue(hardwareUUID string, dataDir string) *RequestQueue {
	rq := &RequestQueue{
		hardwareUUID:   hardwareUUID,
		byDeviceID:     make(map[string][]*PendingRequest),
		byHardwareUUID: make(map[string][]*PendingRequest),
		statePath:      filepath.Join(dataDir, hardwareUUID, "cache", "request_queue.json"),
	}
	return rq
}

// Enqueue adds a request to the queue
// Returns error if duplicate request already exists
func (rq *RequestQueue) Enqueue(req *PendingRequest) error {
	if req == nil {
		return fmt.Errorf("cannot enqueue nil request")
	}

	rq.mu.Lock()
	defer rq.mu.Unlock()

	// Check for duplicates by deviceID and type
	for _, existing := range rq.byDeviceID[req.DeviceID] {
		if existing.Type == req.Type {
			// For photo requests, check if same photo hash
			if req.Type == RequestTypePhoto && existing.PhotoHash == req.PhotoHash {
				return nil // Already queued, no error
			}
			// For profile requests, just check type
			if req.Type == RequestTypeProfile {
				return nil // Already queued, no error
			}
		}
	}

	// Add to byDeviceID index
	rq.byDeviceID[req.DeviceID] = append(rq.byDeviceID[req.DeviceID], req)

	// Add to byHardwareUUID index if hardware UUID is known
	if req.HardwareUUID != "" {
		rq.byHardwareUUID[req.HardwareUUID] = append(rq.byHardwareUUID[req.HardwareUUID], req)
	}

	return nil
}

// DequeueForConnection retrieves all pending requests for a newly connected device
// Returns the requests and removes them from the queue
func (rq *RequestQueue) DequeueForConnection(hardwareUUID string) []*PendingRequest {
	rq.mu.Lock()
	defer rq.mu.Unlock()

	requests := rq.byHardwareUUID[hardwareUUID]
	if len(requests) == 0 {
		return nil
	}

	// Remove from byHardwareUUID index
	delete(rq.byHardwareUUID, hardwareUUID)

	// Remove from byDeviceID index
	for _, req := range requests {
		rq.removeFromDeviceIDIndex(req.DeviceID, req.Type)
	}

	return requests
}

// DequeueForDeviceID retrieves all pending requests for a device by its device ID
// This is used when we learn the hardware UUID for a device we only knew by device ID
func (rq *RequestQueue) DequeueForDeviceID(deviceID string) []*PendingRequest {
	rq.mu.Lock()
	defer rq.mu.Unlock()

	requests := rq.byDeviceID[deviceID]
	if len(requests) == 0 {
		return nil
	}

	// Remove from byDeviceID index
	delete(rq.byDeviceID, deviceID)

	// Remove from byHardwareUUID index
	for _, req := range requests {
		if req.HardwareUUID != "" {
			rq.removeFromHardwareUUIDIndex(req.HardwareUUID, req.DeviceID, req.Type)
		}
	}

	return requests
}

// Remove deletes a request (after successful send or permanent failure)
func (rq *RequestQueue) Remove(deviceID string, reqType RequestType) {
	rq.mu.Lock()
	defer rq.mu.Unlock()

	rq.removeFromDeviceIDIndex(deviceID, reqType)
}

// removeFromDeviceIDIndex removes a specific request type from device ID index
// Must be called with lock held
func (rq *RequestQueue) removeFromDeviceIDIndex(deviceID string, reqType RequestType) {
	requests := rq.byDeviceID[deviceID]
	filtered := []*PendingRequest{}

	for _, req := range requests {
		if req.Type != reqType {
			filtered = append(filtered, req)
		} else {
			// Also remove from hardware UUID index
			if req.HardwareUUID != "" {
				rq.removeFromHardwareUUIDIndex(req.HardwareUUID, deviceID, reqType)
			}
		}
	}

	if len(filtered) == 0 {
		delete(rq.byDeviceID, deviceID)
	} else {
		rq.byDeviceID[deviceID] = filtered
	}
}

// removeFromHardwareUUIDIndex removes a specific request from hardware UUID index
// Must be called with lock held
func (rq *RequestQueue) removeFromHardwareUUIDIndex(hardwareUUID, deviceID string, reqType RequestType) {
	requests := rq.byHardwareUUID[hardwareUUID]
	filtered := []*PendingRequest{}

	for _, req := range requests {
		if req.DeviceID != deviceID || req.Type != reqType {
			filtered = append(filtered, req)
		}
	}

	if len(filtered) == 0 {
		delete(rq.byHardwareUUID, hardwareUUID)
	} else {
		rq.byHardwareUUID[hardwareUUID] = filtered
	}
}

// GetPendingCount returns number of queued requests
func (rq *RequestQueue) GetPendingCount() int {
	rq.mu.RLock()
	defer rq.mu.RUnlock()

	count := 0
	for _, requests := range rq.byDeviceID {
		count += len(requests)
	}
	return count
}

// GetPendingForDevice returns pending requests for a specific device
func (rq *RequestQueue) GetPendingForDevice(deviceID string) []*PendingRequest {
	rq.mu.RLock()
	defer rq.mu.RUnlock()

	requests := rq.byDeviceID[deviceID]
	// Return a copy to prevent external modification
	result := make([]*PendingRequest, len(requests))
	copy(result, requests)
	return result
}

// SaveToDisk persists queue state
func (rq *RequestQueue) SaveToDisk() error {
	rq.mu.RLock()
	defer rq.mu.RUnlock()

	// Collect all requests from byDeviceID (canonical source)
	allRequests := []*PendingRequest{}
	for _, requests := range rq.byDeviceID {
		allRequests = append(allRequests, requests...)
	}

	state := requestQueueState{
		Requests: allRequests,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal request queue: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(rq.statePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create request queue directory: %w", err)
	}

	// Atomic write: write to temp file, then rename
	tempPath := rq.statePath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write request queue temp file: %w", err)
	}

	if err := os.Rename(tempPath, rq.statePath); err != nil {
		return fmt.Errorf("failed to rename request queue file: %w", err)
	}

	return nil
}

// LoadFromDisk restores queue state
func (rq *RequestQueue) LoadFromDisk() error {
	rq.mu.Lock()
	defer rq.mu.Unlock()

	data, err := os.ReadFile(rq.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No saved state, not an error
		}
		return fmt.Errorf("failed to read request queue: %w", err)
	}

	var state requestQueueState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("failed to unmarshal request queue: %w", err)
	}

	// Clear existing state
	rq.byDeviceID = make(map[string][]*PendingRequest)
	rq.byHardwareUUID = make(map[string][]*PendingRequest)

	// Rebuild indexes
	for _, req := range state.Requests {
		rq.byDeviceID[req.DeviceID] = append(rq.byDeviceID[req.DeviceID], req)
		if req.HardwareUUID != "" {
			rq.byHardwareUUID[req.HardwareUUID] = append(rq.byHardwareUUID[req.HardwareUUID], req)
		}
	}

	return nil
}

// UpdateHardwareUUID updates the hardware UUID for queued requests when we learn it
// This is called after handshake when we map device ID to hardware UUID
func (rq *RequestQueue) UpdateHardwareUUID(deviceID, hardwareUUID string) {
	rq.mu.Lock()
	defer rq.mu.Unlock()

	requests := rq.byDeviceID[deviceID]
	for _, req := range requests {
		if req.HardwareUUID == "" {
			req.HardwareUUID = hardwareUUID
			// Add to hardware UUID index
			rq.byHardwareUUID[hardwareUUID] = append(rq.byHardwareUUID[hardwareUUID], req)
		}
	}
}
