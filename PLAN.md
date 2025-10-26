# Gossip Protocol Integration Plan

## Executive Summary

**Current State**: The gossip protocol infrastructure exists (`phone/mesh_view.go`) but is completely unused. Devices use direct handshakes and photo transfers only.

**Goal**: Fully integrate gossip protocol to replace handshakes, enable mesh networking, and distribute both photos and profiles efficiently across N devices without full mesh connectivity.

**Key Insight**: Handshakes and gossip serve the same purpose - exchanging device state (deviceID, photoHash, firstName, profileVersion). We should **unify them into a single gossip protocol** that runs periodically and eliminates the need for separate handshake messages.

---

## Architecture Changes

### Before (Current)
```
Device A ←--handshake--→ Device B ←--handshake--→ Device C
   ↓                         ↓                         ↓
Photo transfer         Photo transfer           Photo transfer
   ↓                         ↓                         ↓
Profile msg            Profile msg              Profile msg
```
- Every device connects to every other device (O(N²) connections)
- Handshakes sent on connection + every 5 seconds if stale
- Photos transferred directly between connected devices
- Profile messages sent separately after handshake

### After (Gossip-Based)
```
Device A ←--gossip--→ Device B ←--gossip--→ Device C ←--gossip--→ Device D
   ↓                     ↓                     ↓                      ↓
3 neighbors         3 neighbors           3 neighbors            3 neighbors
(deterministic)     (deterministic)       (deterministic)        (deterministic)
```
- Each device connects to **3 neighbors only** (O(log N) connections)
- Single unified gossip message contains: deviceID, photoHash, firstName, profileVersion, profileSummary
- Photos requested on-demand when gossip reveals new hashes
- Profile details (Instagram, TikTok, etc.) fetched on-demand when profileVersion changes
- Multi-hop information propagation (learn about Device D through Device B)

---

## Protocol Design

### New Unified Gossip Protocol

**Gossip Message** (sent every 5 seconds to neighbors):
```protobuf
message GossipMessage {
  string sender_device_id = 1;
  int64 timestamp = 2;
  repeated DeviceState mesh_view = 3;
  int32 gossip_round = 4;
}

message DeviceState {
  string device_id = 1;
  bytes photo_hash = 2;              // SHA-256 (32 bytes)
  int64 last_seen_timestamp = 3;
  string first_name = 4;
  int32 profile_version = 5;         // Increments on ANY profile change
  bytes profile_summary_hash = 6;    // SHA-256 of (instagram+tiktok+linkedin+...) for change detection
}
```

**When Gossip Arrives**:
1. Merge into local `MeshView`
2. Detect new/changed devices
3. Request missing photos (if `photo_hash` is new)
4. Request full profile (if `profile_version` > cached version OR `profile_summary_hash` differs)

**Request Messages** (sent on-demand):
```protobuf
message PhotoRequestMessage {
  string requester_device_id = 1;
  string target_device_id = 2;      // Whose photo we want
  bytes photo_hash = 3;             // Specific hash we want
}

message ProfileRequestMessage {
  string requester_device_id = 1;
  string target_device_id = 2;      // Whose profile we want
  int32 expected_version = 3;       // Version we're requesting
}
```

**Response Messages**:
- Photos: Use existing photo characteristic with chunked transfer
- Profiles: Use existing profile characteristic with `ProfileMessage`

---

## Dual-Role (Central + Peripheral) Strategy

### Challenge
Both iOS and Android need to:
1. **Act as Central**: Connect to neighbors with lower hardware UUID
2. **Act as Peripheral**: Accept connections from neighbors with higher hardware UUID
3. Handle gossip/photo/profile messages in **both directions simultaneously**

### Solution: Connection-Oriented State + Role-Agnostic Handlers

#### Connection State Machine
```
DISCONNECTED → CONNECTING → CONNECTED → DISCONNECTING → DISCONNECTED
                    ↓
                FAILED (retry after 5s)
```

**Track connections by hardware UUID**:
- **Central mode**: `connectedPeripherals[remoteUUID]` (iOS) / `connectedGatts[remoteUUID]` (Android)
- **Peripheral mode**: `connectedCentrals[remoteUUID]` (both platforms)

**Key Rule**: Hardware UUID is used for connection management, deviceID is used for application logic (photos, profiles, gossip).

#### Message Handlers (Role-Agnostic)

Both platforms need **two parallel code paths**:

**iOS**:
1. **Central-side handlers**: `OnCharacteristicChanged(peripheral, characteristic)` - receive from remote GATT servers
2. **Peripheral-side handlers**: `OnWriteRequest(central, characteristic, data)` - receive from remote GATT clients

**Android**:
1. **Central-side handlers**: `OnCharacteristicChanged(gatt, characteristic)` - receive from remote GATT servers
2. **Peripheral-side handlers**: `OnCharacteristicWriteRequest(device, characteristic, data)` - receive from remote GATT clients

