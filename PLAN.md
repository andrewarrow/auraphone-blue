# Auraphone Blue - Real BLE Readiness Plan

This document outlines the changes needed to make the simulator behave like real Bluetooth devices, so we're not surprised when moving to actual iOS/Android hardware.

---

## Issue #1: Bidirectional Sockets vs BLE's Asymmetric Roles

### Problem
Unix domain sockets are fully bidirectional - both devices can send/receive simultaneously over one connection. Real BLE is asymmetric and role-based:

- **Central → Peripheral**: Central writes to characteristics
- **Peripheral → Central**: Only via notifications (requires subscription)
- Cannot have both devices writing to each other's characteristics on same connection

### Current Code Issues
- `wire/wire.go:505-578` - `SendToDevice()` treats connections as bidirectional pipes
- `wire/wire.go:174` - Single `connections map[string]net.Conn` doesn't track which role
- `phone/connection_manager.go` has `sendViaCentralMode()` and `sendViaPeripheralMode()` but wire layer doesn't enforce this

### Real World Behavior
When Device A (Central) connects to Device B (Peripheral):
- A writes to B's characteristics ✅
- B sends notifications to A ✅
- B **cannot** write to A's characteristics ❌
- Need **second connection** (B as Central, A as Peripheral) for bidirectional comms

### Solution
**Phase 1: Track Connection Roles**
```go
// wire/wire.go
type Connection struct {
    conn       net.Conn
    role       ConnectionRole // "central" or "peripheral"
    remoteUUID string
}

type ConnectionRole string
const (
    RoleCentral    ConnectionRole = "central"    // We initiated, we can write
    RolePeripheral ConnectionRole = "peripheral" // They initiated, we can only notify
)

// Replace: connections map[string]net.Conn
// With: connections map[string]*Connection
```

**Phase 2: Enforce Role-Based Operations**
```go
// wire/wire.go
func (sw *Wire) WriteCharacteristic(targetUUID, serviceUUID, charUUID string, data []byte) error {
    conn := sw.connections[targetUUID]
    if conn == nil {
        return fmt.Errorf("not connected")
    }

    // ENFORCE: Can only write if we are Central
    if conn.role != RoleCentral {
        return fmt.Errorf("cannot write: we are peripheral, use NotifyCharacteristic instead")
    }

    // ... rest of implementation
}

func (sw *Wire) NotifyCharacteristic(targetUUID, serviceUUID, charUUID string, data []byte) error {
    conn := sw.connections[targetUUID]
    if conn == nil {
        return fmt.Errorf("not connected")
    }

    // ENFORCE: Can only notify if we are Peripheral (they are Central)
    if conn.role != RolePeripheral {
        return fmt.Errorf("cannot notify: we are central, use WriteCharacteristic instead")
    }

    // ... rest of implementation
}
```

**Phase 3: Support Dual Connections**
- When Device A discovers Device B, role negotiation determines who connects first
- Device A (Central) connects to Device B (Peripheral) → Connection #1
- Device B then creates reverse connection (B as Central, A as Peripheral) → Connection #2
- Both devices now have **two connections** to each other with opposite roles
- Track connections by `(remoteUUID, role)` tuple

**Phase 4: Update phone/connection_manager.go**
- Already has `sendViaCentralMode()` and `sendViaPeripheralMode()`
- Update to pass role parameter to wire layer
- ConnectionManager picks correct connection based on desired role

### Files to Modify
- `wire/wire.go` - Add Connection struct, role tracking, enforce role restrictions
- `phone/connection_manager.go` - Update send functions to specify role
- `iphone/iphone.go` - Ensure dual connections established
- `android/android.go` - Ensure dual connections established

### Testing
- Unit test: Central trying to notify → should fail
- Unit test: Peripheral trying to write → should fail
- Integration test: 2 devices establish dual connections, bidirectional comms works
- Hostile mode: Randomly close one connection, verify comms still works on other

---

## Issue #2: Characteristic Subscriptions Required for Notifications

