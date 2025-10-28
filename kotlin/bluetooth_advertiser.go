package kotlin

import (
	"fmt"
	"time"

	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/wire"
)

// AdvertiseCallback matches Android's AdvertiseCallback interface
type AdvertiseCallback interface {
	OnStartSuccess(settingsInEffect *AdvertiseSettings)
	OnStartFailure(errorCode int)
}

// AdvertiseSettings matches Android's AdvertiseSettings class
type AdvertiseSettings struct {
	AdvertiseMode int // ADVERTISE_MODE_LOW_POWER, BALANCED, LOW_LATENCY
	Connectable   bool
	Timeout       int // milliseconds, 0 = no timeout
	TxPowerLevel  int // ADVERTISE_TX_POWER_ULTRA_LOW, LOW, MEDIUM, HIGH
}

// AdvertiseSettings modes
const (
	ADVERTISE_MODE_LOW_POWER   = 0 // 1000ms interval
	ADVERTISE_MODE_BALANCED    = 1 // 250ms interval
	ADVERTISE_MODE_LOW_LATENCY = 2 // 100ms interval
)

// AdvertiseSettings TX power levels
const (
	ADVERTISE_TX_POWER_ULTRA_LOW = 0 // -21 dBm
	ADVERTISE_TX_POWER_LOW       = 1 // -15 dBm
	ADVERTISE_TX_POWER_MEDIUM    = 2 // -7 dBm
	ADVERTISE_TX_POWER_HIGH      = 3 // 1 dBm
)

// AdvertiseSettings error codes
const (
	ADVERTISE_FAILED_DATA_TOO_LARGE       = 1
	ADVERTISE_FAILED_TOO_MANY_ADVERTISERS = 2
	ADVERTISE_FAILED_ALREADY_STARTED      = 3
	ADVERTISE_FAILED_INTERNAL_ERROR       = 4
	ADVERTISE_FAILED_FEATURE_UNSUPPORTED  = 5
)

// AdvertiseData matches Android's AdvertiseData class
type AdvertiseData struct {
	ServiceUUIDs        []string
	ManufacturerData    map[int][]byte    // Company ID -> data
	ServiceData         map[string][]byte // Service UUID -> data
	IncludeTxPowerLevel bool
	IncludeDeviceName   bool
}

// BluetoothLeAdvertiser matches Android's BluetoothLeAdvertiser class
type BluetoothLeAdvertiser struct {
	uuid            string
	deviceName      string
	wire            *wire.Wire
	isAdvertising   bool
	stopAdvertising chan struct{}
	callback        AdvertiseCallback
	settings        *AdvertiseSettings
	gattServer      *BluetoothGattServer
}

// NewBluetoothLeAdvertiser creates a new advertiser
func NewBluetoothLeAdvertiser(uuid string, deviceName string, sharedWire *wire.Wire) *BluetoothLeAdvertiser {
	return &BluetoothLeAdvertiser{
		uuid:          uuid,
		deviceName:    deviceName,
		wire:          sharedWire,
		isAdvertising: false,
	}
}

// SetGattServer links the advertiser with a GATT server
func (a *BluetoothLeAdvertiser) SetGattServer(server *BluetoothGattServer) {
	a.gattServer = server
}

