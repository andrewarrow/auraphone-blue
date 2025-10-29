package att

import (
	"bytes"
	"testing"

	"github.com/user/auraphone-blue/util"
)

func TestEncodeDecodeExchangeMTU(t *testing.T) {
	util.SetRandom()

	// Test MTU Request
	req := &ExchangeMTURequest{ClientRxMTU: 517}
	encoded, err := EncodePacket(req)
	if err != nil {
		t.Fatalf("EncodePacket failed: %v", err)
	}

	expectedReq := []byte{0x02, 0x05, 0x02} // opcode, MTU (517 = 0x0205 little-endian)
	if !bytes.Equal(encoded, expectedReq) {
		t.Errorf("Encoded = %v, want %v", encoded, expectedReq)
	}

	decoded, err := DecodePacket(encoded)
	if err != nil {
		t.Fatalf("DecodePacket failed: %v", err)
	}

	decodedReq, ok := decoded.(*ExchangeMTURequest)
	if !ok {
		t.Fatalf("Decoded type = %T, want *ExchangeMTURequest", decoded)
	}

	if decodedReq.ClientRxMTU != req.ClientRxMTU {
		t.Errorf("ClientRxMTU = %d, want %d", decodedReq.ClientRxMTU, req.ClientRxMTU)
	}

	// Test MTU Response
	resp := &ExchangeMTUResponse{ServerRxMTU: 247}
	encoded, err = EncodePacket(resp)
	if err != nil {
		t.Fatalf("EncodePacket failed: %v", err)
	}

	decoded, err = DecodePacket(encoded)
	if err != nil {
		t.Fatalf("DecodePacket failed: %v", err)
	}

	decodedResp, ok := decoded.(*ExchangeMTUResponse)
	if !ok {
		t.Fatalf("Decoded type = %T, want *ExchangeMTUResponse", decoded)
	}

	if decodedResp.ServerRxMTU != resp.ServerRxMTU {
		t.Errorf("ServerRxMTU = %d, want %d", decodedResp.ServerRxMTU, resp.ServerRxMTU)
	}
}

func TestEncodeDecodeErrorResponse(t *testing.T) {
	util.SetRandom()

	errResp := &ErrorResponse{
		RequestOpcode: OpReadRequest,
		Handle:        0x0003,
		ErrorCode:     ErrReadNotPermitted,
	}

	encoded, err := EncodePacket(errResp)
	if err != nil {
		t.Fatalf("EncodePacket failed: %v", err)
	}

	decoded, err := DecodePacket(encoded)
	if err != nil {
		t.Fatalf("DecodePacket failed: %v", err)
	}

	decodedErr, ok := decoded.(*ErrorResponse)
	if !ok {
		t.Fatalf("Decoded type = %T, want *ErrorResponse", decoded)
	}

	if decodedErr.RequestOpcode != errResp.RequestOpcode {
		t.Errorf("RequestOpcode = 0x%02X, want 0x%02X", decodedErr.RequestOpcode, errResp.RequestOpcode)
	}

	if decodedErr.Handle != errResp.Handle {
		t.Errorf("Handle = 0x%04X, want 0x%04X", decodedErr.Handle, errResp.Handle)
	}

	if decodedErr.ErrorCode != errResp.ErrorCode {
		t.Errorf("ErrorCode = 0x%02X, want 0x%02X", decodedErr.ErrorCode, errResp.ErrorCode)
	}
}

func TestEncodeDecodeRead(t *testing.T) {
	util.SetRandom()

	// Test Read Request
	req := &ReadRequest{Handle: 0x000A}
	encoded, err := EncodePacket(req)
	if err != nil {
		t.Fatalf("EncodePacket failed: %v", err)
	}

	decoded, err := DecodePacket(encoded)
	if err != nil {
		t.Fatalf("DecodePacket failed: %v", err)
	}

	decodedReq, ok := decoded.(*ReadRequest)
	if !ok {
		t.Fatalf("Decoded type = %T, want *ReadRequest", decoded)
	}

	if decodedReq.Handle != req.Handle {
		t.Errorf("Handle = 0x%04X, want 0x%04X", decodedReq.Handle, req.Handle)
	}

	// Test Read Response
	resp := &ReadResponse{Value: []byte{0x48, 0x65, 0x6C, 0x6C, 0x6F}} // "Hello"
	encoded, err = EncodePacket(resp)
	if err != nil {
		t.Fatalf("EncodePacket failed: %v", err)
	}

	decoded, err = DecodePacket(encoded)
	if err != nil {
		t.Fatalf("DecodePacket failed: %v", err)
	}

	decodedResp, ok := decoded.(*ReadResponse)
	if !ok {
		t.Fatalf("Decoded type = %T, want *ReadResponse", decoded)
	}

	if !bytes.Equal(decodedResp.Value, resp.Value) {
		t.Errorf("Value = %v, want %v", decodedResp.Value, resp.Value)
	}
}

