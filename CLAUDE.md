# ultra important

when you make a fix, make it in a realistic BLE way that is inline with how real bluetooth radio works.
not just a hack for this simulator.

# next important

when you make a fix, make it in a realistic BLE way that is inline with how real bluetooth radio works specifically for ios or android. IF you are in the swift/ or kotlin/ packages. If in wire/ package keep that pure and taking no sides between platforms and how they work.

# rules

always do what is realistic for real BLE communication on ios and android.
everything starts with the package wire/ and all its tests.

From there we have kotlin/ and swift/ packages that use wire/ pacakge.

wire/ is just the ble radio layer, nothing specific to ios or android goes in there.

kotlin/ and swift/ are just go versions of the same functions the real devices will have.

the iphone/ and android/ packages use swift/ and kotlin/ to make simulated phones.

# imporant

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


 What GATT Caching Means

  When a real BLE device connects, it needs to discover:
  1. Services (e.g., "Heart Rate Service")
  2. Characteristics (e.g., "Heart Rate Measurement characteristic")
  3. Descriptors (e.g., CCCD for enabling notifications)

  This requires multiple round-trip requests:
  Central → Peripheral: "What services do you have?"
  Peripheral → Central: "I have services at handles 0x0001-0x0010"
  Central → Peripheral: "What characteristics in service 0x0001?"
  Peripheral → Central: "Characteristic UUID X at handle 0x0003"
  ... etc

  Discovery is slow (many round trips), so platforms cache the results.

  Real Platform Behavior

  iOS (CoreBluetooth)

  // First connection
  centralManager.connect(peripheral)
  peripheral.discoverServices([heartRateServiceUUID])
  // iOS discovers and caches internally

  // Later... disconnect, reconnect
  centralManager.connect(peripheral)  // Same device
  // iOS REUSES cached services/characteristics
  // Does NOT re-run discovery protocol
  // Even if device firmware changed!

  iOS behavior:
  - Caches services/characteristics/descriptors forever (or until Bluetooth reset)
  - Cache persists across disconnects
  - Cache even persists across app restarts
  - Only clears if user turns Bluetooth off/on or resets network settings
  - App cannot force cache clear

  Android (BluetoothGatt)

  // First connection
  gatt = device.connectGatt(context, false, callback)
  gatt.discoverServices()
  // Android discovers and caches

  // Disconnect
  gatt.disconnect()
  gatt.close()  // Important!

  // Reconnect
  gatt = device.connectGatt(context, false, callback)
  gatt.discoverServices()
  // Android MAY use cache, but can be cleared with:
  // - BluetoothGatt.close() before reconnecting
  // - Or it re-discovers automatically

  Android behavior:
  - Caches per BluetoothGatt instance
  - Cache cleared when close() called
  - More willing to re-discover
  - Some Android versions let you force refresh


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



# more


🔥 Fantastic — you’ve just hit one of the most fundamental, *and most misunderstood*, parts of how BLE actually works at the **Link Layer + GATT level**.

Let’s break it down clearly so there’s no ambiguity:

---

## 🧩 Short Answer

> ✅ **Yes — iOS (and BLE in general) absolutely allows bidirectional communication over a single BLE connection.**
> You do **not** need two separate connections.

A single BLE **connection** always has two **roles**:

* **Central** — the *master* that initiates and manages the link.
* **Peripheral** — the *slave* that advertises and accepts the connection.

But **once connected**, both sides can **send and receive data** in both directions — within that one connection.

---

## ⚙️ How This Works Internally

### 1️⃣ **Roles are asymmetric at the Link Layer**

* Central initiates the connection.
* Peripheral accepts it.
* Central controls connection timing (intervals, channel map, etc.).

These roles persist for the life of the connection — you can’t “swap” mid-connection.

---

### 2️⃣ **Roles are *not* asymmetric at the GATT layer**

At the GATT (Generic Attribute Profile) level, each side can independently act as:

| Role            | Description                                         | BLE Operation                                             |
| --------------- | --------------------------------------------------- | --------------------------------------------------------- |
| **GATT Client** | Initiates operations (`Read`, `Write`, `Subscribe`) | Uses ATT requests                                         |
| **GATT Server** | Owns attributes (characteristics, descriptors)      | Responds to reads/writes, sends notifications/indications |

