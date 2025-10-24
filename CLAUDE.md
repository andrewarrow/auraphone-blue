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
  - Discovery works by scanning for other UUID directories
  - Data transfer via binary files

### Current Capabilities
✅ Device discovery (iOS discovers Android, Android discovers iOS)
✅ Connection establishment (both directions)
✅ Data transmission (write characteristic)
✅ Data reception (read characteristic with polling)
✅ Clean logging with platform prefixes

## Known Limitations & Unrealistic Behaviors

### What's Too Fake:
1. **Instant Discovery** - Real BLE has advertising intervals (typically 100ms-10s). We discover immediately when directories exist.

2. **No Advertising Data** - Real BLE peripherals broadcast service UUIDs and manufacturer data. We just see a directory.

3. **Instant Connection** - Real BLE connections take 30ms-100ms and can fail. We connect synchronously with no failure modes.

4. **No Service/Characteristic Model** - Real BLE has a hierarchy (Services → Characteristics → Descriptors). We just have a generic "write data" method.

5. **Polling for Reads** - Real BLE uses notifications/indications (push model). We poll the inbox every 50ms (pull model).

6. **No MTU Limits** - Real BLE has 20-512 byte packet limits. We write files of any size.

7. **No Connection States** - Real BLE has connecting/connected/disconnecting states with timing. We switch instantly.

8. **No RSSI/Signal Strength** - Real BLE has distance-based signal strength. We return fixed dummy values.

9. **Both Central & Peripheral** - Real iOS typically acts as Central only (pre-iOS 6). We have both roles on same device.

10. **No Radio Interference** - Real BLE has packet loss, retries, collisions. Our filesystem is 100% reliable.

### What's Realistic:
✅ API naming matches real iOS CoreBluetooth and Android BLE
✅ Delegate/Callback patterns match platform conventions
✅ Async operations (discovery runs in background goroutines)
✅ UUID-based device identification
✅ Binary data payloads
✅ Connection-oriented communication

## Design Principles
- **Use real platform API names** - CBCentralManager, BluetoothGatt, etc.
- **Match real patterns** - Delegates on iOS, Callbacks on Android
- **Keep it simple** - Focus on core communication flow, not edge cases
- **Visible wire protocol** - Filesystem makes debugging easy

## Future Improvements Needed
- [ ] Add service/characteristic UUID structure
- [ ] Implement proper advertising with intervals
- [ ] Add connection timing delays
- [ ] Support MTU negotiation and packet fragmentation
- [ ] Add notifications/indications instead of polling
- [ ] Simulate connection failures and retries
- [ ] Add peripheral mode (advertising/serving)
- [ ] Model RSSI based on "distance" between devices
