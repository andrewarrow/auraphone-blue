package wire

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/wire/att"
)

// SendGATTMessage sends a GATT message to a peer
// This function converts the high-level GATTMessage to binary ATT packets
func (w *Wire) SendGATTMessage(peerUUID string, msg *GATTMessage) error {
	// Set sender UUID if not already set
	if msg.SenderUUID == "" {
		msg.SenderUUID = w.hardwareUUID
	}

	logger.Debug(shortHash(w.hardwareUUID)+" Wire", "📡 SendGATTMessage to %s: op=%s, type=%s",
		shortHash(peerUUID), msg.Operation, msg.Type)

	// Debug log: GATT operation
	handle := w.uuidToHandle(peerUUID, msg.ServiceUUID, msg.CharacteristicUUID)
	w.debugLogger.LogGATTOperation("tx", peerUUID, msg.Operation, msg.ServiceUUID, msg.CharacteristicUUID, fmt.Sprintf("0x%04X", handle), msg.Data)

	// Convert GATTMessage to ATT packet
	// For now, we use a simple handle mapping (UUID hash to handle)
	// TODO: Implement proper GATT handle database with discovery
	var attPacket interface{}
	var err error

	switch msg.Type {
	case "gatt_request":
		switch msg.Operation {
		case "write":
			// Map UUID to handle (tries discovery cache first, falls back to hash)
			handle := w.uuidToHandle(peerUUID, msg.ServiceUUID, msg.CharacteristicUUID)

			// Get connection to check MTU
			w.mu.RLock()
			connection, exists := w.connections[peerUUID]
			w.mu.RUnlock()
			if !exists {
				return fmt.Errorf("no connection to peer %s", peerUUID)
			}

			// Check if fragmentation is needed
			if att.ShouldFragment(connection.mtu, msg.Data) {
				// Use Prepare Write + Execute Write for long values
				logger.Debug(shortHash(w.hardwareUUID)+" Wire",
					"🔀 Fragmenting write (len=%d, mtu=%d)", len(msg.Data), connection.mtu)
				return w.sendFragmentedWrite(peerUUID, handle, msg.Data, connection)
			}

			// Normal write for small values
			attPacket = &att.WriteRequest{
				Handle: handle,
				Value:  msg.Data,
			}
		case "read":
			// Map UUID to handle (tries discovery cache first, falls back to hash)
			handle := w.uuidToHandle(peerUUID, msg.ServiceUUID, msg.CharacteristicUUID)
			attPacket = &att.ReadRequest{
				Handle: handle,
			}
		default:
			return fmt.Errorf("unsupported operation: %s", msg.Operation)
		}

	case "gatt_response":
		switch msg.Status {
		case "success":
			if msg.Operation == "read" {
				attPacket = &att.ReadResponse{
					Value: msg.Data,
				}
			} else if msg.Operation == "write" {
				attPacket = &att.WriteResponse{}
			} else {
				return fmt.Errorf("unsupported response operation: %s", msg.Operation)
			}
		case "error":
			// Generic error response
			attPacket = &att.ErrorResponse{
				RequestOpcode: att.OpReadRequest, // Default, should be set properly
				Handle:        0x0000,
				ErrorCode:     att.ErrAttributeNotFound,
			}
		default:
			return fmt.Errorf("unsupported status: %s", msg.Status)
		}

	case "gatt_notification":
		// Map UUID to handle (tries discovery cache first, falls back to hash)
		handle := w.uuidToHandle(peerUUID, msg.ServiceUUID, msg.CharacteristicUUID)
		attPacket = &att.HandleValueNotification{
			Handle: handle,
			Value:  msg.Data,
		}

	default:
		return fmt.Errorf("unsupported message type: %s", msg.Type)
	}

	// Send the ATT packet
	err = w.sendATTPacket(peerUUID, attPacket)
	if err != nil {
		logger.Warn(shortHash(w.hardwareUUID)+" Wire", "❌ Failed to send ATT packet: %v", err)
		return err
	}

	return nil
}