**Critical**: Both paths must call the **same underlying logic**:
- `handleGossipMessage(senderUUID, data)` - merge gossip, update mesh view
- `handlePhotoChunk(senderUUID, data)` - accumulate photo chunks
- `handleProfileMessage(senderUUID, data)` - update cached profile

#### Sending Messages (Role-Aware)

**To devices we connected to (we are Central)**:
- iOS: `peripheral.WriteValue(data, characteristic, .withResponse)`
- Android: `characteristic.Value = data; gatt.WriteCharacteristic(characteristic)`

**To devices that connected to us (we are Peripheral)**:
- iOS: `peripheralManager.UpdateValue(data, characteristic, [central])`
- Android: `gattServer.NotifyCharacteristicChanged(device, characteristic, confirm)`

**Abstraction**: Create `sendToDevice(remoteUUID, characteristicUUID, data)` helper that checks:
```go
if remoteUUID in connectedPeripherals:
    send via central mode (write)
elif remoteUUID in connectedCentrals:
    send via peripheral mode (notify)
else:
    error: not connected
```

---

## Implementation Steps

### Phase 1: Protocol Updates (Protobuf)

**Step 1.1**: Update `proto/handshake.proto` ✅ COMPLETE (commit f6c65bb)
- [x] `DevicePhotoState` renamed to `DeviceState`
- [x] Add `profile_version` field to `DeviceState`
- [x] Add `profile_summary_hash` field to `DeviceState` (optional, for optimization)
- [x] Add `ProfileRequestMessage` (requester_device_id, target_device_id, expected_version)
- [x] Keep `ProfileMessage` as-is (response message)
- [x] `GossipMessage` already exists
- [x] `PhotoRequestMessage` already exists

**Step 1.2**: Regenerate protobuf ✅ COMPLETE (commit f6c65bb)
```bash
cd proto
protoc --go_out=. --go_opt=paths=source_relative handshake.proto
```

---

### Phase 2: Shared Infrastructure (phone/ package) ✅ COMPLETE

**Step 2.1**: Enhance `MeshView` (`phone/mesh_view.go`) ✅ COMPLETE
- [x] Add `ProfileVersion int32` to `MeshDeviceState`
- [x] Add `ProfileSummaryHash string` to `MeshDeviceState`
- [x] Add `HaveProfile bool` to `MeshDeviceState`
- [x] Add `ProfileRequestSent bool` to `MeshDeviceState`
- [x] Update `UpdateDevice()` to accept `profileVersion` and `profileSummaryHash`
- [x] Update `MergeGossip()` to detect profile changes (version diff or hash diff)
- [x] Add `GetMissingProfiles() []*MeshDeviceState` - returns devices whose profiles we need
- [x] Add `MarkProfileRequested(deviceID string)`
- [x] Add `MarkProfileReceived(deviceID string, version int32)`
- [x] Update `BuildGossipMessage()` to include profile_version and profile_summary_hash

**Step 2.2**: Create `phone/message_router.go` (NEW FILE) ✅ COMPLETE
- [x] Created MessageRouter struct with mesh view, cache manager, photo coordinator
- [x] Implemented HandleProtocolMessage() for routing gossip/handshake/request messages
- [x] Implemented handleGossipMessage() with photo/profile need detection
- [x] Implemented handlePhotoRequest() and handleProfileRequest()
- [x] Added handleLegacyHandshake() for backwards compatibility
- [x] Set up callback system for onPhotoNeeded and onProfileNeeded

**Step 2.3**: Create `phone/connection_manager.go` (NEW FILE) ✅ COMPLETE
- [x] Created ConnectionManager struct for dual-role tracking
- [x] Implemented RegisterCentralConnection/UnregisterCentralConnection
- [x] Implemented RegisterPeripheralConnection/UnregisterPeripheralConnection
- [x] Implemented SendToDevice() with automatic role detection
- [x] Implemented GetAllConnectedUUIDs() with deduplication
- [x] Implemented IsConnected(), IsConnectedAsCentral(), IsConnectedAsPeripheral()
- [x] Added GetCentralConnection() and GetConnectionCount() helpers

---

### Phase 3: iOS Integration

**Step 3.1**: Add new fields to `iPhone` struct (`iphone/iphone.go`)
```go
type iPhone struct {
    // ... existing fields ...

    meshView         *phone.MeshView
    messageRouter    *phone.MessageRouter
    connManager      *phone.ConnectionManager

    // Gossip timing
    lastGossipTime   time.Time
    gossipInterval   time.Duration
}
```

