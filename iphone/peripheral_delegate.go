package iphone

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/swift"
	pb "github.com/user/auraphone-blue/proto"
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

	// For now, respond with empty data
	peripheralManager.RespondToRequest(request, 0) // 0 = success
}

func (ip *IPhone) DidReceiveWriteRequests(peripheralManager *swift.CBPeripheralManager, requests []*swift.CBATTRequest) {
	for _, request := range requests {
		logger.Trace(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "✍️  Write request from %s to %s (%d bytes)",
			shortHash(request.Central.UUID), shortHash(request.Characteristic.UUID), len(request.Value))

		// Handle based on characteristic
		if request.Characteristic.UUID == phone.AuraProtocolCharUUID {
			// Protocol characteristic receives both handshakes and gossip messages
			// Try to distinguish by parsing as gossip first (has MeshView field)
			var gossipMsg pb.GossipMessage
			if proto.Unmarshal(request.Value, &gossipMsg) == nil && len(gossipMsg.MeshView) > 0 {
				// It's a gossip message
				ip.handleGossipMessage(request.Central.UUID, request.Value)
			} else {
				// It's a handshake message
				ip.handleHandshake(request.Central.UUID, request.Value)
			}
		} else if request.Characteristic.UUID == phone.AuraPhotoCharUUID {
			// Photo data
			ip.handlePhotoData(request.Central.UUID, request.Value)
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
