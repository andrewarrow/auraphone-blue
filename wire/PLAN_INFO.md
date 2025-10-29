# Wire Package - Technical Details

**Last Updated**: 2025-10-29

## 📦 Binary Protocol Stack

```
┌─────────────────────────────────────────────┐
│  GATT Operations (Application Layer)       │
│  ReadCharacteristic(), WriteCharacteristic()│
│  NotifyCharacteristic(), DiscoverServices() │
└─────────────────┬───────────────────────────┘
                  │ converts via SendGATTMessage()
                  ↓
┌─────────────────────────────────────────────┐
│  ATT Protocol (Binary)                      │
│  Opcodes: 0x02 (MTU Req), 0x0A (Read),     │
│           0x12 (Write), 0x1B (Notify)       │
│           0x10 (Read By Group - Services)   │
│           0x08 (Read By Type - Chars)       │
│           0x04 (Find Info - Descriptors)    │
│  Handle-based addressing                    │
└─────────────────┬───────────────────────────┘
                  │ wrapped in L2CAP
                  ↓
┌─────────────────────────────────────────────┐
│  L2CAP (Binary Transport)                   │
│  Channel 0x0004 (ATT)                       │
│  Channel 0x0005 (LE Signaling)              │
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
                           ├── gatt_operations.jsonl
                           └── advertising.json
```

---

## 🔍 Example Packet Flows

### MTU Negotiation
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

### Service Discovery
```
Device A (Central)                    Device B (Peripheral)
────────────────                      ──────────────────────
DiscoverServices() →
  ↓
  Read By Group Type Request (0x10)
  L2CAP: [07 00][04 00][10 01 00 FF FF 00 28]
         len=7  ATT    op  start end   Primary Service UUID
  ───────────────────────────────→
                                     Query attributeDB
                                     ← Read By Group Type Response (0x11)
                                       L2CAP: [... 06 ...]
                                              Length=6 per service
                                              [start][end][UUID]...
  ←───────────────────────────────
Parse services, store in discoveryCache
```

---

## 🔧 Implementation Details

### Wire Struct Fields
```go
type Wire struct {
    hardwareUUID    string
    socketPath      string
    connections     map[string]*Connection
    gattHandler     func(peerUUID string, msg *GATTMessage)
    attributeDB     *gatt.AttributeDatabase  // Server-side GATT database
    debugLogger     *debug.DebugLogger
    // ... other fields
}
```

### Connection Struct Fields
```go
type Connection struct {
    conn            net.Conn
    remoteUUID      string
    role            ConnectionRole
    mtu             int
    fragmenter      *att.Fragmenter
    requestTracker  *att.RequestTracker
    params          *l2cap.ConnectionParameters
    discoveryCache  *gatt.DiscoveryCache  // Client-side discovery cache
    // ... other fields
}
```

### ATT Opcodes Reference
```
Service Discovery:
- 0x10: Read By Group Type Request
- 0x11: Read By Group Type Response

Characteristic Discovery:
- 0x08: Read By Type Request
- 0x09: Read By Type Response

Descriptor Discovery:
- 0x04: Find Information Request
- 0x05: Find Information Response

Read/Write:
- 0x0A: Read Request
- 0x0B: Read Response
- 0x12: Write Request
- 0x13: Write Response
- 0x52: Write Command (no response)

MTU Exchange:
- 0x02: MTU Request
- 0x03: MTU Response

Notifications:
- 0x1B: Handle Value Notification
- 0x1D: Handle Value Indication
- 0x1E: Handle Value Confirmation

Fragmentation:
- 0x16: Prepare Write Request
- 0x17: Prepare Write Response
- 0x18: Execute Write Request
- 0x19: Execute Write Response

Error:
- 0x01: Error Response
```