**Step 3.2**: Initialize in `NewIPhone()`
```go
func NewIPhone(hardwareUUID string) *iPhone {
    // ... existing initialization ...

    ip.meshView = phone.NewMeshView(ip.deviceID, ip.hardwareUUID, dataDir, ip.cacheManager)

    ip.messageRouter = &phone.MessageRouter{
        meshView:         ip.meshView,
        cacheManager:     ip.cacheManager,
        photoCoordinator: ip.photoCoordinator,
        onPhotoNeeded: func(deviceID, photoHash string) {
            ip.requestPhoto(deviceID, photoHash)
        },
        onProfileNeeded: func(deviceID string, version int32) {
            ip.requestProfile(deviceID, version)
        },
    }

    ip.connManager = &phone.ConnectionManager{
        hardwareUUID: hardwareUUID,
        centralConnections:    make(map[string]interface{}),
        peripheralConnections: make(map[string]bool),
        sendViaCentral: func(remoteUUID, charUUID string, data []byte) error {
            return ip.sendViaCentralMode(remoteUUID, charUUID, data)
        },
        sendViaPeripheral: func(remoteUUID, charUUID string, data []byte) error {
            return ip.sendViaPeripheralMode(remoteUUID, charUUID, data)
        },
    }

    ip.gossipInterval = 5 * time.Second

    return ip
}
```

**Step 3.3**: Update connection handlers to register with `ConnectionManager`

**In `DidConnectPeripheral()`** (Central mode):
```go
func (ip *iPhone) DidConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral) {
    // ... existing code ...

    // Register connection
    ip.connManager.RegisterCentralConnection(peripheral.UUID, peripheral)
}
```

**In `DidDisconnectPeripheral()`** (Central mode):
```go
func (ip *iPhone) DidDisconnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, err error) {
    // ... existing code ...

    // Unregister connection
    ip.connManager.UnregisterCentralConnection(peripheral.UUID)
}
```

**In `CentralDidConnect()`** (Peripheral mode):
```go
func (d *iPhonePeripheralDelegate) CentralDidConnect(peripheralManager *swift.CBPeripheralManager, central swift.CBCentral) {
    // ... existing code ...

    // Register connection
    d.iphone.connManager.RegisterPeripheralConnection(central.UUID)
}
```

**In `CentralDidDisconnect()`** (Peripheral mode):
```go
func (d *iPhonePeripheralDelegate) CentralDidDisconnect(peripheralManager *swift.CBPeripheralManager, central swift.CBCentral) {
    // ... existing code ...

    // Unregister connection
    d.iphone.connManager.UnregisterPeripheralConnection(central.UUID)
}
```

**Step 3.4**: Replace `handleHandshakeMessage()` with unified handler

**In `DidUpdateValueForCharacteristic()` (Central mode - receiving notifications)**:
```go
func (ip *iPhone) DidUpdateValueForCharacteristic(peripheral *swift.CBPeripheral, characteristic *swift.CBCharacteristic, err error) {
    // ... existing code ...

    if characteristic.UUID == phone.AuraProtocolCharUUID {
        // Protocol characteristic handles gossip (and legacy handshakes)
        if err := ip.messageRouter.HandleProtocolMessage(peripheral.UUID, characteristic.Value); err != nil {
            logger.Error(prefix, "❌ Failed to handle protocol message: %v", err)
        }
    } else if characteristic.UUID == phone.AuraPhotoCharUUID {
        ip.handlePhotoMessage(peripheral.UUID, characteristic.Value)
    } else if characteristic.UUID == phone.AuraProfileCharUUID {
        ip.handleProfileMessage(peripheral.UUID, characteristic.Value)
    }
}
```

**In `DidReceiveWriteRequests()` (Peripheral mode - receiving writes)**:
```go
func (d *iPhonePeripheralDelegate) DidReceiveWriteRequests(peripheralManager *swift.CBPeripheralManager, requests []*swift.CBATTRequest) {
    for _, request := range requests {
        switch request.Characteristic.UUID {
        case phone.AuraProtocolCharUUID:
            // Protocol characteristic handles gossip (and legacy handshakes)
            if err := d.iphone.messageRouter.HandleProtocolMessage(request.Central.UUID, request.Value); err != nil {
                logger.Error(prefix, "❌ Failed to handle protocol message: %v", err)
            }
            peripheralManager.RespondToRequest(request, swift.CBATTErrorSuccess)

        case phone.AuraPhotoCharUUID:
            d.iphone.handlePhotoMessage(request.Central.UUID, request.Value)
            peripheralManager.RespondToRequest(request, swift.CBATTErrorSuccess)

        case phone.AuraProfileCharUUID:
            d.iphone.handleProfileMessage(request.Central.UUID, request.Value)
            peripheralManager.RespondToRequest(request, swift.CBATTErrorSuccess)
        }
    }
}
```

**Step 3.5**: Add gossip sending goroutine