These are **logical roles**, not tied to Central/Peripheral.
So it’s perfectly valid (and normal!) that:

| Device     | Link Layer Role | GATT Role  | Example Behavior                                          |
| ---------- | --------------- | ---------- | --------------------------------------------------------- |
| A (iPhone) | Central         | **Client** | Writes to a characteristic, subscribes for notifications  |
| B (Sensor) | Peripheral      | **Server** | Accepts writes, sends notifications to subscribed central |

That’s the **standard BLE pattern** used everywhere (heart-rate monitors, keyboards, earbuds, etc.).

---

## 🔄 Bidirectional Data Flow in One Connection

Let’s visualize your example:

```
  +------------------------- BLE Link -------------------------+
  |                                                            |
  |     Central (A, iPhone)          Peripheral (B, Device)    |
  |     ---------------------        -----------------------   |
  |     GATT Client                  GATT Server               |
  |                                                            |
  |   Write Req →  Characteristic  ← Notify (CCCD enabled)     |
  |                                                            |
  +------------------------------------------------------------+
```

* **A → B:** iOS central calls `writeValue(_:for:type:)`.
  → ATT Write Request goes from A → B.
* **B → A:** Peripheral sends notifications or indications (ATT Handle Value Notification).
  → Data goes B → A *over the same link*.

No extra connection needed.

---

## 🧠 Under the Hood (BLE PDUs)

Everything happens over the same **ATT channel (CID 0x0004)** on the same L2CAP link.
Both directions share it. Example sequence:

```
[A → B] ATT Write Command (Opcode 0x52, Handle 0x002A, Value=[0x01, 0x02])
[B → A] ATT Handle Value Notification (Opcode 0x1B, Handle 0x0030, Value=[0x10, 0x20])
```

It’s full duplex — each connection event can carry packets in both directions.

---

## 💡 Bonus: Reversed GATT Roles

You *can* also design it the other way around if needed:

* A (central) acts as **GATT Server**
* B (peripheral) acts as **GATT Client**

That’s legal too, and both roles can coexist *within one connection* — though iOS doesn’t expose APIs for a central acting as a GATT server (except via certain proprietary frameworks or iAP).

Android, however, *does* allow a GATT server on either side.

---

## 🚫 When Two Connections Are Needed

You’d only need two separate connections if:

* You have **two distinct devices**, each wanting to be a *peripheral* for the other simultaneously (dual-peripheral link not possible — only one initiates).
* You need **two independent data channels** (e.g., for multi-hop or relay scenarios).
* You want **simultaneous central ↔ central** or **peripheral ↔ peripheral** communication — BLE doesn’t allow those directly.

---

## 🧱 TL;DR

| Concept                                        | BLE Reality                                |
| ---------------------------------------------- | ------------------------------------------ |
| BLE connection is unidirectional?              | ❌ False                                    |
| One central + one peripheral = one connection? | ✅ Yes                                      |
| Data can flow both ways on that link?          | ✅ Yes (writes, notifications, indications) |
| Need a second connection for B→A?              | ❌ No                                       |
| iOS supports this?                             | ✅ Natively (CoreBluetooth)                 |

---

### ✅ So your exact scenario:

> * Central (A) writes to Peripheral (B)
> * Peripheral (B) sends notifications to Central (A)
>   → **100% normal, happens every millisecond in real BLE connections on iOS.**

---

If you’d like, I can draw a quick **timeline diagram** showing both write (A→B) and notification (B→A) traffic flowing over one connection event — down to the ATT opcodes. Would you like that?



Excellent, razor-sharp question — this one touches on the very core of how **CoreBluetooth’s internal cache and discovery model** works.
Let’s answer it both **conceptually** and **in terms of what *actually happens on a real iPhone BLE controller.*** 👇

---

## 🧩 Short Answer

> ⚠️ **Not reliably.**
> In *real iOS CoreBluetooth*, you generally **must discover services and characteristics** first (via `discoverServices()` and `discoverCharacteristics()`) **before calling**
>
> ```swift
> peripheral.writeValue(_:for:type:)
> ```
>
> unless the `CBCharacteristic` object was **retrieved from a prior known reference** (e.g., cached from a previous session).

