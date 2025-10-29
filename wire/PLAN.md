# Wire Package Binary Protocol Refactor Plan

## Goal
Convert wire/ from JSON-over-length-prefix to real binary BLE protocols (L2CAP + ATT/GATT), while maintaining human-readable JSON debug files that are never used in the actual data flow.

## Current Status
**Phase 1, 2.1-2.5, 3.1-3.2, 4.1-4.2 COMPLETED** ✅ (2025-10-29)

### ✅ Completed Today
- **Phase 1**: Binary protocol foundation (L2CAP, ATT, GATT layers)
- **Phase 2.1**: Wire.go migrated to binary L2CAP/ATT protocol
- **Phase 2.2**: GATT operations converted to binary ATT packets
- **Phase 2.3**: MTU negotiation on connection establishment
- **Phase 2.4**: Fragmentation for large writes using Prepare Write + Execute Write
- **Phase 2.5**: Request/response tracking with timeouts
- **Phase 3.1**: Binary advertising packets with TLV encoding
- **Phase 3.2**: Discovery mechanism with binary advertising data
- **Phase 4.1**: Debug logging infrastructure with human-readable JSON
- **Phase 4.2**: Human-readable formatters for L2CAP, ATT, and advertising data

### 📊 Current State
- **Tests**: 88/88 passing across 6 packages (wire, l2cap, att, gatt, advertising, debug)
  - wire: 4 tests (1 existing + 3 MTU tests)
  - wire/connection_params_test.go: 5 tests (NEW!)
  - wire/mtu_enforcement_test.go: 3 tests (NEW!)
  - l2cap: 17 tests (6 packet + 11 connection params) (NEW!)
  - att: 27 tests
  - gatt: 16 tests
  - advertising: 25 tests
- **Binary Protocol**: Fully functional L2CAP + ATT communication
- **MTU Negotiation**: Working with request/response tracking, negotiates to 512 bytes
- **MTU Enforcement**: ✅ Verified across all code paths with comprehensive tests
- **Request/Response Tracking**: ✅ Implemented with 30s default timeout
- **Fragmentation**: Automatic for writes > MTU-3, uses Prepare Write + Execute Write
- **Connection Parameters**: ✅ Implemented L2CAP connection parameter update protocol (NEW!)
- **Advertising**: Binary PDU encoding with 31-byte limit, TLV AD structures
- **Debug Files**: `l2cap_packets.jsonl`, `att_packets.jsonl`, `gatt_operations.jsonl`, `advertising.json`
- **Backward Compatibility**: GATTMessage conversion layer for existing handlers

### 🔧 Implementation Details
- Wire now sends/receives binary L2CAP packets (little-endian)
- ATT packets properly encoded with opcodes 0x02 (MTU Request), 0x03 (MTU Response)
- **Request Tracker**: Enforces one-request-at-a-time ATT constraint, matches responses, handles timeouts
- **Timeout Handling**: Pending requests automatically timeout after 30s, callbacks supported
- **Connection Cleanup**: Pending requests cancelled on disconnect
- Temporary UUID-to-handle mapping (hash-based, will be replaced with discovery)
- Debug logging enabled by default (disable with `WIRE_DEBUG=0`)

### 📝 Known Limitations
- UUID-to-handle mapping is hash-based (needs proper GATT discovery)
- Execute Write doesn't deliver reassembled data to GATT handler yet
- Subscription/CCCD writes not yet implemented

### 🎯 Next Steps
**Completed Today (2025-10-29):**
- ✅ Phase 4.2: Human-readable formatters (already implemented in debug/logger.go)
- ✅ Section 6.4: MTU enforcement verification
- ✅ Section 6.4: Connection parameter updates

**Suggested Next:**
- Physical layer simulation (realistic latency based on connection parameters)
- Link Layer control PDUs
- Proper GATT service discovery (Phase 1.4)
- CCCD writes for subscriptions