### ATT Error Codes
```
0x01: Invalid Handle
0x02: Read Not Permitted
0x03: Write Not Permitted
0x04: Invalid PDU
0x05: Insufficient Authentication
0x06: Request Not Supported
0x07: Invalid Offset
0x08: Insufficient Authorization
0x09: Prepare Queue Full
0x0A: Attribute Not Found
0x0B: Attribute Not Long
0x0C: Insufficient Encryption Key Size
0x0D: Invalid Attribute Value Length
0x0E: Unlikely Error
0x0F: Insufficient Encryption
0x10: Unsupported Group Type
0x11: Insufficient Resources
```

---

## 📁 File Structure

### Protocol Layers
```
wire/
├── l2cap/
│   ├── packet.go (158 lines)
│   │   - Encode(), Decode()
│   │   - Fragmentation support
│   │   - Channel IDs: 0x0004 (ATT), 0x0005 (Signaling)
│   ├── packet_test.go (6 tests)
│   ├── connection_params.go (207 lines)
│   │   - ConnectionParameters type
│   │   - Request/Response encoding
│   │   - Validation
│   └── connection_params_test.go (11 tests)
│
├── att/
│   ├── opcodes.go (178 lines)
│   │   - All ATT opcodes
│   │   - IsRequest(), IsResponse(), GetResponseOpcode()
│   ├── errors.go (119 lines)
│   │   - ATT error codes
│   │   - Error type
│   ├── packet.go (451 lines)
│   │   - EncodePacket(), DecodePacket()
│   │   - All ATT packet types
│   ├── packet_test.go (11 tests)
│   ├── fragmenter.go (152 lines)
│   │   - Fragmenter type
│   │   - ShouldFragment(), FragmentWrite()
│   │   - AddPrepareWriteResponse(), Reassemble()
│   ├── fragmenter_test.go (7 tests)
│   ├── request_tracker.go (215 lines)
│   │   - RequestTracker type
│   │   - SendRequest(), CompleteRequest()
│   │   - Timeout handling
│   └── request_tracker_test.go (9 tests)
│
├── gatt/
│   ├── handles.go (155 lines)
│   │   - AttributeDatabase type
│   │   - AddAttribute(), GetAttribute()
│   │   - FindAttributesByType()
│   ├── handles_test.go (9 tests)
│   ├── service_builder.go (181 lines)
│   │   - BuildAttributeDatabase()
│   │   - Service, Characteristic, Descriptor types
│   │   - Helper functions for common services
│   ├── service_builder_test.go (7 tests)
│   ├── discovery.go (490 lines)
│   │   - ParseReadByGroupTypeResponse()
│   │   - ParseReadByTypeResponse()
│   │   - ParseFindInformationResponse()
│   │   - BuildReadByGroupTypeResponse()
│   │   - BuildReadByTypeResponse()
│   │   - BuildFindInformationResponse()
│   │   - DiscoverServicesFromDatabase()
│   │   - DiscoverCharacteristicsFromDatabase()
│   │   - DiscoverDescriptorsFromDatabase()
│   │   - DiscoveryCache type
│   └── discovery_test.go (9 tests)
│
├── advertising/
│   ├── packet.go (367 lines)
│   │   - AdvertisingPDU type
│   │   - Encode(), Decode()
│   │   - TLV AD structure encoding
│   │   - Helper functions for common AD types
│   └── packet_test.go (25 tests)
│
└── debug/
    └── logger.go (250 lines)
        - DebugLogger type
        - LogL2CAPPacket(), LogATTPacket()
        - LogGATTOperation()
        - Human-readable formatters
```

### Wire Core
```
wire/
├── wire.go (main implementation)
│   - NewWire(), Start(), Stop()
│   - Connect(), Disconnect()
│   - SendGATTMessage()
│   - readMessages(), handleATTPacket()
│   - Discovery request/response handlers
│   - MTU negotiation
│   - Fragmentation
│   - Request tracking
│
├── types.go
│   - Wire struct
│   - Connection struct
│   - GATTMessage
│   - AdvertisingData
│
├── constants.go
│   - DefaultMTU, MaxMTU
│   - Connection roles
│
├── discovery.go
│   - ReadAdvertisingData()
│   - WriteAdvertisingData()
│   - Binary advertising support
│
├── audit.go
│   - ConnectionEventLogger
│   - SocketHealthMonitor
│
└── tests
    ├── wire_test.go (4 tests)
    ├── mtu_enforcement_test.go (3 tests)
    └── connection_params_test.go (5 tests)
```