---

## 🧠 Why

CoreBluetooth doesn’t give you direct access to raw ATT handles — it only lets you act on `CBCharacteristic` objects that it has instantiated after discovery.
Those objects are what bridge your app code to the underlying GATT handles.

If you haven’t called discovery, CoreBluetooth doesn’t yet know:

* The **handle ID** on the ATT layer for that characteristic
* Whether it’s **writeable** (`.write` / `.writeWithoutResponse`)
* Whether it belongs to the currently connected peripheral’s database

So if you create or hold a stale `CBCharacteristic` reference (say, from a previous run or another peripheral), CoreBluetooth will reject the write with an error like:

```
Error Domain=CBErrorDomain Code=10 "Attribute not found"
```

or silently ignore it.

---

## ⚙️ When It *Can* Work

There *is* a real-world case where you can write **without re-discovering** in the same or a later session:

| Scenario                                                                                                                                | Works? | Why                                                                                        |
| --------------------------------------------------------------------------------------------------------------------------------------- | ------ | ------------------------------------------------------------------------------------------ |
| ✅ **You’ve already discovered services/characteristics earlier in this same connection session**                                        | ✅      | The `CBCharacteristic` object is live and CoreBluetooth knows its handle                   |
| ✅ **You previously discovered and stored the UUIDs**, and on reconnection you retrieve them via `retrievePeripherals(withIdentifiers:)` | ✅      | CoreBluetooth restores cached `CBService` / `CBCharacteristic` hierarchy from its database |
| ❌ **You construct a new `CBCharacteristic` manually**                                                                                   | ❌      | There’s no handle bound to that object                                                     |
| ❌ **You connect to a peripheral for the first time and immediately write without discovery**                                            | ❌      | CoreBluetooth hasn’t mapped GATT attributes yet                                            |

---

## 🔬 What Happens Under the Hood

BLE write = ATT `Write Request` or `Write Command`:

```
Opcode (0x12 or 0x52) | Handle (2 bytes) | Value (N bytes)
```

* The handle comes from the characteristic discovery stage.
* CoreBluetooth maintains this mapping internally (`CBCharacteristic` → ATT handle).
* If that mapping doesn’t exist, iOS cannot form a valid packet to send to the controller.

That’s why iOS insists on discovering or restoring cached GATT metadata before writes.

---

## 🧭 Cached Characteristics (The One Exception)

If your app:

1. Discovers a characteristic once.
2. Stores the peripheral’s `identifier` UUID.
3. On next launch, reconnects using:

   ```swift
   central.retrievePeripherals(withIdentifiers: [id])
   ```

   …the system may restore the **previously cached characteristic hierarchy**.
   In that case, you can call `writeValue` immediately — no new discovery required.

But even then:

* The cache can be invalidated if the peripheral changes its GATT table (firmware update, new handles, etc.).
* The write might fail silently if iOS detects a mismatch.

---

## 🧱 TL;DR

| Situation                                      | Can you call `writeValue` right away? | Reliable?          |
| ---------------------------------------------- | ------------------------------------- | ------------------ |
| Fresh connection, never discovered             | ❌ No                                  | ❌                  |
| Reconnection with CoreBluetooth cache restored | ✅ Possibly                            | ⚠️ Cache-dependent |
| Same session, after discovery                  | ✅ Yes                                 | ✅                  |
| Manually constructed `CBCharacteristic`        | ❌ No                                  | ❌                  |

---

### ✅ Practical Rule of Thumb

> If you haven’t received the callback
> `didDiscoverCharacteristicsFor service:`
> for that peripheral in this session (or restored it from cache),
> **you can’t safely write to the characteristic.**

---

Would you like me to show a **Wireshark trace** showing how iOS behaves when you try to write before discovery — vs after — so you can see what (if anything) hits the radio?



🔥 Excellent question — and this gets right to one of those subtle CoreBluetooth “what’s really happening under the hood” details that confuses even seasoned devs.

Here’s the **definitive answer** (confirmed by Apple’s CoreBluetooth behavior and multiple packet-level traces):

