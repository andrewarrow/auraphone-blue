package wire

import (
	"fmt"
	"time"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/wire/att"
	"github.com/user/auraphone-blue/wire/gatt"
)

// DiscoverServices initiates GATT service discovery on a connected peer
// This sends a Read By Group Type Request to discover all primary services
// Results are stored in the connection's discovery cache and can be retrieved with GetDiscoveredServices()
func (w *Wire) DiscoverServices(peerUUID string) error {
	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if !exists {
		return fmt.Errorf("not connected to %s", peerUUID)
	}

	logger.Debug(shortHash(w.hardwareUUID)+" Wire",
		"🔍 Discovering services from %s", shortHash(peerUUID))

	// Simulate realistic discovery delay (iOS/Android: 100-500ms)
	time.Sleep(randomDelay(100*time.Millisecond, 500*time.Millisecond))

	// Clear existing services in discovery cache before starting fresh discovery
	cache := connection.discoveryCache.(*gatt.DiscoveryCache)
	cache.Services = []gatt.DiscoveredService{}

	// Send Read By Group Type Request for primary services
	// Start: 0x0001, End: 0xFFFF, Type: 0x2800 (Primary Service UUID)
	tracker := connection.requestTracker.(*att.RequestTracker)
	responseC, err := tracker.StartRequest(att.OpReadByGroupTypeRequest, 0x0001, 0)
	if err != nil {
		return fmt.Errorf("failed to start service discovery: %w", err)
	}

	req := &att.ReadByGroupTypeRequest{
		StartHandle: 0x0001,
		EndHandle:   0xFFFF,
		Type:        []byte{0x00, 0x28}, // Primary Service UUID (0x2800)
	}

	err = w.sendATTPacket(peerUUID, req)
	if err != nil {
		tracker.FailRequest(err)
		return fmt.Errorf("failed to send service discovery request: %w", err)
	}

	// Wait for response
	select {
	case resp := <-responseC:
		if resp.Error != nil {
			return fmt.Errorf("service discovery failed: %w", resp.Error)
		}
		// Services are already stored in discovery cache by handleATTPacket
		logger.Debug(shortHash(w.hardwareUUID)+" Wire",
			"✅ Service discovery complete: %d services found", len(cache.Services))
		return nil
	case <-time.After(30 * time.Second):
		return fmt.Errorf("service discovery timeout")
	}
}

// DiscoverCharacteristics initiates characteristic discovery for a specific service
// This sends a Read By Type Request to discover all characteristics within the service
// Results are stored in the connection's discovery cache
func (w *Wire) DiscoverCharacteristics(peerUUID string, serviceUUID []byte) error {
	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if !exists {
		return fmt.Errorf("not connected to %s", peerUUID)
	}

	// Find the service in the discovery cache
	cache := connection.discoveryCache.(*gatt.DiscoveryCache)
	var service *gatt.DiscoveredService
	for i := range cache.Services {
		if bytesEqual(cache.Services[i].UUID, serviceUUID) {
			service = &cache.Services[i]
			break
		}
	}

	if service == nil {
		return fmt.Errorf("service not found in discovery cache, run DiscoverServices first")
	}

	logger.Debug(shortHash(w.hardwareUUID)+" Wire",
		"🔍 Discovering characteristics from %s for service 0x%04X-0x%04X",
		shortHash(peerUUID), service.StartHandle, service.EndHandle)

	// Simulate realistic discovery delay (iOS/Android: 100-500ms)
	time.Sleep(randomDelay(100*time.Millisecond, 500*time.Millisecond))

	// Send Read By Type Request for characteristics
	// Type: 0x2803 (Characteristic Declaration UUID)
	tracker := connection.requestTracker.(*att.RequestTracker)
	responseC, err := tracker.StartRequest(att.OpReadByTypeRequest, service.StartHandle, 0)
	if err != nil {
		return fmt.Errorf("failed to start characteristic discovery: %w", err)
	}

	req := &att.ReadByTypeRequest{
		StartHandle: service.StartHandle,
		EndHandle:   service.EndHandle,
		Type:        []byte{0x03, 0x28}, // Characteristic Declaration UUID (0x2803)
	}

	err = w.sendATTPacket(peerUUID, req)
	if err != nil {
		tracker.FailRequest(err)
		return fmt.Errorf("failed to send characteristic discovery request: %w", err)
	}

	// Wait for response
	select {
	case resp := <-responseC:
		if resp.Error != nil {
			return fmt.Errorf("characteristic discovery failed: %w", resp.Error)
		}
		// Characteristics are already stored in discovery cache by handleATTPacket
		chars := cache.Characteristics[service.StartHandle]
		logger.Debug(shortHash(w.hardwareUUID)+" Wire",
			"✅ Characteristic discovery complete: %d characteristics found", len(chars))
		return nil
	case <-time.After(30 * time.Second):
		return fmt.Errorf("characteristic discovery timeout")
	}
}

