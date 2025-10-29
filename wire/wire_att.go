package wire

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/wire/att"
	"github.com/user/auraphone-blue/wire/gatt"
	"github.com/user/auraphone-blue/wire/l2cap"
)

// handleATTPacket processes an incoming ATT packet
func (w *Wire) handleATTPacket(peerUUID string, connection *Connection, packet interface{}) {
	switch p := packet.(type) {
	case *att.ExchangeMTURequest:
		// Peer is requesting MTU exchange
		logger.Debug(shortHash(w.hardwareUUID)+" Wire", "📥 MTU Request from %s: client_mtu=%d", shortHash(peerUUID), p.ClientRxMTU)

		// Determine the MTU to use (minimum of client and our max)
		negotiatedMTU := int(p.ClientRxMTU)
		if negotiatedMTU > MaxMTU {
			negotiatedMTU = MaxMTU
		}
		if negotiatedMTU < l2cap.MinMTU {
			negotiatedMTU = l2cap.MinMTU
		}

		// Update connection MTU
		connection.mtu = negotiatedMTU

		// Send MTU response
		response := &att.ExchangeMTUResponse{
			ServerRxMTU: uint16(negotiatedMTU),
		}
		err := w.sendATTPacket(peerUUID, response)
		if err != nil {
			logger.Warn(shortHash(w.hardwareUUID)+" Wire", "❌ Failed to send MTU response to %s: %v", shortHash(peerUUID), err)
		}
		logger.Debug(shortHash(w.hardwareUUID)+" Wire", "📤 MTU Response to %s: server_mtu=%d", shortHash(peerUUID), negotiatedMTU)

	case *att.ExchangeMTUResponse:
		// Peer responded to our MTU request
		logger.Debug(shortHash(w.hardwareUUID)+" Wire", "📥 MTU Response from %s: server_mtu=%d", shortHash(peerUUID), p.ServerRxMTU)

		// Complete the pending MTU request
		if connection.requestTracker != nil {
			tracker := connection.requestTracker.(*att.RequestTracker)
			err := tracker.CompleteRequest(att.OpExchangeMTUResponse, p)
			if err != nil {
				logger.Warn(shortHash(w.hardwareUUID)+" Wire", "⚠️  MTU response without pending request: %v", err)
			}
		}

		// Determine the MTU to use (minimum of server and our request)
		negotiatedMTU := int(p.ServerRxMTU)
		if negotiatedMTU > MaxMTU {
			negotiatedMTU = MaxMTU
		}
		if negotiatedMTU < l2cap.MinMTU {
			negotiatedMTU = l2cap.MinMTU
		}

		// Update connection MTU
		connection.mtu = negotiatedMTU
		logger.Debug(shortHash(w.hardwareUUID)+" Wire", "✅ MTU negotiated with %s: %d bytes", shortHash(peerUUID), negotiatedMTU)

	case *att.PrepareWriteRequest:
		// Peer is sending a prepare write fragment
		logger.Debug(shortHash(w.hardwareUUID)+" Wire",
			"📥 Prepare Write Request from %s: handle=0x%04X, offset=%d, len=%d",
			shortHash(peerUUID), p.Handle, p.Offset, len(p.Value))

		// Add to fragmenter queue
		fragmenter := connection.fragmenter.(*att.Fragmenter)
		resp := &att.PrepareWriteResponse{
			Handle: p.Handle,
			Offset: p.Offset,
			Value:  p.Value, // Echo back the value
		}
		err := fragmenter.AddPrepareWriteResponse(resp)
		if err != nil {
			logger.Warn(shortHash(w.hardwareUUID)+" Wire",
				"❌ Failed to add prepare write fragment: %v", err)
			// Send error response
			errorResp := &att.ErrorResponse{
				RequestOpcode: att.OpPrepareWriteRequest,
				Handle:        p.Handle,
				ErrorCode:     att.ErrInvalidOffset,
			}
			w.sendATTPacket(peerUUID, errorResp)
			return
		}

		// Send echo response
		w.sendATTPacket(peerUUID, resp)

	case *att.ExecuteWriteRequest:
		// Peer is executing (committing) or canceling the prepared writes
		logger.Debug(shortHash(w.hardwareUUID)+" Wire",
			"📥 Execute Write Request from %s: flags=0x%02X", shortHash(peerUUID), p.Flags)

		fragmenter := connection.fragmenter.(*att.Fragmenter)

		if p.Flags == 0x01 {
			// Execute (commit) - get all queued handles and reassemble
			queuedHandles := fragmenter.GetQueuedHandles()

			for _, handle := range queuedHandles {
				// Reassemble the fragmented value
				value := fragmenter.GetReassembledValue(handle)
				if value == nil {
					continue
				}

				logger.Debug(shortHash(w.hardwareUUID)+" Wire",
					"📦 Reassembled fragmented write: handle=0x%04X, len=%d", handle, len(value))

				// Create a write request with the reassembled value
				// and deliver it as if it were a normal write
				writeReq := &att.WriteRequest{
					Handle: handle,
					Value:  value,
				}

				// Convert to GATT message and deliver to handler
				gattMsg := w.attToGATTMessage(writeReq)
				if gattMsg != nil {
					gattMsg.SenderUUID = peerUUID
					w.handlerMu.RLock()
					handler := w.gattHandler
					w.handlerMu.RUnlock()

					if handler != nil {
						handler(peerUUID, gattMsg)
					}
				}

				// Clear this handle's queue
				fragmenter.ClearQueue(handle)
			}

			logger.Debug(shortHash(w.hardwareUUID)+" Wire",
				"✅ Execute write committed")
		} else {
			// Cancel - clear the prepare queue
			fragmenter.ClearAllQueues()
			logger.Debug(shortHash(w.hardwareUUID)+" Wire",
				"❌ Execute write canceled")
		}

		// Send execute write response
		resp := &att.ExecuteWriteResponse{}
		w.sendATTPacket(peerUUID, resp)

	case *att.PrepareWriteResponse:
		// Response to our prepare write request
		logger.Debug(shortHash(w.hardwareUUID)+" Wire",
			"📥 Prepare Write Response from %s: handle=0x%04X, offset=%d, len=%d",
			shortHash(peerUUID), p.Handle, p.Offset, len(p.Value))

		// Complete the pending prepare write request
		if connection.requestTracker != nil {
			tracker := connection.requestTracker.(*att.RequestTracker)
			err := tracker.CompleteRequest(att.OpPrepareWriteResponse, p)
			if err != nil {
				logger.Warn(shortHash(w.hardwareUUID)+" Wire", "⚠️  Prepare write response without pending request: %v", err)
			}
		}

	case *att.ExecuteWriteResponse:
		// Response to our execute write request
		logger.Debug(shortHash(w.hardwareUUID)+" Wire",
			"✅ Execute Write Response from %s", shortHash(peerUUID))

		// Complete the pending execute write request
		if connection.requestTracker != nil {
			tracker := connection.requestTracker.(*att.RequestTracker)
			err := tracker.CompleteRequest(att.OpExecuteWriteResponse, p)
			if err != nil {
				logger.Warn(shortHash(w.hardwareUUID)+" Wire", "⚠️  Execute write response without pending request: %v", err)
			}
		}

	case *att.ReadResponse:
		// Complete pending read request
		if connection.requestTracker != nil {
			tracker := connection.requestTracker.(*att.RequestTracker)
			err := tracker.CompleteRequest(att.OpReadResponse, p)
			if err != nil {
				logger.Warn(shortHash(w.hardwareUUID)+" Wire", "⚠️  Read response without pending request: %v", err)
			}
		}
		// Also pass to GATT handler for backward compatibility
		msg := w.attToGATTMessage(packet)
		if msg != nil {
			w.handlerMu.RLock()
			handler := w.gattHandler
			w.handlerMu.RUnlock()
			if handler != nil {
				handler(peerUUID, msg)
			}
		}

	case *att.WriteResponse:
		// Complete pending write request
		if connection.requestTracker != nil {
			tracker := connection.requestTracker.(*att.RequestTracker)
			err := tracker.CompleteRequest(att.OpWriteResponse, p)
			if err != nil {
				logger.Warn(shortHash(w.hardwareUUID)+" Wire", "⚠️  Write response without pending request: %v", err)
			}
		}
		// Also pass to GATT handler for backward compatibility
		msg := w.attToGATTMessage(packet)
		if msg != nil {
			w.handlerMu.RLock()
			handler := w.gattHandler
			w.handlerMu.RUnlock()
			if handler != nil {
				handler(peerUUID, msg)
			}
		}

	case *att.ErrorResponse:
		// Complete pending request with error
		if connection.requestTracker != nil {
			tracker := connection.requestTracker.(*att.RequestTracker)
			err := tracker.CompleteRequest(att.OpErrorResponse, p)
			if err != nil {
				logger.Warn(shortHash(w.hardwareUUID)+" Wire", "⚠️  Error response without pending request: %v", err)
			}
		}
		// Also pass to GATT handler for backward compatibility
		msg := w.attToGATTMessage(packet)
		if msg != nil {
			w.handlerMu.RLock()
			handler := w.gattHandler
			w.handlerMu.RUnlock()
			if handler != nil {
				handler(peerUUID, msg)
			}
		}

	case *att.ReadByGroupTypeRequest:
		// Service discovery request
		logger.Debug(shortHash(w.hardwareUUID)+" Wire",
			"📥 Read By Group Type Request from %s: handles=0x%04X-0x%04X",
			shortHash(peerUUID), p.StartHandle, p.EndHandle)

		// Discover services from our attribute database
		w.dbMu.RLock()
		services := gatt.DiscoverServicesFromDatabase(w.attributeDB, p.StartHandle, p.EndHandle)
		w.dbMu.RUnlock()

		if len(services) == 0 {
			// No services found - send error response
			errorResp := &att.ErrorResponse{
				RequestOpcode: att.OpReadByGroupTypeRequest,
				Handle:        p.StartHandle,
				ErrorCode:     att.ErrAttributeNotFound,
			}
			w.sendATTPacket(peerUUID, errorResp)
			logger.Debug(shortHash(w.hardwareUUID)+" Wire",
				"📤 Error Response: Attribute Not Found")
		} else {
			// Build and send response
			responseData, err := gatt.BuildReadByGroupTypeResponse(services)
			if err != nil {
				logger.Warn(shortHash(w.hardwareUUID)+" Wire",
					"❌ Failed to build service discovery response: %v", err)
				return
			}

			response := &att.ReadByGroupTypeResponse{
				Length:        responseData[0],
				AttributeData: responseData[1:],
			}
			w.sendATTPacket(peerUUID, response)
			logger.Debug(shortHash(w.hardwareUUID)+" Wire",
				"📤 Service Discovery Response: %d services", len(services))
		}

	case *att.ReadByTypeRequest:
		// Characteristic discovery request
		logger.Debug(shortHash(w.hardwareUUID)+" Wire",
			"📥 Read By Type Request from %s: handles=0x%04X-0x%04X",
			shortHash(peerUUID), p.StartHandle, p.EndHandle)

		// Discover characteristics from our attribute database
		w.dbMu.RLock()
		characteristics := gatt.DiscoverCharacteristicsFromDatabase(w.attributeDB, p.StartHandle, p.EndHandle)
		w.dbMu.RUnlock()

		if len(characteristics) == 0 {
			// No characteristics found - send error response
			errorResp := &att.ErrorResponse{
				RequestOpcode: att.OpReadByTypeRequest,
				Handle:        p.StartHandle,
				ErrorCode:     att.ErrAttributeNotFound,
			}
			w.sendATTPacket(peerUUID, errorResp)
			logger.Debug(shortHash(w.hardwareUUID)+" Wire",
				"📤 Error Response: Attribute Not Found")
		} else {
			// Build and send response
			responseData, err := gatt.BuildReadByTypeResponse(characteristics)
			if err != nil {
				logger.Warn(shortHash(w.hardwareUUID)+" Wire",
					"❌ Failed to build characteristic discovery response: %v", err)
				return
			}

			response := &att.ReadByTypeResponse{
				Length:        responseData[0],
				AttributeData: responseData[1:],
			}
			w.sendATTPacket(peerUUID, response)
			logger.Debug(shortHash(w.hardwareUUID)+" Wire",
				"📤 Characteristic Discovery Response: %d characteristics", len(characteristics))
		}

	case *att.FindInformationRequest:
		// Descriptor discovery request
		logger.Debug(shortHash(w.hardwareUUID)+" Wire",
			"📥 Find Information Request from %s: handles=0x%04X-0x%04X",
			shortHash(peerUUID), p.StartHandle, p.EndHandle)

		// Discover descriptors from our attribute database
		w.dbMu.RLock()
		descriptors := gatt.DiscoverDescriptorsFromDatabase(w.attributeDB, p.StartHandle, p.EndHandle)
		w.dbMu.RUnlock()

		if len(descriptors) == 0 {
			// No descriptors found - send error response
			errorResp := &att.ErrorResponse{
				RequestOpcode: att.OpFindInformationRequest,
				Handle:        p.StartHandle,
				ErrorCode:     att.ErrAttributeNotFound,
			}
			w.sendATTPacket(peerUUID, errorResp)
			logger.Debug(shortHash(w.hardwareUUID)+" Wire",
				"📤 Error Response: Attribute Not Found")
		} else {
			// Build and send response
			responseData, err := gatt.BuildFindInformationResponse(descriptors)
			if err != nil {
				logger.Warn(shortHash(w.hardwareUUID)+" Wire",
					"❌ Failed to build descriptor discovery response: %v", err)
				return
			}

			response := &att.FindInformationResponse{
				Format: responseData[0],
				Data:   responseData[1:],
			}
			w.sendATTPacket(peerUUID, response)
			logger.Debug(shortHash(w.hardwareUUID)+" Wire",
				"📤 Descriptor Discovery Response: %d descriptors", len(descriptors))
		}

	case *att.ReadByGroupTypeResponse:
		// Service discovery response - store in connection's discovery cache
		logger.Debug(shortHash(w.hardwareUUID)+" Wire",
			"📥 Read By Group Type Response from %s", shortHash(peerUUID))

		// Parse the response
		responseData := make([]byte, 1+len(p.AttributeData))
		responseData[0] = p.Length
		copy(responseData[1:], p.AttributeData)

		services, err := gatt.ParseReadByGroupTypeResponse(responseData)
		if err != nil {
			logger.Warn(shortHash(w.hardwareUUID)+" Wire",
				"⚠️  Failed to parse service discovery response: %v", err)
		} else {
			// Store in discovery cache
			cache := connection.discoveryCache.(*gatt.DiscoveryCache)
			for _, service := range services {
				cache.AddService(service)
			}
			logger.Debug(shortHash(w.hardwareUUID)+" Wire",
				"✅ Stored %d services in discovery cache", len(services))
		}

		// Complete the pending request
		if connection.requestTracker != nil {
			tracker := connection.requestTracker.(*att.RequestTracker)
			err := tracker.CompleteRequest(att.OpReadByGroupTypeResponse, p)
			if err != nil {
				logger.Warn(shortHash(w.hardwareUUID)+" Wire",
					"⚠️  Service discovery response without pending request: %v", err)
			}
		}

	case *att.ReadByTypeResponse:
		// Characteristic discovery response - store in connection's discovery cache
		logger.Debug(shortHash(w.hardwareUUID)+" Wire",
			"📥 Read By Type Response from %s", shortHash(peerUUID))

		// Parse the response
		responseData := make([]byte, 1+len(p.AttributeData))
		responseData[0] = p.Length
		copy(responseData[1:], p.AttributeData)

		characteristics, err := gatt.ParseReadByTypeResponse(responseData)
		if err != nil {
			logger.Warn(shortHash(w.hardwareUUID)+" Wire",
				"⚠️  Failed to parse characteristic discovery response: %v", err)
		} else {
			// Store in discovery cache
			// Determine which service these characteristics belong to based on their handles
			cache := connection.discoveryCache.(*gatt.DiscoveryCache)

			if len(characteristics) > 0 {
				// Find the service that contains these characteristics
				charHandle := characteristics[0].DeclarationHandle
				for _, service := range cache.Services {
					if charHandle >= service.StartHandle && charHandle <= service.EndHandle {
						for _, char := range characteristics {
							cache.AddCharacteristic(service.StartHandle, char)
						}
						logger.Debug(shortHash(w.hardwareUUID)+" Wire",
							"✅ Stored %d characteristics in discovery cache for service 0x%04X",
							len(characteristics), service.StartHandle)
						break
					}
				}
			}
		}

		// Complete the pending request
		if connection.requestTracker != nil {
			tracker := connection.requestTracker.(*att.RequestTracker)
			err := tracker.CompleteRequest(att.OpReadByTypeResponse, p)
			if err != nil {
				logger.Warn(shortHash(w.hardwareUUID)+" Wire",
					"⚠️  Characteristic discovery response without pending request: %v", err)
			}
		}

	case *att.FindInformationResponse:
		// Descriptor discovery response - store in connection's discovery cache
		logger.Debug(shortHash(w.hardwareUUID)+" Wire",
			"📥 Find Information Response from %s", shortHash(peerUUID))

		// Parse the response
		responseData := make([]byte, 1+len(p.Data))
		responseData[0] = p.Format
		copy(responseData[1:], p.Data)

		descriptors, err := gatt.ParseFindInformationResponse(responseData)
		if err != nil {
			logger.Warn(shortHash(w.hardwareUUID)+" Wire",
				"⚠️  Failed to parse descriptor discovery response: %v", err)
		} else {
			// Store in discovery cache
			cache := connection.discoveryCache.(*gatt.DiscoveryCache)
			// TODO: Track which characteristic these descriptors belong to
			// For now, we just store them
			for _, desc := range descriptors {
				cache.AddDescriptor(0x0001, desc) // Placeholder handle
			}
			logger.Debug(shortHash(w.hardwareUUID)+" Wire",
				"✅ Stored %d descriptors in discovery cache", len(descriptors))
		}

		// Complete the pending request
		if connection.requestTracker != nil {
			tracker := connection.requestTracker.(*att.RequestTracker)
			err := tracker.CompleteRequest(att.OpFindInformationResponse, p)
			if err != nil {
				logger.Warn(shortHash(w.hardwareUUID)+" Wire",
					"⚠️  Descriptor discovery response without pending request: %v", err)
			}
		}

	case *att.ReadRequest, *att.WriteRequest, *att.WriteCommand,
		*att.HandleValueNotification, *att.HandleValueIndication:
		// These are GATT operations - convert to GATTMessage for compatibility
		// TODO: Remove this conversion once higher layers use binary protocol directly
		msg := w.attToGATTMessage(packet)
		if msg != nil {
			w.handlerMu.RLock()
			handler := w.gattHandler
			w.handlerMu.RUnlock()

			if handler != nil {
				logger.Debug(shortHash(w.hardwareUUID)+" Wire", "   ➡️  Calling GATT handler for ATT packet from %s", shortHash(peerUUID))
				handler(peerUUID, msg)
			} else {
				logger.Warn(shortHash(w.hardwareUUID)+" Wire", "⚠️  No GATT handler registered for ATT packet from %s", shortHash(peerUUID))
			}
		}

	default:
		logger.Warn(shortHash(w.hardwareUUID)+" Wire", "⚠️  Unsupported ATT packet type %T from %s", packet, shortHash(peerUUID))
	}
}