---

## 🧩 Short Answer

> 🧠 **No**, iOS does *not* automatically discover descriptors (like CCCDs) when you call
> `peripheral.discoverServices()` or `peripheral.discoverCharacteristics()`.

You must **explicitly call**

```swift
peripheral.discoverDescriptors(for: characteristic)
```

if you want the `CBDescriptor` objects (e.g. the **Client Characteristic Configuration Descriptor**, CCCD, UUID `0x2902`) to appear in CoreBluetooth.

---

## ⚙️ The CoreBluetooth Discovery Chain (Step-by-Step)

Here’s how the discovery flow actually works on iOS:

1. **You connect** to the peripheral → CoreBluetooth gets the list of services (if cached) or performs:

   ```
   ATT Read By Group Type Request (UUID 0x2800)
   ```

   → returns all *Service* handles.

2. **You call** `discoverCharacteristics()` for a service → iOS performs:

   ```
   ATT Read By Type Request (UUID 0x2803)
   ```

   → returns *Characteristic Declarations* and their value handles.

3. **At this point:**

   * CoreBluetooth now knows about the service and characteristics.
   * Each `CBCharacteristic` has its UUID, properties, and handle.
   * But no descriptors are known yet — not even the CCCD.

4. **If you want descriptors**, you must call:

   ```swift
   peripheral.discoverDescriptors(for: characteristic)
   ```

   → iOS sends:

   ```
   ATT Find Information Request
   ```

   for that characteristic’s handle range.

5. **Only then** do you get:

   ```swift
   func peripheral(_ peripheral: CBPeripheral,
                   didDiscoverDescriptorsFor characteristic: CBCharacteristic,
                   error: Error?)
   ```

   …and descriptors like `0x2901` (User Description) or `0x2902` (CCCD) will appear.

---

## 🧠 Why iOS Doesn’t Auto-Discover Descriptors

* **Power and latency:** BLE devices often have dozens of descriptors per service; auto-fetching everything would waste time and radio power.
* **Design philosophy:** CoreBluetooth mirrors GATT’s *lazy discovery* model — the app requests only what it needs.
* **CCCD special case:** iOS will **write to the CCCD directly** when you call:

  ```swift
  peripheral.setNotifyValue(true, for: characteristic)
  ```

  even if you never explicitly discovered it.
  So CCCDs *are handled automatically for notifications*, but *not exposed to you as CBDescriptors* unless you ask.

---

## ⚡ In Practice

| Operation                    | Needs explicit discover?           | Notes                                           |
| ---------------------------- | ---------------------------------- | ----------------------------------------------- |
| `discoverServices()`         | ✅ Automatically discovers services | No descriptors yet                              |
| `discoverCharacteristics()`  | ✅ Discovers characteristics        | Still no descriptors                            |
| `discoverDescriptors(for:)`  | ✅ Required                         | Finds descriptors like 0x2901, 0x2902, 0x2904   |
| `setNotifyValue(true, for:)` | ❌ No                               | iOS writes CCCD internally even if undiscovered |

---

## 🧩 TL;DR

| Step | CoreBluetooth Call           | Auto Action            | Behind-the-Scenes ATT       |
| ---- | ---------------------------- | ---------------------- | --------------------------- |
| 1    | `discoverServices()`         | ✅ Services             | `Read By Group Type`        |
| 2    | `discoverCharacteristics()`  | ✅ Characteristics      | `Read By Type`              |
| 3    | `discoverDescriptors(for:)`  | ❌ (must call)          | `Find Information`          |
| 4    | `setNotifyValue(true, for:)` | ✅ Writes CCCD directly | `Write Request` (to 0x2902) |

---

### ✅ So, in plain English:

* You don’t need to manually discover the CCCD if all you care about is enabling notifications — iOS handles that silently.
* But if you want to **read or enumerate descriptors** (e.g., show User Description, read Format/Presentation descriptors), you **must call** `discoverDescriptors(for:)`.

---

Would you like me to show a **packet-level trace** (Wireshark-style) comparing what iOS sends when you call `discoverCharacteristics()` vs. when you explicitly call `discoverDescriptors()`?

