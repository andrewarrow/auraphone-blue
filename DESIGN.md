# Photo Caching & Handshake Implementation Design

## Overview
This document describes how we integrate the iOS/Android photo caching logic and protobuf handshakes into the auraphone-blue simulator to enable realistic photo transfer scenarios with collision detection and caching.

## Architecture

### 1. Device Cache Manager (`wire/cache.go`)
Manages per-device persistent state including:
- **Local photo**: Our profile photo stored as `Photos/local_user.jpg`
- **Local photo hash**: SHA-256 hash of our current photo (tx_photo_hash)
- **Remote device data**: Metadata for each device we've met
- **Remote photos**: Photos received from other devices, named by hash `Photos/{hash}.jpg`
- **Photo versions sent**: Map of deviceID → photoHash tracking which version we sent to each device

```go
type DeviceCacheManager struct {
    baseDir string  // Base cache directory (e.g., "data/{uuid}/cache")
}

// Key methods:
- GetLocalUserPhotoHash() string
- SaveLocalUserPhoto(data []byte) error
- GetDevicePhotoHash(deviceID string) string  // Their photo we have cached
- SaveDevicePhoto(deviceID string, data []byte, hash string) error
- MarkPhotoSentToDevice(deviceID, hash string)
- GetPhotoVersionSentToDevice(deviceID string) string
```

### 2. Handshake Protocol (protobuf)
Uses `proto/handshake.proto` with generated Go code:

```protobuf
message HandshakeMessage {
  string device_id = 1;
  string first_name = 2;
  int32 protocol_version = 3;
  // ... other profile fields ...
  string rx_photo_hash = 8;  // Hash of photo WE received FROM them
  string tx_photo_hash = 9;  // Hash of OUR photo available to send
}
```

**Hash Semantics:**
- **tx_photo_hash**: "Here's the hash of MY photo I can send to you"
- **rx_photo_hash**: "Here's the hash of YOUR photo that I already have cached"