### Problem
Current code sends notifications without checking if Central subscribed. Real BLE:
- Central must call `setNotifyValue(true)` to subscribe
- Writes `0x0001` to CCCD (Client Characteristic Configuration Descriptor)
- Peripherals **cannot notify** unsubscribed Centrals (OS drops silently)

### Current Code Issues
- `wire/wire.go:1002-1040` - `NotifyCharacteristic()` has no subscription check
- No CCCD descriptor (`00002902-0000-1000-8000-00805f9b34fb`) in GATT table
- `swift/cb_peripheral_manager.go` doesn't track subscriptions per Central
- `kotlin/bluetooth_gatt_server.go` doesn't track subscriptions per device

### Real World Behavior
```
Time  Central (iOS)           Peripheral (Android)
----  ------------------      ---------------------
T+0   Connect()               [accepts connection]
T+1   DiscoverServices()      [returns GATT table]
T+2   [sees characteristic]   [ready to notify]
T+3   [NO SUBSCRIPTION YET]   Notify() → ❌ DROPPED
T+4   SetNotifyValue(true)    [receives subscribe request]
T+5   [subscribed]            Notify() → ✅ DELIVERED
```

### Solution
**Phase 1: Add Subscription State Tracking**
```go
// wire/wire.go
type Wire struct {
    // ... existing fields

    // Track which Centrals are subscribed to which characteristics
    // Key: remoteUUID + serviceUUID + charUUID
    subscriptions map[string]bool
    subMutex      sync.RWMutex
}

func (sw *Wire) IsSubscribed(centralUUID, serviceUUID, charUUID string) bool {
    sw.subMutex.RLock()
    defer sw.subMutex.RUnlock()
    key := centralUUID + serviceUUID + charUUID
    return sw.subscriptions[key]
}
```

**Phase 2: Implement Subscribe/Unsubscribe Protocol**
```go
// wire/wire.go
func (sw *Wire) SubscribeCharacteristic(targetUUID, serviceUUID, charUUID string) error {
    msg := CharacteristicMessage{
        Operation:   "subscribe",
        ServiceUUID: serviceUUID,
        CharUUID:    charUUID,
        SenderUUID:  sw.localUUID,
    }
    return sw.sendCharacteristicMessage(targetUUID, &msg)
}

// In message handler (receiver side)
func (sw *Wire) handleSubscribeMessage(msg *CharacteristicMessage) {
    sw.subMutex.Lock()
    key := msg.SenderUUID + msg.ServiceUUID + msg.CharUUID
    sw.subscriptions[key] = true
    sw.subMutex.Unlock()

    logger.Debug("Central %s subscribed to %s", msg.SenderUUID[:8], msg.CharUUID[:8])

    // Notify delegate/callback
    if sw.subscriptionCallback != nil {
        sw.subscriptionCallback(msg.SenderUUID, msg.ServiceUUID, msg.CharUUID, true)
    }
}
```

**Phase 3: Enforce Subscription Requirement**
```go
// wire/wire.go
func (sw *Wire) NotifyCharacteristic(targetUUID, serviceUUID, charUUID string, data []byte) error {
    // Check subscription state
    if !sw.IsSubscribed(targetUUID, serviceUUID, charUUID) {
        logger.Warn("Cannot notify %s: not subscribed to %s", targetUUID[:8], charUUID[:8])
        return fmt.Errorf("central not subscribed to characteristic")
    }

    // ... rest of notification logic
}
```

**Phase 4: Add CCCD Descriptors to GATT Table**
```go
// iphone/iphone.go and android/android.go
func setupBLE() {
    gattTable := &wire.GATTTable{
        Services: []wire.GATTService{
            {
                UUID: phone.AuraServiceUUID,
                Type: "primary",
                Characteristics: []wire.GATTCharacteristic{
                    {
                        UUID: phone.AuraProtocolCharUUID,
                        Properties: []string{"read", "write", "notify"},
                        Descriptors: []wire.GATTDescriptor{
                            {
                                UUID: "00002902-0000-1000-8000-00805f9b34fb", // CCCD
                                Type: "CCCD",
                            },
                        },
                    },
                    // ... other characteristics with CCCD
                },
            },
        },
    }
}
```

