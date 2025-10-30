package swift

import (
	"fmt"
	"time"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/wire"
	"github.com/user/auraphone-blue/wire/gatt"
)

// shortUUID safely truncates a UUID for logging (max 8 chars)
func shortUUID(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}

// CBPeripheralManagerDelegate matches iOS CoreBluetooth peripheral manager delegate
type CBPeripheralManagerDelegate interface {
	DidUpdatePeripheralState(peripheralManager *CBPeripheralManager)
	DidStartAdvertising(peripheralManager *CBPeripheralManager, err error)
	DidReceiveReadRequest(peripheralManager *CBPeripheralManager, request *CBATTRequest)
	DidReceiveWriteRequests(peripheralManager *CBPeripheralManager, requests []*CBATTRequest)
	CentralDidSubscribe(peripheralManager *CBPeripheralManager, central CBCentral, characteristic *CBMutableCharacteristic)
	CentralDidUnsubscribe(peripheralManager *CBPeripheralManager, central CBCentral, characteristic *CBMutableCharacteristic)
}

// CBCentral represents a remote central device (in real iOS, this represents a connected central)
type CBCentral struct {
	UUID           string
	MaximumUpdateValueLength int
}

// CBATTRequest represents a read/write request from a central device
// Matches iOS CoreBluetooth CBATTRequest
type CBATTRequest struct {
	Central        CBCentral
	Characteristic *CBMutableCharacteristic
	Offset         int
	Value          []byte // For write requests: contains the data being written
	               // For read requests: app can set this before calling RespondToRequest
}

// CBMutableCharacteristic is the peripheral-side representation of a characteristic
type CBMutableCharacteristic struct {
	UUID        string
	Properties  CBCharacteristicProperties
	Value       []byte
	Permissions CBAttributePermissions
	Descriptors []*CBMutableDescriptor
	Service     *CBMutableService // Parent service

	// Track which centrals are subscribed for notifications
	subscribedCentrals map[string]bool
}

// CBMutableDescriptor represents a GATT descriptor (peripheral-side)
type CBMutableDescriptor struct {
	UUID  string
	Value []byte
}

// CBMutableService is the peripheral-side representation of a service
type CBMutableService struct {
	UUID            string
	IsPrimary       bool
	Characteristics []*CBMutableCharacteristic
}

// CBCharacteristicProperties represents characteristic properties bitmask
type CBCharacteristicProperties int

const (
	CBCharacteristicPropertyBroadcast                      CBCharacteristicProperties = 1 << 0
	CBCharacteristicPropertyRead                           CBCharacteristicProperties = 1 << 1
	CBCharacteristicPropertyWriteWithoutResponse           CBCharacteristicProperties = 1 << 2
	CBCharacteristicPropertyWrite                          CBCharacteristicProperties = 1 << 3
	CBCharacteristicPropertyNotify                         CBCharacteristicProperties = 1 << 4
	CBCharacteristicPropertyIndicate                       CBCharacteristicProperties = 1 << 5
	CBCharacteristicPropertyAuthenticatedSignedWrites      CBCharacteristicProperties = 1 << 6
	CBCharacteristicPropertyExtendedProperties             CBCharacteristicProperties = 1 << 7
	CBCharacteristicPropertyNotifyEncryptionRequired       CBCharacteristicProperties = 1 << 8
	CBCharacteristicPropertyIndicateEncryptionRequired     CBCharacteristicProperties = 1 << 9
)

// CBAttributePermissions represents characteristic permissions bitmask
type CBAttributePermissions int

const (
	CBAttributePermissionsReadable                CBAttributePermissions = 1 << 0
	CBAttributePermissionsWriteable               CBAttributePermissions = 1 << 1
	CBAttributePermissionsReadEncryptionRequired  CBAttributePermissions = 1 << 2
	CBAttributePermissionsWriteEncryptionRequired CBAttributePermissions = 1 << 3
)

