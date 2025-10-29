package att

import (
	"fmt"
	"sync"
	"time"
)

// RequestTracker manages pending ATT requests and matches them with responses.
// In real BLE, only one ATT request can be outstanding at a time per connection.
// This enforces that constraint and provides timeout handling.
type RequestTracker struct {
	mu              sync.Mutex
	pending         *PendingRequest
	defaultTimeout  time.Duration
	timeoutCallback func(opcode byte, handle uint16)
}

// PendingRequest represents a single outstanding ATT request
type PendingRequest struct {
	Opcode    byte      // Request opcode (e.g., 0x0A for Read Request)
	Handle    uint16    // Attribute handle being accessed
	ResponseC chan Response // Channel for response delivery
	TimerC    <-chan time.Time // Timeout timer
	SentAt    time.Time
}

// Response represents an ATT response or error
type Response struct {
	Packet interface{} // The response packet (may be nil on timeout)
	Error  error       // Error if timeout or ATT error occurred
}

// NewRequestTracker creates a new request tracker
func NewRequestTracker(timeout time.Duration) *RequestTracker {
	if timeout == 0 {
		timeout = 30 * time.Second // Default ATT transaction timeout
	}
	return &RequestTracker{
		defaultTimeout: timeout,
	}
}

// SetTimeoutCallback sets a callback to be invoked when a request times out
func (rt *RequestTracker) SetTimeoutCallback(cb func(opcode byte, handle uint16)) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.timeoutCallback = cb
}

// GetPendingRequest returns the current pending request (for accessing context)
func (rt *RequestTracker) GetPendingRequest() *PendingRequest {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.pending
}

// StartRequest registers a new ATT request and returns a response channel.
// Returns error if another request is already pending (ATT allows only one at a time).
func (rt *RequestTracker) StartRequest(opcode byte, handle uint16, timeout time.Duration) (<-chan Response, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	// Check if there's already a pending request
	if rt.pending != nil {
		return nil, fmt.Errorf("ATT request already pending (opcode 0x%02X on handle 0x%04X)",
			rt.pending.Opcode, rt.pending.Handle)
	}

	// Use default timeout if not specified
	if timeout == 0 {
		timeout = rt.defaultTimeout
	}

	// Create pending request
	responseC := make(chan Response, 1)
	timerC := time.After(timeout)

	rt.pending = &PendingRequest{
		Opcode:    opcode,
		Handle:    handle,
		ResponseC: responseC,
		TimerC:    timerC,
		SentAt:    time.Now(),
	}

	// Start timeout goroutine
	go rt.handleTimeout()

	return responseC, nil
}

// handleTimeout waits for timeout and cancels the request if no response arrives
func (rt *RequestTracker) handleTimeout() {
	rt.mu.Lock()
	if rt.pending == nil {
		rt.mu.Unlock()
		return
	}
	timerC := rt.pending.TimerC
	rt.mu.Unlock()

	// Wait for timeout
	<-timerC

	// Check if request is still pending
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if rt.pending == nil {
		return // Already completed
	}

	// Request timed out
	opcode := rt.pending.Opcode
	handle := rt.pending.Handle
	responseC := rt.pending.ResponseC

	// Clear pending request
	rt.pending = nil

	// Send timeout error
	responseC <- Response{
		Error: fmt.Errorf("ATT request timeout: opcode 0x%02X, handle 0x%04X", opcode, handle),
	}
	close(responseC)

	// Invoke callback if set
	if rt.timeoutCallback != nil {
		rt.timeoutCallback(opcode, handle)
	}
}

// CompleteRequest delivers a response to a pending request.
// Returns error if no matching request is pending.
func (rt *RequestTracker) CompleteRequest(responseOpcode byte, packet interface{}) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if rt.pending == nil {
		return fmt.Errorf("no pending ATT request for response opcode 0x%02X", responseOpcode)
	}

	// Check if response matches request
	expectedResponse := GetResponseOpcode(rt.pending.Opcode)
	if responseOpcode != expectedResponse && responseOpcode != OpErrorResponse {
		return fmt.Errorf("unexpected response opcode 0x%02X for request 0x%02X (expected 0x%02X)",
			responseOpcode, rt.pending.Opcode, expectedResponse)
	}

	// Deliver response
	rt.pending.ResponseC <- Response{
		Packet: packet,
		Error:  nil,
	}
	close(rt.pending.ResponseC)

	// Clear pending request
	rt.pending = nil

	return nil
}

// FailRequest fails a pending request with the given error.
// This is used for connection errors, protocol errors, etc.
func (rt *RequestTracker) FailRequest(err error) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if rt.pending == nil {
		return fmt.Errorf("no pending ATT request to fail")
	}

	// Deliver error
	rt.pending.ResponseC <- Response{
		Error: err,
	}
	close(rt.pending.ResponseC)

	// Clear pending request
	rt.pending = nil

	return nil
}

// HasPending returns true if there is a pending request
func (rt *RequestTracker) HasPending() bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.pending != nil
}

// GetPendingInfo returns info about the pending request (for debugging)
func (rt *RequestTracker) GetPendingInfo() (opcode byte, handle uint16, duration time.Duration, hasPending bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if rt.pending == nil {
		return 0, 0, 0, false
	}

	return rt.pending.Opcode, rt.pending.Handle, time.Since(rt.pending.SentAt), true
}

// CancelPending cancels any pending request (used during disconnection)
func (rt *RequestTracker) CancelPending() {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if rt.pending == nil {
		return
	}

	// Send cancellation error
	rt.pending.ResponseC <- Response{
		Error: fmt.Errorf("ATT request cancelled (connection closed)"),
	}
	close(rt.pending.ResponseC)

	rt.pending = nil
}
