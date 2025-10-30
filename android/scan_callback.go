package android

import (
	"fmt"

	"github.com/user/auraphone-blue/kotlin"
	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
)

// ============================================================================
// ScanCallback Implementation (implements kotlin.ScanCallback interface)
// ============================================================================

func (a *Android) OnScanResult(callbackType int, result *kotlin.ScanResult) {
	a.mu.Lock()

	// Check if already discovered
	if _, exists := a.discovered[result.Device.Address]; exists {
		a.mu.Unlock()
		return
	}

	logger.Info(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "📡 Discovered: %s (RSSI: %d)", result.Device.Name, result.Rssi)

	// Store discovered device
	device := phone.DiscoveredDevice{
		HardwareUUID: result.Device.Address,
		Name:         result.Device.Name,
		RSSI:         float64(result.Rssi),
	}
	a.discovered[result.Device.Address] = device

	// Unlock BEFORE calling callback and connectToDevice to avoid deadlock
	// (callbacks may trigger other operations that need the mutex)
	a.mu.Unlock()

	// Call callback
	if a.callback != nil {
		a.callback(device)
	}

	// Check if we're already connected (e.g., they connected to us as Peripheral)
	// Real Android BLE: Don't initiate a new connection if already connected
	alreadyConnected := a.identityManager.IsConnected(result.Device.Address)

	// Decide if we should initiate connection based on role negotiation
	// IMPORTANT: Android CAN discover other Android devices (unlike iOS which blocks iOS-to-iOS discovery)
	if !alreadyConnected && a.manager.Adapter.ShouldInitiateConnection(result.Device.Address) {
		logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "🔌 Initiating connection to %s (role: Central)", shortHash(result.Device.Address))

		// Connect
		a.connectToDevice(result.Device.Address)
	} else {
		// Don't log if already connected (to avoid log spam)
		if !alreadyConnected {
			logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "⏳ Waiting for %s to connect (role: Peripheral)", shortHash(result.Device.Address))
		}
	}
}

// connectToDevice creates a GATT connection to the remote device
func (a *Android) connectToDevice(peerUUID string) {
	// REALISTIC: Check if we already have a GATT connection (in progress or completed)
	// Real Android: Apps must track BluetoothGatt objects to avoid duplicate connectGatt() calls
	// Calling connectGatt() twice on the same device causes connection conflicts
	a.mu.Lock()
	if _, exists := a.connectedGatts[peerUUID]; exists {
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()

	// Get remote device
	device := a.manager.Adapter.GetRemoteDevice(peerUUID)
	if device == nil {
		logger.Error(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "Failed to get remote device %s", shortHash(peerUUID))
		return
	}

	// Connect with autoConnect=false (manual reconnect, can be changed to true for iOS-like auto-reconnect)
	gatt := device.ConnectGatt(nil, false, a)

	// Store GATT connection
	a.mu.Lock()
	a.connectedGatts[peerUUID] = gatt
	a.mu.Unlock()
}
