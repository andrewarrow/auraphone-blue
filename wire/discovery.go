package wire

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/user/auraphone-blue/util"
	"github.com/user/auraphone-blue/wire/advertising"
)

// StartDiscovery starts scanning for devices (stub for old API)
// TODO Step 4: Implement proper discovery with callbacks
func (w *Wire) StartDiscovery(callback func(deviceUUID string)) chan struct{} {
	stopChan := make(chan struct{})

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				return
			case <-ticker.C:
				// Use our new ListAvailableDevices
				devices := w.ListAvailableDevices()
				for _, deviceUUID := range devices {
					callback(deviceUUID)
				}
			}
		}
	}()

	return stopChan
}

// ListAvailableDevices scans socket directory for .sock files and returns hardware UUIDs
// REALISTIC BLE: Only returns devices that are currently advertising
// Real BLE: Centrals can only discover Peripherals that are actively broadcasting advertising packets
func (w *Wire) ListAvailableDevices() []string {
	devices := make([]string, 0)

	// Scan socket directory for auraphone-*.sock files
	socketDir := util.GetSocketDir()
	pattern := filepath.Join(socketDir, "auraphone-*.sock")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return devices
	}

	for _, path := range matches {
		// Extract UUID from filename
		filename := filepath.Base(path)
		// Format: auraphone-{UUID}.sock
		if len(filename) < len("auraphone-") || filepath.Ext(filename) != ".sock" {
			continue
		}

		uuid := filename[len("auraphone-") : len(filename)-len(".sock")]

		// Don't include ourselves
		if uuid == w.hardwareUUID {
			continue
		}

		// REALISTIC BLE: Only include devices that are currently advertising
		// Check if advertising.bin file exists for this device
		deviceDir := util.GetDeviceCacheDir(uuid)
		advPath := filepath.Join(deviceDir, "advertising.bin")
		if _, err := os.Stat(advPath); err != nil {
			// No advertising.bin file = device is not advertising
			// Real BLE: Non-advertising devices are invisible to scanning Centrals
			continue
		}

		devices = append(devices, uuid)
	}

	return devices
}

// ReadAdvertisingData reads advertising data for a device from filesystem
// Real BLE: this simulates discovering advertising packets "over the air"
func (w *Wire) ReadAdvertisingData(deviceUUID string) (*AdvertisingData, error) {
	deviceDir := util.GetDeviceCacheDir(deviceUUID)
	advPath := filepath.Join(deviceDir, "advertising.bin")

	// Read binary advertising packet (simulates discovering advertising packet)
	data, err := os.ReadFile(advPath)
	if err != nil {
		// Return default advertising data if not found
		return &AdvertisingData{
			DeviceName:    fmt.Sprintf("Device-%s", shortHash(deviceUUID)),
			ServiceUUIDs:  []string{},
			IsConnectable: true,
		}, nil
	}

	// Decode binary advertising PDU
	pdu, err := advertising.DecodeAdvertisingPDU(data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode advertising PDU: %w", err)
	}

	// Decode AD structures from advertising data
	structures, err := advertising.DecodeADStructures(pdu.AdvData)
	if err != nil {
		return nil, fmt.Errorf("failed to decode AD structures: %w", err)
	}

	// Extract information from AD structures
	deviceName := advertising.GetLocalName(structures)
	if deviceName == "" {
		deviceName = fmt.Sprintf("Device-%s", shortHash(deviceUUID))
	}

	// Extract 16-bit service UUIDs (UPPERCASE to match iOS/Android APIs)
	uuids16 := advertising.Get16BitServiceUUIDs(structures)
	serviceUUIDs := make([]string, len(uuids16))
	for i, uuid := range uuids16 {
		serviceUUIDs[i] = fmt.Sprintf("%04X", uuid)
	}

	// Extract 128-bit service UUIDs
	uuids128 := advertising.Get128BitServiceUUIDs(structures)
	for _, uuid := range uuids128 {
		// Convert 128-bit UUID to standard format (UPPERCASE to match iOS/Android APIs)
		// REALISTIC BLE: Both CoreBluetooth and Android return UUIDs in uppercase
		serviceUUIDs = append(serviceUUIDs, fmt.Sprintf(
			"%02X%02X%02X%02X-%02X%02X-%02X%02X-%02X%02X-%02X%02X%02X%02X%02X%02X",
			uuid[0], uuid[1], uuid[2], uuid[3], uuid[4], uuid[5], uuid[6], uuid[7],
			uuid[8], uuid[9], uuid[10], uuid[11], uuid[12], uuid[13], uuid[14], uuid[15],
		))
	}

	// Extract manufacturer data
	_, mfgData, hasMfgData := advertising.GetManufacturerData(structures)
	var manufacturerData []byte
	if hasMfgData {
		manufacturerData = mfgData
	}

	// Extract flags to determine if connectable
	_, hasFlags := advertising.GetFlags(structures)
	isConnectable := true
	if hasFlags {
		// Check PDU type for connectability
		isConnectable = (pdu.PDUType == advertising.PDUTypeAdvInd || pdu.PDUType == advertising.PDUTypeAdvDirectInd)
	}

	// Extract Tx Power (if present)
	var txPowerLevel *int
	for _, s := range structures {
		if s.Type == advertising.ADTypeTxPowerLevel && len(s.Data) > 0 {
			power := int(int8(s.Data[0]))
			txPowerLevel = &power
			break
		}
	}

	return &AdvertisingData{
		DeviceName:       deviceName,
		ServiceUUIDs:     serviceUUIDs,
		ManufacturerData: manufacturerData,
		TxPowerLevel:     txPowerLevel,
		IsConnectable:    isConnectable,
	}, nil
}

