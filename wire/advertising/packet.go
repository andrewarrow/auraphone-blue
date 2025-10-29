package advertising

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// PDU Types for BLE advertising packets (Link Layer)
const (
	PDUTypeAdvInd        = 0x00 // Connectable undirected advertising
	PDUTypeAdvDirectInd  = 0x01 // Connectable directed advertising
	PDUTypeAdvNonconnInd = 0x02 // Non-connectable undirected advertising
	PDUTypeScanReq       = 0x03 // Scan request
	PDUTypeScanRsp       = 0x04 // Scan response
	PDUTypeConnectReq    = 0x05 // Connection request
	PDUTypeAdvScanInd    = 0x06 // Scannable undirected advertising
)

// AD Types (Advertising Data Types) - EIR/AD format
const (
	ADTypeFlags                      = 0x01 // Flags
	ADTypeIncomplete16BitServiceUUIDs = 0x02 // Incomplete List of 16-bit Service UUIDs
	ADTypeComplete16BitServiceUUIDs   = 0x03 // Complete List of 16-bit Service UUIDs
	ADTypeIncomplete32BitServiceUUIDs = 0x04 // Incomplete List of 32-bit Service UUIDs
	ADTypeComplete32BitServiceUUIDs   = 0x05 // Complete List of 32-bit Service UUIDs
	ADTypeIncomplete128BitServiceUUIDs = 0x06 // Incomplete List of 128-bit Service UUIDs
	ADTypeComplete128BitServiceUUIDs   = 0x07 // Complete List of 128-bit Service UUIDs
	ADTypeShortenedLocalName          = 0x08 // Shortened Local Name
	ADTypeCompleteLocalName           = 0x09 // Complete Local Name
	ADTypeTxPowerLevel                = 0x0A // Tx Power Level
	ADTypeClassOfDevice               = 0x0D // Class of Device
	ADTypeSimplePairingHashC          = 0x0E // Simple Pairing Hash C
	ADTypeSimplePairingRandomizerR    = 0x0F // Simple Pairing Randomizer R
	ADTypeSecurityManagerTKValue      = 0x10 // Security Manager TK Value
	ADTypeSecurityManagerOOBFlags     = 0x11 // Security Manager Out of Band Flags
	ADTypeSlaveConnectionIntervalRange = 0x12 // Slave Connection Interval Range
	ADType16BitServiceSolicitationUUIDs = 0x14 // List of 16-bit Service Solicitation UUIDs
	ADType128BitServiceSolicitationUUIDs = 0x15 // List of 128-bit Service Solicitation UUIDs
	ADTypeServiceData16Bit            = 0x16 // Service Data - 16-bit UUID
	ADTypePublicTargetAddress         = 0x17 // Public Target Address
	ADTypeRandomTargetAddress         = 0x18 // Random Target Address
	ADTypeAppearance                  = 0x19 // Appearance
	ADTypeAdvertisingInterval         = 0x1A // Advertising Interval
	ADTypeLEBluetoothDeviceAddress    = 0x1B // LE Bluetooth Device Address
	ADTypeLERole                      = 0x1C // LE Role
	ADTypeServiceData32Bit            = 0x20 // Service Data - 32-bit UUID
	ADTypeServiceData128Bit           = 0x21 // Service Data - 128-bit UUID
	ADTypeURI                         = 0x24 // URI
	ADTypeManufacturerSpecificData    = 0xFF // Manufacturer Specific Data
)

// Advertising Flags (used in ADTypeFlags)
const (
	FlagLELimitedDiscoverableMode = 0x01 // LE Limited Discoverable Mode
	FlagLEGeneralDiscoverableMode = 0x02 // LE General Discoverable Mode
	FlagBREDRNotSupported         = 0x04 // BR/EDR Not Supported
	FlagSimultaneousLEBREDRController = 0x08 // Simultaneous LE and BR/EDR to Same Device Capable (Controller)
	FlagSimultaneousLEBREDRHost       = 0x10 // Simultaneous LE and BR/EDR to Same Device Capable (Host)
)

const (
	MaxAdvertisingDataLen = 31 // BLE 4.x advertising data limit
	BLEAddressLen         = 6  // Bluetooth address is 6 bytes
)

// AdvertisingPDU represents a BLE advertising packet at the Link Layer
// Format: [PDU Type: 1 byte] [Length: 1 byte] [AdvA: 6 bytes] [AdvData: 0-31 bytes]
type AdvertisingPDU struct {
	PDUType byte     // PDU type (ADV_IND, ADV_SCAN_IND, etc.)
	AdvA    [6]byte  // Advertiser's address (6 bytes)
	AdvData []byte   // Advertising data (0-31 bytes)
}

// ADStructure represents a single TLV (Type-Length-Value) structure in advertising data
// Format: [Length: 1 byte] [Type: 1 byte] [Data: N bytes]
// Note: Length includes the Type byte but not itself
type ADStructure struct {
	Type byte   // AD Type (flags, service UUIDs, etc.)
	Data []byte // AD Data
}