**Decision Logic:**
- **We send if**: Our tx_photo_hash != their rx_photo_hash (they don't have our current photo)
- **We receive if**: Their tx_photo_hash != our cached rx_photo_hash (we don't have their current photo)

### 3. Scenario Actions (tests/runner.go)

#### Existing Actions:
- `start_advertising`, `stop_advertising`
- `start_scanning`, `stop_scanning`
- `discover`, `connect`, `disconnect`

#### New Actions to Implement:
- `discover_services` - Read remote GATT table
- `send_handshake` - Send protobuf handshake with tx/rx hashes
- `receive_handshake` - Parse handshake and log hashes
- `send_photo_metadata` - Initiate photo transfer (checks hash first)
- `receive_photo_metadata` - Receive photo transfer request
- `send_photo_data` - Transfer photo chunks over BLE
- `receive_photo_data` - Receive photo chunks
- `send_photo_ack` - Acknowledge photo completion with CRC
- `receive_photo_ack` - Receive acknowledgment

#### Collision Detection Actions:
- When `send_photo_metadata` and `receive_photo_metadata` happen simultaneously
- Tie-breaker: lexicographic device name comparison (larger name wins)
- Winner continues sending
- Loser aborts and switches to receiving

### 4. Device State Tracking (SimulatedDevice)
Extend `SimulatedDevice` in `tests/runner.go` to track:

```go
type SimulatedDevice struct {
    Config         *DeviceConfig
    UUID           string
    Wire           *wire.Wire
    Cache          *wire.DeviceCacheManager
    IOSMgr         *swift.CBCentralManager
    AndroidMgr     *kotlin.BluetoothManager

    // Photo transfer state
    IsSendingPhoto     bool
    IsReceivingPhoto   bool
    PhotoSendTarget    string  // Device ID we're sending to
    PhotoReceiveSource string  // Device ID we're receiving from

    // Handshake state
    HandshakesReceived map[string]*proto.HandshakeMessage
    HandshakesSent     map[string]*proto.HandshakeMessage

    // Collision detection
    CollisionDetected  bool
    CollisionWinner    bool
    AbortedSendTo      string
}
```

### 5. Assertions (tests/scenario.go)

#### New Assertion Types:
- `connected` - Device A connected to device B
- `handshake_received` - Device received handshake from specific device
- `collision_detected` - Device detected simultaneous send collision
- `collision_winner` - Device won tie-breaker and continued sending
- `collision_loser` - Device lost tie-breaker and aborted send
- `transfer_aborted` - Device aborted photo send due to collision
- `photo_transfer_completed` - Photo successfully transferred from A to B
- `photo_cached` - Device has cached photo with specific hash
- `photo_not_sent` - Device skipped sending (recipient already has it)

### 6. Photo Transfer Flow

#### Initial Connection (both devices have photos):
1. Device A connects to Device B
2. A sends handshake: `tx_hash=A123`, `rx_hash=""` (never met before)
3. B receives handshake, stores A's tx_hash
4. B sends handshake: `tx_hash=B456`, `rx_hash=""`
5. A receives handshake, stores B's tx_hash
6. A compares: B's rx_hash ("") != our tx_hash ("A123") → **SEND PHOTO**
7. B compares: A's rx_hash ("") != our tx_hash ("B456") → **SEND PHOTO**
8. **Collision happens!** Both try to send simultaneously

#### Collision Resolution:
1. Both devices detect incoming metadata while sending
2. Compare device names: "Pixel 8 Pro" vs "iPhone 15 Pro"
3. "Pixel 8 Pro" > "iPhone 15 Pro" (lexicographic)
4. Pixel continues sending, iPhone aborts and receives
5. After Pixel→iPhone transfer completes, iPhone retries its send
6. iPhone→Pixel transfer completes

#### Reconnection (photos already exchanged):
1. Device A connects to Device B again
2. A sends handshake: `tx_hash=A123`, `rx_hash=B456` (we have B's photo)
3. B sends handshake: `tx_hash=B456`, `rx_hash=A123` (we have A's photo)
4. A compares: B's rx_hash ("A123") == our tx_hash ("A123") → **SKIP SEND**
5. B compares: A's rx_hash ("B456") == our tx_hash ("B456") → **SKIP SEND**
6. No photo transfer needed!

#### Photo Update Scenario:
1. Device A updates their photo (A123 → A789)
2. A connects to B (who still has A123 cached)
3. A sends handshake: `tx_hash=A789`, `rx_hash=B456`
4. B sends handshake: `tx_hash=B456`, `rx_hash=A123` (old hash!)
5. A compares: B's rx_hash ("A123") != our tx_hash ("A789") → **SEND PHOTO**
6. B compares: A's rx_hash ("B456") == our tx_hash ("B456") → **SKIP SEND**
7. Only A→B transfer happens (unidirectional)

### 7. Wire Protocol Extensions

#### Handshake Message (write to text characteristic):
```json
{
  "op": "handshake",
  "data": "<base64-encoded-protobuf>",
  "timestamp": 1234567890,
  "sender": "device-uuid"
}
```

#### Photo Metadata Message:
```json
{
  "op": "photo_metadata",
  "photo_hash": "abc123...",
  "photo_size": 52428,
  "chunk_count": 52,
  "timestamp": 1234567890,
  "sender": "device-uuid"
}
```

#### Photo Chunk Message:
```json
{
  "op": "photo_chunk",
  "chunk_index": 0,
  "chunk_data": "<base64-encoded-bytes>",
  "timestamp": 1234567890,
  "sender": "device-uuid"
}
```

#### Photo ACK Message:
```json
{
  "op": "photo_ack",
  "success": true,
  "transfer_crc": 3735928559,
  "timestamp": 1234567890,
  "sender": "device-uuid"
}
```

## Implementation Plan

### Phase 1: Device Cache Manager ✅
- Create `wire/cache.go` with DeviceCacheManager
- SHA-256 hashing for photos
- Persistent storage in device directories
- Photo version tracking

### Phase 2: Handshake Actions
- Implement `handleSendHandshake()` - create and send protobuf message
- Implement `handleReceiveHandshake()` - parse and store handshake
- Implement `handleDiscoverServices()` - read remote GATT
- Update `SimulatedDevice` to track handshake state

### Phase 3: Photo Transfer Actions
- Implement `handleSendPhotoMetadata()` - initiate transfer
- Implement `handleReceivePhotoMetadata()` - receive transfer request
- Implement photo chunking and reassembly
- Implement photo ACK with CRC-32 verification

### Phase 4: Collision Detection
- Detect simultaneous send/receive
- Implement tie-breaker logic (lexicographic device name)
- Handle abort and retry

### Phase 5: Assertions
- Implement all assertion types
- Track state changes for validation
- Verify cache persistence

### Phase 6: Full Scenarios
- `photo_collision.json` - simultaneous send with collision
- `photo_reconnect.json` - reconnect after exchange (should skip)
- `photo_update.json` - one device updates photo (unidirectional send)

## Testing Strategy

1. **Unit Tests**: DeviceCacheManager hash tracking
2. **Integration Tests**: Handshake exchange between iOS and Android
3. **Scenario Tests**: Full photo exchange with collision
4. **Cache Persistence**: Verify reconnection skips transfer
5. **Update Detection**: Verify changed photos trigger new transfer

## Key Design Decisions

1. **No real BLE**: Simulator only, filesystem-based communication
2. **Protobuf via wire protocol**: Messages sent as base64 in JSON
3. **SHA-256 for hashing**: Matches iOS/Android implementation
4. **Lexicographic tie-breaker**: Simple, deterministic collision resolution
5. **Deterministic scenarios**: Precise timing control for testing
