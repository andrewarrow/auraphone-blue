# Auraphone Blue - Architecture Fix Plan

## Executive Summary

The `RetryMissingRequestsForConnection` commit revealed fundamental architectural issues in how the simulator handles the timing gap between **gossip-based discovery** and **actual BLE connections**. This plan addresses three critical concerns before moving to real hardware.

---

## Current State Assessment

### ✅ What's Working Well
- Core gossip protocol implementation (mesh view, neighbor selection)
- Dual-role BLE simulation (Central + Peripheral via Unix sockets)
- Wire protocol with realistic MTU, packet loss, fragmentation
- Photo transfer coordinator with chunking and retries
- Platform-specific iOS/Android API simulation

### 🚨 Critical Issues Discovered

#### Issue #1: Race Condition - Gossip Discovery ≠ Connection Availability
**Problem**: Devices learn about peers via multi-hop gossip before establishing direct connections. Code attempts to send requests immediately, which fail silently.

**Evidence**:
- `phone/message_router.go:124-129` - Sends requests to unreachable devices
- `RetryMissingRequestsForConnection` exists solely to fix failed sends
- No connection prerequisite checks before sending

**Impact on Real Hardware**: 10x worse - gossip spreads in 100ms, BLE connections take 2-5 seconds

#### Issue #2: Two-Layer Identity Mapping Creates Routing Chaos
**Problem**: Two identifier systems (hardware UUID for wire layer, device ID for app layer) with inconsistent mapping across 3+ data structures.

**Evidence**:
- `iphone.go:34` - `peripheralToDeviceID`
- `android.go:37` - `remoteUUIDToDeviceID`
- `mesh_view.go:19` - `HardwareUUID` field
- No single source of truth for bidirectional mapping

**Impact on Real Hardware**: iOS/Android randomize hardware UUIDs across restarts - mapping will break

#### Issue #3: No Explicit Request Queue / Connection-Aware Sending
**Problem**: Failed requests are lost until connection callback re-scans entire mesh view. O(N²) complexity on every connection event.

**Evidence**:
- `phone/message_router.go:170-184` - Scans all devices on every connection
- No persistent queue for failed requests
- No prioritization or deduplication

**Impact on Real Hardware**: Battery drain, CPU waste with 50+ devices and frequent reconnections

---

## The Fix - 4-Week Implementation Plan

### Week 1: Connection-Aware Request Queue System

**Goal**: Stop sending requests to unreachable devices. Queue them for later delivery.

#### Step 1.1: Create Request Queue Infrastructure
**File**: `phone/request_queue.go` (new)

```go
package phone

// RequestType identifies the kind of pending request
type RequestType string

const (
    RequestTypePhoto   RequestType = "photo"
    RequestTypeProfile RequestType = "profile"
)

// PendingRequest represents a request waiting for a connection
type PendingRequest struct {
    DeviceID      string      // Target device (base36 ID)
    HardwareUUID  string      // Target hardware UUID (if known)
    Type          RequestType
    PhotoHash     string      // For photo requests
    ProfileVersion int32      // For profile requests
    CreatedAt     time.Time
    Attempts      int
}

// RequestQueue manages pending requests that can't be sent immediately
type RequestQueue struct {
    mu                sync.RWMutex
    hardwareUUID      string

    // Pending requests by device ID
    byDeviceID        map[string][]*PendingRequest

    // Pending requests by hardware UUID (for fast lookup on connection)
    byHardwareUUID    map[string][]*PendingRequest

    // Persistent storage
    statePath         string
}

// NewRequestQueue creates a new request queue
func NewRequestQueue(hardwareUUID string) *RequestQueue

// Enqueue adds a request to the queue
func (rq *RequestQueue) Enqueue(req *PendingRequest) error

// DequeueForConnection retrieves all pending requests for a newly connected device
func (rq *RequestQueue) DequeueForConnection(hardwareUUID string) []*PendingRequest

// Remove deletes a request (after successful send or permanent failure)
func (rq *RequestQueue) Remove(deviceID string, reqType RequestType)

// GetPendingCount returns number of queued requests
func (rq *RequestQueue) GetPendingCount() int

// SaveToDisk persists queue state
func (rq *RequestQueue) SaveToDisk() error

// LoadFromDisk restores queue state
func (rq *RequestQueue) LoadFromDisk() error
```

**Tests**: `phone/request_queue_test.go`
- Test enqueueing requests
- Test dequeue on connection
- Test persistence across restarts
- Test removal after success
- Test duplicate detection

#### Step 1.2: Integrate Request Queue into Message Router
**File**: `phone/message_router.go`

