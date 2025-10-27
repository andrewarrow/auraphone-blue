package iphone

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/swift"
)

// CBPeripheralManagerDelegate methods for Peripheral mode (advertising and serving)

func (d *iPhonePeripheralDelegate) DidUpdateState(peripheralManager *swift.CBPeripheralManager) {
	// State updated
}

func (d *iPhonePeripheralDelegate) DidStartAdvertising(peripheralManager *swift.CBPeripheralManager, err error) {
	prefix := fmt.Sprintf("%s iOS", d.iphone.hardwareUUID[:8])
	if err != nil {
		logger.Error(prefix, "❌ Failed to start advertising: %v", err)
	} else {
		logger.Debug(prefix, "✅ Advertising started")
	}
}

func (d *iPhonePeripheralDelegate) DidReceiveReadRequest(peripheralManager *swift.CBPeripheralManager, request *swift.CBATTRequest) {
	// We don't support reads - all data pushed via writes/notifications
	peripheralManager.RespondToRequest(request, 0)
}

func (d *iPhonePeripheralDelegate) DidReceiveWriteRequests(peripheralManager *swift.CBPeripheralManager, requests []*swift.CBATTRequest) {
	prefix := fmt.Sprintf("%s iOS", d.iphone.hardwareUUID[:8])

	for _, request := range requests {
		senderUUID := request.Central.UUID
		logger.Debug(prefix, "📥 Peripheral write from %s to char %s (%d bytes)",
			senderUUID[:8], request.Characteristic.UUID[:8], len(request.Value))

		// Route message based on characteristic UUID
		switch request.Characteristic.UUID {
		case phone.AuraProtocolCharUUID:
			// Gossip, photo request, or profile request
			if err := d.iphone.messageRouter.HandleProtocolMessage(senderUUID, request.Value); err != nil {
				logger.Error(prefix, "❌ Failed to handle protocol message: %v", err)
			}

		case phone.AuraPhotoCharUUID:
			// Photo chunk data
			d.iphone.photoHandler.HandlePhotoChunk(senderUUID, request.Value)

		case phone.AuraProfileCharUUID:
			// Profile message
			d.iphone.profileHandler.HandleProfileMessage(senderUUID, request.Value)
		}
	}

	// Respond success
	peripheralManager.RespondToRequest(requests[0], 0)
}

func (d *iPhonePeripheralDelegate) CentralDidSubscribe(peripheralManager *swift.CBPeripheralManager, central swift.CBCentral, characteristic *swift.CBMutableCharacteristic) {
	prefix := fmt.Sprintf("%s iOS", d.iphone.hardwareUUID[:8])
	logger.Debug(prefix, "🔔 Central %s subscribed to %s", central.UUID[:8], characteristic.UUID[:8])

	// Register this central connection
	d.iphone.connManager.RegisterPeripheralConnection(central.UUID)

	// Mark device as connected in identity manager
	d.iphone.identityManager.MarkConnected(central.UUID)

	// NEW (Week 3): Mark device as connected in mesh view
	if deviceID, ok := d.iphone.identityManager.GetDeviceID(central.UUID); ok {
		d.iphone.meshView.MarkDeviceConnected(deviceID)
	}

	// Send initial gossip when they subscribe to protocol characteristic
	if characteristic.UUID == phone.AuraProtocolCharUUID {
		go d.iphone.gossipHandler.SendGossipToDevice(central.UUID)
	}
}

func (d *iPhonePeripheralDelegate) CentralDidUnsubscribe(peripheralManager *swift.CBPeripheralManager, central swift.CBCentral, characteristic *swift.CBMutableCharacteristic) {
	prefix := fmt.Sprintf("%s iOS", d.iphone.hardwareUUID[:8])
	logger.Debug(prefix, "🔕 Central %s unsubscribed from %s", central.UUID[:8], characteristic.UUID[:8])

	// Get device ID before cleanup
	deviceID, _ := d.iphone.identityManager.GetDeviceID(central.UUID)

	// Mark device as disconnected in identity manager
	d.iphone.identityManager.MarkDisconnected(central.UUID)

	// NEW (Week 3): Mark device as disconnected in mesh view
	if deviceID != "" {
		d.iphone.meshView.MarkDeviceDisconnected(deviceID)
	}

	// Unregister when they disconnect completely (we'd get unsubscribe for all chars)
	d.iphone.connManager.UnregisterPeripheralConnection(central.UUID)
}

// sendViaPeripheralMode sends data to a device that connected to us (we're Peripheral)
func (ip *iPhone) sendViaPeripheralMode(remoteUUID, charUUID string, data []byte) error {
	var targetChar *swift.CBMutableCharacteristic
	switch charUUID {
	case phone.AuraProtocolCharUUID:
		targetChar = ip.protocolChar
	case phone.AuraPhotoCharUUID:
		targetChar = ip.photoChar
	case phone.AuraProfileCharUUID:
		targetChar = ip.profileChar
	default:
		return fmt.Errorf("unknown characteristic %s", charUUID[:8])
	}

	central := swift.CBCentral{UUID: remoteUUID}
	if success := ip.peripheralManager.UpdateValue(data, targetChar, []swift.CBCentral{central}); !success {
		return fmt.Errorf("failed to send notification (queue full)")
	}

	return nil
}
