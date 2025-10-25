please do not run go build or go run yourself. Just edit the code and tell me when you are done.

make sure never to write logic that cheats. this program is a simulator to simulate real phones
using real bluetooth. if you cheat and do things like instantly transfer a photo from one phone
together without actually sending it "down the wire" or "over the air" in bluetooth case, you
are not helping anyone. The files you have in phone/ iphone/ android/ and wire/ need to work 
and act like real phones. They cache things to disk. When they start up if that cache is empty
then they have no photo to display. No cheating and just using the fact that the test photos are in
testdata/*.jpg and you know the path and could just display the photo.

its ok to make iphone/iphone.go and android/android.go not share any code and be very UN-dry
but also ok to let them share code in phone/ pacakge

ignore  tests/runner.go 
ignore tests/scenario.go
ignore cmd/

start from main.go that is active code


# Auraphone Blue - Fake Bluetooth Simulator

## Overview
This project simulates Bluetooth Low Energy (BLE) communication between iOS and Android devices using filesystem-based message passing. It provides Go implementations of iOS CoreBluetooth and Android Bluetooth APIs that behave like the real platform APIs but communicate via local files instead of radio.

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

- **`wire/`** - Filesystem-based communication layer
  - Each device gets a UUID and directory (`data/{uuid}/`)
  - `inbox/` and `outbox/` subdirectories for message passing
  - `gatt.json` file defines each device's GATT table (services/characteristics)
  - Discovery works by scanning for other UUID directories
  - Data transfer via JSON message files with service/characteristic UUIDs

### Current Capabilities
✅ Device discovery (iOS discovers Android, Android discovers iOS)
✅ **Advertising data** - Devices broadcast service UUIDs, device name, manufacturer data, TX power
✅ Connection establishment (both directions)
✅ Data transmission (write characteristic)
✅ Data reception (read characteristic with polling)
✅ Clean logging with platform prefixes

## Known Limitations & Unrealistic Behaviors

### What's Now Realistic (✅ Fixed):
1. **✅ MTU Limits** - BLE has 23-512 byte packet limits (default: 185 bytes). Data is automatically fragmented into MTU-sized chunks.

2. **✅ Connection Timing** - Connections take 30-100ms with realistic ~1.6% failure rate. Connection states (disconnected/connecting/connected/disconnecting) match real BLE.

3. **✅ Discovery Delays** - Advertising intervals (100ms default) with discovery delays (100ms-1s). Devices are discovered gradually, not instantly.

4. **✅ RSSI/Signal Strength** - Distance-based RSSI (-100 to -20 dBm) with realistic 10dBm variance simulating radio interference.

5. **✅ Packet Loss & Retries** - ~1.5% packet loss rate with automatic retries (up to 3 attempts). Overall success rate: ~98.4%.

6. **✅ Device Roles & Negotiation** - Both iOS and Android support dual-role (Central+Peripheral). Smart role negotiation prevents connection conflicts:
   - **iOS → iOS**: Lexicographic device UUID comparison - device with LARGER UUID acts as Central
   - **iOS → Android**: iOS acts as Central (initiates connection)
   - **Android → iOS**: Android acts as Peripheral (waits for iOS to connect)
   - **Android → Android**: Lexicographic device name comparison - device with LARGER name acts as Central

7. **✅ Platform-Specific Reconnection Behavior** - iOS and Android have completely different reconnection behaviors:
   - **iOS Auto-Reconnect**: When you call `Connect()`, iOS remembers the peripheral. If connection drops, iOS automatically retries in background until it succeeds. App just waits for `DidConnectPeripheral` callback again.
   - **Android Manual Reconnect**: By default (autoConnect=false), Android does NOT auto-reconnect. App must detect disconnect and manually call `connectGatt()` again.
   - **Android Auto-Reconnect Mode**: Optional autoConnect=true parameter makes Android retry in background like iOS (but with longer delays ~5s vs iOS ~2s).

### Intentional Simplifications:
- **Polling for reads (50ms)** - Real BLE uses notifications/indications, but filesystem polling is reasonable here. Alternatives like filesystem watchers are OS-specific, and Go channels would bypass the wire abstraction.

- **Simplified collision detection** - Real BLE has sophisticated channel hopping and collision avoidance. We simulate this at the application layer.

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
- **Visible wire protocol** - Filesystem makes debugging easy

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
Characteristic operations are sent as JSON message files in inbox/outbox:
```json
{
  "op": "write",
  "service": "1800",
  "characteristic": "2A00",
  "data": [104, 105],
  "timestamp": 1234567890,
  "sender": "device-uuid"
}
```

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

## Role Negotiation

Both iOS and Android devices support dual-role BLE (can act as Central or Peripheral). To prevent connection conflicts when two devices discover each other simultaneously, the system uses smart role arbitration:

```go
// iOS devices use UUID comparison for iOS-to-iOS
ios1 := wire.NewWireWithPlatform(uuid1, wire.PlatformIOS, "iPhone 15", nil)
ios2 := wire.NewWireWithPlatform(uuid2, wire.PlatformIOS, "iPhone 14", nil)

// Device with larger UUID acts as Central
shouldConnect := ios1.ShouldActAsCentral(ios2) // Compares UUIDs lexicographically

// iOS to Android: iOS always acts as Central
iosWire := wire.NewWireWithPlatform(uuidIOS, wire.PlatformIOS, "iPhone 15", nil)
androidWire := wire.NewWireWithPlatform(uuidAndroid, wire.PlatformAndroid, "Pixel 8", nil)

// iOS initiates connection to Android
shouldConnect := iosWire.ShouldActAsCentral(androidWire) // true

// Android devices use device name comparison for Android-to-Android
android1 := wire.NewWireWithPlatform(uuid1, wire.PlatformAndroid, "Pixel 8", nil)
android2 := wire.NewWireWithPlatform(uuid2, wire.PlatformAndroid, "Samsung S23", nil)

// "Pixel 8" > "Samsung S23" lexicographically, so Pixel acts as Central
shouldConnect := android1.ShouldActAsCentral(android2) // true
```

**Rules:**
1. **iOS → iOS**: Device with lexicographically larger UUID acts as Central
   - Example: UUID starting with "b1..." > UUID starting with "5a..." → "b1" device connects
   - Deterministic role assignment prevents simultaneous connection attempts
2. **iOS → Android**: iOS always acts as Central (initiates connection)
3. **Android → iOS**: Android acts as Peripheral (waits for iOS)
4. **Android → Android**: Device with lexicographically larger name acts as Central
   - Example: "Pixel 8" > "Galaxy S23" → Pixel connects to Galaxy
   - Prevents simultaneous connection attempts

## BLE Simulation Configuration

The simulator provides realistic BLE behavior with ~98.4% success rate:

### Default Parameters (wire.DefaultSimulationConfig())
```go
MinMTU: 23 bytes                    // BLE 4.0 minimum
MaxMTU: 512 bytes                   // BLE 5.0+ maximum
DefaultMTU: 185 bytes               // Common negotiated value

MinConnectionDelay: 30ms            // Fast connection
MaxConnectionDelay: 100ms           // Typical max
ConnectionFailureRate: 1.6%         // Realistic failure rate

AdvertisingInterval: 100ms          // Apple recommended
MinDiscoveryDelay: 100ms            // First advertising packet
MaxDiscoveryDelay: 1000ms           // Discovery window

BaseRSSI: -50 dBm                   // Close range (~1m)
RSSIVariance: 10 dBm                // Radio interference

PacketLossRate: 1.5%                // Per-packet loss
MaxRetries: 3                       // Automatic retries
RetryDelay: 50ms                    // Between retries

Overall Success Rate: ~98.4%        // After all retries
```

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

## Future Improvements
- [x] Add service/characteristic UUID structure
- [x] Add advertising data (service UUIDs, device name, manufacturer data)
- [x] Implement advertising intervals
- [x] Add connection timing delays
- [x] Support MTU negotiation and packet fragmentation
- [x] Simulate connection failures and retries
- [x] Add device role enforcement (iOS dual, Android peripheral-only)
- [x] Model RSSI based on distance
- [x] Add peripheral mode for iOS (CBPeripheralManager)
- [x] Add peripheral mode for Android (BluetoothLeAdvertiser + BluetoothGattServer)
- [x] Add subscribe/unsubscribe protocol for notifications/indications
- [ ] Add filesystem watchers instead of polling (intentionally simplified for portability)