// CBPeripheralManager manages the local device as a BLE peripheral
// Matches iOS CoreBluetooth CBPeripheralManager API
type CBPeripheralManager struct {
	Delegate        CBPeripheralManagerDelegate
	State           CBManagerState // Use enum instead of string
	IsAdvertising   bool
	uuid            string
	wire            *wire.Wire
	services        []*CBMutableService
	stopAdvertising chan struct{}
	stopListening   chan struct{}
	connectedCentrals map[string]CBCentral // UUID -> Central
}

// NewCBPeripheralManager creates a new peripheral manager
// Matches: CBPeripheralManager(delegate:queue:options:)
func NewCBPeripheralManager(delegate CBPeripheralManagerDelegate, uuid string, deviceName string, sharedWire *wire.Wire) *CBPeripheralManager {
	pm := &CBPeripheralManager{
		Delegate:          delegate,
		State:             CBManagerStateUnknown, // REALISTIC: Start in unknown state
		uuid:              uuid,
		wire:              sharedWire,
		services:          make([]*CBMutableService, 0),
		IsAdvertising:     false,
		connectedCentrals: make(map[string]CBCentral),
	}

	// REALISTIC iOS BEHAVIOR: Bluetooth initialization takes time (50-200ms)
	// Notify delegate of state transition after initialization delay
	if delegate != nil {
		go func() {
			// Simulate BLE stack initialization delay
			time.Sleep(100 * time.Millisecond)

			pm.State = CBManagerStatePoweredOn

			// CRITICAL: Always call DidUpdatePeripheralState when state changes
			delegate.DidUpdatePeripheralState(pm)
		}()
	}

	return pm
}

// AddService adds a service to the peripheral's GATT database
// Matches: peripheralManager.add(_:)
func (pm *CBPeripheralManager) AddService(service *CBMutableService) error {
	if pm.IsAdvertising {
		return fmt.Errorf("cannot add service while advertising")
	}

	// Initialize subscribedCentrals for characteristics
	for _, char := range service.Characteristics {
		char.Service = service
		if char.subscribedCentrals == nil {
			char.subscribedCentrals = make(map[string]bool)
		}
	}

	pm.services = append(pm.services, service)

	// Build AttributeDatabase using new wire/gatt package
	db := pm.buildAttributeDatabase()
	pm.wire.SetAttributeDatabase(db)

	// Also write GATT table for backward compatibility with file-based discovery
	gattTable := pm.buildGATTTable()
	if err := pm.wire.WriteGATTTable(gattTable); err != nil {
		return fmt.Errorf("failed to write GATT table: %w", err)
	}

	logger.Info(fmt.Sprintf("%s iOS", pm.uuid[:8]), "📋 Added Service to GATT: %s", service.UUID)

	return nil
}

// RemoveService removes a service from the peripheral's GATT database
// Matches: peripheralManager.remove(_:)
func (pm *CBPeripheralManager) RemoveService(service *CBMutableService) error {
	if pm.IsAdvertising {
		return fmt.Errorf("cannot remove service while advertising")
	}

	for i, s := range pm.services {
		if s.UUID == service.UUID {
			pm.services = append(pm.services[:i], pm.services[i+1:]...)
			break
		}
	}

	// Update GATT table
	gattTable := pm.buildGATTTable()
	return pm.wire.WriteGATTTable(gattTable)
}

// RemoveAllServices removes all services
// Matches: peripheralManager.removeAllServices()
func (pm *CBPeripheralManager) RemoveAllServices() error {
	if pm.IsAdvertising {
		return fmt.Errorf("cannot remove services while advertising")
	}

	pm.services = make([]*CBMutableService, 0)

	// Update GATT table
	gattTable := pm.buildGATTTable()
	return pm.wire.WriteGATTTable(gattTable)
}

