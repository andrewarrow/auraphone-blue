# Plan: Fix Photo Transfer Reliability Issues

## Problem Summary

From test run analysis (2025-10-27_09-00-37), we have:
- **80% success rate** (16/20 transfers succeeded)
- **4 failed transfers** due to:
  1. Duplicate photo discovery events (same photo "discovered" multiple times)
  2. Out-of-order chunk delivery (chunks arrive as 1→3→2, missing 0 and 4)
  3. No missing chunk detection (receivers silently give up on incomplete photos)
  4. Socket errors (112 errors on X0XUCWL8) causing packet loss
  5. No retry mechanism for lost chunks

## Phase 1: Write Tests to Reproduce the Problems

### Test 1: `message_requeue_test.go` - Duplicate Photo Discovery
**Location**: `phone/message_requeue_test.go` (already exists, expand it)

**Goal**: Prove that MeshView can discover the same photo multiple times

```go
func TestMeshView_DuplicatePhotoDiscovery(t *testing.T) {
    // Setup: Create mesh view with device A
    // Action: Merge gossip from device A twice
    // Assert: photo_discovered should only trigger once
    // Assert: PhotoRequestSent flag should prevent re-requesting
}
```

### Test 2: `photo_assembly_test.go` - Out-of-Order Chunks
**Location**: `phone/photo_assembly_test.go` (new file)

**Goal**: Prove that PhotoTransferCoordinator can handle out-of-order chunks

```go
func TestPhotoAssembly_OutOfOrderChunks(t *testing.T) {
    // Setup: Photo with 5 chunks
    // Action: Deliver chunks in order: 1, 3, 2, 0, 4
    // Assert: Photo should assemble correctly
}

func TestPhotoAssembly_MissingChunks(t *testing.T) {
    // Setup: Photo with 5 chunks
    // Action: Deliver chunks: 0, 1, 3 (skip 2 and 4)
    // Wait for timeout
    // Assert: Transfer should be marked as incomplete/failed
    // Assert: Missing chunks [2, 4] should be tracked
}

func TestPhotoAssembly_AllChunksMissing(t *testing.T) {
    // Setup: Start receiving photo
    // Action: Never deliver any chunks
    // Wait for timeout
    // Assert: Transfer marked as failed
}
```

### Test 3: `packet_loss_test.go` - Socket Errors
**Location**: `wire/packet_loss_test.go` (new file)

**Goal**: Prove that packet loss doesn't cause silent failures

```go
func TestPacketLoss_WithRetries(t *testing.T) {
    // Setup: Wire with 20% packet loss rate
    // Action: Send 10 messages
    // Assert: All messages eventually delivered (via retries)
}

func TestPacketLoss_RetriesExhausted(t *testing.T) {
    // Setup: Wire with 100% packet loss (unreachable)
    // Action: Send message with 3 max retries
    // Assert: Error returned after 3 failed attempts
    // Assert: Sender knows delivery failed
}
```

### Test 4: `integration_reliability_test.go` - End-to-End
**Location**: `integration_reliability_test.go` (new file)

**Goal**: Reproduce the exact scenario from the failed test run

```go
func TestE2E_FivePhones_AllPhotosTransferred(t *testing.T) {
    // Setup: 5 phones (2 iOS, 3 Android) with packet loss enabled
    // Action: Run for 60 seconds
    // Assert: All 20 photo transfers complete (5 phones × 4 others)
    // Assert: No duplicate photo discovery events
    // Assert: No incomplete transfers
}

func TestE2E_LateJoiner(t *testing.T) {
    // Setup: 4 phones start, 5th joins after 20 seconds
    // Action: Run for 60 seconds
    // Assert: Late joiner receives all 4 photos
    // Assert: All 4 early phones receive late joiner's photo
}

func TestE2E_HighPacketLoss(t *testing.T) {
    // Setup: 5 phones with 10% packet loss
    // Action: Run for 120 seconds
    // Assert: Eventually all photos transferred (even with retries)
}
```

## Phase 2: Fix the Issues

### Fix 1: Prevent Duplicate Photo Discovery
**Files**: `phone/mesh_view.go`

