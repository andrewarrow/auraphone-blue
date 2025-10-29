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

	// Subscribe to protocol characteristic for handshake and gossip
	// CRITICAL: Real Android requires two steps:
	// 1. setCharacteristicNotification() - local tracking only
	// 2. writeDescriptor(CCCD) - actually enables notifications on peripheral
	char := gatt.GetCharacteristic(phone.AuraServiceUUID, phone.AuraProtocolCharUUID)
	if char != nil {
		// Step 1: Enable local notification tracking
		gatt.SetCharacteristicNotification(char, true)

		// Step 2: Write to CCCD descriptor to enable notifications on peripheral
		cccdDescriptor := char.GetDescriptor(kotlin.CCCD_UUID)
		if cccdDescriptor != nil {
			cccdDescriptor.Value = kotlin.ENABLE_NOTIFICATION_VALUE
			gatt.WriteDescriptor(cccdDescriptor)
			logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "✅ Wrote CCCD for protocol characteristic")
		} else {
			logger.Error(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "❌ CCCD descriptor not found for protocol characteristic!")
		}
	} else {
		logger.Error(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "❌ Protocol characteristic not found!")
	}

	// Subscribe to photo characteristic for photo transfers
	photoChar := gatt.GetCharacteristic(phone.AuraServiceUUID, phone.AuraPhotoCharUUID)
	if photoChar != nil {
		// Step 1: Enable local notification tracking
		gatt.SetCharacteristicNotification(photoChar, true)

		// Step 2: Write to CCCD descriptor to enable notifications on peripheral
		cccdDescriptor := photoChar.GetDescriptor(kotlin.CCCD_UUID)
		if cccdDescriptor != nil {
			cccdDescriptor.Value = kotlin.ENABLE_NOTIFICATION_VALUE
			gatt.WriteDescriptor(cccdDescriptor)
			logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "✅ Wrote CCCD for photo characteristic")
		} else {
			logger.Error(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "❌ CCCD descriptor not found for photo characteristic!")
		}
	} else {
		logger.Error(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "❌ Photo characteristic not found!")
	}

	// Send handshake
	a.sendHandshake(peerUUID, gatt)
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

func (a *Android) OnDescriptorWrite(gatt *kotlin.BluetoothGatt, descriptor *kotlin.BluetoothGattDescriptor, status int) {
	// Descriptor write completed (CCCD write to enable notifications)
	if status != kotlin.GATT_SUCCESS {
		logger.Warn(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "⚠️  Descriptor write failed")
	}
}

func (a *Android) OnDescriptorRead(gatt *kotlin.BluetoothGatt, descriptor *kotlin.BluetoothGattDescriptor, status int) {
	// Descriptor read completed (not commonly used)
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
