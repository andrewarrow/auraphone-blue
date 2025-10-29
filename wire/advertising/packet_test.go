package advertising

import (
	"bytes"
	"testing"

	"github.com/user/auraphone-blue/util"
)

func TestAdvertisingPDUEncodeDecodeRoundTrip(t *testing.T) {
	util.SetRandom()

	// Create a simple advertising PDU
	originalPDU := &AdvertisingPDU{
		PDUType: PDUTypeAdvInd,
		AdvA:    [6]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF},
		AdvData: []byte{0x02, 0x01, 0x06}, // Flags AD structure
	}

	// Encode
	encoded, err := originalPDU.Encode()
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Expected format: [PDU Type: 1] [Length: 1] [AdvA: 6] [AdvData: 3]
	expectedLen := 2 + 6 + 3
	if len(encoded) != expectedLen {
		t.Errorf("Expected encoded length %d, got %d", expectedLen, len(encoded))
	}

	// Decode
	decodedPDU, err := DecodeAdvertisingPDU(encoded)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	// Verify
	if decodedPDU.PDUType != originalPDU.PDUType {
		t.Errorf("PDU type mismatch: expected 0x%02X, got 0x%02X", originalPDU.PDUType, decodedPDU.PDUType)
	}
	if decodedPDU.AdvA != originalPDU.AdvA {
		t.Errorf("Address mismatch: expected %v, got %v", originalPDU.AdvA, decodedPDU.AdvA)
	}
	if !bytes.Equal(decodedPDU.AdvData, originalPDU.AdvData) {
		t.Errorf("AdvData mismatch: expected %v, got %v", originalPDU.AdvData, decodedPDU.AdvData)
	}
}

func TestAdvertisingPDUMaxLength(t *testing.T) {
	util.SetRandom()

	// Create PDU with maximum advertising data length
	maxAdvData := make([]byte, MaxAdvertisingDataLen)
	for i := range maxAdvData {
		maxAdvData[i] = byte(i)
	}

	pdu := &AdvertisingPDU{
		PDUType: PDUTypeAdvInd,
		AdvA:    [6]byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66},
		AdvData: maxAdvData,
	}

	// Should succeed
	encoded, err := pdu.Encode()
	if err != nil {
		t.Fatalf("Encode with max length failed: %v", err)
	}

	// Decode should also succeed
	decodedPDU, err := DecodeAdvertisingPDU(encoded)
	if err != nil {
		t.Fatalf("Decode with max length failed: %v", err)
	}

	if !bytes.Equal(decodedPDU.AdvData, maxAdvData) {
		t.Errorf("AdvData mismatch after round-trip with max length")
	}
}

func TestAdvertisingPDUExceedsMaxLength(t *testing.T) {
	util.SetRandom()

	// Create PDU with advertising data exceeding max length
	tooLongAdvData := make([]byte, MaxAdvertisingDataLen+1)

	pdu := &AdvertisingPDU{
		PDUType: PDUTypeAdvInd,
		AdvA:    [6]byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66},
		AdvData: tooLongAdvData,
	}

	// Should fail
	_, err := pdu.Encode()
	if err == nil {
		t.Fatalf("Expected error when encoding PDU exceeding max length, got nil")
	}
}

func TestAdvertisingPDUEmptyAdvData(t *testing.T) {
	util.SetRandom()

	// Create PDU with no advertising data
	pdu := &AdvertisingPDU{
		PDUType: PDUTypeAdvNonconnInd,
		AdvA:    [6]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF},
		AdvData: nil,
	}

	// Encode
	encoded, err := pdu.Encode()
	if err != nil {
		t.Fatalf("Encode with empty AdvData failed: %v", err)
	}

	// Expected: [PDU Type: 1] [Length: 1] [AdvA: 6]
	expectedLen := 2 + 6
	if len(encoded) != expectedLen {
		t.Errorf("Expected encoded length %d, got %d", expectedLen, len(encoded))
	}

	// Decode
	decodedPDU, err := DecodeAdvertisingPDU(encoded)
	if err != nil {
		t.Fatalf("Decode with empty AdvData failed: %v", err)
	}

	if len(decodedPDU.AdvData) != 0 {
		t.Errorf("Expected empty AdvData, got %d bytes", len(decodedPDU.AdvData))
	}
}

