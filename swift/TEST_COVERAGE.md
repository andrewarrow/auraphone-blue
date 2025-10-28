# Swift Package Test Coverage Analysis

## Current Test Files
- **swift/integration_test.go** (1486 lines) - Swift layer integration tests (19 tests)
- **wire/wire_test.go** (360 lines) - Wire layer unit tests (5 tests)

## Wire Layer Test Coverage (wire/wire_test.go)

### ✅ Excellent Coverage - Prevents Dual-Socket Regression

1. **TestSingleConnection** - Verifies only ONE connection exists per device pair
   - Both sides see connection
   - GetConnectedPeers() returns 1 peer, not 2

2. **TestConnectionRoles** - Verifies roles assigned correctly
   - Initiator becomes Central
   - Acceptor becomes Peripheral
   - GetConnectionRole() returns correct role

3. **TestBidirectionalGATTMessages** - Verifies both directions work over single connection
   - Central can send write requests to Peripheral
   - Peripheral can send notifications to Central
   - Same connection used for both

4. **TestDisconnectCallback** - Verifies disconnect detection
   - Both sides detect disconnect
   - IsConnected() returns false after disconnect

5. **TestNoDoubleConnection** - Verifies connecting twice doesn't create duplicate connections
   - Connect() is idempotent
   - Still only 1 peer in GetConnectedPeers()

**Wire Layer Summary:** 5 tests, ~360 lines, excellent coverage of single-socket architecture

## Architectural Issues from Old Code (apb/main) That Need Test Coverage

Based on review of `/Users/aa/os/apb` (old broken code) and `~/Documents/fix.txt`, the following architectural flaws existed:

### 1. Dual-Socket Architecture (FIXED - Need Tests)
**Old Problem:** `wire/wire.go:109-127` had `DualConnection` with separate `asCentral` and `asPeripheral` connections
- Two TCP sockets per device pair
- Connection state split across multiple maps
- Partial connection failures (one socket drops, other stays alive)

**New Architecture:** Single socket per device, single connection per device pair
**Test Coverage Needed:**
- ✅ `TestCentralPeripheralConnection` - Verifies single connection
- ✅ `TestRoleAssignment` - Verifies role based on initiator
- ❌ **MISSING:** Test that verifies no dual connections exist after Connect()
- ❌ **MISSING:** Test that connection drop closes completely (not partial)

### 2. UUID vs DeviceID Identity Crisis (FIXED - Need Tests)
**Old Problem:** Mixing Hardware UUIDs (BLE wire ID) with DeviceIDs (base36 logical ID)
- Race condition: Photo chunks arrive BEFORE handshake populates UUID→DeviceID mapping
- Wrong device responds because routing conflates UUIDs and DeviceIDs
- `phone/photo_handler.go:122-162` sent chunks to senderUUID but needed deviceID lookup

**New Architecture:** Hardware UUID is ONLY identifier in wire/swift layers
**Test Coverage Needed:**
- ✅ Tests use UUIDs directly (no DeviceID in swift layer)
- ❌ **MISSING:** Test that explicitly verifies wire layer never sees DeviceID
- ❌ **MISSING:** Test that message routing uses Hardware UUID only

### 3. Callback Hell (FIXED - Need Tests)
**Old Problem:** 8 layers of indirection: wire → phone → handlers → coordinators → callbacks → wire
- Impossible to trace message paths
- Callbacks capture closures from different scopes → shared state bugs

**New Architecture:** Direct delegate pattern (1-hop callbacks matching iOS API)
**Test Coverage Needed:**
- ✅ `TestGATTWriteRequest` - Tests write flow through delegates
- ✅ `TestGATTNotification` - Tests notification flow through delegates
- ❌ **MISSING:** Test that measures call stack depth (should be ≤4 layers)
- ❌ **MISSING:** Test that verifies no circular dependencies in message flow

## Current Test Coverage

### ✅ Well-Covered Areas

1. **Connection Establishment** (3 tests)
   - `TestCentralPeripheralConnection` - Discovery, connection, role verification
   - `TestRoleAssignment` - Correct role assignment based on initiator
   - `TestBidirectionalCommunication` - Both directions work over single connection

2. **GATT Operations** (4 tests)
   - `TestGATTWriteRequest` - Central writes to Peripheral characteristic
   - `TestGATTNotification` - Peripheral sends notifications to Central
   - `TestGATTMessageFormat` - GATT message structure validation
   - `TestBidirectionalCommunication` - Requests and notifications work together

3. **CBCentralManager** (4 tests)
   - `TestCBCentralManager_ScanForPeripherals_ServiceFiltering` - Service UUID filtering
   - `TestCBCentralManager_ShouldInitiateConnection` - Role negotiation logic
   - `TestCBCentralManager_AutoReconnect` - iOS auto-reconnect behavior
   - `TestCBCentralManager_CancelPeripheralConnection` - Stop auto-reconnect

4. **CBPeripheralManager** (3 tests)
   - `TestCBPeripheralManager_AddService` - GATT table updates
   - `TestCBPeripheralManager_StartAdvertising` - Advertising data
   - `TestCBPeripheralManager_UpdateValue_MultipleSubscribers` - Multi-central notifications

5. **CBPeripheral** (2 tests)
   - `TestCBPeripheral_WriteQueue` - Async write queue handling
   - `TestCBPeripheral_WriteWithoutResponse` - Fast write mode

6. **Edge Cases** (3 tests)
   - `TestEdgeCase_WriteToDisconnectedPeripheral` - Error handling
   - `TestEdgeCase_InvalidCharacteristic` - Input validation
   - `TestEdgeCase_AddServiceWhileAdvertising` - State validation

**Total: 19 tests covering core functionality**

### ❌ Missing Test Coverage

