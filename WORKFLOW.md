# Testing Workflow Guide

## From Real-World Bug to Automated Test

This guide shows how to capture edge cases from the real iOS/Android apps and turn them into automated, replayable tests.

## Step 1: Capture Logs from Real Devices

When you encounter a bug or edge case with real devices:

### iOS (Xcode Console)
```bash
# Option 1: Copy from Xcode Console during testing
# Look for logs with prefixes: [SCAN], [HANDSHAKE], [PHOTO-COLLISION], etc.

# Option 2: Export device logs
xcrun simctl spawn booted log stream --predicate 'subsystem == "dev.andrewarrow.hotmicevents"' > ios.log
```

### Android (Logcat)
```bash
# During testing, capture logs with adb
adb logcat -s BluetoothProxaurati BluetoothPhoto > android.log

# Or filter for specific tags
adb logcat -s BluetoothProxaurati:D BluetoothPhoto:D *:S > android.log
```

**Example captured logs:**

`ios.log`:
```
2025-10-24 10:30:15.123 📡 [SCAN] Discovered device: Pixel 8 Pro
2025-10-24 10:30:16.100 ⚔️ [PHOTO-COLLISION] Loss (iPhone 15 Pro <= Pixel 8 Pro)
2025-10-24 10:30:16.200 📸 [COLLISION-ABORT] Stored aborted send
2025-10-24 10:30:18.600 🔄 [COLLISION-RETRY] Retrying send to peripheral
```

`android.log`:
```
D/BluetoothProxaurati: 📡 [SCAN] New device discovered: iPhone 15 Pro
D/BluetoothPhoto: ⚔️ [PHOTO-COLLISION] Win (Pixel 8 Pro > iPhone 15 Pro)
D/BluetoothProxaurati: ✅ [PROFILE-PHOTO] Completed sending photo
```

## Step 2: Convert Logs to Test Scenario

Use the log parser to extract events and assertions:

```bash
go run cmd/log2scenario/main.go \
  --ios ios.log \
  --android android.log \
  --output scenarios/my_bug_2024_10_24.json
```

This generates a JSON scenario with:
- **Devices**: Auto-detected from log messages
- **Timeline**: Events extracted with relative timestamps
- **Assertions**: Expected outcomes detected from log patterns

## Step 3: Refine the Scenario

The auto-generated scenario needs human refinement:

```bash
vim scenarios/my_bug_2024_10_24.json
```

**Refine:**
1. Add descriptive name and description
2. Fix device platforms (change "unknown" to "ios" or "android")
3. Add profile data (first name, photo hash)
4. Add missing events that weren't logged
5. Add advertising configuration
6. Verify assertion types and add comments

**Before:**
```json
{
  "name": "Scenario from logs",
  "devices": [
    {
      "id": "pixel-8-pro",
      "platform": "unknown",
      "device_name": "Pixel 8 Pro"
    }
  ]
}
```

**After:**
```json
{
  "name": "Photo Collision in Conference Room",
  "description": "Reproduced bug from 2024-10-24 where collision handling failed",
  "devices": [
    {
      "id": "pixel-8-pro",
      "platform": "android",
      "device_name": "Pixel 8 Pro",
      "profile": {
        "first_name": "Bob",
        "photo_hash": "xyz789"
      },
      "advertising": {
        "service_uuids": ["E621E1F8-C36C-495A-93FC-0C247A3E6E5F"],
        "interval_ms": 100,
        "tx_power": 0
      }
    }
  ]
}
```

## Step 4: Validate the Scenario

```bash
# Check if scenario is valid
go run cmd/replay/main.go --scenario scenarios/my_bug_2024_10_24.json --validate

# Or just run it (validation happens automatically)
go run cmd/replay/main.go --scenario scenarios/my_bug_2024_10_24.json
```

**Output:**
```
=== Running Scenario: Photo Collision in Conference Room ===
Description: Reproduced bug from 2024-10-24...
Devices: 2
Events: 12
Duration: 3.5s

Setting up devices...
✓ Devices initialized

Executing timeline...
[0ms] [android-device] start_advertising: Android starts first
[100ms] [ios-device] start_scanning: iOS scans for devices
[250ms] [ios-device] discover: Discovered Pixel 8 Pro
...

--- Assertion Results ---
✅ PASS - collision_detected: Both devices detected collision
✅ PASS - collision_winner: Android won tie-breaker
✅ PASS - photo_transfer_completed: iOS received photo
✅ PASS - photo_transfer_completed: Android received photo after retry

Total: 4/4 assertions passed
```