**Changes**:
```go
type MessageRouter struct {
    // ... existing fields ...
    requestQueue     *RequestQueue // NEW
}

func NewMessageRouter(...) *MessageRouter {
    // ... existing code ...
    mr.requestQueue = NewRequestQueue(hardwareUUID)
    mr.requestQueue.LoadFromDisk()
    return mr
}

// handleGossipMessage - MODIFIED
func (mr *MessageRouter) handleGossipMessage(senderUUID string, gossip *proto.GossipMessage) error {
    // ... existing gossip merge ...

    // Check for missing photos - MODIFIED LOGIC
    if mr.onPhotoNeeded != nil {
        missingPhotos := mr.meshView.GetMissingPhotos()
        for _, device := range missingPhotos {
            hardwareUUID := mr.meshView.GetHardwareUUID(device.DeviceID)

            // Check if we're connected before sending
            if hardwareUUID != "" && mr.isConnected(hardwareUUID) {
                // Direct send
                err := mr.onPhotoNeeded(device.DeviceID, device.PhotoHash)
                if err == nil {
                    mr.meshView.MarkPhotoRequested(device.DeviceID)
                }
            } else {
                // Queue for later delivery
                req := &PendingRequest{
                    DeviceID:     device.DeviceID,
                    HardwareUUID: hardwareUUID,
                    Type:         RequestTypePhoto,
                    PhotoHash:    device.PhotoHash,
                    CreatedAt:    time.Now(),
                }
                mr.requestQueue.Enqueue(req)
            }
        }
    }

    // Same logic for profiles...
}

// RetryMissingRequestsForConnection - REPLACED WITH FlushQueueForConnection
func (mr *MessageRouter) FlushQueueForConnection(hardwareUUID string) {
    prefix := fmt.Sprintf("%s %s", mr.deviceHardwareUUID[:8], mr.devicePlatform)

    pendingRequests := mr.requestQueue.DequeueForConnection(hardwareUUID)

    logger.Debug(prefix, "📤 Flushing %d pending requests for newly connected %s",
        len(pendingRequests), hardwareUUID[:8])

    for _, req := range pendingRequests {
        switch req.Type {
        case RequestTypePhoto:
            err := mr.onPhotoNeeded(req.DeviceID, req.PhotoHash)
            if err == nil {
                mr.meshView.MarkPhotoRequested(req.DeviceID)
                mr.requestQueue.Remove(req.DeviceID, RequestTypePhoto)
            } else {
                // Re-queue with incremented attempt count
                req.Attempts++
                if req.Attempts < 5 {
                    mr.requestQueue.Enqueue(req)
                } else {
                    logger.Warn(prefix, "Giving up on photo request for %s after %d attempts",
                        req.DeviceID[:8], req.Attempts)
                }
            }

        case RequestTypeProfile:
            // Similar logic...
        }
    }
}

// NEW: Helper to check if connected
func (mr *MessageRouter) isConnected(hardwareUUID string) bool {
    // Delegate to connection manager or wire layer
    // Implementation depends on how you track connections
    return false // TODO: implement
}
```

**Changes to iOS/Android**:
- Replace `messageRouter.RetryMissingRequestsForConnection()` calls with `messageRouter.FlushQueueForConnection()`
- `iphone/central_delegate.go:190`
- `android/central_delegate.go:189`

**Success Criteria**:
- ✅ No more immediate send failures for unreachable devices
- ✅ Queue persists across app restarts
- ✅ Requests automatically send when connection becomes available
- ✅ Failed sends retry with backoff (max 5 attempts)

---

### Week 2: Centralized Identity Management

**Goal**: Single source of truth for hardware UUID ↔ device ID mapping.

#### Step 2.1: Create Identity Manager
**File**: `phone/identity_manager.go` (new)