**Phase 5: Update Central Code to Subscribe**
```go
// swift/cb_peripheral.go (iOS Central)
func (p *CBPeripheral) DiscoverCharacteristics() {
    // ... discover characteristics

    // After discovery, automatically subscribe to notify-enabled characteristics
    for _, char := range service.Characteristics {
        if char.Properties & CBCharacteristicPropertyNotify != 0 {
            p.SetNotifyValue(true, char)
        }
    }
}

// kotlin/bluetooth_gatt.go (Android Central)
func (g *BluetoothGatt) DiscoverServices() {
    // ... discover services

    // After discovery, subscribe to notify-enabled characteristics
    for _, service := range g.services {
        for _, char := range service.Characteristics {
            if char.Properties & PROPERTY_NOTIFY != 0 {
                g.SetCharacteristicNotification(char, true)
            }
        }
    }
}
```

### Files to Modify
- `wire/wire.go` - Add subscription tracking, enforce in NotifyCharacteristic()
- `wire/wire.go` - Already has SubscribeCharacteristic(), add handler for subscribe messages
- `swift/cb_peripheral.go` - Auto-subscribe after service discovery
- `kotlin/bluetooth_gatt.go` - Auto-subscribe after service discovery
- `iphone/iphone.go` - Add CCCD descriptors to GATT table
- `android/android.go` - Add CCCD descriptors to GATT table

### Testing
- Unit test: Notify before subscribe → should fail/drop
- Unit test: Subscribe, then notify → should succeed
- Unit test: Unsubscribe, then notify → should fail/drop
- Integration test: Central subscribes, receives 100 notifications, all delivered
- Race test: Peripheral sends notification while Central is subscribing

---

## Issue #3: Photo Transfer Resilience Against Connection Loss

### Problem
`PhotoTransferCoordinator` assumes stable connections. Real BLE connections are fragile:
- **iOS background**: Connections drop within 10 seconds
- **Android Doze**: Connections killed after 1 minute screen-off
- **Radio interference**: Microwaves, Wi-Fi, USB 3.0 cause dropouts
- **Distance**: Phone moves pocket → table → connection lost
- **OS limits**: Kill connections when >7-10 active

Current simulation: 98% uptime (2% random disconnect every 5s)
Real world: **30-60% uptime** in typical conditions

### Current Code Issues
- `phone/photo_transfer_coordinator.go` - No resume capability
- `phone/photo_handler.go` - No timeout detection
- Transfer fails mid-stream → `photoSendInProgress` stuck true
- No cleanup on disconnect → blocks future transfers
- No exponential backoff → hammers broken connections

### Real World Behavior
```
Time  Action                          Connection   Transfer State
----  ----------------------------    -----------  ---------------
T+0   Start photo transfer (1MB)      Connected    0% complete
T+2   Send 500KB...                   Connected    50% complete
T+3   [User puts phone in pocket]     DROPPED      50% complete (stuck)
T+4   Reconnect automatically         Connected    50% complete (stuck)
T+5   Try to send another photo       Connected    ❌ Blocked (photoSendInProgress=true)
```

### Solution
**Phase 1: Chunked Transfer Protocol with Resume**
```go
// proto/handshake.proto
message PhotoChunkMessage {
  string sender_device_id = 1;
  string target_device_id = 2;
  bytes photo_hash = 3;          // SHA-256
  int32 chunk_index = 4;         // 0-based chunk number
  int32 total_chunks = 5;        // Total number of chunks
  bytes chunk_data = 6;          // Actual chunk bytes (e.g., 4KB per chunk)
  int64 timestamp = 7;
}

message PhotoChunkAck {
  string receiver_device_id = 1;
  bytes photo_hash = 2;
  int32 chunk_index = 3;         // Acknowledge this chunk received
  repeated int32 missing_chunks = 4; // Request these chunks (for resume)
}
```

