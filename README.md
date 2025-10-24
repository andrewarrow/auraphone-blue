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

