package phone

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRequestQueue_Enqueue(t *testing.T) {
	tmpDir := t.TempDir()
	rq := NewRequestQueue("test-uuid", tmpDir)

	req := &PendingRequest{
		DeviceID:     "device1",
		HardwareUUID: "hw-uuid-1",
		Type:         RequestTypePhoto,
		PhotoHash:    "hash123",
		CreatedAt:    time.Now(),
		Attempts:     0,
	}

	err := rq.Enqueue(req)
	if err != nil {
		t.Fatalf("Failed to enqueue request: %v", err)
	}

	// Verify request is in the queue
	count := rq.GetPendingCount()
	if count != 1 {
		t.Errorf("Expected 1 pending request, got %d", count)
	}

	// Verify request can be retrieved by device ID
	pending := rq.GetPendingForDevice("device1")
	if len(pending) != 1 {
		t.Errorf("Expected 1 request for device1, got %d", len(pending))
	}
}

func TestRequestQueue_EnqueueDuplicate(t *testing.T) {
	tmpDir := t.TempDir()
	rq := NewRequestQueue("test-uuid", tmpDir)

	req1 := &PendingRequest{
		DeviceID:     "device1",
		HardwareUUID: "hw-uuid-1",
		Type:         RequestTypePhoto,
		PhotoHash:    "hash123",
		CreatedAt:    time.Now(),
		Attempts:     0,
	}

	req2 := &PendingRequest{
		DeviceID:     "device1",
		HardwareUUID: "hw-uuid-1",
		Type:         RequestTypePhoto,
		PhotoHash:    "hash123", // Same hash
		CreatedAt:    time.Now(),
		Attempts:     0,
	}

	err := rq.Enqueue(req1)
	if err != nil {
		t.Fatalf("Failed to enqueue first request: %v", err)
	}

	err = rq.Enqueue(req2)
	if err != nil {
		t.Fatalf("Failed to enqueue duplicate request: %v", err)
	}

	// Should only have 1 request (duplicate rejected)
	count := rq.GetPendingCount()
	if count != 1 {
		t.Errorf("Expected 1 pending request (duplicate rejected), got %d", count)
	}
}

func TestRequestQueue_DequeueForConnection(t *testing.T) {
	tmpDir := t.TempDir()
	rq := NewRequestQueue("test-uuid", tmpDir)

	// Enqueue requests for multiple devices
	req1 := &PendingRequest{
		DeviceID:     "device1",
		HardwareUUID: "hw-uuid-1",
		Type:         RequestTypePhoto,
		PhotoHash:    "hash123",
		CreatedAt:    time.Now(),
		Attempts:     0,
	}

	req2 := &PendingRequest{
		DeviceID:     "device2",
		HardwareUUID: "hw-uuid-2",
		Type:         RequestTypePhoto,
		PhotoHash:    "hash456",
		CreatedAt:    time.Now(),
		Attempts:     0,
	}

	rq.Enqueue(req1)
	rq.Enqueue(req2)

	// Dequeue for connection to hw-uuid-1
	requests := rq.DequeueForConnection("hw-uuid-1")
	if len(requests) != 1 {
		t.Errorf("Expected 1 request for hw-uuid-1, got %d", len(requests))
	}

	if requests[0].DeviceID != "device1" {
		t.Errorf("Expected device1, got %s", requests[0].DeviceID)
	}

	// Should only have 1 request left in queue
	count := rq.GetPendingCount()
	if count != 1 {
		t.Errorf("Expected 1 pending request remaining, got %d", count)
	}

	// Verify the dequeued request was removed from device ID index
	pending := rq.GetPendingForDevice("device1")
	if len(pending) != 0 {
		t.Errorf("Expected 0 requests for device1 after dequeue, got %d", len(pending))
	}
}

func TestRequestQueue_Remove(t *testing.T) {
	tmpDir := t.TempDir()
	rq := NewRequestQueue("test-uuid", tmpDir)

	req := &PendingRequest{
		DeviceID:     "device1",
		HardwareUUID: "hw-uuid-1",
		Type:         RequestTypePhoto,
		PhotoHash:    "hash123",
		CreatedAt:    time.Now(),
		Attempts:     0,
	}

	rq.Enqueue(req)

	// Remove the request
	rq.Remove("device1", RequestTypePhoto)

	// Should have 0 requests
	count := rq.GetPendingCount()
	if count != 0 {
		t.Errorf("Expected 0 pending requests after removal, got %d", count)
	}
}

