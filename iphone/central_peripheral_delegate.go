package iphone

import (
	"fmt"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
	"github.com/user/auraphone-blue/swift"
)

// ============================================================================
// CBPeripheralDelegate Implementation (for when we're Central connecting to Peripherals)
// These callbacks fire when we discover services/characteristics on remote peripherals
// ============================================================================

func (ip *IPhone) DidDiscoverServices(peripheral *swift.CBPeripheral, services []*swift.CBService, err error) {
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "❌ Service discovery failed for %s: %v", shortHash(peripheral.UUID), err)
		return
	}

	logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "📋 Services discovered for %s", shortHash(peripheral.UUID))

	// Find the Aura service
	var auraService *swift.CBService
	for _, service := range services {
		if service.UUID == phone.AuraServiceUUID {
			auraService = service
			break
		}
	}

	if auraService == nil {
		logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "❌ Aura service not found on %s", shortHash(peripheral.UUID))
		return
	}

	// In real iOS CoreBluetooth, characteristics are part of the service after discovery
	// We need to discover characteristics next, but since we're simulating, the characteristics
	// are already in the service from the GATT table. We can directly access them.

	// Subscribe to protocol characteristic for handshake, gossip, and profiles
	logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔍 Getting protocol characteristic for subscription")
	protocolChar := peripheral.GetCharacteristic(phone.AuraServiceUUID, phone.AuraProtocolCharUUID)
	if protocolChar != nil {
		logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔍 Subscribing to protocol characteristic")
		peripheral.SetNotifyValue(true, protocolChar)
	} else {
		logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "❌ Protocol characteristic not found!")
	}

	// Subscribe to photo characteristic for photo transfers
	logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔍 Getting photo characteristic for subscription")
	photoChar := peripheral.GetCharacteristic(phone.AuraServiceUUID, phone.AuraPhotoCharUUID)
	if photoChar != nil {
		logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔍 Subscribing to photo characteristic")
		peripheral.SetNotifyValue(true, photoChar)
	} else {
		logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "❌ Photo characteristic not found!")
	}

	// Now that we've subscribed to characteristics, send the handshake
	// This is the realistic flow: connect → discover services → subscribe → send data
	logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔍 Calling sendHandshake")
	ip.sendHandshake(peripheral.UUID)
	logger.Debug(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "🔍 OnServicesDiscovered complete")
}

func (ip *IPhone) DidDiscoverCharacteristics(peripheral *swift.CBPeripheral, service *swift.CBService, err error) {
	// In our simulation, characteristics are discovered along with services
	// Real iOS would require an explicit DiscoverCharacteristics call
	// We don't need this callback for our current implementation
}

func (ip *IPhone) DidDiscoverDescriptorsForCharacteristic(peripheral *swift.CBPeripheral, characteristic *swift.CBCharacteristic, err error) {
	// Called when descriptors are discovered for a characteristic
	// In our simulation, descriptors (CCCD) are auto-generated with characteristics
	// We don't need to handle this for now
}

func (ip *IPhone) DidWriteValueForCharacteristic(peripheral *swift.CBPeripheral, characteristic *swift.CBCharacteristic, err error) {
	// Called after a write operation completes
	// We don't need to handle this for now
}

func (ip *IPhone) DidWriteValueForDescriptor(peripheral *swift.CBPeripheral, descriptor *swift.CBDescriptor, err error) {
	// Called after a descriptor write operation completes (e.g., enabling notifications via CCCD)
	// We don't need to handle this for now
}

func (ip *IPhone) DidUpdateValueForCharacteristic(peripheral *swift.CBPeripheral, characteristic *swift.CBCharacteristic, err error) {
	// Called when a notification/indication is received
	// Route to appropriate handler based on characteristic UUID
	if err != nil {
		logger.Error(fmt.Sprintf("%s iOS", ip.hardwareUUID[:8]), "❌ Error receiving notification from %s: %v", shortHash(peripheral.UUID), err)
		return
	}

	// Handle based on characteristic
	if characteristic.UUID == phone.AuraProtocolCharUUID {
		// Protocol characteristic receives handshakes, gossip, profile messages, and photo requests
		ip.handleProtocolMessage(peripheral.UUID, characteristic.Value)
	} else if characteristic.UUID == phone.AuraPhotoCharUUID {
		// Photo data chunks
		ip.handlePhotoData(peripheral.UUID, characteristic.Value)
	}
}

func (ip *IPhone) DidUpdateValueForDescriptor(peripheral *swift.CBPeripheral, descriptor *swift.CBDescriptor, err error) {
	// Called when a descriptor value is read
	// We don't need to handle this for now
}
