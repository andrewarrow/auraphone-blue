package iphone

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/swift"
)

// CBCentralManagerDelegate methods for Central mode (scanning and connecting)

func (ip *iPhone) DidUpdateState(central swift.CBCentralManager) {
	// State updated
}

func (ip *iPhone) DidDiscoverPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, advertisementData map[string]interface{}, rssi float64) {
	name := peripheral.Name
	if advName, ok := advertisementData["kCBAdvDataLocalName"].(string); ok {
		name = advName
	}

	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
	logger.Debug(prefix, "📱 Discovered %s (%s) RSSI: %.0f dBm", peripheral.UUID[:8], name, rssi)

	// Trigger discovery callback (shows device in GUI immediately)
	if ip.discoveryCallback != nil {
		ip.discoveryCallback(phone.DiscoveredDevice{
			DeviceID:     "", // Will be filled after gossip exchange
			HardwareUUID: peripheral.UUID,
			Name:         name,
			RSSI:         rssi,
			Platform:     "unknown",
			PhotoHash:    "",
		})
	}

	// Auto-connect if we should act as Central (role negotiation)
	ip.mu.RLock()
	_, alreadyConnected := ip.connectedPeripherals[peripheral.UUID]
	ip.mu.RUnlock()

	if !alreadyConnected && ip.shouldActAsCentral(peripheral.UUID, name) {
		logger.Debug(prefix, "🔌 Connecting to %s (acting as Central)", peripheral.UUID[:8])
		ip.manager.Connect(&peripheral, nil)
	} else if alreadyConnected {
		logger.Debug(prefix, "⏭️  Already connected to %s", peripheral.UUID[:8])
	} else {
		logger.Debug(prefix, "⏸️  Not connecting to %s (will act as Peripheral)", peripheral.UUID[:8])
	}
}

func (ip *iPhone) DidConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral) {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
	logger.Info(prefix, "✅ Connected to %s (Central mode)", peripheral.UUID[:8])

	// Store peripheral and register with connection manager
	// IMPORTANT: Store a copy in the map first, then work with the stored pointer
	// to avoid issues with the peripheral being passed by value
	ip.mu.Lock()
	ip.connectedPeripherals[peripheral.UUID] = &peripheral
	storedPeripheral := ip.connectedPeripherals[peripheral.UUID]
	ip.mu.Unlock()

	ip.connManager.RegisterCentralConnection(peripheral.UUID, storedPeripheral)

	// Mark device as connected in identity manager (connection manager will do this too, but this is explicit)
	ip.identityManager.MarkConnected(peripheral.UUID)

	// NEW (Week 3): Mark device as connected in mesh view
	if deviceID, ok := ip.identityManager.GetDeviceID(peripheral.UUID); ok {
		ip.meshView.MarkDeviceConnected(deviceID)

		// Resume any paused photo transfers
		hasSend, hasRecv := ip.photoCoordinator.ResumeTransfersOnReconnect(deviceID)
		if hasSend || hasRecv {
			logger.Info(prefix, "🔄 Reconnected to %s - will resume incomplete transfers", deviceID)
		}
	}

	// Set delegate and discover services on the stored peripheral
	storedPeripheral.Delegate = ip
	storedPeripheral.DiscoverServices([]string{phone.AuraServiceUUID})

	// Start listening for notifications from this peripheral
	// Use the stored pointer, not the local copy
	storedPeripheral.StartListening()

	// Retry any pending photo/profile requests for devices reachable via this connection
	// This handles race condition where gossip arrives before connection completes
	go ip.messageRouter.RetryMissingRequestsForConnection(peripheral.UUID)
}

func (ip *iPhone) DidFailToConnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, err error) {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
	logger.Error(prefix, "❌ Failed to connect to %s: %v", peripheral.UUID[:8], err)
}

func (ip *iPhone) DidDisconnectPeripheral(central swift.CBCentralManager, peripheral swift.CBPeripheral, err error) {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
	if err != nil {
		logger.Error(prefix, "❌ Disconnected from %s: %v", peripheral.UUID[:8], err)
	} else {
		logger.Info(prefix, "📡 Disconnected from %s", peripheral.UUID[:8])
	}

	// Get device ID before cleanup
	deviceID, _ := ip.identityManager.GetDeviceID(peripheral.UUID)

	// Mark device as disconnected in identity manager
	ip.identityManager.MarkDisconnected(peripheral.UUID)

	// NEW (Week 3): Mark device as disconnected in mesh view
	if deviceID != "" {
		ip.meshView.MarkDeviceDisconnected(deviceID)
	}

	// Clean up photo transfer state for disconnected device
	if deviceID != "" {
		ip.photoCoordinator.CleanupDisconnectedDevice(deviceID)
	}

	// Unregister from connection manager
	ip.connManager.UnregisterCentralConnection(peripheral.UUID)

	// Clean up peripheral
	ip.mu.Lock()
	if storedPeripheral, exists := ip.connectedPeripherals[peripheral.UUID]; exists {
		storedPeripheral.StopListening()
		storedPeripheral.StopWriteQueue()
		delete(ip.connectedPeripherals, peripheral.UUID)
	}
	ip.mu.Unlock()

	// iOS auto-reconnect: CBCentralManager will retry automatically
	logger.Info(prefix, "🔄 iOS will auto-reconnect to %s...", peripheral.UUID[:8])
}

