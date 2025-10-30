package iphone

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	pb "github.com/user/auraphone-blue/proto"
	"github.com/user/auraphone-blue/swift"
	"google.golang.org/protobuf/proto"
)

// ============================================================================
// CBPeripheralManagerDelegate Implementation (Peripheral role - accepting connections)
// ============================================================================

func (ip *IPhone) DidUpdatePeripheralState(peripheralManager *swift.CBPeripheralManager) {
	logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Peripheral manager state: %s", peripheralManager.State)
}

func (ip *IPhone) DidStartAdvertising(peripheralManager *swift.CBPeripheralManager, err error) {
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to start advertising: %v", err)
	} else {
		logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "📡 Advertising started")
	}
}

func (ip *IPhone) DidReceiveReadRequest(peripheralManager *swift.CBPeripheralManager, request *swift.CBATTRequest) {
	logger.Trace(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "📖 Read request from %s for %s",
		shortHash(request.Central.UUID), shortHash(request.Characteristic.UUID))

	// REALISTIC BLE: Generate proper response based on which characteristic was read
	if request.Characteristic.UUID == phone.AuraProtocolCharUUID {
		// Generate handshake response with OUR data, not echoing back what was written
		ip.mu.RLock()
		photoHashBytes := []byte{}
		if ip.photoHash != "" {
			// Convert hex string to bytes
			for i := 0; i < len(ip.photoHash); i += 2 {
				var b byte
				fmt.Sscanf(ip.photoHash[i:i+2], "%02x", &b)
				photoHashBytes = append(photoHashBytes, b)
			}
		}
		profileVersion := ip.profileVersion
		firstName := ip.firstName
		deviceID := ip.deviceID
		ip.mu.RUnlock()

		logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "📖 Read handler: returning firstName='%s', deviceID='%s'", firstName, deviceID)

		// Use protobuf HandshakeMessage
		pbHandshake := &pb.HandshakeMessage{
			DeviceId:        deviceID,
			FirstName:       firstName,
			ProtocolVersion: 1,
			TxPhotoHash:     photoHashBytes,
			ProfileVersion:  profileVersion,
		}

		data, err := proto.Marshal(pbHandshake)
		if err != nil {
			logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Failed to marshal handshake for read response: %v", err)
			peripheralManager.RespondToRequest(request, 1) // 1 = error
			return
		}

		// Set the response data
		request.Value = data
	}

	// Respond with success (data is in request.Value or characteristic.Value)
	peripheralManager.RespondToRequest(request, 0) // 0 = success
}

func (ip *IPhone) DidReceiveWriteRequests(peripheralManager *swift.CBPeripheralManager, requests []*swift.CBATTRequest) {
	for _, request := range requests {
		logger.Trace(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "✍️  Write request from %s to %s (%d bytes)",
			shortHash(request.Central.UUID), shortHash(request.Characteristic.UUID), len(request.Value))

		// Handle based on characteristic
		if request.Characteristic.UUID == phone.AuraProtocolCharUUID {
			// Protocol characteristic receives handshakes, gossip, profile messages, and profile requests
			ip.handleProtocolMessage(request.Central.UUID, request.Value)
		} else if request.Characteristic.UUID == phone.AuraPhotoCharUUID {
			// Photo data
			ip.handlePhotoData(request.Central.UUID, request.Value)
		} else if request.Characteristic.UUID == phone.AuraProfileCharUUID {
			// Profile updates - route through protocol handler for parsing
			ip.handleProtocolMessage(request.Central.UUID, request.Value)
		}
	}

	peripheralManager.RespondToRequest(requests[0], 0) // 0 = success
}

func (ip *IPhone) CentralDidSubscribe(peripheralManager *swift.CBPeripheralManager, central swift.CBCentral, characteristic *swift.CBMutableCharacteristic) {
	logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔔 Central %s subscribed to %s",
		shortHash(central.UUID), shortHash(characteristic.UUID))

	// If they subscribed to photo characteristic, send them our photo
	if characteristic.UUID == phone.AuraPhotoCharUUID {
		logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "📸 Central %s subscribed to photo - sending chunks",
			shortHash(central.UUID))
		go ip.sendPhotoChunks(central.UUID)
	}
}

func (ip *IPhone) CentralDidUnsubscribe(peripheralManager *swift.CBPeripheralManager, central swift.CBCentral, characteristic *swift.CBMutableCharacteristic) {
	logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔕 Central %s unsubscribed from %s",
		shortHash(central.UUID), shortHash(characteristic.UUID))
}