---

## 🔬 Key Algorithms

### MTU Negotiation
1. Central sends MTU Request (0x02) with ClientRxMTU
2. Peripheral responds with MTU Response (0x03) with ServerRxMTU
3. Negotiated MTU = min(ClientRxMTU, ServerRxMTU, MaxMTU)
4. Both devices store negotiated MTU in connection.mtu
5. All subsequent writes enforce MTU limit

### Fragmentation (Prepare Write + Execute Write)
1. Check if write value > MTU-3
2. If yes, use Prepare Write:
   - Split value into chunks of MTU-3 bytes
   - Send Prepare Write Request (0x16) for each chunk with offset
   - Wait for Prepare Write Response (0x17) echoing back
3. Send Execute Write Request (0x18) with flags=0x01 to commit
4. Wait for Execute Write Response (0x19)

### Service Discovery
1. Client sends Read By Group Type Request (0x10)
   - StartHandle = 0x0001
   - EndHandle = 0xFFFF
   - Type = 0x2800 (Primary Service UUID)
2. Server queries attributeDB for primary service declarations
3. Server responds with Read By Group Type Response (0x11)
   - Each service: [StartHandle][EndHandle][UUID]
4. Client parses and stores in discoveryCache

### Characteristic Discovery
1. Client sends Read By Type Request (0x08)
   - StartHandle = service.StartHandle
   - EndHandle = service.EndHandle
   - Type = 0x2803 (Characteristic UUID)
2. Server queries attributeDB for characteristic declarations
3. Server responds with Read By Type Response (0x09)
   - Each char: [DeclHandle][Properties][ValueHandle][UUID]
4. Client parses and stores in discoveryCache

---

## 🧪 Testing Strategy

### Unit Tests
- Each protocol layer tested independently
- Binary encoding/decoding round-trip tests
- Error handling and validation tests

### Integration Tests
- Full MTU negotiation flow
- Connection parameter updates
- Fragmented writes end-to-end
- Discovery protocol flows

### Test Coverage
- 106 tests across 6 packages
- All protocol layers covered
- Edge cases and error conditions tested

---

## 🐛 Debug Files

### Location
```
~/.apb/{session_id}/{device_uuid}/debug/
├── l2cap_packets.jsonl
├── att_packets.jsonl
├── gatt_operations.jsonl
└── advertising.json
```

### Format
```json
// l2cap_packets.jsonl
{"timestamp":"2025-10-29T10:15:30.123Z","direction":"tx","peer_uuid":"device-b","channel":"0x0004","length":3,"raw_hex":"020002"}

// att_packets.jsonl
{"timestamp":"2025-10-29T10:15:30.123Z","direction":"tx","peer_uuid":"device-b","opcode":"0x02","opcode_name":"Exchange MTU Request","data":{"client_rx_mtu":512},"raw_hex":"020002"}

// gatt_operations.jsonl
{"timestamp":"2025-10-29T10:15:30.123Z","operation":"read","peer_uuid":"device-b","service_uuid":"1800","char_uuid":"2A00","status":"success"}
```

### Usage
- Enabled by default (`WIRE_DEBUG=0` to disable)
- JSONL format for easy grepping
- Never read in production code
- Useful for debugging protocol issues

---

## 📚 References

### BLE Specification
- Bluetooth Core Spec v5.3
- Volume 3, Part F: ATT Protocol
- Volume 3, Part G: GATT Profile
- Volume 6, Part B: Link Layer

### Implementation Notes
- All binary data is little-endian
- Handles are 16-bit (0x0001 - 0xFFFF, 0x0000 reserved)
- MTU default: 23 bytes, max: 512 bytes (configurable)
- Connection parameters: interval, latency, supervision timeout
