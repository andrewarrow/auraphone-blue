# Wire Package - Next Steps

**Last Updated**: 2025-10-29

## 🎯 Immediate Next Steps

### 1. Complete GATT Discovery (Client-Side API)
**Priority**: HIGH
**Estimated Effort**: Medium

- [x] Add public `DiscoverServices(peerUUID string)` method to Wire
- [x] Add public `DiscoverCharacteristics(peerUUID string, serviceUUID []byte)` method
- [x] Add public `DiscoverDescriptors(peerUUID string, charHandle uint16)` method
- [x] Replace temporary hash-based UUID-to-handle mapping with discovery cache lookups
- [x] Add realistic delays (100-500ms per discovery operation)
- [x] Create integration test for full discovery flow

**Why**: Completes the discovery implementation, enables real usage of discovery protocol

**Status**: ✅ COMPLETED

---

### 2. Multiple Simultaneous Connections
**Priority**: HIGH
**Estimated Effort**: Medium

- [x] Test multiple connections to the same device
- [x] Verify per-connection state isolation (MTU, discovery cache, request tracker)
- [x] Add tests for concurrent discovery on multiple connections
- [x] Test connection limits (iOS typically 7-10, Android varies)

**Why**: Critical for realistic BLE scenarios, many apps connect to multiple peripherals

**Status**: ✅ COMPLETED

---

### 3. CCCD Writes (Subscriptions)
**Priority**: MEDIUM
**Estimated Effort**: Small

- [x] Implement CCCD write handling (0x2902 descriptor)
- [x] Enable/disable notifications via CCCD writes
- [x] Track subscription state per connection
- [x] Add tests for subscribe/unsubscribe flow

**Why**: Required for notifications/indications to work properly

**Status**: ✅ COMPLETED

---

## 🔮 Future Work

### Physical Layer Simulation
- Add realistic latency based on connection parameters
- Simulate radio propagation delays
- Implement connection interval timing

### Link Layer Control PDUs
- Connection update requests (LL_CONNECTION_UPDATE_IND)
- Channel map updates
- Feature exchange

### Advanced Features
- Bonding/pairing simulation
- Security Manager Protocol (SMP)
- L2CAP Credit-Based Channels
- Multiple advertising sets

---

## 📝 Known Limitations to Address

1. ~~**UUID-to-handle mapping**: Still uses hash-based temporary approach~~ ✅ FIXED - Now uses discovery cache
2. **Execute Write**: Delivers reassembled data to GATT handler ✅
3. ~~**No CCCD support**: Subscriptions not yet implemented~~ ✅ FIXED - CCCD subscriptions implemented

---

## 🚀 Success Criteria

Before moving to kotlin/ and swift/ integration:

- ✅ Binary L2CAP + ATT communication working
- ✅ MTU negotiation and enforcement
- ✅ GATT discovery protocol (server-side)
- ✅ GATT discovery protocol (client-side API)
- ✅ Multiple simultaneous connections
- ✅ CCCD writes for subscriptions
- ✅ All tests passing with realistic scenarios (117 tests total)
