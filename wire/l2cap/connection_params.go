package l2cap

import (
	"encoding/binary"
	"fmt"
)

// Connection parameter update opcodes (LE L2CAP Signaling Channel)
const (
	// L2CAP signaling command codes
	CodeConnectionParameterUpdateRequest  = 0x12
	CodeConnectionParameterUpdateResponse = 0x13
)

// Connection parameter result codes
const (
	ConnectionParameterAccepted uint16 = 0x0000
	ConnectionParameterRejected uint16 = 0x0001
)

// ConnectionParameters represents BLE connection parameters
// These control the timing characteristics of the connection
type ConnectionParameters struct {
	// Connection interval in units of 1.25ms
	// Range: 6 (7.5ms) to 3200 (4s)
	// iOS typically uses 15-30 (18.75ms to 37.5ms)
	// Android can use 8-4000 (10ms to 5s)
	IntervalMin uint16

	// Maximum connection interval in units of 1.25ms
	IntervalMax uint16

	// Slave latency (number of connection events the peripheral can skip)
	// Range: 0 to 499
	// Higher latency saves power but increases response time
	SlaveLatency uint16

	// Supervision timeout in units of 10ms
	// Range: 100ms (10) to 32s (3200)
	// Must be larger than (1 + SlaveLatency) * IntervalMax * 2
	SupervisionTimeout uint16
}

// DefaultConnectionParameters returns typical iOS/Android connection parameters
func DefaultConnectionParameters() *ConnectionParameters {
	return &ConnectionParameters{
		IntervalMin:        24, // 30ms (24 * 1.25ms)
		IntervalMax:        40, // 50ms (40 * 1.25ms)
		SlaveLatency:       0,  // No latency for responsive connection
		SupervisionTimeout: 600, // 6 seconds (600 * 10ms)
	}
}

// FastConnectionParameters returns parameters for low-latency connections
func FastConnectionParameters() *ConnectionParameters {
	return &ConnectionParameters{
		IntervalMin:        6,   // 7.5ms (minimum)
		IntervalMax:        12,  // 15ms
		SlaveLatency:       0,   // No latency
		SupervisionTimeout: 500, // 5 seconds
	}
}

// PowerSavingConnectionParameters returns parameters optimized for power saving
func PowerSavingConnectionParameters() *ConnectionParameters {
	return &ConnectionParameters{
		IntervalMin:        80,  // 100ms
		IntervalMax:        160, // 200ms
		SlaveLatency:       4,   // Can skip 4 connection events
		SupervisionTimeout: 600, // 6 seconds
	}
}

// Validate checks if connection parameters are within valid BLE ranges
func (p *ConnectionParameters) Validate() error {
	if p.IntervalMin < 6 || p.IntervalMin > 3200 {
		return fmt.Errorf("l2cap: IntervalMin out of range (6-3200): %d", p.IntervalMin)
	}
	if p.IntervalMax < 6 || p.IntervalMax > 3200 {
		return fmt.Errorf("l2cap: IntervalMax out of range (6-3200): %d", p.IntervalMax)
	}
	if p.IntervalMax < p.IntervalMin {
		return fmt.Errorf("l2cap: IntervalMax (%d) must be >= IntervalMin (%d)", p.IntervalMax, p.IntervalMin)
	}
	if p.SlaveLatency > 499 {
		return fmt.Errorf("l2cap: SlaveLatency out of range (0-499): %d", p.SlaveLatency)
	}
	if p.SupervisionTimeout < 10 || p.SupervisionTimeout > 3200 {
		return fmt.Errorf("l2cap: SupervisionTimeout out of range (10-3200): %d", p.SupervisionTimeout)
	}

	// Supervision timeout must be larger than (1 + SlaveLatency) * IntervalMax * 2
	// All converted to 10ms units for comparison
	minTimeout := (1 + uint32(p.SlaveLatency)) * uint32(p.IntervalMax) * 125 / 1000 // Convert 1.25ms to 10ms units
	if uint32(p.SupervisionTimeout) <= minTimeout {
		return fmt.Errorf("l2cap: SupervisionTimeout (%d * 10ms) must be > (1+latency)*interval*2 (%d * 10ms)",
			p.SupervisionTimeout, minTimeout)
	}

	return nil
}

// IntervalMinMs returns the minimum connection interval in milliseconds
func (p *ConnectionParameters) IntervalMinMs() float64 {
	return float64(p.IntervalMin) * 1.25
}

// IntervalMaxMs returns the maximum connection interval in milliseconds
func (p *ConnectionParameters) IntervalMaxMs() float64 {
	return float64(p.IntervalMax) * 1.25
}