// Encode serializes the advertising PDU to binary format
func (pdu *AdvertisingPDU) Encode() ([]byte, error) {
	if len(pdu.AdvData) > MaxAdvertisingDataLen {
		return nil, fmt.Errorf("advertising data exceeds %d bytes: %d", MaxAdvertisingDataLen, len(pdu.AdvData))
	}

	// Header (2 bytes) + Address (6 bytes) + Data (variable)
	totalLen := 2 + BLEAddressLen + len(pdu.AdvData)
	buf := make([]byte, totalLen)

	buf[0] = pdu.PDUType
	buf[1] = byte(BLEAddressLen + len(pdu.AdvData)) // Length of payload (address + data)
	copy(buf[2:8], pdu.AdvA[:])
	copy(buf[8:], pdu.AdvData)

	return buf, nil
}

// Decode parses a binary advertising PDU
func DecodeAdvertisingPDU(data []byte) (*AdvertisingPDU, error) {
	if len(data) < 8 {
		return nil, errors.New("advertising PDU too short (minimum 8 bytes)")
	}

	pdu := &AdvertisingPDU{
		PDUType: data[0],
	}

	payloadLen := int(data[1])
	if payloadLen < BLEAddressLen {
		return nil, errors.New("invalid payload length (must be at least 6 for address)")
	}

	expectedLen := 2 + payloadLen
	if len(data) < expectedLen {
		return nil, fmt.Errorf("advertising PDU truncated: expected %d bytes, got %d", expectedLen, len(data))
	}

	copy(pdu.AdvA[:], data[2:8])
	advDataLen := payloadLen - BLEAddressLen
	if advDataLen > 0 {
		if advDataLen > MaxAdvertisingDataLen {
			return nil, fmt.Errorf("advertising data exceeds %d bytes: %d", MaxAdvertisingDataLen, advDataLen)
		}
		pdu.AdvData = make([]byte, advDataLen)
		copy(pdu.AdvData, data[8:8+advDataLen])
	}

	return pdu, nil
}

// EncodeADStructures encodes multiple AD structures into a single advertising data payload
func EncodeADStructures(structures []ADStructure) ([]byte, error) {
	var buf []byte

	for _, s := range structures {
		// Length = 1 (type byte) + len(data)
		length := 1 + len(s.Data)
		if length > 255 {
			return nil, fmt.Errorf("AD structure too long: %d bytes (max 255)", length)
		}

		buf = append(buf, byte(length)) // Length byte
		buf = append(buf, s.Type)       // Type byte
		buf = append(buf, s.Data...)    // Data
	}

	if len(buf) > MaxAdvertisingDataLen {
		return nil, fmt.Errorf("total advertising data exceeds %d bytes: %d", MaxAdvertisingDataLen, len(buf))
	}

	return buf, nil
}

// DecodeADStructures parses advertising data into individual AD structures
func DecodeADStructures(data []byte) ([]ADStructure, error) {
	var structures []ADStructure
	offset := 0

	for offset < len(data) {
		if offset+1 > len(data) {
			// End of data
			break
		}

		length := int(data[offset])
		if length == 0 {
			// Padding or end of data
			break
		}

		offset++
		if offset+length > len(data) {
			return nil, fmt.Errorf("AD structure length exceeds data: length=%d, remaining=%d", length, len(data)-offset)
		}

		if length < 1 {
			return nil, errors.New("AD structure length must be at least 1")
		}

		adType := data[offset]
		offset++
		adData := make([]byte, length-1)
		copy(adData, data[offset:offset+length-1])
		offset += length - 1

		structures = append(structures, ADStructure{
			Type: adType,
			Data: adData,
		})
	}

	return structures, nil
}

// Helper functions for common AD structures

// NewFlagsAD creates a flags AD structure
func NewFlagsAD(flags byte) ADStructure {
	return ADStructure{
		Type: ADTypeFlags,
		Data: []byte{flags},
	}
}

// NewCompleteLocalNameAD creates a complete local name AD structure
func NewCompleteLocalNameAD(name string) ADStructure {
	return ADStructure{
		Type: ADTypeCompleteLocalName,
		Data: []byte(name),
	}
}

// NewShortenedLocalNameAD creates a shortened local name AD structure
func NewShortenedLocalNameAD(name string) ADStructure {
	return ADStructure{
		Type: ADTypeShortenedLocalName,
		Data: []byte(name),
	}
}

// NewComplete16BitServiceUUIDsAD creates a complete 16-bit service UUIDs AD structure
func NewComplete16BitServiceUUIDsAD(uuids []uint16) ADStructure {
	data := make([]byte, len(uuids)*2)
	for i, uuid := range uuids {
		binary.LittleEndian.PutUint16(data[i*2:], uuid)
	}
	return ADStructure{
		Type: ADTypeComplete16BitServiceUUIDs,
		Data: data,
	}
}

