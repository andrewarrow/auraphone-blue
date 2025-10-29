# Wire Package Binary Protocol Refactor Plan

## Goal
Convert wire/ from JSON-over-length-prefix to real binary BLE protocols (L2CAP + ATT/GATT), while maintaining human-readable JSON debug files that are never used in the actual data flow.

## Current Status
**Phase 1 COMPLETED** ✅ (2025-10-29)
- Binary protocol foundation complete with L2CAP, ATT, and GATT layers
- 33 tests passing across 3 new packages (l2cap, att, gatt)
- Ready for Phase 2 integration with existing wire.go

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

## Phase 2: Binary Protocol Integration

### 2.1 Update Wire Protocol Core
- [ ] Modify `wire.go` to send/receive binary L2CAP packets instead of JSON
- [ ] Replace `sendMessage()` with `sendL2CAPPacket()`
- [ ] Replace message parsing with L2CAP/ATT decoding
- [ ] Update connection handshake to use ATT MTU Exchange (opcodes 0x02/0x03)

### 2.2 Update GATT Operations
- [ ] Rewrite `ReadCharacteristic()` to:
  - Look up handle from UUID
  - Send ATT Read Request (0x0A) with handle
  - Wait for ATT Read Response (0x0B) or Error (0x01)
  - Return value or error code

- [ ] Rewrite `WriteCharacteristic()` to:
  - Look up handle from UUID
  - Send ATT Write Request (0x12) with handle + value
  - Wait for ATT Write Response (0x13) or Error (0x01)

- [ ] Rewrite `WriteCharacteristicNoResponse()` to:
  - Look up handle from UUID
  - Send ATT Write Command (0x52) with handle + value
  - Do not wait for response

- [ ] Rewrite `NotifyCharacteristic()` to:
  - Look up handle from UUID
  - Send ATT Handle Value Notification (0x1B)

- [ ] Update subscription handling:
  - Writing to CCCD (handle 0x29 descriptor) enables/disables notifications
  - Use ATT Write Request to CCCD handle
  - Server tracks which clients have notifications enabled

### 2.3 Implement MTU Negotiation
- [ ] Send ATT MTU Request (0x02) on connection establishment
- [ ] Handle ATT MTU Response (0x03) and update connection MTU
- [ ] Enforce MTU strictly in all ATT operations
- [ ] Reject packets that exceed negotiated MTU

### 2.4 Implement Fragmentation
- [ ] Add `l2cap/fragmenter.go`:
  - Split large ATT payloads into multiple L2CAP packets
  - Use ATT Prepare Write (0x08) + Execute Write (0x18) for long writes
  - Reassemble fragmented reads

- [ ] Update read operations to handle long attributes
- [ ] Update write operations to use prepare/execute for values > MTU-3

---

## Phase 3: Advertising & Discovery Binary Protocol

### 3.1 Implement Binary Advertising Packets
- [ ] Create `advertising/packet.go` with advertising PDU structure:
  ```
  [PDU Type: 1 byte] [Length: 1 byte] [AdvA: 6 bytes] [AdvData: 0-31 bytes]
  ```

- [ ] Implement advertising data TLV encoding:
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

- [ ] Enforce 31-byte advertising data limit
- [ ] Support scan response data (additional 31 bytes)

### 3.2 Update Discovery Mechanism
- [ ] Store advertising packets as binary files: `{base_dir}/{device_uuid}/advertising.bin`
- [ ] Parse binary advertising data in `ListAvailableDevices()`
- [ ] Extract service UUIDs and device name from advertising data
- [ ] Calculate realistic RSSI based on simulated distance

---

## Phase 4: Debug JSON Generation

### 4.1 Create Debug Logging Infrastructure
- [ ] Create `debug/logger.go`:
  ```go
  type DebugLogger struct {
      baseDir string
      enabled bool
  }

  func (d *DebugLogger) LogL2CAPPacket(direction string, packet *l2cap.Packet)
  func (d *DebugLogger) LogATTPacket(direction string, packet *att.Packet)
  func (d *DebugLogger) LogAdvertising(deviceUUID string, advData []byte)
  func (d *DebugLogger) LogAttributeDatabase(deviceUUID string, db *gatt.AttributeDatabase)
  ```

- [ ] Write JSON representations to `{base_dir}/{device_uuid}/debug/` directory
- [ ] Never read these JSON files in production code
- [ ] Add timestamps and sequence numbers to debug logs

### 4.2 Create Human-Readable Formatters
- [ ] Create `debug/formatters.go`:
  - Format L2CAP packets with channel names
  - Format ATT packets with opcode names and decoded fields
  - Format attribute databases with handle maps
  - Format advertising data with TLV breakdown

- [ ] Add hex dumps with ASCII annotations
- [ ] Color-code output for terminal viewing (optional)

### 4.3 Add Debug Configuration
- [ ] Add `Wire.EnableDebugLogging(baseDir string)` method
- [ ] Add environment variable `WIRE_DEBUG=1` to enable globally
- [ ] Ensure zero performance impact when debugging is disabled

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

### 6.4 Performance Testing
- [ ] Benchmark binary encoding vs old JSON encoding
- [ ] Verify MTU enforcement
- [ ] Test fragmentation performance
- [ ] Measure memory usage with many connections

---

## Phase 7: Documentation

### 7.1 Update Package Documentation
- [ ] Document binary protocol format in README
- [ ] Add packet format diagrams
- [ ] Document debug logging usage
- [ ] Add examples of binary packet inspection

### 7.2 Add Protocol Reference
- [ ] Document all supported ATT opcodes
- [ ] Document L2CAP channel IDs
- [ ] Document error codes
- [ ] Document handle allocation rules

### 7.3 Migration Guide
- [ ] Document breaking changes
- [ ] Provide migration examples
- [ ] Note changes to kotlin/ and swift/ packages (out of scope for now)

---

## Implementation Order

### Week 1: Foundation
1. L2CAP packet encoding/decoding
2. ATT packet structures and basic opcodes (Read/Write/Error)
3. Basic binary wire protocol (replace JSON)

### Week 2: GATT Layer
4. Handle allocation system
5. Attribute database builder
6. GATT operations using handles
7. MTU negotiation

### Week 3: Advanced Features
8. Fragmentation/reassembly
9. GATT discovery protocol
10. Binary advertising packets

### Week 4: Polish
11. Debug JSON logging infrastructure
12. Error handling and validation
13. Update all tests
14. Documentation

---

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

**Remaining (Future Phases):**
- [ ] Wire.go integration with binary L2CAP/ATT encoding
- [ ] JSON only written to debug files, never read
- [ ] MTU negotiation on connection establishment
- [ ] GATT discovery protocol (service/characteristic/descriptor discovery)
- [ ] Binary advertising packets
- [ ] All existing wire tests pass with binary protocol
- [ ] Debug JSON files for troubleshooting
- [ ] No performance regression vs current implementation
- [ ] Ready for kotlin/ and swift/ integration (future work)

---

## Out of Scope (Future Work)

- SMP (Security Manager Protocol) - encryption/pairing
- LE Secure Connections
- Connection parameter updates
- Channel hopping simulation
- Physical layer simulation
- Link Layer control PDUs
- Multiple simultaneous connections to same device
- Changes to kotlin/ and swift/ packages (will break until they're updated)

---

## Notes

- All changes stay in wire/ directory per CLAUDE.md
- Breaking changes to kotlin/ and swift/ are expected and acceptable
- Focus on realism for BLE radio layer
- iOS and Android specifics stay in their respective packages
- Debug files should be easy to grep and inspect
- Consider adding a debug packet viewer tool later
