package att

// ATT Opcodes (Bluetooth Core Spec v5.3 Vol 3, Part F, Section 3.4)
const (
	// Error handling
	OpErrorResponse = 0x01

	// MTU Exchange
	OpExchangeMTURequest  = 0x02
	OpExchangeMTUResponse = 0x03

	// Find Information (Discovery)
	OpFindInformationRequest  = 0x04
	OpFindInformationResponse = 0x05

	// Service Discovery
	OpFindByTypeValueRequest  = 0x06
	OpFindByTypeValueResponse = 0x07

	// Read Operations
	OpReadByTypeRequest  = 0x08
	OpReadByTypeResponse = 0x09
	OpReadRequest        = 0x0A
	OpReadResponse       = 0x0B
	OpReadBlobRequest    = 0x0C
	OpReadBlobResponse   = 0x0D
	OpReadMultipleRequest = 0x0E
	OpReadMultipleResponse = 0x0F

	// Group Read (Service Discovery)
	OpReadByGroupTypeRequest  = 0x10
	OpReadByGroupTypeResponse = 0x11

	// Write Operations
	OpWriteRequest  = 0x12
	OpWriteResponse = 0x13

	// Write Command (no response)
	OpWriteCommand = 0x52

	// Signed Write (no response)
	OpSignedWriteCommand = 0xD2

	// Prepared Write (for long writes)
	OpPrepareWriteRequest  = 0x16
	OpPrepareWriteResponse = 0x17
	OpExecuteWriteRequest  = 0x18
	OpExecuteWriteResponse = 0x19

	// Server-initiated
	OpHandleValueNotification = 0x1B
	OpHandleValueIndication   = 0x1D
	OpHandleValueConfirmation = 0x1E
)

// OpcodeNames maps opcodes to human-readable names (useful for debugging)
var OpcodeNames = map[uint8]string{
	OpErrorResponse:           "Error Response",
	OpExchangeMTURequest:      "Exchange MTU Request",
	OpExchangeMTUResponse:     "Exchange MTU Response",
	OpFindInformationRequest:  "Find Information Request",
	OpFindInformationResponse: "Find Information Response",
	OpFindByTypeValueRequest:  "Find By Type Value Request",
	OpFindByTypeValueResponse: "Find By Type Value Response",
	OpReadByTypeRequest:       "Read By Type Request",
	OpReadByTypeResponse:      "Read By Type Response",
	OpReadRequest:             "Read Request",
	OpReadResponse:            "Read Response",
	OpReadBlobRequest:         "Read Blob Request",
	OpReadBlobResponse:        "Read Blob Response",
	OpReadMultipleRequest:     "Read Multiple Request",
	OpReadMultipleResponse:    "Read Multiple Response",
	OpReadByGroupTypeRequest:  "Read By Group Type Request",
	OpReadByGroupTypeResponse: "Read By Group Type Response",
	OpWriteRequest:            "Write Request",
	OpWriteResponse:           "Write Response",
	OpWriteCommand:            "Write Command",
	OpSignedWriteCommand:      "Signed Write Command",
	OpPrepareWriteRequest:     "Prepare Write Request",
	OpPrepareWriteResponse:    "Prepare Write Response",
	OpExecuteWriteRequest:     "Execute Write Request",
	OpExecuteWriteResponse:    "Execute Write Response",
	OpHandleValueNotification: "Handle Value Notification",
	OpHandleValueIndication:   "Handle Value Indication",
	OpHandleValueConfirmation: "Handle Value Confirmation",
}

// IsRequest returns true if the opcode represents a request that expects a response
func IsRequest(opcode uint8) bool {
	switch opcode {
	case OpExchangeMTURequest,
		OpFindInformationRequest,
		OpFindByTypeValueRequest,
		OpReadByTypeRequest,
		OpReadRequest,
		OpReadBlobRequest,
		OpReadMultipleRequest,
		OpReadByGroupTypeRequest,
		OpWriteRequest,
		OpPrepareWriteRequest,
		OpExecuteWriteRequest,
		OpHandleValueIndication:
		return true
	default:
		return false
	}
}

// IsResponse returns true if the opcode represents a response
func IsResponse(opcode uint8) bool {
	switch opcode {
	case OpErrorResponse,
		OpExchangeMTUResponse,
		OpFindInformationResponse,
		OpFindByTypeValueResponse,
		OpReadByTypeResponse,
		OpReadResponse,
		OpReadBlobResponse,
		OpReadMultipleResponse,
		OpReadByGroupTypeResponse,
		OpWriteResponse,
		OpPrepareWriteResponse,
		OpExecuteWriteResponse,
		OpHandleValueConfirmation:
		return true
	default:
		return false
	}
}

// IsCommand returns true if the opcode is a command (no response expected)
func IsCommand(opcode uint8) bool {
	switch opcode {
	case OpWriteCommand, OpSignedWriteCommand:
		return true
	default:
		return false
	}
}

// IsNotification returns true if the opcode is a notification (no confirmation expected)
func IsNotification(opcode uint8) bool {
	return opcode == OpHandleValueNotification
}

// GetResponseOpcode returns the expected response opcode for a given request opcode
// Returns 0 if the opcode doesn't have a response
func GetResponseOpcode(requestOpcode uint8) uint8 {
	switch requestOpcode {
	case OpExchangeMTURequest:
		return OpExchangeMTUResponse
	case OpFindInformationRequest:
		return OpFindInformationResponse
	case OpFindByTypeValueRequest:
		return OpFindByTypeValueResponse
	case OpReadByTypeRequest:
		return OpReadByTypeResponse
	case OpReadRequest:
		return OpReadResponse
	case OpReadBlobRequest:
		return OpReadBlobResponse
	case OpReadMultipleRequest:
		return OpReadMultipleResponse
	case OpReadByGroupTypeRequest:
		return OpReadByGroupTypeResponse
	case OpWriteRequest:
		return OpWriteResponse
	case OpPrepareWriteRequest:
		return OpPrepareWriteResponse
	case OpExecuteWriteRequest:
		return OpExecuteWriteResponse
	case OpHandleValueIndication:
		return OpHandleValueConfirmation
	default:
		return 0
	}
}