```go
package phone

// IdentityManager maintains bidirectional mapping between hardware UUIDs and device IDs
// This is the ONLY place where this mapping should exist
type IdentityManager struct {
    mu               sync.RWMutex
    ourHardwareUUID  string
    ourDeviceID      string

    // Bidirectional mapping
    hardwareToDevice map[string]string // hardware UUID -> device ID
    deviceToHardware map[string]string // device ID -> hardware UUID

    // Connection state (tracks which devices are currently reachable)
    connectedDevices map[string]bool   // hardware UUID -> is connected

    // Persistence
    statePath        string
}

type IdentityMapping struct {
    HardwareUUID string `json:"hardware_uuid"`
    DeviceID     string `json:"device_id"`
    LastSeen     int64  `json:"last_seen"`
}

// NewIdentityManager creates a new identity manager
func NewIdentityManager(ourHardwareUUID, ourDeviceID string) *IdentityManager

// RegisterDevice adds or updates a hardware UUID <-> device ID mapping
func (im *IdentityManager) RegisterDevice(hardwareUUID, deviceID string)

// GetDeviceID looks up device ID from hardware UUID
func (im *IdentityManager) GetDeviceID(hardwareUUID string) (string, bool)

// GetHardwareUUID looks up hardware UUID from device ID
func (im *IdentityManager) GetHardwareUUID(deviceID string) (string, bool)

// MarkConnected marks a device as currently connected
func (im *IdentityManager) MarkConnected(hardwareUUID string)

// MarkDisconnected marks a device as disconnected
func (im *IdentityManager) MarkDisconnected(hardwareUUID string)

// IsConnected checks if a device is currently reachable
func (im *IdentityManager) IsConnected(hardwareUUID string) bool

// IsConnectedByDeviceID checks if a device is reachable by device ID
func (im *IdentityManager) IsConnectedByDeviceID(deviceID string) bool

// GetAllConnectedDevices returns list of connected device IDs
func (im *IdentityManager) GetAllConnectedDevices() []string

// GetAllKnownDevices returns all device IDs we've ever seen
func (im *IdentityManager) GetAllKnownDevices() []string

// SaveToDisk persists mappings
func (im *IdentityManager) SaveToDisk() error

// LoadFromDisk restores mappings
func (im *IdentityManager) LoadFromDisk() error
```

**Tests**: `phone/identity_manager_test.go`
- Test bidirectional mapping
- Test connection state tracking
- Test persistence
- Test concurrent access (race detector)
- Test handling of missing/invalid UUIDs

#### Step 2.2: Integrate Identity Manager into Core
**Files to modify**:
- `iphone/iphone.go`
- `android/android.go`
- `phone/message_router.go`
- `phone/connection_manager.go`

**Changes to iPhone**:
```go
type iPhone struct {
    // ... existing fields ...

    // REMOVED:
    // peripheralToDeviceID   map[string]string

    // ADDED:
    identityManager *IdentityManager
}

func NewIPhone(hardwareUUID string) *iPhone {
    // ... existing code ...

    // Initialize identity manager
    ip.identityManager = phone.NewIdentityManager(hardwareUUID, deviceID)
    ip.identityManager.LoadFromDisk()

    // Set callback to register new devices
    ip.messageRouter.SetDeviceIDDiscoveredCallback(func(hardwareUUID, deviceID string) {
        ip.identityManager.RegisterDevice(hardwareUUID, deviceID)
        ip.identityManager.SaveToDisk()
    })

    // ... rest of initialization ...
}

// Update all places that used peripheralToDeviceID:
// BEFORE: deviceID := ip.peripheralToDeviceID[hardwareUUID]
// AFTER:  deviceID, _ := ip.identityManager.GetDeviceID(hardwareUUID)
```

**Similar changes for Android**.

**Changes to MeshView**:
```go
// REMOVE HardwareUUID field from MeshDeviceState
type MeshDeviceState struct {
    DeviceID            string    `json:"device_id"`
    // HardwareUUID        string    `json:"hardware_uuid"` // REMOVED - use IdentityManager instead
    PhotoHash           string    `json:"photo_hash"`
    // ... rest unchanged ...
}

// REMOVE GetHardwareUUID method - replaced by IdentityManager
// BEFORE:
// func (mv *MeshView) GetHardwareUUID(deviceID string) string

// Update UpdateDevice to not take hardwareUUID parameter
func (mv *MeshView) UpdateDevice(deviceID, photoHashHex, firstName string, ...) {
    // No longer store hardwareUUID here
}
```

**Changes to MessageRouter**:
```go
type MessageRouter struct {
    // ... existing fields ...
    identityManager  *IdentityManager // NEW
}

func (mr *MessageRouter) SetIdentityManager(im *IdentityManager) {
    mr.identityManager = im
}

func (mr *MessageRouter) isConnected(hardwareUUID string) bool {
    return mr.identityManager.IsConnected(hardwareUUID)
}
```

**Changes to ConnectionManager**:
```go
type ConnectionManager struct {
    // ... existing fields ...
    identityManager  *IdentityManager // NEW
}

func (cm *ConnectionManager) SetIdentityManager(im *IdentityManager) {
    cm.identityManager = im
}

// Update RegisterCentralConnection to mark as connected
func (cm *ConnectionManager) RegisterCentralConnection(remoteUUID string, conn interface{}) {
    cm.mu.Lock()
    defer cm.mu.Unlock()

    cm.centralConnections[remoteUUID] = conn

    if cm.identityManager != nil {
        cm.identityManager.MarkConnected(remoteUUID)
    }
}

// Similar for RegisterPeripheralConnection, Unregister* methods
```