**Phase 2: Add Resume State Tracking**
```go
// phone/photo_transfer_coordinator.go
type TransferState struct {
    DeviceID         string
    PhotoHash        string
    TotalChunks      int32
    ReceivedChunks   map[int32]bool  // Track which chunks we have
    LastActivityTime time.Time
    InProgress       bool
}

type PhotoTransferCoordinator struct {
    // ... existing fields

    activeTransfers map[string]*TransferState // Key: deviceID + photoHash
    transferMutex   sync.RWMutex

    chunkSize       int // Default: 4096 bytes (fits in 5 BLE packets at 185 MTU)
}

func (ptc *PhotoTransferCoordinator) ResumeTransfer(deviceID, photoHash string) *TransferState {
    key := deviceID + photoHash
    ptc.transferMutex.RLock()
    state := ptc.activeTransfers[key]
    ptc.transferMutex.RUnlock()

    if state == nil {
        // New transfer
        return ptc.StartTransfer(deviceID, photoHash)
    }

    // Resume existing transfer
    return state
}
```

**Phase 3: Timeout Detection & Cleanup**
```go
// phone/photo_transfer_coordinator.go
func (ptc *PhotoTransferCoordinator) StartTimeoutMonitor() {
    go func() {
        ticker := time.NewTicker(10 * time.Second)
        defer ticker.Stop()

        for {
            select {
            case <-ticker.C:
                ptc.cleanupStalledTransfers()
            }
        }
    }()
}

func (ptc *PhotoTransferCoordinator) cleanupStalledTransfers() {
    ptc.transferMutex.Lock()
    defer ptc.transferMutex.Unlock()

    now := time.Now()
    for key, state := range ptc.activeTransfers {
        // If no activity for 30 seconds, mark as stalled
        if now.Sub(state.LastActivityTime) > 30*time.Second && state.InProgress {
            logger.Warn("Transfer stalled: %s -> %s (%d/%d chunks)",
                state.DeviceID, phone.TruncateHash(state.PhotoHash, 8),
                len(state.ReceivedChunks), state.TotalChunks)

            state.InProgress = false // Allow retry

            // Keep state in map for resume (don't delete)
        }
    }
}
```

**Phase 4: Disconnect Handler Cleanup**
```go
// iphone/iphone.go and android/android.go
func (ip *iPhone) handleDisconnect(remoteUUID string) {
    // Clean up transfer state for this device
    deviceID := ip.peripheralToDeviceID[remoteUUID]
    if deviceID != "" {
        // Mark all transfers to/from this device as inactive
        ip.photoCoordinator.PauseTransfers(deviceID)
    }

    // Remove from connected peripherals
    delete(ip.connectedPeripherals, remoteUUID)
    delete(ip.peripheralToDeviceID, remoteUUID)
}

// phone/photo_transfer_coordinator.go
func (ptc *PhotoTransferCoordinator) PauseTransfers(deviceID string) {
    ptc.transferMutex.Lock()
    defer ptc.transferMutex.Unlock()

    for key, state := range ptc.activeTransfers {
        if state.DeviceID == deviceID {
            state.InProgress = false
            logger.Info("Paused transfer to %s due to disconnect (can resume)", deviceID)
        }
    }
}
```

**Phase 5: Exponential Backoff for Retries**
```go
// phone/photo_handler.go
type RetryState struct {
    attempts      int
    nextRetryTime time.Time
}

var photoRetryState = make(map[string]*RetryState) // Key: deviceID + photoHash

func (ph *PhotoHandler) RequestPhotoWithBackoff(deviceID, photoHash string) {
    key := deviceID + photoHash
    retry := photoRetryState[key]

    if retry == nil {
        retry = &RetryState{attempts: 0, nextRetryTime: time.Now()}
        photoRetryState[key] = retry
    }

    // Check if we should retry now
    if time.Now().Before(retry.nextRetryTime) {
        logger.Debug("Backoff: not retrying %s until %v", key[:16], retry.nextRetryTime)
        return
    }

    // Send request
    ph.RequestPhoto(deviceID, photoHash)

    // Update backoff (exponential: 1s, 2s, 4s, 8s, max 60s)
    retry.attempts++
    backoffDuration := time.Duration(math.Min(float64(1<<retry.attempts), 60)) * time.Second
    retry.nextRetryTime = time.Now().Add(backoffDuration)
}
```