**In `Start()`**:
```go
func (ip *iPhone) Start() {
    // ... existing code ...

    // Start gossip broadcast goroutine
    go ip.gossipLoop()
}

func (ip *iPhone) gossipLoop() {
    ticker := time.NewTicker(ip.gossipInterval)
    defer ticker.Stop()

    for range ticker.C {
        ip.sendGossipToNeighbors()
    }
}

func (ip *iPhone) sendGossipToNeighbors() {
    prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])

    // Select neighbors using mesh view
    neighbors := ip.meshView.SelectRandomNeighbors()

    // Get all currently connected devices
    connectedUUIDs := ip.connManager.GetAllConnectedUUIDs()

    // Map deviceIDs to hardware UUIDs
    ip.mu.RLock()
    deviceIDToUUID := make(map[string]string)
    for uuid, deviceID := range ip.peripheralToDeviceID {
        deviceIDToUUID[deviceID] = uuid
    }
    ip.mu.RUnlock()

    // Calculate profile summary hash
    profileSummaryHash := ip.calculateProfileSummaryHash()

    // Build gossip message
    gossip := ip.meshView.BuildGossipMessage(ip.photoHash, ip.localProfile.FirstName)

    // Add our profile version to our own entry in the mesh view
    for _, deviceState := range gossip.MeshView {
        if deviceState.DeviceId == ip.deviceID {
            deviceState.ProfileVersion = ip.localProfile.ProfileVersion
            deviceState.ProfileSummaryHash = profileSummaryHash
            break
        }
    }

    data, err := proto.Marshal(gossip)
    if err != nil {
        logger.Error(prefix, "❌ Failed to marshal gossip: %v", err)
        return
    }

    logger.Debug(prefix, "📢 Broadcasting gossip (round %d) to %d neighbors", gossip.GossipRound, len(neighbors))

    // Send to neighbors that are currently connected
    sentCount := 0
    for _, neighborDeviceID := range neighbors {
        neighborUUID, exists := deviceIDToUUID[neighborDeviceID]
        if !exists {
            continue // Don't know hardware UUID yet
        }

        if !ip.connManager.IsConnected(neighborUUID) {
            continue // Not currently connected
        }

        if err := ip.connManager.SendToDevice(neighborUUID, phone.AuraProtocolCharUUID, data); err != nil {
            logger.Warn(prefix, "⚠️  Failed to send gossip to %s: %v", neighborUUID[:8], err)
        } else {
            sentCount++
        }
    }

    logger.Debug(prefix, "📢 Sent gossip to %d/%d neighbors", sentCount, len(neighbors))

    // Save mesh view periodically
    ip.meshView.SaveToDisk()
}

func (ip *iPhone) calculateProfileSummaryHash() []byte {
    // Hash all profile fields together
    h := sha256.New()
    h.Write([]byte(ip.localProfile.LastName))
    h.Write([]byte(ip.localProfile.PhoneNumber))
    h.Write([]byte(ip.localProfile.Tagline))
    h.Write([]byte(ip.localProfile.Insta))
    h.Write([]byte(ip.localProfile.LinkedIn))
    h.Write([]byte(ip.localProfile.Youtube))
    h.Write([]byte(ip.localProfile.TikTok))
    h.Write([]byte(ip.localProfile.Gmail))
    h.Write([]byte(ip.localProfile.IMessage))
    h.Write([]byte(ip.localProfile.WhatsApp))
    h.Write([]byte(ip.localProfile.Signal))
    h.Write([]byte(ip.localProfile.Telegram))
    return h.Sum(nil)
}
```

**Step 3.6**: Add helper methods for sending via Central/Peripheral modes

```go
func (ip *iPhone) sendViaCentralMode(remoteUUID, charUUID string, data []byte) error {
    ip.mu.RLock()
    peripheral, exists := ip.connectedPeripherals[remoteUUID]
    ip.mu.RUnlock()

    if !exists {
        return fmt.Errorf("not connected as central to %s", remoteUUID[:8])
    }

    char := peripheral.GetCharacteristic(phone.AuraServiceUUID, charUUID)
    if char == nil {
        return fmt.Errorf("characteristic %s not found", charUUID[:8])
    }

    return peripheral.WriteValue(data, char, swift.CBCharacteristicWriteWithResponse)
}

func (ip *iPhone) sendViaPeripheralMode(remoteUUID, charUUID string, data []byte) error {
    // Find the mutable characteristic by UUID
    var targetChar *swift.CBMutableCharacteristic
    switch charUUID {
    case phone.AuraProtocolCharUUID:
        targetChar = ip.protocolChar
    case phone.AuraPhotoCharUUID:
        targetChar = ip.photoChar
    case phone.AuraProfileCharUUID:
        targetChar = ip.profileChar
    default:
        return fmt.Errorf("unknown characteristic %s", charUUID[:8])
    }

    central := swift.CBCentral{UUID: remoteUUID}
    if success := ip.peripheralManager.UpdateValue(data, targetChar, []swift.CBCentral{central}); !success {
        return fmt.Errorf("failed to send notification (queue full)")
    }

    return nil
}
```

**Step 3.7**: Add request handlers