**Success Criteria**:
- ✅ Single source of truth for all identity mappings
- ✅ No duplicate mapping data structures in iOS/Android
- ✅ Connection state tracked alongside identity
- ✅ Mappings persist and restore correctly

---

### Week 3: Mesh View Connection State Separation

**Goal**: Distinguish between "devices we know about" vs "devices we can reach".

#### Step 3.1: Add Connection State to Mesh View
**File**: `phone/mesh_view.go`

**Changes**:
```go
type MeshView struct {
    mu sync.RWMutex

    // Our identity
    ourDeviceID     string
    ourHardwareUUID string

    // Mesh state: ALL devices we've heard about (via gossip)
    devices map[string]*MeshDeviceState

    // NEW: Devices we're actually connected to right now
    connectedNeighbors map[string]bool // deviceID -> is connected

    // Neighbor management (for gossip protocol)
    maxNeighbors     int
    gossipNeighbors  []string // Deterministic neighbor selection for gossip

    // ... rest unchanged ...

    // NEW: Reference to identity manager for connection checks
    identityManager *IdentityManager
}

func NewMeshView(..., identityManager *IdentityManager) *MeshView {
    mv := &MeshView{
        // ... existing ...
        connectedNeighbors: make(map[string]bool),
        identityManager:    identityManager,
    }
    return mv
}

// NEW: Mark device as connected (call when connection established)
func (mv *MeshView) MarkDeviceConnected(deviceID string) {
    mv.mu.Lock()
    defer mv.mu.Unlock()
    mv.connectedNeighbors[deviceID] = true
}

// NEW: Mark device as disconnected (call when connection drops)
func (mv *MeshView) MarkDeviceDisconnected(deviceID string) {
    mv.mu.Lock()
    defer mv.mu.Unlock()
    delete(mv.connectedNeighbors, deviceID)
}

// NEW: Check if device is currently connected
func (mv *MeshView) IsDeviceConnected(deviceID string) bool {
    mv.mu.RLock()
    defer mv.mu.RUnlock()
    return mv.connectedNeighbors[deviceID]
}

// NEW: Get only reachable devices (for photo/profile requests)
func (mv *MeshView) GetConnectedDevices() []*MeshDeviceState {
    mv.mu.RLock()
    defer mv.mu.RUnlock()

    connected := []*MeshDeviceState{}
    for deviceID := range mv.connectedNeighbors {
        if device, exists := mv.devices[deviceID]; exists {
            connected = append(connected, device)
        }
    }
    return connected
}

// MODIFIED: GetMissingPhotos - only return photos from connected devices
func (mv *MeshView) GetMissingPhotos() []*MeshDeviceState {
    mv.mu.RLock()
    defer mv.mu.RUnlock()

    missing := []*MeshDeviceState{}

    for _, device := range mv.devices {
        // Only include if: missing photo AND currently connected
        if !device.HavePhoto && device.PhotoHash != "" && !device.PhotoRequestSent {
            // Check if this device is reachable via identity manager
            if mv.identityManager != nil {
                if hardwareUUID, ok := mv.identityManager.GetHardwareUUID(device.DeviceID); ok {
                    if mv.identityManager.IsConnected(hardwareUUID) {
                        missing = append(missing, device)
                    }
                }
            }
        }
    }

    return missing
}

// Similar for GetMissingProfiles
```

#### Step 3.2: Update Connection Callbacks
**Files**: `iphone/central_delegate.go`, `android/central_delegate.go`

**Changes**:
```go
// iOS: DidConnectPeripheral
func (ip *iPhone) DidConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral) {
    // ... existing connection logic ...

    // NEW: Mark device as connected in mesh view
    if deviceID, ok := ip.identityManager.GetDeviceID(peripheral.UUID); ok {
        ip.meshView.MarkDeviceConnected(deviceID)
    }

    // Flush queued requests for this connection
    go ip.messageRouter.FlushQueueForConnection(peripheral.UUID)
}

// iOS: DidDisconnectPeripheral
func (ip *iPhone) DidDisconnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, err error) {
    // ... existing disconnect logic ...

    // NEW: Mark device as disconnected in mesh view
    if deviceID, ok := ip.identityManager.GetDeviceID(peripheral.UUID); ok {
        ip.meshView.MarkDeviceDisconnected(deviceID)
    }
}

// Android: Similar changes in OnConnectionStateChange
```

#### Step 3.3: Update Request Logic
**File**: `phone/message_router.go`

