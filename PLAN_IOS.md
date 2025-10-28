# PLAN_IOS.md - Making Wire + Swift Work Like Real iOS BLE

## Current Status: Steps 2-8 Complete + Gossip Protocol ✅

We have:
- ✅ Step 2: Basic discovery via socket scanning
- ✅ Step 3: Single connection between devices (role negotiation)
- ✅ Step 4: GATT message protocol (handshake, gossip, photos)
- ✅ Step 5: Swift Central integration (scan, connect, read/write/notify)
- ✅ Step 6: Swift Peripheral integration (advertise, accept, respond)
- ✅ Step 7: iPhone handshake protocol (DeviceID assignment, identity mapping)
- ✅ Step 8: Photo transfer (chunking, caching, display)
- ✅ **Gossip Protocol**: Mesh network device/photo discovery
  - `phone/mesh_view.go` - Shared gossip logic (400 lines)
  - Gossip timer (5-second intervals)
  - Gossip message routing (distinguishes from handshakes)
  - Mesh view persistence (`cache/mesh_view.json`)
  - Gossip audit logging (`gossip_audit.jsonl`)
- ✅ **GUI Fix**: Cached photos now trigger UI refresh

**Current Architecture:**
- Single socket per device: `/tmp/auraphone-{uuid}.sock`
- Hardware UUID for wire routing, DeviceID for application logic
- Direct delegate pattern (no callback hell)
- Photo transfer works 100% (test report: 12/12 successful)
- Gossip protocol integrated (multi-hop discovery ready)

**Next Goal:** Test gossip with 4+ devices to verify multi-hop mesh discovery works correctly.

---

## Lessons from ~/Documents/fix.txt (What NOT To Do)

### ❌ Mistake #1: UUID vs DeviceID Identity Crisis
**Old Architecture (apb repo):**
- Mixing Hardware UUIDs (BLE wire ID) with DeviceIDs (base36 logical ID)
- Race condition: Photo chunks arrive BEFORE handshake populates UUID→DeviceID mapping
- Wrong device responds because routing conflates UUIDs and DeviceIDs

**Fix:**
- ✅ **Hardware UUID is the ONLY identifier until handshake completes**
- ✅ **DeviceID is assigned AFTER successful handshake, never used for wire routing**
- ✅ **Wire layer uses Hardware UUID exclusively**
- ✅ **Application layer can use DeviceID for display, but always maps back to Hardware UUID for sending**

### ❌ Mistake #2: Dual-Socket Architecture Creates Connection Chaos
**Old Architecture (apb/wire/wire.go:109-127):**
- Two sockets per device: `-peripheral.sock` and `-central.sock`
- Two TCP connections per device pair: `asCentral` and `asPeripheral`
- Connection state split across multiple maps
- Failure mode: One socket drops, other still alive → partial connection state

**Why This Is Wrong:**
Real BLE has:
1. **ONE connection** between two devices
2. **Roles are logical, not physical** - both sides can read/write/notify on the SAME connection
3. **GATT operations work bidirectionally** on the same underlying ACL link
4. **Notifications and writes use the same connection** - no "dual sockets"

**Fix:**
- ✅ **Single socket per device** (already implemented in Step 2)
- ✅ **Single connection per device pair** (not two)
- ✅ **Roles are determined by who initiates the connection**, but both sides can send data
- ✅ **GATT operations are request/response over the same socket**, not separate channels

### ❌ Mistake #3: Callback Hell with No Clear Message Flow
**Old Architecture (apb repo):**
- 6-8 layers of indirection: wire → phone → handlers → coordinators → callbacks → wire
- Impossible to trace message paths
- Callbacks capture closures from different scopes → shared state bugs

**Fix:**
- ✅ **Direct function calls** - no callback chains
- ✅ **Explicit message routing** - clear path from wire → swift → iphone
- ✅ **Single responsibility** per layer - no cross-cutting concerns

---

## ✅ Callbacks Are GOOD When Used Correctly (iOS Delegate Pattern)

### The Confusion: Not All Callbacks Are Bad

The old architecture had **callback hell** (8 layers), but that doesn't mean we should avoid callbacks entirely. **Real iOS CoreBluetooth uses delegates/callbacks extensively**, and that's the **correct pattern**.

### ✅ GOOD Callbacks: iOS CoreBluetooth Delegate Pattern