// NewComplete128BitServiceUUIDsAD creates a complete 128-bit service UUIDs AD structure
func NewComplete128BitServiceUUIDsAD(uuids [][16]byte) ADStructure {
	data := make([]byte, len(uuids)*16)
	for i, uuid := range uuids {
		copy(data[i*16:], uuid[:])
	}
	return ADStructure{
		Type: ADTypeComplete128BitServiceUUIDs,
		Data: data,
	}
}

// NewTxPowerLevelAD creates a Tx power level AD structure
func NewTxPowerLevelAD(powerLevel int8) ADStructure {
	return ADStructure{
		Type: ADTypeTxPowerLevel,
		Data: []byte{byte(powerLevel)},
	}
}

// NewManufacturerSpecificDataAD creates a manufacturer-specific data AD structure
func NewManufacturerSpecificDataAD(companyID uint16, data []byte) ADStructure {
	payload := make([]byte, 2+len(data))
	binary.LittleEndian.PutUint16(payload[0:2], companyID)
	copy(payload[2:], data)
	return ADStructure{
		Type: ADTypeManufacturerSpecificData,
		Data: payload,
	}
}

// GetLocalName extracts the local name from AD structures (complete or shortened)
func GetLocalName(structures []ADStructure) string {
	for _, s := range structures {
		if s.Type == ADTypeCompleteLocalName || s.Type == ADTypeShortenedLocalName {
			return string(s.Data)
		}
	}
	return ""
}

// GetFlags extracts the flags from AD structures
func GetFlags(structures []ADStructure) (byte, bool) {
	for _, s := range structures {
		if s.Type == ADTypeFlags && len(s.Data) > 0 {
			return s.Data[0], true
		}
	}
	return 0, false
}

// Get16BitServiceUUIDs extracts all 16-bit service UUIDs from AD structures
func Get16BitServiceUUIDs(structures []ADStructure) []uint16 {
	var uuids []uint16
	for _, s := range structures {
		if (s.Type == ADTypeComplete16BitServiceUUIDs || s.Type == ADTypeIncomplete16BitServiceUUIDs) && len(s.Data)%2 == 0 {
			for i := 0; i < len(s.Data); i += 2 {
				uuid := binary.LittleEndian.Uint16(s.Data[i : i+2])
				uuids = append(uuids, uuid)
			}
		}
	}
	return uuids
}

// Get128BitServiceUUIDs extracts all 128-bit service UUIDs from AD structures
func Get128BitServiceUUIDs(structures []ADStructure) [][16]byte {
	var uuids [][16]byte
	for _, s := range structures {
		if (s.Type == ADTypeComplete128BitServiceUUIDs || s.Type == ADTypeIncomplete128BitServiceUUIDs) && len(s.Data)%16 == 0 {
			for i := 0; i < len(s.Data); i += 16 {
				var uuid [16]byte
				copy(uuid[:], s.Data[i:i+16])
				uuids = append(uuids, uuid)
			}
		}
	}
	return uuids
}

// GetManufacturerData extracts manufacturer-specific data from AD structures
func GetManufacturerData(structures []ADStructure) (companyID uint16, data []byte, found bool) {
	for _, s := range structures {
		if s.Type == ADTypeManufacturerSpecificData && len(s.Data) >= 2 {
			companyID = binary.LittleEndian.Uint16(s.Data[0:2])
			data = s.Data[2:]
			found = true
			return
		}
	}
	return 0, nil, false
}

// PDUTypeName returns a human-readable name for a PDU type
func PDUTypeName(pduType byte) string {
	switch pduType {
	case PDUTypeAdvInd:
		return "ADV_IND"
	case PDUTypeAdvDirectInd:
		return "ADV_DIRECT_IND"
	case PDUTypeAdvNonconnInd:
		return "ADV_NONCONN_IND"
	case PDUTypeScanReq:
		return "SCAN_REQ"
	case PDUTypeScanRsp:
		return "SCAN_RSP"
	case PDUTypeConnectReq:
		return "CONNECT_REQ"
	case PDUTypeAdvScanInd:
		return "ADV_SCAN_IND"
	default:
		return fmt.Sprintf("Unknown(0x%02X)", pduType)
	}
}

// ADTypeName returns a human-readable name for an AD type
func ADTypeName(adType byte) string {
	switch adType {
	case ADTypeFlags:
		return "Flags"
	case ADTypeIncomplete16BitServiceUUIDs:
		return "Incomplete 16-bit Service UUIDs"
	case ADTypeComplete16BitServiceUUIDs:
		return "Complete 16-bit Service UUIDs"
	case ADTypeIncomplete128BitServiceUUIDs:
		return "Incomplete 128-bit Service UUIDs"
	case ADTypeComplete128BitServiceUUIDs:
		return "Complete 128-bit Service UUIDs"
	case ADTypeShortenedLocalName:
		return "Shortened Local Name"
	case ADTypeCompleteLocalName:
		return "Complete Local Name"
	case ADTypeTxPowerLevel:
		return "Tx Power Level"
	case ADTypeManufacturerSpecificData:
		return "Manufacturer Specific Data"
	case ADTypeAppearance:
		return "Appearance"
	default:
		return fmt.Sprintf("Unknown(0x%02X)", adType)
	}
}