// WriteAdvertisingData writes advertising data to device's filesystem
// Real BLE: this sets what we broadcast in advertising packets
func (w *Wire) WriteAdvertisingData(data *AdvertisingData) error {
	deviceDir := util.GetDeviceCacheDir(w.hardwareUUID)
	if err := os.MkdirAll(deviceDir, 0755); err != nil {
		return fmt.Errorf("failed to create device directory: %w", err)
	}

	// Build AD structures from high-level data
	var structures []advertising.ADStructure

	// Add flags (LE General Discoverable, BR/EDR not supported)
	flags := byte(advertising.FlagLEGeneralDiscoverableMode | advertising.FlagBREDRNotSupported)
	structures = append(structures, advertising.NewFlagsAD(flags))

	// Add device name if present
	if data.DeviceName != "" {
		structures = append(structures, advertising.NewCompleteLocalNameAD(data.DeviceName))
	}

	// Add service UUIDs
	if len(data.ServiceUUIDs) > 0 {
		// Separate 16-bit and 128-bit UUIDs
		var uuids16 []uint16
		var uuids128 [][16]byte

		for _, uuidStr := range data.ServiceUUIDs {
			if len(uuidStr) == 4 {
				// 16-bit UUID
				var uuid uint16
				fmt.Sscanf(uuidStr, "%04x", &uuid)
				uuids16 = append(uuids16, uuid)
			} else if len(uuidStr) == 36 {
				// 128-bit UUID (standard format with dashes)
				var uuid [16]byte
				fmt.Sscanf(uuidStr,
					"%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
					&uuid[0], &uuid[1], &uuid[2], &uuid[3], &uuid[4], &uuid[5], &uuid[6], &uuid[7],
					&uuid[8], &uuid[9], &uuid[10], &uuid[11], &uuid[12], &uuid[13], &uuid[14], &uuid[15],
				)
				uuids128 = append(uuids128, uuid)
			}
		}

		if len(uuids16) > 0 {
			structures = append(structures, advertising.NewComplete16BitServiceUUIDsAD(uuids16))
		}
		if len(uuids128) > 0 {
			structures = append(structures, advertising.NewComplete128BitServiceUUIDsAD(uuids128))
		}
	}

	// Add Tx power level if present
	if data.TxPowerLevel != nil {
		structures = append(structures, advertising.NewTxPowerLevelAD(int8(*data.TxPowerLevel)))
	}

	// Add manufacturer data if present
	if len(data.ManufacturerData) > 0 {
		// Use company ID 0xFFFF for testing (reserved)
		structures = append(structures, advertising.NewManufacturerSpecificDataAD(0xFFFF, data.ManufacturerData))
	}

	// Encode AD structures
	advData, err := advertising.EncodeADStructures(structures)
	if err != nil {
		return fmt.Errorf("failed to encode AD structures: %w", err)
	}

	// Create advertising PDU
	pdu := &advertising.AdvertisingPDU{
		PDUType: advertising.PDUTypeAdvInd, // Connectable undirected advertising
		AdvA:    [6]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}, // Placeholder MAC address
		AdvData: advData,
	}

	// Use device UUID hash as MAC address (for uniqueness)
	deviceHash := []byte(w.hardwareUUID)
	for i := 0; i < 6 && i < len(deviceHash); i++ {
		pdu.AdvA[i] = deviceHash[i]
	}

	// Encode PDU to binary
	encoded, err := pdu.Encode()
	if err != nil {
		return fmt.Errorf("failed to encode advertising PDU: %w", err)
	}

	// Write binary advertising packet
	advPath := filepath.Join(deviceDir, "advertising.bin")
	if err := os.WriteFile(advPath, encoded, 0644); err != nil {
		return fmt.Errorf("failed to write advertising.bin: %w", err)
	}

	// Also write debug JSON (write-only, never read in production)
	debugDir := filepath.Join(deviceDir, "debug")
	if err := os.MkdirAll(debugDir, 0755); err == nil {
		debugPath := filepath.Join(debugDir, "advertising.json")
		jsonData, err := json.MarshalIndent(map[string]interface{}{
			"timestamp":   time.Now().Format(time.RFC3339Nano),
			"pdu_type":    advertising.PDUTypeName(pdu.PDUType),
			"mac_address": fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", pdu.AdvA[0], pdu.AdvA[1], pdu.AdvA[2], pdu.AdvA[3], pdu.AdvA[4], pdu.AdvA[5]),
			"high_level":  data,
			"ad_structures": func() []map[string]interface{} {
				result := make([]map[string]interface{}, len(structures))
				for i, s := range structures {
					result[i] = map[string]interface{}{
						"type":      advertising.ADTypeName(s.Type),
						"type_code": fmt.Sprintf("0x%02X", s.Type),
						"data_hex":  fmt.Sprintf("%X", s.Data),
						"data":      s.Data,
					}
				}
				return result
			}(),
			"raw_hex": fmt.Sprintf("%X", encoded),
		}, "", "  ")
		if err == nil {
			os.WriteFile(debugPath, jsonData, 0644)
		}
	}

	return nil
}

