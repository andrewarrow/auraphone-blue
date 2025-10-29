# rules

always do what is realistic for real BLE communication on ios and android.
everything starts with the package wire/ and all its tests.

From there we have kotlin/ and swift/ packages that use wire/ pacakge.

wire/ is just the ble radio layer, nothing specific to ios or android goes in there.

kotlin/ and swift/ are just go versions of the same functions the real devices will have.

the iphone/ and android/ packages use swift/ and kotlin/ to make simulated phones.

# imporant

only work in the wire/ directory right now. We are refactoring it to be more real
and are making breaking changes to other packages.

  Real BLE:
  - BLE connections are always unidirectional in role: one device is Central, the other is
  Peripheral for that specific connection
  - A device can be Central to Device A and Peripheral to Device B, but on separate connections
  - The radio-level connection is asymmetric - Central manages the connection timing

Single Socket is CORRECT

  Here's why:

  ✅ Single Socket Matches Real BLE:

  1. One BLE connection = One socket ✅
    - Real BLE: One L2CAP channel per connection
    - Your model: One Unix socket per connection
  2. Full-duplex communication ✅
    - Real BLE: Both sides can send on the same connection
    - Your model: Both sides can read/write same socket
  3. Separate connections for mutual connectivity ✅
    - Real BLE: A→B and B→A are two connections
    - Your model: A→B and B→A are two sockets
  4. Connection asymmetry ✅
    - Real BLE: Central/Peripheral roles per connection
    - Your model: Each socket has role assignment

  ❌ Dual Socket Would Be WRONG:

  1. Over-complicated setup
    - Need handshake to establish second socket
    - Race conditions during setup
    - Coordination problems
  2. Not how real BLE works
    - Real BLE has ONE radio link per connection
    - Not two separate channels
  3. Confusion about "bidirectional"
    - Real BLE IS bidirectional (both can send/receive)
    - But roles are asymmetric (different allowed operations)
    - Single socket captures this correctly


# tests

always call util.SetRandom() at the start of each test

# ios vs android

**BLE itself is standardized at the protocol level**, both iOS and Android speak the same *over-the-air binary protocol*, but they expose it through **very different software abstractions, lifecycles, and caching behavior**.
Here’s a deep breakdown of how they diverge from the radio all the way up to your app code:

---

## ⚙️ 1. Stack Architecture

### **iOS (CoreBluetooth)**

* Entire BLE stack is **fully encapsulated in the OS**.
  Developers can’t touch raw Link Layer, L2CAP, or ATT directly — you only talk to Apple’s **CoreBluetooth framework**, which sits *above GATT*.
* Your app’s code (using `CBCentralManager`, `CBPeripheral`, etc.) triggers **GATT operations only**:

  * `readValue(for:)`
  * `writeValue(_:for:type:)`
  * `setNotifyValue(_:for:)`
* Advertising and scanning are handled by the OS via an internal daemon (`bluetoothd`), with strict throttling and privacy rules.

> 🧩 Apple acts as a strict gatekeeper — the device’s radio always goes through the system BLE stack, which enforces pairing, caching, and privacy policies.

---

### **Android (BluetoothGatt / BluetoothLeScanner)**

* Android exposes a **thinner abstraction** over the BLE stack.

  * The app communicates with the **Bluetooth HAL (Hardware Abstraction Layer)** via Binder IPC.
  * You can use **BluetoothGatt**, **BluetoothGattServer**, **BluetoothGattCharacteristic**, etc.
* You get access to **central and peripheral roles** (most Androids support both).
* Android allows slightly lower-level operations, like raw **L2CAP CoC (Credit-Based Channels)** and **GATT server implementation**, not just client-side GATT.

> 🧠 Android lets developers build nearly the whole ATT/GATT layer in user space if needed.

---

## 📡 2. Advertising & Scanning Differences

| Aspect               | **iOS**                                                                                  | **Android**                                                                                         |
| -------------------- | ---------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| Advertising Interval | Limited by Apple; min ~100 ms, often throttled when in background.                       | Configurable down to 20 ms, more flexible, even in background (with restrictions since Android 8+). |
| Advertising Payload  | Strictly capped; only Apple-approved Service UUIDs and local name visible in background. | Fully customizable payload (manufacturer data, service UUIDs, etc.).                                |
| Scanning             | OS-mediated. Apps can’t continuously scan in background.                                 | More freedom; can do active scanning and get scan records.                                          |
| Privacy              | MAC randomization per session; no persistent identifiers.                                | Randomized MAC but often reused across sessions or cached longer.                                   |

---

## 🔄 3. GATT Behavior

| Behavior      | **iOS (CoreBluetooth)**                                                                                        | **Android (BluetoothGatt)**                                                                                          |
| ------------- | -------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| Caching       | **Heavy GATT cache.** iOS may reuse old services/characteristics even if device firmware changed.              | GATT cache may persist, but can be cleared with reconnect or by toggling Bluetooth.                                  |
| Connection    | iOS manages reconnects automatically — if a peripheral disappears and returns, CoreBluetooth silently retries. | Android requires explicit reconnect logic; disconnections are final unless you call `connect()` again.               |
| Parallel Ops  | iOS serializes all GATT operations internally; no queuing in app code.                                         | Android requires manual queueing — calling `read` or `write` too fast breaks things.                                 |
| Write Types   | `withResponse` or `withoutResponse`; iOS buffers writes and retries as needed.                                 | Android exposes **WRITE_TYPE_NO_RESPONSE** and **WRITE_TYPE_DEFAULT**, but you must throttle manually.               |
| Notifications | iOS automatically handles CCCD writes behind the scenes.                                                       | Android requires explicit enabling of notifications via `setCharacteristicNotification()` and writing CCCD yourself. |

---

## 🔐 4. Security (SMP) & Pairing

* **iOS:** pairing flow is entirely system-handled — you can’t modify key exchange, I/O capabilities, or passkey entry.
* **Android:** lets you respond to pairing requests via callbacks; you can implement numeric comparison, passkey, or “Just Works”.

---

## 🧠 5. L2CAP & Custom Protocols

* **iOS:** prior to iOS 11, custom L2CAP channels were impossible.
  Since **iOS 11**, Apple introduced `CBL2CAPChannel`, but it’s still gated — only accessible after pairing and only for connected GATT peripherals using iAP2-style profiles.
* **Android:** supports **LE Credit-Based Channels** directly with `BluetoothSocket`-style APIs — you can implement your own binary protocol over BLE, beyond GATT.

---

## 🧱 6. Developer View of the Binary Protocol

| Layer            | iOS Exposure                           | Android Exposure               |
| ---------------- | -------------------------------------- | ------------------------------ |
| PHY / LL         | ❌ No access                            | ❌ No access                    |
| L2CAP            | ⚠️ Limited (post-pairing only)         | ✅ Exposed via CoC APIs         |
| ATT / GATT       | ✅ Full (through CoreBluetooth)         | ✅ Full (through BluetoothGatt) |
| SMP              | ❌ Fully managed by OS                  | ⚠️ Limited pairing callbacks   |
| Application Data | ✅ You define bytes for characteristics | ✅ Same                         |

---

## 📊 7. Practical Consequences

* **iOS BLE feels like a “virtualized sandbox”** — safe, consistent, but opaque.
  CoreBluetooth abstracts every byte, so you never see actual PDUs or handles.
* **Android BLE feels like a “hardware-level sandbox”** — more flexible but fragile.
  You must manage timing, reconnections, and cache invalidation manually.

