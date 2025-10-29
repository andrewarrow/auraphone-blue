package att

import (
	"encoding/binary"
	"fmt"
)

// Packet represents a generic ATT packet
// All ATT packets start with an opcode byte
type Packet struct {
	Opcode uint8
	Data   []byte // Opcode-specific data
}

// MTU Exchange Request/Response (Opcodes 0x02/0x03)
type ExchangeMTURequest struct {
	ClientRxMTU uint16 // Client's maximum receive MTU
}

type ExchangeMTUResponse struct {
	ServerRxMTU uint16 // Server's maximum receive MTU
}

// Error Response (Opcode 0x01)
type ErrorResponse struct {
	RequestOpcode uint8  // The opcode that caused the error
	Handle        uint16 // The handle that caused the error
	ErrorCode     uint8  // The error code
}

// Read Request/Response (Opcodes 0x0A/0x0B)
type ReadRequest struct {
	Handle uint16 // Handle of the attribute to read
}

type ReadResponse struct {
	Value []byte // The value of the attribute
}

// Read By Type Request/Response (Opcodes 0x08/0x09)
type ReadByTypeRequest struct {
	StartHandle uint16 // First handle to read
	EndHandle   uint16 // Last handle to read
	Type        []byte // 2 or 16 byte UUID
}

type ReadByTypeResponse struct {
	Length         uint8  // Length of each attribute data
	AttributeData  []byte // List of (Handle, Value) pairs
}

// Read By Group Type Request/Response (Opcodes 0x10/0x11) - used for service discovery
type ReadByGroupTypeRequest struct {
	StartHandle uint16 // First handle to search
	EndHandle   uint16 // Last handle to search
	Type        []byte // 2 or 16 byte UUID (usually Primary Service UUID)
}

type ReadByGroupTypeResponse struct {
	Length        uint8  // Length of each attribute data
	AttributeData []byte // List of (Handle, EndGroupHandle, Value) tuples
}

// Find Information Request/Response (Opcodes 0x04/0x05)
type FindInformationRequest struct {
	StartHandle uint16 // First handle to search
	EndHandle   uint16 // Last handle to search
}

type FindInformationResponse struct {
	Format uint8  // 0x01 = 16-bit UUIDs, 0x02 = 128-bit UUIDs
	Data   []byte // List of (Handle, UUID) pairs
}

// Write Request/Response (Opcodes 0x12/0x13)
type WriteRequest struct {
	Handle uint16 // Handle of the attribute to write
	Value  []byte // Value to write
}

type WriteResponse struct {
	// Empty on success
}

// Write Command (Opcode 0x52) - no response
type WriteCommand struct {
	Handle uint16 // Handle of the attribute to write
	Value  []byte // Value to write
}

// Prepare Write Request/Response (Opcodes 0x16/0x17)
type PrepareWriteRequest struct {
	Handle uint16 // Handle of the attribute
	Offset uint16 // Offset for the write
	Value  []byte // Partial value to write
}

type PrepareWriteResponse struct {
	Handle uint16 // Echo of the handle
	Offset uint16 // Echo of the offset
	Value  []byte // Echo of the value
}

// Execute Write Request/Response (Opcodes 0x18/0x19)
type ExecuteWriteRequest struct {
	Flags uint8 // 0x00 = cancel, 0x01 = execute
}

type ExecuteWriteResponse struct {
	// Empty on success
}

// Handle Value Notification (Opcode 0x1B) - no confirmation
type HandleValueNotification struct {
	Handle uint16 // Handle of the attribute
	Value  []byte // Value of the attribute
}

// Handle Value Indication (Opcode 0x1D) - requires confirmation
type HandleValueIndication struct {
	Handle uint16 // Handle of the attribute
	Value  []byte // Value of the attribute
}

// Handle Value Confirmation (Opcode 0x1E)
type HandleValueConfirmation struct {
	// Empty
}