### 📦 Binary Protocol Stack (Current)
```
┌─────────────────────────────────────────────┐
│  GATT Operations (Application Layer)       │
│  ReadCharacteristic(), WriteCharacteristic()│
│  NotifyCharacteristic()                     │
└─────────────────┬───────────────────────────┘
                  │ converts via SendGATTMessage()
                  ↓
┌─────────────────────────────────────────────┐
│  ATT Protocol (Binary)                      │
│  Opcodes: 0x02 (MTU Req), 0x0A (Read),     │
│           0x12 (Write), 0x1B (Notify)       │
│  Handle-based addressing                    │
└─────────────────┬───────────────────────────┘
                  │ wrapped in L2CAP
                  ↓
┌─────────────────────────────────────────────┐
│  L2CAP (Binary Transport)                   │
│  Channel 0x0004 (ATT)                       │
│  [Len:2][Channel:2][Payload:N]              │
└─────────────────┬───────────────────────────┘
                  │ over Unix sockets
                  ↓
┌─────────────────────────────────────────────┐
│  Unix Domain Socket (Simulated Radio)      │
│  /tmp/auraphone-{uuid}.sock                 │
└─────────────────────────────────────────────┘

Debug JSON (Write-Only) →  ~/.apb/{session}/{uuid}/debug/
                           ├── l2cap_packets.jsonl
                           ├── att_packets.jsonl
                           └── gatt_operations.jsonl
```

### 🔍 Example Packet Flow (MTU Negotiation)
```
Device A (Central)                    Device B (Peripheral)
────────────────                      ──────────────────────
Connect() →
  ↓
  MTU Request (0x02)
  L2CAP: [03 00][04 00][02 00 02]
         len=3  ATT    MTU op, 512
  ───────────────────────────────→
                                     handleATTPacket()
                                     ← MTU Response (0x03)
                                       L2CAP: [03 00][04 00][03 00 02]
                                              len=3  ATT    MTU resp, 512
  ←───────────────────────────────
MTU negotiated: 512 bytes
connection.mtu = 512
```

---

## Phase 1: Binary Protocol Foundation ✅ COMPLETED

### 1.1 Implement L2CAP Layer ✅
- [x] Create `l2cap/packet.go` with binary packet structure:
  ```
  [Length: 2 bytes] [Channel ID: 2 bytes] [Payload: N bytes]
  ```
- [x] Define L2CAP channel IDs:
  - `0x0004`: ATT channel
  - `0x0005`: LE signaling
  - `0x0006`: SMP channel (future)
- [x] Implement `l2cap.Encode()` and `l2cap.Decode()`
- [x] Add fragmentation support for payloads exceeding MTU
- [x] Add reassembly logic for fragmented packets
- [x] Comprehensive test coverage (6 test functions, all passing)

**Files Created:**
- `l2cap/packet.go` - Binary packet encoding/decoding with fragmentation
- `l2cap/packet_test.go` - Full test coverage

### 1.2 Implement ATT Protocol Layer ✅
- [x] Create `att/opcodes.go` with all ATT opcodes:
  - `0x01`: Error Response
  - `0x02`: MTU Request
  - `0x03`: MTU Response
  - `0x0A`: Read Request
  - `0x0B`: Read Response
  - `0x12`: Write Request
  - `0x13`: Write Response
  - `0x52`: Write Command (no response)
  - `0x1B`: Handle Value Notification
  - `0x1D`: Handle Value Indication
  - `0x10`: Read By Type Request (for discovery)
  - `0x11`: Read By Type Response
  - `0x04`: Find Information Request
  - `0x05`: Find Information Response
  - `0x06`: Read By Group Type Request (service discovery)
  - `0x07`: Read By Group Type Response
  - `0x08`: Write Request Prepare
  - `0x09`: Write Response Prepare
  - `0x18`: Execute Write Request
  - `0x19`: Execute Write Response

- [x] Create `att/packet.go` with ATT packet structures for all major operations

- [x] Implement `att.EncodePacket()` and `att.DecodePacket()` for all packet types
- [x] Create `att/errors.go` with error codes:
  - `0x01`: Invalid Handle
  - `0x02`: Read Not Permitted
  - `0x03`: Write Not Permitted
  - `0x04`: Invalid PDU
  - `0x05`: Insufficient Authentication
  - `0x06`: Request Not Supported
  - `0x07`: Invalid Offset
  - `0x08`: Insufficient Authorization
  - `0x09`: Prepare Queue Full
  - `0x0A`: Attribute Not Found
  - `0x0B`: Attribute Not Long
  - `0x0C`: Insufficient Encryption Key Size
  - `0x0D`: Invalid Attribute Value Length
  - `0x0E`: Unlikely Error
  - `0x0F`: Insufficient Encryption
  - `0x10`: Unsupported Group Type
  - `0x11`: Insufficient Resources
