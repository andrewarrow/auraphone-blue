# Auraphone Blue - Back to Basics Refactor Plan

## Current Status: Gossip Protocol Implementation Complete ✅

**What's implemented:**
- ✅ New minimal `phone/phone.go` with additional components:
  - `Phone` interface with Hardware UUID + DeviceID
  - `DiscoveredDevice` struct with photo support
  - `IdentityManager` for UUID ↔ DeviceID mapping
  - `PhotoCache` for content-addressed photo storage
  - `MeshView` for gossip protocol (shared iOS/Android logic)
  - Gossip audit logging to `gossip_audit.jsonl`
- ✅ Full `iphone/iphone.go` implementation (~1000 lines)
  - Hardware UUID + DeviceID (base36) identity system
  - BLE handshake protocol with protobuf messages
  - Photo transfer with chunking and retry logic
  - **Gossip protocol integration** (sends/receives mesh state)
  - Connection management (tracks connected devices)
  - Mesh view updates on handshake, disconnect, photo receipt
- ✅ `wire/wire.go` with realistic BLE simulation
  - Single socket per device at `/tmp/auraphone-{uuid}.sock`
  - GATT message routing (handshake, gossip, photos)
  - MTU-based fragmentation, packet loss simulation
  - Connection state tracking
- ✅ Updated `main.go` with GUI fixes
  - Fixed cached photo display bug (adds refresh trigger)
  - Full 5-tab GUI with profile/photo support
  - Removed android (coming back later)

**Recent additions (this session):**
- ✅ `phone/mesh_view.go` - Shared gossip logic (~400 lines)
- ✅ Gossip timer in iphone.go (5-second intervals)
- ✅ Gossip message handling (distinguishes from handshakes)
- ✅ Mesh view persistence to `cache/mesh_view.json`
- ✅ GUI fix: cached photos now trigger refresh

**Next steps:**
- Test gossip with 4+ devices to verify multi-hop discovery
- Verify gossip_audit.jsonl logging
- Android implementation (reuse phone/mesh_view.go)

---

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
**File:** `phone/phone.go` (new, ~170 lines)

```go
package phone

type DiscoveredDevice struct {
    HardwareUUID string
    DeviceID     string // EMPTY for now (base36 ID added later with handshake)
    Name         string
    RSSI         float64
    PhotoHash    string // EMPTY for now (added later with photo transfer)
    PhotoData    []byte // nil for now (added later with photo transfer)
}

type DeviceDiscoveryCallback func(device DiscoveredDevice)

type Phone interface {
    Start()
    Stop()
    SetDiscoveryCallback(callback DeviceDiscoveryCallback)
    GetDeviceUUID() string
    GetDeviceName() string
    GetPlatform() string

    // GUI compatibility methods (stubs for now)
    SetProfilePhoto(photoPath string) error
    GetLocalProfileMap() map[string]string
    UpdateLocalProfile(profile map[string]string) error
}
```

**Key Points:**
- **DiscoveredDevice has extra fields** (DeviceID, PhotoHash, PhotoData) but they are EMPTY/nil in Step 2
  - These exist for GUI compatibility (main.go expects them)
  - They will be populated in future steps when we add handshake + photo transfer
- **Phone interface has extra methods** (GetPlatform, SetProfilePhoto, etc.) as stubs
  - These are no-ops for now, just return empty/success
  - This lets main.go GUI code compile without changes
- **Primary identification is HardwareUUID** - the only field that matters right now
  - Device list in GUI shows Hardware UUID (first 8 chars)
  - No base36 DeviceIDs yet, no handshakes, no photo transfers

**Also includes:**
- `HardwareUUIDManager` - Allocates UUIDs from testdata/hardware_uuids.txt
- `DeviceCacheManager` - Stub for future metadata caching
- `GetDataDir()` / `GetDeviceCacheDir()` - Helper functions for GUI

**Why these extras:** The GUI (main.go) has full profile/photo functionality built in. Instead of gutting main.go, we add minimal stubs to phone/iphone packages so the GUI compiles. The UI will work but won't show photos/profiles yet - that's fine for Step 2.