## Step 5: Add to Test Suite

Once validated, add the scenario to your test suite:

```bash
# Move to scenarios directory if not already there
mv scenarios/my_bug_2024_10_24.json scenarios/

# Run all scenarios
go test ./tests -v

# Or run specific scenario in CI/CD
./scripts/test_scenario.sh scenarios/my_bug_2024_10_24.json
```

## Step 6: Document the Bug Fix

Add comments to the scenario describing:
- When the bug was discovered
- What caused it
- How it was fixed
- Related PR/commit

```json
{
  "name": "Photo Collision in Conference Room",
  "description": "Bug discovered 2024-10-24 during team meeting with 5 devices. iOS collision retry logic failed when Android won tie-breaker. Fixed in PR #123.",
  ...
}
```

## Real Example: Stale Handshake Bug

### Discovery (2024-10-20)
User reported: "Profile pictures not updating when I come back to the office"

### Investigation
1. Captured logs during reproduction
2. Found pattern: Handshake >60s old not triggering reconnection
3. Converted logs to scenario

### Log Extraction
```bash
# iOS showed:
📡 [SCAN] Discovered device: Work Phone (rssi: -65)
# But no reconnection logged

# Android showed:
🔄 [STALE-HANDSHAKE] Handshake is stale for Work Phone (last: 85.2s ago), re-connecting
```

### Generated Scenario
```bash
go run cmd/log2scenario/main.go \
  --ios stale_handshake_ios.log \
  --android stale_handshake_android.log \
  --output scenarios/stale_handshake_bug.json
```

### Result
- Identified iOS wasn't checking handshake staleness
- Added check in `didDiscoverPeripheral` callback
- Scenario now tests both iOS and Android handle stale handshakes
- Bug never happened again

## Tips for Good Scenarios

### 1. Use Descriptive Names
❌ Bad: `test1.json`, `collision.json`
✅ Good: `photo_collision_5_devices_2024_10_24.json`

### 2. Document Context
```json
{
  "comment": "Simulates 5 people in conference room all trying to share photos. Bug: iOS didn't wait for subscription before sending."
}
```

### 3. Add Event Comments
```json
{
  "time_ms": 600,
  "action": "send_photo_metadata",
  "device": "ios-1",
  "comment": "COLLISION STARTS HERE - both devices send simultaneously"
}
```

### 4. Include Timing
Use realistic timings from real logs:
- Discovery: ~100-200ms after scan starts
- Connection: ~50-150ms after discovery
- Handshake: ~100ms after connection
- Service discovery: ~50-100ms after connection

### 5. Test Negative Cases
Don't just test happy paths:
```json
{
  "type": "photo_transfer_failed",
  "device": "ios-1",
  "comment": "Should fail due to CRC mismatch"
}
```

## Continuous Testing

### Pre-Commit Hook
```bash
#!/bin/bash
# .git/hooks/pre-commit

echo "Running BLE scenario tests..."
go run cmd/replay/main.go --scenario scenarios/*.json

if [ $? -ne 0 ]; then
  echo "❌ BLE scenarios failed"
  exit 1
fi
```

### CI/CD Integration
```yaml
# .github/workflows/test.yml
- name: Run BLE Scenarios
  run: |
    for scenario in scenarios/*.json; do
      go run cmd/replay/main.go --scenario "$scenario" || exit 1
    done
```

## Debugging Failed Scenarios

If a scenario fails:

1. **Check event log** - See which event failed
2. **Add verbose logging** - Set debug flags
3. **Inspect data directory** - Look at generated files
4. **Compare with real logs** - Does simulation match reality?

```bash
# Run with verbose output
DEBUG=1 go run cmd/replay/main.go --scenario scenarios/my_scenario.json

# Inspect generated device files
ls -la data/test-device-*/
cat data/test-device-*/advertising.json
cat data/test-device-*/inbox/*.json
```

## Summary

**The Loop:**
1. 📱 Bug happens on real devices
2. 📝 Capture logs
3. 🔄 Convert to scenario
4. ✏️ Refine and document
5. ✅ Validate with replay
6. 📦 Add to test suite
7. 🚀 Never happens again!

**Benefits:**
- Bug reproduced deterministically
- No hardware needed for regression testing
- Documents exact conditions that caused bug
- Fast iteration on fixes
- Prevents regressions