- [x] Helper functions for opcode validation and response matching
- [x] Comprehensive test coverage (11 test functions, all passing)

**Files Created:**
- `att/opcodes.go` - All ATT opcodes with helper functions
- `att/errors.go` - ATT error codes and error type
- `att/packet.go` - Binary encoding/decoding for all ATT packet types
- `att/packet_test.go` - Full test coverage for all packet types

### 1.3 Implement GATT Attribute Handle System ✅
- [x] Create `gatt/handles.go` with handle allocation:
  - Thread-safe attribute database
  - Handle-based attribute lookup
  - UUID helpers (16-bit and 128-bit)
  - Well-known GATT UUIDs

- [x] Implement handle allocation algorithm:
  - Service declarations start at 0x0001
  - Characteristics follow their service
  - Descriptors (CCCD, etc.) follow their characteristic
  - Handles are sequential and never reused

- [x] Create `gatt/service_builder.go`:
  - `BuildAttributeDatabase(services []Service)` - Converts high-level service definitions to handle-based attributes
  - Assigns handles automatically
  - Creates service, characteristic, and descriptor entries
  - Auto-generates CCCD for notify/indicate characteristics
  - Helper functions for common services (Generic Access, Generic Attribute)

- [x] Comprehensive test coverage including concurrency tests (16 test functions, all passing)

**Files Created:**
- `gatt/handles.go` - Attribute database with handle allocation
- `gatt/service_builder.go` - High-level service to binary attribute conversion
- `gatt/handles_test.go` - Full test coverage including concurrency
- `gatt/service_builder_test.go` - Service building and CCCD generation tests

### 1.4 Implement GATT Discovery Protocol
- [ ] Create `gatt/discovery.go` with discovery operations:
  - `DiscoverPrimaryServices()`: Uses ATT Read By Group Type (0x10)
  - `DiscoverCharacteristics()`: Uses ATT Read By Type (0x08)
  - `DiscoverDescriptors()`: Uses ATT Find Information (0x04)

- [ ] Implement server-side handlers for discovery requests
- [ ] Add realistic delays (100-500ms per discovery operation)

**Note:** Discovery protocol functionality is deferred to Phase 2 integration as it requires wire.go connection handling.

---

## Phase 2: Binary Protocol Integration (IN PROGRESS)

### 2.1 Update Wire Protocol Core ✅ COMPLETED
- [x] Modify `wire.go` to send/receive binary L2CAP packets instead of JSON
- [x] Replace `sendMessage()` with `sendL2CAPPacket()`
- [x] Replace message parsing with L2CAP/ATT decoding
- [x] Update connection handshake to use ATT MTU Exchange (opcodes 0x02/0x03)

### 2.2 Update GATT Operations ✅ COMPLETED
- [x] Rewrite `ReadCharacteristic()` to:
  - Look up handle from UUID (using temporary hash-based mapping)
  - Send ATT Read Request (0x0A) with handle
  - Convert responses back to GATTMessage for backward compatibility

- [x] Rewrite `WriteCharacteristic()` to:
  - Look up handle from UUID (using temporary hash-based mapping)
  - Send ATT Write Request (0x12) with handle + value
  - Convert responses back to GATTMessage for backward compatibility

- [x] Rewrite `WriteCharacteristicNoResponse()` to:
  - Look up handle from UUID (using temporary hash-based mapping)
  - Send ATT Write Command (0x52) with handle + value
  - No response expected

- [x] Rewrite `NotifyCharacteristic()` to:
  - Look up handle from UUID (using temporary hash-based mapping)
  - Send ATT Handle Value Notification (0x1B)

- [x] Update `SendGATTMessage()` to convert high-level GATTMessage to binary ATT packets
- [x] Add `attToGATTMessage()` to convert incoming ATT packets to GATTMessage for handlers
- [x] Implement temporary UUID-to-handle mapping (hash-based, will be replaced with proper discovery later)

