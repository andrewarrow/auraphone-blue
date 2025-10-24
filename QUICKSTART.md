# Quick Start Guide

## 1. Run Basic Demo

See iOS and Android devices communicate:

```bash
go run main.go
```

**Output:**
```
[iOS] Discovered Android device: Samsung Galaxy Test
[iOS]   - Service UUIDs: [E621E1F8-C36C-495A-93FC-0C247A3E6E5F]
[iOS] Connected to Android device
[Android] 📤 SENT: "hi from Android"
[iOS] 📩 RECEIVED: "hi from Android"
```

## 2. Run a Test Scenario

Try the photo collision scenario:

```bash
go run cmd/replay/main.go --scenario scenarios/photo_collision.json
```

## 3. Convert Your Logs to a Scenario

When you hit a bug:

```bash
# 1. Capture logs from devices
adb logcat -s BluetoothProxaurati > android.log
# (copy iOS logs from Xcode)

# 2. Convert to scenario
go run cmd/log2scenario/main.go \
  --ios ios.log \
  --android android.log \
  --output scenarios/my_bug.json

# 3. Refine the JSON
vim scenarios/my_bug.json

# 4. Test it
go run cmd/replay/main.go --scenario scenarios/my_bug.json
```

## Available Scenarios

All scenarios in `scenarios/` directory:

1. **photo_collision.json** - Two devices send photos simultaneously
2. **stale_handshake.json** - Handshake becomes >60s old
3. **multi_device_room.json** - 5 devices in a conference room

## File Structure

```
auraphone-blue/
├── main.go                          # Basic demo
├── cmd/
│   ├── replay/main.go              # Run scenarios
│   └── log2scenario/main.go        # Convert logs
├── scenarios/                       # Test scenarios (JSON)
├── testdata/                        # Sample logs
├── tests/                           # Test framework
├── wire/                            # Communication layer
├── swift/                           # iOS simulation
└── kotlin/                          # Android simulation
```

## Log Patterns to Look For

When capturing logs from your iOS/Android apps:

### iOS
- `📡 [SCAN] Discovered device:`
- `⚔️ [PHOTO-COLLISION] Win/Loss`
- `🔄 [STALE-HANDSHAKE]`
- `🔄 [COLLISION-RETRY]`
- `✅ [PHOTO] Completed`

### Android
- `📡 [SCAN] New device discovered:`
- `waiting for them to connect (we're peripheral)`
- `⚔️ [PHOTO-COLLISION]`
- `[STALE-HANDSHAKE]`
- `✅ [PROFILE-PHOTO] Completed`

## Next Steps

- Read [TESTING.md](TESTING.md) for edge case details
- Read [WORKFLOW.md](WORKFLOW.md) for complete workflow
- Read [CLAUDE.md](CLAUDE.md) for implementation details

## Common Commands

```bash
# Run demo
go run main.go

# Run scenario
go run cmd/replay/main.go --scenario scenarios/photo_collision.json

# Convert logs
go run cmd/log2scenario/main.go --ios ios.log --android android.log --output my_scenario.json

# List scenarios
ls scenarios/*.json

# Check what devices were created
ls -la data/
```

## Troubleshooting

**Problem:** `go run` fails with import errors

**Solution:** Run `go mod tidy` first

**Problem:** Scenario has validation errors

**Solution:** Check that all referenced device IDs exist and match

**Problem:** Want more verbose output

**Solution:** Add logging to runner.go or check data/ directory for generated files