func TestEncodeDecodeWrite(t *testing.T) {
	util.SetRandom()

	// Test Write Request
	req := &WriteRequest{
		Handle: 0x000B,
		Value:  []byte{0x01, 0x00}, // Enable notifications
	}

	encoded, err := EncodePacket(req)
	if err != nil {
		t.Fatalf("EncodePacket failed: %v", err)
	}

	decoded, err := DecodePacket(encoded)
	if err != nil {
		t.Fatalf("DecodePacket failed: %v", err)
	}

	decodedReq, ok := decoded.(*WriteRequest)
	if !ok {
		t.Fatalf("Decoded type = %T, want *WriteRequest", decoded)
	}

	if decodedReq.Handle != req.Handle {
		t.Errorf("Handle = 0x%04X, want 0x%04X", decodedReq.Handle, req.Handle)
	}

	if !bytes.Equal(decodedReq.Value, req.Value) {
		t.Errorf("Value = %v, want %v", decodedReq.Value, req.Value)
	}

	// Test Write Response
	resp := &WriteResponse{}
	encoded, err = EncodePacket(resp)
	if err != nil {
		t.Fatalf("EncodePacket failed: %v", err)
	}

	if !bytes.Equal(encoded, []byte{OpWriteResponse}) {
		t.Errorf("Encoded = %v, want %v", encoded, []byte{OpWriteResponse})
	}

	decoded, err = DecodePacket(encoded)
	if err != nil {
		t.Fatalf("DecodePacket failed: %v", err)
	}

	_, ok = decoded.(*WriteResponse)
	if !ok {
		t.Fatalf("Decoded type = %T, want *WriteResponse", decoded)
	}
}

func TestEncodeDecodeWriteCommand(t *testing.T) {
	util.SetRandom()

	cmd := &WriteCommand{
		Handle: 0x000C,
		Value:  []byte{0xAA, 0xBB, 0xCC},
	}

	encoded, err := EncodePacket(cmd)
	if err != nil {
		t.Fatalf("EncodePacket failed: %v", err)
	}

	decoded, err := DecodePacket(encoded)
	if err != nil {
		t.Fatalf("DecodePacket failed: %v", err)
	}

	decodedCmd, ok := decoded.(*WriteCommand)
	if !ok {
		t.Fatalf("Decoded type = %T, want *WriteCommand", decoded)
	}

	if decodedCmd.Handle != cmd.Handle {
		t.Errorf("Handle = 0x%04X, want 0x%04X", decodedCmd.Handle, cmd.Handle)
	}

	if !bytes.Equal(decodedCmd.Value, cmd.Value) {
		t.Errorf("Value = %v, want %v", decodedCmd.Value, cmd.Value)
	}
}

func TestEncodeDecodeNotification(t *testing.T) {
	util.SetRandom()

	notif := &HandleValueNotification{
		Handle: 0x000D,
		Value:  []byte{0x01, 0x02, 0x03, 0x04},
	}

	encoded, err := EncodePacket(notif)
	if err != nil {
		t.Fatalf("EncodePacket failed: %v", err)
	}

	decoded, err := DecodePacket(encoded)
	if err != nil {
		t.Fatalf("DecodePacket failed: %v", err)
	}

	decodedNotif, ok := decoded.(*HandleValueNotification)
	if !ok {
		t.Fatalf("Decoded type = %T, want *HandleValueNotification", decoded)
	}

	if decodedNotif.Handle != notif.Handle {
		t.Errorf("Handle = 0x%04X, want 0x%04X", decodedNotif.Handle, notif.Handle)
	}

	if !bytes.Equal(decodedNotif.Value, notif.Value) {
		t.Errorf("Value = %v, want %v", decodedNotif.Value, notif.Value)
	}
}

func TestEncodeDecodeReadByType(t *testing.T) {
	util.SetRandom()

	// Test with 16-bit UUID
	uuid16 := []byte{0x03, 0x28} // Characteristic UUID

	req := &ReadByTypeRequest{
		StartHandle: 0x0001,
		EndHandle:   0xFFFF,
		Type:        uuid16,
	}

	encoded, err := EncodePacket(req)
	if err != nil {
		t.Fatalf("EncodePacket failed: %v", err)
	}

	decoded, err := DecodePacket(encoded)
	if err != nil {
		t.Fatalf("DecodePacket failed: %v", err)
	}

	decodedReq, ok := decoded.(*ReadByTypeRequest)
	if !ok {
		t.Fatalf("Decoded type = %T, want *ReadByTypeRequest", decoded)
	}

	if decodedReq.StartHandle != req.StartHandle {
		t.Errorf("StartHandle = 0x%04X, want 0x%04X", decodedReq.StartHandle, req.StartHandle)
	}

	if decodedReq.EndHandle != req.EndHandle {
		t.Errorf("EndHandle = 0x%04X, want 0x%04X", decodedReq.EndHandle, req.EndHandle)
	}

	if !bytes.Equal(decodedReq.Type, req.Type) {
		t.Errorf("Type = %v, want %v", decodedReq.Type, req.Type)
	}
}