**Note:** Subscription handling (CCCD writes) deferred to Phase 2.3

### 2.3 Implement MTU Negotiation ✅ COMPLETED
- [x] Send ATT MTU Request (0x02) on connection establishment (Central role)
- [x] Handle ATT MTU Request (0x02) and respond with MTU Response (Peripheral role)
- [x] Handle ATT MTU Response (0x03) and update connection MTU
- [x] Negotiated MTU stored in Connection struct
- [x] Enforce MTU strictly in all ATT operations (implemented in Phase 2.4)
- [x] Reject packets that exceed negotiated MTU with descriptive errors

### 2.4 Implement Fragmentation ✅ COMPLETED
- [x] Add `att/fragmenter.go`:
  - Split large write values into multiple Prepare Write requests
  - Use ATT Prepare Write (0x16) + Execute Write (0x18) for long writes
  - Reassemble fragmented writes on receiving side
  - Per-connection fragmenter with offset validation

- [x] Update write operations to use prepare/execute for values > MTU-3
- [x] Add MTU enforcement to reject oversized packets
- [x] Comprehensive test coverage (7 test functions, all passing)

### 2.5 Implement Request/Response Tracking ✅ COMPLETED
- [x] Create `att/request_tracker.go`:
  - Manages pending ATT requests (only one at a time per connection)
  - Matches responses to requests by opcode
  - Automatic timeout handling (default 30 seconds)
  - Thread-safe request tracking
  - Support for timeout callbacks

- [x] Integrate request tracker into Wire:
  - Initialize tracker for each connection (Central and Peripheral)
  - Track MTU exchange requests/responses
  - Track Prepare Write requests/responses
  - Track Execute Write requests/responses
  - Track Read/Write requests/responses
  - Cancel pending requests on disconnect

- [x] Add timeout handling:
  - Pending requests timeout after 30 seconds
  - Timeout delivers error to caller
  - Optional callback on timeout

- [x] Add error handling:
  - ATT Error Response completes pending request
  - Connection errors fail pending request
  - Wrong response opcode rejected with error

- [x] Comprehensive test coverage:
  - Single request/response flow
  - Only one request at a time enforcement
  - Timeout scenarios
  - Timeout callbacks
  - Error responses
  - Failed requests
  - Pending request cancellation
  - Wrong response opcode handling
  - Response opcode mapping
  - 9 test functions, all passing

**Files Created:**
- `att/request_tracker.go` (215 lines) - Request tracking with timeout handling
- `att/request_tracker_test.go` (9 tests) - Full test coverage

---

## Phase 3: Advertising & Discovery Binary Protocol ✅ COMPLETED (Phase 3.1)

### 3.1 Implement Binary Advertising Packets ✅ COMPLETED
- [x] Create `advertising/packet.go` with advertising PDU structure:
  ```
  [PDU Type: 1 byte] [Length: 1 byte] [AdvA: 6 bytes] [AdvData: 0-31 bytes]
  ```

- [x] Implement advertising data TLV encoding:
  ```
  [Length: 1 byte] [Type: 1 byte] [Value: N bytes]
  ```
  - Type 0x01: Flags
  - Type 0x02: Incomplete 16-bit Service UUIDs
  - Type 0x03: Complete 16-bit Service UUIDs
  - Type 0x06: Incomplete 128-bit Service UUIDs
  - Type 0x07: Complete 128-bit Service UUIDs
  - Type 0x08: Shortened Local Name
  - Type 0x09: Complete Local Name
  - Type 0x0A: Tx Power Level
  - Type 0xFF: Manufacturer Specific Data

- [x] Enforce 31-byte advertising data limit
- [x] Helper functions for common AD structures (flags, names, UUIDs, Tx power, manufacturer data)
- [x] Helper functions to extract data from AD structures
- [x] Human-readable names for PDU types and AD types
- [x] Comprehensive test coverage (25 tests, all passing)

**Files Created:**
- `advertising/packet.go` (367 lines) - Binary advertising PDU and TLV encoding
- `advertising/packet_test.go` (25 tests) - Full test coverage

