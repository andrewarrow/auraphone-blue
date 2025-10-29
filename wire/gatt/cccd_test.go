package gatt

import (
	"testing"

	"github.com/user/auraphone-blue/util"
)

func TestCCCDEncodeDecode(t *testing.T) {
	util.SetRandom()

	tests := []struct {
		name            string
		notifyEnabled   bool
		indicateEnabled bool
		expectedValue   uint16
	}{
		{
			name:            "both disabled",
			notifyEnabled:   false,
			indicateEnabled: false,
			expectedValue:   CCCDNotificationsDisabled,
		},
		{
			name:            "notifications enabled",
			notifyEnabled:   true,
			indicateEnabled: false,
			expectedValue:   CCCDNotificationsEnabled,
		},
		{
			name:            "indications enabled",
			notifyEnabled:   false,
			indicateEnabled: true,
			expectedValue:   CCCDIndicationsEnabled,
		},
		{
			name:            "both enabled",
			notifyEnabled:   true,
			indicateEnabled: true,
			expectedValue:   CCCDBothEnabled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Encode
			cccdValue := EncodeCCCDValue(tt.notifyEnabled, tt.indicateEnabled)
			if len(cccdValue) != 2 {
				t.Errorf("Expected CCCD value length 2, got %d", len(cccdValue))
			}

			// Check value
			value := uint16(cccdValue[0]) | (uint16(cccdValue[1]) << 8)
			if value != tt.expectedValue {
				t.Errorf("Expected CCCD value 0x%04X, got 0x%04X", tt.expectedValue, value)
			}

			// Decode
			notify, indicate, err := DecodeCCCDValue(cccdValue)
			if err != nil {
				t.Errorf("DecodeCCCDValue failed: %v", err)
			}
			if notify != tt.notifyEnabled {
				t.Errorf("Expected notify=%v, got %v", tt.notifyEnabled, notify)
			}
			if indicate != tt.indicateEnabled {
				t.Errorf("Expected indicate=%v, got %v", tt.indicateEnabled, indicate)
			}
		})
	}
}

func TestCCCDDecodeInvalidLength(t *testing.T) {
	util.SetRandom()

	invalidValues := [][]byte{
		{},
		{0x01},
		{0x01, 0x00, 0x00},
	}

	for _, val := range invalidValues {
		_, _, err := DecodeCCCDValue(val)
		if err == nil {
			t.Errorf("Expected error for invalid CCCD value length %d", len(val))
		}
	}
}

func TestCCCDManagerSetSubscription(t *testing.T) {
	util.SetRandom()

	cm := NewCCCDManager()

	// Enable notifications
	handle := uint16(0x0010)
	cccdValue := EncodeCCCDValue(true, false)
	err := cm.SetSubscription(handle, cccdValue)
	if err != nil {
		t.Fatalf("SetSubscription failed: %v", err)
	}

	// Verify subscription
	state, exists := cm.GetSubscription(handle)
	if !exists {
		t.Fatal("Expected subscription to exist")
	}
	if !state.NotifyEnabled {
		t.Error("Expected notifications to be enabled")
	}
	if state.IndicateEnabled {
		t.Error("Expected indications to be disabled")
	}

	// Enable indications
	cccdValue = EncodeCCCDValue(false, true)
	err = cm.SetSubscription(handle, cccdValue)
	if err != nil {
		t.Fatalf("SetSubscription failed: %v", err)
	}

	// Verify updated subscription
	state, exists = cm.GetSubscription(handle)
	if !exists {
		t.Fatal("Expected subscription to exist")
	}
	if state.NotifyEnabled {
		t.Error("Expected notifications to be disabled")
	}
	if !state.IndicateEnabled {
		t.Error("Expected indications to be enabled")
	}

	// Disable both (should remove subscription)
	cccdValue = EncodeCCCDValue(false, false)
	err = cm.SetSubscription(handle, cccdValue)
	if err != nil {
		t.Fatalf("SetSubscription failed: %v", err)
	}

	// Verify subscription removed
	_, exists = cm.GetSubscription(handle)
	if exists {
		t.Error("Expected subscription to be removed")
	}
}

