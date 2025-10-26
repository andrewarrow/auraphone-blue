package android

import (
	"fmt"

	"github.com/user/auraphone-blue/kotlin"
	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
)

// Peripheral mode BLE callbacks (Advertiser + GATT Server)

// AdvertiseCallback methods

// OnStartSuccess is called when advertising starts successfully
func (a *Android) OnStartSuccess(settingsInEffect *kotlin.AdvertiseSettings) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
	logger.Debug(prefix, "✅ Advertising started successfully")
}

// OnStartFailure is called when advertising fails to start
func (a *Android) OnStartFailure(errorCode int) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
	logger.Error(prefix, "❌ Advertising failed to start (error code: %d)", errorCode)
}

// BluetoothGattServerCallback methods (GATT Server callbacks)

// OnConnectionStateChange is called when a central device connects/disconnects to our GATT server
func (d *androidGattServerDelegate) OnConnectionStateChange(device *kotlin.BluetoothDevice, status int, newState int) {
	prefix := fmt.Sprintf("%s Android", d.android.hardwareUUID[:8])

	if newState == kotlin.STATE_CONNECTED {
		logger.Debug(prefix, "📥 GATT server: Central %s connected", device.Address[:8])

		// Register connection with ConnectionManager
		if d.android.connManager != nil {
			d.android.connManager.RegisterPeripheralConnection(device.Address)
		}

		// Track this central connection
		d.android.mu.Lock()
		d.android.connectedCentrals[device.Address] = true
		d.android.mu.Unlock()

		// Send initial gossip after connection
		go d.android.sendGossipToDevice(device.Address)

	} else if newState == kotlin.STATE_DISCONNECTED {
		logger.Debug(prefix, "📥 GATT server: Central %s disconnected", device.Address[:8])

		// Unregister connection with ConnectionManager
		if d.android.connManager != nil {
			d.android.connManager.UnregisterPeripheralConnection(device.Address)
		}

		// Remove from connected centrals
		d.android.mu.Lock()
		delete(d.android.connectedCentrals, device.Address)
		d.android.mu.Unlock()
	}
}

// OnCharacteristicReadRequest is called when a central reads a characteristic
func (d *androidGattServerDelegate) OnCharacteristicReadRequest(device *kotlin.BluetoothDevice, requestId int, offset int, characteristic *kotlin.BluetoothGattCharacteristic) {
	prefix := fmt.Sprintf("%s Android", d.android.hardwareUUID[:8])
	logger.Debug(prefix, "📥 GATT server: Read request from %s for char %s", device.Address[:8], characteristic.UUID[:8])
	// For now, we don't support reads - all data is pushed via writes
}

// OnCharacteristicWriteRequest is called when a central writes to a characteristic
func (d *androidGattServerDelegate) OnCharacteristicWriteRequest(device *kotlin.BluetoothDevice, requestId int, characteristic *kotlin.BluetoothGattCharacteristic, preparedWrite bool, responseNeeded bool, offset int, value []byte) {
	prefix := fmt.Sprintf("%s Android", d.android.hardwareUUID[:8])

	logger.Debug(prefix, "📥 GATT server: Write request from %s to char %s (%d bytes)", device.Address[:8], characteristic.UUID[:8], len(value))

	// Process based on characteristic
	senderUUID := device.Address

	switch characteristic.UUID {
	case phone.AuraProtocolCharUUID:
		// Protocol characteristic handles gossip (and legacy handshakes)
		if d.android.messageRouter != nil {
			if err := d.android.messageRouter.HandleProtocolMessage(senderUUID, value); err != nil {
				logger.Error(prefix, "❌ Failed to handle protocol message: %v", err)
			}
		}

	case phone.AuraPhotoCharUUID:
		// Photo chunk - copy data before passing to avoid race condition
		dataCopy := make([]byte, len(value))
		copy(dataCopy, value)
		go d.android.handlePhotoChunk(senderUUID, dataCopy)

	case phone.AuraProfileCharUUID:
		// Profile message - copy data before passing
		dataCopy := make([]byte, len(value))
		copy(dataCopy, value)
		go d.android.handleProfileMessage(senderUUID, dataCopy)
	}
}

// OnDescriptorReadRequest is called when a central reads a descriptor
func (d *androidGattServerDelegate) OnDescriptorReadRequest(device *kotlin.BluetoothDevice, requestId int, offset int, descriptor *kotlin.BluetoothGattDescriptor) {
	// Not used in this implementation
}

// OnDescriptorWriteRequest is called when a central writes to a descriptor
func (d *androidGattServerDelegate) OnDescriptorWriteRequest(device *kotlin.BluetoothDevice, requestId int, descriptor *kotlin.BluetoothGattDescriptor, preparedWrite bool, responseNeeded bool, offset int, value []byte) {
	// Used for enabling/disabling notifications - handle subscribe/unsubscribe
	prefix := fmt.Sprintf("%s Android", d.android.hardwareUUID[:8])
	logger.Debug(prefix, "📥 GATT server: Descriptor write from %s", device.Address[:8])
}