// StartAdvertising starts advertising with the specified data
// Matches: peripheralManager.startAdvertising(_:)
// advertisementData keys: "kCBAdvDataLocalName", "kCBAdvDataServiceUUIDs"
func (pm *CBPeripheralManager) StartAdvertising(advertisementData map[string]interface{}) error {
	if pm.IsAdvertising {
		return fmt.Errorf("already advertising")
	}

	// Build AdvertisingData from iOS-style dictionary
	advData := &wire.AdvertisingData{
		IsConnectable: true,
	}

	if localName, ok := advertisementData["kCBAdvDataLocalName"].(string); ok {
		advData.DeviceName = localName
	}

	if serviceUUIDs, ok := advertisementData["kCBAdvDataServiceUUIDs"].([]string); ok {
		advData.ServiceUUIDs = serviceUUIDs
	}

	if manufacturerData, ok := advertisementData["kCBAdvDataManufacturerData"].([]byte); ok {
		advData.ManufacturerData = manufacturerData
	}

	if txPower, ok := advertisementData["kCBAdvDataTxPowerLevel"].(int); ok {
		advData.TxPowerLevel = &txPower
	}

	// Write advertising data to wire layer
	if err := pm.wire.WriteAdvertisingData(advData); err != nil {
		// Notify delegate of failure
		if pm.Delegate != nil {
			pm.Delegate.DidStartAdvertising(pm, err)
		}
		return err
	}

	pm.IsAdvertising = true
	pm.stopAdvertising = make(chan struct{})

	logger.Info(fmt.Sprintf("%s iOS", pm.uuid[:8]), "📡 Started Advertising")

	// Start listening for incoming requests
	pm.startListeningForRequests()

	// Notify delegate of success
	if pm.Delegate != nil {
		go func() {
			// Small delay to match real iOS async behavior
			time.Sleep(10 * time.Millisecond)
			pm.Delegate.DidStartAdvertising(pm, nil)
		}()
	}

	return nil
}

// StopAdvertising stops advertising
// Matches: peripheralManager.stopAdvertising()
func (pm *CBPeripheralManager) StopAdvertising() {
	if !pm.IsAdvertising {
		return
	}

	if pm.stopAdvertising != nil {
		close(pm.stopAdvertising)
		pm.stopAdvertising = nil
	}

	if pm.stopListening != nil {
		close(pm.stopListening)
		pm.stopListening = nil
	}

	pm.IsAdvertising = false

	// REALISTIC BLE: Clear advertising data so device becomes undiscoverable
	// Real BLE: When peripheral stops advertising, scanning Centrals can no longer discover it
	if err := pm.wire.ClearAdvertisingData(); err != nil {
		logger.Warn(fmt.Sprintf("%s iOS", pm.uuid[:8]), "Failed to clear advertising data: %v", err)
	}

	logger.Info(fmt.Sprintf("%s iOS", pm.uuid[:8]), "📡 Stopped Advertising")
}

// UpdateValue sends a notification/indication to subscribed centrals
// Matches: peripheralManager.updateValue(_:for:onSubscribedCentrals:)
// Returns false if transmission queue is full (real iOS behavior)
func (pm *CBPeripheralManager) UpdateValue(value []byte, characteristic *CBMutableCharacteristic, centrals []CBCentral) bool {
	if characteristic == nil {
		return false
	}

	// If no centrals specified, send to all subscribed centrals
	targetCentrals := centrals
	if len(targetCentrals) == 0 {
		// Send to all subscribed centrals
		for uuid := range characteristic.subscribedCentrals {
			targetCentrals = append(targetCentrals, CBCentral{UUID: uuid})
		}
	}

	if len(targetCentrals) == 0 {
		// No subscribed centrals
		return true
	}

	// Send notification to each subscribed central
	success := true
	for _, central := range targetCentrals {
		// Check if this central is actually subscribed
		if !characteristic.subscribedCentrals[central.UUID] {
			continue
		}

		err := pm.wire.NotifyCharacteristic(central.UUID, characteristic.Service.UUID, characteristic.UUID, value)
		if err != nil {
			logger.Trace(fmt.Sprintf("%s iOS", pm.uuid[:8]), "⚠️  Failed to notify central %s: %v", central.UUID[:8], err)
			success = false
		} else {
			logger.Trace(fmt.Sprintf("%s iOS", pm.uuid[:8]), "📤 Sent notification to central %s (%d bytes)", central.UUID[:8], len(value))
		}
	}

	return success
}