// EncodePacket encodes an ATT packet to binary format
func EncodePacket(pkt interface{}) ([]byte, error) {
	switch p := pkt.(type) {
	case *ExchangeMTURequest:
		buf := make([]byte, 3)
		buf[0] = OpExchangeMTURequest
		binary.LittleEndian.PutUint16(buf[1:3], p.ClientRxMTU)
		return buf, nil

	case *ExchangeMTUResponse:
		buf := make([]byte, 3)
		buf[0] = OpExchangeMTUResponse
		binary.LittleEndian.PutUint16(buf[1:3], p.ServerRxMTU)
		return buf, nil

	case *ErrorResponse:
		buf := make([]byte, 5)
		buf[0] = OpErrorResponse
		buf[1] = p.RequestOpcode
		binary.LittleEndian.PutUint16(buf[2:4], p.Handle)
		buf[4] = p.ErrorCode
		return buf, nil

	case *ReadRequest:
		buf := make([]byte, 3)
		buf[0] = OpReadRequest
		binary.LittleEndian.PutUint16(buf[1:3], p.Handle)
		return buf, nil

	case *ReadResponse:
		buf := make([]byte, 1+len(p.Value))
		buf[0] = OpReadResponse
		copy(buf[1:], p.Value)
		return buf, nil

	case *ReadByTypeRequest:
		buf := make([]byte, 5+len(p.Type))
		buf[0] = OpReadByTypeRequest
		binary.LittleEndian.PutUint16(buf[1:3], p.StartHandle)
		binary.LittleEndian.PutUint16(buf[3:5], p.EndHandle)
		copy(buf[5:], p.Type)
		return buf, nil

	case *ReadByTypeResponse:
		buf := make([]byte, 2+len(p.AttributeData))
		buf[0] = OpReadByTypeResponse
		buf[1] = p.Length
		copy(buf[2:], p.AttributeData)
		return buf, nil

	case *ReadByGroupTypeRequest:
		buf := make([]byte, 5+len(p.Type))
		buf[0] = OpReadByGroupTypeRequest
		binary.LittleEndian.PutUint16(buf[1:3], p.StartHandle)
		binary.LittleEndian.PutUint16(buf[3:5], p.EndHandle)
		copy(buf[5:], p.Type)
		return buf, nil

	case *ReadByGroupTypeResponse:
		buf := make([]byte, 2+len(p.AttributeData))
		buf[0] = OpReadByGroupTypeResponse
		buf[1] = p.Length
		copy(buf[2:], p.AttributeData)
		return buf, nil

	case *FindInformationRequest:
		buf := make([]byte, 5)
		buf[0] = OpFindInformationRequest
		binary.LittleEndian.PutUint16(buf[1:3], p.StartHandle)
		binary.LittleEndian.PutUint16(buf[3:5], p.EndHandle)
		return buf, nil

	case *FindInformationResponse:
		buf := make([]byte, 2+len(p.Data))
		buf[0] = OpFindInformationResponse
		buf[1] = p.Format
		copy(buf[2:], p.Data)
		return buf, nil

	case *WriteRequest:
		buf := make([]byte, 3+len(p.Value))
		buf[0] = OpWriteRequest
		binary.LittleEndian.PutUint16(buf[1:3], p.Handle)
		copy(buf[3:], p.Value)
		return buf, nil

	case *WriteResponse:
		return []byte{OpWriteResponse}, nil

	case *WriteCommand:
		buf := make([]byte, 3+len(p.Value))
		buf[0] = OpWriteCommand
		binary.LittleEndian.PutUint16(buf[1:3], p.Handle)
		copy(buf[3:], p.Value)
		return buf, nil

	case *PrepareWriteRequest:
		buf := make([]byte, 5+len(p.Value))
		buf[0] = OpPrepareWriteRequest
		binary.LittleEndian.PutUint16(buf[1:3], p.Handle)
		binary.LittleEndian.PutUint16(buf[3:5], p.Offset)
		copy(buf[5:], p.Value)
		return buf, nil

	case *PrepareWriteResponse:
		buf := make([]byte, 5+len(p.Value))
		buf[0] = OpPrepareWriteResponse
		binary.LittleEndian.PutUint16(buf[1:3], p.Handle)
		binary.LittleEndian.PutUint16(buf[3:5], p.Offset)
		copy(buf[5:], p.Value)
		return buf, nil

	case *ExecuteWriteRequest:
		return []byte{OpExecuteWriteRequest, p.Flags}, nil

	case *ExecuteWriteResponse:
		return []byte{OpExecuteWriteResponse}, nil

	case *HandleValueNotification:
		buf := make([]byte, 3+len(p.Value))
		buf[0] = OpHandleValueNotification
		binary.LittleEndian.PutUint16(buf[1:3], p.Handle)
		copy(buf[3:], p.Value)
		return buf, nil

	case *HandleValueIndication:
		buf := make([]byte, 3+len(p.Value))
		buf[0] = OpHandleValueIndication
		binary.LittleEndian.PutUint16(buf[1:3], p.Handle)
		copy(buf[3:], p.Value)
		return buf, nil

	case *HandleValueConfirmation:
		return []byte{OpHandleValueConfirmation}, nil

	default:
		return nil, fmt.Errorf("att: unknown packet type %T", pkt)
	}
}