```go
func (ip *iPhone) requestPhoto(deviceID, photoHash string) {
    prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])

    logger.Info(prefix, "📸 Requesting photo %s from device %s (learned via gossip)", photoHash[:8], deviceID[:8])

    // Build PhotoRequestMessage
    hashBytes, _ := hex.DecodeString(photoHash)
    req := &proto.PhotoRequestMessage{
        RequesterDeviceId: ip.deviceID,
        TargetDeviceId:    deviceID,
        PhotoHash:         hashBytes,
    }

    data, err := proto.Marshal(req)
    if err != nil {
        logger.Error(prefix, "❌ Failed to marshal photo request: %v", err)
        return
    }

    // Find hardware UUID for this deviceID
    ip.mu.RLock()
    var targetUUID string
    for uuid, devID := range ip.peripheralToDeviceID {
        if devID == deviceID {
            targetUUID = uuid
            break
        }
    }
    ip.mu.RUnlock()

    if targetUUID == "" {
        logger.Warn(prefix, "⚠️  Cannot request photo: don't know hardware UUID for device %s", deviceID[:8])
        return
    }

    // Send request
    if err := ip.connManager.SendToDevice(targetUUID, phone.AuraProtocolCharUUID, data); err != nil {
        logger.Error(prefix, "❌ Failed to send photo request: %v", err)
        return
    }

    ip.meshView.MarkPhotoRequested(deviceID)
    logger.Debug(prefix, "📤 Sent photo request for %s", photoHash[:8])
}

func (ip *iPhone) requestProfile(deviceID string, version int32) {
    prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])

    logger.Info(prefix, "📝 Requesting profile v%d from device %s (learned via gossip)", version, deviceID[:8])

    // Build ProfileRequestMessage
    req := &proto.ProfileRequestMessage{
        RequesterDeviceId: ip.deviceID,
        TargetDeviceId:    deviceID,
        ExpectedVersion:   version,
    }

    data, err := proto.Marshal(req)
    if err != nil {
        logger.Error(prefix, "❌ Failed to marshal profile request: %v", err)
        return
    }

    // Find hardware UUID for this deviceID
    ip.mu.RLock()
    var targetUUID string
    for uuid, devID := range ip.peripheralToDeviceID {
        if devID == deviceID {
            targetUUID = uuid
            break
        }
    }
    ip.mu.RUnlock()

    if targetUUID == "" {
        logger.Warn(prefix, "⚠️  Cannot request profile: don't know hardware UUID for device %s", deviceID[:8])
        return
    }

    // Send request
    if err := ip.connManager.SendToDevice(targetUUID, phone.AuraProtocolCharUUID, data); err != nil {
        logger.Error(prefix, "❌ Failed to send profile request: %v", err)
        return
    }

    ip.meshView.MarkProfileRequested(deviceID)
    logger.Debug(prefix, "📤 Sent profile request for v%d", version)
}
```

**Step 3.8**: Remove old handshake logic
- [ ] Delete `sendHandshakeMessage()` method
- [ ] Delete `lastHandshakeTime` map (no longer needed)
- [ ] Delete periodic handshake re-send logic
- [ ] Keep photo/profile sending logic but triggered by gossip/requests instead

---

### Phase 4: Android Integration

**Step 4.1**: Add new fields to `Android` struct (`android/android.go`)
```go
type Android struct {
    // ... existing fields ...

    meshView         *phone.MeshView
    messageRouter    *phone.MessageRouter
    connManager      *phone.ConnectionManager

    // Gossip timing
    lastGossipTime   time.Time
    gossipInterval   time.Duration
}
```

**Step 4.2**: Initialize in `NewAndroid()`
```go
func NewAndroid(hardwareUUID string) *Android {
    // ... existing initialization ...

    a.meshView = phone.NewMeshView(a.deviceID, a.hardwareUUID, dataDir, a.cacheManager)

    a.messageRouter = &phone.MessageRouter{
        meshView:         a.meshView,
        cacheManager:     a.cacheManager,
        photoCoordinator: a.photoCoordinator,
        onPhotoNeeded: func(deviceID, photoHash string) {
            a.requestPhoto(deviceID, photoHash)
        },
        onProfileNeeded: func(deviceID string, version int32) {
            a.requestProfile(deviceID, version)
        },
    }

    a.connManager = &phone.ConnectionManager{
        hardwareUUID: hardwareUUID,
        centralConnections:    make(map[string]interface{}),
        peripheralConnections: make(map[string]bool),
        sendViaCentral: func(remoteUUID, charUUID string, data []byte) error {
            return a.sendViaCentralMode(remoteUUID, charUUID, data)
        },
        sendViaPeripheral: func(remoteUUID, charUUID string, data []byte) error {
            return a.sendViaPeripheralMode(remoteUUID, charUUID, data)
        },
    }

    a.gossipInterval = 5 * time.Second

    return a
}
```

**Step 4.3**: Update connection handlers (same pattern as iOS)
- [ ] `OnConnectionStateChange()` - register/unregister with `ConnectionManager`
- [ ] `OnConnectionStateChange()` (GATT server callback) - register/unregister peripherals