// RespondToRequest responds to a read/write request from a central
// Matches: peripheralManager.respond(to:withResult:)
// REALISTIC iOS BEHAVIOR:
// - For read requests: App can set request.value in DidReceiveReadRequest, then call this
// - Response sends request.value if set, otherwise falls back to characteristic.value
// - For write requests: Request already has request.value from the write data
func (pm *CBPeripheralManager) RespondToRequest(request *CBATTRequest, result int) {
	// REALISTIC iOS BEHAVIOR: Send ATT response back to the central
	if result == int(CBATTErrorSuccess) && request.Characteristic != nil {
		// Determine what value to send back
		// Real iOS uses request.value if set by app, otherwise characteristic.value
		responseData := request.Value
		if responseData == nil {
			responseData = request.Characteristic.Value
		}

		// Success - send response value back
		msg := &wire.GATTMessage{
			Type:               "gatt_response",
			Operation:          "read_response",
			ServiceUUID:        request.Characteristic.Service.UUID,
			CharacteristicUUID: request.Characteristic.UUID,
			Data:               responseData,
		}

		err := pm.wire.SendGATTMessage(request.Central.UUID, msg)
		if err != nil {
			logger.Trace(fmt.Sprintf("%s iOS", pm.uuid[:8]), "⚠️  Failed to send read response to central %s: %v", request.Central.UUID[:8], err)
		} else {
			logger.Trace(fmt.Sprintf("%s iOS", pm.uuid[:8]), "📨 Sent read response to central %s (%d bytes)", request.Central.UUID[:8], len(responseData))
		}
	} else {
		// Error response
		logger.Trace(fmt.Sprintf("%s iOS", pm.uuid[:8]), "📨 Responded to request from central %s with error (result=%d)", request.Central.UUID[:8], result)
	}
}

// buildGATTTable converts services to wire.GATTTable format
func (pm *CBPeripheralManager) buildGATTTable() *wire.GATTTable {
	gattTable := &wire.GATTTable{
		Services: make([]wire.GATTService, 0),
	}

	for _, service := range pm.services {
		gattService := wire.GATTService{
			UUID: service.UUID,
			Type: "primary",
			Characteristics: make([]wire.GATTCharacteristic, 0),
		}

		if !service.IsPrimary {
			gattService.Type = "secondary"
		}

		for _, char := range service.Characteristics {
			gattChar := wire.GATTCharacteristic{
				UUID:       char.UUID,
				Properties: pm.propertiesToStrings(char.Properties),
			}
			gattService.Characteristics = append(gattService.Characteristics, gattChar)
		}

		gattTable.Services = append(gattTable.Services, gattService)
	}

	return gattTable
}

// propertiesToStrings converts bitmask to string array
func (pm *CBPeripheralManager) propertiesToStrings(props CBCharacteristicProperties) []string {
	var result []string
	if props&CBCharacteristicPropertyRead != 0 {
		result = append(result, "read")
	}
	if props&CBCharacteristicPropertyWrite != 0 {
		result = append(result, "write")
	}
	if props&CBCharacteristicPropertyWriteWithoutResponse != 0 {
		result = append(result, "write_without_response")
	}
	if props&CBCharacteristicPropertyNotify != 0 {
		result = append(result, "notify")
	}
	if props&CBCharacteristicPropertyIndicate != 0 {
		result = append(result, "indicate")
	}
	if props&CBCharacteristicPropertyBroadcast != 0 {
		result = append(result, "broadcast")
	}
	return result
}

// startListeningForRequests is now a no-op
// GATT message handling is done via HandleGATTMessage() callback from iPhone layer
func (pm *CBPeripheralManager) startListeningForRequests() {
	// No-op: New architecture uses callbacks, not polling
	// iPhone layer will call pm.HandleGATTMessage() for each incoming request
}

