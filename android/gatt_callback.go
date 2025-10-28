package android

import (
	"fmt"

	"github.com/user/auraphone-blue/kotlin"
	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
)

// ============================================================================
// BluetoothGattCallback Implementation (Central role - we're connected to a peripheral)
// ============================================================================

func (a *Android) OnConnectionStateChange(gatt *kotlin.BluetoothGatt, status int, newState int) {
	peerUUID := gatt.GetRemoteUUID()

	switch newState {
	case kotlin.STATE_CONNECTED:
		logger.Info(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "✅ Connected to %s", shortHash(peerUUID))

		// Mark as connected in IdentityManager (tracks connection state by hardware UUID)
		a.identityManager.MarkConnected(peerUUID)

		// Discover services
		gatt.DiscoverServices()

	case kotlin.STATE_DISCONNECTED:
		logger.Info(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "🔌 Disconnected from %s", shortHash(peerUUID))

		// Mark as disconnected in IdentityManager
		a.identityManager.MarkDisconnected(peerUUID)

		// Mark as disconnected in mesh view
		if deviceID, exists := a.identityManager.GetDeviceID(peerUUID); exists {
			a.meshView.MarkDeviceDisconnected(deviceID)
		}

		a.mu.Lock()
		delete(a.handshaked, peerUUID)
		delete(a.connectedGatts, peerUUID)
		a.mu.Unlock()
	}
}

func (a *Android) OnServicesDiscovered(gatt *kotlin.BluetoothGatt, status int) {
	peerUUID := gatt.GetRemoteUUID()

	if status != kotlin.GATT_SUCCESS {
		logger.Error(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "❌ Service discovery failed for %s", shortHash(peerUUID))
		return
	}

	logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "📋 Services discovered for %s", shortHash(peerUUID))

	// Subscribe to protocol characteristic for handshake and gossip
	logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "🔍 Getting protocol characteristic for subscription")
	char := gatt.GetCharacteristic(phone.AuraServiceUUID, phone.AuraProtocolCharUUID)
	if char != nil {
		logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "🔍 Subscribing to protocol characteristic")
		gatt.SetCharacteristicNotification(char, true)
	} else {
		logger.Error(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "❌ Protocol characteristic not found!")
	}

	// Subscribe to photo characteristic for photo transfers
	logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "🔍 Getting photo characteristic for subscription")
	photoChar := gatt.GetCharacteristic(phone.AuraServiceUUID, phone.AuraPhotoCharUUID)
	if photoChar != nil {
		logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "🔍 Subscribing to photo characteristic")
		gatt.SetCharacteristicNotification(photoChar, true)
	} else {
		logger.Error(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "❌ Photo characteristic not found!")
	}

	// Send handshake
	logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "🔍 Calling sendHandshake")
	a.sendHandshake(peerUUID, gatt)
	logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "🔍 OnServicesDiscovered complete")
}

func (a *Android) OnCharacteristicWrite(gatt *kotlin.BluetoothGatt, characteristic *kotlin.BluetoothGattCharacteristic, status int) {
	// Write completed (not critical to log)
	if status != kotlin.GATT_SUCCESS {
		logger.Warn(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "⚠️  Write failed for char %s", shortHash(characteristic.UUID))
	}
}

func (a *Android) OnCharacteristicRead(gatt *kotlin.BluetoothGatt, characteristic *kotlin.BluetoothGattCharacteristic, status int) {
	// Read completed (not commonly used in our protocol)
}

func (a *Android) OnCharacteristicChanged(gatt *kotlin.BluetoothGatt, characteristic *kotlin.BluetoothGattCharacteristic) {
	// This is called by BluetoothGatt.HandleGATTMessage when a notification arrives
	// Route based on characteristic UUID
	peerUUID := gatt.GetRemoteUUID()

	switch characteristic.UUID {
	case phone.AuraProtocolCharUUID:
		// Handshake or gossip message
		a.handleProtocolMessage(peerUUID, characteristic.Value)

	case phone.AuraPhotoCharUUID:
		// Photo chunk
		a.handlePhotoChunk(peerUUID, characteristic.Value)

	case phone.AuraProfileCharUUID:
		// Profile data (not implemented yet)
	}
}
