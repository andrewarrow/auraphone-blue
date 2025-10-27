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
	defer ip.mu.Unlock()

	// Check if already discovered
	if _, exists := ip.discovered[peripheral.UUID]; exists {
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

	// Call callback
	if ip.callback != nil {
		ip.callback(device)
	}

	// Decide if we should initiate connection based on role negotiation
	if ip.central.ShouldInitiateConnection(peripheral.UUID) {
		logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔌 Initiating connection to %s (role: Central)", shortHash(peripheral.UUID))

		// Store peripheral for message routing
		peripheralObj := &peripheral
		ip.connectedPeers[peripheral.UUID] = peripheralObj

		// Connect
		ip.central.Connect(peripheralObj, nil)
	} else {
		logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "⏳ Waiting for %s to connect (role: Peripheral)", shortHash(peripheral.UUID))
	}
}

func (ip *IPhone) DidConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral) {
	logger.Info(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "✅ Connected to %s", shortHash(peripheral.UUID))

	// Mark as connected in IdentityManager (tracks connection state by hardware UUID)
	ip.identityManager.MarkConnected(peripheral.UUID)

	// Send handshake
	ip.sendHandshake(peripheral.UUID)
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

	ip.mu.Lock()
	delete(ip.handshaked, peripheral.UUID)
	delete(ip.connectedPeers, peripheral.UUID)
	ip.mu.Unlock()
}