### 3.2 Update Discovery Mechanism ✅ COMPLETED
- [x] Store advertising packets as binary files: `{base_dir}/{device_uuid}/advertising.bin`
- [x] Parse binary advertising data in `ReadAdvertisingData()`
- [x] Extract service UUIDs (16-bit and 128-bit) from advertising data
- [x] Extract device name from advertising data
- [x] Extract manufacturer data from advertising data
- [x] Extract Tx power level from advertising data
- [x] Determine connectability from PDU type and flags
- [x] Write debug JSON to `debug/advertising.json` (write-only, never read)
- [ ] Calculate realistic RSSI based on simulated distance (future work)
- [ ] Support scan response data (additional 31 bytes) (future work)

---

## Phase 4: Debug JSON Generation (IN PROGRESS)

### 4.1 Create Debug Logging Infrastructure ✅ COMPLETED
- [x] Create `debug/logger.go`:
  - `DebugLogger` struct with device UUID and debug directory
  - `LogL2CAPPacket()` - logs to `debug/l2cap_packets.jsonl`
  - `LogATTPacket()` - logs to `debug/att_packets.jsonl`
  - `LogGATTOperation()` - logs to `debug/gatt_operations.jsonl`

- [x] Write JSON representations to `{device_cache_dir}/{device_uuid}/debug/` directory
- [x] Never read these JSON files in production code (write-only)
- [x] Add timestamps (RFC3339Nano format) to all debug logs
- [x] Include direction ("tx"/"rx"), peer UUID, and decoded data
- [x] Hex dumps of raw binary data included
- [x] Human-readable names for opcodes, channels, and error codes
- [x] Integrated into wire.go at all send/receive points
- [x] Enabled by default (can disable with `WIRE_DEBUG=0`)

### 4.2 Create Human-Readable Formatters ✅ COMPLETED
- [x] Create `debug/formatters.go`:
  - Format L2CAP packets with channel names (implemented in debug/logger.go:172-193)
  - Format ATT packets with opcode names and decoded fields (implemented in debug/logger.go:195-268)
  - Format attribute databases with handle maps (basic implementation complete)
  - Format advertising data with TLV breakdown (completed in Phase 3.1)

---

## Phase 5: Error Handling & Validation

### 5.1 Implement ATT Error Responses
- [ ] Add error handling to all ATT operations
- [ ] Return proper ATT error codes instead of generic errors
- [ ] Log errors to debug JSON files

### 5.2 Add Protocol Validation
- [ ] Validate L2CAP packet lengths
- [ ] Validate ATT opcode validity
- [ ] Validate handle ranges (0x0001 - 0xFFFF, 0x0000 reserved)
- [ ] Validate UUID formats
- [ ] Validate MTU constraints

### 5.3 Add Connection State Machine
- [ ] Track connection state: Disconnected → Connecting → Connected → Disconnecting
- [ ] Reject operations in invalid states
- [ ] Handle race conditions during connection/disconnection

---

## Phase 6: Testing & Migration

### 6.1 Update Existing Tests
- [ ] Update all tests in `wire_test.go` to expect binary protocol
- [ ] Add test utilities for creating binary packets
- [ ] Add test utilities for parsing binary responses

### 6.2 Add New Binary Protocol Tests
- [ ] Test L2CAP encoding/decoding
- [ ] Test ATT packet encoding/decoding
- [ ] Test handle allocation
- [ ] Test fragmentation/reassembly
- [ ] Test MTU negotiation
- [ ] Test all ATT opcodes
- [ ] Test error conditions

### 6.3 Add Integration Tests
- [ ] Test full GATT service discovery flow
- [ ] Test read/write/notify operations end-to-end
- [ ] Test multiple concurrent connections
- [ ] Test connection parameters
- [ ] Test advertising packet parsing

### 6.4 Misc
- [x] Verify MTU enforcement (2025-10-29)
  - Created comprehensive MTU enforcement tests
  - Verified fragmentation logic works correctly
  - Verified MTU negotiation between devices
  - Verified per-connection MTU tracking
- [x] Connection parameter updates (2025-10-29)
  - Implemented L2CAP connection parameter update protocol
  - Added ConnectionParameters type with validation
  - Added Request/Response encoding/decoding
  - Integrated with Wire layer via L2CAP signaling channel
  - Created comprehensive tests for parameter updates
  - Supports Fast, Default, and Power-Saving parameter presets