func TestRequestQueue_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	rq1 := NewRequestQueue("test-uuid", tmpDir)

	// Enqueue some requests
	req1 := &PendingRequest{
		DeviceID:     "device1",
		HardwareUUID: "hw-uuid-1",
		Type:         RequestTypePhoto,
		PhotoHash:    "hash123",
		CreatedAt:    time.Now(),
		Attempts:     0,
	}

	req2 := &PendingRequest{
		DeviceID:     "device2",
		HardwareUUID: "hw-uuid-2",
		Type:         RequestTypeProfile,
		ProfileVersion: 1,
		CreatedAt:    time.Now(),
		Attempts:     1,
	}

	rq1.Enqueue(req1)
	rq1.Enqueue(req2)

	// Save to disk
	err := rq1.SaveToDisk()
	if err != nil {
		t.Fatalf("Failed to save request queue: %v", err)
	}

	// Create a new queue and load from disk
	rq2 := NewRequestQueue("test-uuid", tmpDir)
	err = rq2.LoadFromDisk()
	if err != nil {
		t.Fatalf("Failed to load request queue: %v", err)
	}

	// Verify state was restored
	count := rq2.GetPendingCount()
	if count != 2 {
		t.Errorf("Expected 2 pending requests after load, got %d", count)
	}

	// Verify individual requests
	pending1 := rq2.GetPendingForDevice("device1")
	if len(pending1) != 1 {
		t.Errorf("Expected 1 request for device1, got %d", len(pending1))
	} else {
		if pending1[0].PhotoHash != "hash123" {
			t.Errorf("Expected photo hash 'hash123', got '%s'", pending1[0].PhotoHash)
		}
	}

	pending2 := rq2.GetPendingForDevice("device2")
	if len(pending2) != 1 {
		t.Errorf("Expected 1 request for device2, got %d", len(pending2))
	} else {
		if pending2[0].Type != RequestTypeProfile {
			t.Errorf("Expected profile request, got %s", pending2[0].Type)
		}
		if pending2[0].Attempts != 1 {
			t.Errorf("Expected 1 attempt, got %d", pending2[0].Attempts)
		}
	}

	// Verify hardware UUID index was rebuilt
	requests := rq2.DequeueForConnection("hw-uuid-1")
	if len(requests) != 1 {
		t.Errorf("Expected 1 request for hw-uuid-1, got %d", len(requests))
	}
}

func TestRequestQueue_LoadNonexistent(t *testing.T) {
	tmpDir := t.TempDir()
	rq := NewRequestQueue("test-uuid", tmpDir)

	// Load from nonexistent file should not error
	err := rq.LoadFromDisk()
	if err != nil {
		t.Errorf("Expected no error loading nonexistent file, got: %v", err)
	}

	// Queue should be empty
	count := rq.GetPendingCount()
	if count != 0 {
		t.Errorf("Expected 0 pending requests, got %d", count)
	}
}

func TestRequestQueue_UpdateHardwareUUID(t *testing.T) {
	tmpDir := t.TempDir()
	rq := NewRequestQueue("test-uuid", tmpDir)

	// Enqueue request without hardware UUID
	req := &PendingRequest{
		DeviceID:     "device1",
		HardwareUUID: "", // Unknown initially
		Type:         RequestTypePhoto,
		PhotoHash:    "hash123",
		CreatedAt:    time.Now(),
		Attempts:     0,
	}

	rq.Enqueue(req)

	// Verify it's not in hardware UUID index
	requests := rq.DequeueForConnection("hw-uuid-1")
	if len(requests) != 0 {
		t.Errorf("Expected 0 requests for hw-uuid-1 before update, got %d", len(requests))
	}

	// Update hardware UUID
	rq.UpdateHardwareUUID("device1", "hw-uuid-1")

	// Now should be able to dequeue by hardware UUID
	requests = rq.DequeueForConnection("hw-uuid-1")
	if len(requests) != 1 {
		t.Errorf("Expected 1 request for hw-uuid-1 after update, got %d", len(requests))
	}
}

