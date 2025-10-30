package wire

import (
	"encoding/binary"
	"io"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/wire/att"
	"github.com/user/auraphone-blue/wire/l2cap"
)

// readMessages continuously reads messages from a connection
// Note: Must be called with wg.Add(1) already done by caller
func (w *Wire) readMessages(peerUUID string, connection *Connection, stopChan chan struct{}) {
	defer func() {
		w.wg.Done()
		// Log read loop ended
		socketType := string(connection.role)
		if connection.role == RolePeripheral {
			socketType = "peripheral"
		} else {
			socketType = "central"
		}
		w.connectionEventLog.LogReadLoopEnded(socketType, peerUUID, "", "connection closed")
		w.socketHealthMonitor.RemoveConnection(socketType, peerUUID)

		// Clean up on exit
		w.mu.Lock()
		delete(w.connections, peerUUID)
		w.mu.Unlock()

		w.stopMu.Lock()
		delete(w.stopReading, peerUUID)
		w.stopMu.Unlock()

		connection.conn.Close()

		// Notify disconnect callback
		w.callbackMu.RLock()
		disconnectCb := w.disconnectCallback
		w.callbackMu.RUnlock()
		if disconnectCb != nil {
			disconnectCb(peerUUID)
		}
	}()

	for {
		select {
		case <-stopChan:
			return
		default:
		}

		// Read L2CAP packet length (2 bytes, little-endian)
		var l2capLen uint16
		err := binary.Read(connection.conn, binary.LittleEndian, &l2capLen)
		if err != nil {
			return // Connection closed or error
		}

		// Read the rest of the L2CAP header and payload
		// Total packet size = 4 bytes header (2 len + 2 channel) + payload
		packetData := make([]byte, l2cap.L2CAPHeaderLen+int(l2capLen))
		binary.LittleEndian.PutUint16(packetData[0:2], l2capLen)

		_, err = io.ReadFull(connection.conn, packetData[2:])
		if err != nil {
			return // Connection closed or error
		}

		// Decode L2CAP packet
		l2capPacket, err := l2cap.Decode(packetData)
		if err != nil {
			logger.Warn(shortHash(w.hardwareUUID)+" Wire", "❌ Failed to decode L2CAP packet from %s: %v", shortHash(peerUUID), err)
			continue
		}

		logger.Debug(shortHash(w.hardwareUUID)+" Wire", "📥 Received L2CAP packet from %s: channel=0x%04X, len=%d bytes",
			shortHash(peerUUID), l2capPacket.ChannelID, len(l2capPacket.Payload))

		// Debug log: L2CAP packet received
		w.debugLogger.LogL2CAPPacket("rx", peerUUID, l2capPacket)

		// Track message received in health monitor
		socketType := string(connection.role)
		if connection.role == RolePeripheral {
			socketType = "peripheral"
		} else {
			socketType = "central"
		}
		w.socketHealthMonitor.RecordMessageReceived(socketType, peerUUID)

		// Route based on L2CAP channel
		switch l2capPacket.ChannelID {
		case l2cap.ChannelATT:
			// Decode ATT packet
			attPacket, err := att.DecodePacket(l2capPacket.Payload)
			if err != nil {
				logger.Warn(shortHash(w.hardwareUUID)+" Wire", "❌ Failed to decode ATT packet from %s: %v", shortHash(peerUUID), err)
				continue
			}

			// Debug log: ATT packet received
			w.debugLogger.LogATTPacket("rx", peerUUID, attPacket, l2capPacket.Payload)

			// Handle ATT packet
			w.handleATTPacket(peerUUID, connection, attPacket)

		case l2cap.ChannelLESignal:
			// Handle L2CAP LE signaling channel (connection parameter updates)
			w.handleL2CAPSignaling(peerUUID, connection, l2capPacket.Payload)

		default:
			logger.Warn(shortHash(w.hardwareUUID)+" Wire", "⚠️  Unsupported L2CAP channel 0x%04X from %s", l2capPacket.ChannelID, shortHash(peerUUID))
		}
	}
}