**Changes**:
```go
// handleGossipMessage - SIMPLIFIED (no more manual connection checks)
func (mr *MessageRouter) handleGossipMessage(senderUUID string, gossip *proto.GossipMessage) error {
    // ... merge gossip ...

    // Get missing photos - NOW ONLY RETURNS CONNECTED DEVICES
    if mr.onPhotoNeeded != nil {
        missingPhotos := mr.meshView.GetMissingPhotos()
        for _, device := range missingPhotos {
            // We know these devices are connected, safe to send
            err := mr.onPhotoNeeded(device.DeviceID, device.PhotoHash)
            if err == nil {
                mr.meshView.MarkPhotoRequested(device.DeviceID)
            }
            // If error, it's a real failure (not "not connected"), log it
        }
    }

    // Same for profiles...
}
```

**Success Criteria**:
- ✅ `GetMissingPhotos()` only returns devices we're connected to
- ✅ No more "not connected" errors when sending requests
- ✅ Connection state accurately reflects current reachability
- ✅ Mesh view knows about 50 devices but only tries to request from 3-5 connected ones

---

### Week 4: Integration Testing & Performance Validation

**Goal**: Validate fixes work correctly and scale to 50+ devices.

#### Step 4.1: Integration Test Suite
**File**: `phone/integration_test.go` (new)

**Test Scenarios**:

```go
// Test 1: Late Connection After Gossip
func TestLateConnectionAfterGossip(t *testing.T) {
    // Setup: Device A, B, C
    // 1. Connect A <-> B
    // 2. B sends gossip about C to A (A learns C exists but not connected)
    // 3. Verify A doesn't immediately try to send to C
    // 4. Verify request is queued
    // 5. Connect A <-> C
    // 6. Verify queued request is sent
    // 7. Verify no duplicate sends
}

// Test 2: Multi-Hop Discovery
func TestMultiHopDiscovery(t *testing.T) {
    // Setup: A <-> B <-> C <-> D (chain topology)
    // 1. B gossips about C and D to A
    // 2. Verify A knows about C and D
    // 3. Verify A doesn't send requests to C or D (not connected)
    // 4. Connect A <-> C
    // 5. Verify A sends requests to C only (not D)
    // 6. Connect A <-> D
    // 7. Verify A sends requests to D
}

// Test 3: Connection Churn
func TestConnectionChurn(t *testing.T) {
    // Setup: A and 10 devices
    // 1. Connect A to all 10
    // 2. Start gossip and photo exchanges
    // 3. Randomly disconnect/reconnect devices (50 events)
    // 4. Verify no lost requests
    // 5. Verify all photos eventually received
    // 6. Verify no duplicate transfers
}

// Test 4: Identity Mapping Persistence
func TestIdentityMappingPersistence(t *testing.T) {
    // 1. Create device A, connect to B and C
    // 2. Gossip and exchange photos
    // 3. Shutdown device A, save state
    // 4. Restart device A
    // 5. Verify identity mappings restored
    // 6. Verify can still communicate with B and C
    // 7. Verify no duplicate photo requests
}

// Test 5: Request Queue Persistence
func TestRequestQueuePersistence(t *testing.T) {
    // 1. Create device A
    // 2. Learn about B via gossip (not connected)
    // 3. Verify request queued
    // 4. Shutdown device A
    // 5. Restart device A
    // 6. Connect A <-> B
    // 7. Verify queued request still sent
}

// Test 6: Scale Test - 50 Devices
func TestScaleFiftyDevices(t *testing.T) {
    // 1. Create 50 devices
    // 2. Connect each device to 3-5 random neighbors
    // 3. Start gossip on all devices
    // 4. Verify gossip propagates to all devices (within 30s)
    // 5. Trigger photo changes on 10 random devices
    // 6. Verify photo requests sent only to connected neighbors
    // 7. Verify queue grows for non-connected devices
    // 8. Gradually connect all devices
    // 9. Verify all queued requests sent
    // 10. Verify total message count is reasonable (no O(N²) behavior)
}

// Test 7: Connection Manager Integration
func TestConnectionManagerSendRouting(t *testing.T) {
    // 1. Device A connects to B as Central
    // 2. Device C connects to A as Central (A is Peripheral)
    // 3. Verify A can send to B via Central mode
    // 4. Verify A can send to C via Peripheral mode
    // 5. Verify identity manager tracks both connections
    // 6. Verify mesh view shows both as connected
}
```

#### Step 4.2: Performance Benchmarks
**File**: `phone/benchmark_test.go` (new)

