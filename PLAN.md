# Plan: Fix 50% Photo Transfer Failure Rate

## Problem Analysis (Test Run 2025-10-27_10-14-10)

**Result**: 10/20 transfers succeeded (50% success rate)

**Previous run (2025-10-27_09-49-03)**: Also 10/20 (50%) - **SAME ISSUE PERSISTS**

### Root Causes Identified

#### 1. **Chunks Sent But Never Received** (CRITICAL)
- **Evidence**: 187 `send_started` events but only 65 `receive_started` events
- **Symptoms**:
  - Sender logs: `send_started` + `chunk_sent` (all chunks)
  - Receiver logs: **NOTHING** (no `receive_started`, no `chunk_received`)
- **Impact**: ~65% of transfer attempts fail silently on receiver side

**Example failure:**
```
YMLA0QYO → ASG1KC19:
  09:48:07 - photo_request_received [YMLA0QYO side]
  09:48:07 - send_started [YMLA0QYO side]
  09:48:07 - chunk_sent (x4) [YMLA0QYO side]
  [NO EVENTS ON ASG1KC19 SIDE] ❌
```

**Possible causes:**
1. Photo chunks not reaching `HandlePhotoChunk()` on receiver
2. UUID → DeviceID mapping failures causing early returns
3. Wire layer dropping messages silently
4. Wrong characteristic/socket being used for chunk delivery

#### 2. **Duplicate Photo Requests** (MAJOR)
- **Evidence**: ASG1KC19 → TI05WNAZ shows 13+ `photo_request_received` events
- **Impact**: Senders restart transfers multiple times, potentially losing progress
- **Status**: MergeGossip fix IS implemented (prevents duplicate discoveries), but requests are still sent multiple times

**Why it's still happening:**
- Photo requests sent on every gossip interval (5 seconds)
- `PhotoRequestSent` flag not checked before sending requests
- Flag might be reset incorrectly somewhere

#### 3. **Uneven Device Participation** (MODERATE)
- **ASG1KC19** (F09FFFB7): Only 21 timeline events (vs 400-520 for others)
  - 1 send, 2 receive_started, 1 receive_complete
- **KXX18IL0** (E0D11F4F): Only 98 timeline events
  - 7 sends, 9 receive_started, 7 receive_complete

**Why**: These devices are not discovering/requesting photos properly

#### 4. **Connection Drops During Transfers** (MINOR)
- Only 6 socket errors total (very low impact)
- ASG1KC19 ↔ TI05WNAZ: Connection dropped at 09:48:07, reconnected 09:48:28
- May have interrupted 1-2 transfers

---

## Investigation Plan

### Phase 1: Diagnose Why Chunks Don't Reach Receiver (2-3 hours)

#### Step 1.1: Add Detailed Logging to Wire Layer
**File**: `wire/wire.go` (or iOS/Android write methods)

Add logging BEFORE and AFTER every chunk write:
```go
func (w *Wire) WriteCharacteristic(uuid, serviceUUID, charUUID string, data []byte) error {
    logger.Debug(w.hardwareUUID[:8], "📤 WIRE WRITE: target=%s char=%s bytes=%d",
        uuid[:8], charUUID[:8], len(data))

    err := w.actualWriteToSocket(uuid, data)

    if err != nil {
        logger.Error(w.hardwareUUID[:8], "❌ WIRE WRITE FAILED: target=%s error=%v",
            uuid[:8], err)
    } else {
        logger.Debug(w.hardwareUUID[:8], "✅ WIRE WRITE SUCCESS: target=%s", uuid[:8])
    }

    return err
}
```

#### Step 1.2: Add Logging to iOS/Android Receive Path
**Files**: `iphone/central_delegate.go`, `iphone/peripheral_delegate.go`, `android/gatt_callback.go`

Add logging IMMEDIATELY when data arrives:
```go
func (d *CentralDelegate) DidUpdateValueForCharacteristic(characteristic *swift.CBCharacteristic) {
    logger.Debug(d.iphone.hardwareUUID[:8],
        "📥 RECEIVED DATA: char=%s bytes=%d from_peripheral=%s",
        characteristic.UUID[:8], len(characteristic.Value), peripheral.UUID[:8])

    // Then decode and call HandlePhotoChunk...
}
```

#### Step 1.3: Run Test With Enhanced Logging
```bash
go run main.go --phones 5 --duration 60s --packet-loss 0.001

# Check logs for:
# 1. Do WIRE WRITE SUCCESS events exist for failed transfers?
# 2. Do RECEIVED DATA events exist on receiver side?
# 3. Where is the data being lost?
```