### 2.4 Update main.go for Minimal UI
**File:** `main.go`

**Status:** ✅ **DONE - main.go kept FULLY INTACT**

**What we did:**
- Removed `android` package import
- Changed `NewIPhone(uuid, name)` → `NewIPhone(uuid)` (name auto-generated)
- Disabled "Start Android Device" button in launcher
- Updated auto-start and stress-test modes to only create iOS devices

**What we kept:**
- ✅ **Full 5-tab GUI** (Home, Search, Add, Play, Profile)
- ✅ **Profile tab** with photo selector, name/tagline fields, contact methods
- ✅ **Device list** with profile image placeholders and metadata
- ✅ **Person modal** with full profile view
- ✅ **All threading logic** (Fyne.Do, goroutines, mutexes) - UNTOUCHED

**Why this approach:**
- The GUI has full functionality built-in (profiles, photos, contacts)
- Instead of removing GUI features, we added stub methods to `Phone` interface
- GUI compiles and runs, profile tab works, but photos/profiles are empty for now
- This is fine for Step 2 - we're proving BLE discovery works first
- Profile/photo functionality will "light up" in future steps when we implement:
  - Step 3: Handshake protocol (DeviceID assignment, name exchange)
  - Step 4: Photo transfer (PhotoHash, PhotoData population)

**Current behavior:**
- Device list shows: `UUID: {hardwareUUID[:8]} | Unknown Device | RSSI: -45`
- Profile tab: You can edit fields and select photos (stored locally, not broadcast yet)
- Person modal: Shows device info but no profile data yet (empty until handshake implemented)

---

## STEP 3: VERIFICATION & NEXT STEPS

### 3.1 Test Scenario
```bash
go run main.go
# Launcher window appears
# Click "Start iOS Device" twice to create 2 iPhone windows
# Each window shows:
#   - Header: "Auraphone - iPhone ({uuid[:8]}) ({uuid[:8]})"
#   - Home tab: Device list with discovered devices
#   - Profile tab: Full profile editor (works but not broadcast yet)
# Expected device list entries:
#   "Unknown Device"
#   "Device: {other_uuid[:8]}"
#   "RSSI: -45 dBm, Connected: Yes"
```

### 3.2 Success Criteria
- [ ] Both iPhones discover each other within 1-2 seconds
- [ ] Discovery happens via actual Unix socket scanning (/tmp/auraphone-*.sock)
- [ ] Device list shows Hardware UUID (DeviceID field is empty)
- [ ] Profile tab allows editing local profile (stored but not broadcast)
- [ ] No crashes, no race conditions
- [ ] Clean shutdown (no leaked sockets in /tmp)

### 3.3 What's Working (Step 2)
- ✅ Unix socket creation/cleanup (`/tmp/auraphone-{uuid}.sock`)
- ✅ Device discovery via socket scanning
- ✅ GUI shows discovered devices by Hardware UUID
- ✅ Profile tab allows local profile editing
- ✅ Clean shutdown releases resources

### 3.4 What's NOT Working Yet (Expected)
- ❌ No device names (shows "Unknown Device")
- ❌ No DeviceID/base36 IDs (field is empty)
- ❌ No profile photo display (images not transferred)
- ❌ No profile data exchange (first_name, contacts, etc. not shared)
- ❌ Person modal shows empty profile data

### 3.5 Future Steps (NOT in this branch)
After Step 2 is verified working:
1. **Step 3: Handshake Protocol**
   - Exchange device names via socket connection
   - Assign base36 DeviceID after handshake
   - Update GUI to show DeviceID instead of Hardware UUID
2. **Step 4: Photo Transfer**
   - Broadcast PhotoHash when profile photo changes
   - Transfer photo data over sockets
   - Cache photos to disk
   - Display photos in device list and person modal
3. **Step 5: Profile Exchange**
   - Broadcast profile metadata (first_name, contacts, etc.)
   - Update person modal with full profile data
4. **Step 6: Android Support**
   - Create `android/android.go` (reuse wire package)
   - Enable "Start Android Device" button
5. **Step 7: Gossip Protocol**
   - Multi-hop device/photo discovery
   - Mesh network without full connectivity

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