```go
// Benchmark request queue operations
func BenchmarkRequestQueueEnqueue(b *testing.B)
func BenchmarkRequestQueueDequeue(b *testing.B)

// Benchmark identity lookups
func BenchmarkIdentityManagerLookup(b *testing.B)

// Benchmark mesh view operations with 50 devices
func BenchmarkMeshViewGetMissingPhotos50Devices(b *testing.B)
func BenchmarkMeshViewGetConnectedDevices50Devices(b *testing.B)

// Benchmark gossip handling with 50 devices
func BenchmarkGossipHandling50Devices(b *testing.B)
```

**Performance Targets**:
- ✅ Request queue enqueue/dequeue: < 1ms per operation
- ✅ Identity manager lookup: < 100μs per lookup
- ✅ GetMissingPhotos with 50 devices: < 5ms
- ✅ Gossip merge with 50 devices: < 10ms
- ✅ FlushQueueForConnection: < 10ms per connection

#### Step 4.3: Stress Testing Scenarios
**File**: `cmd/stress_test.go` (new)

```go
// Scenario 1: Rapid Connection/Disconnection
// - 20 devices, each connects/disconnects every 5 seconds
// - Run for 5 minutes
// - Verify no memory leaks, no goroutine leaks

// Scenario 2: Gossip Storm
// - 50 devices in full mesh
// - All devices gossip simultaneously every 5 seconds
// - Run for 10 minutes
// - Verify CPU usage reasonable, no message loss

// Scenario 3: Photo Flood
// - 10 devices, each changes photo every 30 seconds
// - All devices connected to all others
// - Run for 10 minutes
// - Verify all photos propagate, no duplicate transfers

// Scenario 4: Mixed Workload
// - 30 devices with varying connection patterns
// - Random photo changes
// - Random profile updates
// - Random connection churn
// - Run for 30 minutes
// - Verify system stability
```

#### Step 4.4: Memory & Goroutine Leak Detection
**File**: `phone/leak_test.go` (new)

```go
func TestNoMemoryLeaks(t *testing.T) {
    // 1. Measure baseline memory
    // 2. Create 50 devices
    // 3. Run for 5 minutes with activity
    // 4. Shutdown all devices
    // 5. Run GC
    // 6. Verify memory returns close to baseline
}

func TestNoGoroutineLeaks(t *testing.T) {
    // 1. Count baseline goroutines
    // 2. Create and destroy 100 devices
    // 3. Wait for cleanup
    // 4. Verify goroutine count returns to baseline
}
```

#### Step 4.5: Real-World Simulation
**File**: `cmd/real_world_sim.go` (new)

Simulate realistic BLE behavior:
- **Discovery delays**: 500ms - 2s per device
- **Connection delays**: 30ms - 100ms per connection
- **Packet loss**: 0.1% as configured
- **Connection drops**: Random every 30-120s
- **iOS auto-reconnect**: Re-establish dropped connections after 2s
- **Android manual reconnect**: App detects disconnect and reconnects after 5s

**Test Matrix**:
| Devices | Connection Pattern | Duration | Success Criteria |
|---------|-------------------|----------|------------------|
| 5 | Full mesh | 10 min | 100% photo delivery |
| 10 | Star (1 hub) | 20 min | 100% photo delivery, < 5% retries |
| 20 | Random 3-5 neighbors | 30 min | 100% photo delivery, < 10% retries |
| 50 | Sparse mesh (avg 4 neighbors) | 60 min | 95%+ photo delivery, < 20% retries |

**Success Criteria**:
- ✅ All integration tests pass
- ✅ All performance benchmarks meet targets
- ✅ No memory or goroutine leaks detected
- ✅ 50-device real-world simulation runs stable for 1 hour
- ✅ Request queue never grows unbounded
- ✅ No O(N²) behavior in message handling
- ✅ CPU usage reasonable (< 10% average on modern hardware)

---

## Implementation Order & Dependencies

### Phase 1: Week 1 (Request Queue)
**Dependencies**: None
**Risk**: Low
**Blockers**: None

Start here because it's self-contained and provides immediate value.

### Phase 2: Week 2 (Identity Manager)
**Dependencies**: Week 1 complete (request queue needs identity manager)
**Risk**: Medium (touches many files)
**Blockers**: Must finish before Week 3

Critical path - clean up identity confusion before adding more connection state.

### Phase 3: Week 3 (Mesh View Connection State)
**Dependencies**: Week 2 complete (uses identity manager)
**Risk**: Medium (changes mesh view logic)
**Blockers**: Must finish before Week 4

Completes the architectural fixes - makes request logic clean and correct.

### Phase 4: Week 4 (Testing & Validation)
**Dependencies**: Weeks 1-3 complete
**Risk**: Low (mostly new test code)
**Blockers**: None

Validates everything works together and scales properly.

---

## Migration Strategy

