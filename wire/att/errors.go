package att

import "fmt"

// ATT Error Codes (Bluetooth Core Spec v5.3 Vol 3, Part F, Section 3.4.1.1)
const (
	ErrSuccess                     = 0x00 // Not an actual error, used internally
	ErrInvalidHandle               = 0x01
	ErrReadNotPermitted            = 0x02
	ErrWriteNotPermitted           = 0x03
	ErrInvalidPDU                  = 0x04
	ErrInsufficientAuthentication  = 0x05
	ErrRequestNotSupported         = 0x06
	ErrInvalidOffset               = 0x07
	ErrInsufficientAuthorization   = 0x08
	ErrPrepareQueueFull            = 0x09
	ErrAttributeNotFound           = 0x0A
	ErrAttributeNotLong            = 0x0B
	ErrInsufficientEncryptionKeySize = 0x0C
	ErrInvalidAttributeValueLength = 0x0D
	ErrUnlikelyError               = 0x0E
	ErrInsufficientEncryption      = 0x0F
	ErrUnsupportedGroupType        = 0x10
	ErrInsufficientResources       = 0x11

	// Application Error codes (0x80 - 0x9F)
	ErrApplicationErrorStart = 0x80
	ErrApplicationErrorEnd   = 0x9F

	// Common Profile and Service Error Codes (0xE0 - 0xFF)
	ErrCommonErrorStart = 0xE0
	ErrCommonErrorEnd   = 0xFF

	// Write Request Rejected is a common error
	ErrWriteRequestRejected = 0xFC
	ErrCCCDImproperlyConfigured = 0xFD
	ErrProcedureAlreadyInProgress = 0xFE
	ErrOutOfRange               = 0xFF
)

// ErrorNames maps error codes to human-readable names
var ErrorNames = map[uint8]string{
	ErrSuccess:                     "Success",
	ErrInvalidHandle:               "Invalid Handle",
	ErrReadNotPermitted:            "Read Not Permitted",
	ErrWriteNotPermitted:           "Write Not Permitted",
	ErrInvalidPDU:                  "Invalid PDU",
	ErrInsufficientAuthentication:  "Insufficient Authentication",
	ErrRequestNotSupported:         "Request Not Supported",
	ErrInvalidOffset:               "Invalid Offset",
	ErrInsufficientAuthorization:   "Insufficient Authorization",
	ErrPrepareQueueFull:            "Prepare Queue Full",
	ErrAttributeNotFound:           "Attribute Not Found",
	ErrAttributeNotLong:            "Attribute Not Long",
	ErrInsufficientEncryptionKeySize: "Insufficient Encryption Key Size",
	ErrInvalidAttributeValueLength: "Invalid Attribute Value Length",
	ErrUnlikelyError:               "Unlikely Error",
	ErrInsufficientEncryption:      "Insufficient Encryption",
	ErrUnsupportedGroupType:        "Unsupported Group Type",
	ErrInsufficientResources:       "Insufficient Resources",
	ErrWriteRequestRejected:        "Write Request Rejected",
	ErrCCCDImproperlyConfigured:    "CCCD Improperly Configured",
	ErrProcedureAlreadyInProgress:  "Procedure Already in Progress",
	ErrOutOfRange:                  "Out of Range",
}

// Error represents an ATT error
type Error struct {
	Code          uint8
	RequestOpcode uint8
	Handle        uint16
}

// Error implements the error interface
func (e *Error) Error() string {
	name, ok := ErrorNames[e.Code]
	if !ok {
		if e.Code >= ErrApplicationErrorStart && e.Code <= ErrApplicationErrorEnd {
			name = fmt.Sprintf("Application Error (0x%02X)", e.Code)
		} else if e.Code >= ErrCommonErrorStart && e.Code <= ErrCommonErrorEnd {
			name = fmt.Sprintf("Common Profile Error (0x%02X)", e.Code)
		} else {
			name = fmt.Sprintf("Unknown Error (0x%02X)", e.Code)
		}
	}

	opcodeName, ok := OpcodeNames[e.RequestOpcode]
	if !ok {
		opcodeName = fmt.Sprintf("0x%02X", e.RequestOpcode)
	}

	return fmt.Sprintf("ATT Error: %s (handle 0x%04X, request %s)", name, e.Handle, opcodeName)
}

// NewError creates a new ATT error
func NewError(code uint8, requestOpcode uint8, handle uint16) *Error {
	return &Error{
		Code:          code,
		RequestOpcode: requestOpcode,
		Handle:        handle,
	}
}

// IsATTError checks if an error is an ATT error with a specific code
func IsATTError(err error, code uint8) bool {
	if attErr, ok := err.(*Error); ok {
		return attErr.Code == code
	}
	return false
}

// GetErrorCode returns the ATT error code from an error, or 0 if not an ATT error
func GetErrorCode(err error) uint8 {
	if attErr, ok := err.(*Error); ok {
		return attErr.Code
	}
	return 0
}