// sendATTPacket sends an ATT packet to a peer
func (w *Wire) sendATTPacket(peerUUID string, packet interface{}) error {
	// Encode ATT packet
	attData, err := att.EncodePacket(packet)
	if err != nil {
		return fmt.Errorf("failed to encode ATT packet: %w", err)
	}

	// Get connection to check MTU
	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()
	if !exists {
		return fmt.Errorf("not connected to %s", peerUUID)
	}

	// Enforce MTU strictly (except for MTU exchange packets and error responses)
	// MTU exchange and error responses are always allowed regardless of MTU
	switch packet.(type) {
	case *att.ExchangeMTURequest, *att.ExchangeMTUResponse, *att.ErrorResponse:
		// These packets are exempt from MTU checks
	default:
		// Check if ATT payload exceeds MTU
		if len(attData) > connection.mtu {
			logger.Warn(shortHash(w.hardwareUUID)+" Wire",
				"❌ ATT packet exceeds MTU: len=%d, mtu=%d (use fragmentation for large writes)",
				len(attData), connection.mtu)
			return fmt.Errorf("ATT packet exceeds MTU: %d > %d (use Prepare Write + Execute Write for large values)", len(attData), connection.mtu)
		}
	}

	// Debug log: ATT packet sent
	w.debugLogger.LogATTPacket("tx", peerUUID, packet, attData)

	// Wrap in L2CAP packet
	l2capPacket := l2cap.NewATTPacket(attData)

	// Send L2CAP packet
	return w.sendL2CAPPacket(peerUUID, l2capPacket)
}