**Phase 6: Multi-Hop Transfer via Gossip (Future Enhancement)**
```go
// phone/mesh_view.go
func (mv *MeshView) FindPhotoSource(photoHash string) []string {
    // Return list of deviceIDs that have this photo (from gossip)
    var sources []string
    for deviceID, device := range mv.devices {
        if device.PhotoHash == photoHash && device.HavePhoto {
            sources = append(sources, deviceID)
        }
    }
    return sources
}

// phone/photo_handler.go
func (ph *PhotoHandler) RequestPhotoMultiHop(photoHash string) {
    // Try to get photo from ANY device that has it (not just original owner)
    sources := ph.meshView.FindPhotoSource(photoHash)

    for _, sourceDeviceID := range sources {
        if ph.isConnected(sourceDeviceID) {
            ph.RequestPhoto(sourceDeviceID, photoHash)
            return
        }
    }

    logger.Warn("Photo %s not available from any connected source", photoHash[:8])
}
```

### Files to Modify
- `proto/handshake.proto` - Add PhotoChunkMessage and PhotoChunkAck
- `phone/photo_transfer_coordinator.go` - Add chunked transfer, resume state, timeout monitor
- `phone/photo_handler.go` - Add exponential backoff, handle chunk messages
- `iphone/iphone.go` - Add disconnect cleanup
- `android/android.go` - Add disconnect cleanup
- `wire/wire.go` - Ensure disconnect callback is reliable

### Testing
- Unit test: Transfer 1MB photo in 4KB chunks, disconnect at 50%, reconnect, resume
- Unit test: Timeout detection marks transfer as stalled after 30s
- Hostile mode: 50% connection uptime, verify photos eventually transfer
- Stress test: 10 devices, 50 photos, random disconnects, verify all photos propagate

---

## Issue #4: MTU Negotiation Timing

### Problem
Current simulation assumes MTU is negotiated at connection time. Real BLE:
- **iOS**: MTU negotiation happens **after** service discovery (500ms-2s delay)
- **Android**: Must explicitly call `requestMtu()` - not automatic
- **Default**: MTU is 23 bytes until negotiation completes
- Sending large data before MTU negotiation = 8x overhead (185 bytes fragmented into 23-byte chunks)

### Current Code Issues
- `wire/simulation.go:217-233` - `NegotiatedMTU()` exists but not used in connection flow
- `wire/wire.go:172` - MTU set to `DefaultMTU` (185) immediately at connection
- No MTU negotiation delay simulation
- No fallback to 23-byte default

### Real World Behavior
```
Time  iOS Central                   Android Peripheral         MTU
----  -------------------------     ---------------------      ----
T+0   Connect()                     [accepts]                  23 bytes (default)
T+1   DiscoverServices()            [returns GATT table]       23 bytes
T+2   [service discovery done]      [waiting]                  23 bytes
T+3   [OS negotiates MTU]           [OS responds with max]     23 bytes (negotiating...)
T+4   [MTU negotiation done]        [MTU negotiation done]     185 bytes ✅
T+5   Write(1KB data)               [receives efficiently]     185 bytes
```

If you write at T+2, data is fragmented to 23 bytes (43 packets instead of 6 packets).

### Solution
**Phase 1: Add MTU Negotiation State**
```go
// wire/wire.go
type Connection struct {
    conn               net.Conn
    role               ConnectionRole
    remoteUUID         string
    mtu                int           // Current MTU (starts at 23)
    mtuNegotiated      bool          // Has MTU negotiation completed?
    mtuNegotiationTime time.Time     // When negotiation completed
}

func (sw *Wire) Connect(targetUUID string) error {
    // ... existing connection logic

    conn := &Connection{
        conn:       actualConn,
        role:       RoleCentral,
        remoteUUID: targetUUID,
        mtu:        23, // Start with BLE minimum
        mtuNegotiated: false,
    }

    sw.connections[targetUUID] = conn

    // Start MTU negotiation in background (after service discovery)
    go sw.negotiateMTU(targetUUID)

    return nil
}
```

