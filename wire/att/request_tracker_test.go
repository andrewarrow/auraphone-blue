package att

import (
	"fmt"
	"testing"
	"time"
)

func TestRequestTracker_SingleRequest(t *testing.T) {
	tracker := NewRequestTracker(100 * time.Millisecond)

	// Start a read request
	responseC, err := tracker.StartRequest(OpReadRequest, 0x0010, 0)
	if err != nil {
		t.Fatalf("StartRequest failed: %v", err)
	}

	// Verify pending state
	if !tracker.HasPending() {
		t.Fatal("Expected pending request")
	}

	opcode, handle, _, hasPending := tracker.GetPendingInfo()
	if !hasPending {
		t.Fatal("Expected hasPending=true")
	}
	if opcode != OpReadRequest {
		t.Errorf("Expected opcode 0x%02X, got 0x%02X", OpReadRequest, opcode)
	}
	if handle != 0x0010 {
		t.Errorf("Expected handle 0x0010, got 0x%04X", handle)
	}

	// Complete with response
	readResp := &ReadResponse{
		Value: []byte{0x01, 0x02, 0x03},
	}
	err = tracker.CompleteRequest(OpReadResponse, readResp)
	if err != nil {
		t.Fatalf("CompleteRequest failed: %v", err)
	}

	// Verify response received
	select {
	case resp := <-responseC:
		if resp.Error != nil {
			t.Fatalf("Expected no error, got: %v", resp.Error)
		}
		readRespReceived, ok := resp.Packet.(*ReadResponse)
		if !ok {
			t.Fatalf("Expected *ReadResponse, got %T", resp.Packet)
		}
		if string(readRespReceived.Value) != string(readResp.Value) {
			t.Errorf("Expected value %v, got %v", readResp.Value, readRespReceived.Value)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Timeout waiting for response")
	}

	// Verify no longer pending
	if tracker.HasPending() {
		t.Fatal("Expected no pending request after completion")
	}
}

func TestRequestTracker_OnlyOneRequestAtTime(t *testing.T) {
	tracker := NewRequestTracker(100 * time.Millisecond)

	// Start first request
	_, err := tracker.StartRequest(OpReadRequest, 0x0010, 0)
	if err != nil {
		t.Fatalf("First StartRequest failed: %v", err)
	}

	// Try to start second request (should fail)
	_, err = tracker.StartRequest(OpWriteRequest, 0x0020, 0)
	if err == nil {
		t.Fatal("Expected error when starting second request, got nil")
	}

	// Complete first request
	readResp := &ReadResponse{Value: []byte{0x01}}
	err = tracker.CompleteRequest(OpReadResponse, readResp)
	if err != nil {
		t.Fatalf("CompleteRequest failed: %v", err)
	}

	// Now second request should succeed
	_, err = tracker.StartRequest(OpWriteRequest, 0x0020, 0)
	if err != nil {
		t.Fatalf("Second StartRequest failed after first completed: %v", err)
	}
}

func TestRequestTracker_Timeout(t *testing.T) {
	tracker := NewRequestTracker(50 * time.Millisecond)

	// Start request with short timeout
	responseC, err := tracker.StartRequest(OpReadRequest, 0x0010, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("StartRequest failed: %v", err)
	}

	// Wait for timeout
	select {
	case resp := <-responseC:
		if resp.Error == nil {
			t.Fatal("Expected timeout error, got nil")
		}
		// Check error message contains "timeout"
		if resp.Packet != nil {
			t.Fatalf("Expected nil packet on timeout, got %T", resp.Packet)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Timeout waiting for timeout error")
	}

	// Verify no longer pending
	if tracker.HasPending() {
		t.Fatal("Expected no pending request after timeout")
	}
}

func TestRequestTracker_TimeoutCallback(t *testing.T) {
	tracker := NewRequestTracker(50 * time.Millisecond)

	// Set up timeout callback
	callbackCalled := false
	var callbackOpcode byte
	var callbackHandle uint16
	tracker.SetTimeoutCallback(func(opcode byte, handle uint16) {
		callbackCalled = true
		callbackOpcode = opcode
		callbackHandle = handle
	})

	// Start request
	responseC, err := tracker.StartRequest(OpReadRequest, 0x0010, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("StartRequest failed: %v", err)
	}

	// Wait for timeout
	<-responseC
	time.Sleep(10 * time.Millisecond) // Give callback time to run

	if !callbackCalled {
		t.Fatal("Expected timeout callback to be called")
	}
	if callbackOpcode != OpReadRequest {
		t.Errorf("Expected callback opcode 0x%02X, got 0x%02X", OpReadRequest, callbackOpcode)
	}
	if callbackHandle != 0x0010 {
		t.Errorf("Expected callback handle 0x0010, got 0x%04X", callbackHandle)
	}
}

func TestRequestTracker_ErrorResponse(t *testing.T) {
	tracker := NewRequestTracker(100 * time.Millisecond)

	// Start request
	responseC, err := tracker.StartRequest(OpReadRequest, 0x0010, 0)
	if err != nil {
		t.Fatalf("StartRequest failed: %v", err)
	}

	// Complete with error response
	errorResp := &ErrorResponse{
		RequestOpcode: OpReadRequest,
		Handle:        0x0010,
		ErrorCode:     ErrInvalidHandle,
	}
	err = tracker.CompleteRequest(OpErrorResponse, errorResp)
	if err != nil {
		t.Fatalf("CompleteRequest with error failed: %v", err)
	}

	// Verify error response received
	select {
	case resp := <-responseC:
		if resp.Error != nil {
			t.Fatalf("Expected no transport error, got: %v", resp.Error)
		}
		errRespReceived, ok := resp.Packet.(*ErrorResponse)
		if !ok {
			t.Fatalf("Expected *ErrorResponse, got %T", resp.Packet)
		}
		if errRespReceived.ErrorCode != ErrInvalidHandle {
			t.Errorf("Expected error code 0x%02X, got 0x%02X", ErrInvalidHandle, errRespReceived.ErrorCode)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Timeout waiting for error response")
	}
}

func TestRequestTracker_FailRequest(t *testing.T) {
	tracker := NewRequestTracker(100 * time.Millisecond)

	// Start request
	responseC, err := tracker.StartRequest(OpReadRequest, 0x0010, 0)
	if err != nil {
		t.Fatalf("StartRequest failed: %v", err)
	}

	// Fail the request (e.g., connection closed)
	err = tracker.FailRequest(fmt.Errorf("connection closed"))
	if err != nil {
		t.Fatalf("FailRequest failed: %v", err)
	}

	// Verify error received
	select {
	case resp := <-responseC:
		if resp.Error == nil {
			t.Fatal("Expected error, got nil")
		}
		if resp.Packet != nil {
			t.Fatalf("Expected nil packet, got %T", resp.Packet)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Timeout waiting for error")
	}

	// Verify no longer pending
	if tracker.HasPending() {
		t.Fatal("Expected no pending request after failure")
	}
}

func TestRequestTracker_CancelPending(t *testing.T) {
	tracker := NewRequestTracker(100 * time.Millisecond)

	// Start request
	responseC, err := tracker.StartRequest(OpReadRequest, 0x0010, 0)
	if err != nil {
		t.Fatalf("StartRequest failed: %v", err)
	}

	// Cancel pending
	tracker.CancelPending()

	// Verify cancellation error received
	select {
	case resp := <-responseC:
		if resp.Error == nil {
			t.Fatal("Expected cancellation error, got nil")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Timeout waiting for cancellation")
	}

	// Verify no longer pending
	if tracker.HasPending() {
		t.Fatal("Expected no pending request after cancellation")
	}
}

func TestRequestTracker_WrongResponseOpcode(t *testing.T) {
	tracker := NewRequestTracker(100 * time.Millisecond)

	// Start read request
	_, err := tracker.StartRequest(OpReadRequest, 0x0010, 0)
	if err != nil {
		t.Fatalf("StartRequest failed: %v", err)
	}

	// Try to complete with wrong response opcode (write response instead of read response)
	writeResp := &WriteResponse{}
	err = tracker.CompleteRequest(OpWriteResponse, writeResp)
	if err == nil {
		t.Fatal("Expected error for wrong response opcode, got nil")
	}

	// Request should still be pending
	if !tracker.HasPending() {
		t.Fatal("Expected request to still be pending after wrong response")
	}
}

func TestRequestTracker_NoRequestToComplete(t *testing.T) {
	tracker := NewRequestTracker(100 * time.Millisecond)

	// Try to complete without starting a request
	readResp := &ReadResponse{Value: []byte{0x01}}
	err := tracker.CompleteRequest(OpReadResponse, readResp)
	if err == nil {
		t.Fatal("Expected error when completing without pending request, got nil")
	}
}

func TestGetResponseOpcode(t *testing.T) {
	tests := []struct {
		request  byte
		response byte
	}{
		{OpExchangeMTURequest, OpExchangeMTUResponse},
		{OpReadRequest, OpReadResponse},
		{OpWriteRequest, OpWriteResponse},
		{OpPrepareWriteRequest, OpPrepareWriteResponse},
		{OpExecuteWriteRequest, OpExecuteWriteResponse},
		{OpReadByTypeRequest, OpReadByTypeResponse},
		{OpReadByGroupTypeRequest, OpReadByGroupTypeResponse},
		{OpWriteCommand, 0}, // No response expected
		{OpHandleValueNotification, 0}, // No response expected
	}

	for _, tt := range tests {
		got := GetResponseOpcode(tt.request)
		if got != tt.response {
			t.Errorf("GetResponseOpcode(0x%02X) = 0x%02X, want 0x%02X", tt.request, got, tt.response)
		}
	}
}