- [ ] Physical layer simulation
- [ ] Link Layer control PDUs
- [ ] Multiple simultaneous connections to same device

## Success Criteria

**Phase 1 (Foundation):**
- ✅ L2CAP binary packet encoding/decoding with fragmentation support
- ✅ All ATT opcodes implemented with proper binary encoding
- ✅ ATT error codes defined and integrated
- ✅ Handle-based attribute database with automatic allocation
- ✅ Service builder converting high-level definitions to binary attributes
- ✅ CCCD auto-generation for notify/indicate characteristics
- ✅ Thread-safe operations throughout
- ✅ Comprehensive test coverage (33 tests, all passing)

**Progress Update:**
- ✅ Wire.go integration with binary L2CAP/ATT encoding (Phase 2.1-2.3)
- ✅ JSON only written to debug files, never read (Phase 4.1)
- ✅ MTU negotiation on connection establishment (Phase 2.3)
- ✅ All existing wire tests pass with binary protocol (34/34 tests passing)
- ✅ Debug JSON files for troubleshooting (Phase 4.1)
- [ ] GATT discovery protocol (service/characteristic/descriptor discovery) (Phase 2.4/3)
- [ ] Binary advertising packets (Phase 3)
- [ ] Ready for kotlin/ and swift/ integration (future work)

## Notes

- All changes stay in wire/ directory per CLAUDE.md
- Breaking changes to kotlin/ and swift/ are expected and acceptable
- Focus on realism for BLE radio layer
- iOS and Android specifics stay in their respective packages
- Debug files should be easy to grep and inspect
- Consider adding a debug packet viewer tool later

---

## Files Created/Modified (2025-10-29)

### New Files Created
```
wire/
├── l2cap/
│   ├── packet.go              (158 lines) - L2CAP packet encoding/decoding
│   ├── packet_test.go         (6 tests) - L2CAP tests
│   ├── connection_params.go   (207 lines) - Connection parameter protocol (NEW 2025-10-29)
│   └── connection_params_test.go (11 tests) - Connection param tests (NEW 2025-10-29)
├── att/
│   ├── opcodes.go         (178 lines) - ATT opcodes and helpers
│   ├── errors.go          (119 lines) - ATT error codes
│   ├── packet.go          (451 lines) - ATT packet encoding/decoding
│   ├── packet_test.go     (11 tests) - ATT tests
│   ├── fragmenter.go      (152 lines) - Write fragmentation/reassembly
│   ├── fragmenter_test.go (7 tests) - Fragmentation tests
│   ├── request_tracker.go (215 lines) - Request/response tracking with timeouts
│   └── request_tracker_test.go (9 tests) - Request tracker tests
├── gatt/
│   ├── handles.go         (155 lines) - Attribute database
│   ├── handles_test.go    (9 tests) - Handle tests
│   ├── service_builder.go (181 lines) - Service builder
│   └── service_builder_test.go (7 tests) - Builder tests
├── advertising/
│   ├── packet.go          (367 lines) - Binary advertising PDU and TLV encoding
│   └── packet_test.go     (25 tests) - Advertising tests
├── debug/
│   └── logger.go          (250 lines) - Debug JSON logging
├── mtu_enforcement_test.go   (199 lines, 3 tests) - MTU verification tests (NEW 2025-10-29)
└── connection_params_test.go (267 lines, 5 tests) - Connection parameter tests (NEW 2025-10-29)
```