**Expected outcomes:**
- **If WIRE WRITE SUCCESS but no RECEIVED DATA**: Socket/connection issue
- **If RECEIVED DATA but no HandlePhotoChunk**: Decoding/routing issue
- **If HandlePhotoChunk called but no StartReceive**: UUID→DeviceID mapping failure

#### Step 1.4: Check UUID → DeviceID Mapping
**File**: `phone/photo_handler.go` line 130-165

The code already logs the entire UUID→DeviceID map on every chunk. Check logs for:
```
📋 Current UUID→DeviceID map has N entries:
   - UUID1 → DeviceID1
   - UUID2 → DeviceID2
```

**If map is empty or missing sender**: That's the bug! Receiver can't identify sender.

**Verify handshake completes:**
- Handshake should populate UUID→DeviceID map
- Check if handshake happens BEFORE photo chunks are sent
- May need to wait for handshake before sending photo requests

---

### Phase 2: Fix Duplicate Photo Requests (1 hour)

#### Issue: Photo requests sent repeatedly despite PhotoRequestSent=true

**Find where photo requests are sent:**
```bash
grep -r "SendPhotoRequest\|photo_request_received" iphone/ android/ phone/
```

**Expected fix location**: Likely in gossip processing or connection establishment handler

**Fix**: Add guard before sending request:
```go
// Before sending photo request
device := meshView.GetDevice(deviceID)
if device.PhotoRequestSent {
    logger.Debug("⏭️ Skipping photo request to %s (already sent)", deviceID)
    return
}

// Send request...
meshView.MarkPhotoRequested(deviceID)
```

---

### Phase 3: Fix Uneven Participation (1 hour)

#### Issue: ASG1KC19 and KXX18IL0 barely participating

**Check gossip neighbors:**
```bash
# From gossip_audit.jsonl, check which devices are gossiping to whom
grep "gossip_sent\|gossip_received" ~/.auraphone-blue-data/*/gossip_audit.jsonl
```

**Verify neighbor selection:**
- Should have ~3 neighbors each
- ASG1KC19 might not be in anyone's neighbor list (due to SHA256 hash sorting)
- If isolated, increase `maxNeighbors` or use different neighbor selection

**Fix**: Ensure all devices have at least 2 neighbors
```go
// In mesh_view.go SelectRandomNeighbors()
// Add minimum neighbor guarantee
if len(neighbors) < 2 && len(mv.devices) >= 2 {
    // Force inclusion of at least 2 neighbors even if hash is high
}
```

---

### Phase 4: Handle Connection Drops Gracefully (30 min)

#### Current behavior: Transfers lost when connection drops

**Fix**: Pause transfers on disconnect, resume on reconnect

**File**: `phone/photo_transfer_coordinator.go`

```go
// Add methods:
func (c *PhotoTransferCoordinator) PauseTransfersForDevice(deviceID string) {
    c.mu.Lock()
    defer c.mu.Unlock()

    if send, exists := c.inProgressSends[deviceID]; exists {
        send.Paused = true
        logger.Debug(c.hardwareUUID[:8], "⏸️ Paused send to %s", deviceID)
    }

    if recv, exists := c.inProgressReceives[deviceID]; exists {
        recv.Paused = true
        logger.Debug(c.hardwareUUID[:8], "⏸️ Paused receive from %s", deviceID)
    }
}

func (c *PhotoTransferCoordinator) ResumeTransfersForDevice(deviceID string) {
    // Resume and retry missing chunks
}
```

**Call from**: iOS/Android disconnect handlers

---

## Testing Plan

### Test 1: Enhanced Logging Test (diagnose)
```bash
go run main.go --phones 5 --duration 60s

# Check logs line-by-line to find where chunks are lost
# Focus on failed transfers from test report
```

### Test 2: After Fixes (validate)
```bash
# Run 5-phone test
go run main.go --phones 5 --duration 120s --packet-loss 0.001

# Expected: 20/20 transfers succeed (100%)
# Check test report
cat ~/.auraphone-blue-data/test_report_*.md
```

### Test 3: Stress Test (ensure reliability)
```bash
# Run with higher packet loss
go run main.go --phones 5 --duration 180s --packet-loss 0.05

# Should still achieve 90%+ success rate with retries
```

---

## Success Criteria

✅ **Primary Goal: 100% success rate** (20/20 transfers)
- All `send_started` events matched by `receive_started` events
- No silent failures

