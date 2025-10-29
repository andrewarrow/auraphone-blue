
  2. ✅ FIXED: Error Response Opcode is Hardcoded (wire_gatt.go)

  Fixed in wire_gatt.go:89-117
  - Now maps msg.Operation to correct request opcode
  - read → OpReadRequest
  - write → OpWriteRequest
  - subscribe/unsubscribe → OpWriteRequest (CCCD writes)
  - Also resolves handle from UUID for error responses
  - Added comprehensive tests in error_response_test.go

  3. No Graceful Disconnect Protocol

  - Current: Socket just closes abruptly
  - Real BLE: Sends disconnect reason codes
  - Impact: kotlin/swift can't distinguish between crash vs. intentional disconnect
  - Mitigation: Add disconnect PDU or at least a shutdown handshake

  ⚠️ Important Considerations for kotlin/swift Integration


  6. Android GATT Queue Required

  Per your notes:
  "Android requires manual queueing — calling read or write too fast breaks things"

  Current wire/: All operations are async, no queueing enforcedRecommendation: kotlin/ wrapper must
  serialize GATT operations

  7. iOS CCCD Auto-Write Not Modeled

  - Real iOS: CoreBluetooth writes CCCD automatically when you call setNotifyValue()
  - Real Android: Must explicitly write CCCD descriptor

  Current wire/: Explicit CCCD write required (Android-style)Recommendation: swift/ wrapper should
  auto-write CCCD when subscribing

  🔧 Minor Issues

  8. Connection Callback Race Condition

  // Disconnect callback runs in goroutine
  go func() {
      if w.disconnectCallback != nil {
          w.disconnectCallback(peerUUID)
      }
  }()
  Risk: App may try to reconnect before cleanup finishesStatus: Probably intentional to avoid
  deadlocksAction: Document this async behavior clearly
