Issue: Missing MTU Enforcement and Fragmentation

  Location: wire/wire.go - SendGATTMessage() (lines 461-511)

  Problem:
  func (w *Wire) SendGATTMessage(peerUUID string, msg *GATTMessage) error {
      // Marshal message to JSON
      data, err := json.Marshal(msg)
      // ... sends entire message without checking size
      msgLen := uint32(len(data))
      // Sends full message in one shot
  }

  What's unrealistic:
  1. CLAUDE.md claims: "✅ MTU negotiation - Packet size limits with automatic fragmentation"
  2. But wire.go has no MTU field, no size checking, and no fragmentation logic
  3. Messages of ANY size are sent in a single packet
  4. In real BLE:
    - Default MTU: 23 bytes (BLE 4.0) to 512 bytes (BLE 5.0+)
    - Common negotiated MTU: 185 bytes
    - Large data must be fragmented into MTU-sized chunks

  Example unrealistic scenario:
  // Photo chunk could be 4KB
  photoData := make([]byte, 4096)

  // This sends 4KB in one message - unrealistic!
  // Real BLE would require ~23 fragments at 185-byte MTU
  wire.NotifyCharacteristic(peerUUID, serviceUUID, charUUID, photoData)

  Recommended fix:
  type Wire struct {
      // ... existing fields ...
      mtu int  // Default: 185 bytes, configurable 23-512
  }

  func (w *Wire) SendGATTMessage(peerUUID string, msg *GATTMessage) error {
      data, err := json.Marshal(msg)

      // Fragment if message exceeds MTU
      if len(data) > w.mtu {
          return w.sendFragmented(peerUUID, data)
      }

      // Send normally if under MTU
      return w.sendSinglePacket(peerUUID, data)
  }

  func (w *Wire) sendFragmented(peerUUID string, data []byte) error {
      // Split into MTU-sized chunks
      // Add fragment headers (sequence number, total fragments, etc.)
      // Send each fragment with small delay (simulate radio timing)
      // Apply packet loss simulation per fragment
  }