### Option A: Big Bang (Recommended for Simulator)
**Approach**: Implement all fixes in a feature branch, test thoroughly, merge when Week 4 passes.

**Pros**:
- Clean architecture from day one
- No half-broken states
- Easier to test as a complete system

**Cons**:
- Can't test incrementally on main branch
- Merge conflicts if main branch changes

### Option B: Incremental (Lower Risk)
**Approach**: Merge each week's changes to main after testing that week.

**Pros**:
- Can catch regressions early
- Smaller merges
- Main branch always works (maybe not optimally)

**Cons**:
- More complex - need shims for incomplete features
- Users might see partial improvements

**Recommendation**: Use **Option A** since you're pre-real-hardware and can afford a feature branch.

---

## Definition of Done

Before declaring this plan complete and moving to real hardware:

### Code Quality
- ✅ All new code has unit tests (> 80% coverage)
- ✅ All integration tests pass
- ✅ No compiler warnings
- ✅ `go vet` passes
- ✅ `go test -race` passes (no data races)

### Performance
- ✅ All benchmarks meet target numbers
- ✅ No memory leaks detected (heap profile analysis)
- ✅ No goroutine leaks detected
- ✅ CPU profiling shows no hot spots > 10% of total time

### Functional Correctness
- ✅ 50-device simulation runs stable for 1 hour
- ✅ 100% photo delivery in full mesh topology
- ✅ 95%+ photo delivery in sparse mesh topology
- ✅ Request queue bounded (< 100 pending requests per device)
- ✅ Identity mappings persist and restore correctly
- ✅ Connection state accurately reflects reality

### Documentation
- ✅ ARCHITECTURE.md updated with new components
- ✅ All public functions have godoc comments
- ✅ README.md updated with new testing instructions
- ✅ CLAUDE.md updated with new design rules

### Real Hardware Readiness Checklist
- ✅ All simulator tests pass with realistic BLE delays (2-5s connections)
- ✅ Stress tests pass with 50+ devices
- ✅ Code handles connection churn gracefully
- ✅ Identity persistence tested across restarts
- ✅ Request queue tested across restarts
- ✅ No hardcoded assumptions about connection timing
- ✅ All iOS/Android platform differences documented

---

## Post-Implementation: Hardware Testing Plan

Once the above is complete, test on real hardware in this order:

### Stage 1: Basic Hardware Validation (2 devices)
1. iOS ↔ iOS connection
2. Android ↔ Android connection
3. iOS ↔ Android connection
4. Photo exchange both directions
5. Reconnection after disconnect

### Stage 2: Small Mesh (3-5 devices)
1. Triangle topology (A ↔ B, B ↔ C, C ↔ A)
2. Gossip propagation
3. Photo requests via gossip
4. Connection drop recovery

### Stage 3: Medium Mesh (10-15 devices)
1. Sparse mesh with 3-5 neighbors per device
2. Multi-hop gossip
3. Photo propagation across network
4. Device churn (add/remove devices)

### Stage 4: Full Scale (20-50 devices)
1. Real-world scenarios (conference room, subway car)
2. Long-duration testing (hours)
3. Battery life measurement
4. Performance monitoring

---

## Risk Mitigation

### Risk 1: Breaking Changes During Implementation
**Mitigation**:
- Create feature branch `fix/architecture-gaps`
- Don't merge to main until Week 4 tests pass
- Keep main branch working with old code

### Risk 2: Unforeseen Issues with Identity Mapping
**Mitigation**:
- Add extensive logging to identity manager
- Create debug endpoints to dump identity state
- Test with hardware UUID rotation (iOS privacy feature)

### Risk 3: Performance Regressions
**Mitigation**:
- Run benchmarks before starting (baseline)
- Run benchmarks after each week
- Use Go profiler (pprof) to catch slow paths

### Risk 4: Test Coverage Gaps
**Mitigation**:
- Run coverage analysis: `go test -coverprofile=coverage.out ./...`
- Visualize coverage: `go tool cover -html=coverage.out`
- Aim for > 80% coverage on new code

---

## Success Metrics

### Before (Current State)
- ❌ `RetryMissingRequestsForConnection` exists
- ❌ O(N²) mesh scanning on every connection
- ❌ Duplicate identity mappings across 3+ data structures
- ❌ Silent request failures
- ❌ No request persistence
- ⚠️ Passes basic tests but not stress tests

### After (Target State)
- ✅ No retry logic needed - requests sent to connected devices only
- ✅ O(N) mesh operations
- ✅ Single identity manager
- ✅ All requests either sent or queued
- ✅ Request queue persists across restarts
- ✅ Passes 50-device, 1-hour stress test

---

## Timeline

