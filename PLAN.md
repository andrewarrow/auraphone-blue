# Photo Transfer ACK Protocol - Implementation Plan

## Problem Summary

The current photo transfer system has a critical reliability issue:
- BLE notifications have a 1% drop rate (realistic simulation)
- Dropped chunks are never detected or retried
- Photos with any dropped chunk fail silently and never complete
- The `PhotoRequestSent` flag prevents re-requesting, causing permanent stalls

### Evidence
From test logs with 3 devices:
- Device F09FFFB7 received only 2 of 5 chunks from 12D90340
- Chunks 3, 4, 5 were sent successfully but dropped by the notification layer
- F09FFFB7 never received any complete photos despite being connected for extended time

## Solution: Implement ACK/Retry Protocol

The protobuf definitions already exist (`PhotoChunkAck` in `proto/handshake.proto`), but are not used. We need to implement the full ACK protocol.

---

## Implementation Plan

### Phase 1: Add ACK Sending (Receiver Side)

**File: `phone/photo_handler.go`**

After receiving each chunk (line 138), send an ACK back to the sender:

```go
// Record this chunk
coordinator.RecordReceivedChunk(deviceID, int(chunk.ChunkIndex), chunk.ChunkData)

// NEW: Send ACK for this chunk
ph.sendChunkAck(senderUUID, deviceID, chunk)
```

Add new method:
```go
func (ph *PhotoHandler) sendChunkAck(senderUUID, senderDeviceID string, chunk *proto.PhotoChunkMessage) {
    device := ph.device
    recvState := device.GetPhotoCoordinator().GetReceiveState(senderDeviceID)

    // Build list of missing chunks
    missingChunks := []int32{}
    for i := 0; i < int(chunk.TotalChunks); i++ {
        if _, exists := recvState.ReceivedChunks[i]; !exists {
            missingChunks = append(missingChunks, int32(i))
        }
    }

    transferComplete := recvState.ChunksReceived == recvState.TotalChunks

    ack, _ := CreatePhotoChunkAck(
        device.GetDeviceID(),
        senderDeviceID,
        chunk.PhotoHash,
        int32(chunk.ChunkIndex), // Last chunk we just received
        missingChunks,
        transferComplete,
    )

    ackData, _ := EncodePhotoChunkAck(ack)
    device.GetConnManager().SendToDevice(senderUUID, AuraProtocolCharUUID, ackData)
}
```

**Changes:**
- Add ACK sending after every chunk received
- Calculate missing chunks list
- Send via `AuraProtocolCharUUID` (not photo characteristic)

---

### Phase 2: Handle Incoming ACKs (Sender Side)

**File: `phone/message_router.go`**

Add ACK handling to the protocol message router (similar to how gossip/photo requests are handled):

```go
// In routeProtocolMessage(), add case for PhotoChunkAck:
case *proto.PhotoChunkAck:
    mr.photoHandler.HandlePhotoChunkAck(senderUUID, msg)
```

**File: `phone/photo_handler.go`**

Add new method:
```go
func (ph *PhotoHandler) HandlePhotoChunkAck(senderUUID string, data []byte) {
    // Decode ACK
    ack, err := DecodePhotoChunkAck(data)
    if err != nil {
        return
    }

    // Get deviceID
    deviceID := ack.ReceiverDeviceId

    // Update coordinator state
    coordinator := ph.device.GetPhotoCoordinator()

    if ack.TransferComplete {
        // They received everything - mark as complete
        coordinator.CompleteSend(deviceID)
    } else if len(ack.MissingChunks) > 0 {
        // They're missing chunks - schedule retries
        coordinator.ScheduleChunkRetries(deviceID, ack.MissingChunks)
    }
}
```

**Changes:**
- Decode incoming ACKs
- Update send state based on ACK feedback
- Handle completion or retry scheduling

---

### Phase 3: Add Retry Logic (Sender Side)

**File: `phone/photo_transfer_coordinator.go`**

Extend `PhotoSendState` to track per-chunk state:

```go
type PhotoSendState struct {
    PhotoHash    string
    TotalChunks  int
    ChunksSent   int
    StartTime    time.Time
    LastActivity time.Time

    // NEW: Per-chunk tracking
    ChunkAckReceived map[int]bool       // Which chunks have been ACK'd
    ChunkRetryCount  map[int]int        // How many times each chunk was sent
    ChunkLastSent    map[int]time.Time  // When each chunk was last sent
}
```

Add retry scheduling method:
```go
func (c *PhotoTransferCoordinator) ScheduleChunkRetries(deviceID string, missingChunks []int32) {
    c.mu.Lock()
    defer c.mu.Unlock()

    sendState, exists := c.inProgressSends[deviceID]
    if !exists {
        return
    }

    // Mark chunks as needing retry
    for _, chunkIdx := range missingChunks {
        if sendState.ChunkRetryCount[int(chunkIdx)] < MAX_CHUNK_RETRIES {
            // Trigger retry via callback or event
            // (Implementation depends on how photo sending works)
        } else {
            // Max retries exceeded - fail the transfer
            c.FailSend(deviceID, fmt.Sprintf("chunk %d exceeded max retries", chunkIdx))
            return
        }
    }
}
```

Add timeout-based retry in the existing timeout monitor (line 517):
```go
func (c *PhotoTransferCoordinator) checkForTimeouts() {
    now := time.Now()

    for deviceID, sendState := range c.inProgressSends {
        // NEW: Check for chunks that need retry (no ACK received within timeout)
        for chunkIdx := 0; chunkIdx < sendState.TotalChunks; chunkIdx++ {
            if !sendState.ChunkAckReceived[chunkIdx] {
                lastSent := sendState.ChunkLastSent[chunkIdx]
                if now.Sub(lastSent) > CHUNK_ACK_TIMEOUT {
                    // No ACK received - retry this chunk
                    c.retryChunk(deviceID, chunkIdx)
                }
            }
        }
    }
}
```