// StartAdvertising starts advertising with the specified settings and data
// Matches: bluetoothLeAdvertiser.startAdvertising(settings, advertiseData, scanResponse, callback)
func (a *BluetoothLeAdvertiser) StartAdvertising(
	settings *AdvertiseSettings,
	advertiseData *AdvertiseData,
	scanResponse *AdvertiseData,
	callback AdvertiseCallback,
) {
	if a.isAdvertising {
		if callback != nil {
			go callback.OnStartFailure(ADVERTISE_FAILED_ALREADY_STARTED)
		}
		return
	}

	if settings == nil {
		settings = &AdvertiseSettings{
			AdvertiseMode: ADVERTISE_MODE_LOW_POWER,
			Connectable:   true,
			Timeout:       0,
			TxPowerLevel:  ADVERTISE_TX_POWER_MEDIUM,
		}
	}

	a.callback = callback
	a.settings = settings

	// Build wire.AdvertisingData from Android-style AdvertiseData
	wireAdvData := &wire.AdvertisingData{
		IsConnectable: settings.Connectable,
	}

	// Collect service UUIDs
	var serviceUUIDs []string
	if advertiseData != nil {
		serviceUUIDs = append(serviceUUIDs, advertiseData.ServiceUUIDs...)
	}
	if scanResponse != nil {
		serviceUUIDs = append(serviceUUIDs, scanResponse.ServiceUUIDs...)
	}
	wireAdvData.ServiceUUIDs = serviceUUIDs

	// Include device name if requested
	if advertiseData != nil && advertiseData.IncludeDeviceName {
		wireAdvData.DeviceName = a.deviceName
	}

	// Include TX power level if requested
	if advertiseData != nil && advertiseData.IncludeTxPowerLevel {
		txPower := a.txPowerLevelToDbm(settings.TxPowerLevel)
		wireAdvData.TxPowerLevel = &txPower
	}

	// Include manufacturer data (use first entry if multiple)
	if advertiseData != nil && len(advertiseData.ManufacturerData) > 0 {
		for _, data := range advertiseData.ManufacturerData {
			wireAdvData.ManufacturerData = data
			break // Only include first manufacturer data in advertising
		}
	}

	// Write advertising data to wire layer
	if err := a.wire.WriteAdvertisingData(wireAdvData); err != nil {
		if callback != nil {
			go callback.OnStartFailure(ADVERTISE_FAILED_INTERNAL_ERROR)
		}
		return
	}

	// Write GATT table if GATT server is linked
	if a.gattServer != nil {
		gattTable := a.gattServer.buildGATTTable()
		if err := a.wire.WriteGATTTable(gattTable); err != nil {
			logger.Trace(fmt.Sprintf("%s Android", a.uuid[:8]), "⚠️  Failed to write GATT table: %v", err)
		}
	}

	a.isAdvertising = true
	a.stopAdvertising = make(chan struct{})

	logger.Info(fmt.Sprintf("%s Android", a.uuid[:8]), "📡 Started Advertising")

	// Handle timeout if specified
	if settings.Timeout > 0 {
		go func() {
			select {
			case <-time.After(time.Duration(settings.Timeout) * time.Millisecond):
				a.StopAdvertising()
			case <-a.stopAdvertising:
				// Stopped manually before timeout
			}
		}()
	}

	// Notify callback of success
	if callback != nil {
		go func() {
			// Small delay to match real Android async behavior
			time.Sleep(10 * time.Millisecond)
			callback.OnStartSuccess(settings)
		}()
	}
}

// StopAdvertising stops advertising
// Matches: bluetoothLeAdvertiser.stopAdvertising(callback)
func (a *BluetoothLeAdvertiser) StopAdvertising() {
	if !a.isAdvertising {
		return
	}

	if a.stopAdvertising != nil {
		close(a.stopAdvertising)
		a.stopAdvertising = nil
	}

	a.isAdvertising = false

	logger.Info(fmt.Sprintf("%s Android", a.uuid[:8]), "📡 Stopped Advertising")
}

// IsAdvertising returns whether currently advertising
func (a *BluetoothLeAdvertiser) IsAdvertising() bool {
	return a.isAdvertising
}

// txPowerLevelToDbm converts Android TX power level to dBm
func (a *BluetoothLeAdvertiser) txPowerLevelToDbm(level int) int {
	switch level {
	case ADVERTISE_TX_POWER_ULTRA_LOW:
		return -21
	case ADVERTISE_TX_POWER_LOW:
		return -15
	case ADVERTISE_TX_POWER_MEDIUM:
		return -7
	case ADVERTISE_TX_POWER_HIGH:
		return 1
	default:
		return -7 // Default to medium
	}
}

// HandleGATTMessage is called by android.go when a GATT message arrives in peripheral mode
// This replaces the old inbox polling mechanism with direct message delivery
func (a *BluetoothLeAdvertiser) HandleGATTMessage(msg *wire.GATTMessage) {
	// Forward to GATT server
	if a.gattServer != nil {
		a.gattServer.handleCharacteristicMessage(msg)
	}
}

// BluetoothGattServer matches Android's BluetoothGattServer class
// Manages the local GATT database when device is in peripheral role
type BluetoothGattServer struct {
	uuid             string
	wire             *wire.Wire
	services         []*BluetoothGattService
	callback         BluetoothGattServerCallback
	connectedDevices map[string]*BluetoothDevice // device UUID -> device
}

// BluetoothGattServerCallback matches Android's BluetoothGattServerCallback
type BluetoothGattServerCallback interface {
	OnConnectionStateChange(device *BluetoothDevice, status int, newState int)
	OnCharacteristicReadRequest(device *BluetoothDevice, requestId int, offset int, characteristic *BluetoothGattCharacteristic)
	OnCharacteristicWriteRequest(device *BluetoothDevice, requestId int, characteristic *BluetoothGattCharacteristic, preparedWrite bool, responseNeeded bool, offset int, value []byte)
	OnDescriptorReadRequest(device *BluetoothDevice, requestId int, offset int, descriptor *BluetoothGattDescriptor)
	OnDescriptorWriteRequest(device *BluetoothDevice, requestId int, descriptor *BluetoothGattDescriptor, preparedWrite bool, responseNeeded bool, offset int, value []byte)
}