func TestEncodeDecodeReadByGroupType(t *testing.T) {
	util.SetRandom()

	// Primary Service UUID (16-bit)
	primaryServiceUUID := []byte{0x00, 0x28}

	req := &ReadByGroupTypeRequest{
		StartHandle: 0x0001,
		EndHandle:   0xFFFF,
		Type:        primaryServiceUUID,
	}

	encoded, err := EncodePacket(req)
	if err != nil {
		t.Fatalf("EncodePacket failed: %v", err)
	}

	decoded, err := DecodePacket(encoded)
	if err != nil {
		t.Fatalf("DecodePacket failed: %v", err)
	}

	decodedReq, ok := decoded.(*ReadByGroupTypeRequest)
	if !ok {
		t.Fatalf("Decoded type = %T, want *ReadByGroupTypeRequest", decoded)
	}

	if decodedReq.StartHandle != req.StartHandle {
		t.Errorf("StartHandle = 0x%04X, want 0x%04X", decodedReq.StartHandle, req.StartHandle)
	}

	if decodedReq.EndHandle != req.EndHandle {
		t.Errorf("EndHandle = 0x%04X, want 0x%04X", decodedReq.EndHandle, req.EndHandle)
	}

	if !bytes.Equal(decodedReq.Type, req.Type) {
		t.Errorf("Type = %v, want %v", decodedReq.Type, req.Type)
	}
}

func TestEncodeDecodeFindInformation(t *testing.T) {
	util.SetRandom()

	req := &FindInformationRequest{
		StartHandle: 0x0001,
		EndHandle:   0x0010,
	}

	encoded, err := EncodePacket(req)
	if err != nil {
		t.Fatalf("EncodePacket failed: %v", err)
	}

	decoded, err := DecodePacket(encoded)
	if err != nil {
		t.Fatalf("DecodePacket failed: %v", err)
	}

	decodedReq, ok := decoded.(*FindInformationRequest)
	if !ok {
		t.Fatalf("Decoded type = %T, want *FindInformationRequest", decoded)
	}

	if decodedReq.StartHandle != req.StartHandle {
		t.Errorf("StartHandle = 0x%04X, want 0x%04X", decodedReq.StartHandle, req.StartHandle)
	}

	if decodedReq.EndHandle != req.EndHandle {
		t.Errorf("EndHandle = 0x%04X, want 0x%04X", decodedReq.EndHandle, req.EndHandle)
	}
}

func TestEncodeDecodePrepareWrite(t *testing.T) {
	util.SetRandom()

	req := &PrepareWriteRequest{
		Handle: 0x0020,
		Offset: 0x0005,
		Value:  []byte{0x01, 0x02, 0x03},
	}

	encoded, err := EncodePacket(req)
	if err != nil {
		t.Fatalf("EncodePacket failed: %v", err)
	}

	decoded, err := DecodePacket(encoded)
	if err != nil {
		t.Fatalf("DecodePacket failed: %v", err)
	}

	decodedReq, ok := decoded.(*PrepareWriteRequest)
	if !ok {
		t.Fatalf("Decoded type = %T, want *PrepareWriteRequest", decoded)
	}

	if decodedReq.Handle != req.Handle {
		t.Errorf("Handle = 0x%04X, want 0x%04X", decodedReq.Handle, req.Handle)
	}

	if decodedReq.Offset != req.Offset {
		t.Errorf("Offset = 0x%04X, want 0x%04X", decodedReq.Offset, req.Offset)
	}

	if !bytes.Equal(decodedReq.Value, req.Value) {
		t.Errorf("Value = %v, want %v", decodedReq.Value, req.Value)
	}
}

func TestEncodeDecodeExecuteWrite(t *testing.T) {
	util.SetRandom()

	req := &ExecuteWriteRequest{Flags: 0x01}

	encoded, err := EncodePacket(req)
	if err != nil {
		t.Fatalf("EncodePacket failed: %v", err)
	}

	decoded, err := DecodePacket(encoded)
	if err != nil {
		t.Fatalf("DecodePacket failed: %v", err)
	}

	decodedReq, ok := decoded.(*ExecuteWriteRequest)
	if !ok {
		t.Fatalf("Decoded type = %T, want *ExecuteWriteRequest", decoded)
	}

	if decodedReq.Flags != req.Flags {
		t.Errorf("Flags = 0x%02X, want 0x%02X", decodedReq.Flags, req.Flags)
	}
}

func TestDecodeErrors(t *testing.T) {
	util.SetRandom()

	tests := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{
			name:    "empty packet",
			data:    []byte{},
			wantErr: true,
		},
		{
			name:    "unknown opcode",
			data:    []byte{0xFF},
			wantErr: true,
		},
		{
			name:    "truncated MTU request",
			data:    []byte{0x02, 0x05}, // Missing one byte
			wantErr: true,
		},
		{
			name:    "truncated error response",
			data:    []byte{0x01, 0x0A, 0x03}, // Missing handle and error code
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodePacket(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("DecodePacket() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