// sendFragmentedWrite sends a long write using ATT Prepare Write + Execute Write
// This is used when the value exceeds the negotiated MTU
// Real BLE requires waiting for each Prepare Write Response before sending the next fragment
func (w *Wire) sendFragmentedWrite(peerUUID string, handle uint16, value []byte, connection *Connection) error {
	// Fragment the write into Prepare Write requests
	requests, err := att.FragmentWrite(handle, value, connection.mtu)
	if err != nil {
		return fmt.Errorf("failed to fragment write: %w", err)
	}

	logger.Debug(shortHash(w.hardwareUUID)+" Wire",
		"   Sending %d prepare write fragments", len(requests))

	// Get request tracker
	if connection.requestTracker == nil {
		return fmt.Errorf("connection has no request tracker")
	}
	tracker := connection.requestTracker.(*att.RequestTracker)

	// Send each Prepare Write request and wait for response
	// ATT enforces one outstanding request at a time
	for i, req := range requests {
		logger.Debug(shortHash(w.hardwareUUID)+" Wire",
			"   Fragment %d/%d: offset=%d, len=%d", i+1, len(requests), req.Offset, len(req.Value))

		// Start tracking this request
		responseC, err := tracker.StartRequest(att.OpPrepareWriteRequest, req.Handle, 0)
		if err != nil {
			return fmt.Errorf("failed to start tracking prepare write %d: %w", i, err)
		}

		// Send the Prepare Write request
		err = w.sendATTPacket(peerUUID, req)
		if err != nil {
			tracker.FailRequest(err)
			return fmt.Errorf("failed to send prepare write fragment %d: %w", i, err)
		}

		// Wait for the Prepare Write Response
		response := <-responseC
		if response.Error != nil {
			return fmt.Errorf("prepare write fragment %d failed: %w", i, response.Error)
		}

		// Verify the response
		prepareResp, ok := response.Packet.(*att.PrepareWriteResponse)
		if !ok {
			return fmt.Errorf("prepare write fragment %d: unexpected response type %T", i, response.Packet)
		}

		// Real BLE: server must echo back the same handle, offset, and value
		if prepareResp.Handle != req.Handle {
			return fmt.Errorf("prepare write fragment %d: handle mismatch (expected 0x%04X, got 0x%04X)",
				i, req.Handle, prepareResp.Handle)
		}
		if prepareResp.Offset != req.Offset {
			return fmt.Errorf("prepare write fragment %d: offset mismatch (expected %d, got %d)",
				i, req.Offset, prepareResp.Offset)
		}
		if len(prepareResp.Value) != len(req.Value) {
			return fmt.Errorf("prepare write fragment %d: value length mismatch (expected %d, got %d)",
				i, len(req.Value), len(prepareResp.Value))
		}
		// Note: We could also verify the actual value bytes match, but that's optional

		logger.Debug(shortHash(w.hardwareUUID)+" Wire",
			"   Fragment %d/%d confirmed", i+1, len(requests))
	}

	// Send Execute Write request to commit the write
	logger.Debug(shortHash(w.hardwareUUID)+" Wire",
		"   Sending execute write (commit)")

	// Start tracking the execute write request
	responseC, err := tracker.StartRequest(att.OpExecuteWriteRequest, handle, 0)
	if err != nil {
		return fmt.Errorf("failed to start tracking execute write: %w", err)
	}

	executeReq := &att.ExecuteWriteRequest{
		Flags: 0x01, // 0x01 = execute (commit), 0x00 = cancel
	}
	err = w.sendATTPacket(peerUUID, executeReq)
	if err != nil {
		tracker.FailRequest(err)
		return fmt.Errorf("failed to send execute write: %w", err)
	}

	// Wait for Execute Write Response
	response := <-responseC
	if response.Error != nil {
		return fmt.Errorf("execute write failed: %w", response.Error)
	}

	logger.Debug(shortHash(w.hardwareUUID)+" Wire",
		"   Fragmented write completed successfully")

	return nil
}

// attToGATTMessage converts an ATT packet to a GATTMessage for backward compatibility
// TODO: Remove this once higher layers use binary protocol directly
func (w *Wire) attToGATTMessage(packet interface{}) *GATTMessage {
	switch p := packet.(type) {
	case *att.ReadRequest:
		// Convert handle back to UUIDs (reverse of uuidToHandle)
		// For now, we use placeholder UUIDs since we don't have a reverse mapping
		return &GATTMessage{
			Type:               "gatt_request",
			Operation:          "read",
			ServiceUUID:        fmt.Sprintf("service-handle-%04x", p.Handle),
			CharacteristicUUID: fmt.Sprintf("char-handle-%04x", p.Handle),
		}

	case *att.ReadResponse:
		return &GATTMessage{
			Type:      "gatt_response",
			Operation: "read",
			Status:    "success",
			Data:      p.Value,
		}

	case *att.WriteRequest:
		return &GATTMessage{
			Type:               "gatt_request",
			Operation:          "write",
			ServiceUUID:        fmt.Sprintf("service-handle-%04x", p.Handle),
			CharacteristicUUID: fmt.Sprintf("char-handle-%04x", p.Handle),
			Data:               p.Value,
		}

	case *att.WriteCommand:
		return &GATTMessage{
			Type:               "gatt_request",
			Operation:          "write",
			ServiceUUID:        fmt.Sprintf("service-handle-%04x", p.Handle),
			CharacteristicUUID: fmt.Sprintf("char-handle-%04x", p.Handle),
			Data:               p.Value,
		}

	case *att.WriteResponse:
		return &GATTMessage{
			Type:      "gatt_response",
			Operation: "write",
			Status:    "success",
		}

	case *att.HandleValueNotification:
		return &GATTMessage{
			Type:               "gatt_notification",
			Operation:          "notify",
			ServiceUUID:        fmt.Sprintf("service-handle-%04x", p.Handle),
			CharacteristicUUID: fmt.Sprintf("char-handle-%04x", p.Handle),
			Data:               p.Value,
		}

	case *att.HandleValueIndication:
		return &GATTMessage{
			Type:               "gatt_notification",
			Operation:          "indicate",
			ServiceUUID:        fmt.Sprintf("service-handle-%04x", p.Handle),
			CharacteristicUUID: fmt.Sprintf("char-handle-%04x", p.Handle),
			Data:               p.Value,
		}

	case *att.ErrorResponse:
		return &GATTMessage{
			Type:      "gatt_response",
			Operation: "unknown",
			Status:    "error",
		}

	default:
		return nil
	}
}

// SetGATTMessageHandler sets the callback for incoming GATT messages
func (w *Wire) SetGATTMessageHandler(handler func(peerUUID string, msg *GATTMessage)) {
	w.handlerMu.Lock()
	w.gattHandler = handler
	w.handlerMu.Unlock()
}