**Changes**:
```go
// MergeGossip should check if photo already exists before adding to newDiscoveries
func (mv *MeshView) MergeGossip(msg *proto.GossipMessage) []string {
    var newDiscoveries []string

    for _, deviceState := range msg.MeshView {
        existing, exists := mv.Devices[deviceState.DeviceId]

        // Only add to newDiscoveries if:
        // 1. Device is new (doesn't exist)
        // 2. OR photo hash changed (device updated their photo)
        if !exists {
            newDiscoveries = append(newDiscoveries, deviceState.DeviceId)
        } else if existing.PhotoHash != hex.EncodeToString(deviceState.PhotoHash) {
            // Photo changed - treat as new discovery
            newDiscoveries = append(newDiscoveries, deviceState.DeviceId)
        }
        // If photo hash is same, DON'T add to newDiscoveries

        // Update device state...
    }

    return newDiscoveries
}

// MarkPhotoRequested should be idempotent
func (mv *MeshView) MarkPhotoRequested(deviceID string) {
    if dev, exists := mv.Devices[deviceID]; exists {
        if dev.PhotoRequestSent {
            // Already requested, don't request again
            return
        }
        dev.PhotoRequestSent = true
    }
}
```

### Fix 2: Track Missing Chunks and Retry
**Files**: `phone/photo_transfer_coordinator.go` (new fields)

**Add chunk tracking per transfer**:
```go
type PhotoReceiveState struct {
    FromDeviceID     string
    PhotoHash        string
    TotalChunks      int
    ReceivedChunks   map[int]bool    // NEW: Track which chunks we have
    ChunkData        map[int][]byte  // NEW: Store chunks by index
    StartTime        time.Time
    LastChunkTime    time.Time       // NEW: Last time we received any chunk
    Status           string          // "receiving", "complete", "failed"
}

// Check for missing chunks after timeout
func (ptc *PhotoTransferCoordinator) detectStalledTransfers() {
    ptc.mu.Lock()
    defer ptc.mu.Unlock()

    now := time.Now()
    for key, state := range ptc.receives {
        if state.Status != "receiving" {
            continue
        }

        // If no chunk in last 10 seconds, check for missing
        if now.Sub(state.LastChunkTime) > 10*time.Second {
            missing := state.GetMissingChunks()
            if len(missing) > 0 {
                // Request missing chunks specifically
                ptc.requestMissingChunks(state, missing)
            } else if len(state.ReceivedChunks) == state.TotalChunks {
                // All chunks received, assemble photo
                ptc.assemblePhoto(state)
            } else {
                // Timeout - mark as failed
                state.Status = "failed"
            }
        }
    }
}

func (state *PhotoReceiveState) GetMissingChunks() []int {
    var missing []int
    for i := 0; i < state.TotalChunks; i++ {
        if !state.ReceivedChunks[i] {
            missing = append(missing, i)
        }
    }
    return missing
}
```

### Fix 3: Chunk Reassembly with Out-of-Order Handling
**Files**: `phone/photo_transfer_coordinator.go`

**Changes**:
```go
func (ptc *PhotoTransferCoordinator) HandlePhotoChunk(chunk *proto.PhotoChunk) error {
    key := fmt.Sprintf("%s:%s", chunk.FromDeviceId, hex.EncodeToString(chunk.PhotoHash))

    ptc.mu.Lock()
    state, exists := ptc.receives[key]
    if !exists {
        // First chunk - initialize state
        state = &PhotoReceiveState{
            FromDeviceID:   chunk.FromDeviceId,
            PhotoHash:      hex.EncodeToString(chunk.PhotoHash),
            TotalChunks:    int(chunk.TotalChunks),
            ReceivedChunks: make(map[int]bool),
            ChunkData:      make(map[int][]byte),
            StartTime:      time.Now(),
            LastChunkTime:  time.Now(),
            Status:         "receiving",
        }
        ptc.receives[key] = state
    }
    ptc.mu.Unlock()

    // Store this chunk (out-of-order OK)
    state.ReceivedChunks[int(chunk.ChunkIndex)] = true
    state.ChunkData[int(chunk.ChunkIndex)] = chunk.Data
    state.LastChunkTime = time.Now()

    // Check if we have all chunks now
    if len(state.ReceivedChunks) == state.TotalChunks {
        return ptc.assemblePhoto(state)
    }

    return nil
}

func (ptc *PhotoTransferCoordinator) assemblePhoto(state *PhotoReceiveState) error {
    // Assemble chunks in correct order (0, 1, 2, 3, ...)
    var photoData []byte
    for i := 0; i < state.TotalChunks; i++ {
        chunk, exists := state.ChunkData[i]
        if !exists {
            return fmt.Errorf("missing chunk %d during assembly", i)
        }
        photoData = append(photoData, chunk...)
    }

    // Verify hash
    actualHash := sha256.Sum256(photoData)
    if hex.EncodeToString(actualHash[:]) != state.PhotoHash {
        state.Status = "failed"
        return fmt.Errorf("photo hash mismatch")
    }

    // Save to disk
    cacheManager.SavePhoto(state.PhotoHash, photoData)
    state.Status = "complete"

    return nil
}
```