**Changes:**
- Track per-chunk ACK status
- Implement timeout-based retries (2-5 seconds per chunk)
- Implement explicit retry on receiving ACK with missing chunks
- Limit retries to prevent infinite loops (MAX_CHUNK_RETRIES = 3)

---

### Phase 4: Modify Photo Sending to Support Retries

**File: `phone/photo_handler.go`**

Refactor `HandlePhotoRequest()` to support sending individual chunks:

Current code (lines 40-81) sends all chunks in a loop. Split into:

1. `sendPhotoChunk(deviceID, chunkIndex)` - Send single chunk
2. `sendAllPhotoChunks(deviceID)` - Send all chunks (calls sendPhotoChunk in loop)

```go
func (ph *PhotoHandler) sendPhotoChunk(targetDeviceID string, chunkIndex int) error {
    // Load photo data
    // Create chunk message for specific index
    // Send via coordinator
    // Update send state (ChunkLastSent, increment ChunkRetryCount)
}

func (ph *PhotoHandler) HandlePhotoRequest(senderUUID string, data []byte) {
    // ... existing validation code ...

    // Send all chunks initially
    ph.sendAllPhotoChunks(req.RequesterDeviceId)
}

func (ph *PhotoHandler) retryChunk(deviceID string, chunkIndex int) {
    // Called by coordinator when chunk needs retry
    ph.sendPhotoChunk(deviceID, chunkIndex)
}
```

**Changes:**
- Extract chunk sending into reusable function
- Add retry entry point
- Update coordinator state after each chunk send

---

### Phase 5: Update Mesh View to Clear Request Flags on Retry

**File: `phone/mesh_view.go`**

Already fixed: `MarkDeviceDisconnected()` now clears `PhotoRequestSent` flag.

Add method to check if transfer is stalled and clear flag:
```go
func (mv *MeshView) CheckStalledTransfers(coordinator *PhotoTransferCoordinator) {
    // Called periodically (every 30s?)
    for deviceID, device := range mv.devices {
        if device.PhotoRequestSent && !device.HavePhoto {
            // Check if coordinator has active transfer
            recvState := coordinator.GetReceiveState(deviceID)
            if recvState == nil || time.Since(recvState.LastActivity) > 60*time.Second {
                // Transfer stalled - clear flag to allow retry
                device.PhotoRequestSent = false
            }
        }
    }
}
```

**Changes:**
- Add stall detection
- Clear request flags for stalled transfers
- Allow re-requesting after stall timeout

---

## Configuration Constants

Add to `phone/photo_transfer_coordinator.go`:

```go
const (
    CHUNK_ACK_TIMEOUT     = 5 * time.Second  // How long to wait for ACK before retry
    MAX_CHUNK_RETRIES     = 5                // Maximum retry attempts per chunk
    TRANSFER_STALL_TIMEOUT = 60 * time.Second // When to consider transfer stalled
)
```

---

## Testing Plan

### Test 1: Normal Transfer (No Drops)
- Disable notification drops
- Verify ACKs are sent and received
- Verify transfer completes normally

### Test 2: Single Chunk Drop
- Enable 1% drop rate
- Send 5-chunk photo
- Verify dropped chunk is detected via missing ACK
- Verify chunk is retried
- Verify transfer completes

### Test 3: Multiple Chunk Drops
- Increase drop rate to 10%
- Send 20-chunk photo
- Verify all dropped chunks are retried
- Verify transfer eventually completes

### Test 4: Max Retries Exceeded
- Force chunk to always drop (simulate permanent connection issue)
- Verify transfer fails after MAX_CHUNK_RETRIES
- Verify PhotoRequestSent flag is cleared
- Verify re-request happens after timeout

### Test 5: Three-Device Mesh
- Repeat original failing scenario
- All devices should eventually receive all photos
- Check cache directories to verify all photos saved

---

## Rollout Plan

1. **Commit 1**: Add ACK sending (Phase 1) - Receivers send ACKs
2. **Commit 2**: Add ACK handling (Phase 2) - Senders receive ACKs
3. **Commit 3**: Add coordinator state tracking (Phase 3 partial)
4. **Commit 4**: Add retry logic (Phase 3 + Phase 4)
5. **Commit 5**: Add stall detection (Phase 5)
6. **Commit 6**: Testing and tuning timeouts

Each commit should be tested independently before moving to the next.

---

## Estimated Effort

- Phase 1 (ACK sending): 30 minutes
- Phase 2 (ACK handling): 30 minutes
- Phase 3 (Coordinator updates): 1 hour
- Phase 4 (Retry logic): 1.5 hours
- Phase 5 (Stall detection): 30 minutes
- Testing & debugging: 2-3 hours

**Total: ~6-7 hours** for complete implementation and testing

---

## Alternative: Quick Fix

If time is critical, temporarily disable notification drops in `wire/simulation.go`:

```go
func (s *Simulator) ShouldNotificationDrop() bool {
    return false // Disable drops until ACK protocol is implemented
}
```

This makes the current system work reliably but doesn't solve the underlying issue.

---

## Success Criteria

✅ All photo transfers complete successfully with 1% notification drop rate
✅ Dropped chunks are detected within CHUNK_ACK_TIMEOUT
✅ Chunks are retried up to MAX_CHUNK_RETRIES times
✅ Transfers fail gracefully after max retries
✅ Three-device test completes with all photos cached
✅ No stalled transfers (all complete or fail within 2 minutes)
