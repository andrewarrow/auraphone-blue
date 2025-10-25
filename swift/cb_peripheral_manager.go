package swift

import (
	"fmt"
	"time"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/wire"
)

// CBPeripheralManagerDelegate matches iOS CoreBluetooth peripheral manager delegate
type CBPeripheralManagerDelegate interface {
	DidUpdateState(peripheralManager *CBPeripheralManager)
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
type CBATTRequest struct {
	Central        CBCentral
	Characteristic *CBMutableCharacteristic
	Offset         int
	Value          []byte // For write requests
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
	State           string // "poweredOn", "poweredOff", etc.
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
func NewCBPeripheralManager(delegate CBPeripheralManagerDelegate, uuid string, platform wire.Platform, deviceName string) *CBPeripheralManager {
	w := wire.NewWireWithPlatform(uuid, platform, deviceName, nil)

	pm := &CBPeripheralManager{
		Delegate:          delegate,
		State:             "poweredOn",
		uuid:              uuid,
		wire:              w,
		services:          make([]*CBMutableService, 0),
		IsAdvertising:     false,
		connectedCentrals: make(map[string]CBCentral),
	}

	// Notify delegate of initial state
	if delegate != nil {
		go func() {
			// Small delay to match real iOS behavior
			time.Sleep(10 * time.Millisecond)
			delegate.DidUpdateState(pm)
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

	// Write GATT table to wire layer
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
			logger.Trace(fmt.Sprintf("%s iOS", pm.uuid[:8]), fmt.Sprintf("⚠️  Failed to notify central %s: %v", central.UUID[:8], err))
			success = false
		} else {
			logger.Trace(fmt.Sprintf("%s iOS", pm.uuid[:8]), fmt.Sprintf("📤 Sent notification to central %s (%d bytes)", central.UUID[:8], len(value)))
		}
	}

	return success
}

// RespondToRequest responds to a read/write request from a central
// Matches: peripheralManager.respond(to:withResult:)
func (pm *CBPeripheralManager) RespondToRequest(request *CBATTRequest, result int) {
	// In the simulator, we handle requests synchronously
	// Real iOS would send ATT response packets back to the central
	logger.Trace(fmt.Sprintf("%s iOS", pm.uuid[:8]), fmt.Sprintf("📨 Responded to request from central %s (result=%d)", request.Central.UUID[:8], result))
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

// startListeningForRequests listens for incoming GATT requests
func (pm *CBPeripheralManager) startListeningForRequests() {
	if pm.stopListening != nil {
		// Already listening
		return
	}

	pm.stopListening = make(chan struct{})

	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-pm.stopListening:
				return
			case <-ticker.C:
				// Read and consume incoming messages (automatically deleted by wire layer)
				messages, err := pm.wire.ReadAndConsumeCharacteristicMessages()
				if err != nil {
					continue
				}

				for _, msg := range messages {
					pm.handleCharacteristicMessage(msg)
				}
			}
		}
	}()
}

// handleCharacteristicMessage processes incoming GATT messages
func (pm *CBPeripheralManager) handleCharacteristicMessage(msg *wire.CharacteristicMessage) {
	// Find the characteristic
	var targetChar *CBMutableCharacteristic
	for _, service := range pm.services {
		if service.UUID == msg.ServiceUUID {
			for _, char := range service.Characteristics {
				if char.UUID == msg.CharUUID {
					targetChar = char
					break
				}
			}
		}
		if targetChar != nil {
			break
		}
	}

	if targetChar == nil {
		logger.Trace(fmt.Sprintf("%s iOS", pm.uuid[:8]), fmt.Sprintf("⚠️  Received request for unknown characteristic %s", msg.CharUUID))
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
		// Write request from central
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

	case "subscribe":
		// Central subscribed to notifications
		targetChar.subscribedCentrals[msg.SenderUUID] = true
		if pm.Delegate != nil {
			pm.Delegate.CentralDidSubscribe(pm, central, targetChar)
		}

	case "unsubscribe":
		// Central unsubscribed from notifications
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