**Phase 2: Simulate MTU Negotiation Delay**
```go
// wire/wire.go
func (sw *Wire) negotiateMTU(targetUUID string) {
    // Simulate service discovery delay (iOS: 500ms-2s, Android: requires explicit call)
    delay := sw.simulator.ServiceDiscoveryDelay()
    time.Sleep(delay)

    // Negotiate MTU
    conn := sw.connections[targetUUID]
    if conn == nil {
        return // Connection closed during negotiation
    }

    // Simulate MTU negotiation (both devices propose max, select minimum)
    localMaxMTU := sw.simulator.config.DefaultMTU
    remoteMaxMTU := localMaxMTU // In real BLE, would read from remote device capabilities

    negotiatedMTU := sw.simulator.NegotiatedMTU(localMaxMTU, remoteMaxMTU)

    conn.mtu = negotiatedMTU
    conn.mtuNegotiated = true
    conn.mtuNegotiationTime = time.Now()

    logger.Info(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
        "✅ MTU negotiated: %d bytes (was 23 bytes) with %s", negotiatedMTU, targetUUID[:8])
}
```

**Phase 3: Use Connection-Specific MTU for Fragmentation**
```go
// wire/wire.go
func (sw *Wire) SendToDevice(targetUUID string, data []byte) error {
    conn := sw.connections[targetUUID]
    if conn == nil {
        return fmt.Errorf("not connected")
    }

    // Use connection-specific MTU (may be 23 bytes if negotiation not done yet)
    fragments := sw.simulator.FragmentData(data, conn.mtu)

    if !conn.mtuNegotiated && len(fragments) > 10 {
        logger.Warn(fmt.Sprintf("%s %s", sw.localUUID[:8], sw.platform),
            "⚠️  Sending %d bytes in %d fragments (MTU: %d) - negotiation not complete, high overhead",
            len(data), len(fragments), conn.mtu)
    }

    // ... rest of send logic
}
```

**Phase 4: Android Explicit MTU Request**
```go
// kotlin/bluetooth_gatt.go
func (g *BluetoothGatt) DiscoverServices() bool {
    // ... existing service discovery

    // After service discovery, request larger MTU (Android-specific)
    go func() {
        time.Sleep(100 * time.Millisecond) // Small delay after discovery
        g.RequestMtu(247) // Android max reliable MTU (247 bytes)
    }()

    return true
}

func (g *BluetoothGatt) RequestMtu(mtu int) bool {
    // Send MTU request to peripheral
    // In simulator, this triggers MTU negotiation in wire layer
    g.wire.RequestMTUNegotiation(g.device.Address, mtu)
    return true
}
```

**Phase 5: iOS Automatic MTU Negotiation**
```go
// swift/cb_peripheral.go
func (p *CBPeripheral) DiscoverServices(serviceUUIDs []string) {
    // ... existing service discovery

    // iOS automatically negotiates MTU after service discovery (no explicit call needed)
    // Simulate this in wire layer
    go func() {
        time.Sleep(500 * time.Millisecond) // iOS MTU negotiation takes ~500ms-2s
        p.wire.RequestMTUNegotiation(p.UUID, 185) // iOS typical MTU
    }()
}
```

### Files to Modify
- `wire/wire.go` - Add Connection struct with per-connection MTU, negotiateMTU()
- `wire/simulation.go` - Add MTU negotiation delay config
- `swift/cb_peripheral.go` - Trigger automatic MTU negotiation after service discovery
- `kotlin/bluetooth_gatt.go` - Add RequestMtu() call after service discovery

