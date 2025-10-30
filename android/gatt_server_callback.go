package android

import (
	"fmt"

	"github.com/user/auraphone-blue/kotlin"
	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	pb "github.com/user/auraphone-blue/proto"
	"google.golang.org/protobuf/proto"
)

// ============================================================================
// BluetoothGattServerCallback Implementation (Peripheral role - others connect to us)
// ============================================================================

// androidGattServerCallback wraps Android to implement BluetoothGattServerCallback
type androidGattServerCallback struct {
	android *Android
}

func (cb *androidGattServerCallback) OnConnectionStateChange(device *kotlin.BluetoothDevice, status int, newState int) {
	a := cb.android
	peerUUID := device.Address

	switch newState {
	case kotlin.STATE_CONNECTED:
		logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "📱 Central %s connected to us (peripheral mode)", shortHash(peerUUID))

		// Mark as connected in IdentityManager
		a.identityManager.MarkConnected(peerUUID)

	case kotlin.STATE_DISCONNECTED:
		logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "📱 Central %s disconnected from us", shortHash(peerUUID))

		// Mark as disconnected in IdentityManager
		a.identityManager.MarkDisconnected(peerUUID)

		// Mark as disconnected in mesh view
		if deviceID, exists := a.identityManager.GetDeviceID(peerUUID); exists {
			a.meshView.MarkDeviceDisconnected(deviceID)
		}
	}
}

func (cb *androidGattServerCallback) OnCharacteristicReadRequest(device *kotlin.BluetoothDevice, requestId int, offset int, characteristic *kotlin.BluetoothGattCharacteristic) {
	a := cb.android
	peerUUID := device.Address

	logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "📖 Read request from %s for char %s", shortHash(peerUUID), shortHash(characteristic.UUID))

	// REALISTIC BLE: Generate proper response based on which characteristic was read
	var responseData []byte
	if characteristic.UUID == phone.AuraProtocolCharUUID {
		// Generate handshake response with OUR data, not echoing back what was written
		a.mu.RLock()
		photoHashBytes := []byte{}
		if a.photoHash != "" {
			// Convert hex string to bytes
			for i := 0; i < len(a.photoHash); i += 2 {
				var b byte
				fmt.Sscanf(a.photoHash[i:i+2], "%02x", &b)
				photoHashBytes = append(photoHashBytes, b)
			}
		}
		profileVersion := a.profileVersion
		firstName := a.firstName
		deviceID := a.deviceID
		a.mu.RUnlock()

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
			logger.Error(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "Failed to marshal handshake for read response: %v", err)
			a.gattServer.SendResponse(device, requestId, kotlin.GATT_FAILURE, offset, nil)
			return
		}

		responseData = data
	}

	// Respond with success (data is in responseData or empty for unhandled characteristics)
	a.gattServer.SendResponse(device, requestId, kotlin.GATT_SUCCESS, offset, responseData)
}

func (cb *androidGattServerCallback) OnCharacteristicWriteRequest(device *kotlin.BluetoothDevice, requestId int, characteristic *kotlin.BluetoothGattCharacteristic, preparedWrite bool, responseNeeded bool, offset int, value []byte) {
	a := cb.android
	peerUUID := device.Address

	// Route based on characteristic UUID
	switch characteristic.UUID {
	case phone.AuraProtocolCharUUID:
		// Handshake or gossip message
		a.handleProtocolMessage(peerUUID, value)

	case phone.AuraPhotoCharUUID:
		// Photo chunk
		a.handlePhotoChunk(peerUUID, value)

	case phone.AuraProfileCharUUID:
		// Profile updates - route through protocol handler for parsing
		a.handleProtocolMessage(peerUUID, value)
	}

	// Send response if needed
	if responseNeeded {
		a.gattServer.SendResponse(device, requestId, kotlin.GATT_SUCCESS, offset, nil)
	}
}

func (cb *androidGattServerCallback) OnDescriptorReadRequest(device *kotlin.BluetoothDevice, requestId int, offset int, descriptor *kotlin.BluetoothGattDescriptor) {
	a := cb.android
	// Return empty response (descriptors not used in our protocol)
	a.gattServer.SendResponse(device, requestId, kotlin.GATT_SUCCESS, offset, []byte{})
}

func (cb *androidGattServerCallback) OnDescriptorWriteRequest(device *kotlin.BluetoothDevice, requestId int, descriptor *kotlin.BluetoothGattDescriptor, preparedWrite bool, responseNeeded bool, offset int, value []byte) {
	a := cb.android
	peerUUID := device.Address

	// CCCD descriptor writes happen when centrals subscribe to notifications
	// Check if this is a subscribe (value = [0x01, 0x00] for notifications)
	if len(value) >= 2 && value[0] == 0x01 {
		// Central subscribed to notifications
		charUUID := descriptor.Characteristic.UUID
		logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "🔔 Central %s subscribed to %s",
			shortHash(peerUUID), shortHash(charUUID))

		// If they subscribed to photo characteristic, send them our photo
		if charUUID == phone.AuraPhotoCharUUID {
			logger.Info(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "📸 Central %s subscribed to photo - sending chunks",
				shortHash(peerUUID))
			go a.sendPhotoChunks(peerUUID)
		}
	}

	if responseNeeded {
		a.gattServer.SendResponse(device, requestId, kotlin.GATT_SUCCESS, offset, nil)
	}
}
