# DeviceID Architecture - Correct Implementation

## Problem Statement (from ~/Documents/fix.txt)

The old architecture had a **UUID vs DeviceID Identity Crisis**:
- Hardware UUID and DeviceID were mixed inconsistently
- Race conditions: Photo chunks arrived BEFORE handshake populated UUID→DeviceID mapping
- Routing logic conflated UUIDs and DeviceIDs → wrong device responses

## Solution: Two Separate Identity Layers

```
┌─────────────────────────────────────────────┐
│ Layer 1: Wire (Hardware UUID ONLY)         │
│ - Unix socket paths: /tmp/auraphone-{UUID} │
│ - Connection state                          │
│ - Message routing                           │
└─────────────────────────────────────────────┘
              ↕ (IdentityManager mapping)
┌─────────────────────────────────────────────┐
│ Layer 2: Application (DeviceID)            │
│ - User-visible IDs                          │
│ - Persistent storage keys                   │
│ - Display names                             │
└─────────────────────────────────────────────┘
```

---

## Key Components

### 1. **DeviceID Generation** (`phone/deviceid.go`)

```go
// LoadOrGenerateDeviceID - called once on first start
deviceID, err := phone.LoadOrGenerateDeviceID(hardwareUUID)

// Generates 8-char base36 ID: "ZSJSMVL7"
// Stored in: ~/.auraphone-blue-data/{hardwareUUID}/cache/device_id.json
// Persistent across restarts
```

**Lifecycle:**
1. First start → Generate random base36 ID → Save to disk
2. Subsequent starts → Load from disk
3. Never changes (unless cache deleted)

---

### 2. **IdentityManager** (`phone/identity_manager.go`)

**THE ONLY PLACE** where Hardware UUID ↔ DeviceID mapping exists.

```go
type IdentityManager struct {
    ourHardwareUUID string
    ourDeviceID     string

    // BIDIRECTIONAL mapping
    hardwareToDevice map[string]string  // UUID → DeviceID
    deviceToHardware map[string]string  // DeviceID → UUID

    // Connection tracking (by hardware UUID!)
    connectedDevices map[string]bool
}
```

**Critical Methods:**

```go
// After handshake - register peer's mapping
identityManager.RegisterDevice(hardwareUUID, deviceID)

// Sending message - lookup hardware UUID from device ID
hardwareUUID, ok := identityManager.GetHardwareUUID(deviceID)
if ok {
    wire.SendGATTMessage(hardwareUUID, data)
}

// Receiving message - lookup device ID from hardware UUID
deviceID, ok := identityManager.GetDeviceID(hardwareUUID)
// Use deviceID for display/logging

// Connection tracking
identityManager.MarkConnected(hardwareUUID)
identityManager.MarkDisconnected(hardwareUUID)
```

---

### 3. **Handshake Protocol** (`iphone/iphone.go`)

**Handshake Message:**
```json
{
  "hardware_uuid": "B4698CEC-...",
  "device_id": "ZSJSMVL7",
  "device_name": "iPhone (ZSJS)",
  "first_name": "ZSJS"
}
```

**Flow:**
1. Connection established (wire layer uses hardware UUID)
2. Both devices send handshake → exchange DeviceIDs
3. `handleHandshake()` calls `identityManager.RegisterDevice(hardwareUUID, deviceID)`
4. Mappings persisted to disk: `~/.auraphone-blue-data/{uuid}/identity_mappings.json`

---

## The Rules (CRITICAL)

### ✅ DO:

1. **Wire layer uses Hardware UUID exclusively**
   ```go
   wire.Connect(peerHardwareUUID)
   wire.SendGATTMessage(peerHardwareUUID, data)
   ```

2. **Lookup before sending**
   ```go
   // CORRECT
   hardwareUUID, ok := identityManager.GetHardwareUUID(deviceID)
   if !ok {
       return fmt.Errorf("device %s not handshaked yet", deviceID)
   }
   wire.SendGATTMessage(hardwareUUID, data)
   ```

3. **Connection tracking uses Hardware UUID**
   ```go
   identityManager.MarkConnected(hardwareUUID)    // ✅
   identityManager.IsConnected(hardwareUUID)      // ✅
   ```

4. **DeviceID for display only**
   ```go
   deviceID, _ := identityManager.GetDeviceID(hardwareUUID)
   logger.Info("Received message from %s", deviceID) // ✅
   ```

### ❌ DON'T:

1. **Never use DeviceID for wire routing**
   ```go
   wire.SendGATTMessage(deviceID, data)  // ❌ WRONG - deviceID is not a hardware UUID!
   ```

2. **Never mix identifiers**
   ```go
   connectedDevices[deviceID] = true  // ❌ WRONG - use hardwareUUID
   ```

