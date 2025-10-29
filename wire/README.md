# Wire Package

Binary BLE protocol implementation for auraphone-blue simulator.

## 📋 Documentation

- **[PLAN_NEXT.md](PLAN_NEXT.md)** - Next steps and priorities
- **[PLAN_DONE.md](PLAN_DONE.md)** - Completed work
- **[PLAN_INFO.md](PLAN_INFO.md)** - Technical details and reference

## 🚀 Quick Start

```go
import "github.com/user/auraphone-blue/wire"

// Create a wire instance
w := wire.NewWire("device-uuid")

// Start listening
w.Start()

// Connect to another device
w.Connect("peer-uuid")

// Send GATT messages
w.SendGATTMessage("peer-uuid", &wire.GATTMessage{
    Type: "gatt_request",
    Operation: "read",
    ServiceUUID: "1800",
    CharacteristicUUID: "2A00",
})
```

## ✅ Current Status

**106/106 tests passing** - Full binary L2CAP + ATT + GATT protocol implementation

### Implemented Features
- ✅ Binary L2CAP + ATT communication
- ✅ MTU negotiation (512 bytes max)
- ✅ Request/response tracking with timeouts
- ✅ Automatic fragmentation for long writes
- ✅ Connection parameter updates
- ✅ GATT discovery protocol (server-side)
- ✅ Discovery cache (client-side)
- ✅ Binary advertising with TLV encoding

### Next Up
- ⏳ Client-side discovery API (DiscoverServices, etc.)
- ⏳ Multiple simultaneous connections
- ⏳ CCCD writes for subscriptions

## 🧪 Testing

```bash
# Run all tests
go test ./...

# Run specific package
go test ./gatt -v

# Run with race detector
go test -race ./...
```

## 📦 Package Structure

```
wire/
├── l2cap/          # L2CAP layer
├── att/            # ATT protocol
├── gatt/           # GATT services & discovery
├── advertising/    # Advertising packets
├── debug/          # Debug logging
└── *.go            # Wire core
```

## 🐛 Debug Mode

Debug logging is enabled by default. Logs are written to:
```
~/.apb/{session_id}/{device_uuid}/debug/
├── l2cap_packets.jsonl
├── att_packets.jsonl
├── gatt_operations.jsonl
└── advertising.json
```

To disable: `export WIRE_DEBUG=0`

## 📚 References

See [PLAN_INFO.md](PLAN_INFO.md) for:
- Binary protocol stack details
- Packet flow examples
- ATT opcodes reference
- File structure
- Testing strategy
