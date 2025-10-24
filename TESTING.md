# Auraphone Blue - Testing Framework

## Overview
This simulator helps test real-world BLE edge cases discovered in the iOS and Android Aura apps without needing multiple physical devices.

## Key Edge Cases Identified

### 1. **Photo Send Collision**
**Scenario:** Two devices try to send photos to each other simultaneously
- Both devices receive metadata packets while sending
- Tie-breaker logic: Device with lexicographically larger ID wins
- Winner: Continues sending, stores incoming metadata for later reception
- Loser: Aborts send, receives other device's photo, then retries send
- **Real logs:** `⚔️ [PHOTO-COLLISION] Win/Loss` messages

### 2. **Stale Handshake Detection**
**Scenario:** Device is nearby but handshake is >60 seconds old
- Periodic scan results trigger duplicate callbacks
- Throttling prevents spam (5 second intervals)
- Stale handshake triggers reconnection to check for profile updates
- **Real logs:** `🔄 [STALE-HANDSHAKE] Handshake is stale`

### 3. **Connection Arbitration (iOS ↔ Android)**
**Scenario:** iOS and Android discover each other simultaneously
- **iOS:** Always acts as Central (initiates connection)
- **Android:** Always acts as Peripheral (waits for iOS to connect)
- Prevents dual-connection race condition
- **Real logs:** `📡 [SCAN] waiting for them to connect (we're peripheral)`

### 4. **MTU Negotiation**
**Scenario:** Handshake messages must fit in one packet
- iOS/Android request 512-byte MTU
- Fallback to 20-byte default if negotiation fails
- Affects protobuf handshake delivery reliability
- **Real logs:** `MTU changed to X bytes` or `MTU change failed`

### 5. **Notification Subscription Timing**
**Scenario:** Central writes handshake before subscription is ready
- Android peripheral delays handshake response (100ms)
- iOS central waits for subscription before sending photo
- Race condition if photo sent before subscription active
- **Real logs:** `⚠️ [HANDSHAKE-RESPONSE] Failed to send (central may not be subscribed)`

### 6. **Cache Resurrection**
**Scenario:** Device was cached previously, reappears after app restart
- BluetoothDevice is null in cached device
- On rediscovery, attach real peripheral and reconnect
- Must trigger handshake to check for profile updates
- **Real logs:** `📡 [SCAN] Rediscovered cached device`

### 7. **Chunk Timeout and Retry**
**Scenario:** Photo chunk write times out (didn't receive write confirmation)
- Per-chunk 3-second timeout with 3 retries
- Exponential backoff or immediate retry
- Abort transfer after 3 failed retries
- **Real logs:** `Chunk X timeout, retrying (Y/3)`

### 8. **Peripheral Photo Send Flow Control**
**Scenario:** Android peripheral sends photo via notifications
- Must use `onNotificationSent` callback for pacing
- Can't send next chunk until previous notification confirmed
- Queue management for multiple pending centrals
- **Real logs:** `📡 [NOTIFICATION-SENT] Callback received`

### 9. **Advertising Interval Discovery Delays**
**Scenario:** Real BLE advertising intervals (100ms-10s)
- Devices not instantly discovered
- Multiple scan cycles before connection
- RSSI fluctuation based on distance/interference

### 10. **Connection State Race Conditions**
**Scenario:** Disconnect during handshake or photo transfer
- Clean up pending transfers
- Remove from connection maps
- Don't retry on disconnected device

## Test Scenario Format

Test scenarios are defined in JSON format:

```json
{
  "name": "Photo Send Collision - iOS vs Android",
  "description": "Two devices try to send photos simultaneously, tie-breaker resolves",
  "devices": [
    {
      "id": "ios-device-1",
      "platform": "ios",
      "device_name": "iPhone 15 Pro",
      "profile": {
        "first_name": "Alice",
        "photo_hash": "abc123"
      }
    },
    {
      "id": "android-device-1",
      "platform": "android",
      "device_name": "Pixel 8",
      "profile": {
        "first_name": "Bob",
        "photo_hash": "def456"
      }
    }
  ],
  "timeline": [
    {
      "time_ms": 0,
      "action": "start_advertising",
      "device": "android-device-1"
    },
    {
      "time_ms": 100,
      "action": "start_scanning",
      "device": "ios-device-1"
    },
    {
      "time_ms": 1200,
      "action": "discover",
      "device": "ios-device-1",
      "target": "android-device-1"
    },
    {
      "time_ms": 1300,
      "action": "connect",
      "device": "ios-device-1",
      "target": "android-device-1"
    },
    {
      "time_ms": 1400,
      "action": "discover_services",
      "device": "ios-device-1"
    },
    {
      "time_ms": 1500,
      "action": "send_handshake",
      "device": "ios-device-1"
    },
    {
      "time_ms": 1600,
      "action": "send_handshake",
      "device": "android-device-1"
    },
    {
      "time_ms": 2000,
      "action": "send_photo_metadata",
      "device": "ios-device-1",
      "comment": "Both devices start sending simultaneously"
    },
    {
      "time_ms": 2000,
      "action": "send_photo_metadata",
      "device": "android-device-1"
    }
  ],
  "assertions": [
    {
      "type": "collision_detected",
      "device": "ios-device-1"
    },
    {
      "type": "collision_winner",
      "device": "android-device-1",
      "reason": "Pixel 8 > iPhone 15 Pro (lexicographic)"
    },
    {
      "type": "photo_transfer_completed",
      "from": "android-device-1",
      "to": "ios-device-1"
    },
    {
      "type": "photo_transfer_completed",
      "from": "ios-device-1",
      "to": "android-device-1",
      "comment": "iOS retries after receiving"
    }
  ]
}
```

## Log Parsing

Convert real mobile app logs into test scenarios:

### iOS Log Example:
```
📡 [SCAN] Discovered device: Pixel 8
⚔️ [PHOTO-COLLISION] Loss (iPhone 15 Pro <= Pixel 8). Aborting my send to receive theirs.
📸 [COLLISION-ABORT] Stored aborted send to peripheral: Pixel 8
✅ [PHOTO] Completed receiving photo from Pixel 8
🔄 [COLLISION-RETRY] Retrying send to peripheral: Pixel 8
```

### Android Log Example:
```
📡 [SCAN] Discovered device: iPhone 15 Pro, waiting for them to connect (we're peripheral)
📥 [HANDSHAKE] Received handshake from central
⚔️ [PHOTO-COLLISION] Win (Pixel 8 > iPhone 15 Pro). Storing their metadata to receive after our send completes.
📸 [PROFILE-PHOTO] Completed sending photo to central iPhone 15 Pro
```

## Running Tests

```bash
# Run a specific scenario
go test ./tests -run TestPhotoCollision

# Run all edge case scenarios
go test ./tests -v

# Generate scenario from logs
go run cmd/log2scenario/main.go --ios ios.log --android android.log --output scenario.json

# Replay scenario
go run cmd/replay/main.go --scenario scenarios/photo_collision.json
```

## Future Scenarios to Add

- [ ] 5+ devices in room (many-to-many connections)
- [ ] Device goes out of range mid-transfer
- [ ] App backgrounded during photo send
- [ ] Bluetooth toggled off/on
- [ ] Memory pressure causing disconnections
- [ ] Different MTU capabilities (iOS 512, Android 247, etc.)
- [ ] Corrupted photo data (CRC mismatch)
- [ ] Profile photo changed mid-transfer
- [ ] Rapid connect/disconnect cycles
- [ ] iOS background mode restrictions
