package wire

import (
	"fmt"
	"time"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/wire/l2cap"
)

// handleL2CAPSignaling processes L2CAP LE signaling channel packets
// This handles connection parameter update requests/responses
func (w *Wire) handleL2CAPSignaling(peerUUID string, connection *Connection, payload []byte) {
	if len(payload) < 4 {
		logger.Warn(shortHash(w.hardwareUUID)+" Wire", "⚠️  L2CAP signaling packet too short from %s", shortHash(peerUUID))
		return
	}

	commandCode := payload[0]

	switch commandCode {
	case l2cap.CodeConnectionParameterUpdateRequest:
		// Peer is requesting connection parameter update
		req, err := l2cap.DecodeConnectionParameterUpdateRequest(payload)
		if err != nil {
			logger.Warn(shortHash(w.hardwareUUID)+" Wire", "❌ Failed to decode connection parameter request from %s: %v", shortHash(peerUUID), err)
			return
		}

		logger.Debug(shortHash(w.hardwareUUID)+" Wire",
			"📥 Connection parameter update request from %s: interval=%.1f-%.1fms, latency=%d, timeout=%dms",
			shortHash(peerUUID), req.Params.IntervalMinMs(), req.Params.IntervalMaxMs(),
			req.Params.SlaveLatency, req.Params.SupervisionTimeoutMs())

		// In real BLE, only the Central can accept or reject parameter update requests
		// Peripheral receiving a request is a protocol violation
		result := l2cap.ConnectionParameterRejected
		if connection.role == RoleCentral {
			// Central can accept or reject based on its policy
			// For simulation, accept if parameters are valid
			result = l2cap.ConnectionParameterAccepted
			connection.params = req.Params
			connection.paramsUpdatedAt = time.Now()

			// Update event scheduler with new interval
			if connection.eventScheduler != nil {
				connection.eventScheduler.UpdateInterval(req.Params.IntervalMaxMs())
			}

			logger.Debug(shortHash(w.hardwareUUID)+" Wire",
				"✅ Central accepted parameter update from %s", shortHash(peerUUID))
		} else {
			// Peripheral receiving a request is a protocol error
			logger.Warn(shortHash(w.hardwareUUID)+" Wire",
				"⚠️  Peripheral received connection parameter update request from %s - rejecting (protocol violation)",
				shortHash(peerUUID))
		}

		// Send response
		resp := &l2cap.ConnectionParameterUpdateResponse{
			Identifier: req.Identifier,
			Result:     result,
		}

		respData := l2cap.EncodeConnectionParameterUpdateResponse(resp)
		respPacket := &l2cap.Packet{
			ChannelID: l2cap.ChannelLESignal,
			Payload:   respData,
		}

		if err := w.sendL2CAPPacket(peerUUID, respPacket); err != nil {
			logger.Warn(shortHash(w.hardwareUUID)+" Wire", "❌ Failed to send connection parameter response to %s: %v", shortHash(peerUUID), err)
			return
		}

		logger.Debug(shortHash(w.hardwareUUID)+" Wire",
			"📤 Connection parameter update response to %s: %s",
			shortHash(peerUUID), map[uint16]string{
				l2cap.ConnectionParameterAccepted: "accepted",
				l2cap.ConnectionParameterRejected: "rejected",
			}[result])

	case l2cap.CodeConnectionParameterUpdateResponse:
		// Peer responded to our connection parameter update request
		resp, err := l2cap.DecodeConnectionParameterUpdateResponse(payload)
		if err != nil {
			logger.Warn(shortHash(w.hardwareUUID)+" Wire", "❌ Failed to decode connection parameter response from %s: %v", shortHash(peerUUID), err)
			return
		}

		resultStr := "rejected"
		if resp.Result == l2cap.ConnectionParameterAccepted {
			resultStr = "accepted"
		}

		logger.Debug(shortHash(w.hardwareUUID)+" Wire",
			"📥 Connection parameter update response from %s: %s",
			shortHash(peerUUID), resultStr)

	default:
		logger.Warn(shortHash(w.hardwareUUID)+" Wire", "⚠️  Unsupported L2CAP signaling command 0x%02X from %s", commandCode, shortHash(peerUUID))
	}
}