### Fix 4: Add Missing Chunk Request Protocol
**Files**: `proto/handshake.proto`

**Add new message type**:
```protobuf
message MissingChunksRequest {
  string requester_device_id = 1;
  bytes photo_hash = 2;
  repeated int32 missing_chunk_indices = 3;  // e.g., [0, 4]
}
```

**Files**: `phone/photo_transfer_coordinator.go`

```go
func (ptc *PhotoTransferCoordinator) requestMissingChunks(state *PhotoReceiveState, missing []int) {
    // Send MissingChunksRequest to original sender
    // Sender will re-send only the missing chunks
}
```

### Fix 5: Background Goroutine for Stalled Transfer Detection
**Files**: `phone/phone.go` (or `iphone/iphone.go`, `android/android.go`)

**Add periodic check**:
```go
func (p *Phone) startTransferMonitor() {
    ticker := time.NewTicker(5 * time.Second)
    go func() {
        for range ticker.C {
            p.coordinator.detectStalledTransfers()
        }
    }()
}
```

## Phase 3: Validation

### Run Tests
```bash
# Run new tests
go test ./phone -run TestPhotoAssembly
go test ./phone -run TestMeshView_DuplicatePhotoDiscovery
go test ./wire -run TestPacketLoss
go test . -run TestE2E_FivePhones_AllPhotosTransferred

# Run full suite
go test ./...
```

### Run Integration Test
```bash
# Run 5-phone test with packet loss enabled
go run main.go --phones 5 --duration 120s --packet-loss 0.05

# Check results
cat ~/.auraphone-blue-data/test_report_*.md
# Should show 100% success rate (20/20 transfers)
```

## Success Criteria

✅ **All new tests pass**
✅ **No duplicate photo discovery events** in gossip_audit.jsonl
✅ **100% photo transfer success rate** in 5-phone test (20/20)
✅ **No silent failures** - all incomplete transfers logged as failed
✅ **Missing chunks detected and re-requested** within 10 seconds
✅ **Out-of-order chunks handled correctly** (chunks can arrive in any order)
✅ **Socket errors don't cause permanent failures** (retries succeed)

## Timeline

- **Phase 1 (Tests)**: 2-3 hours
  - Write 4 test files
  - Run and verify tests fail (showing the bugs exist)

- **Phase 2 (Fixes)**: 4-5 hours
  - Fix duplicate discovery (30 min)
  - Add chunk tracking (2 hours)
  - Add missing chunk detection (1 hour)
  - Add retry protocol (1 hour)
  - Add background monitor (30 min)

- **Phase 3 (Validation)**: 1 hour
  - Run all tests
  - Run 5-phone integration test
  - Verify 100% success rate

**Total: 7-9 hours**

## Files to Create/Modify

### New Files
- `phone/photo_assembly_test.go`
- `wire/packet_loss_test.go`
- `integration_reliability_test.go`

### Modified Files
- `phone/mesh_view.go` (fix duplicate discovery)
- `phone/photo_transfer_coordinator.go` (add chunk tracking, assembly, retry)
- `proto/handshake.proto` (add MissingChunksRequest)
- `phone/message_requeue_test.go` (expand existing test)
- `iphone/iphone.go` and `android/android.go` (start transfer monitor)
