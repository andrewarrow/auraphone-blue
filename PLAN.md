# Auraphone Blue - Back to Basics Refactor Plan

## Problem Summary
Based on `~/Documents/fix.txt`, the codebase has three critical architectural flaws:

1. **UUID vs DeviceID Identity Crisis** - Mixing Hardware UUIDs (BLE radio ID) with DeviceIDs (base36 logical IDs assigned after handshake) causes race conditions and routing confusion
2. **Dual-Socket Architecture** - Separate central.sock and peripheral.sock per device creates unrealistic connection state complexity that doesn't match real BLE
3. **Callback Hell** - 6-8 layers of indirection (wire → phone → handlers → coordinators → callbacks → wire) makes message flow impossible to trace

## Refactor Goal
Get back to **SIMPLE, REALISTIC BLE** with:
- Only Hardware UUIDs (no base36 DeviceIDs yet)
- Single socket per device (realistic BLE connection model)
- Direct message flow (no callback chains)
- Two iPhones discovering each other and listing by Hardware UUID

---

## STEP 1: DEMOLITION

### 1.1 Delete Flawed Packages Entirely
**Delete these directories:**
```
rm -rf phone/
rm -rf iphone/
rm -rf android/
rm -rf wire/
```

**Why:** These packages embody all three architectural flaws. Starting fresh is faster than untangling.

### 1.2 Keep Good Packages
**Keep these directories untouched:**
```
swift/          # CoreBluetooth API wrappers (good abstractions)
kotlin/         # Android BLE API wrappers (good abstractions)
proto/          # Protobuf definitions (reusable)
logger/         # Logging utilities
testdata/       # Test fixtures (hardware_uuids.txt, face*.jpg)
cmd/            # CLI tools
```

**Why:** `swift/` and `kotlin/` are thin wrappers around BLE APIs with correct delegate/callback patterns. The problem is in the layers *above* them (phone/iphone/android/wire).

### 1.3 Remove Callbacks from swift/ and kotlin/
**Files to modify:**
- `swift/cb_central_manager.go` - Remove `wire.Wire` dependency, callbacks
- `swift/cb_peripheral_manager.go` - Remove `wire.Wire` dependency, callbacks
- `swift/cb_peripheral.go` - Remove `wire.Wire` dependency, callbacks
- `kotlin/bluetooth_manager.go` - Remove `wire.Wire` dependency, callbacks
- `kotlin/bluetooth_gatt.go` - Remove `wire.Wire` dependency, callbacks
- `kotlin/bluetooth_advertiser.go` - Remove `wire.Wire` dependency, callbacks

**What to remove:**
- All `import "github.com/user/auraphone-blue/wire"` references
- All `*wire.Wire` fields and parameters
- All callback function types and SetCallback methods
- Keep only the BLE API method signatures and data structures

**Why:** These packages should be pure API definitions, not coupled to implementation.

### 1.4 Update main.go Dependencies
**File:** `main.go`

**Remove imports:**
```go
"github.com/user/auraphone-blue/android"
"github.com/user/auraphone-blue/iphone"
"github.com/user/auraphone-blue/phone"
```

**Temporarily comment out:**
- All `PhoneWindow` logic
- Phone creation (`iphone.NewIPhone`, `android.NewAndroid`)
- Phone interface calls (`Start()`, `SetDiscoveryCallback()`, etc.)

**Why:** We'll rewrite `PhoneWindow` in Step 2 to use new simple architecture.

---

## STEP 2: MINIMAL BLE IMPLEMENTATION (iOS Only)

### 2.1 Create New Simple Wire Package
**File:** `wire/wire.go` (new, ~200 lines max)

**Single Responsibility:** Unix domain socket communication with BLE realism.

**Key Design:**
```go
type Wire struct {
    hardwareUUID string
    socketPath   string  // /tmp/auraphone-{uuid}.sock
    listener     net.Listener
    connections  map[string]net.Conn  // peer UUID → connection
    mu           sync.RWMutex
}

func NewWire(hardwareUUID string) *Wire
func (w *Wire) Start() error
func (w *Wire) Stop()
func (w *Wire) SendMessage(peerUUID string, data []byte) error
func (w *Wire) SetMessageHandler(handler func(peerUUID string, data []byte))
func (w *Wire) ListAvailableDevices() []string  // Scan /tmp for .sock files
```

**No callbacks, no delegates, no indirection.** Just send/receive bytes.

**BLE Realism:**
- Single socket per device at `/tmp/auraphone-{hardwareUUID}.sock`
- Connection-oriented (matches real BLE pairing)
- Length-prefixed messages (4-byte big-endian length + payload)
- Simple handshake: connect → send UUID → ready

### 2.2 Create Minimal iphone Package
**File:** `iphone/iphone.go` (new, ~150 lines max)

**Implements:** `Phone` interface from new minimal package

```go
type IPhone struct {
    hardwareUUID string
    deviceName   string
    wire         *wire.Wire
    central      *swift.CBCentralManager
    peripheral   *swift.CBPeripheralManager
    discovered   map[string]DiscoveredDevice  // UUID → device
    mu           sync.RWMutex
    callback     DeviceDiscoveryCallback
}

func NewIPhone(hardwareUUID string) *IPhone
func (ip *IPhone) Start()
func (ip *IPhone) Stop()
func (ip *IPhone) SetDiscoveryCallback(callback DeviceDiscoveryCallback)
func (ip *IPhone) GetDeviceUUID() string
func (ip *IPhone) GetDeviceName() string
```

