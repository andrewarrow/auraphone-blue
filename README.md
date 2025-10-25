# Auraphone Blue - Fake Bluetooth Simulator

A Go-based Bluetooth Low Energy (BLE) simulator that uses filesystem-based message passing to simulate iOS and Android BLE communication without requiring real devices.

## Why This Exists

I've been working on a bluetooth only ios and android app for a few months now. Been through lots of different ways to test. I ran multiple real phones from my macbook. I wrote a golang program using [github.com/go-ble/ble](https://github.com/go-ble/ble) that actually works and connects from the macbook to a phone. But in the end to really get the level of testing I needed I started this repo.

Which is a 100% go program but it has a "swift" package with [cb_central_manager.go](swift/cb_central_manager.go), [cb_peripheral_manager.go](swift/cb_peripheral_manager.go), and [cb_peripheral.go](swift/cb_peripheral.go). And a "kotlin" package with [bluetooth_device.go](kotlin/bluetooth_device.go), [bluetooth_gatt.go](kotlin/bluetooth_gatt.go) and [bluetooth_manager.go](kotlin/bluetooth_manager.go). These simulate the real ios and android bluetooth stacks with all their subtle differences.

Using go's fyne GUI I made the actual phone "apps" and can run many android phones and many iphones. The filesystem is used to write data "down the wire" or "over the air" since this is bluetooth.

![screenshot](https://i.imgur.com/Io3OZ5x.png)

To test complex scenarios like 7 iphones and 4 androids all running at the same time I run this gui and keep fine tuning the logic and fixing all the edge cases. Then I move this logic from go back to real kotlin and swift for the real apps. The ios app is live in the app store:

https://apps.apple.com/us/app/auraphone/id6752836343

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
go mod tidy
go run main.go
```