// CBPeripheralDelegate methods for characteristic operations

func (ip *iPhone) DidDiscoverServices(peripheral *swift.CBPeripheral, services []*swift.CBService, err error) {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
	if err != nil {
		logger.Error(prefix, "❌ Service discovery failed for %s: %v", peripheral.UUID[:8], err)
		return
	}

	// Find Aura service and discover characteristics
	for _, service := range services {
		if service.UUID == phone.AuraServiceUUID {
			logger.Debug(prefix, "🔍 Discovering characteristics for %s", peripheral.UUID[:8])
			peripheral.DiscoverCharacteristics([]string{
				phone.AuraProtocolCharUUID,
				phone.AuraPhotoCharUUID,
				phone.AuraProfileCharUUID,
			}, service)
			break
		}
	}
}

func (ip *iPhone) DidDiscoverCharacteristics(peripheral *swift.CBPeripheral, service *swift.CBService, err error) {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
	if err != nil {
		logger.Error(prefix, "❌ Characteristic discovery failed: %v", err)
		return
	}

	logger.Debug(prefix, "✅ Discovered %d characteristics for %s", len(service.Characteristics), peripheral.UUID[:8])

	// Subscribe to all characteristics for notifications
	for _, char := range service.Characteristics {
		hasNotify := false
		for _, prop := range char.Properties {
			if prop == "notify" || prop == "indicate" {
				hasNotify = true
				break
			}
		}
		if hasNotify {
			logger.Debug(prefix, "🔔 Subscribing to notifications on %s", char.UUID[:8])
			peripheral.SetNotifyValue(true, char)
		}
	}

	// Send initial gossip after characteristics are discovered
	go ip.gossipHandler.SendGossipToDevice(peripheral.UUID)
}

func (ip *iPhone) DidWriteValueForCharacteristic(peripheral *swift.CBPeripheral, characteristic *swift.CBCharacteristic, err error) {
	if err != nil {
		prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])
		logger.Error(prefix, "❌ Write failed to %s: %v", peripheral.UUID[:8], err)
	}
}

func (ip *iPhone) DidUpdateValueForCharacteristic(peripheral *swift.CBPeripheral, characteristic *swift.CBCharacteristic, err error) {
	prefix := fmt.Sprintf("%s iOS", ip.hardwareUUID[:8])

	logger.Debug(prefix,
		"📥 RECEIVED DATA: char=%s bytes=%d from_peripheral=%s",
		characteristic.UUID[len(characteristic.UUID)-4:], len(characteristic.Value), peripheral.UUID[:8])

	if err != nil {
		logger.Error(prefix, "❌ Characteristic update error from %s: %v", peripheral.UUID[:8], err)
		return
	}

	// Route message based on characteristic UUID
	switch characteristic.UUID {
	case phone.AuraProtocolCharUUID:
		// Gossip, photo request, or profile request
		logger.Debug(prefix, "📥 RX Protocol message from %s (%d bytes)", peripheral.UUID[:8], len(characteristic.Value))
		if err := ip.messageRouter.HandleProtocolMessage(peripheral.UUID, characteristic.Value); err != nil {
			logger.Error(prefix, "❌ Failed to handle protocol message from %s: %v", peripheral.UUID[:8], err)
		}

	case phone.AuraPhotoCharUUID:
		// Photo chunk data
		logger.Debug(prefix, "📥 RX Photo chunk from %s (%d bytes)", peripheral.UUID[:8], len(characteristic.Value))
		ip.photoHandler.HandlePhotoChunk(peripheral.UUID, characteristic.Value)

	case phone.AuraProfileCharUUID:
		// Profile message
		logger.Debug(prefix, "📥 RX Profile message from %s (%d bytes)", peripheral.UUID[:8], len(characteristic.Value))
		ip.profileHandler.HandleProfileMessage(peripheral.UUID, characteristic.Value)
	}
}

// sendViaCentralMode sends data to a device we're connected to as Central
func (ip *iPhone) sendViaCentralMode(remoteUUID, charUUID string, data []byte) error {
	ip.mu.RLock()
	peripheral, exists := ip.connectedPeripherals[remoteUUID]
	ip.mu.RUnlock()

	if !exists {
		return fmt.Errorf("not connected as central to %s", remoteUUID[:8])
	}

	char := peripheral.GetCharacteristic(phone.AuraServiceUUID, charUUID)
	if char == nil {
		return fmt.Errorf("characteristic %s not found", charUUID[:8])
	}

	return peripheral.WriteValue(data, char, swift.CBCharacteristicWriteWithResponse)
}
