package wire

import (
	"testing"
	"time"

	"github.com/user/auraphone-blue/phone"
)

// TestPeripheralNotificationToCentral verifies that notifications sent from
// a Peripheral are properly received by a Central via the central_inbox.
// This test reproduces the bug where F09FFFB7 (Central) doesn't receive
// notifications from B4698CEC (Peripheral).
func TestPeripheralNotificationToCentral(t *testing.T) {
	// Clean up any leftover test data
	phone.CleanupDataDir()

	// Use perfect simulation config for deterministic testing
	config := PerfectSimulationConfig()

	// Create two devices
	// Device1 will be Central (larger UUID)
	// Device2 will be Peripheral (smaller UUID)
	device1UUID := "BBBBBBBB-1111-1111-1111-111111111111" // Larger UUID = Central
	device2UUID := "AAAAAAAA-2222-2222-2222-222222222222" // Smaller UUID = Peripheral

	wire1 := NewWireWithPlatform(device1UUID, PlatformIOS, "Central Device", config)
	wire2 := NewWireWithPlatform(device2UUID, PlatformAndroid, "Peripheral Device", config)

	defer wire1.Cleanup()
	defer wire2.Cleanup()

	// Initialize devices
	if err := wire1.InitializeDevice(); err != nil {
		t.Fatalf("Failed to initialize device1: %v", err)
	}
	if err := wire2.InitializeDevice(); err != nil {
		t.Fatalf("Failed to initialize device2: %v", err)
	}

	// Give devices time to create their sockets
	time.Sleep(50 * time.Millisecond)

	// Device1 (Central) connects to Device2 (Peripheral)
	if err := wire1.Connect(device2UUID); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	// Wait for connection to establish
	time.Sleep(100 * time.Millisecond)

	// Device1 subscribes to a characteristic on Device2
	serviceUUID := "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	charUUID := "E621E1F8-C36C-495A-93FC-0C247A3E6E5D" // Protocol characteristic

	if err := wire1.SubscribeCharacteristic(device2UUID, serviceUUID, charUUID); err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	// Wait for subscription to be registered
	time.Sleep(50 * time.Millisecond)

	// Device2 (Peripheral) sends a notification to Device1 (Central)
	notificationData := []byte("test notification from peripheral")
	if err := wire2.NotifyCharacteristic(device1UUID, serviceUUID, charUUID, notificationData); err != nil {
		t.Fatalf("Failed to send notification: %v", err)
	}

	// Wait for notification to be delivered
	time.Sleep(100 * time.Millisecond)

	// Device1 (Central) should receive the notification via central_inbox
	messages, err := wire1.ReadAndConsumeCharacteristicMessagesFromInbox("central_inbox")
	if err != nil {
		t.Fatalf("Failed to read from central_inbox: %v", err)
	}

	if len(messages) != 1 {
		t.Errorf("Expected 1 notification in central_inbox, got %d", len(messages))
		t.Logf("Messages received: %+v", messages)
	}

	if len(messages) > 0 {
		msg := messages[0]
		if msg.Operation != "notify" {
			t.Errorf("Expected operation 'notify', got '%s'", msg.Operation)
		}
		if msg.SenderUUID != device2UUID {
			t.Errorf("Expected sender %s, got %s", device2UUID[:8], msg.SenderUUID[:8])
		}
		if string(msg.Data) != string(notificationData) {
			t.Errorf("Expected data '%s', got '%s'", string(notificationData), string(msg.Data))
		}
	}
}

// TestMultipleNotifications verifies that multiple notifications are delivered correctly
func TestMultipleNotifications(t *testing.T) {
	phone.CleanupDataDir()

	config := PerfectSimulationConfig()

	device1UUID := "BBBBBBBB-1111-1111-1111-111111111111" // Central
	device2UUID := "AAAAAAAA-2222-2222-2222-222222222222" // Peripheral

	wire1 := NewWireWithPlatform(device1UUID, PlatformIOS, "Central Device", config)
	wire2 := NewWireWithPlatform(device2UUID, PlatformAndroid, "Peripheral Device", config)

	defer wire1.Cleanup()
	defer wire2.Cleanup()

	if err := wire1.InitializeDevice(); err != nil {
		t.Fatalf("Failed to initialize device1: %v", err)
	}
	if err := wire2.InitializeDevice(); err != nil {
		t.Fatalf("Failed to initialize device2: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	if err := wire1.Connect(device2UUID); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	serviceUUID := "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	charUUID := "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"

	if err := wire1.SubscribeCharacteristic(device2UUID, serviceUUID, charUUID); err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Send 5 notifications
	for i := 0; i < 5; i++ {
		data := []byte("notification " + string(rune('A'+i)))
		if err := wire2.NotifyCharacteristic(device1UUID, serviceUUID, charUUID, data); err != nil {
			t.Fatalf("Failed to send notification %d: %v", i, err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)

	messages, err := wire1.ReadAndConsumeCharacteristicMessagesFromInbox("central_inbox")
	if err != nil {
		t.Fatalf("Failed to read from central_inbox: %v", err)
	}

	if len(messages) != 5 {
		t.Errorf("Expected 5 notifications, got %d", len(messages))
		for i, msg := range messages {
			t.Logf("Message %d: op=%s, data=%s, sender=%s", i, msg.Operation, string(msg.Data), msg.SenderUUID[:8])
		}
	}
}