// RequestConnectionParameterUpdate requests new connection parameters from the peer
// This is typically called by the Peripheral to request parameter updates from the Central
// In real BLE, iOS/Android as Central will accept or reject the request
func (w *Wire) RequestConnectionParameterUpdate(peerUUID string, params *l2cap.ConnectionParameters) error {
	if params == nil {
		return fmt.Errorf("nil connection parameters")
	}

	if err := params.Validate(); err != nil {
		return fmt.Errorf("invalid connection parameters: %w", err)
	}

	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if !exists {
		return fmt.Errorf("not connected to %s", peerUUID)
	}

	// In real BLE, only peripherals can request parameter updates via L2CAP signaling
	// The central then accepts or rejects the request
	if connection.role != RolePeripheral {
		return fmt.Errorf("only peripheral can request connection parameter updates (current role: %s)", connection.role)
	}
	logger.Debug(shortHash(w.hardwareUUID)+" Wire",
		"📶 Requesting connection parameters from %s: interval=%.1f-%.1fms, latency=%d, timeout=%dms",
		shortHash(peerUUID), params.IntervalMinMs(), params.IntervalMaxMs(),
		params.SlaveLatency, params.SupervisionTimeoutMs())

	// Generate identifier for request/response matching
	identifier := uint8(time.Now().UnixNano() & 0xFF)

	req := &l2cap.ConnectionParameterUpdateRequest{
		Identifier: identifier,
		Params:     params,
	}

	data, err := l2cap.EncodeConnectionParameterUpdateRequest(req)
	if err != nil {
		return fmt.Errorf("failed to encode connection parameter request: %w", err)
	}

	// Send via L2CAP signaling channel
	packet := &l2cap.Packet{
		ChannelID: l2cap.ChannelLESignal,
		Payload:   data,
	}

	return w.sendL2CAPPacket(peerUUID, packet)
}

// SetConnectionParameters sets new connection parameters (Central only)
// This allows the Central to unilaterally change connection parameters without peripheral request
// In real BLE, the Central can update parameters at any time to optimize for latency or power
func (w *Wire) SetConnectionParameters(peerUUID string, params *l2cap.ConnectionParameters) error {
	if params == nil {
		return fmt.Errorf("nil connection parameters")
	}

	if err := params.Validate(); err != nil {
		return fmt.Errorf("invalid connection parameters: %w", err)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	connection, exists := w.connections[peerUUID]
	if !exists {
		return fmt.Errorf("not connected to %s", peerUUID)
	}

	// In real BLE, only Central can set connection parameters unilaterally
	// Peripheral must use RequestConnectionParameterUpdate() instead
	if connection.role != RoleCentral {
		return fmt.Errorf("only central can set connection parameters directly (current role: %s)", connection.role)
	}

	connection.params = params
	connection.paramsUpdatedAt = time.Now()

	// Update event scheduler with new interval
	if connection.eventScheduler != nil {
		connection.eventScheduler.UpdateInterval(params.IntervalMaxMs())
	}

	logger.Debug(shortHash(w.hardwareUUID)+" Wire",
		"📶 Central set connection parameters for %s: interval=%.1f-%.1fms, latency=%d, timeout=%dms",
		shortHash(peerUUID), params.IntervalMinMs(), params.IntervalMaxMs(),
		params.SlaveLatency, params.SupervisionTimeoutMs())

	return nil
}

// GetConnectionParameters returns the current connection parameters for a peer
func (w *Wire) GetConnectionParameters(peerUUID string) (*l2cap.ConnectionParameters, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	connection, exists := w.connections[peerUUID]
	if !exists {
		return nil, fmt.Errorf("not connected to %s", peerUUID)
	}

	if connection.params == nil {
		// Return default parameters if none have been negotiated
		return l2cap.DefaultConnectionParameters(), nil
	}

	return connection.params.(*l2cap.ConnectionParameters), nil
}