// ClearAdvertisingData removes advertising data for this device
// Real BLE: this stops broadcasting advertising packets (makes device undiscoverable)
func (w *Wire) ClearAdvertisingData() error {
	deviceDir := util.GetDeviceCacheDir(w.hardwareUUID)
	advPath := filepath.Join(deviceDir, "advertising.bin")

	// Remove advertising file
	// REALISTIC BLE: When a device stops advertising, it becomes invisible to scanning Centrals
	err := os.Remove(advPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove advertising.bin: %w", err)
	}

	return nil
}

// GetRSSI returns simulated RSSI for a device (stub for old API)
// TODO Step 4: Implement realistic RSSI simulation
func (w *Wire) GetRSSI(deviceUUID string) float64 {
	return -45.0 // Good signal strength
}

// DeviceExists checks if a device exists (stub for old API)
// TODO Step 4: Implement proper device discovery state tracking
func (w *Wire) DeviceExists(deviceUUID string) bool {
	// Check if device socket exists
	socketDir := util.GetSocketDir()
	socketPath := filepath.Join(socketDir, fmt.Sprintf("auraphone-%s.sock", deviceUUID))
	_, err := os.Stat(socketPath)
	return err == nil
}

// GetSimulator returns a stub simulator (stub for old API)
// TODO Step 4: Remove simulator dependency from swift layer
func (w *Wire) GetSimulator() *SimulatorStub {
	return &SimulatorStub{
		ServiceDiscoveryDelay: 100 * time.Millisecond,
	}
}