#### Critical Gaps (Must Add)

1. **GATT Message Routing Through Full Stack**
   - Wire receives bytes → Swift parses GATT message → Delegate callback
   - Need end-to-end test verifying message flow
   - Should test that HandleGATTMessage() routes correctly to Central vs Peripheral

2. **Connection State Transitions**
   - disconnected → connecting → connected → disconnecting → disconnected
   - Test all valid transitions
   - Test invalid transitions return errors

3. **Service Discovery**
   - DiscoverServices() with filtering
   - DiscoverServices() without filtering (all services)
   - Service discovery failure handling

4. **MTU Negotiation**
   - Default MTU (23 bytes)
   - MTU negotiation up to 185 bytes
   - Large message fragmentation

5. **Subscription Management**
   - SetNotifyValue(true) enables notifications
   - SetNotifyValue(false) disables notifications
   - Unsubscribe stops notifications from arriving
   - Multiple characteristics subscriptions

6. **Advertising Data Completeness**
   - All iOS kCBAdvData* keys present
   - Service UUIDs list correct
   - Manufacturer data binary format
   - TX power level integer

7. **Error Propagation Through Delegates**
   - DidFailToConnectPeripheral called on connection failure
   - DidDisconnectPeripheral called on disconnect
   - DidUpdateValueForCharacteristic called with error on read failure

8. **Concurrent Operations**
   - Multiple writes queued correctly
   - Multiple notifications delivered in order
   - Concurrent connections to different devices
   - Race condition testing

#### Important Gaps (Should Add)

9. **RetrievePeripheralsByIdentifiers()**
   - Returns peripherals that exist
   - Returns empty for non-existent UUIDs
   - Can connect without scanning

10. **Characteristic Properties Validation**
   - Can't write to read-only characteristic
   - Can't read from write-only characteristic
   - Can't subscribe to non-notifiable characteristic

11. **Connection Timeout**
   - Connection attempt times out after X seconds
   - Retry logic works correctly

12. **Disconnect During Operation**
   - Write fails gracefully if disconnected mid-operation
   - Notification stops if central disconnects
   - Subscribe request fails if disconnected

13. **GATT Response Messages**
   - Read operations receive responses
   - Response data matches request

14. **Platform-Specific Behavior**
   - iOS auto-reconnect vs Android manual reconnect (not in swift/ but worth documenting)

## Recommended New Test Files

Given that `integration_test.go` is already 1486 lines, create:

1. **connection_test.go** (~500 lines)
   - Connection state transitions
   - Connection timeout
   - Disconnect during operations
   - Concurrent connections

2. **gatt_routing_test.go** (~400 lines)
   - Message routing from wire → swift → delegate
   - HandleGATTMessage() routing logic
   - Request/response matching
   - Characteristic properties validation

3. **service_discovery_test.go** (~300 lines)
   - DiscoverServices() with/without filtering
   - DiscoverCharacteristics()
   - GATT table reading
   - Service discovery errors

4. **subscription_test.go** (~300 lines)
   - SetNotifyValue() enable/disable
   - Multiple characteristic subscriptions
   - Unsubscribe stops notifications
   - Subscription to multiple centrals

5. **mtu_fragmentation_test.go** (~300 lines)
   - MTU negotiation
   - Large message fragmentation
   - Reassembly verification
   - MTU-based chunking

## Anti-Pattern Tests (Prevent Regression)

These tests should explicitly verify that old architectural problems DO NOT return:

1. **TestNoDualConnections** - Verify only ONE connection exists per device pair
2. **TestNoUUIDDeviceIDMixing** - Verify wire layer never receives DeviceID
3. **TestNoCallbackHell** - Verify call stack depth ≤ 4 for message delivery
4. **TestDirectMessageFlow** - Verify no circular dependencies (wire → delegate → wire)
5. **TestSingleSocketPerDevice** - Verify only one socket file exists per device

## Test Metrics

- **Current Tests:** 24 (19 swift + 5 wire)
- **Current Lines of Test Code:** ~1850 lines
- **Recommended New Tests:** ~20-25 additional tests
- **Total Target:** 45-50 tests
- **Current Files:**
  - wire/wire_test.go (360 lines) ✅
  - swift/integration_test.go (1486 lines) - too large, needs splitting
- **Target Files:** 5-6 files (~300-500 lines each)

## Overall Coverage Assessment

### ✅ Well Protected Against Architectural Regressions

**Dual-Socket Architecture:** ✅ EXCELLENT
- wire/wire_test.go has 5 specific tests preventing dual-socket regression
- Tests verify single connection, correct roles, bidirectional communication
- No way to accidentally reintroduce DualConnection without failing tests

**UUID-only Routing:** ⚠️ GOOD (could be better)
- All tests use Hardware UUIDs
- No DeviceID in wire/ or swift/ packages
- **MISSING:** Explicit test that wire layer rejects DeviceID strings

**Direct Callback Flow:** ✅ GOOD
- Tests use direct delegate pattern (1-hop callbacks)
- No circular dependencies in test setup
- **MISSING:** Test measuring call stack depth

### ⚠️ Gaps in Swift Layer Coverage

The main gap is **service discovery and characteristic operations**:
- Service discovery with filtering (partially tested)
- Characteristic properties validation (not tested)
- Read operations (not tested)
- MTU negotiation (not tested)
- Connection state transitions (not tested)

## Next Steps

1. Create `connection_test.go` with connection state and lifecycle tests
2. Create `gatt_routing_test.go` with end-to-end message routing tests
3. Add anti-pattern tests to `integration_test.go` or new `regression_test.go`
4. Move some existing tests out of `integration_test.go` to specialized files
5. Run full test suite and measure coverage with `go test -cover`