// SupervisionTimeoutMs returns the supervision timeout in milliseconds
func (p *ConnectionParameters) SupervisionTimeoutMs() uint32 {
	return uint32(p.SupervisionTimeout) * 10
}

// ConnectionParameterUpdateRequest is sent by the peripheral to request new connection parameters
type ConnectionParameterUpdateRequest struct {
	Identifier uint8 // Command identifier for matching request/response
	Params     *ConnectionParameters
}

// ConnectionParameterUpdateResponse is sent by the central in response to an update request
type ConnectionParameterUpdateResponse struct {
	Identifier uint8  // Must match the request identifier
	Result     uint16 // ConnectionParameterAccepted or ConnectionParameterRejected
}

// EncodeConnectionParameterUpdateRequest encodes a connection parameter update request
func EncodeConnectionParameterUpdateRequest(req *ConnectionParameterUpdateRequest) ([]byte, error) {
	if req.Params == nil {
		return nil, fmt.Errorf("l2cap: nil connection parameters")
	}
	if err := req.Params.Validate(); err != nil {
		return nil, err
	}

	// L2CAP signaling packet format:
	// [Code: 1] [Identifier: 1] [Length: 2] [IntervalMin: 2] [IntervalMax: 2] [SlaveLatency: 2] [Timeout: 2]
	buf := make([]byte, 12)
	buf[0] = CodeConnectionParameterUpdateRequest
	buf[1] = req.Identifier
	binary.LittleEndian.PutUint16(buf[2:4], 8) // Length of parameters
	binary.LittleEndian.PutUint16(buf[4:6], req.Params.IntervalMin)
	binary.LittleEndian.PutUint16(buf[6:8], req.Params.IntervalMax)
	binary.LittleEndian.PutUint16(buf[8:10], req.Params.SlaveLatency)
	binary.LittleEndian.PutUint16(buf[10:12], req.Params.SupervisionTimeout)

	return buf, nil
}

// DecodeConnectionParameterUpdateRequest decodes a connection parameter update request
func DecodeConnectionParameterUpdateRequest(data []byte) (*ConnectionParameterUpdateRequest, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("l2cap: connection parameter update request too short: %d bytes", len(data))
	}

	if data[0] != CodeConnectionParameterUpdateRequest {
		return nil, fmt.Errorf("l2cap: invalid command code: 0x%02X", data[0])
	}

	length := binary.LittleEndian.Uint16(data[2:4])
	if length != 8 {
		return nil, fmt.Errorf("l2cap: invalid parameter length: %d", length)
	}

	params := &ConnectionParameters{
		IntervalMin:        binary.LittleEndian.Uint16(data[4:6]),
		IntervalMax:        binary.LittleEndian.Uint16(data[6:8]),
		SlaveLatency:       binary.LittleEndian.Uint16(data[8:10]),
		SupervisionTimeout: binary.LittleEndian.Uint16(data[10:12]),
	}

	if err := params.Validate(); err != nil {
		return nil, fmt.Errorf("l2cap: invalid connection parameters: %w", err)
	}

	return &ConnectionParameterUpdateRequest{
		Identifier: data[1],
		Params:     params,
	}, nil
}

// EncodeConnectionParameterUpdateResponse encodes a connection parameter update response
func EncodeConnectionParameterUpdateResponse(resp *ConnectionParameterUpdateResponse) []byte {
	// [Code: 1] [Identifier: 1] [Length: 2] [Result: 2]
	buf := make([]byte, 6)
	buf[0] = CodeConnectionParameterUpdateResponse
	buf[1] = resp.Identifier
	binary.LittleEndian.PutUint16(buf[2:4], 2) // Length of result field
	binary.LittleEndian.PutUint16(buf[4:6], resp.Result)
	return buf
}

// DecodeConnectionParameterUpdateResponse decodes a connection parameter update response
func DecodeConnectionParameterUpdateResponse(data []byte) (*ConnectionParameterUpdateResponse, error) {
	if len(data) < 6 {
		return nil, fmt.Errorf("l2cap: connection parameter update response too short: %d bytes", len(data))
	}

	if data[0] != CodeConnectionParameterUpdateResponse {
		return nil, fmt.Errorf("l2cap: invalid command code: 0x%02X", data[0])
	}

	length := binary.LittleEndian.Uint16(data[2:4])
	if length != 2 {
		return nil, fmt.Errorf("l2cap: invalid response length: %d", length)
	}

	return &ConnectionParameterUpdateResponse{
		Identifier: data[1],
		Result:     binary.LittleEndian.Uint16(data[4:6]),
	}, nil
}
