package swift

// CBManagerState represents the current state of a CBCentralManager or CBPeripheralManager
// Matches iOS CoreBluetooth CBManagerState enum
type CBManagerState int

const (
	CBManagerStateUnknown      CBManagerState = 0 // State is unknown, cannot use Bluetooth yet
	CBManagerStateResetting    CBManagerState = 1 // Connection to the system service was momentarily lost, update imminent
	CBManagerStateUnsupported  CBManagerState = 2 // Platform doesn't support Bluetooth Low Energy
	CBManagerStateUnauthorized CBManagerState = 3 // App is not authorized to use Bluetooth Low Energy
	CBManagerStatePoweredOff   CBManagerState = 4 // Bluetooth is currently powered off
	CBManagerStatePoweredOn    CBManagerState = 5 // Bluetooth is currently powered on and available to use
)

// String returns the string representation of the CBManagerState
func (s CBManagerState) String() string {
	switch s {
	case CBManagerStateUnknown:
		return "unknown"
	case CBManagerStateResetting:
		return "resetting"
	case CBManagerStateUnsupported:
		return "unsupported"
	case CBManagerStateUnauthorized:
		return "unauthorized"
	case CBManagerStatePoweredOff:
		return "poweredOff"
	case CBManagerStatePoweredOn:
		return "poweredOn"
	default:
		return "unknown"
	}
}

// CBPeripheralState represents the connection state of a CBPeripheral
// Matches iOS CoreBluetooth CBPeripheralState enum
type CBPeripheralState int

const (
	CBPeripheralStateDisconnected  CBPeripheralState = 0 // Not connected to the central
	CBPeripheralStateConnecting    CBPeripheralState = 1 // Connection is being established
	CBPeripheralStateConnected     CBPeripheralState = 2 // Connected to the central
	CBPeripheralStateDisconnecting CBPeripheralState = 3 // Disconnection is in progress
)

// String returns the string representation of the CBPeripheralState
func (s CBPeripheralState) String() string {
	switch s {
	case CBPeripheralStateDisconnected:
		return "disconnected"
	case CBPeripheralStateConnecting:
		return "connecting"
	case CBPeripheralStateConnected:
		return "connected"
	case CBPeripheralStateDisconnecting:
		return "disconnecting"
	default:
		return "disconnected"
	}
}

// CBConnectionEvent represents different connection events for realistic timing
type CBConnectionEvent int

const (
	CBConnectionEventConnecting CBConnectionEvent = iota
	CBConnectionEventConnected
	CBConnectionEventDisconnecting
	CBConnectionEventDisconnected
	CBConnectionEventFailedToConnect
)

// CBATTError represents ATT protocol errors
type CBATTError int

const (
	CBATTErrorSuccess                     CBATTError = 0x00
	CBATTErrorInvalidHandle               CBATTError = 0x01
	CBATTErrorReadNotPermitted            CBATTError = 0x02
	CBATTErrorWriteNotPermitted           CBATTError = 0x03
	CBATTErrorInvalidPDU                  CBATTError = 0x04
	CBATTErrorInsufficientAuthentication  CBATTError = 0x05
	CBATTErrorRequestNotSupported         CBATTError = 0x06
	CBATTErrorInvalidOffset               CBATTError = 0x07
	CBATTErrorInsufficientAuthorization   CBATTError = 0x08
	CBATTErrorPrepareQueueFull            CBATTError = 0x09
	CBATTErrorAttributeNotFound           CBATTError = 0x0A
	CBATTErrorAttributeNotLong            CBATTError = 0x0B
	CBATTErrorInsufficientEncryptionKeySize CBATTError = 0x0C
	CBATTErrorInvalidAttributeValueLength CBATTError = 0x0D
	CBATTErrorUnlikelyError               CBATTError = 0x0E
	CBATTErrorInsufficientEncryption      CBATTError = 0x0F
	CBATTErrorUnsupportedGroupType        CBATTError = 0x10
	CBATTErrorInsufficientResources       CBATTError = 0x11
)

// CBError represents CoreBluetooth errors
type CBError int

const (
	CBErrorUnknown                       CBError = 0
	CBErrorInvalidParameters             CBError = 1
	CBErrorInvalidHandle                 CBError = 2
	CBErrorNotConnected                  CBError = 3
	CBErrorOutOfSpace                    CBError = 4
	CBErrorOperationCancelled            CBError = 5
	CBErrorConnectionTimeout             CBError = 6
	CBErrorPeripheralDisconnected        CBError = 7
	CBErrorUUIDNotAllowed                CBError = 8
	CBErrorAlreadyAdvertising            CBError = 9
	CBErrorConnectionFailed              CBError = 10
	CBErrorConnectionLimitReached        CBError = 11
	CBErrorUnknownDevice                 CBError = 12
	CBErrorOperationNotSupported         CBError = 13
	CBErrorPeerRemovedPairingInformation CBError = 14
	CBErrorEncryptionTimedOut            CBError = 15
	CBErrorTooManyLEPairedDevices        CBError = 16
)