// DiscoverDescriptors initiates descriptor discovery for a specific characteristic
// This sends a Find Information Request to discover all descriptors for the characteristic
// Results are stored in the connection's discovery cache
func (w *Wire) DiscoverDescriptors(peerUUID string, charHandle uint16) error {
	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if !exists {
		return fmt.Errorf("not connected to %s", peerUUID)
	}

	// Find the characteristic in the discovery cache to determine handle range
	cache := connection.discoveryCache.(*gatt.DiscoveryCache)
	var startHandle, endHandle uint16

	// Find the characteristic and determine its descriptor range
	// Descriptors are stored between the characteristic value handle + 1 and the next characteristic
	found := false
	for _, chars := range cache.Characteristics {
		for i, char := range chars {
			if char.ValueHandle == charHandle {
				startHandle = char.ValueHandle + 1
				// End handle is either the next characteristic or 0xFFFF
				if i+1 < len(chars) {
					endHandle = chars[i+1].DeclarationHandle - 1
				} else {
					endHandle = 0xFFFF
				}
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		return fmt.Errorf("characteristic handle 0x%04X not found in discovery cache", charHandle)
	}

	if startHandle > endHandle {
		// No descriptors for this characteristic
		logger.Debug(shortHash(w.hardwareUUID)+" Wire",
			"✅ Descriptor discovery complete: 0 descriptors (no descriptor range)")
		return nil
	}

	logger.Debug(shortHash(w.hardwareUUID)+" Wire",
		"🔍 Discovering descriptors from %s for characteristic 0x%04X (range 0x%04X-0x%04X)",
		shortHash(peerUUID), charHandle, startHandle, endHandle)

	// Simulate realistic discovery delay (iOS/Android: 100-500ms)
	time.Sleep(randomDelay(100*time.Millisecond, 500*time.Millisecond))

	// Send Find Information Request for descriptors
	tracker := connection.requestTracker.(*att.RequestTracker)
	responseC, err := tracker.StartRequest(att.OpFindInformationRequest, startHandle, 0)
	if err != nil {
		return fmt.Errorf("failed to start descriptor discovery: %w", err)
	}

	req := &att.FindInformationRequest{
		StartHandle: startHandle,
		EndHandle:   endHandle,
	}

	err = w.sendATTPacket(peerUUID, req)
	if err != nil {
		tracker.FailRequest(err)
		return fmt.Errorf("failed to send descriptor discovery request: %w", err)
	}

	// Wait for response
	select {
	case resp := <-responseC:
		if resp.Error != nil {
			// Attribute Not Found is OK - means no descriptors
			if attErr, ok := resp.Error.(*att.Error); ok && attErr.Code == att.ErrAttributeNotFound {
				logger.Debug(shortHash(w.hardwareUUID)+" Wire",
					"✅ Descriptor discovery complete: 0 descriptors")
				return nil
			}
			return fmt.Errorf("descriptor discovery failed: %w", resp.Error)
		}
		// Descriptors are already stored in discovery cache by handleATTPacket
		descs := cache.Descriptors[charHandle]
		logger.Debug(shortHash(w.hardwareUUID)+" Wire",
			"✅ Descriptor discovery complete: %d descriptors found", len(descs))
		return nil
	case <-time.After(30 * time.Second):
		return fmt.Errorf("descriptor discovery timeout")
	}
}

// GetDiscoveredServices returns all services discovered on a connection
func (w *Wire) GetDiscoveredServices(peerUUID string) ([]gatt.DiscoveredService, error) {
	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("not connected to %s", peerUUID)
	}

	cache := connection.discoveryCache.(*gatt.DiscoveryCache)
	return cache.Services, nil
}

// GetDiscoveredCharacteristics returns all characteristics discovered for a service
func (w *Wire) GetDiscoveredCharacteristics(peerUUID string, serviceStartHandle uint16) ([]gatt.DiscoveredCharacteristic, error) {
	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("not connected to %s", peerUUID)
	}

	cache := connection.discoveryCache.(*gatt.DiscoveryCache)
	chars := cache.Characteristics[serviceStartHandle]
	return chars, nil
}

// GetCharacteristicHandle returns the value handle for a characteristic UUID
// This uses the discovery cache to look up the handle
func (w *Wire) GetCharacteristicHandle(peerUUID string, charUUID []byte) (uint16, error) {
	w.mu.RLock()
	connection, exists := w.connections[peerUUID]
	w.mu.RUnlock()

	if !exists {
		return 0, fmt.Errorf("not connected to %s", peerUUID)
	}

	cache := connection.discoveryCache.(*gatt.DiscoveryCache)
	return cache.GetCharacteristicHandle(charUUID)
}

// SetAttributeDatabase sets the GATT attribute database for this device (server-side)
// This is used when acting as a peripheral to expose services to centrals
func (w *Wire) SetAttributeDatabase(db *gatt.AttributeDatabase) {
	w.dbMu.Lock()
	w.attributeDB = db
	w.dbMu.Unlock()
}