// sendL2CAPPacket sends an L2CAP packet to a peer
func (w *Wire) sendL2CAPPacket(peerUUID string, packet *l2cap.Packet) error {
	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if !exists {
		logger.Warn(shortHash(w.hardwareUUID)+" Wire", "❌ sendL2CAPPacket: not connected to %s", shortHash(peerUUID))
		return fmt.Errorf("not connected to %s", peerUUID)
	}

	// Encode L2CAP packet
	data := packet.Encode()

	// Debug log: L2CAP packet sent
	w.debugLogger.LogL2CAPPacket("tx", peerUUID, packet)

	// Real BLE: Wait for next connection event (discrete time slots)
	// - Central controls timing and schedules events at connection interval
	// - Peripheral can ONLY send during scheduled connection events
	// - This enforces realistic connection event boundaries
	if connection.eventScheduler != nil {
		connection.eventScheduler.WaitForNextEvent()
	}

	// Lock for thread-safe writes
	connection.sendMutex.Lock()
	defer connection.sendMutex.Unlock()

	// Write packet data
	_, err := connection.conn.Write(data)
	if err != nil {
		return fmt.Errorf("failed to send L2CAP packet: %w", err)
	}

	logger.Debug(shortHash(w.hardwareUUID)+" Wire", "📡 Sent L2CAP packet to %s: channel=0x%04X, len=%d bytes",
		shortHash(peerUUID), packet.ChannelID, len(data))

	// Track message sent in health monitor
	socketType := string(connection.role)
	if connection.role == RolePeripheral {
		socketType = "peripheral"
	} else {
		socketType = "central"
	}
	w.socketHealthMonitor.RecordMessageSent(socketType, peerUUID)

	return nil
}