✅ **No duplicate photo requests**
- Each device→device pair: exactly 1 `photo_request_received` event
- Unless photo hash changes (legitimate re-request)

✅ **All devices participate equally**
- Each device should have 100-500 timeline events
- No device with <50 events

✅ **Connection drops don't cause failures**
- Transfers resume after reconnection
- Missing chunks detected and re-requested

---

## Implementation Order

### Day 1: Investigation (3-4 hours)
1. 🔄 Add enhanced logging to wire layer (30 min) - IN PROGRESS
2. ⏳ Add logging to iOS/Android receive paths (30 min)
3. ⏳ Run test with logging and analyze (2 hours)
4. ⏳ Identify exact failure point (1 hour)

### Day 2: Fixes (3-4 hours)
1. ✅ Fix root cause from investigation (varies)
2. ✅ Fix duplicate photo requests (1 hour)
3. ✅ Fix uneven participation (1 hour)
4. ✅ Add connection drop handling (30 min)
5. ✅ Run validation tests (1 hour)

**Total: 6-8 hours**

---

## Files to Modify

### Investigation Phase
- `wire/wire.go` - Add write logging
- `iphone/central_delegate.go` - Add receive logging
- `iphone/peripheral_delegate.go` - Add receive logging
- `android/gatt_callback.go` - Add receive logging

### Fix Phase (TBD based on investigation results)
Likely:
- `phone/photo_handler.go` - Fix UUID→DeviceID handling
- `iphone/iphone.go` or `android/android.go` - Fix photo request logic
- `phone/mesh_view.go` - Fix neighbor selection
- `phone/photo_transfer_coordinator.go` - Add pause/resume

---

## Key Insights

1. **The old PLAN.md was wrong** - It assumed duplicate discovery was the main issue, but actually **chunks not reaching receivers** is the critical bug

2. **Tests pass but integration fails** - Unit tests verify individual components work, but the end-to-end flow has a delivery problem

3. **50% failure rate is suspiciously high** - Suggests a systematic issue, not random packet loss. Likely:
   - Photo requests sent before handshake completes (no UUID→DeviceID mapping yet)
   - Or chunks sent to wrong socket/characteristic
   - Or receiver not subscribed to notifications properly

4. **Device F09FFFB7 (ASG1KC19) is the canary** - Only 21 events means it's barely working. If we fix its issues, we'll likely fix everything.

---

## Bug Found! (2025-10-27 Analysis)

### Root Cause
**Multiple devices are responding to photo requests intended for a single device.**

Example from logs:
- Device 0JSEUJO6 (B4698CEC) requests ZSJSMVL7's photo (hash 35a9a4ad)
- ZSJSMVL7 (12D90340) correctly responds with its 18777-byte photo ✅
- BUT: B4698CEC, F09FFFB7, and 88716210 ALSO respond with THEIR OWN photos ❌
- Result: ZSJSMVL7 receives wrong photos from wrong devices

### Evidence from Logs
```
[B4698CEC iOS DEBUG] 📤 Sent photo request for 35a9a4ad
[B4698CEC iOS INFO ] 📸 Sending our photo to ZSJSMVL7 (13964 bytes, 4 chunks)  ❌ WRONG!
[F09FFFB7 iOS INFO ] 📸 Sending our photo to ZSJSMVL7 (15311 bytes, 4 chunks)  ❌ WRONG!
[88716210 iOS INFO ] 📸 Sending our photo to ZSJSMVL7 (11138 bytes, 3 chunks)  ❌ WRONG!
```

Wire statistics:
- 327 photo chunks SENT
- 432 photo chunks RECEIVED (32% more!)
- Chunks ARE arriving, but wrong devices are sending

### Hypothesis
The `PhotoRequestMessage` is either:
1. Being broadcast to all devices instead of just the target
2. Has swapped requester/target fields
3. Handler logic incorrectly interprets who should respond

### Fix Applied
Added debug logging to `photo_handler.go:67-68` to log:
```go
logger.Debug(prefix, "📩 Photo request: from=%s requester=%s target=%s (we are %s)",
    senderUUID[:8], req.RequesterDeviceId[:8], req.TargetDeviceId[:8], device.GetDeviceID()[:8])
```

### Next Step
Run test again with new logging to see exact field values in PhotoRequestMessage.

---

## Previous Analysis (Obsolete)

**Immediate action**: ~~Add logging from Phase 1 and run a test~~. Logging already added in commits fad5094 and 94b97d6.