func TestADStructuresEncodeDecodeRoundTrip(t *testing.T) {
	util.SetRandom()

	// Create multiple AD structures
	structures := []ADStructure{
		NewFlagsAD(FlagLEGeneralDiscoverableMode | FlagBREDRNotSupported),
		NewCompleteLocalNameAD("TestDevice"),
		NewComplete16BitServiceUUIDsAD([]uint16{0x180A, 0x180F}),
		NewTxPowerLevelAD(-10),
	}

	// Encode
	encoded, err := EncodeADStructures(structures)
	if err != nil {
		t.Fatalf("EncodeADStructures failed: %v", err)
	}

	// Decode
	decoded, err := DecodeADStructures(encoded)
	if err != nil {
		t.Fatalf("DecodeADStructures failed: %v", err)
	}

	// Verify count
	if len(decoded) != len(structures) {
		t.Fatalf("Expected %d structures, got %d", len(structures), len(decoded))
	}

	// Verify each structure
	for i, expected := range structures {
		got := decoded[i]
		if got.Type != expected.Type {
			t.Errorf("Structure %d type mismatch: expected 0x%02X, got 0x%02X", i, expected.Type, got.Type)
		}
		if !bytes.Equal(got.Data, expected.Data) {
			t.Errorf("Structure %d data mismatch: expected %v, got %v", i, expected.Data, got.Data)
		}
	}
}

func TestADStructuresTooLong(t *testing.T) {
	util.SetRandom()

	// Create structures that exceed the max advertising data length
	longName := string(make([]byte, MaxAdvertisingDataLen))
	structures := []ADStructure{
		NewCompleteLocalNameAD(longName),
	}

	// Should fail
	_, err := EncodeADStructures(structures)
	if err == nil {
		t.Fatalf("Expected error when encoding structures exceeding max length, got nil")
	}
}

func TestADStructureSingleTooLong(t *testing.T) {
	util.SetRandom()

	// Create a single structure that's too long (length would be > 255)
	tooLongData := make([]byte, 255)
	structures := []ADStructure{
		{
			Type: ADTypeManufacturerSpecificData,
			Data: tooLongData,
		},
	}

	// Should fail
	_, err := EncodeADStructures(structures)
	if err == nil {
		t.Fatalf("Expected error when encoding single structure exceeding 255 bytes, got nil")
	}
}

func TestNewFlagsAD(t *testing.T) {
	util.SetRandom()

	flags := byte(FlagLEGeneralDiscoverableMode | FlagBREDRNotSupported)
	ad := NewFlagsAD(flags)

	if ad.Type != ADTypeFlags {
		t.Errorf("Expected type 0x%02X, got 0x%02X", ADTypeFlags, ad.Type)
	}
	if len(ad.Data) != 1 || ad.Data[0] != flags {
		t.Errorf("Expected data [0x%02X], got %v", flags, ad.Data)
	}
}

func TestNewCompleteLocalNameAD(t *testing.T) {
	util.SetRandom()

	name := "AuraPhone"
	ad := NewCompleteLocalNameAD(name)

	if ad.Type != ADTypeCompleteLocalName {
		t.Errorf("Expected type 0x%02X, got 0x%02X", ADTypeCompleteLocalName, ad.Type)
	}
	if string(ad.Data) != name {
		t.Errorf("Expected name %q, got %q", name, string(ad.Data))
	}
}

func TestNewComplete16BitServiceUUIDsAD(t *testing.T) {
	util.SetRandom()

	uuids := []uint16{0x180A, 0x180F, 0x1812}
	ad := NewComplete16BitServiceUUIDsAD(uuids)

	if ad.Type != ADTypeComplete16BitServiceUUIDs {
		t.Errorf("Expected type 0x%02X, got 0x%02X", ADTypeComplete16BitServiceUUIDs, ad.Type)
	}
	if len(ad.Data) != len(uuids)*2 {
		t.Errorf("Expected data length %d, got %d", len(uuids)*2, len(ad.Data))
	}
}