// HandleGATTMessage processes incoming GATT request messages (public API for iPhone layer)
// Should be called for gatt_request messages only (read, write, subscribe, unsubscribe)
// Returns true if message was handled, false if it should be routed elsewhere
func (pm *CBPeripheralManager) HandleGATTMessage(peerUUID string, msg *wire.GATTMessage) bool {
	// Only handle gatt_request messages
	if msg.Type != "gatt_request" {
		return false
	}

	// Convert to old CharacteristicMessage format for compatibility
	charMsg := &wire.CharacteristicMessage{
		Type:               msg.Type,
		Operation:          msg.Operation,
		ServiceUUID:        msg.ServiceUUID,
		CharacteristicUUID: msg.CharacteristicUUID,
		Data:               msg.Data,
		SenderUUID:         peerUUID,
	}

	pm.handleCharacteristicMessage(charMsg)
	return true
}

// handleCharacteristicMessage processes incoming GATT messages (internal)
func (pm *CBPeripheralManager) handleCharacteristicMessage(msg *wire.CharacteristicMessage) {
	// REALISTIC BLE: Check if this is a CCCD descriptor write FIRST
	// CCCD writes have msg.CharacteristicUUID == "00002902-..."
	// For CCCD writes, we need to find the notifiable characteristic in the service
	isCCCDWrite := (msg.CharacteristicUUID == CBUUID_CCCD || msg.CharacteristicUUID == "00002902-0000-1000-8000-00805f9b34fb")

	// Find the characteristic
	var targetChar *CBMutableCharacteristic
	for _, service := range pm.services {
		// REALISTIC BLE: Compare UUIDs using their 16-byte representation
		// This handles truncation when UUIDs are longer than 16 bytes
		if pm.uuidMatches(service.UUID, msg.ServiceUUID) {
			if isCCCDWrite {
				// For CCCD writes, find the first notifiable characteristic in this service
				// (In real BLE, each characteristic has its own CCCD, but our simplified protocol
				//  assumes one notifiable char per service for now)
				for _, char := range service.Characteristics {
					// Check if characteristic has notify or indicate properties (bitmask)
					hasNotify := (char.Properties&CBCharacteristicPropertyNotify != 0) ||
						(char.Properties&CBCharacteristicPropertyIndicate != 0)
					if hasNotify {
						targetChar = char
						break
					}
				}
			} else {
				// Regular characteristic lookup by UUID
				for _, char := range service.Characteristics {
					if pm.uuidMatches(char.UUID, msg.CharacteristicUUID) {
						targetChar = char
						break
					}
				}
			}
		}
		if targetChar != nil {
			break
		}
	}

	if targetChar == nil {
		if isCCCDWrite {
			logger.Trace(fmt.Sprintf("%s iOS", shortUUID(pm.uuid)), "⚠️  Received CCCD write for service %s, but no notifiable characteristic found", shortUUID(msg.ServiceUUID))
		} else {
			logger.Trace(fmt.Sprintf("%s iOS", shortUUID(pm.uuid)), "⚠️  Received request for unknown characteristic %s (service: %s, op: %s)", msg.CharacteristicUUID, msg.ServiceUUID, msg.Operation)
		}
		return
	}

	// Get or create central
	central, exists := pm.connectedCentrals[msg.SenderUUID]
	if !exists {
		central = CBCentral{
			UUID:                     msg.SenderUUID,
			MaximumUpdateValueLength: 512, // BLE 5.0 max
		}
		pm.connectedCentrals[msg.SenderUUID] = central
	}

	switch msg.Operation {
	case "read":
		// Read request from central
		if pm.Delegate != nil {
			request := &CBATTRequest{
				Central:        central,
				Characteristic: targetChar,
				Offset:         0,
			}
			pm.Delegate.DidReceiveReadRequest(pm, request)
		}

	case "write", "write_no_response":
		// REALISTIC BLE: Check if this is a CCCD descriptor write (0x2902)
		// Real BLE enables notifications by writing 0x01 0x00 or 0x02 0x00 to CCCD descriptor
		if msg.CharacteristicUUID == CBUUID_CCCD || msg.CharacteristicUUID == "00002902-0000-1000-8000-00805f9b34fb" {
			// This is a CCCD write - find the characteristic from the service UUID
			// (CCCD writes use the service UUID to identify which characteristic)
			if targetChar != nil && len(msg.Data) >= 2 {
				// Check the CCCD value
				if msg.Data[0] == 0x01 || msg.Data[0] == 0x02 { // 0x01 = notify, 0x02 = indicate
					// Subscribe
					targetChar.subscribedCentrals[msg.SenderUUID] = true
					logger.Trace(fmt.Sprintf("%s iOS", shortUUID(pm.uuid)), "🔔 Central %s subscribed to characteristic %s via CCCD", shortUUID(msg.SenderUUID), shortUUID(targetChar.UUID))
					if pm.Delegate != nil {
						pm.Delegate.CentralDidSubscribe(pm, central, targetChar)
					}
				} else if msg.Data[0] == 0x00 && msg.Data[1] == 0x00 {
					// Unsubscribe
					delete(targetChar.subscribedCentrals, msg.SenderUUID)
					logger.Trace(fmt.Sprintf("%s iOS", shortUUID(pm.uuid)), "🔕 Central %s unsubscribed from characteristic %s via CCCD", shortUUID(msg.SenderUUID), shortUUID(targetChar.UUID))
					if pm.Delegate != nil {
						pm.Delegate.CentralDidUnsubscribe(pm, central, targetChar)
					}
				}
			}
		} else {
			// Regular characteristic write
			if pm.Delegate != nil {
				request := &CBATTRequest{
					Central:        central,
					Characteristic: targetChar,
					Offset:         0,
					Value:          msg.Data,
				}
				pm.Delegate.DidReceiveWriteRequests(pm, []*CBATTRequest{request})

				// Update characteristic value
				targetChar.Value = msg.Data
			}
		}

	case "subscribe":
		// DEPRECATED: Old-style subscribe message (kept for backward compatibility)
		// Real BLE uses CCCD descriptor writes (handled in "write" case above)
		targetChar.subscribedCentrals[msg.SenderUUID] = true
		if pm.Delegate != nil {
			pm.Delegate.CentralDidSubscribe(pm, central, targetChar)
		}

	case "unsubscribe":
		// DEPRECATED: Old-style unsubscribe message (kept for backward compatibility)
		delete(targetChar.subscribedCentrals, msg.SenderUUID)
		if pm.Delegate != nil {
			pm.Delegate.CentralDidUnsubscribe(pm, central, targetChar)
		}
	}
}