**Real iOS API:**
```swift
class MyDevice: CBPeripheralManagerDelegate {
    func peripheralManager(_ peripheral: CBPeripheralManager,
                          didReceiveWrite requests: [CBATTRequest]) {
        // Handle write request
        peripheral.respond(to: requests[0], withResult: .success)
    }

    func peripheralManager(_ peripheral: CBPeripheralManager,
                          central: CBCentral,
                          didSubscribeTo characteristic: CBCharacteristic) {
        // Handle subscription
        peripheral.updateValue(data, for: characteristic, onSubscribedCentrals: nil)
    }
}
```

**Our Go simulation:**
```go
type CBPeripheralManagerDelegate interface {
    DidReceiveWriteRequests(pm CBPeripheralManager, requests []CBATTRequest)
    CentralDidSubscribe(pm CBPeripheralManager, central CBCentral, char CBCharacteristic)
}

// iPhone implements the delegate
func (ip *IPhone) DidReceiveWriteRequests(pm swift.CBPeripheralManager, requests []CBATTRequest) {
    for _, req := range requests {
        if req.Characteristic.UUID == AuraProtocolCharUUID {
            ip.handleHandshake(req.Central.UUID, req.Value)
        }
    }
    pm.RespondToRequest(requests[0], "success")  // ✅ Direct response, no callback chain
}
```

**Call stack (4 layers, linear):**
```
wire.readMessages()              // Wire layer: receives bytes
  → pm.handleGATTRequest()       // Swift layer: parses GATT message
    → ip.DidReceiveWriteRequests() // iPhone layer: delegate callback
      → ip.handleHandshake()     // Business logic: process data
```

**Why this is GOOD:**
- ✅ **Single layer of callbacks** - delegate methods call directly into your code
- ✅ **Clear ownership** - CBPeripheralManager owns the delegate, simple lifecycle
- ✅ **Matches real iOS API** - this is how Apple designed it
- ✅ **Short call stack** - 4 levels total, linear flow
- ✅ **No circular dependencies** - iPhone never calls back into wire

### ❌ BAD Callbacks: Old Architecture from fix.txt

**What the old codebase did wrong (8 layers, circular):**
```
wire.dispatchMessage()                    // Layer 1
  → phone/photo_handler.go                // Layer 2
    → phone/photo_transfer_coordinator.go // Layer 3
      → message_router.go callbacks       // Layer 4 (callback 1)
        → iphone.go callback closures     // Layer 5 (callback 2)
          → gossip_handler.go             // Layer 6 (callback 3)
            → connection_manager.go       // Layer 7 (callback 4)
              → wire.WriteCharacteristic() // Layer 8 (BACK TO WIRE - circular!)
```

**Why this was BAD:**
- ❌ **8 layers of indirection** - impossible to trace message flow
- ❌ **Circular dependencies** - wire → phone → handlers → wire (loops back!)
- ❌ **Callback chains** - callbacks calling callbacks calling callbacks
- ❌ **Shared state bugs** - closures capturing variables from different scopes
- ❌ **No clear ownership** - who owns what callback? Lifecycle unclear

### The Rule for Callbacks

**✅ Good callback usage:**
```
Framework/Library → Your Code (1 hop, then done)
Example: CBPeripheralManager → IPhone.DidReceiveWriteRequests() → handle & done
```

**❌ Bad callback usage:**
```
Framework → Layer1 → Layer2 → Layer3 → ... → Layer8 → Framework (circular!)
Example: wire → phone → handlers → callbacks → wire (mess)
```

### iOS Delegate Methods We Use

**CBCentralManagerDelegate:**
- `DidUpdateState()` - Bluetooth powered on/off
- `DidDiscoverPeripheral()` - Found advertising device
- `DidConnectPeripheral()` - Connection established
- `DidDisconnectPeripheral()` - Connection lost

**CBPeripheralManagerDelegate:**
- `DidReceiveReadRequest()` - Central reading our characteristic
- `DidReceiveWriteRequests()` - Central writing to our characteristic
- `CentralDidSubscribe()` - Central subscribed to notifications
- `CentralDidUnsubscribe()` - Central unsubscribed

**CBPeripheralDelegate:**
- `DidDiscoverServices()` - GATT services discovered
- `DidUpdateValueForCharacteristic()` - Notification received