**Key Behavior:**
1. **Start()**:
   - Create `wire.Wire`
   - Start advertising via `CBPeripheralManager`
   - Start scanning via `CBCentralManager`
2. **On device discovered** (from `CBCentralManager` delegate):
   - Add to `discovered` map with Hardware UUID as key
   - Call `callback(DiscoveredDevice{HardwareUUID: uuid, Name: name})`
3. **No photo transfer, no handshakes, no base36 IDs yet**

### 2.3 Create Minimal Phone Interface
**File:** `phone/phone.go` (new, ~30 lines)

```go
package phone

type DiscoveredDevice struct {
    HardwareUUID string
    Name         string
    RSSI         float64
}

type DeviceDiscoveryCallback func(device DiscoveredDevice)

type Phone interface {
    Start()
    Stop()
    SetDiscoveryCallback(callback DeviceDiscoveryCallback)
    GetDeviceUUID() string
    GetDeviceName() string
}
```

**Why so minimal:** We're proving the BLE foundation works before adding features.

### 2.4 Update main.go for Minimal UI
**File:** `main.go`

**Changes:**
- Add back `import "github.com/user/auraphone-blue/iphone"`
- Add back `import "github.com/user/auraphone-blue/phone"`
- Restore `PhoneWindow` but simplified:
  - Only `hardwareUUID` and `name` displayed
  - No photos, no base36 IDs, no profile tab
  - Device list shows: `UUID: {hardwareUUID[:8]} | Name: {name} | RSSI: {rssi}`

**Acceptance Criteria:**
- Create 2 iPhone windows
- Each lists the other by Hardware UUID within 1 second
- Clean shutdown (sockets cleaned up)

---

## STEP 3: VERIFICATION & NEXT STEPS

### 3.1 Test Scenario
```bash
go run main.go
# Creates 2 iPhone windows
# Each window shows:
#   - Header: "Auraphone - iPhone ({uuid[:8]})"
#   - Device list:
#     "UUID: {other_uuid[:8]} | iPhone | RSSI: -45"
```

### 3.2 Success Criteria
- [x] Both iPhones discover each other
- [x] Discovery happens via actual Unix socket scanning
- [x] Device list shows Hardware UUID (not base36)
- [x] No crashes, no race conditions
- [x] Clean shutdown (no leaked sockets)

### 3.3 Future Steps (NOT in this branch)
After Step 2 is verified working:
1. Add handshake protocol (exchange name, assign base36 DeviceID)
2. Add photo transfer (profile photo exchange)
3. Add Android support (reuse same wire package)
4. Add gossip protocol (mesh discovery)

---

## Key Architectural Principles

### ✅ DO:
- **One socket per device** (matches real BLE connection model)
- **Hardware UUID as primary key** until handshake
- **Direct function calls** (no callback chains)
- **Single responsibility** per package (wire = sockets, iphone = iOS logic)
- **Explicit error handling** (no silent failures)

### ❌ DON'T:
- **No dual sockets** (central.sock / peripheral.sock)
- **No base36 DeviceIDs yet** (wait until handshake works)
- **No callback indirection** (wire → handler → coordinator → callback → wire)
- **No premature optimization** (gossip, mesh, photos come later)
- **No cheating** (devices learn about each other via actual socket scanning)

---

## File Checklist

### To Delete:
- [ ] `phone/` (entire directory)
- [ ] `iphone/` (entire directory)
- [ ] `android/` (entire directory)
- [ ] `wire/` (entire directory)

### To Modify:
- [ ] `swift/cb_central_manager.go` (remove wire dependency, callbacks)
- [ ] `swift/cb_peripheral_manager.go` (remove wire dependency, callbacks)
- [ ] `swift/cb_peripheral.go` (remove wire dependency, callbacks)
- [ ] `kotlin/bluetooth_manager.go` (remove wire dependency, callbacks)
- [ ] `kotlin/bluetooth_gatt.go` (remove wire dependency, callbacks)
- [ ] `kotlin/bluetooth_advertiser.go` (remove wire dependency, callbacks)
- [ ] `main.go` (simplify PhoneWindow, remove complex dependencies)

### To Create:
- [ ] `wire/wire.go` (new minimal socket layer, ~200 lines)
- [ ] `phone/phone.go` (new minimal interface, ~30 lines)
- [ ] `iphone/iphone.go` (new minimal iOS implementation, ~150 lines)

### To Keep Untouched:
- `proto/` (protobuf definitions)
- `logger/` (logging utilities)
- `testdata/` (test fixtures)
- `cmd/` (CLI tools)
- `integration_test.go` (will need updates later)
- `integration_reliability_test.go` (will need updates later)

---

## Estimated Line Counts

**Deleted:** ~8,000 lines (phone/ + iphone/ + android/ + wire/)
**New Code:** ~400 lines (wire + phone + iphone)
**Net Change:** -7,600 lines

**Goal:** 95% less code, 100% more clarity.
