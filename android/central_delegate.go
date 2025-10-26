package android

import (
	"fmt"

	"github.com/user/auraphone-blue/kotlin"
	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
)

// Central mode BLE callbacks (Scanner + GATT Client)

// OnScanResult is called when a BLE device is discovered during scanning
func (a *Android) OnScanResult(callbackType int, result *kotlin.ScanResult) {
	name := result.Device.Name
	if result.ScanRecord != nil && result.ScanRecord.DeviceName != "" {
		name = result.ScanRecord.DeviceName
	}

	rssi := float64(result.Rssi)

	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
	logger.Debug(prefix, "📱 DISCOVERED device %s (%s)", result.Device.Address[:8], name)
	logger.Debug(prefix, "   └─ RSSI: %.0f dBm", rssi)

	// Store discovered device for potential reconnect
	a.mu.Lock()
	a.discoveredDevices[result.Device.Address] = result.Device
	_, alreadyConnected := a.connectedGatts[result.Device.Address]
	a.mu.Unlock()

	// Trigger discovery callback immediately with hardware UUID
	// This ensures the device shows up in GUI right away
	// Will be updated later with logical base36 deviceID after handshake
	if a.discoveryCallback != nil {
		a.discoveryCallback(phone.DiscoveredDevice{
			DeviceID:     "", // Will be filled in after handshake
			HardwareUUID: result.Device.Address,
			Name:         name,
			RSSI:         rssi,
			Platform:     "unknown", // Will be updated after handshake
			PhotoHash:    "",        // Will be filled in after handshake
		})
	}

	// Auto-connect if not already connected AND we should act as Central
	if !alreadyConnected {
		// Check if we should act as Central for this device using role negotiation
		if a.shouldActAsCentral(result.Device.Address, name) {
			logger.Debug(prefix, "🔌 Connecting to %s (acting as Central, autoConnect=%v)", result.Device.Address[:8], a.useAutoConnect)
			gatt := result.Device.ConnectGatt(nil, a.useAutoConnect, a)
			a.mu.Lock()
			a.connectedGatts[result.Device.Address] = gatt
			a.mu.Unlock()
		} else {
			logger.Debug(prefix, "⏸️  Not connecting to %s (will act as Peripheral, waiting for them to connect)", result.Device.Address[:8])
		}
	}
}

// shouldActAsCentral determines if this Android device should initiate connection to a discovered device
// Returns true if we should connect (act as Central), false if we should wait (act as Peripheral)
// Uses simple hardware UUID comparison regardless of remote platform
func (a *Android) shouldActAsCentral(remoteUUID, remoteName string) bool {
	// Use hardware UUID comparison for all devices
	// Device with LARGER UUID acts as Central (deterministic collision avoidance)
	return a.hardwareUUID > remoteUUID
}

// BluetoothGattCallback methods (GATT Client callbacks)

// OnConnectionStateChange handles connection state changes
func (a *Android) OnConnectionStateChange(gatt *kotlin.BluetoothGatt, status int, newState int) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
	remoteUUID := gatt.GetRemoteUUID()

	if newState == 2 { // STATE_CONNECTED
		logger.Info(prefix, "✅ Connected to device %s", remoteUUID[:8])

		// Register connection with ConnectionManager
		if a.connManager != nil {
			a.connManager.RegisterCentralConnection(remoteUUID, gatt)
		}

		// Re-add to connected list (might have been removed on disconnect)
		a.mu.Lock()
		a.connectedGatts[remoteUUID] = gatt
		a.mu.Unlock()

		// Discover services (gossip will be sent in OnServicesDiscovered)
		gatt.DiscoverServices()

	} else if newState == 0 { // STATE_DISCONNECTED
		if status != 0 {
			logger.Error(prefix, "❌ Disconnected from %s with error (status=%d)", remoteUUID[:8], status)
		} else {
			logger.Info(prefix, "📡 Disconnected from %s (interference/distance)", remoteUUID[:8])
		}

		// Get device ID before cleanup
		a.mu.Lock()
		deviceID := a.remoteUUIDToDeviceID[remoteUUID]
		a.mu.Unlock()

		// Clean up photo transfer state for disconnected device
		if deviceID != "" {
			a.photoCoordinator.CleanupDisconnectedDevice(deviceID)
		}

		// Unregister connection with ConnectionManager
		if a.connManager != nil {
			a.connManager.UnregisterCentralConnection(remoteUUID)
		}

		// Stop listening and write queue on the gatt
		gatt.StopListening()
		gatt.StopWriteQueue()

		// Remove from connected list
		a.mu.Lock()
		if _, exists := a.connectedGatts[remoteUUID]; exists {
			delete(a.connectedGatts, remoteUUID)
		}
		a.mu.Unlock()

		// Android reconnect behavior depends on autoConnect parameter:
		// - If autoConnect=true: BluetoothGatt will retry automatically in background
		// - If autoConnect=false: App must manually call connectGatt() again
		if a.useAutoConnect {
			logger.Info(prefix, "🔄 Android autoConnect=true: Will retry in background...")
			// Auto-reconnect is handled by BluetoothGatt.attemptReconnect() in kotlin layer
		} else {
			logger.Warn(prefix, "❌ Android autoConnect=false: App must manually reconnect (disconnected from %s)", remoteUUID[:8])
			// For manual reconnection, implement reconnect logic here or in manualReconnect()
		}
	}
}