// GetCharacteristic finds a characteristic by service and characteristic UUID
func (pm *CBPeripheralManager) GetCharacteristic(serviceUUID, charUUID string) *CBMutableCharacteristic {
	for _, service := range pm.services {
		if service.UUID == serviceUUID {
			for _, char := range service.Characteristics {
				if char.UUID == charUUID {
					return char
				}
			}
		}
	}
	return nil
}

// buildAttributeDatabase builds a wire/gatt AttributeDatabase from the services
func (pm *CBPeripheralManager) buildAttributeDatabase() *gatt.AttributeDatabase {
	var gattServices []gatt.Service

	for _, service := range pm.services {
		gattService := gatt.Service{
			UUID:            pm.parseUUID(service.UUID),
			Primary:         service.IsPrimary,
			Characteristics: []gatt.Characteristic{},
		}

		for _, char := range service.Characteristics {
			gattChar := gatt.Characteristic{
				UUID:       pm.parseUUID(char.UUID),
				Properties: pm.propertiesToGATTBitmask(char.Properties),
				Value:      char.Value,
				Descriptors: []gatt.Descriptor{}, // Initialize descriptors slice
			}

			// CRITICAL: Real BLE requires CCCD descriptor for any characteristic with notify/indicate
			// This matches the BLE Core Specification requirement
			if char.Properties&(CBCharacteristicPropertyNotify|CBCharacteristicPropertyIndicate) != 0 {
				// Add CCCD descriptor (Client Characteristic Configuration Descriptor)
				// UUID 0x2902, default value 0x0000 (notifications/indications disabled)
				cccdDescriptor := gatt.Descriptor{
					UUID:  []byte{0x02, 0x29}, // CCCD UUID (0x2902) in little-endian
					Value: []byte{0x00, 0x00}, // Disabled by default
				}
				gattChar.Descriptors = append(gattChar.Descriptors, cccdDescriptor)
			}

			gattService.Characteristics = append(gattService.Characteristics, gattChar)
		}

		gattServices = append(gattServices, gattService)
	}

	db, _ := gatt.BuildAttributeDatabase(gattServices)
	return db
}

