package android

import (
	"fmt"

	"github.com/user/auraphone-blue/kotlin"
	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
)

// ============================================================================
// BluetoothGattServerCallback Implementation (Peripheral role - others connect to us)
// ============================================================================

func (a *Android) OnConnectionStateChange(device *kotlin.BluetoothDevice, status int, newState int) {
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

func (a *Android) OnCharacteristicReadRequest(device *kotlin.BluetoothDevice, requestId int, offset int, characteristic *kotlin.BluetoothGattCharacteristic) {
	peerUUID := device.Address

	logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "📖 Read request from %s for char %s", shortHash(peerUUID), shortHash(characteristic.UUID))

	// Return empty response for now (reads not commonly used in our protocol)
	a.gattServer.SendResponse(device, requestId, kotlin.GATT_SUCCESS, offset, []byte{})
}

func (a *Android) OnCharacteristicWriteRequest(device *kotlin.BluetoothDevice, requestId int, characteristic *kotlin.BluetoothGattCharacteristic, preparedWrite bool, responseNeeded bool, offset int, value []byte) {
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
		// Profile data (not implemented yet)
	}

	// Send response if needed
	if responseNeeded {
		a.gattServer.SendResponse(device, requestId, kotlin.GATT_SUCCESS, offset, nil)
	}
}

func (a *Android) OnDescriptorReadRequest(device *kotlin.BluetoothDevice, requestId int, offset int, descriptor *kotlin.BluetoothGattDescriptor) {
	// Return empty response (descriptors not used in our protocol)
	a.gattServer.SendResponse(device, requestId, kotlin.GATT_SUCCESS, offset, []byte{})
}

func (a *Android) OnDescriptorWriteRequest(device *kotlin.BluetoothDevice, requestId int, descriptor *kotlin.BluetoothGattDescriptor, preparedWrite bool, responseNeeded bool, offset int, value []byte) {
	// CCCD descriptor writes happen when centrals subscribe to notifications
	// We don't need to do anything special here - just acknowledge
	if responseNeeded {
		a.gattServer.SendResponse(device, requestId, kotlin.GATT_SUCCESS, offset, nil)
	}
}