// BluetoothGattCharacteristic permissions
const (
	PERMISSION_READ  = 0x01
	PERMISSION_WRITE = 0x10
)

// GATT status codes
const (
	GATT_SUCCESS = 0
	GATT_FAILURE = 257
)

// NewBluetoothGattServer creates a new GATT server
// Matches: bluetoothManager.openGattServer(context, callback)
func NewBluetoothGattServer(uuid string, callback BluetoothGattServerCallback, deviceName string, sharedWire *wire.Wire) *BluetoothGattServer {
	return &BluetoothGattServer{
		uuid:             uuid,
		wire:             sharedWire,
		services:         make([]*BluetoothGattService, 0),
		callback:         callback,
		connectedDevices: make(map[string]*BluetoothDevice),
	}
}

// AddService adds a service to the GATT server
// Matches: gattServer.addService(service)
func (s *BluetoothGattServer) AddService(service *BluetoothGattService) bool {
	s.services = append(s.services, service)

	// Write GATT table to wire layer
	gattTable := s.buildGATTTable()
	if err := s.wire.WriteGATTTable(gattTable); err != nil {
		logger.Trace(fmt.Sprintf("%s Android", s.uuid[:8]), "⚠️  Failed to add service: %v", err)
		return false
	}

	logger.Info(fmt.Sprintf("%s Android", s.uuid[:8]), "📋 Added Service to GATT: %s", service.UUID)
	return true
}

// RemoveService removes a service from the GATT server
// Matches: gattServer.removeService(service)
func (s *BluetoothGattServer) RemoveService(service *BluetoothGattService) bool {
	for i, svc := range s.services {
		if svc.UUID == service.UUID {
			s.services = append(s.services[:i], s.services[i+1:]...)
			break
		}
	}

	gattTable := s.buildGATTTable()
	if err := s.wire.WriteGATTTable(gattTable); err != nil {
		return false
	}
	return true
}

// ClearServices removes all services
// Matches: gattServer.clearServices()
func (s *BluetoothGattServer) ClearServices() {
	s.services = make([]*BluetoothGattService, 0)
	gattTable := s.buildGATTTable()
	s.wire.WriteGATTTable(gattTable)
}

// SendResponse sends a response to a read/write request
// Matches: gattServer.sendResponse(device, requestId, status, offset, value)
func (s *BluetoothGattServer) SendResponse(device *BluetoothDevice, requestId int, status int, offset int, value []byte) bool {
	// In the simulator, we handle requests synchronously
	// Real Android would send ATT response packets back to the device
	logger.Trace(fmt.Sprintf("%s Android", s.uuid[:8]), "📨 Sent response to device %s (reqId=%d, status=%d)", device.Address[:8], requestId, status)
	return true
}

// NotifyCharacteristicChanged sends a notification/indication to a connected device
// Matches: gattServer.notifyCharacteristicChanged(device, characteristic, confirm)
func (s *BluetoothGattServer) NotifyCharacteristicChanged(device *BluetoothDevice, characteristic *BluetoothGattCharacteristic, confirm bool) bool {
	if device == nil || characteristic == nil {
		return false
	}

	// Find service for this characteristic
	var serviceUUID string
	for _, service := range s.services {
		for _, char := range service.Characteristics {
			if char.UUID == characteristic.UUID {
				serviceUUID = service.UUID
				break
			}
		}
		if serviceUUID != "" {
			break
		}
	}

	if serviceUUID == "" {
		logger.Trace(fmt.Sprintf("%s Android", s.uuid[:8]), "⚠️  Characteristic not found in any service")
		return false
	}

	err := s.wire.NotifyCharacteristic(device.Address, serviceUUID, characteristic.UUID, characteristic.Value)
	if err != nil {
		logger.Trace(fmt.Sprintf("%s Android", s.uuid[:8]), "⚠️  Failed to notify device %s: %v", device.Address[:8], err)
		return false
	}

	logger.Trace(fmt.Sprintf("%s Android", s.uuid[:8]), "📤 Sent notification to device %s (%d bytes)", device.Address[:8], len(characteristic.Value))
	return true
}

// Close closes the GATT server
// Matches: gattServer.close()
func (s *BluetoothGattServer) Close() {
	s.services = make([]*BluetoothGattService, 0)
	s.connectedDevices = make(map[string]*BluetoothDevice)
}