3. **Never skip IdentityManager lookup**
   ```go
   // ❌ WRONG - assuming deviceID == hardwareUUID
   wire.Connect(deviceID)

   // ✅ CORRECT - always lookup first
   hardwareUUID, ok := identityManager.GetHardwareUUID(deviceID)
   if ok {
       wire.Connect(hardwareUUID)
   }
   ```

4. **Never send messages before handshake**
   ```go
   // ❌ WRONG - no mapping yet!
   hardwareUUID, ok := identityManager.GetHardwareUUID(deviceID)
   if !ok {
       // Handshake not complete, can't send yet
   }
   ```

---

## Example: Sending a Photo Request

**WRONG (old architecture - caused bug in fix.txt):**
```go
// User clicks "request photo from device ZSJSMVL7"
SendPhotoRequest(targetDeviceID)  // Uses deviceID directly
  → wire.Send(targetDeviceID, data)  // ❌ deviceID is not a socket path!
```

**CORRECT (new architecture):**
```go
// User clicks "request photo from device ZSJSMVL7"
func SendPhotoRequest(targetDeviceID string) error {
    // Step 1: Lookup hardware UUID from device ID
    hardwareUUID, ok := identityManager.GetHardwareUUID(targetDeviceID)
    if !ok {
        return fmt.Errorf("device %s not yet handshaked", targetDeviceID)
    }

    // Step 2: Check if connected (by hardware UUID)
    if !identityManager.IsConnected(hardwareUUID) {
        return fmt.Errorf("device %s not connected", targetDeviceID)
    }

    // Step 3: Send via wire using hardware UUID
    msg := CreatePhotoRequest(ourDeviceID, targetDeviceID, photoHash)
    return wire.SendGATTMessage(hardwareUUID, msg)
}
```

---

## File Structure

```
~/.auraphone-blue-data/
  B4698CEC-.../                    # Hardware UUID directory
    cache/
      device_id.json               # Our DeviceID (generated once)
    identity_mappings.json         # Peer hardware UUID ↔ device ID mappings
```

**device_id.json:**
```json
{
  "device_id": "ZSJSMVL7"
}
```

**identity_mappings.json:**
```json
{
  "our_hardware_uuid": "B4698CEC-...",
  "our_device_id": "ZSJSMVL7",
  "mappings": [
    {
      "hardware_uuid": "88716210-...",
      "device_id": "0JSEUJO6"
    }
  ]
}
```

---

## Real-World Scenarios: UUID/DeviceID Changes

### Scenario 1: iOS Privacy UUID Rotation

**What happens:**
iOS periodically rotates hardware UUIDs for privacy (similar to MAC address randomization).

**Example:**
1. First connection: iPhone connects with UUID `12D90340-F045-495D-938E-28B6F3E2FE80`
2. Handshake completes: DeviceID `ABCD1234` is exchanged
3. IdentityManager registers: `12D90340... → ABCD1234`, `ABCD1234 → 12D90340...`
4. Connection drops
5. iOS rotates UUID to `B4698CEC-8C31-40FE-B536-1C0710BBCDCE`
6. iPhone reconnects with new UUID `B4698CEC...`
7. Handshake completes: Same DeviceID `ABCD1234` is sent again

**How RegisterDevice() handles it:**
```go
// DeviceID ABCD1234 is already mapped to old UUID 12D90340...
// New mapping: B4698CEC... → ABCD1234
if oldHardwareUUID, exists := im.deviceToHardware["ABCD1234"]; exists {
    // oldHardwareUUID = "12D90340..."
    // Remove stale mapping: 12D90340... → ABCD1234
    delete(im.hardwareToDevice, oldHardwareUUID)
    // Also clean up connection state for old UUID
    delete(im.connectedDevices, oldHardwareUUID)
}

// Add new mapping
im.hardwareToDevice["B4698CEC..."] = "ABCD1234"
im.deviceToHardware["ABCD1234"] = "B4698CEC..."
```

**Result:** Same logical device (ABCD1234) now mapped to new hardware UUID. Old UUID cleaned up.

---

### Scenario 2: Android Device Reuse (Factory Reset or New Owner)

**What happens:**
User factory resets their phone or sells it to someone new. Hardware UUID stays the same, but new owner gets a new DeviceID.

**Example:**
1. First owner connects: UUID `F09FFFB7-FEC6-402A-AC0C-FB06425E2670`
2. Handshake: DeviceID `ABCD1234`
3. IdentityManager registers: `F09FFFB7... → ABCD1234`, `ABCD1234 → F09FFFB7...`
4. Phone is factory reset and sold to new owner
5. New owner installs app, gets new DeviceID `ABCD1235`
6. Phone reconnects with same UUID `F09FFFB7...`
7. Handshake: New DeviceID `ABCD1235` is sent

