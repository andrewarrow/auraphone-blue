package att

import (
	"fmt"
)

// Fragmenter handles fragmentation of large writes and reassembly of fragmented data
type Fragmenter struct {
	// PrepareWriteQueue stores pending prepare write requests
	// Key: handle, Value: ordered list of prepare write responses
	prepareQueue map[uint16][]*PrepareWriteResponse
}

// NewFragmenter creates a new Fragmenter
func NewFragmenter() *Fragmenter {
	return &Fragmenter{
		prepareQueue: make(map[uint16][]*PrepareWriteResponse),
	}
}

// ShouldFragment returns true if the value exceeds MTU and needs fragmentation
// mtu is the negotiated MTU, value is the data to write
// ATT Write Request format: [Opcode:1][Handle:2][Value:N]
// So max value size = MTU - 3
func ShouldFragment(mtu int, value []byte) bool {
	if mtu <= 0 {
		mtu = 23 // Default BLE MTU
	}
	maxValueSize := mtu - 3 // Subtract opcode (1) + handle (2)
	return len(value) > maxValueSize
}

// FragmentWrite splits a large write into multiple Prepare Write requests
// Returns a list of PrepareWriteRequest packets
func FragmentWrite(handle uint16, value []byte, mtu int) ([]*PrepareWriteRequest, error) {
	if !ShouldFragment(mtu, value) {
		return nil, fmt.Errorf("att: value does not need fragmentation (len=%d, mtu=%d)", len(value), mtu)
	}

	// Calculate chunk size
	// PrepareWriteRequest format: [Opcode:1][Handle:2][Offset:2][Value:N]
	// So max value size per chunk = MTU - 5
	maxChunkSize := mtu - 5
	if maxChunkSize <= 0 {
		return nil, fmt.Errorf("att: MTU too small for fragmentation (mtu=%d)", mtu)
	}

	var requests []*PrepareWriteRequest
	offset := uint16(0)

	for offset < uint16(len(value)) {
		chunkSize := maxChunkSize
		remaining := len(value) - int(offset)
		if remaining < chunkSize {
			chunkSize = remaining
		}

		chunk := make([]byte, chunkSize)
		copy(chunk, value[offset:offset+uint16(chunkSize)])

		requests = append(requests, &PrepareWriteRequest{
			Handle: handle,
			Offset: offset,
			Value:  chunk,
		})

		offset += uint16(chunkSize)
	}

	return requests, nil
}

// AddPrepareWriteResponse adds a prepare write response to the queue for reassembly
// Returns an error if the response is invalid (e.g., mismatched offset)
func (f *Fragmenter) AddPrepareWriteResponse(resp *PrepareWriteResponse) error {
	if resp == nil {
		return fmt.Errorf("att: nil prepare write response")
	}

	// Get existing queue for this handle
	queue, exists := f.prepareQueue[resp.Handle]
	if !exists {
		queue = []*PrepareWriteResponse{}
	}

	// Verify offset matches expected position
	expectedOffset := uint16(0)
	for _, r := range queue {
		expectedOffset += uint16(len(r.Value))
	}
	if resp.Offset != expectedOffset {
		return fmt.Errorf("att: prepare write offset mismatch (expected %d, got %d)", expectedOffset, resp.Offset)
	}

	// Add to queue
	queue = append(queue, resp)
	f.prepareQueue[resp.Handle] = queue

	return nil
}

// GetReassembledValue returns the fully reassembled value for a handle
// Returns nil if no data is queued for this handle
func (f *Fragmenter) GetReassembledValue(handle uint16) []byte {
	queue, exists := f.prepareQueue[handle]
	if !exists || len(queue) == 0 {
		return nil
	}

	// Calculate total size
	totalSize := 0
	for _, resp := range queue {
		totalSize += len(resp.Value)
	}

	// Reassemble
	result := make([]byte, totalSize)
	offset := 0
	for _, resp := range queue {
		copy(result[offset:], resp.Value)
		offset += len(resp.Value)
	}

	return result
}

// ClearQueue clears the prepare write queue for a handle
// This should be called after ExecuteWriteRequest is processed
func (f *Fragmenter) ClearQueue(handle uint16) {
	delete(f.prepareQueue, handle)
}

// ClearAllQueues clears all prepare write queues
// This should be called on disconnection or error
func (f *Fragmenter) ClearAllQueues() {
	f.prepareQueue = make(map[uint16][]*PrepareWriteResponse)
}

// GetQueueLength returns the number of prepare write responses queued for a handle
func (f *Fragmenter) GetQueueLength(handle uint16) int {
	queue, exists := f.prepareQueue[handle]
	if !exists {
		return 0
	}
	return len(queue)
}

// GetQueuedHandles returns a list of all handles that have queued prepare writes
func (f *Fragmenter) GetQueuedHandles() []uint16 {
	handles := make([]uint16, 0, len(f.prepareQueue))
	for handle := range f.prepareQueue {
		handles = append(handles, handle)
	}
	return handles
}