// buildGATTTable converts services to wire.GATTTable format
func (s *BluetoothGattServer) buildGATTTable() *wire.GATTTable {
	gattTable := &wire.GATTTable{
		Services: make([]wire.GATTService, 0),
	}

	for _, service := range s.services {
		gattService := wire.GATTService{
			UUID:            service.UUID,
			Type:            "primary",
			Characteristics: make([]wire.GATTCharacteristic, 0),
		}

		if service.Type == SERVICE_TYPE_SECONDARY {
			gattService.Type = "secondary"
		}

		for _, char := range service.Characteristics {
			gattChar := wire.GATTCharacteristic{
				UUID:       char.UUID,
				Properties: s.propertiesToStrings(char.Properties),
			}
			gattService.Characteristics = append(gattService.Characteristics, gattChar)
		}

		gattTable.Services = append(gattTable.Services, gattService)
	}

	return gattTable
}

// propertiesToStrings converts bitmask to string array
func (s *BluetoothGattServer) propertiesToStrings(props int) []string {
	var result []string
	if props&PROPERTY_READ != 0 {
		result = append(result, "read")
	}
	if props&PROPERTY_WRITE != 0 {
		result = append(result, "write")
	}
	if props&PROPERTY_WRITE_NO_RESPONSE != 0 {
		result = append(result, "write_without_response")
	}
	if props&PROPERTY_NOTIFY != 0 {
		result = append(result, "notify")
	}
	if props&PROPERTY_INDICATE != 0 {
		result = append(result, "indicate")
	}
	return result
}

// handleCharacteristicMessage processes incoming GATT messages
func (s *BluetoothGattServer) handleCharacteristicMessage(msg *wire.CharacteristicMessage) {
	// Find the characteristic
	var targetChar *BluetoothGattCharacteristic
	for _, service := range s.services {
		if service.UUID == msg.ServiceUUID {
			for _, char := range service.Characteristics {
				if char.UUID == msg.CharacteristicUUID {
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
		logger.Trace(fmt.Sprintf("%s Android", s.uuid[:8]), "⚠️  Received request for unknown characteristic %s (service: %s, op: %s)", msg.CharacteristicUUID, msg.ServiceUUID, msg.Operation)
		return
	}

	// Get or create device
	device, exists := s.connectedDevices[msg.SenderUUID]
	if !exists {
		device = &BluetoothDevice{
			Address: msg.SenderUUID,
			Name:    "Unknown Device",
		}
		s.connectedDevices[msg.SenderUUID] = device

		// Notify connection state change
		if s.callback != nil {
			go s.callback.OnConnectionStateChange(device, GATT_SUCCESS, STATE_CONNECTED)
		}
	}

	// Generate request ID (timestamp-based)
	requestId := int(time.Now().UnixNano() & 0x7FFFFFFF)

	switch msg.Operation {
	case "read":
		// Read request from central
		if s.callback != nil {
			s.callback.OnCharacteristicReadRequest(device, requestId, 0, targetChar)
		}

	case "write", "write_no_response":
		// Write request from central
		responseNeeded := msg.Operation == "write"
		if s.callback != nil {
			s.callback.OnCharacteristicWriteRequest(device, requestId, targetChar, false, responseNeeded, 0, msg.Data)

			// Update characteristic value
			targetChar.Value = msg.Data
		}

	case "subscribe":
		// Central is subscribing to notifications
		// In real BLE, this would write to the CCCD descriptor
		// For now, just log it - the subscription tracking happens in the wire layer
		centralID := device.Address
		if len(centralID) > 8 {
			centralID = centralID[:8]
		}
		charID := targetChar.UUID
		if len(charID) > 8 {
			charID = charID[:8]
		}
		logger.Debug(fmt.Sprintf("%s Android", s.uuid[:8]), "📲 Central %s subscribed to characteristic %s", centralID, charID)

	case "unsubscribe":
		// Central is unsubscribing from notifications
		centralID := device.Address
		if len(centralID) > 8 {
			centralID = centralID[:8]
		}
		charID := targetChar.UUID
		if len(charID) > 8 {
			charID = charID[:8]
		}
		logger.Debug(fmt.Sprintf("%s Android", s.uuid[:8]), "📲 Central %s unsubscribed from characteristic %s", centralID, charID)
	}
}

// GetCharacteristic finds a characteristic by service and characteristic UUID
func (s *BluetoothGattServer) GetCharacteristic(serviceUUID, charUUID string) *BluetoothGattCharacteristic {
	for _, service := range s.services {
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