func TestCCCDManagerMultipleSubscriptions(t *testing.T) {
	util.SetRandom()

	cm := NewCCCDManager()

	// Enable notifications for multiple characteristics
	handle1 := uint16(0x0010)
	handle2 := uint16(0x0020)
	handle3 := uint16(0x0030)

	cm.SetSubscription(handle1, EncodeCCCDValue(true, false))
	cm.SetSubscription(handle2, EncodeCCCDValue(false, true))
	cm.SetSubscription(handle3, EncodeCCCDValue(true, true))

	// Verify all subscriptions
	if !cm.IsNotifyEnabled(handle1) {
		t.Error("Expected notifications enabled for handle1")
	}
	if cm.IsIndicateEnabled(handle1) {
		t.Error("Expected indications disabled for handle1")
	}

	if cm.IsNotifyEnabled(handle2) {
		t.Error("Expected notifications disabled for handle2")
	}
	if !cm.IsIndicateEnabled(handle2) {
		t.Error("Expected indications enabled for handle2")
	}

	if !cm.IsNotifyEnabled(handle3) {
		t.Error("Expected notifications enabled for handle3")
	}
	if !cm.IsIndicateEnabled(handle3) {
		t.Error("Expected indications enabled for handle3")
	}

	// Check count
	if cm.Count() != 3 {
		t.Errorf("Expected 3 subscriptions, got %d", cm.Count())
	}

	// Get all subscriptions
	subs := cm.GetAllSubscriptions()
	if len(subs) != 3 {
		t.Errorf("Expected 3 subscriptions, got %d", len(subs))
	}
}

func TestCCCDManagerIsSubscribed(t *testing.T) {
	util.SetRandom()

	cm := NewCCCDManager()
	handle := uint16(0x0010)

	// Initially not subscribed
	if cm.IsSubscribed(handle) {
		t.Error("Expected not subscribed initially")
	}

	// Enable notifications
	cm.SetSubscription(handle, EncodeCCCDValue(true, false))
	if !cm.IsSubscribed(handle) {
		t.Error("Expected subscribed after enabling notifications")
	}

	// Enable indications
	cm.SetSubscription(handle, EncodeCCCDValue(false, true))
	if !cm.IsSubscribed(handle) {
		t.Error("Expected subscribed after enabling indications")
	}

	// Disable both
	cm.SetSubscription(handle, EncodeCCCDValue(false, false))
	if cm.IsSubscribed(handle) {
		t.Error("Expected not subscribed after disabling both")
	}
}

func TestCCCDManagerClear(t *testing.T) {
	util.SetRandom()

	cm := NewCCCDManager()

	// Add multiple subscriptions
	cm.SetSubscription(0x0010, EncodeCCCDValue(true, false))
	cm.SetSubscription(0x0020, EncodeCCCDValue(false, true))
	cm.SetSubscription(0x0030, EncodeCCCDValue(true, true))

	if cm.Count() != 3 {
		t.Errorf("Expected 3 subscriptions, got %d", cm.Count())
	}

	// Clear all subscriptions
	cm.Clear()

	if cm.Count() != 0 {
		t.Errorf("Expected 0 subscriptions after clear, got %d", cm.Count())
	}

	// Verify no subscriptions exist
	if cm.IsSubscribed(0x0010) || cm.IsSubscribed(0x0020) || cm.IsSubscribed(0x0030) {
		t.Error("Expected no subscriptions after clear")
	}
}

func TestCCCDManagerSetSubscriptionInvalidLength(t *testing.T) {
	util.SetRandom()

	cm := NewCCCDManager()
	handle := uint16(0x0010)

	invalidValues := [][]byte{
		{},
		{0x01},
		{0x01, 0x00, 0x00},
	}

	for _, val := range invalidValues {
		err := cm.SetSubscription(handle, val)
		if err == nil {
			t.Errorf("Expected error for invalid CCCD value length %d", len(val))
		}
	}
}
