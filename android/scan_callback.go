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
	peripheralUUID := result.Device.Address // Peripheral UUID for BLE routing only

	a.mu.Lock()

	// Check if already discovered
	if _, exists := a.discovered[peripheralUUID]; exists {
		a.mu.Unlock()
		return
	}

	// Try to extract Base36 device ID from advertisement (device name in Android)
	advertisedDeviceID := result.Device.Name

	logger.Info(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "📡 Discovered: %s (RSSI: %d, advertised ID: %s)",
		result.Device.Name, result.Rssi, advertisedDeviceID)

	// Store discovered device (will be updated with Base36 ID after handshake if not in advertisement)
	device := phone.DiscoveredDevice{
		PeripheralUUID: peripheralUUID,
		DeviceID:       advertisedDeviceID, // May be empty until handshake
		Name:           result.Device.Name,
		RSSI:           float64(result.Rssi),
	}
	a.discovered[peripheralUUID] = device

	// Check if we're already connected (e.g., they connected to us as Peripheral)
	// Real Android BLE: Don't initiate a new connection if already connected
	alreadyConnected := a.identityManager.IsConnected(peripheralUUID)

	// ROLE POLICY: Apply role policy based on Base36 device ID if available in advertisement
	// Otherwise, connect anyway to get Base36 ID via handshake (quick connect pattern)
	var shouldConnect bool
	if !alreadyConnected {
		if advertisedDeviceID != "" {
			// We have the Base36 device ID from advertisement - apply role policy now
			// Validate it's a proper Base36 ID (6-16 chars, alphanumeric)
			isValidBase36 := len(advertisedDeviceID) >= 6 && len(advertisedDeviceID) <= 16

			if isValidBase36 {
				// Filter out self (should not connect to ourselves)
				if advertisedDeviceID == a.deviceID {
					a.mu.Unlock()
					logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "⏭️  Skipping self-discovery: %s", advertisedDeviceID)
					return
				}

				// ROLE POLICY: Device with LARGER Base36 ID initiates connection
				shouldConnect = a.shouldInitiateConnection(advertisedDeviceID)

				if !shouldConnect {
					// We should NOT initiate - they will connect to us
					a.mu.Unlock()
					logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]),
						"🔀 Role policy: Waiting for %s to connect (our ID: %s > their ID: %s = %v)",
						advertisedDeviceID, a.deviceID, advertisedDeviceID, a.deviceID > advertisedDeviceID)

					// Still call callback so device appears in GUI
					if a.callback != nil {
						a.callback(device)
					}
					return
				}
			}
		}

		// Either no device ID in advertisement, invalid format, or we won the role policy
		shouldConnect = true
	}

	// Unlock BEFORE calling callback and connectToDevice to avoid deadlock
	// (callbacks may trigger other operations that need the mutex)
	a.mu.Unlock()

	// Call callback
	if a.callback != nil {
		a.callback(device)
	}

	// Decide if we should initiate connection based on role negotiation
	if shouldConnect {
		if advertisedDeviceID != "" {
			logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]),
				"🔌 Initiating connection to %s (%s) - role: Central",
				advertisedDeviceID, shortHash(peripheralUUID))
		} else {
			logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]),
				"🔌 Initiating connection to %s (will identify via handshake) - role: Central",
				shortHash(peripheralUUID))
		}

		// Connect
		a.connectToDevice(peripheralUUID)
	} else {
		// Don't log if already connected (to avoid log spam)
		if !alreadyConnected {
			logger.Debug(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "⏳ Waiting for %s to connect (role: Peripheral)", shortHash(peripheralUUID))
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
	// Reserve the slot with nil to prevent race condition where multiple threads
	// try to connect before the GATT object is stored
	// REALISTIC: Real Android apps track pending connections to avoid duplicates
	a.connectedGatts[peerUUID] = nil
	a.mu.Unlock()

	// Get remote device
	device := a.manager.Adapter.GetRemoteDevice(peerUUID)
	if device == nil {
		logger.Error(fmt.Sprintf("%s Android", a.hardwareUUID[:8]), "Failed to get remote device %s", shortHash(peerUUID))
		// Clean up reservation on failure
		a.mu.Lock()
		delete(a.connectedGatts, peerUUID)
		a.mu.Unlock()
		return
	}

	// Connect with autoConnect=false (manual reconnect, can be changed to true for iOS-like auto-reconnect)
	gatt := device.ConnectGatt(nil, false, a)

	// Store GATT connection (replace nil reservation with actual gatt)
	a.mu.Lock()
	a.connectedGatts[peerUUID] = gatt
	a.mu.Unlock()
}