func TestNewComplete128BitServiceUUIDsAD(t *testing.T) {
	util.SetRandom()

	uuid1 := [16]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10}
	uuid2 := [16]byte{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1A, 0x1B, 0x1C, 0x1D, 0x1E, 0x1F, 0x20}
	uuids := [][16]byte{uuid1, uuid2}

	ad := NewComplete128BitServiceUUIDsAD(uuids)

	if ad.Type != ADTypeComplete128BitServiceUUIDs {
		t.Errorf("Expected type 0x%02X, got 0x%02X", ADTypeComplete128BitServiceUUIDs, ad.Type)
	}
	if len(ad.Data) != len(uuids)*16 {
		t.Errorf("Expected data length %d, got %d", len(uuids)*16, len(ad.Data))
	}
}

func TestNewTxPowerLevelAD(t *testing.T) {
	util.SetRandom()

	powerLevel := int8(-20)
	ad := NewTxPowerLevelAD(powerLevel)

	if ad.Type != ADTypeTxPowerLevel {
		t.Errorf("Expected type 0x%02X, got 0x%02X", ADTypeTxPowerLevel, ad.Type)
	}
	if len(ad.Data) != 1 || int8(ad.Data[0]) != powerLevel {
		t.Errorf("Expected power level %d, got %d", powerLevel, int8(ad.Data[0]))
	}
}

func TestNewManufacturerSpecificDataAD(t *testing.T) {
	util.SetRandom()

	companyID := uint16(0x004C) // Apple
	data := []byte{0x01, 0x02, 0x03, 0x04}
	ad := NewManufacturerSpecificDataAD(companyID, data)

	if ad.Type != ADTypeManufacturerSpecificData {
		t.Errorf("Expected type 0x%02X, got 0x%02X", ADTypeManufacturerSpecificData, ad.Type)
	}
	if len(ad.Data) != 2+len(data) {
		t.Errorf("Expected data length %d, got %d", 2+len(data), len(ad.Data))
	}
}

func TestGetLocalName(t *testing.T) {
	util.SetRandom()

	structures := []ADStructure{
		NewFlagsAD(FlagLEGeneralDiscoverableMode),
		NewCompleteLocalNameAD("MyDevice"),
	}

	name := GetLocalName(structures)
	if name != "MyDevice" {
		t.Errorf("Expected name 'MyDevice', got %q", name)
	}

	// Test with no name
	structuresNoName := []ADStructure{
		NewFlagsAD(FlagLEGeneralDiscoverableMode),
	}
	nameEmpty := GetLocalName(structuresNoName)
	if nameEmpty != "" {
		t.Errorf("Expected empty name, got %q", nameEmpty)
	}
}

func TestGetFlags(t *testing.T) {
	util.SetRandom()

	expectedFlags := byte(FlagLEGeneralDiscoverableMode | FlagBREDRNotSupported)
	structures := []ADStructure{
		NewFlagsAD(expectedFlags),
		NewCompleteLocalNameAD("Test"),
	}

	flags, found := GetFlags(structures)
	if !found {
		t.Fatalf("Expected to find flags, but not found")
	}
	if flags != expectedFlags {
		t.Errorf("Expected flags 0x%02X, got 0x%02X", expectedFlags, flags)
	}

	// Test with no flags
	structuresNoFlags := []ADStructure{
		NewCompleteLocalNameAD("Test"),
	}
	_, foundNoFlags := GetFlags(structuresNoFlags)
	if foundNoFlags {
		t.Errorf("Expected no flags, but found some")
	}
}