**Step 4.4**: Replace `handleHandshakeMessage()` with unified handler (same pattern as iOS)
- [ ] Update `OnCharacteristicChanged()` (Central mode)
- [ ] Update `OnCharacteristicWriteRequest()` (Peripheral mode)

**Step 4.5**: Add gossip sending goroutine (same pattern as iOS)
- [ ] Add `gossipLoop()` to `Start()`
- [ ] Implement `sendGossipToNeighbors()` (mirror iOS logic)
- [ ] Implement `calculateProfileSummaryHash()` (mirror iOS logic)

**Step 4.6**: Add helper methods for sending (same pattern as iOS)
- [ ] `sendViaCentralMode()`
- [ ] `sendViaPeripheralMode()`

**Step 4.7**: Add request handlers (same pattern as iOS)
- [ ] `requestPhoto()`
- [ ] `requestProfile()`

**Step 4.8**: Remove old handshake logic (same as iOS)

---

### Phase 5: Connection Strategy & Neighbor Management

**Step 5.1**: Update discovery flow

**Current** (connects to everyone):
```go
func (ip *iPhone) DidDiscoverPeripheral(...) {
    // Connect to every discovered device if we should be Central
    if ip.wire.ShouldActAsCentral(remoteUUID) {
        ip.manager.Connect(peripheral, nil)
    }
}
```

**New** (connects only to neighbors):
```go
func (ip *iPhone) DidDiscoverPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, advertisementData map[string]interface{}, rssi int) {
    // ... existing discovery callback logic ...

    // Extract deviceID from advertising data (need to add this!)
    // For now, we won't know deviceID until first gossip exchange
    // So we'll connect optimistically and let gossip determine if they're a neighbor

    // Check if we should act as Central for this device
    if !ip.wire.ShouldActAsCentral(peripheral.UUID) {
        logger.Debug(prefix, "⏭️  Skipping connection to %s (they should connect to us)", peripheral.UUID[:8])
        return
    }

    // Check if already connected
    if ip.connManager.IsConnected(peripheral.UUID) {
        return
    }

    // Connect (will determine neighbor status after gossip exchange)
    logger.Info(prefix, "🔌 Connecting to %s as Central", peripheral.UUID[:8])
    ip.manager.Connect(peripheral, nil)
}
```

**Step 5.2**: Add neighbor pruning logic

After receiving first gossip from a device, check if they're actually a neighbor:

```go
func (ip *iPhone) pruneNonNeighborConnections() {
    prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])

    neighbors := ip.meshView.GetCurrentNeighbors()
    neighborMap := make(map[string]bool)
    for _, deviceID := range neighbors {
        neighborMap[deviceID] = true
    }

    // Get all connected hardware UUIDs
    connectedUUIDs := ip.connManager.GetAllConnectedUUIDs()

    ip.mu.RLock()
    defer ip.mu.RUnlock()

    for _, uuid := range connectedUUIDs {
        deviceID := ip.peripheralToDeviceID[uuid]
        if deviceID == "" {
            continue // Don't know deviceID yet
        }

        // Check if this is a neighbor
        if !neighborMap[deviceID] {
            logger.Info(prefix, "✂️  Disconnecting from %s (not a neighbor)", uuid[:8])

            // Disconnect based on role
            if peripheral, exists := ip.connectedPeripherals[uuid]; exists {
                // We connected as Central, so we disconnect
                ip.manager.CancelPeripheralConnection(peripheral)
            }
            // If they connected to us as Peripheral, we can't force disconnect
            // (that's up to them when they prune their neighbors)
        }
    }
}
```

Call `pruneNonNeighborConnections()` periodically (e.g., every 30 seconds) or after gossip rounds.

**Step 5.3**: Handle connection churn gracefully

When a neighbor disconnects (network issue, battery, etc.):
- Don't immediately reconnect (avoid flapping)
- Let gossip protocol naturally discover it's missing
- Neighbor selection algorithm is deterministic, so both sides know who should connect
- Device with higher UUID will attempt reconnect if they're supposed to be Central

---

### Phase 6: Profile Management

**Step 6.1**: Add `ProfileVersion` tracking

**In `LocalProfile` struct** (`iphone/iphone.go` and `android/android.go`):
```go
type LocalProfile struct {
    FirstName   string
    LastName    string
    PhoneNumber string
    Tagline     string
    Insta       string
    LinkedIn    string
    Youtube     string
    TikTok      string
    Gmail       string
    IMessage    string
    WhatsApp    string
    Signal      string
    Telegram    string

    ProfileVersion int32  // NEW: Increments on any change
}
```

**Step 6.2**: Update `UpdateProfile()` to increment version

```go
func (ip *iPhone) UpdateProfile(profile *LocalProfile) error {
    // Increment version
    profile.ProfileVersion = ip.localProfile.ProfileVersion + 1

    ip.localProfile = profile

    // Save to disk (with version)
    // ... existing save logic ...

    // Trigger immediate gossip to broadcast new version
    go ip.sendGossipToNeighbors()

    return nil
}
```

