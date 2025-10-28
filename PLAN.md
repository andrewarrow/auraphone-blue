1. No Connection Interval Simulation - The Biggest Surprise 🚨

  Current behavior:
  Your photo transfer code sends chunks as fast as possible:
  for i, chunk := range chunks {
      wire.NotifyCharacteristic(peerUUID, serviceUUID, charUUID, chunk)
      // These all fire instantly via Unix sockets
  }

  Real BLE behavior:
  - BLE connections have a connection interval (typically 30-50ms on iOS, configurable 7.5ms-4s)
  - You can only send data during connection events - one event per interval
  - Each event has a small window (typically 2-5ms) to exchange packets
  - During that window, you can send ~3-5 packets (depending on data length and radio timing)
  - If you miss the window, you wait for the next interval

  What will shock you:
  - Sending 100 photo chunks that takes ~100ms in simulator will take 3-5 seconds on real hardware
  - UpdateValue() (iOS) / NotifyCharacteristicChanged() (Android) will start returning false after
  the first 3-5 chunks when the TX buffer fills up
  - You'll need to implement flow control - wait for peripheralManagerIsReady(toUpdateSubscribers:)
  callback on iOS or retry on Android
  - With multiple connected devices, the radio is time-sliced between connections - 5 connections
  means each gets ~20% of bandwidth

  Missing from simulator:
  - wire/wire.go uses instant socket sends with no connection interval delays
  - No TX buffer depth limits (real iOS/Android buffer 1-5 packets max)
  - No peripheralManagerIsReady callback when buffer drains

  ---
  2. Service Discovery Caching Will Cause Mysterious Bugs 🐛

  Current behavior:
  // swift/cb_peripheral.go:74
  gattTable, err := p.wire.ReadGATTTable(p.remoteUUID)
  // Instant read from global registry, takes ~100ms with artificial delay

  Real BLE behavior:
  - First service discovery takes 500ms - 2 seconds (multiple ATT protocol round trips)
  - iOS aggressively caches the GATT table - sometimes across app restarts
  - Changing characteristics during development? iOS won't see the changes until:
    - Device is unpaired/forgotten
    - iOS Bluetooth cache is cleared (Settings → General → Reset → Reset Network Settings)
    - Device sends a "Service Changed" indication (0x2A05 characteristic)
  - Android has similar caching plus infamous GATT 133 errors when cache corrupts

  What will shock you:
  - "Why is my iPhone not seeing the updated photo characteristic?" → Cache
  - "It works on the first connection but not after reconnecting" → Stale cache
  - "One phone works, the other doesn't" → One has cached old GATT table

  Missing from simulator:
  - No service discovery round-trip simulation (ATT Read By Group Type, Read By Type, Find
  Information)
  - No persistent GATT cache across device restarts
  - No Service Changed characteristic (0x2A05) to invalidate cache

  ---
  3. Perfectly Reliable Message Delivery vs. Reality 📉

  Current behavior:
  - Unix domain sockets provide TCP-like reliability - messages arrive in order, no loss (except the
   simulated 0.1%)
  - Length-prefixed framing ensures you never get partial messages
  - Notifications/writes succeed unless the simulated 0.1% packet loss triggers

  Real BLE behavior:
  - Notifications are fire-and-forget at the radio level - no ACK, no guaranteed delivery
  - If receiver's connection event is skipped (radio busy, interference), notification is silently 
  dropped
  - Write without response truly means "I sent it, good luck" - no confirmation
  - No flow control for notifications - if you send faster than receiver can process, packets are
  dropped
  - Connection events can be skipped during:
    - High radio interference (2.4GHz is crowded - WiFi, microwaves, other BLE devices)
    - Device is servicing another BLE connection
    - OS is doing background tasks (scan, connection to another device)

  What will shock you:
  - Photo chunks will go missing with no error reported
  - You'll need application-level ACKs - can't rely on notifications arriving
  - Current code assumes if NotifyCharacteristic() doesn't error, the chunk was delivered - WRONG on
   real BLE
  - Notification order isn't guaranteed across characteristics (though usually maintained within one
   characteristic)

  Missing from simulator:
  - Notification delivery confirmation (it's truly fire-and-forget, no way to know if it arrived)
  - Connection event skipping due to radio contention
  - Realistic buffer overflow behavior (simulator has unlimited Go channel buffering)

  ---
  Bonus: Radio Contention With Multiple Connections 📻

  Your mesh design connects to multiple peers simultaneously. Real BLE has ONE radio that
  time-slices between connections:

  - 1 connection: ~90% bandwidth available for your app
  - 3 connections: ~30% bandwidth each
  - 5 connections: ~18% bandwidth each (plus overhead)

  Photo transfer throughput will drop dramatically as you scale up the mesh. The simulator has
  independent Unix sockets with no shared resource contention.

  ---
  Recommendations

  1. Add connection interval simulation to wire/wire.go - rate-limit sends to ~20 packets/second per
   connection
  2. Implement flow control in photo transfer - wait for ACKs before sending next chunk batch
  3. Add sequence numbers to PhotoChunkMessage and implement retransmission
  4. Test with iOS Developer mode "Connection Interval" set to 100ms+ to see realistic timing