// OnServicesDiscovered is called when GATT services are discovered
func (a *Android) OnServicesDiscovered(gatt *kotlin.BluetoothGatt, status int) {
	prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])

	if status != 0 { // GATT_SUCCESS = 0
		logger.Error(prefix, "❌ Service discovery failed")
		return
	}

	logger.Debug(prefix, "🔍 Discovered %d services", len(gatt.GetServices()))

	// Enable notifications for characteristics (matches real Android behavior)
	protocolChar := gatt.GetCharacteristic(phone.AuraServiceUUID, phone.AuraProtocolCharUUID)
	if protocolChar != nil {
		if !gatt.SetCharacteristicNotification(protocolChar, true) {
			logger.Error(prefix, "❌ Failed to enable notifications for protocol characteristic")
		}
	}

	photoChar := gatt.GetCharacteristic(phone.AuraServiceUUID, phone.AuraPhotoCharUUID)
	if photoChar != nil {
		if !gatt.SetCharacteristicNotification(photoChar, true) {
			logger.Error(prefix, "❌ Failed to enable notifications for photo characteristic")
		}
	}

	profileChar := gatt.GetCharacteristic(phone.AuraServiceUUID, phone.AuraProfileCharUUID)
	if profileChar != nil {
		if !gatt.SetCharacteristicNotification(profileChar, true) {
			logger.Error(prefix, "❌ Failed to enable notifications for profile characteristic")
		}
	}

	// Start write queue for async writes (matches real Android behavior)
	gatt.StartWriteQueue()

	// Start listening for notifications
	gatt.StartListening()

	// Send initial gossip after service discovery completes
	remoteUUID := gatt.GetRemoteUUID()
	go a.gossipHandler.SendGossipToDevice(remoteUUID)
}

// OnCharacteristicChanged is called when a notification/indication is received
func (a *Android) OnCharacteristicChanged(gatt *kotlin.BluetoothGatt, characteristic *kotlin.BluetoothGattCharacteristic) {
	remoteUUID := gatt.GetRemoteUUID()

	// Handle based on characteristic type
	if characteristic.UUID == phone.AuraProtocolCharUUID {
		// Protocol characteristic handles gossip (and legacy handshakes)
		if a.messageRouter != nil {
			if err := a.messageRouter.HandleProtocolMessage(remoteUUID, characteristic.Value); err != nil {
				prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
				logger.Error(prefix, "❌ Failed to handle protocol message: %v", err)
			}
		}
	} else if characteristic.UUID == phone.AuraPhotoCharUUID {
		// Photo characteristic is for photo chunks
		a.photoHandler.HandlePhotoChunk(remoteUUID, characteristic.Value)
	} else if characteristic.UUID == phone.AuraProfileCharUUID {
		// Profile characteristic is for ProfileMessage
		a.profileHandler.HandleProfileMessage(remoteUUID, characteristic.Value)
	}
}

// OnCharacteristicWrite is called when a write operation completes
func (a *Android) OnCharacteristicWrite(gatt *kotlin.BluetoothGatt, characteristic *kotlin.BluetoothGattCharacteristic, status int) {
	if status != 0 { // GATT_SUCCESS = 0
		prefix := fmt.Sprintf("%s Android", a.hardwareUUID[:8])
		logger.Error(prefix, "❌ Write failed for characteristic %s", characteristic.UUID[:8])
	}
}

// OnCharacteristicRead is called when a read operation completes
func (a *Android) OnCharacteristicRead(gatt *kotlin.BluetoothGatt, characteristic *kotlin.BluetoothGattCharacteristic, status int) {
	// Not used in this implementation
}
