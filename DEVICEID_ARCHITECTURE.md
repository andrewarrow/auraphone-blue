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

## Benefits of This Architecture

1. **No race conditions** - Hardware UUID available immediately, DeviceID after handshake
2. **Clear separation** - Wire never knows about DeviceIDs, app never routes by UUID
3. **Persistent across restarts** - Mappings saved to disk
4. **Single source of truth** - IdentityManager is the ONLY place with mappings
5. **Connection tracking** - By hardware UUID (wire layer), mapped to DeviceID (app layer)

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