**Step 6.3**: Handle incoming `ProfileRequestMessage`

In `MessageRouter.HandleProtocolMessage()`, add:
```go
// Try ProfileRequestMessage
profileReq := &proto.ProfileRequestMessage{}
if err := proto.Unmarshal(data, profileReq); err == nil && profileReq.RequesterDeviceId != "" {
    return mr.handleProfileRequest(senderUUID, profileReq)
}
```

Implement handler:
```go
func (mr *MessageRouter) handleProfileRequest(senderUUID string, req *proto.ProfileRequestMessage) error {
    // Check if they're requesting OUR profile
    if req.TargetDeviceId != mr.ourDeviceID {
        // They want someone else's profile - we don't relay (yet)
        return nil
    }

    // Send our profile via callback
    if mr.onProfileRequested != nil {
        mr.onProfileRequested(senderUUID, req.ExpectedVersion)
    }

    return nil
}
```

**Step 6.4**: Send profile in response

```go
func (ip *iPhone) onProfileRequested(senderUUID string, expectedVersion int32) {
    prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])

    logger.Info(prefix, "📝 Received profile request from %s (they want v%d, we have v%d)",
        senderUUID[:8], expectedVersion, ip.localProfile.ProfileVersion)

    // Send current profile
    profileMsg := &proto.ProfileMessage{
        DeviceId:    ip.deviceID,
        LastName:    ip.localProfile.LastName,
        PhoneNumber: ip.localProfile.PhoneNumber,
        Tagline:     ip.localProfile.Tagline,
        Insta:       ip.localProfile.Insta,
        Linkedin:    ip.localProfile.LinkedIn,
        Youtube:     ip.localProfile.Youtube,
        Tiktok:      ip.localProfile.TikTok,
        Gmail:       ip.localProfile.Gmail,
        Imessage:    ip.localProfile.IMessage,
        Whatsapp:    ip.localProfile.WhatsApp,
        Signal:      ip.localProfile.Signal,
        Telegram:    ip.localProfile.Telegram,
    }

    data, err := proto.Marshal(profileMsg)
    if err != nil {
        logger.Error(prefix, "❌ Failed to marshal profile: %v", err)
        return
    }

    // Send via appropriate path
    if err := ip.connManager.SendToDevice(senderUUID, phone.AuraProfileCharUUID, data); err != nil {
        logger.Error(prefix, "❌ Failed to send profile: %v", err)
    } else {
        logger.Debug(prefix, "📤 Sent profile to %s", senderUUID[:8])
    }
}
```

---

### Phase 7: Testing & Validation

**Step 7.1**: Unit tests for new components
- [ ] Test `MeshView.MergeGossip()` with various scenarios
- [ ] Test `MeshView.SelectRandomNeighbors()` is deterministic
- [ ] Test `ConnectionManager` routes messages correctly
- [ ] Test `MessageRouter` detects message types correctly

**Step 7.2**: Integration test scenarios

**Scenario 1: 2 devices**
- Device A (higher UUID) connects to Device B as Central
- Device B accepts connection as Peripheral
- Both exchange gossip every 5 seconds
- Verify: Each device learns about the other via gossip
- Verify: Photos are requested and transferred
- Verify: Profiles are requested and transferred

**Scenario 2: 4 devices in a line**
```
A ←→ B ←→ C ←→ D
```
- Start devices sequentially
- Verify: Each device connects to 3 neighbors (or fewer if <3 devices exist)
- Verify: Device A eventually learns about Device D (via multi-hop gossip)
- Verify: Device A requests Device D's photo (direct connection or multi-hop)

**Scenario 3: Profile update propagation**
- Device B updates Instagram handle
- Verify: ProfileVersion increments
- Verify: Gossip broadcasts new version
- Verify: Neighbors request updated profile
- Verify: All devices eventually have new Instagram handle

**Scenario 4: Connection loss & recovery**
- Disconnect Device B
- Verify: Neighbors detect disconnection
- Verify: Gossip continues via alternate paths (A→C, C→D)
- Reconnect Device B
- Verify: Connections re-establish
- Verify: Gossip resumes normally

**Scenario 5: Dual-role stress test**
- Device B acts as Central for Device C
- Device B acts as Peripheral for Device A
- Send gossip from A→B, B→C, C→B, B→A simultaneously
- Verify: No race conditions, no dropped messages
- Verify: All gossip messages processed correctly

**Step 7.3**: Load testing
- [ ] 10 devices, verify O(log N) connections (each device ~3-4 connections)
- [ ] 20 devices, verify gossip convergence time
- [ ] Measure memory usage with large mesh views

---

### Phase 8: Cleanup & Optimization

**Step 8.1**: Remove deprecated code
- [ ] Delete `handleHandshakeMessage()` (replaced by `HandleProtocolMessage()`)
- [ ] Delete `sendHandshakeMessage()` (replaced by gossip)
- [ ] Delete `lastHandshakeTime` tracking (no longer needed)
- [ ] Delete stale handshake retry logic