// propertiesToGATTBitmask converts CBCharacteristicProperties to gatt properties bitmask
func (pm *CBPeripheralManager) propertiesToGATTBitmask(props CBCharacteristicProperties) uint8 {
	var result uint8
	if props&CBCharacteristicPropertyRead != 0 {
		result |= gatt.PropRead
	}
	if props&CBCharacteristicPropertyWrite != 0 {
		result |= gatt.PropWrite
	}
	if props&CBCharacteristicPropertyWriteWithoutResponse != 0 {
		result |= gatt.PropWriteWithoutResponse
	}
	if props&CBCharacteristicPropertyNotify != 0 {
		result |= gatt.PropNotify
	}
	if props&CBCharacteristicPropertyIndicate != 0 {
		result |= gatt.PropIndicate
	}
	if props&CBCharacteristicPropertyBroadcast != 0 {
		result |= gatt.PropBroadcast
	}
	return result
}

// uuidMatches compares two UUID strings using their 16-byte representation
// This handles cases where UUIDs are longer than 16 bytes and get truncated
// REALISTIC BLE: Real BLE UUIDs are either 16-bit (2 bytes) or 128-bit (16 bytes)
// Test UUIDs can be arbitrary strings, so we normalize by comparing their byte representations
func (pm *CBPeripheralManager) uuidMatches(uuid1, uuid2 string) bool {
	// Convert both to bytes (which truncates to 16 bytes)
	bytes1 := pm.parseUUID(uuid1)
	bytes2 := pm.parseUUID(uuid2)

	// Compare byte-by-byte
	if len(bytes1) != len(bytes2) {
		return false
	}
	for i := range bytes1 {
		if bytes1[i] != bytes2[i] {
			return false
		}
	}
	return true
}

// parseUUID converts a string UUID to bytes
// Supports both 16-bit (e.g., "1800") and 128-bit UUIDs
// Real BLE: UUIDs are binary data, not ASCII strings
func (pm *CBPeripheralManager) parseUUID(uuidStr string) []byte {
	// Try to parse as 16-bit UUID first (4 hex chars)
	if len(uuidStr) == 4 {
		var uuid16 uint16
		fmt.Sscanf(uuidStr, "%04x", &uuid16)
		// Return in little-endian format as per BLE spec
		return []byte{byte(uuid16), byte(uuid16 >> 8)}
	}

	// For standard 128-bit UUIDs (36 chars with dashes: XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX)
	// Real BLE: Parse hex digits into binary bytes, not ASCII characters!
	if len(uuidStr) == 36 {
		var uuid [16]byte
		// Parse UUID according to RFC 4122 format
		// Parse each hex byte pair and store in the byte array
		fmt.Sscanf(uuidStr,
			"%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
			&uuid[0], &uuid[1], &uuid[2], &uuid[3],
			&uuid[4], &uuid[5],
			&uuid[6], &uuid[7],
			&uuid[8], &uuid[9],
			&uuid[10], &uuid[11], &uuid[12], &uuid[13], &uuid[14], &uuid[15])
		return uuid[:]
	}

	// For test/custom UUIDs, create a deterministic 16-byte UUID from the string
	// by hashing the string into 16 bytes
	uuid := make([]byte, 16)
	for i := 0; i < len(uuidStr) && i < 16; i++ {
		uuid[i] = uuidStr[i]
	}
	// Fill remaining bytes with zero
	for i := len(uuidStr); i < 16; i++ {
		uuid[i] = 0
	}
	return uuid
}