func TestGet16BitServiceUUIDs(t *testing.T) {
	util.SetRandom()

	expectedUUIDs := []uint16{0x180A, 0x180F, 0x1812}
	structures := []ADStructure{
		NewComplete16BitServiceUUIDsAD(expectedUUIDs),
	}

	uuids := Get16BitServiceUUIDs(structures)
	if len(uuids) != len(expectedUUIDs) {
		t.Fatalf("Expected %d UUIDs, got %d", len(expectedUUIDs), len(uuids))
	}

	for i, expected := range expectedUUIDs {
		if uuids[i] != expected {
			t.Errorf("UUID %d mismatch: expected 0x%04X, got 0x%04X", i, expected, uuids[i])
		}
	}
}

func TestGet128BitServiceUUIDs(t *testing.T) {
	util.SetRandom()

	uuid1 := [16]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10}
	expectedUUIDs := [][16]byte{uuid1}
	structures := []ADStructure{
		NewComplete128BitServiceUUIDsAD(expectedUUIDs),
	}

	uuids := Get128BitServiceUUIDs(structures)
	if len(uuids) != len(expectedUUIDs) {
		t.Fatalf("Expected %d UUIDs, got %d", len(expectedUUIDs), len(uuids))
	}

	if uuids[0] != uuid1 {
		t.Errorf("UUID mismatch: expected %v, got %v", uuid1, uuids[0])
	}
}

func TestGetManufacturerData(t *testing.T) {
	util.SetRandom()

	expectedCompanyID := uint16(0x004C)
	expectedData := []byte{0xAA, 0xBB, 0xCC}
	structures := []ADStructure{
		NewManufacturerSpecificDataAD(expectedCompanyID, expectedData),
	}

	companyID, data, found := GetManufacturerData(structures)
	if !found {
		t.Fatalf("Expected to find manufacturer data, but not found")
	}
	if companyID != expectedCompanyID {
		t.Errorf("Expected company ID 0x%04X, got 0x%04X", expectedCompanyID, companyID)
	}
	if !bytes.Equal(data, expectedData) {
		t.Errorf("Expected data %v, got %v", expectedData, data)
	}

	// Test with no manufacturer data
	structuresNoMfg := []ADStructure{
		NewCompleteLocalNameAD("Test"),
	}
	_, _, foundNoMfg := GetManufacturerData(structuresNoMfg)
	if foundNoMfg {
		t.Errorf("Expected no manufacturer data, but found some")
	}
}

func TestPDUTypeName(t *testing.T) {
	util.SetRandom()

	tests := []struct {
		pduType byte
		name    string
	}{
		{PDUTypeAdvInd, "ADV_IND"},
		{PDUTypeAdvDirectInd, "ADV_DIRECT_IND"},
		{PDUTypeAdvNonconnInd, "ADV_NONCONN_IND"},
		{PDUTypeScanReq, "SCAN_REQ"},
		{PDUTypeScanRsp, "SCAN_RSP"},
		{PDUTypeConnectReq, "CONNECT_REQ"},
		{PDUTypeAdvScanInd, "ADV_SCAN_IND"},
		{0xFF, "Unknown(0xFF)"},
	}

	for _, tt := range tests {
		name := PDUTypeName(tt.pduType)
		if name != tt.name {
			t.Errorf("PDUTypeName(0x%02X): expected %q, got %q", tt.pduType, tt.name, name)
		}
	}
}

func TestADTypeName(t *testing.T) {
	util.SetRandom()

	tests := []struct {
		adType byte
		name   string
	}{
		{ADTypeFlags, "Flags"},
		{ADTypeIncomplete16BitServiceUUIDs, "Incomplete 16-bit Service UUIDs"},
		{ADTypeComplete16BitServiceUUIDs, "Complete 16-bit Service UUIDs"},
		{ADTypeCompleteLocalName, "Complete Local Name"},
		{ADTypeTxPowerLevel, "Tx Power Level"},
		{ADTypeManufacturerSpecificData, "Manufacturer Specific Data"},
		{0xFF, "Manufacturer Specific Data"},
		{0xAA, "Unknown(0xAA)"},
	}

	for _, tt := range tests {
		name := ADTypeName(tt.adType)
		if name != tt.name {
			t.Errorf("ADTypeName(0x%02X): expected %q, got %q", tt.adType, tt.name, name)
		}
	}
}