// DecodePacket decodes binary data into an ATT packet
func DecodePacket(data []byte) (interface{}, error) {
	if len(data) < 1 {
		return nil, fmt.Errorf("att: packet too short (need at least 1 byte)")
	}

	opcode := data[0]

	switch opcode {
	case OpExchangeMTURequest:
		if len(data) < 3 {
			return nil, fmt.Errorf("att: ExchangeMTURequest too short")
		}
		return &ExchangeMTURequest{
			ClientRxMTU: binary.LittleEndian.Uint16(data[1:3]),
		}, nil

	case OpExchangeMTUResponse:
		if len(data) < 3 {
			return nil, fmt.Errorf("att: ExchangeMTUResponse too short")
		}
		return &ExchangeMTUResponse{
			ServerRxMTU: binary.LittleEndian.Uint16(data[1:3]),
		}, nil

	case OpErrorResponse:
		if len(data) < 5 {
			return nil, fmt.Errorf("att: ErrorResponse too short")
		}
		return &ErrorResponse{
			RequestOpcode: data[1],
			Handle:        binary.LittleEndian.Uint16(data[2:4]),
			ErrorCode:     data[4],
		}, nil

	case OpReadRequest:
		if len(data) < 3 {
			return nil, fmt.Errorf("att: ReadRequest too short")
		}
		return &ReadRequest{
			Handle: binary.LittleEndian.Uint16(data[1:3]),
		}, nil

	case OpReadResponse:
		return &ReadResponse{
			Value: append([]byte{}, data[1:]...),
		}, nil

	case OpReadByTypeRequest:
		if len(data) < 7 { // 1 + 2 + 2 + 2 (opcode + start + end + min UUID)
			return nil, fmt.Errorf("att: ReadByTypeRequest too short")
		}
		return &ReadByTypeRequest{
			StartHandle: binary.LittleEndian.Uint16(data[1:3]),
			EndHandle:   binary.LittleEndian.Uint16(data[3:5]),
			Type:        append([]byte{}, data[5:]...),
		}, nil

	case OpReadByTypeResponse:
		if len(data) < 2 {
			return nil, fmt.Errorf("att: ReadByTypeResponse too short")
		}
		return &ReadByTypeResponse{
			Length:        data[1],
			AttributeData: append([]byte{}, data[2:]...),
		}, nil

	case OpReadByGroupTypeRequest:
		if len(data) < 7 {
			return nil, fmt.Errorf("att: ReadByGroupTypeRequest too short")
		}
		return &ReadByGroupTypeRequest{
			StartHandle: binary.LittleEndian.Uint16(data[1:3]),
			EndHandle:   binary.LittleEndian.Uint16(data[3:5]),
			Type:        append([]byte{}, data[5:]...),
		}, nil

	case OpReadByGroupTypeResponse:
		if len(data) < 2 {
			return nil, fmt.Errorf("att: ReadByGroupTypeResponse too short")
		}
		return &ReadByGroupTypeResponse{
			Length:        data[1],
			AttributeData: append([]byte{}, data[2:]...),
		}, nil

	case OpFindInformationRequest:
		if len(data) < 5 {
			return nil, fmt.Errorf("att: FindInformationRequest too short")
		}
		return &FindInformationRequest{
			StartHandle: binary.LittleEndian.Uint16(data[1:3]),
			EndHandle:   binary.LittleEndian.Uint16(data[3:5]),
		}, nil

	case OpFindInformationResponse:
		if len(data) < 2 {
			return nil, fmt.Errorf("att: FindInformationResponse too short")
		}
		return &FindInformationResponse{
			Format: data[1],
			Data:   append([]byte{}, data[2:]...),
		}, nil

	case OpWriteRequest:
		if len(data) < 3 {
			return nil, fmt.Errorf("att: WriteRequest too short")
		}
		return &WriteRequest{
			Handle: binary.LittleEndian.Uint16(data[1:3]),
			Value:  append([]byte{}, data[3:]...),
		}, nil

	case OpWriteResponse:
		return &WriteResponse{}, nil

	case OpWriteCommand:
		if len(data) < 3 {
			return nil, fmt.Errorf("att: WriteCommand too short")
		}
		return &WriteCommand{
			Handle: binary.LittleEndian.Uint16(data[1:3]),
			Value:  append([]byte{}, data[3:]...),
		}, nil

	case OpPrepareWriteRequest:
		if len(data) < 5 {
			return nil, fmt.Errorf("att: PrepareWriteRequest too short")
		}
		return &PrepareWriteRequest{
			Handle: binary.LittleEndian.Uint16(data[1:3]),
			Offset: binary.LittleEndian.Uint16(data[3:5]),
			Value:  append([]byte{}, data[5:]...),
		}, nil

	case OpPrepareWriteResponse:
		if len(data) < 5 {
			return nil, fmt.Errorf("att: PrepareWriteResponse too short")
		}
		return &PrepareWriteResponse{
			Handle: binary.LittleEndian.Uint16(data[1:3]),
			Offset: binary.LittleEndian.Uint16(data[3:5]),
			Value:  append([]byte{}, data[5:]...),
		}, nil

	case OpExecuteWriteRequest:
		if len(data) < 2 {
			return nil, fmt.Errorf("att: ExecuteWriteRequest too short")
		}
		return &ExecuteWriteRequest{
			Flags: data[1],
		}, nil

	case OpExecuteWriteResponse:
		return &ExecuteWriteResponse{}, nil

	case OpHandleValueNotification:
		if len(data) < 3 {
			return nil, fmt.Errorf("att: HandleValueNotification too short")
		}
		return &HandleValueNotification{
			Handle: binary.LittleEndian.Uint16(data[1:3]),
			Value:  append([]byte{}, data[3:]...),
		}, nil

	case OpHandleValueIndication:
		if len(data) < 3 {
			return nil, fmt.Errorf("att: HandleValueIndication too short")
		}
		return &HandleValueIndication{
			Handle: binary.LittleEndian.Uint16(data[1:3]),
			Value:  append([]byte{}, data[3:]...),
		}, nil

	case OpHandleValueConfirmation:
		return &HandleValueConfirmation{}, nil

	default:
		return nil, fmt.Errorf("att: unknown opcode 0x%02X", opcode)
	}
}