**How RegisterDevice() handles it:**
```go
// Hardware UUID F09FFFB7... is already mapped to old DeviceID ABCD1234
// New mapping: F09FFFB7... → ABCD1235
if oldDeviceID, exists := im.hardwareToDevice["F09FFFB7..."]; exists {
    // oldDeviceID = "ABCD1234"
    // Remove stale reverse mapping: ABCD1234 → F09FFFB7...
    delete(im.deviceToHardware, oldDeviceID)
}

// Add new mapping
im.hardwareToDevice["F09FFFB7..."] = "ABCD1235"
im.deviceToHardware["ABCD1235"] = "F09FFFB7..."
```

**Result:** Same hardware (F09FFFB7...) now mapped to new logical device. Old DeviceID cleaned up.

---

### Why Gossip Must NOT Include Hardware UUIDs

**The Problem:**
If gossip messages contain hardware UUIDs, they become stale immediately:

```protobuf
// ❌ BAD: Hardware UUID becomes outdated
message DeviceState {
  string device_id = 1;
  string hardware_uuid = 7;  // This is ephemeral!
}
```

**What goes wrong:**
1. Device A gossips: "ABCD1234 is at UUID 12D90340..."
2. Gossip spreads to devices B, C, D (takes 5-15 seconds)
3. Meanwhile, iOS rotates UUID to B4698CEC...
4. Device D tries to connect to 12D90340... → **fails, UUID doesn't exist**

**The Solution:**
```protobuf
// ✅ GOOD: Only stable DeviceID in gossip
message DeviceState {
  string device_id = 1;  // Stable identity
  // hardware_uuid removed - discovered via BLE scanning
}
```

**Connection Flow:**
1. Learn about DeviceID `ABCD1234` via gossip (or direct encounter)
2. BLE scan discovers nearby devices by current hardware UUID
3. Connect to hardware UUID and handshake
4. Handshake reveals DeviceID → IdentityManager maps them
5. Now we can send/receive using DeviceID (looks up current hardware UUID)

---

## Benefits of This Architecture

1. **No race conditions** - Hardware UUID available immediately, DeviceID after handshake
2. **Clear separation** - Wire never knows about DeviceIDs, app never routes by UUID
3. **Persistent across restarts** - Mappings saved to disk
4. **Single source of truth** - IdentityManager is the ONLY place with mappings
5. **Connection tracking** - By hardware UUID (wire layer), mapped to DeviceID (app layer)
6. **Handles UUID rotation** - iOS privacy UUID changes handled transparently
7. **Handles device reuse** - Android factory reset/new owner scenarios supported

---

## Migration from Old Code

**Before:**
```go
// Old code mixed UUIDs and DeviceIDs everywhere
uuidToDeviceID map[string]string  // Populated async via callbacks
deviceIDToPhotoHash map[string]string
SendToDevice(deviceID, data)  // Fragile - race conditions!
```

**After:**
```go
// New code: IdentityManager is the single source of truth
identityManager *IdentityManager
  hardwareToDevice map[string]string
  deviceToHardware map[string]string
  connectedDevices map[string]bool  // Keyed by hardware UUID!

// Always lookup before sending
hardwareUUID, _ := identityManager.GetHardwareUUID(deviceID)
wire.SendGATTMessage(hardwareUUID, data)
```

---

## Testing DeviceID Generation

```bash
# Start 2 iPhones
go run main.go

# Check generated device IDs
ls -la ~/.auraphone-blue-data/
# B4698CEC-.../ (first device)
# F09FFFB7-.../ (second device)

cat ~/.auraphone-blue-data/B4698CEC-.../cache/device_id.json
# {"device_id": "ZSJSMVL7"}

cat ~/.auraphone-blue-data/B4698CEC-.../identity_mappings.json
# {
#   "our_hardware_uuid": "B4698CEC-...",
#   "our_device_id": "ZSJSMVL7",
#   "mappings": [
#     {"hardware_uuid": "F09FFFB7-...", "device_id": "0JSEUJO6"}
#   ]
# }
```

---

## Common Pitfalls to Avoid

1. ❌ Using DeviceID as map key for connection state
2. ❌ Passing DeviceID to wire.Connect() or wire.SendGATTMessage()
3. ❌ Sending messages before handshake completes
4. ❌ Multiple places maintaining UUID ↔ DeviceID mappings
5. ❌ Forgetting to call RegisterDevice() after handshake
6. ❌ Using DeviceID for file paths or socket names

**Remember:** Hardware UUID for wire, DeviceID for humans.
