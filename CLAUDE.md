# refactor

we are in the middle of a refactor read PLAN.md ~/Documents/fix.txt and PLAN_IOS.md

# how to read results from a run

read ~/Documents/1.txt look at `ls -lR ~/.auraphone-blue-data/` gossip_audit.jsonl, socket_health.json, photo_timeline.jsonl, connection_events.jsonl, and test_report_* files. 

example:

/Users/aa/.auraphone-blue-data/09727F14-58C6-4A76-A7E8-A43E5C161F1E/gossip_audit.jsonl
~/.auraphone-blue-data/09727F14-58C6-4A76-A7E8-A43E5C161F1E/gossip_audit.jsonl
~/.auraphone-blue-data/88716210-2F9F-483B-ACA4-808CF56923BB/socket_health.json
~/.auraphone-blue-data/09727F14-58C6-4A76-A7E8-A43E5C161F1E/photo_timeline.jsonl
~/.auraphone-blue-data/09727F14-58C6-4A76-A7E8-A43E5C161F1E/connection_events.jsonl


# notes

you can edit the code just please do not run go build or go run yourself. Just edit the code and tell me when you are done and I'll run go run or go build.

make sure never to write logic that cheats. this program is a simulator to simulate real phones
using real bluetooth. if you cheat and do things like instantly transfer a photo from one phone
together without actually sending it "down the wire" or "over the air" in bluetooth case, you
are not helping anyone. The files you have in phone/ iphone/ android/ and wire/ need to work 
and act like real phones. They cache things to disk. When they start up if that cache is empty
then they have no photo to display. No cheating and just using the fact that the test photos are in
testdata/*.jpg and you know the path and could just display the photo.

its ok to make iphone/iphone.go and android/android.go not share any code and be very UN-dry 
if they are doing things with their respecitive swift/ and kotlin/ libraries. But if possible
ok to let them share code in phone/ package.

testdata/hardware_uuids.txt are the never changing UUIDs of the bluetooth radio. Make sure
no logic uses these to map a device when it should be using the base36 device id. The only time
this hardware UUID is the main id for a device is before the 1st handshake that gives us the base36 id.
But after that 1st handshake, in the rest of the code to map which photo goes to which device, the
primary key is the base36 id.

UUID/DeviceID Usage Rules:
- Hardware UUIDs are used for: BLE wire communication, connection state, role negotiation, and all
  connection-scoped state (like lastHandshakeTime, photoSendInProgress).
- Device IDs (base36) are used for: persistent storage (photos, metadata), user-visible data, and
  application-level mappings (deviceIDToPhotoHash, receivedPhotoHashes).

data directory is ~/.auraphone-blue-data


# Auraphone Blue - Fake Bluetooth Simulator

## Overview
This project simulates Bluetooth Low Energy (BLE) communication between iOS and Android devices using **Unix domain sockets** for data transfer. It provides Go implementations of iOS CoreBluetooth and Android Bluetooth APIs that behave like the real platform APIs but communicate via local IPC sockets instead of radio. The filesystem is used only for device discovery and metadata (GATT tables, advertising data).

## Architecture

### Packages
- **`swift/`** - iOS CoreBluetooth API simulation
  - `CBCentralManager` - Scanning and connecting to peripherals
  - `CBPeripheral` - Peripheral device representation with read/write capabilities
  - Delegate pattern matching iOS conventions

- **`kotlin/`** - Android Bluetooth API simulation
  - `BluetoothManager` / `BluetoothAdapter` - Android BLE stack
  - `BluetoothLeScanner` - Device discovery
  - `BluetoothDevice` / `BluetoothGatt` - GATT client for connections and data transfer
  - Callback pattern matching Android conventions

- **`wire/`** - Unix domain socket communication layer
  - Each device creates a Unix socket at `/tmp/auraphone-{hardwareUUID}.sock`
  - Connection-oriented communication (each device can connect to multiple peers)
  - Length-prefixed message framing (4-byte length header + JSON payload)
  - Automatic MTU-based fragmentation with realistic packet loss simulation
  - Filesystem used for: device discovery (scanning `/tmp/` for `.sock` files), GATT tables (`data/{uuid}/gatt.json`), and advertising data (`data/{uuid}/advertising.json`)

- **`phone/`** - Shared phone utilities and protocols
  - `DeviceCacheManager` - Persistent photo and metadata storage
  - `MeshView` - Gossip protocol for mesh network state management
  - `PhotoTransferCoordinator` - Centralized photo transfer state tracking
  - Characteristic UUID constants (`AuraServiceUUID`, `AuraProtocolCharUUID`, `AuraPhotoCharUUID`, `AuraProfileCharUUID`)

### Current Capabilities
✅ Device discovery (iOS discovers Android, Android discovers iOS) via Unix socket scanning
✅ **Advertising data** - Devices broadcast service UUIDs, device name, manufacturer data, TX power
✅ **Unix domain socket communication** - Length-prefixed messages with realistic BLE simulation
✅ Connection establishment (both directions) with state tracking
✅ Data transmission (write characteristic) via connected sockets
✅ Data reception (notifications/indications) via socket read loops
✅ **Gossip protocol** - Mesh network for decentralized device/photo discovery
✅ **Photo transfer coordination** - Centralized state machine for reliable photo delivery
✅ Clean logging with platform prefixes

## Known Limitations & Unrealistic Behaviors

### What's Now Realistic (✅ Fixed):
1. **✅ MTU Limits** - BLE has 23-512 byte packet limits (default: 185 bytes). Data is automatically fragmented into MTU-sized chunks.

2. **✅ Connection Timing** - Connections take 30-100ms with realistic ~0.1% failure rate. Connection states (disconnected/connecting/connected/disconnecting) match real BLE.

3. **✅ Discovery Delays** - Advertising intervals (100ms default) with discovery delays (50-500ms). Devices are discovered gradually, not instantly.

4. **✅ RSSI/Signal Strength** - Distance-based RSSI (-100 to -20 dBm) with realistic 10dBm variance simulating radio interference.

5. **✅ Packet Loss & Retries** - ~0.1% packet loss rate with automatic retries (up to 3 attempts). Overall success rate: ~99.9% (tuned for reliable Unix socket communication).

6. **✅ Device Roles & Negotiation** - Both iOS and Android support dual-role (Central+Peripheral). Smart role negotiation prevents connection conflicts:
   - **All device pairs**: Simple hardware UUID comparison - device with LARGER UUID acts as Central
   - Platform-agnostic: Works identically for iOS↔iOS, iOS↔Android, and Android↔Android connections
   - Deterministic collision avoidance prevents simultaneous connection attempts

7. **✅ Platform-Specific Reconnection Behavior** - iOS and Android have completely different reconnection behaviors:
   - **iOS Auto-Reconnect**: When you call `Connect()`, iOS remembers the peripheral. If connection drops, iOS automatically retries in background until it succeeds. App just waits for `DidConnectPeripheral` callback again.
   - **Android Manual Reconnect**: By default (autoConnect=false), Android does NOT auto-reconnect. App must detect disconnect and manually call `connectGatt()` again.
   - **Android Auto-Reconnect Mode**: Optional autoConnect=true parameter makes Android retry in background like iOS (but with longer delays ~5s vs iOS ~2s).

### Intentional Simplifications:
- **Simplified collision detection** - Real BLE has sophisticated channel hopping and collision avoidance. We simulate this at the application layer with deterministic role negotiation.

### What's Realistic:
✅ API naming matches real iOS CoreBluetooth and Android BLE
✅ Delegate/Callback patterns match platform conventions
✅ Async operations (discovery runs in background goroutines)
✅ UUID-based device identification
✅ Binary data payloads
✅ Connection-oriented communication with realistic timing
✅ **Proper GATT hierarchy** - Services, Characteristics, and Descriptors with UUIDs
✅ **Service discovery** - Devices read gatt.json to discover remote GATT tables
✅ **Characteristic-based operations** - Read/write/notify operations reference specific characteristics
✅ **Property validation** - Characteristics have properties (read, write, notify, indicate)
✅ **Advertising data** - Devices broadcast service UUIDs, device name, manufacturer data, TX power level in `advertising.json`
✅ **Advertising packet parsing** - iOS and Android parse advertising data matching platform APIs (kCBAdvData* and ScanRecord)
✅ **Device roles & negotiation** - Both iOS and Android are dual-role with smart role arbitration
✅ **Connection states** - disconnected → connecting → connected → disconnecting with realistic timing
✅ **MTU negotiation** - Packet size limits with automatic fragmentation
✅ **Packet loss & retries** - ~98.4% overall success rate with realistic radio interference
✅ **RSSI variance** - Distance-based signal strength with realistic fluctuations
✅ **Platform-specific reconnection** - iOS auto-reconnect vs Android manual/auto modes
✅ **Peripheral mode** - iOS CBPeripheralManager and Android BluetoothLeAdvertiser for advertising and serving GATT data
✅ **Notifications/Indications** - Subscribe/unsubscribe mechanism matches real BLE CCCD descriptor writes

## Design Principles
- **Use real platform API names** - CBCentralManager, BluetoothGatt, etc.
- **Match real patterns** - Delegates on iOS, Callbacks on Android
- **Keep it simple** - Focus on core communication flow, not edge cases
- **Realistic wire protocol** - Unix domain sockets with length-prefixed framing, realistic timing delays, and packet loss simulation
- **No cheating** - Photos must be transferred "over the wire", devices cache to disk, no shortcuts using shared filesystem paths

## Implementation Details

### GATT Structure (✅ Implemented)
Each device has a `gatt.json` file in its root directory that defines its GATT database:
```json
{
  "services": [
    {
      "uuid": "E621E1F8-C36C-495A-93FC-0C247A3E6E5F",
      "type": "primary",
      "characteristics": [
        {
          "uuid": "E621E1F8-C36C-495A-93FC-0C247A3E6E5D",
          "properties": ["read", "write", "notify"]
        },
        {
          "uuid": "E621E1F8-C36C-495A-93FC-0C247A3E6E5E",
          "properties": ["write", "notify"]
        }
      ]
    }
  ]
}
```

### Advertising Data Structure (✅ Implemented)
Each device has an `advertising.json` file that defines what it broadcasts during discovery:
```json
{
  "device_name": "iPhone Test Device",
  "service_uuids": [
    "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
  ],
  "manufacturer_data": "AQIDBA==",
  "tx_power_level": 0,
  "is_connectable": true
}
```

**iOS Discovery Format:**
- Maps to CoreBluetooth's `advertisementData` dictionary with keys:
  - `kCBAdvDataLocalName` - device name
  - `kCBAdvDataServiceUUIDs` - array of service UUIDs
  - `kCBAdvDataManufacturerData` - raw bytes
  - `kCBAdvDataTxPowerLevel` - transmit power in dBm
  - `kCBAdvDataIsConnectable` - bool

**Android Discovery Format:**
- Mapped to `ScanRecord` object with fields:
  - `DeviceName` - device name string
  - `ServiceUUIDs` - array of service UUIDs
  - `ManufacturerData` - map of company ID to data bytes
  - `TxPowerLevel` - transmit power in dBm
  - `AdvertiseFlags` - advertising flags (0x06 = general discoverable)

### Wire Protocol (✅ Implemented)

#### Unix Domain Socket Communication
Each device creates a socket at `/tmp/auraphone-{hardwareUUID}.sock` and listens for connections.

**Message Framing:**
```
[4-byte length (big-endian)] [JSON message payload]
```

**Connection Flow:**
1. Device A connects to Device B's socket
2. Device A sends handshake: `[4-byte UUID length] [UUID bytes]`
3. Both devices enter connected state
4. Messages flow bidirectionally with length-prefixed framing
5. Each connection has dedicated read/write loops

**Characteristic Message Format (JSON payload):**
```json
{
  "op": "write",           // or "notify", "indicate"
  "service": "E621E1F8-C36C-495A-93FC-0C247A3E6E5F",
  "characteristic": "E621E1F8-C36C-495A-93FC-0C247A3E6E5D",
  "data": [104, 105, ...], // binary data as byte array
  "timestamp": 1234567890,
  "sender": "device-uuid"
}
```

**Realistic Behavior:**
- MTU-based fragmentation (default 185 bytes, configurable 23-512)
- Packet loss simulation (~0.1% per message)
- Automatic retries (up to 3 attempts with 50ms delay)
- Connection state tracking (disconnected → connecting → connected → disconnecting)
- Random disconnect simulation (rare, configurable)

### API Changes (✅ Implemented)

#### Central Mode (Scanning & Connecting)
- **iOS**: `CBCentralManager` and `CBPeripheral` for scanning and connecting
  - `centralManager.ScanForPeripherals()` discovers advertising devices
  - `peripheral.DiscoverServices()` reads remote gatt.json
  - `peripheral.WriteValue(data, characteristic)` sends to specific characteristic
  - `peripheral.SetNotifyValue(enabled, characteristic)` subscribes to notifications
  - `peripheral.GetCharacteristic(serviceUUID, charUUID)` lookup helper

- **Android**: `BluetoothLeScanner` and `BluetoothGatt` for scanning and connecting
  - `scanner.StartScan(callback)` discovers advertising devices
  - `gatt.DiscoverServices()` reads remote gatt.json
  - `gatt.WriteCharacteristic(characteristic)` sends characteristic.Value
  - `gatt.SetCharacteristicNotification(characteristic, enable)` subscribes to notifications
  - `gatt.GetCharacteristic(serviceUUID, charUUID)` lookup helper

#### Peripheral Mode (Advertising & Serving) ✅ NEW
- **iOS**: `CBPeripheralManager` for advertising and GATT server
  - `peripheralManager.AddService(service)` adds services to local GATT database
  - `peripheralManager.StartAdvertising(data)` begins advertising
  - `peripheralManager.UpdateValue(value, characteristic, centrals)` sends notifications
  - Delegate callbacks: `DidReceiveReadRequest`, `DidReceiveWriteRequests`, `CentralDidSubscribe`

- **Android**: `BluetoothLeAdvertiser` and `BluetoothGattServer` for advertising and GATT server
  - `gattServer.AddService(service)` adds services to local GATT database
  - `advertiser.StartAdvertising(settings, data, callback)` begins advertising
  - `gattServer.NotifyCharacteristicChanged(device, characteristic, confirm)` sends notifications
  - Callbacks: `OnCharacteristicReadRequest`, `OnCharacteristicWriteRequest`, `OnConnectionStateChange`

## Gossip Protocol (✅ Implemented)

The system uses a gossip-based mesh protocol to efficiently discover devices and photos across the network without requiring full mesh connectivity.

### Overview
- **Location**: `phone/mesh_view.go` (414 lines)
- **Purpose**: Decentralized device and photo discovery via periodic information exchange
- **Protocol**: Protobuf messages (`proto.GossipMessage`, `proto.DevicePhotoState`)

### Key Components

#### MeshView State Manager
Each device maintains a `MeshView` that tracks:
- **All known devices** in the mesh (map: deviceID → MeshDeviceState)
- **Current neighbors** (up to 3 devices, deterministically selected)
- **Gossip timing** (5-second intervals, configurable)
- **Photo availability** (which photos we have cached, which we need)

```go
type MeshDeviceState struct {
    DeviceID         string    // Base36 logical ID
    PhotoHash        string    // SHA-256 hash (hex) of their photo
    LastSeenTime     time.Time // When we last heard about this device
    FirstName        string    // Cached name for display
    HavePhoto        bool      // Do we have this photo cached?
    PhotoRequestSent bool      // Have we requested this photo?
}
```

#### Neighbor Selection (Deterministic)
- Uses **consistent hashing**: `sha256(ourDeviceID + theirDeviceID)`
- Selects devices with **lowest N hash values** as neighbors
- Maximum 3 neighbors per device (configurable via `maxNeighbors`)
- Provides stable topology - same neighbors across restarts

#### Gossip Exchange Flow
1. **Every 5 seconds** (configurable), device checks if it should gossip
2. **Build gossip message** with complete mesh view (all known devices + photo hashes)
3. **Send to all current neighbors** via `AuraProtocolCharUUID` characteristic
4. **Receive gossip from neighbors**, merge into local mesh view
5. **Detect new devices/photos**, trigger photo requests if needed

#### Photo Discovery
```go
// Check if we learned about new photos
newDiscoveries := meshView.MergeGossip(gossipMsg)

// Get list of photos we need to fetch
missingPhotos := meshView.GetMissingPhotos()

// Request photos we don't have
for _, device := range missingPhotos {
    sendPhotoRequest(device.DeviceID, device.PhotoHash)
    meshView.MarkPhotoRequested(device.DeviceID)
}
```

#### Persistence
- Mesh view saved to `data/{uuid}/cache/mesh_view.json`
- Survives device restarts
- Atomic writes (temp file + rename pattern)

### Protobuf Messages

**GossipMessage** (proto/handshake.proto lines 56-61):
```protobuf
message GossipMessage {
  string sender_device_id = 1;
  int64 timestamp = 2;
  repeated DevicePhotoState mesh_view = 3;
  int32 gossip_round = 4;
}
```

**DevicePhotoState** (proto/handshake.proto lines 47-52):
```protobuf
message DevicePhotoState {
  string device_id = 1;
  bytes photo_hash = 2;           // SHA-256 (32 bytes)
  int64 last_seen_timestamp = 3;
  string first_name = 4;
}
```

**PhotoRequestMessage** (proto/handshake.proto lines 65-69):
```protobuf
message PhotoRequestMessage {
  string requester_device_id = 1;
  string target_device_id = 2;
  bytes photo_hash = 3;
}
```

### Gossip Benefits
- **Scalability**: Devices only connect to 3 neighbors, not full mesh
- **Efficient discovery**: Learn about all devices via multi-hop gossip
- **Photo distribution**: Photos spread virally through the network
- **Resilience**: No single point of failure, self-healing topology
- **Deterministic**: Same neighbor selection across all devices ensures connectivity

## Role Negotiation

Both iOS and Android devices support dual-role BLE (can act as Central or Peripheral). To prevent connection conflicts when two devices discover each other simultaneously, the system uses simple UUID-based role arbitration:

```go
// Simple rule: Device with larger hardware UUID acts as Central
device1 := wire.NewWireWithPlatform(uuid1, wire.PlatformIOS, "iPhone 15", nil)
device2 := wire.NewWireWithPlatform(uuid2, wire.PlatformAndroid, "Pixel 8", nil)

// If uuid1 > uuid2, device1 connects to device2
// If uuid2 > uuid1, device2 connects to device1
shouldConnect := device1.ShouldActAsCentral(device2) // true if uuid1 > uuid2

// Works identically for all platform combinations:
// iOS ↔ iOS, iOS ↔ Android, Android ↔ Android
```

**Rules:**
1. **All device pairs**: Device with lexicographically larger hardware UUID acts as Central
   - Example: UUID starting with "b1..." > UUID starting with "5a..." → "b1" device connects
   - Platform-agnostic: Same rule applies regardless of iOS or Android
   - Deterministic role assignment prevents simultaneous connection attempts
2. **Every pairing is deterministic**: Given any two hardware UUIDs, exactly one device will always initiate
   - With 4 devices (A, B, C, D), if A > B > C > D, then:
     - A connects to B, C, D (acts as Central for all)
     - B connects to C, D (acts as Central for lower UUIDs, Peripheral for A)
     - C connects to D only (acts as Central for D, Peripheral for A and B)
     - D waits for all others (acts as Peripheral for all)

## BLE Simulation Configuration

The simulator provides realistic BLE behavior with ~99.9% success rate (tuned for Unix sockets):

### Default Parameters (wire.DefaultSimulationConfig())
```go
MinMTU: 23 bytes                    // BLE 4.0 minimum
MaxMTU: 512 bytes                   // BLE 5.0+ maximum
DefaultMTU: 185 bytes               // Common negotiated value

MinConnectionDelay: 30ms            // Fast connection
MaxConnectionDelay: 100ms           // Typical max
ConnectionFailureRate: 0.1%         // Very reliable (Unix sockets are stable)

AdvertisingInterval: 100ms          // Apple recommended
MinDiscoveryDelay: 50ms             // First advertising packet
MaxDiscoveryDelay: 500ms            // Discovery window

BaseRSSI: -50 dBm                   // Close range (~1m)
RSSIVariance: 10 dBm                // Radio interference

PacketLossRate: 0.1%                // Per-packet loss (very low for sockets)
MaxRetries: 3                       // Automatic retries
RetryDelay: 50ms                    // Between retries

NotificationLatency: 10-20ms        // Delay for notify/indicate operations

Overall Success Rate: ~99.9%        // After all retries
```

### Unix Socket Implementation Details
- **Socket Path**: `/tmp/auraphone-{hardwareUUID}.sock`
- **Framing**: 4-byte big-endian length prefix + JSON payload
- **Connection Handshake**: 4-byte UUID length + UUID bytes
- **Concurrency**: Per-connection mutexes for send operations, dedicated read/write goroutines
- **Cleanup**: Sockets automatically cleaned up on graceful shutdown

### Perfect Mode for Testing (wire.PerfectSimulationConfig())
- Zero delays, zero failures, deterministic behavior
- Use for unit tests and reproducible scenarios

### Custom Configuration
```go
config := wire.DefaultSimulationConfig()
config.PacketLossRate = 0.05  // Increase to 5% for poor conditions
config.Distance = 5.0         // Set distance for RSSI calculation
config.Deterministic = true   // Reproducible for scenarios
config.Seed = 12345          // Fixed random seed
```

## Completed Features
- [x] Unix domain socket communication with length-prefixed framing
- [x] Service/characteristic UUID structure
- [x] Advertising data (service UUIDs, device name, manufacturer data)
- [x] Advertising intervals with discovery delays
- [x] Connection timing delays and state machine
- [x] MTU negotiation and packet fragmentation
- [x] Connection failures and retries with realistic success rates
- [x] Dual-role support (both iOS and Android can be Central+Peripheral)
- [x] Deterministic role negotiation via UUID comparison
- [x] RSSI modeling based on distance with variance
- [x] iOS peripheral mode (CBPeripheralManager)
- [x] Android peripheral mode (BluetoothLeAdvertiser + BluetoothGattServer)
- [x] Subscribe/unsubscribe protocol for notifications/indications
- [x] **Gossip protocol** for mesh network device/photo discovery
- [x] **Centralized photo transfer coordinator** for reliable delivery
- [x] **Persistent mesh view** with atomic disk writes

## Future Improvements
- [ ] Photo transfer via gossip (currently uses direct connections only)
- [ ] Multi-hop photo routing (fetch photos from neighbors who have them)
- [ ] Gossip message compression (currently sends full mesh view each time)
- [ ] Dynamic neighbor adjustment based on network conditions
- [ ] Mesh topology visualization/debugging tools

---

## Recent Architecture Changes (Commits 03ee4d3 & 77c4f52)

### Commit 03ee4d3: "big gossip change"
**Summary**: Introduced gossip protocol with protobuf message definitions and refactored iOS/Android to use centralized UUID constants.

**Key Changes**:
1. **New Protobuf Messages** (`proto/handshake.proto`):
   - `GossipMessage` - Contains sender's complete mesh view
   - `DevicePhotoState` - Individual device state (ID, photo hash, timestamp, name)
   - `PhotoRequestMessage` - Request specific photo from mesh

2. **Centralized UUID Constants** (`phone/constants.go`):
   - Moved hardcoded UUIDs from `iphone/iphone.go` and `android/android.go` to shared package
   - `AuraServiceUUID`, `AuraProtocolCharUUID`, `AuraPhotoCharUUID`, `AuraProfileCharUUID`
   - Eliminates duplicate UUID strings across platform implementations

3. **Code Cleanup**:
   - Removed ~141 lines of duplicate UUID declarations
   - iOS and Android now import constants from `phone` package
   - More DRY approach while keeping platform-specific logic separate

### Commit 77c4f52: "big gossip change with files"
**Summary**: Implemented complete gossip protocol infrastructure with mesh view management and persistence.

**Key Changes**:
1. **MeshView Manager** (`phone/mesh_view.go` - 414 lines):
   - `MeshView` struct tracks all known devices and their photo states
   - Deterministic neighbor selection via consistent hashing
   - Gossip interval management (5-second default)
   - Photo availability tracking (HavePhoto, PhotoRequestSent flags)

2. **Core Gossip Features**:
   - `UpdateDevice()` - Add/update device in mesh view
   - `MergeGossip()` - Integrate neighbor's mesh view into ours, returns new discoveries
   - `GetMissingPhotos()` - Find photos we need to fetch
   - `BuildGossipMessage()` - Create gossip payload with complete mesh state
   - `SelectRandomNeighbors()` - Pick 3 deterministic neighbors via SHA-256(ourID + theirID)

3. **Persistence**:
   - Mesh view saved to `data/{uuid}/cache/mesh_view.json`
   - Atomic writes (temp file + rename)
   - Automatic loading on device startup

4. **Gossip Benefits**:
   - **Scalability**: O(log N) connections instead of O(N²) full mesh
   - **Epidemic spread**: Information propagates exponentially across network
   - **Resilience**: No single point of failure, self-organizing topology
   - **Battery efficiency**: Maintain only 3 connections instead of N-1

### Integration Points
These commits lay the foundation for mesh-based photo distribution:
- **Direct connections** still used for handshakes and photo transfers (current implementation)
- **Gossip protocol** spreads awareness of which devices exist and what photos they have
- **Future work**: Multi-hop photo routing (fetch photos from neighbors instead of original source)

### Architecture Impact
Before: Devices discover each other via socket scanning and exchange photos via direct connections.
After: Devices still use direct connections, but gossip protocol provides decentralized discovery of devices N-hops away, enabling future viral photo distribution without full mesh connectivity.
