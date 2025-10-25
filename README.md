# Auraphone Blue - Fake Bluetooth Simulator

A Go-based Bluetooth Low Energy (BLE) simulator that uses filesystem-based message passing to simulate iOS and Android BLE communication without requiring real devices.

## Why This Exists

Testing real-world BLE edge cases is difficult:
- **Hardware Required:** Need multiple physical iOS and Android devices
- **Hard to Reproduce:** Edge cases like photo collision, stale handshakes, and connection race conditions are timing-dependent
- **Slow Iteration:** Each test requires deploying to devices, coordinating timing, and analyzing logs
- **Expensive:** Room full of devices costs $$$

This simulator lets you:
- ✅ Test complex multi-device scenarios on your laptop
- ✅ Reproduce edge cases deterministically with test scenarios
- ✅ Convert real app logs into replayable test cases
- ✅ Iterate rapidly without deploying to devices

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
  - `advertising.json` - BLE advertising data (service UUIDs, device name, TX power)
  - `gatt.json` - GATT table (services and characteristics)
  - Discovery works by scanning for other UUID directories
  - Data transfer via JSON message files

### Current Capabilities

✅ Device discovery with advertising data (service UUIDs, device name, manufacturer data)

✅ Connection establishment (both directions)

✅ GATT service and characteristic discovery

✅ Data transmission (write characteristic)

✅ Data reception (read characteristic with polling)

✅ **Test scenario framework** for edge case reproduction

✅ **Log parser** to convert mobile app logs into test scenarios

✅ Clean logging with platform prefixes

## Quick Start

```bash
go run main.go
```