func TestDecodeAdvertisingPDUTooShort(t *testing.T) {
	util.SetRandom()

	// Test with data that's too short
	shortData := []byte{0x00, 0x01, 0xAA}
	_, err := DecodeAdvertisingPDU(shortData)
	if err == nil {
		t.Fatalf("Expected error decoding short PDU, got nil")
	}
}

func TestDecodeAdvertisingPDUInvalidPayloadLength(t *testing.T) {
	util.SetRandom()

	// Test with payload length less than address length
	invalidData := []byte{0x00, 0x05, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE}
	_, err := DecodeAdvertisingPDU(invalidData)
	if err == nil {
		t.Fatalf("Expected error decoding PDU with invalid payload length, got nil")
	}
}

func TestDecodeAdvertisingPDUTruncated(t *testing.T) {
	util.SetRandom()

	// Test with truncated data (header says more data than available)
	truncatedData := []byte{0x00, 0x10, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}
	_, err := DecodeAdvertisingPDU(truncatedData)
	if err == nil {
		t.Fatalf("Expected error decoding truncated PDU, got nil")
	}
}

func TestDecodeADStructuresInvalidLength(t *testing.T) {
	util.SetRandom()

	// Test with AD structure that claims more data than available
	invalidData := []byte{0x10, 0x09, 0x41, 0x75, 0x72, 0x61} // Length=16 but only 6 bytes total
	_, err := DecodeADStructures(invalidData)
	if err == nil {
		t.Fatalf("Expected error decoding invalid AD structures, got nil")
	}
}

func TestComplexAdvertisingPacket(t *testing.T) {
	util.SetRandom()

	// Create a realistic advertising packet with multiple AD structures
	structures := []ADStructure{
		NewFlagsAD(FlagLEGeneralDiscoverableMode | FlagBREDRNotSupported),
		NewComplete16BitServiceUUIDsAD([]uint16{0x180A, 0x180F}),
		NewCompleteLocalNameAD("AuraPhone"),
		NewTxPowerLevelAD(-10),
		NewManufacturerSpecificDataAD(0x004C, []byte{0x01, 0x02, 0x03}),
	}

	// Encode AD structures
	advData, err := EncodeADStructures(structures)
	if err != nil {
		t.Fatalf("EncodeADStructures failed: %v", err)
	}

	// Create PDU
	pdu := &AdvertisingPDU{
		PDUType: PDUTypeAdvInd,
		AdvA:    [6]byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66},
		AdvData: advData,
	}

	// Encode PDU
	encoded, err := pdu.Encode()
	if err != nil {
		t.Fatalf("Encode PDU failed: %v", err)
	}

	// Decode PDU
	decodedPDU, err := DecodeAdvertisingPDU(encoded)
	if err != nil {
		t.Fatalf("Decode PDU failed: %v", err)
	}

	// Decode AD structures
	decodedStructures, err := DecodeADStructures(decodedPDU.AdvData)
	if err != nil {
		t.Fatalf("DecodeADStructures failed: %v", err)
	}

	// Verify all extracted data
	name := GetLocalName(decodedStructures)
	if name != "AuraPhone" {
		t.Errorf("Expected name 'AuraPhone', got %q", name)
	}

	flags, found := GetFlags(decodedStructures)
	if !found || flags != (FlagLEGeneralDiscoverableMode|FlagBREDRNotSupported) {
		t.Errorf("Flags mismatch")
	}

	uuids := Get16BitServiceUUIDs(decodedStructures)
	if len(uuids) != 2 || uuids[0] != 0x180A || uuids[1] != 0x180F {
		t.Errorf("Service UUIDs mismatch: got %v", uuids)
	}

	companyID, mfgData, found := GetManufacturerData(decodedStructures)
	if !found || companyID != 0x004C || !bytes.Equal(mfgData, []byte{0x01, 0x02, 0x03}) {
		t.Errorf("Manufacturer data mismatch")
	}
}