| Week | Focus | Key Deliverables | Review Point |
|------|-------|------------------|--------------|
| Week 1 | Request Queue | `request_queue.go`, `request_queue_test.go`, updated `message_router.go` | Code review + unit tests pass |
| Week 2 | Identity Manager | `identity_manager.go`, refactored iOS/Android, removed duplicate maps | Code review + no test regressions |
| Week 3 | Mesh View State | Updated `mesh_view.go`, connection callbacks, simplified request logic | Code review + integration tests |
| Week 4 | Testing & Validation | Full test suite, benchmarks, stress tests, performance analysis | All tests pass, ready for hardware |

**Total Duration**: 4 weeks (assuming full-time work)
**Buffer**: +1 week for unexpected issues
**Total with Buffer**: 5 weeks

---

## Open Questions

1. **Hardware UUID Rotation**: iOS randomizes BLE addresses for privacy. How will we handle devices with changing hardware UUIDs?
   - **Answer Needed By**: End of Week 2
   - **Decision Owner**: Architecture review

2. **Request Queue Limits**: What's the maximum queue size per device before we start dropping old requests?
   - **Proposed**: 50 requests per device, FIFO eviction
   - **Answer Needed By**: Week 1

3. **Gossip vs. Direct Connection Priority**: Should we try to fetch photos via multi-hop routing or wait for direct connections?
   - **Current**: Wait for direct connections (simpler, matches BLE reality)
   - **Future**: Add multi-hop routing in v2
   - **Decision**: Stick with direct connections for now

4. **Identity Mapping TTL**: How long should we keep device ID mappings for devices we haven't seen?
   - **Proposed**: 30 days, configurable
   - **Answer Needed By**: Week 2

---

## Conclusion

The `RetryMissingRequestsForConnection` commit exposed fundamental timing issues in how the simulator handles the gap between gossip discovery and connection availability. This plan addresses those issues systematically:

1. **Week 1**: Stop sending to unreachable devices, queue for later
2. **Week 2**: Centralize identity management, eliminate duplicate state
3. **Week 3**: Teach mesh view about connection state, simplify request logic
4. **Week 4**: Validate fixes work at scale

After completion, the simulator will accurately model real BLE behavior and be ready for hardware testing. The architecture will be clean, testable, and maintainable.

**Next Step**: Review this plan with the team, get approval, create feature branch, start Week 1 implementation.






----- progress

Week 1 Complete! 🎉

  I've successfully completed Week 1: Request Queue System from the PLAN.md. Here's what was
  implemented:

  ✅ Completed:

  1. Created phone/request_queue.go - Full request queue infrastructure with:
    - Dual-index structure (by deviceID and hardwareUUID for fast lookups)
    - Enqueue with duplicate detection
    - Dequeue by connection or deviceID
    - Persistent storage with atomic writes
    - Hardware UUID updates when mappings are learned
  2. Created phone/request_queue_test.go - Comprehensive test suite covering:
    - Basic enqueue/dequeue operations
    - Duplicate request handling
    - Persistence across restarts
    - Hardware UUID updates
    - Multiple requests per device
    - Concurrent access (race detection)
    - Atomic file writes
    - 18 test cases total
  3. Integrated RequestQueue into MessageRouter:
    - Added requestQueue field and isConnectedFunc callback
    - Updated NewMessageRouter() to accept hardwareUUID and dataDir
    - Modified handleGossipMessage() to check connection state before sending
    - Queue requests for devices that aren't currently connected
    - Replaced RetryMissingRequestsForConnection() with FlushQueueForConnection()
    - New method processes queued requests when connections are established
    - Automatic retry with backoff (max 5 attempts)
    - Persistent queue state saves after flushing
  4. Updated iOS (iphone/iphone.go):
    - Pass hardwareUUID and dataDir to NewMessageRouter
    - Set isConnected callback using connManager.IsConnected()
    - Queue automatically flushes when connections are established
  5. Updated Android (android/android.go):
    - Pass hardwareUUID and dataDir to NewMessageRouter
    - Set isConnected callback using connManager.IsConnected()
    - Same queue behavior as iOS

  Key Improvements:

  ✅ No more sending to unreachable devices - Requests are queued if device isn't connected✅
  Automatic retry - Queued requests sent when connection becomes available✅ Persistence - Queue
  survives app restarts✅ Backoff logic - Failed requests retry up to 5 times before giving up✅
  Clean architecture - Backward compatible with RetryMissingRequestsForConnection() wrapper

  The code is ready to test! When you run go build or go run, the simulator will now:
  - Queue photo/profile requests for devices learned via gossip but not yet connected
  - Automatically flush the queue when those connections are established
  - Persist pending requests across restarts

