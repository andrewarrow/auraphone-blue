package l2cap

import (
	"testing"
)

func TestConnectionParametersValidation(t *testing.T) {
	tests := []struct {
		name    string
		params  *ConnectionParameters
		wantErr bool
	}{
		{
			name: "Valid default parameters",
			params: &ConnectionParameters{
				IntervalMin:        24,
				IntervalMax:        40,
				SlaveLatency:       0,
				SupervisionTimeout: 600,
			},
			wantErr: false,
		},
		{
			name: "Valid fast parameters",
			params: &ConnectionParameters{
				IntervalMin:        6,
				IntervalMax:        12,
				SlaveLatency:       0,
				SupervisionTimeout: 500,
			},
			wantErr: false,
		},
		{
			name: "Valid power saving parameters",
			params: &ConnectionParameters{
				IntervalMin:        80,
				IntervalMax:        160,
				SlaveLatency:       4,
				SupervisionTimeout: 600,
			},
			wantErr: false,
		},
		{
			name: "IntervalMin too small",
			params: &ConnectionParameters{
				IntervalMin:        5, // < 6
				IntervalMax:        40,
				SlaveLatency:       0,
				SupervisionTimeout: 600,
			},
			wantErr: true,
		},
		{
			name: "IntervalMax too large",
			params: &ConnectionParameters{
				IntervalMin:        24,
				IntervalMax:        3201, // > 3200
				SlaveLatency:       0,
				SupervisionTimeout: 600,
			},
			wantErr: true,
		},
		{
			name: "IntervalMax less than IntervalMin",
			params: &ConnectionParameters{
				IntervalMin:        40,
				IntervalMax:        24, // < IntervalMin
				SlaveLatency:       0,
				SupervisionTimeout: 600,
			},
			wantErr: true,
		},
		{
			name: "SlaveLatency too large",
			params: &ConnectionParameters{
				IntervalMin:        24,
				IntervalMax:        40,
				SlaveLatency:       500, // > 499
				SupervisionTimeout: 600,
			},
			wantErr: true,
		},
		{
			name: "SupervisionTimeout too small",
			params: &ConnectionParameters{
				IntervalMin:        24,
				IntervalMax:        40,
				SlaveLatency:       0,
				SupervisionTimeout: 9, // < 10
			},
			wantErr: true,
		},
		{
			name: "SupervisionTimeout too large",
			params: &ConnectionParameters{
				IntervalMin:        24,
				IntervalMax:        40,
				SlaveLatency:       0,
				SupervisionTimeout: 3201, // > 3200
			},
			wantErr: true,
		},
		{
			name: "SupervisionTimeout less than minimum required",
			params: &ConnectionParameters{
				IntervalMin:        40,
				IntervalMax:        40,
				SlaveLatency:       10,
				SupervisionTimeout: 10, // Too small for latency
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.params.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConnectionParametersConversions(t *testing.T) {
	params := &ConnectionParameters{
		IntervalMin:        24,  // 30ms
		IntervalMax:        40,  // 50ms
		SlaveLatency:       0,
		SupervisionTimeout: 600, // 6000ms
	}

	if got := params.IntervalMinMs(); got != 30.0 {
		t.Errorf("IntervalMinMs() = %v, want 30.0", got)
	}

	if got := params.IntervalMaxMs(); got != 50.0 {
		t.Errorf("IntervalMaxMs() = %v, want 50.0", got)
	}

	if got := params.SupervisionTimeoutMs(); got != 6000 {
		t.Errorf("SupervisionTimeoutMs() = %v, want 6000", got)
	}
}

func TestDefaultConnectionParameters(t *testing.T) {
	params := DefaultConnectionParameters()
	if err := params.Validate(); err != nil {
		t.Errorf("DefaultConnectionParameters() validation failed: %v", err)
	}

	if params.IntervalMinMs() < 7.5 || params.IntervalMinMs() > 100 {
		t.Errorf("DefaultConnectionParameters() IntervalMin out of typical range: %.2fms", params.IntervalMinMs())
	}
}

func TestFastConnectionParameters(t *testing.T) {
	params := FastConnectionParameters()
	if err := params.Validate(); err != nil {
		t.Errorf("FastConnectionParameters() validation failed: %v", err)
	}

	if params.IntervalMinMs() != 7.5 {
		t.Errorf("FastConnectionParameters() should use minimum interval: %.2fms", params.IntervalMinMs())
	}

	if params.SlaveLatency != 0 {
		t.Errorf("FastConnectionParameters() should have zero latency")
	}
}

func TestPowerSavingConnectionParameters(t *testing.T) {
	params := PowerSavingConnectionParameters()
	if err := params.Validate(); err != nil {
		t.Errorf("PowerSavingConnectionParameters() validation failed: %v", err)
	}

	if params.SlaveLatency == 0 {
		t.Errorf("PowerSavingConnectionParameters() should have non-zero latency")
	}

	if params.IntervalMinMs() < 50 {
		t.Errorf("PowerSavingConnectionParameters() should use longer interval: %.2fms", params.IntervalMinMs())
	}
}

func TestConnectionParameterUpdateRequestEncodeDecode(t *testing.T) {
	req := &ConnectionParameterUpdateRequest{
		Identifier: 0x42,
		Params:     DefaultConnectionParameters(),
	}

	// Encode
	data, err := EncodeConnectionParameterUpdateRequest(req)
	if err != nil {
		t.Fatalf("EncodeConnectionParameterUpdateRequest() error = %v", err)
	}

	if len(data) != 12 {
		t.Errorf("Encoded request length = %d, want 12", len(data))
	}

	if data[0] != CodeConnectionParameterUpdateRequest {
		t.Errorf("Command code = 0x%02X, want 0x%02X", data[0], CodeConnectionParameterUpdateRequest)
	}

	if data[1] != 0x42 {
		t.Errorf("Identifier = 0x%02X, want 0x42", data[1])
	}

	// Decode
	decoded, err := DecodeConnectionParameterUpdateRequest(data)
	if err != nil {
		t.Fatalf("DecodeConnectionParameterUpdateRequest() error = %v", err)
	}

	if decoded.Identifier != req.Identifier {
		t.Errorf("Decoded identifier = 0x%02X, want 0x%02X", decoded.Identifier, req.Identifier)
	}

	if decoded.Params.IntervalMin != req.Params.IntervalMin {
		t.Errorf("Decoded IntervalMin = %d, want %d", decoded.Params.IntervalMin, req.Params.IntervalMin)
	}

	if decoded.Params.IntervalMax != req.Params.IntervalMax {
		t.Errorf("Decoded IntervalMax = %d, want %d", decoded.Params.IntervalMax, req.Params.IntervalMax)
	}

	if decoded.Params.SlaveLatency != req.Params.SlaveLatency {
		t.Errorf("Decoded SlaveLatency = %d, want %d", decoded.Params.SlaveLatency, req.Params.SlaveLatency)
	}

	if decoded.Params.SupervisionTimeout != req.Params.SupervisionTimeout {
		t.Errorf("Decoded SupervisionTimeout = %d, want %d", decoded.Params.SupervisionTimeout, req.Params.SupervisionTimeout)
	}
}

func TestConnectionParameterUpdateResponseEncodeDecode(t *testing.T) {
	resp := &ConnectionParameterUpdateResponse{
		Identifier: 0x42,
		Result:     ConnectionParameterAccepted,
	}

	// Encode
	data := EncodeConnectionParameterUpdateResponse(resp)

	if len(data) != 6 {
		t.Errorf("Encoded response length = %d, want 6", len(data))
	}

	if data[0] != CodeConnectionParameterUpdateResponse {
		t.Errorf("Command code = 0x%02X, want 0x%02X", data[0], CodeConnectionParameterUpdateResponse)
	}

	if data[1] != 0x42 {
		t.Errorf("Identifier = 0x%02X, want 0x42", data[1])
	}

	// Decode
	decoded, err := DecodeConnectionParameterUpdateResponse(data)
	if err != nil {
		t.Fatalf("DecodeConnectionParameterUpdateResponse() error = %v", err)
	}

	if decoded.Identifier != resp.Identifier {
		t.Errorf("Decoded identifier = 0x%02X, want 0x%02X", decoded.Identifier, resp.Identifier)
	}

	if decoded.Result != resp.Result {
		t.Errorf("Decoded result = 0x%04X, want 0x%04X", decoded.Result, resp.Result)
	}
}

func TestConnectionParameterUpdateRequestWithInvalidParams(t *testing.T) {
	req := &ConnectionParameterUpdateRequest{
		Identifier: 0x42,
		Params: &ConnectionParameters{
			IntervalMin:        40, // > IntervalMax
			IntervalMax:        24,
			SlaveLatency:       0,
			SupervisionTimeout: 600,
		},
	}

	_, err := EncodeConnectionParameterUpdateRequest(req)
	if err == nil {
		t.Error("EncodeConnectionParameterUpdateRequest() should fail with invalid parameters")
	}
}

func TestDecodeConnectionParameterUpdateRequestTooShort(t *testing.T) {
	data := []byte{0x12, 0x42, 0x08, 0x00} // Too short

	_, err := DecodeConnectionParameterUpdateRequest(data)
	if err == nil {
		t.Error("DecodeConnectionParameterUpdateRequest() should fail with short data")
	}
}

func TestDecodeConnectionParameterUpdateResponseTooShort(t *testing.T) {
	data := []byte{0x13, 0x42} // Too short

	_, err := DecodeConnectionParameterUpdateResponse(data)
	if err == nil {
		t.Error("DecodeConnectionParameterUpdateResponse() should fail with short data")
	}
}

func TestConnectionParameterRejected(t *testing.T) {
	resp := &ConnectionParameterUpdateResponse{
		Identifier: 0x42,
		Result:     ConnectionParameterRejected,
	}

	data := EncodeConnectionParameterUpdateResponse(resp)
	decoded, err := DecodeConnectionParameterUpdateResponse(data)
	if err != nil {
		t.Fatalf("DecodeConnectionParameterUpdateResponse() error = %v", err)
	}

	if decoded.Result != ConnectionParameterRejected {
		t.Errorf("Decoded result = 0x%04X, want 0x%04X (rejected)", decoded.Result, ConnectionParameterRejected)
	}
}