func TestRequestQueue_MultipleRequestsPerDevice(t *testing.T) {
	tmpDir := t.TempDir()
	rq := NewRequestQueue("test-uuid", tmpDir)

	// Enqueue multiple different requests for same device
	req1 := &PendingRequest{
		DeviceID:     "device1",
		HardwareUUID: "hw-uuid-1",
		Type:         RequestTypePhoto,
		PhotoHash:    "hash123",
		CreatedAt:    time.Now(),
		Attempts:     0,
	}

	req2 := &PendingRequest{
		DeviceID:     "device1",
		HardwareUUID: "hw-uuid-1",
		Type:         RequestTypeProfile,
		ProfileVersion: 1,
		CreatedAt:    time.Now(),
		Attempts:     0,
	}

	rq.Enqueue(req1)
	rq.Enqueue(req2)

	// Should have 2 requests
	count := rq.GetPendingCount()
	if count != 2 {
		t.Errorf("Expected 2 pending requests, got %d", count)
	}

	// Both should be in device ID index
	pending := rq.GetPendingForDevice("device1")
	if len(pending) != 2 {
		t.Errorf("Expected 2 requests for device1, got %d", len(pending))
	}

	// Dequeue should return both
	requests := rq.DequeueForConnection("hw-uuid-1")
	if len(requests) != 2 {
		t.Errorf("Expected 2 requests for hw-uuid-1, got %d", len(requests))
	}

	// Queue should be empty
	count = rq.GetPendingCount()
	if count != 0 {
		t.Errorf("Expected 0 pending requests after dequeue, got %d", count)
	}
}

func TestRequestQueue_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	rq := NewRequestQueue("test-uuid", tmpDir)

	req := &PendingRequest{
		DeviceID:     "device1",
		HardwareUUID: "hw-uuid-1",
		Type:         RequestTypePhoto,
		PhotoHash:    "hash123",
		CreatedAt:    time.Now(),
		Attempts:     0,
	}

	rq.Enqueue(req)

	// Save to disk
	err := rq.SaveToDisk()
	if err != nil {
		t.Fatalf("Failed to save request queue: %v", err)
	}

	// Verify no .tmp file remains
	tmpPath := filepath.Join(tmpDir, "test-uuid", "cache", "request_queue.json.tmp")
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("Temp file should not exist after save")
	}

	// Verify actual file exists
	statePath := filepath.Join(tmpDir, "test-uuid", "cache", "request_queue.json")
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		t.Errorf("State file should exist after save")
	}
}

func TestRequestQueue_DequeueForDeviceID(t *testing.T) {
	tmpDir := t.TempDir()
	rq := NewRequestQueue("test-uuid", tmpDir)

	// Enqueue request with hardware UUID
	req := &PendingRequest{
		DeviceID:     "device1",
		HardwareUUID: "hw-uuid-1",
		Type:         RequestTypePhoto,
		PhotoHash:    "hash123",
		CreatedAt:    time.Now(),
		Attempts:     0,
	}

	rq.Enqueue(req)

	// Dequeue by device ID
	requests := rq.DequeueForDeviceID("device1")
	if len(requests) != 1 {
		t.Errorf("Expected 1 request for device1, got %d", len(requests))
	}

	// Should be removed from both indexes
	count := rq.GetPendingCount()
	if count != 0 {
		t.Errorf("Expected 0 pending requests after dequeue, got %d", count)
	}

	// Should not be in hardware UUID index
	hwRequests := rq.DequeueForConnection("hw-uuid-1")
	if len(hwRequests) != 0 {
		t.Errorf("Expected 0 requests for hw-uuid-1 after dequeue by device ID, got %d", len(hwRequests))
	}
}

func TestRequestQueue_NilRequest(t *testing.T) {
	tmpDir := t.TempDir()
	rq := NewRequestQueue("test-uuid", tmpDir)

	err := rq.Enqueue(nil)
	if err == nil {
		t.Error("Expected error when enqueueing nil request")
	}
}

func TestRequestQueue_ConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	rq := NewRequestQueue("test-uuid", tmpDir)

	// Run concurrent enqueues and dequeues
	done := make(chan bool)

	// Enqueue goroutine
	go func() {
		for i := 0; i < 100; i++ {
			req := &PendingRequest{
				DeviceID:     "device1",
				HardwareUUID: "hw-uuid-1",
				Type:         RequestTypePhoto,
				PhotoHash:    "hash",
				CreatedAt:    time.Now(),
				Attempts:     0,
			}
			rq.Enqueue(req)
		}
		done <- true
	}()

	// GetPendingCount goroutine
	go func() {
		for i := 0; i < 100; i++ {
			rq.GetPendingCount()
		}
		done <- true
	}()

	// GetPendingForDevice goroutine
	go func() {
		for i := 0; i < 100; i++ {
			rq.GetPendingForDevice("device1")
		}
		done <- true
	}()

	// Wait for all goroutines
	for i := 0; i < 3; i++ {
		<-done
	}

	// Test should not panic (race detector will catch issues)
	t.Log("Concurrent access test completed")
}
