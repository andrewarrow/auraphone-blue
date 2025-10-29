package iphone

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/swift"
)

// ============================================================================
// CBCentralManagerDelegate Implementation (Central role - we're scanning/connecting)
// ============================================================================

func (ip *IPhone) DidUpdateCentralState(central swift.CBCentralManager) {
	logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "Central manager state: %s", central.State)
}

func (ip *IPhone) DidDiscoverPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, advertisementData map[string]interface{}, rssi float64) {
	ip.mu.Lock()

	// Check if already discovered
	if _, exists := ip.discovered[peripheral.UUID]; exists {
		ip.mu.Unlock()
		return
	}

	logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "📡 Discovered: %s (RSSI: %.0f)", peripheral.Name, rssi)

	// Store discovered device
	device := phone.DiscoveredDevice{
		HardwareUUID: peripheral.UUID,
		Name:         peripheral.Name,
		RSSI:         rssi,
	}
	ip.discovered[peripheral.UUID] = device

	// Store peripheral for message routing (if we're going to connect)
	var shouldConnect bool
	var peripheralObj *swift.CBPeripheral

	// Check if we're already connected (e.g., they connected to us as Peripheral)
	// Real iOS CoreBluetooth: Don't initiate a new connection if already connected
	alreadyConnected := ip.identityManager.IsConnected(peripheral.UUID)

	if !alreadyConnected && ip.central.ShouldInitiateConnection(peripheral.UUID) {
		peripheralObj = &peripheral
		ip.connectedPeers[peripheral.UUID] = peripheralObj
		shouldConnect = true
	}

	// Unlock BEFORE calling callback and Connect to avoid deadlock
	// (callbacks may trigger other operations that need the mutex)
	ip.mu.Unlock()

	// Call callback
	if ip.callback != nil {
		ip.callback(device)
	}

	// Decide if we should initiate connection based on role negotiation
	if shouldConnect {
		logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔌 Initiating connection to %s (role: Central)", shortHash(peripheral.UUID))

		// Connect
		ip.central.Connect(peripheralObj, nil)
	} else {
		// Don't log if already connected (to avoid log spam)
		if !alreadyConnected {
			logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "⏳ Waiting for %s to connect (role: Peripheral)", shortHash(peripheral.UUID))
		}
	}
}

func (ip *IPhone) DidConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral) {
	logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "✅ Connected to %s", shortHash(peripheral.UUID))

	// Mark as connected in IdentityManager (tracks connection state by hardware UUID)
	ip.identityManager.MarkConnected(peripheral.UUID)

	// Get the peripheral object from our map so we can set delegate and discover services
	ip.mu.Lock()
	peripheralPtr, exists := ip.connectedPeers[peripheral.UUID]
	ip.mu.Unlock()

	if !exists {
		logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "❌ Peripheral %s not found in connectedPeers", shortHash(peripheral.UUID))
		return
	}

	// Set ourselves as the delegate to receive service discovery callbacks
	// This is realistic iOS behavior - delegate must be set before discovering services
	peripheralPtr.Delegate = ip

	// Discover services - this is async, will callback to DidDiscoverServices
	// In real iOS CoreBluetooth, you must discover services before you can subscribe to characteristics
	logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔍 Discovering services for %s", shortHash(peripheral.UUID))
	peripheralPtr.DiscoverServices([]string{phone.AuraServiceUUID})
}

func (ip *IPhone) DidFailToConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, err error) {
	logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "❌ Failed to connect to %s: %v", shortHash(peripheral.UUID), err)
}

func (ip *IPhone) DidDisconnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, err error) {
	logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔌 Disconnected from %s", shortHash(peripheral.UUID))

	// Mark as disconnected in IdentityManager
	ip.identityManager.MarkDisconnected(peripheral.UUID)

	// Mark as disconnected in mesh view
	if deviceID, exists := ip.identityManager.GetDeviceID(peripheral.UUID); exists {
		ip.meshView.MarkDeviceDisconnected(deviceID)
	}

	// Clear handshake state (needs to be re-exchanged on reconnect)
	ip.mu.Lock()
	delete(ip.handshaked, peripheral.UUID)
	ip.mu.Unlock()

	// REALISTIC iOS BEHAVIOR: Do NOT remove peripheral from connectedPeers
	// In real iOS CoreBluetooth, when you call central.Connect(peripheral, nil),
	// iOS remembers that peripheral and will auto-reconnect in the background.
	// The CBPeripheral object reference persists across disconnects.
	// The iOS auto-reconnect feature will call DidConnectPeripheral again when
	// the connection is restored, using the same peripheral object.
	// We track connection state separately via identityManager.IsConnected()
}