**All of these are 1-hop callbacks matching the real iOS API - this is CORRECT.**

---

## Real iOS BLE Architecture (How It Actually Works)

### Central vs Peripheral Roles (Logical, Not Physical)

In real iOS CoreBluetooth:

1. **Central Role** (CBCentralManager):
   - Scans for advertising Peripherals
   - Initiates connections to Peripherals
   - Reads/writes Peripheral's GATT characteristics
   - Subscribes to notifications from Peripheral

2. **Peripheral Role** (CBPeripheralManager):
   - Advertises services and characteristics
   - Accepts connections from Centrals
   - Responds to read/write requests from Centrals
   - Sends notifications to subscribed Centrals

3. **Key Point: BOTH ROLES USE THE SAME CONNECTION**
   - When Central connects to Peripheral, there is ONE BLE connection
   - Central can write to Peripheral's characteristics
   - Peripheral can send notifications to Central
   - **Same underlying ACL link, no "dual sockets"**

### Dual-Role Devices (iOS and Android)

- Both iOS and Android devices are **dual-role** (can be both Central and Peripheral simultaneously)
- This means:
  - Device A can connect to Device B as Central
  - Device A can also accept connections from Device C as Peripheral
  - But **Device A ↔ Device B has only ONE connection**, not two

### Connection Establishment Flow

**Real iOS BLE:**
```
Central (Device A)                Peripheral (Device B)
----------------------------------------------------------
ScanForPeripherals()    ------->  StartAdvertising()
DidDiscoverPeripheral() <-------  (advertising packet)
Connect(peripheral)     ------->
DidConnectPeripheral()  <----->  DidSubscribeToCentral()
                        ONE CONNECTION ESTABLISHED
DiscoverServices()      ------->
DidDiscoverServices()   <-------  (GATT table)
WriteValue(data, char)  ------->  DidReceiveWriteRequest()
                        <-------  UpdateValue() [notification]
DidUpdateValue()        <-------
```

**Key observations:**
- Single connection, bidirectional communication
- Central initiates, Peripheral responds
- But Peripheral can send unsolicited notifications over same connection
- No "dual sockets" or "central.sock + peripheral.sock"

---

## Proposed Architecture for Wire + Swift

### Core Principle: ONE Socket = ONE BLE Connection

```
Device A                          Device B
/tmp/auraphone-{uuidA}.sock       /tmp/auraphone-{uuidB}.sock
         |                                 |
         |  A connects to B's socket      |
         |  (A = Central, B = Peripheral) |
         +-------------------------------->|
                   ONE CONNECTION
         <--------------------------------+
         |                                 |
   A can write to B's                B can write to A
   characteristics                   (notifications)
         |                                 |
```

**No second socket needed!** The same socket connection carries:
- GATT requests (read/write from Central)
- GATT responses (from Peripheral)
- Notifications/indications (from Peripheral to Central)

### Wire Layer Responsibilities

**`wire/wire.go` should ONLY handle:**
1. **Socket creation**: Single socket at `/tmp/auraphone-{uuid}.sock`
2. **Discovery**: Scan `/tmp/` for other `.sock` files
3. **Connection**: Connect to peer's socket (initiator becomes Central)
4. **Message framing**: Length-prefixed messages (4-byte header + payload)
5. **Send/Receive**: Raw byte arrays, no interpretation

**What Wire should NOT do:**
- ❌ No GATT interpretation (that's swift layer's job)
- ❌ No role negotiation (roles are implicit: connector = Central)
- ❌ No characteristic routing (that's iphone layer's job)
- ❌ No callbacks (direct function calls only)

### Swift Layer Responsibilities

**`swift/cb_central_manager.go`:**
- Wraps wire discovery as CoreBluetooth scan
- Translates wire messages to GATT operations
- Manages connection state (connecting → connected → disconnected)
- Provides delegate callbacks matching real iOS API

**`swift/cb_peripheral_manager.go`:**
- Handles advertising (writes advertising data to filesystem for discovery)
- Accepts incoming connections (when wire accepts socket connection)
- Responds to GATT read/write requests
- Sends notifications via wire connection

