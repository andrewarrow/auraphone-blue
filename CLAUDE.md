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

### What's Too Fake (and should be fixed):
1. **No MTU Limits** - Real BLE has 20-512 byte packet limits requiring fragmentation. We write files of any size.

2. **Instant Connection** - Real BLE connections take 30ms-100ms and can fail. We connect synchronously with no failure modes or timing.

3. **Instant Discovery** - Real BLE has advertising intervals (typically 100ms-10s) and scan windows. We discover immediately when directories exist.

5. **No Connection States** - Real BLE has connecting/connected/disconnecting states with timing. We switch instantly.

6. **No RSSI/Signal Strength** - Real BLE has distance-based signal strength (-100 to 0 dBm). We return fixed dummy values.

7. **No Radio Interference** - Real BLE has packet loss, retries, collisions. Our filesystem is 100% reliable.

### What's Acceptable Given the Architecture:
- **Polling for reads (50ms)** - Real BLE uses notifications/indications, but filesystem polling is reasonable here. Alternatives like filesystem watchers are OS-specific, and Go channels would bypass the wire abstraction.

- **100% reliable delivery** - Filesystem guarantees delivery. Simulating packet loss would require artificial randomness.

- **Both Central & Peripheral roles** - While real iOS is typically Central-only, having both roles helps demonstrate full communication.

### What's Realistic:
✅ API naming matches real iOS CoreBluetooth and Android BLE
✅ Delegate/Callback patterns match platform conventions
✅ Async operations (discovery runs in background goroutines)
✅ UUID-based device identification
✅ Binary data payloads
✅ Connection-oriented communication
✅ **Proper GATT hierarchy** - Services, Characteristics, and Descriptors with UUIDs
✅ **Service discovery** - Devices read gatt.json to discover remote GATT tables
✅ **Characteristic-based operations** - Read/write/notify operations reference specific characteristics
✅ **Property validation** - Characteristics have properties (read, write, notify, indicate)
✅ **Advertising data** - Devices broadcast service UUIDs, device name, manufacturer data, TX power level in `advertising.json`
✅ **Advertising packet parsing** - iOS and Android parse advertising data matching platform APIs (kCBAdvData* and ScanRecord)

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
- **iOS**: `CBService` and `CBCharacteristic` types added
  - `peripheral.DiscoverServices()` reads remote gatt.json
  - `peripheral.WriteValue(data, characteristic)` sends to specific characteristic
  - `peripheral.GetCharacteristic(serviceUUID, charUUID)` lookup helper
- **Android**: `BluetoothGattService` and `BluetoothGattCharacteristic` types added
  - `gatt.DiscoverServices()` reads remote gatt.json
  - `gatt.WriteCharacteristic(characteristic)` sends characteristic.Value
  - `gatt.GetCharacteristic(serviceUUID, charUUID)` lookup helper

## Future Improvements Needed
- [x] Add service/characteristic UUID structure
- [x] Add advertising data (service UUIDs, device name, manufacturer data)
- [ ] Implement advertising intervals (currently instant)
- [ ] Add connection timing delays
- [ ] Support MTU negotiation and packet fragmentation
- [ ] Add notifications/indications instead of polling
- [ ] Simulate connection failures and retries
- [ ] Add peripheral mode (advertising/serving)
- [ ] Model RSSI based on "distance" between devices