**Step 8.2**: Update CLAUDE.md documentation
- [ ] Document gossip protocol architecture
- [ ] Update wire protocol section (gossip instead of handshakes)
- [ ] Document dual-role connection strategy
- [ ] Add troubleshooting section for common issues

**Step 8.3**: Optimize gossip message size
- [ ] Consider delta-based gossip (only send changes)
- [ ] Add gossip message compression (gzip)
- [ ] Limit mesh view size (e.g., max 100 devices, prune old entries)

**Step 8.4**: Add metrics/observability
- [ ] Log gossip round timings
- [ ] Track # of devices discovered via gossip vs direct
- [ ] Monitor connection churn rate
- [ ] Track photo/profile request success rates

---

## Rollout Strategy

### Phase 1: Preparation (No Breaking Changes)
1. Add protobuf fields (backwards compatible)
2. Add `phone/message_router.go` and `phone/connection_manager.go`
3. Add fields to iPhone/Android structs (don't use yet)

### Phase 2: Dual-Mode Operation (Handshake + Gossip)
1. Keep existing handshake logic
2. Add gossip sending/receiving in parallel
3. Both protocols work simultaneously
4. Test thoroughly with 2-4 devices

### Phase 3: Gossip-Only Mode
1. Remove handshake sending (but keep parsing for backwards compat)
2. All state updates via gossip
3. Test with 4-10 devices

### Phase 4: Cleanup
1. Remove handshake parsing completely
2. Remove deprecated code
3. Final validation with 10-20 devices

---

## Risk Mitigation

### Risk: Race conditions in dual-role operation
**Mitigation**: Use `ConnectionManager` with proper locking, separate connection maps for Central/Peripheral

### Risk: Gossip message amplification (broadcast storm)
**Mitigation**: Fixed 5-second interval, send only to 3 neighbors, no forwarding/relaying

### Risk: Stale mesh view data
**Mitigation**: `last_seen_timestamp` in gossip, prune entries older than 60 seconds

### Risk: Profile data too large for MTU
**Mitigation**: Profile messages already use separate characteristic with chunking if needed

### Risk: Photo request loops (A requests from B, B requests from A)
**Mitigation**: `MarkPhotoRequested()` flag prevents duplicate requests

### Risk: Neighbor selection causes network partitions
**Mitigation**: Deterministic algorithm ensures connectivity graph remains connected (proven for consistent hashing with proper k value)

---

## Success Criteria

- [ ] Each device connects to ≤3 neighbors (not full mesh)
- [ ] Gossip messages sent every 5 seconds
- [ ] All devices learn about all others within 30 seconds (4-hop latency)
- [ ] Photos requested and transferred on-demand when hashes differ
- [ ] Profiles requested and transferred when versions differ
- [ ] No handshake messages sent (deprecated)
- [ ] Both iOS and Android work identically
- [ ] Dual-role (Central+Peripheral) works without race conditions
- [ ] Connection loss/recovery handled gracefully
- [ ] Memory usage scales with # of devices in mesh (not # of connections)

---

## Timeline Estimate

- **Phase 1 (Protocol Updates)**: 2 hours
- **Phase 2 (Shared Infrastructure)**: 4 hours
- **Phase 3 (iOS Integration)**: 8 hours
- **Phase 4 (Android Integration)**: 8 hours
- **Phase 5 (Connection Strategy)**: 4 hours
- **Phase 6 (Profile Management)**: 4 hours
- **Phase 7 (Testing & Validation)**: 8 hours
- **Phase 8 (Cleanup)**: 4 hours

**Total**: ~42 hours of focused development

---

## Open Questions

1. **Should we support photo relaying?** (A requests from B, B fetches from C and relays to A)
   - Pro: Faster convergence, works even if source is disconnected
   - Con: More complex, requires photo caching by intermediaries
   - **Decision**: Start without relaying, add if needed

2. **How to handle deviceID in advertising data?**
   - Currently advertising only shows hardware UUID
   - Need deviceID to prune connections before first gossip
   - **Options**:
     - Add deviceID to advertising data (requires protobuf/binary encoding)
     - Connect optimistically, prune after first gossip (simpler)
   - **Decision**: Connect optimistically for now

3. **Should gossip include timestamp for each device entry?**
   - Current `last_seen_timestamp` shows when we last saw this device
   - But gossip is periodic, so this is always "now"
   - **Decision**: Keep `last_seen_timestamp`, update when merging gossip

4. **How to handle profile changes that decrease version number?** (clock skew, device reset)
   - Use version number + summary hash together
   - If hash differs but version is lower, trust the hash
   - **Decision**: Request profile if EITHER version is higher OR hash differs

5. **Should we persist `MeshView` across app restarts?**
   - Pro: Faster bootstrap, remember devices seen while offline
   - Con: Stale data, need expiration logic
   - **Decision**: Already implemented in `mesh_view.go`, keep it
