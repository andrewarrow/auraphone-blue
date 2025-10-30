# Phone Package Testing

## Overview

The `phone/` package contains the **core mesh networking and gossip protocol logic** that is shared by both iOS and Android implementations. This document explains what the unit tests cover and why they're critical.

## Why Unit Tests for phone/ Package?

The integration tests in `tests/` revealed that bugs in the mesh/gossip logic can be **hard to diagnose** when testing the full BLE stack. Unit tests in `phone/` allow us to:

1. **Test mesh logic in isolation** (no BLE, no timing issues, runs in milliseconds)
2. **Catch bugs before they reach integration tests** (faster feedback loop)
3. **Test edge cases that are hard to reproduce** in full BLE scenarios
4. **Ensure the gossip protocol is rock solid** before building more complex multi-device tests

## Test Coverage

### Device State Management (4 tests)
- ✅ Adding new devices to mesh
- ✅ Updating profile versions
- ✅ Name changes with version bumps
- ✅ Photo hash updates (resets PhotoRequestSent flag)

### Gossip Message Building (3 tests)
- ✅ Building gossip with empty mesh (only ourselves)
- ✅ Building gossip with known devices (includes all)
- ✅ Gossip round incrementing

### Gossip Merging (4 tests)
- ✅ Learning about new devices via gossip
- ✅ Ignoring gossip about ourselves
- ✅ Timestamp-based conflict resolution (newer wins)
- ✅ Ignoring older timestamps (preserves newer data)

### Profile Version Detection (4 tests)
- ✅ Detecting outdated profiles (no cache)
- ✅ Detecting outdated profiles (cache exists but older)
- ✅ Recognizing up-to-date profiles (same version)
- ✅ Ignoring our own profile in outdated checks

### Connection State (3 tests)
- ✅ Marking devices connected/disconnected
- ✅ Resetting PhotoRequestSent on disconnect
- ✅ Getting list of only connected devices

### Multi-hop Routing (3 tests)
- ✅ Recording neighbor data (what they know)
- ✅ Finding neighbors with specific profile versions
- ✅ Only returning connected neighbors

### Persistence (2 tests)
- ✅ Saving and loading mesh state from disk
- ✅ Correct file location (cache/mesh_view.json)

### Edge Cases (5 tests)
- ✅ Handling profile version 0 (no profile set)
- ✅ Empty photo hash handling
- ✅ Empty photo hash preserves existing (doesn't overwrite)
- ✅ Multiple rapid updates (simulates gossip storm)
- ✅ Gossip interval timing (5 second cooldown)

## What the Tests Don't Cover (Yet)

1. **Concurrent access** - MeshView has mutexes but no stress tests yet
2. **Large mesh scaling** - What happens with 50+ devices?
3. **Photo request logic** - GetMissingPhotos() isn't tested yet
4. **Gossip audit logging** - File writes aren't validated

## Bug Found During Test Development

The integration test (`three_phones_first_names_test.go`) revealed a critical bug:

**Bug:** `iphone/profile.go:29` refuses to send ProfileMessage if `last_name` is empty
```go
if profile["last_name"] == "" {
    logger.Debug(..., "Skipping profile send to %s (profile not set)")
    return
}
```

**Impact:** When updating only `first_name`, multi-hop profile requests fail because the profile isn't sent.

**Fix needed:** Either:
1. Remove the `last_name` check (send profiles even if incomplete)
2. Change the check to only skip if ALL fields are empty
3. Add a "has_profile" flag separate from `last_name`

## Running the Tests

```bash
# Run all phone package tests
cd phone/
go test -v

# Run specific test category
go test -v -run TestUpdateDevice
go test -v -run TestGossip
go test -v -run TestPersistence

# Run with coverage
go test -cover
```

## Test Execution Time

All 27 tests complete in ~5.3 seconds:
- 26 tests run instantly (<100ms total)
- 1 test sleeps for 5 seconds to test gossip timing

## Next Steps

Before adding more integration tests in `tests/`:

1. ✅ **DONE:** Create `phone/mesh_view_test.go` with 27 unit tests
2. ⬜ **TODO:** Fix the `last_name` profile bug in `iphone/profile.go` and `android/profile.go`
3. ⬜ **TODO:** Add tests for `GetMissingPhotos()` (photo request logic)
4. ⬜ **TODO:** Add concurrent access stress tests (multiple goroutines)
5. ⬜ **TODO:** Test large mesh scenarios (50+ devices)

## Recommended Testing Strategy

For new features:

1. **Write unit tests first** in `phone/` for the core logic
2. **Write 2-device integration test** in `tests/` to verify BLE behavior
3. **Write 3+ device tests** only after core logic is proven solid

This ensures bugs are caught at the unit level (fast) rather than integration level (slow).