### Testing
- Unit test: Send 1KB data immediately after connect → 43 fragments (23-byte MTU)
- Unit test: Wait for MTU negotiation → 6 fragments (185-byte MTU)
- Integration test: Measure throughput before/after MTU negotiation
- Stress test: Send photo during MTU negotiation, verify no data corruption

---

## Implementation Priority

### Phase 1 (Critical - Will Break Immediately)
1. **Issue #1**: Bidirectional communication fix
   - Must work before testing on real devices
   - Estimated: 2-3 days

2. **Issue #2**: Subscription requirement
   - 50% of notifications will be lost without this
   - Estimated: 1-2 days

### Phase 2 (High Priority - Will Fail in Real Conditions)
3. **Issue #3**: Photo transfer resilience
   - Essential for real-world reliability
   - Estimated: 3-4 days

### Phase 3 (Important - Performance Issue)
4. **Issue #4**: MTU negotiation timing
   - Works but with poor performance if skipped
   - Estimated: 1 day

### Total Estimated Effort
**7-10 days** to implement all fixes

---

## Hostile Mode Testing Config

Create aggressive config to expose these issues before real hardware:

```go
// wire/simulation.go
func HostileSimulationConfig() *SimulationConfig {
    cfg := DefaultSimulationConfig()

    // Aggressive connection instability
    cfg.ConnectionFailureRate = 0.05        // 5% connection failures
    cfg.RandomDisconnectRate = 0.30         // 30% disconnect chance per check (every 5s)
    cfg.ConnectionMonitorInterval = 5000    // Check every 5 seconds

    // High packet loss
    cfg.PacketLossRate = 0.10               // 10% packet loss
    cfg.MaxRetries = 5                      // More retries needed

    // High notification drops
    cfg.NotificationDropRate = 0.05         // 5% notifications dropped

    // Realistic MTU negotiation delay
    cfg.MinServiceDiscoveryDelay = 500      // 500ms-2s delay
    cfg.MaxServiceDiscoveryDelay = 2000

    return cfg
}
```

Use this config during development to shake out all race conditions and failure modes.

---

## Testing Checklist

Before declaring "real BLE ready":

### Bidirectional Communication
- [ ] Central can write to Peripheral ✅
- [ ] Peripheral can notify Central ✅
- [ ] Central cannot notify Peripheral ❌ (should fail)
- [ ] Peripheral cannot write to Central ❌ (should fail)
- [ ] Dual connections work (both directions simultaneously) ✅

### Subscriptions
- [ ] Notification before subscribe fails/drops ✅
- [ ] Notification after subscribe succeeds ✅
- [ ] Unsubscribe stops notifications ✅
- [ ] Multiple Centrals can subscribe independently ✅
- [ ] CCCD descriptors present in GATT table ✅

### Photo Transfer Resilience
- [ ] 1MB photo transfers with 0% packet loss ✅
- [ ] 1MB photo transfers with 10% packet loss ✅
- [ ] Transfer disconnects at 50%, reconnects, resumes ✅
- [ ] Transfer times out after 30s inactivity ✅
- [ ] Multiple photos queue properly ✅
- [ ] Exponential backoff prevents hammering ✅

### MTU Negotiation
- [ ] Data sent before negotiation uses 23-byte MTU ✅
- [ ] Data sent after negotiation uses 185-byte MTU ✅
- [ ] iOS auto-negotiates MTU after service discovery ✅
- [ ] Android RequestMtu() triggers negotiation ✅
- [ ] No data corruption during MTU switch ✅

### Hostile Mode
- [ ] 10 devices, 50 photos, 30% disconnect rate → all photos eventually transfer ✅
- [ ] Gossip protocol survives frequent disconnects ✅
- [ ] No deadlocks or stuck states ✅
- [ ] Memory usage stays bounded (no leaks) ✅

---

## Notes

- All changes maintain backward compatibility with existing simulator tests
- Hostile mode config is opt-in (doesn't break existing code)
- Real BLE devices will still have additional quirks (Android manufacturer differences, iOS background modes, etc.)
- This plan gets us **90% ready** for real hardware - expect 10% of edge cases to still surprise us