### Modified Files
```
wire/
├── wire.go               - Binary L2CAP/ATT protocol integration + fragmentation + connection params
│   ├── Added imports: att, l2cap, debug packages
│   ├── Added debugLogger field to Wire struct
│   ├── Modified readMessages() for L2CAP decoding
│   ├── Added handleATTPacket() for ATT routing (includes Prepare/Execute Write)
│   ├── Added handleL2CAPSignaling() for connection parameter updates (NEW 2025-10-29)
│   ├── Added sendL2CAPPacket() for binary transport
│   ├── Added sendATTPacket() for ATT operations with MTU enforcement
│   ├── Added sendFragmentedWrite() for long writes
│   ├── Modified SendGATTMessage() to detect and fragment long writes
│   ├── Added attToGATTMessage() for backward compat
│   ├── Added uuidToHandle() for handle mapping
│   ├── Added MTU negotiation in Connect() with request tracking
│   ├── Connection initialization with fragmenter, request tracker, and connection params (NEW 2025-10-29)
│   ├── Added request/response completion in handleATTPacket()
│   ├── Cancel pending requests in Disconnect()
│   ├── Added RequestConnectionParameterUpdate() (NEW 2025-10-29)
│   └── Added GetConnectionParameters() (NEW 2025-10-29)
├── discovery.go          - Binary advertising packet support
│   ├── Added import: advertising package
│   ├── Modified ReadAdvertisingData() to parse binary PDU and AD structures
│   ├── Modified WriteAdvertisingData() to encode binary PDU with TLV structures
│   ├── Stores binary packets in advertising.bin
│   └── Writes debug JSON to debug/advertising.json
├── constants.go          - Already had MTU constants (no changes needed)
└── types.go             - Added fragmenter, requestTracker, params, paramsUpdatedAt fields to Connection struct (UPDATED 2025-10-29)
```

### Test Results
```
Package                  Tests    Status
────────────────────────────────────────
wire                     12/12    ✅ PASS (1 existing + 3 MTU + 5 conn params + 3 integration)
wire/l2cap              17/17    ✅ PASS (6 packet + 11 connection params)
wire/att               27/27     ✅ PASS (11 packet + 7 fragmenter + 9 tracker)
wire/gatt              16/16     ✅ PASS
wire/advertising       25/25     ✅ PASS
wire/debug               -       (no test files)
────────────────────────────────────────
Total                  97/97     ✅ ALL PASSING
```

**Latest Updates (2025-10-29):**
- Added MTU enforcement verification tests (3 tests)
- Added connection parameter update tests (5 tests)
- Added L2CAP connection parameter protocol (11 tests)
- Total: +19 tests, all passing

### Debug Output Example
After running tests, debug files are created:
```
~/.apb/{session_id}/{device_uuid}/
├── debug/
│   ├── l2cap_packets.jsonl     - L2CAP layer packets
│   ├── att_packets.jsonl       - ATT protocol packets
│   ├── gatt_operations.jsonl   - GATT operations
│   └── advertising.json        - Advertising packet debug info
├── advertising.bin             - Binary advertising PDU (production)
├── connection_events.jsonl     - Connection audit log
└── socket_health.json          - Socket health metrics
```

### Key Code Snippets

**L2CAP Packet Format (wire/l2cap/packet.go:37)**
```go
func (p *Packet) Encode() []byte {
    buf := make([]byte, L2CAPHeaderLen+len(p.Payload))
    binary.LittleEndian.PutUint16(buf[0:2], uint16(len(p.Payload)))
    binary.LittleEndian.PutUint16(buf[2:4], p.ChannelID)
    copy(buf[4:], p.Payload)
    return buf
}
```

**ATT MTU Exchange (wire/att/packet.go:134)**
```go
case *ExchangeMTURequest:
    buf := make([]byte, 3)
    buf[0] = OpExchangeMTURequest  // 0x02
    binary.LittleEndian.PutUint16(buf[1:3], p.ClientRxMTU)
    return buf, nil
```

**Wire Integration (wire/wire.go:413)**
```go
// Debug log: L2CAP packet received
w.debugLogger.LogL2CAPPacket("rx", peerUUID, l2capPacket)

// Route based on L2CAP channel
switch l2capPacket.ChannelID {
case l2cap.ChannelATT:
    attPacket, err := att.DecodePacket(l2capPacket.Payload)
    w.debugLogger.LogATTPacket("rx", peerUUID, attPacket, l2capPacket.Payload)
    w.handleATTPacket(peerUUID, connection, attPacket)
}
```

**Debug JSON Output (device-a-uuid/debug/att_packets.jsonl)**
```json
{
  "timestamp": "2025-10-29T07:22:36.466245-07:00",
  "direction": "tx",
  "peer_uuid": "device-b-uuid",
  "opcode": "0x02",
  "opcode_name": "Exchange MTU Request",
  "data": {"client_rx_mtu": 512},
  "raw_hex": "020002"
}
```