**`swift/cb_peripheral.go`:**
- Represents a remote Peripheral (from Central's perspective)
- Wraps wire connection for sending GATT requests
- Receives GATT responses and notifications

**Key Point:** Swift layer translates between wire (bytes) and GATT (characteristics).

### iPhone Layer Responsibilities

**`iphone/iphone.go`:**
- Creates CBCentralManager and CBPeripheralManager
- Implements delegate methods (DidDiscoverPeripheral, DidReceiveWriteRequest, etc.)
- Business logic: handshakes, photo transfers, profile exchange
- Maintains application state (discovered devices, profile data, etc.)

---

## Step-by-Step Implementation Plan

### ✅ Step 2: Basic Discovery (DONE)
- Single socket per device
- Discovery via socket scanning
- No connections yet

### 🎯 Step 3: Single Connection Between Devices

**Goal:** Make wire layer support single bidirectional connections.

**Changes to `wire/wire.go`:**
```go
type Wire struct {
    hardwareUUID string
    socketPath   string
    listener     net.Listener
    connections  map[string]*Connection  // peer UUID → connection (single, not dual)
    mu           sync.RWMutex
}

type Connection struct {
    conn         net.Conn
    remoteUUID   string
    role         ConnectionRole  // Central or Peripheral (determined by who connected)
    sendMutex    sync.Mutex
}

type ConnectionRole string
const (
    RoleCentral    ConnectionRole = "central"    // We initiated connection
    RolePeripheral ConnectionRole = "peripheral" // They initiated connection
)

// Connect establishes a connection (we become Central)
func (w *Wire) Connect(peerUUID string) error {
    // Check if already connected
    if w.IsConnected(peerUUID) {
        return nil
    }

    peerSocketPath := fmt.Sprintf("/tmp/auraphone-%s.sock", peerUUID)
    conn, err := net.Dial("unix", peerSocketPath)
    if err != nil {
        return err
    }

    // Send handshake: our UUID
    // ... existing handshake code ...

    // Store as Central connection (we initiated)
    w.mu.Lock()
    w.connections[peerUUID] = &Connection{
        conn:       conn,
        remoteUUID: peerUUID,
        role:       RoleCentral,
    }
    w.mu.Unlock()

    go w.readMessages(peerUUID, conn)
    return nil
}

// acceptConnections handles incoming connections (we become Peripheral)
func (w *Wire) acceptConnections() {
    for {
        conn, err := w.listener.Accept()
        // ... error handling ...

        // Read handshake to get peer UUID
        peerUUID := w.readHandshake(conn)

        // Store as Peripheral connection (they initiated)
        w.mu.Lock()
        w.connections[peerUUID] = &Connection{
            conn:       conn,
            remoteUUID: peerUUID,
            role:       RolePeripheral,
        }
        w.mu.Unlock()

        go w.readMessages(peerUUID, conn)
    }
}
```

**Key changes:**
- ✅ Single `Connection` per peer (not `DualConnection`)
- ✅ Role is determined by who initiated the connection
- ✅ Same connection used for bidirectional communication

**Test:** Two iPhones connect and both have `connections[peerUUID]` with correct roles.

---

### 🎯 Step 4: GATT Message Protocol

**Goal:** Define message format for GATT operations over wire.

**Message types (JSON over wire):**
```json
{
  "type": "gatt_request",
  "request_id": "uuid-v4",
  "operation": "write",
  "service_uuid": "E621E1F8-...",
  "characteristic_uuid": "E621E1F8-...",
  "data": [104, 101, 108, 108, 111]
}

{
  "type": "gatt_response",
  "request_id": "uuid-v4",
  "status": "success",
  "data": [...]
}

{
  "type": "gatt_notification",
  "service_uuid": "E621E1F8-...",
  "characteristic_uuid": "E621E1F8-...",
  "data": [...]
}
```

**Wire layer changes:**
```go
type GATTMessage struct {
    Type                string `json:"type"` // "gatt_request", "gatt_response", "gatt_notification"
    RequestID           string `json:"request_id,omitempty"`
    Operation           string `json:"operation,omitempty"` // "read", "write", "subscribe", "unsubscribe"
    ServiceUUID         string `json:"service_uuid"`
    CharacteristicUUID  string `json:"characteristic_uuid"`
    Data                []byte `json:"data,omitempty"`
    Status              string `json:"status,omitempty"` // "success", "error"
}

// SendGATTMessage sends a GATT message to peer
func (w *Wire) SendGATTMessage(peerUUID string, msg *GATTMessage) error {
    data, err := json.Marshal(msg)
    if err != nil {
        return err
    }
    return w.SendMessage(peerUUID, data)
}

// SetGATTMessageHandler sets handler for incoming GATT messages
func (w *Wire) SetGATTMessageHandler(handler func(peerUUID string, msg *GATTMessage)) {
    w.gattHandler = handler
}
```

**Test:** Send GATT write request from A to B, B receives and responds.

---

### 🎯 Step 5: Swift Layer Integration

**Goal:** Make CBCentralManager and CBPeripheral use wire for connections.

**Changes to `swift/cb_central_manager.go`:**
```go
type CBCentralManager struct {
    Delegate CBCentralManagerDelegate
    State    string
    uuid     string
    wire     *wire.Wire
}

func NewCBCentralManager(delegate CBCentralManagerDelegate, uuid string, sharedWire *wire.Wire) *CBCentralManager {
    cm := &CBCentralManager{
        Delegate: delegate,
        State:    "poweredOn",
        uuid:     uuid,
        wire:     sharedWire,
    }

    // Set GATT message handler
    sharedWire.SetGATTMessageHandler(func(peerUUID string, msg *wire.GATTMessage) {
        cm.handleGATTMessage(peerUUID, msg)
    })

    return cm
}

func (c *CBCentralManager) Connect(peripheral CBPeripheral) {
    // Use wire to connect
    err := c.wire.Connect(peripheral.UUID)
    if err != nil {
        if c.Delegate != nil {
            c.Delegate.DidFailToConnectPeripheral(*c, peripheral, err)
        }
        return
    }

    // Call delegate on success
    if c.Delegate != nil {
        c.Delegate.DidConnectPeripheral(*c, peripheral)
    }
}

func (c *CBCentralManager) handleGATTMessage(peerUUID string, msg *wire.GATTMessage) {
    if msg.Type == "gatt_notification" {
        // Find peripheral by UUID
        peripheral := c.findPeripheral(peerUUID)
        if peripheral != nil && peripheral.Delegate != nil {
            // Convert to characteristic
            char := c.findCharacteristic(msg.ServiceUUID, msg.CharacteristicUUID)
            char.Value = msg.Data

            // Call delegate
            peripheral.Delegate.DidUpdateValueForCharacteristic(*peripheral, char, nil)
        }
    }
}
```

**Changes to `swift/cb_peripheral.go`:**
```go
func (p *CBPeripheral) WriteValue(data []byte, characteristic CBCharacteristic, writeType string) {
    // Send GATT write request via wire
    msg := &wire.GATTMessage{
        Type:               "gatt_request",
        RequestID:          generateUUID(),
        Operation:          "write",
        ServiceUUID:        characteristic.Service.UUID,
        CharacteristicUUID: characteristic.UUID,
        Data:               data,
    }

    p.wire.SendGATTMessage(p.UUID, msg)
}

func (p *CBPeripheral) SetNotifyValue(enabled bool, characteristic CBCharacteristic) {
    op := "subscribe"
    if !enabled {
        op = "unsubscribe"
    }

    msg := &wire.GATTMessage{
        Type:               "gatt_request",
        RequestID:          generateUUID(),
        Operation:          op,
        ServiceUUID:        characteristic.Service.UUID,
        CharacteristicUUID: characteristic.UUID,
    }

    p.wire.SendGATTMessage(p.UUID, msg)
}
```

**Test:** Central connects to Peripheral, writes to characteristic, Peripheral receives write request.

---

### 🎯 Step 6: Peripheral Manager Integration

**Goal:** Make CBPeripheralManager handle incoming GATT requests.

**Changes to `swift/cb_peripheral_manager.go`:**
```go
type CBPeripheralManager struct {
    Delegate      CBPeripheralManagerDelegate
    State         string
    uuid          string
    wire          *wire.Wire
    services      []CBService
    subscriptions map[string][]string  // charUUID → list of subscribed central UUIDs
}

func NewCBPeripheralManager(delegate CBPeripheralManagerDelegate, uuid string, sharedWire *wire.Wire) *CBPeripheralManager {
    pm := &CBPeripheralManager{
        Delegate:      delegate,
        State:         "poweredOn",
        uuid:          uuid,
        wire:          sharedWire,
        subscriptions: make(map[string][]string),
    }

    // Set GATT message handler
    sharedWire.SetGATTMessageHandler(func(peerUUID string, msg *wire.GATTMessage) {
        pm.handleGATTRequest(peerUUID, msg)
    })

    return pm
}

func (pm *CBPeripheralManager) handleGATTRequest(peerUUID string, msg *wire.GATTMessage) {
    if msg.Type != "gatt_request" {
        return
    }

    switch msg.Operation {
    case "write":
        pm.handleWriteRequest(peerUUID, msg)
    case "read":
        pm.handleReadRequest(peerUUID, msg)
    case "subscribe":
        pm.handleSubscribe(peerUUID, msg)
    case "unsubscribe":
        pm.handleUnsubscribe(peerUUID, msg)
    }
}

func (pm *CBPeripheralManager) handleWriteRequest(peerUUID string, msg *wire.GATTMessage) {
    // Find characteristic
    char := pm.findCharacteristic(msg.ServiceUUID, msg.CharacteristicUUID)
    if char == nil {
        return
    }

    // Create write request
    request := CBATTRequest{
        Central:        CBCentral{UUID: peerUUID},
        Characteristic: *char,
        Value:          msg.Data,
    }

    // Call delegate
    if pm.Delegate != nil {
        pm.Delegate.DidReceiveWriteRequests(*pm, []CBATTRequest{request})
    }
}

func (pm *CBPeripheralManager) UpdateValue(value []byte, characteristic CBCharacteristic, centrals []CBCentral) bool {
    // Send notification to all subscribed centrals
    charKey := characteristic.UUID

    var recipients []string
    if len(centrals) > 0 {
        // Specific centrals
        for _, central := range centrals {
            recipients = append(recipients, central.UUID)
        }
    } else {
        // All subscribed centrals
        recipients = pm.subscriptions[charKey]
    }

    for _, centralUUID := range recipients {
        msg := &wire.GATTMessage{
            Type:               "gatt_notification",
            ServiceUUID:        characteristic.Service.UUID,
            CharacteristicUUID: characteristic.UUID,
            Data:               value,
        }
        pm.wire.SendGATTMessage(centralUUID, msg)
    }

    return true
}

func (pm *CBPeripheralManager) handleSubscribe(peerUUID string, msg *wire.GATTMessage) {
    charKey := msg.CharacteristicUUID

    // Add to subscriptions
    if pm.subscriptions[charKey] == nil {
        pm.subscriptions[charKey] = []string{}
    }
    pm.subscriptions[charKey] = append(pm.subscriptions[charKey], peerUUID)

    // Call delegate
    if pm.Delegate != nil {
        char := pm.findCharacteristic(msg.ServiceUUID, msg.CharacteristicUUID)
        pm.Delegate.CentralDidSubscribe(*pm, CBCentral{UUID: peerUUID}, *char)
    }
}
```

**Test:** Central subscribes to characteristic, Peripheral sends notification, Central receives it.

---

### 🎯 Step 7: iPhone Layer with Handshake

**Goal:** Implement handshake protocol to exchange names and assign DeviceIDs.

**Changes to `iphone/iphone.go`:**
```go
type IPhone struct {
    hardwareUUID    string
    deviceID        string  // Base36 ID (assigned after handshake)
    deviceName      string
    wire            *wire.Wire
    central         *swift.CBCentralManager
    peripheral      *swift.CBPeripheralManager
    discovered      map[string]*DiscoveredDevice  // hardwareUUID → device
    handshaked      map[string]bool               // hardwareUUID → handshake complete
    mu              sync.RWMutex
    callback        phone.DeviceDiscoveryCallback
}

// Implement CBCentralManagerDelegate
func (ip *IPhone) DidConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral) {
    // Connection established, send handshake
    ip.sendHandshake(peripheral.UUID)
}

func (ip *IPhone) sendHandshake(peerUUID string) {
    handshake := HandshakeMessage{
        HardwareUUID: ip.hardwareUUID,
        DeviceID:     ip.deviceID,
        DeviceName:   ip.deviceName,
    }

    data, _ := json.Marshal(handshake)

    // Write to AuraProtocolCharUUID
    char := ip.getProtocolCharacteristic(peerUUID)
    ip.central.WriteValue(data, char, "withResponse")
}

// Implement CBPeripheralManagerDelegate
func (ip *IPhone) DidReceiveWriteRequests(peripheralManager swift.CBPeripheralManager, requests []swift.CBATTRequest) {
    for _, request := range requests {
        if request.Characteristic.UUID == phone.AuraProtocolCharUUID {
            // Handshake message received
            ip.handleHandshake(request.Central.UUID, request.Value)
        } else if request.Characteristic.UUID == phone.AuraPhotoCharUUID {
            // Photo data received
            ip.handlePhotoData(request.Central.UUID, request.Value)
        }
    }

    peripheralManager.RespondToRequest(requests[0], "success")
}

func (ip *IPhone) handleHandshake(peerUUID string, data []byte) {
    var handshake HandshakeMessage
    json.Unmarshal(data, &handshake)

    ip.mu.Lock()
    defer ip.mu.Unlock()

    // Mark handshake complete
    ip.handshaked[peerUUID] = true

    // Update discovered device with real name and DeviceID
    if device, exists := ip.discovered[peerUUID]; exists {
        device.DeviceID = handshake.DeviceID
        device.Name = handshake.DeviceName

        // Notify GUI
        if ip.callback != nil {
            ip.callback(*device)
        }
    }
}
```

**Test:** Two iPhones connect, exchange handshakes, device list shows real names and DeviceIDs.

---

### 🎯 Step 8: Photo Transfer

**Goal:** Transfer photos using GATT notifications.

**Flow:**
1. Device A updates profile photo
2. A broadcasts photo hash in advertising data
3. B discovers A, sees photo hash
4. B subscribes to AuraPhotoCharUUID on A
5. A sends photo data via notifications (chunked to MTU size)
6. B receives chunks, reassembles photo, displays in GUI

**Implementation:** Similar to handshake but with chunking for large data.

---

## Summary: Key Architecture Principles

### ✅ DO:
1. **Single socket per device** (not dual sockets)
2. **Single connection per device pair** (bidirectional)
3. **Hardware UUID for wire routing** (never DeviceID)
4. **DeviceID assigned after handshake** (for display only)
5. **GATT messages over wire** (request/response pattern)
6. **Direct function calls** (no callback chains)
7. **Clear layer separation:**
   - Wire: Bytes only
   - Swift: GATT translation
   - iPhone: Business logic

### ❌ DON'T:
1. **No dual sockets** (central.sock + peripheral.sock)
2. **No dual connections** (asCentral + asPeripheral)
3. **No mixing UUIDs and DeviceIDs** in wire routing
4. **No callback hell** (6+ layers of indirection)
5. **No race conditions** (handshake must complete before photo transfer)
6. **No partial connection state** (one connection drops = full disconnect)

---

## Testing Strategy

Each step should be testable in isolation:

1. **Step 3:** Two devices connect, verify single connection exists with correct role
2. **Step 4:** Send GATT message, verify receiver gets it unchanged
3. **Step 5:** Central writes to characteristic, verify Peripheral receives write request
4. **Step 6:** Peripheral sends notification, verify Central receives it
5. **Step 7:** Two devices handshake, verify names and DeviceIDs exchanged
6. **Step 8:** Transfer photo, verify image displays correctly in GUI

---

## Implementation Order

1. ✅ **Step 2 (DONE):** Basic discovery
2. 🎯 **Step 3:** Single connection (wire layer)
3. 🎯 **Step 4:** GATT message protocol (wire layer)
4. 🎯 **Step 5:** Swift Central integration (swift layer)
5. 🎯 **Step 6:** Swift Peripheral integration (swift layer)
6. 🎯 **Step 7:** iPhone handshake (iphone layer)
7. 🎯 **Step 8:** Photo transfer (iphone layer)

Each step builds on the previous, no skipping ahead.

---

## Expected Line Counts

- `wire/wire.go`: ~400 lines (vs 1500+ in old architecture)
- `swift/cb_central_manager.go`: ~200 lines
- `swift/cb_peripheral_manager.go`: ~200 lines
- `swift/cb_peripheral.go`: ~150 lines
- `iphone/iphone.go`: ~300 lines

**Total: ~1250 lines** (vs 5000+ in old architecture)

**Goal: 75% less code, 100% more clarity.**
