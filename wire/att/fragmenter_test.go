package att

import (
	"bytes"
	"testing"
)

func TestShouldFragment(t *testing.T) {
	tests := []struct {
		name     string
		mtu      int
		value    []byte
		expected bool
	}{
		{
			name:     "small value no fragmentation",
			mtu:      23,
			value:    []byte{1, 2, 3},
			expected: false,
		},
		{
			name:     "exact MTU-3 no fragmentation",
			mtu:      23,
			value:    make([]byte, 20), // MTU=23, max value = 20
			expected: false,
		},
		{
			name:     "exceeds MTU-3 needs fragmentation",
			mtu:      23,
			value:    make([]byte, 21), // MTU=23, max value = 20
			expected: true,
		},
		{
			name:     "large value high MTU",
			mtu:      512,
			value:    make([]byte, 600),
			expected: true,
		},
		{
			name:     "default MTU when zero",
			mtu:      0,
			value:    make([]byte, 21),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ShouldFragment(tt.mtu, tt.value)
			if result != tt.expected {
				t.Errorf("ShouldFragment() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestFragmentWrite(t *testing.T) {
	tests := []struct {
		name          string
		handle        uint16
		value         []byte
		mtu           int
		expectedChunks int
		expectError   bool
	}{
		{
			name:          "fragment into 2 chunks",
			handle:        0x0010,
			value:         make([]byte, 40), // MTU=23, chunk size = 18, need 3 chunks
			mtu:           23,
			expectedChunks: 3,
			expectError:   false,
		},
		{
			name:          "fragment large value",
			handle:        0x0020,
			value:         make([]byte, 1000), // MTU=512, chunk size = 507
			mtu:           512,
			expectedChunks: 2,
			expectError:   false,
		},
		{
			name:        "error when value doesn't need fragmentation",
			handle:      0x0030,
			value:       make([]byte, 10),
			mtu:         23,
			expectError: true,
		},
		{
			name:        "error when MTU too small",
			handle:      0x0040,
			value:       make([]byte, 100),
			mtu:         5,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests, err := FragmentWrite(tt.handle, tt.value, tt.mtu)

			if tt.expectError {
				if err == nil {
					t.Errorf("FragmentWrite() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("FragmentWrite() unexpected error: %v", err)
				return
			}

			if len(requests) != tt.expectedChunks {
				t.Errorf("FragmentWrite() got %d chunks, want %d", len(requests), tt.expectedChunks)
			}

			// Verify all requests have correct handle
			for i, req := range requests {
				if req.Handle != tt.handle {
					t.Errorf("chunk %d: handle = 0x%04X, want 0x%04X", i, req.Handle, tt.handle)
				}
			}

			// Verify offsets are sequential
			expectedOffset := uint16(0)
			for i, req := range requests {
				if req.Offset != expectedOffset {
					t.Errorf("chunk %d: offset = %d, want %d", i, req.Offset, expectedOffset)
				}
				expectedOffset += uint16(len(req.Value))
			}

			// Verify total size matches original value
			totalSize := 0
			for _, req := range requests {
				totalSize += len(req.Value)
			}
			if totalSize != len(tt.value) {
				t.Errorf("FragmentWrite() total size = %d, want %d", totalSize, len(tt.value))
			}
		})
	}
}

func TestFragmenterReassembly(t *testing.T) {
	f := NewFragmenter()
	handle := uint16(0x0010)

	// Create test data
	originalData := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	chunk1 := []byte{1, 2, 3, 4}
	chunk2 := []byte{5, 6, 7}
	chunk3 := []byte{8, 9, 10}

	// Add first chunk
	err := f.AddPrepareWriteResponse(&PrepareWriteResponse{
		Handle: handle,
		Offset: 0,
		Value:  chunk1,
	})
	if err != nil {
		t.Errorf("AddPrepareWriteResponse() chunk 1 error: %v", err)
	}

	// Check queue length
	if f.GetQueueLength(handle) != 1 {
		t.Errorf("GetQueueLength() = %d, want 1", f.GetQueueLength(handle))
	}

	// Add second chunk
	err = f.AddPrepareWriteResponse(&PrepareWriteResponse{
		Handle: handle,
		Offset: 4,
		Value:  chunk2,
	})
	if err != nil {
		t.Errorf("AddPrepareWriteResponse() chunk 2 error: %v", err)
	}

	// Add third chunk
	err = f.AddPrepareWriteResponse(&PrepareWriteResponse{
		Handle: handle,
		Offset: 7,
		Value:  chunk3,
	})
	if err != nil {
		t.Errorf("AddPrepareWriteResponse() chunk 3 error: %v", err)
	}

	// Check queue length
	if f.GetQueueLength(handle) != 3 {
		t.Errorf("GetQueueLength() = %d, want 3", f.GetQueueLength(handle))
	}

	// Get reassembled value
	reassembled := f.GetReassembledValue(handle)
	if !bytes.Equal(reassembled, originalData) {
		t.Errorf("GetReassembledValue() = %v, want %v", reassembled, originalData)
	}

	// Clear queue
	f.ClearQueue(handle)
	if f.GetQueueLength(handle) != 0 {
		t.Errorf("GetQueueLength() after clear = %d, want 0", f.GetQueueLength(handle))
	}
}

func TestFragmenterOffsetMismatch(t *testing.T) {
	f := NewFragmenter()
	handle := uint16(0x0010)

	// Add first chunk
	err := f.AddPrepareWriteResponse(&PrepareWriteResponse{
		Handle: handle,
		Offset: 0,
		Value:  []byte{1, 2, 3},
	})
	if err != nil {
		t.Errorf("AddPrepareWriteResponse() chunk 1 error: %v", err)
	}

	// Try to add chunk with wrong offset (should fail)
	err = f.AddPrepareWriteResponse(&PrepareWriteResponse{
		Handle: handle,
		Offset: 5, // Wrong! Should be 3
		Value:  []byte{4, 5, 6},
	})
	if err == nil {
		t.Errorf("AddPrepareWriteResponse() expected error for offset mismatch, got nil")
	}
}

func TestFragmenterMultipleHandles(t *testing.T) {
	f := NewFragmenter()
	handle1 := uint16(0x0010)
	handle2 := uint16(0x0020)

	// Add data for handle 1
	f.AddPrepareWriteResponse(&PrepareWriteResponse{
		Handle: handle1,
		Offset: 0,
		Value:  []byte{1, 2, 3},
	})

	// Add data for handle 2
	f.AddPrepareWriteResponse(&PrepareWriteResponse{
		Handle: handle2,
		Offset: 0,
		Value:  []byte{4, 5, 6},
	})

	// Check both queues
	if f.GetQueueLength(handle1) != 1 {
		t.Errorf("GetQueueLength(handle1) = %d, want 1", f.GetQueueLength(handle1))
	}
	if f.GetQueueLength(handle2) != 1 {
		t.Errorf("GetQueueLength(handle2) = %d, want 1", f.GetQueueLength(handle2))
	}

	// Clear one queue
	f.ClearQueue(handle1)
	if f.GetQueueLength(handle1) != 0 {
		t.Errorf("GetQueueLength(handle1) after clear = %d, want 0", f.GetQueueLength(handle1))
	}
	if f.GetQueueLength(handle2) != 1 {
		t.Errorf("GetQueueLength(handle2) after clear = %d, want 1", f.GetQueueLength(handle2))
	}

	// Clear all queues
	f.ClearAllQueues()
	if f.GetQueueLength(handle2) != 0 {
		t.Errorf("GetQueueLength(handle2) after clear all = %d, want 0", f.GetQueueLength(handle2))
	}
}

func TestFragmenterNilResponse(t *testing.T) {
	f := NewFragmenter()
	err := f.AddPrepareWriteResponse(nil)
	if err == nil {
		t.Errorf("AddPrepareWriteResponse(nil) expected error, got nil")
	}
}

func TestFragmentWriteRoundTrip(t *testing.T) {
	// Test that fragmentation + reassembly returns the original value
	handle := uint16(0x0010)
	mtu := 512
	originalData := make([]byte, 1000)
	for i := range originalData {
		originalData[i] = byte(i % 256)
	}

	// Fragment
	requests, err := FragmentWrite(handle, originalData, mtu)
	if err != nil {
		t.Fatalf("FragmentWrite() error: %v", err)
	}

	// Simulate server responses (echo back)
	f := NewFragmenter()
	for _, req := range requests {
		resp := &PrepareWriteResponse{
			Handle: req.Handle,
			Offset: req.Offset,
			Value:  req.Value,
		}
		err := f.AddPrepareWriteResponse(resp)
		if err != nil {
			t.Fatalf("AddPrepareWriteResponse() error: %v", err)
		}
	}

	// Reassemble
	reassembled := f.GetReassembledValue(handle)
	if !bytes.Equal(reassembled, originalData) {
		t.Errorf("Round trip failed: length mismatch (got %d, want %d)", len(reassembled), len(originalData))
		// Find first mismatch
		for i := 0; i < len(originalData) && i < len(reassembled); i++ {
			if originalData[i] != reassembled[i] {
				t.Errorf("First mismatch at byte %d: got 0x%02X, want 0x%02X", i, reassembled[i], originalData[i])
				break
			}
		}
	}
}
