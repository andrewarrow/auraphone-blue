package gatt

import (
	"encoding/binary"
	"sync"
)

// CCCD (Client Characteristic Configuration Descriptor) values
// These are written by clients to enable/disable notifications and indications
const (
	CCCDNotificationsDisabled  = 0x0000
	CCCDNotificationsEnabled   = 0x0001
	CCCDIndicationsEnabled     = 0x0002
	CCCDBothEnabled            = 0x0003 // Both notifications and indications
)

// SubscriptionState represents the subscription state for a characteristic
type SubscriptionState struct {
	Handle           uint16 // Characteristic value handle
	NotifyEnabled    bool   // Notifications enabled
	IndicateEnabled  bool   // Indications enabled
}

// CCCDManager manages CCCD subscriptions per connection
// In real BLE:
// - Each connection has independent CCCD state
// - CCCD values are NOT shared across connections
// - When a connection is closed, all its subscriptions are cleared
// - Peripheral tracks which connections have enabled notifications/indications
type CCCDManager struct {
	mu sync.RWMutex
	// Map: characteristic value handle -> subscription state
	subscriptions map[uint16]*SubscriptionState
}

// NewCCCDManager creates a new CCCD manager for a connection
func NewCCCDManager() *CCCDManager {
	return &CCCDManager{
		subscriptions: make(map[uint16]*SubscriptionState),
	}
}

// SetSubscription updates the subscription state for a characteristic
// Takes the CCCD value (2 bytes, little-endian) written by the client
func (cm *CCCDManager) SetSubscription(charHandle uint16, cccdValue []byte) error {
	if len(cccdValue) != 2 {
		return ErrInvalidAttributeValueLength
	}

	// Parse CCCD value (little-endian)
	value := binary.LittleEndian.Uint16(cccdValue)

	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Update or create subscription state
	state, exists := cm.subscriptions[charHandle]
	if !exists {
		state = &SubscriptionState{Handle: charHandle}
		cm.subscriptions[charHandle] = state
	}

	// Update notification/indication flags based on CCCD value
	state.NotifyEnabled = (value & CCCDNotificationsEnabled) != 0
	state.IndicateEnabled = (value & CCCDIndicationsEnabled) != 0

	// If both are disabled, remove the subscription
	if !state.NotifyEnabled && !state.IndicateEnabled {
		delete(cm.subscriptions, charHandle)
	}

	return nil
}

// GetSubscription returns the subscription state for a characteristic
func (cm *CCCDManager) GetSubscription(charHandle uint16) (*SubscriptionState, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	state, exists := cm.subscriptions[charHandle]
	if !exists {
		return nil, false
	}

	// Return a copy to prevent external modification
	return &SubscriptionState{
		Handle:          state.Handle,
		NotifyEnabled:   state.NotifyEnabled,
		IndicateEnabled: state.IndicateEnabled,
	}, true
}

// IsSubscribed returns true if notifications or indications are enabled for a characteristic
func (cm *CCCDManager) IsSubscribed(charHandle uint16) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	state, exists := cm.subscriptions[charHandle]
	return exists && (state.NotifyEnabled || state.IndicateEnabled)
}

// IsNotifyEnabled returns true if notifications are enabled for a characteristic
func (cm *CCCDManager) IsNotifyEnabled(charHandle uint16) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	state, exists := cm.subscriptions[charHandle]
	return exists && state.NotifyEnabled
}

// IsIndicateEnabled returns true if indications are enabled for a characteristic
func (cm *CCCDManager) IsIndicateEnabled(charHandle uint16) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	state, exists := cm.subscriptions[charHandle]
	return exists && state.IndicateEnabled
}

// GetAllSubscriptions returns all active subscriptions
func (cm *CCCDManager) GetAllSubscriptions() []SubscriptionState {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	subs := make([]SubscriptionState, 0, len(cm.subscriptions))
	for _, state := range cm.subscriptions {
		subs = append(subs, SubscriptionState{
			Handle:          state.Handle,
			NotifyEnabled:   state.NotifyEnabled,
			IndicateEnabled: state.IndicateEnabled,
		})
	}

	return subs
}

// Clear removes all subscriptions (called when connection is closed)
func (cm *CCCDManager) Clear() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.subscriptions = make(map[uint16]*SubscriptionState)
}

// Count returns the number of active subscriptions
func (cm *CCCDManager) Count() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	return len(cm.subscriptions)
}

// EncodeCCCDValue converts subscription state to CCCD value bytes (little-endian)
func EncodeCCCDValue(notifyEnabled, indicateEnabled bool) []byte {
	var value uint16
	if notifyEnabled {
		value |= CCCDNotificationsEnabled
	}
	if indicateEnabled {
		value |= CCCDIndicationsEnabled
	}

	cccdValue := make([]byte, 2)
	binary.LittleEndian.PutUint16(cccdValue, value)
	return cccdValue
}

// DecodeCCCDValue parses CCCD value bytes to notification/indication flags
func DecodeCCCDValue(cccdValue []byte) (notifyEnabled, indicateEnabled bool, err error) {
	if len(cccdValue) != 2 {
		return false, false, ErrInvalidAttributeValueLength
	}

	value := binary.LittleEndian.Uint16(cccdValue)
	notifyEnabled = (value & CCCDNotificationsEnabled) != 0
	indicateEnabled = (value & CCCDIndicationsEnabled) != 0

	return notifyEnabled, indicateEnabled, nil
}

// ErrInvalidAttributeValueLength is returned when CCCD value has incorrect length
var ErrInvalidAttributeValueLength = &Error{Code: 0x0D, Description: "Invalid Attribute Value Length"}

// Error represents a GATT error
type Error struct {
	Code        uint8
	Description string
}

func (e *Error) Error() string {
	return e.Description
}
