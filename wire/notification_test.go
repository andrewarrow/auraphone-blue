package wire

import (
	"sync"
	"testing"
	"time"

	"github.com/user/auraphone-blue/util"
)

// TestNotificationBasicFlow verifies the complete notification subscription flow
func TestNotificationBasicFlow(t *testing.T) {
	util.SetRandom()
	deviceA := NewWire("device-a-uuid") // Central
	deviceB := NewWire("device-b-uuid") // Peripheral

	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Failed to start device B: %v", err)
	}
	defer deviceB.Stop()

	// Track subscription on peripheral side
	var subscribeCalled bool
	var subscribedCentralUUID string
	deviceB.SetGATTMessageHandler(func(peerUUID string, msg *GATTMessage) {
		if msg.Operation == "subscribe" {
			subscribeCalled = true
			subscribedCentralUUID = peerUUID
		}
	})

	// Track notification on central side
	var notificationReceived bool
	var notificationData []byte
	deviceA.SetGATTMessageHandler(func(peerUUID string, msg *GATTMessage) {
		if msg.Type == "gatt_notification" {
			notificationReceived = true
			notificationData = msg.Data
		}
	})

	time.Sleep(100 * time.Millisecond)

	// Central connects to peripheral
	deviceA.Connect("device-b-uuid")
	time.Sleep(100 * time.Millisecond)

	// Central subscribes to characteristic
	err := deviceA.SubscribeCharacteristic("device-b-uuid", "service-1", "char-1")
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify peripheral received subscription request
	if !subscribeCalled {
		t.Error("Peripheral did not receive subscribe request")
	}
	if subscribedCentralUUID != "device-a-uuid" {
		t.Errorf("Expected subscription from device-a-uuid, got %s", subscribedCentralUUID)
	}

	// Peripheral sends notification
	notificationPayload := []byte("Notification data")
	err = deviceB.NotifyCharacteristic("device-a-uuid", "service-1", "char-1", notificationPayload)
	if err != nil {
		t.Fatalf("Failed to send notification: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify central received notification
	if !notificationReceived {
		t.Error("Central did not receive notification")
	}
	if string(notificationData) != "Notification data" {
		t.Errorf("Expected 'Notification data', got '%s'", string(notificationData))
	}
}

// TestNotificationUnsubscribe verifies unsubscribe flow
func TestNotificationUnsubscribe(t *testing.T) {
	util.SetRandom()
	deviceA := NewWire("device-a-uuid")
	deviceB := NewWire("device-b-uuid")

	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Failed to start device B: %v", err)
	}
	defer deviceB.Stop()

	// Track unsubscribe on peripheral side
	var unsubscribeCalled bool
	deviceB.SetGATTMessageHandler(func(peerUUID string, msg *GATTMessage) {
		if msg.Operation == "unsubscribe" {
			unsubscribeCalled = true
		}
	})

	time.Sleep(100 * time.Millisecond)

	// Connect and subscribe
	deviceA.Connect("device-b-uuid")
	time.Sleep(100 * time.Millisecond)

	deviceA.SubscribeCharacteristic("device-b-uuid", "service-1", "char-1")
	time.Sleep(100 * time.Millisecond)

	// Unsubscribe
	err := deviceA.UnsubscribeCharacteristic("device-b-uuid", "service-1", "char-1")
	if err != nil {
		t.Fatalf("Failed to unsubscribe: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify peripheral received unsubscribe request
	if !unsubscribeCalled {
		t.Error("Peripheral did not receive unsubscribe request")
	}
}

// TestNotificationMultipleCharacteristics verifies notifications on multiple characteristics
func TestNotificationMultipleCharacteristics(t *testing.T) {
	util.SetRandom()
	deviceA := NewWire("device-a-uuid")
	deviceB := NewWire("device-b-uuid")

	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Failed to start device B: %v", err)
	}
	defer deviceB.Stop()

	// Track notifications for different characteristics
	var wg sync.WaitGroup
	wg.Add(2)

	receivedNotifications := make(map[string][]byte)
	var mu sync.Mutex

	deviceA.SetGATTMessageHandler(func(peerUUID string, msg *GATTMessage) {
		if msg.Type == "gatt_notification" {
			mu.Lock()
			receivedNotifications[msg.CharacteristicUUID] = msg.Data
			mu.Unlock()
			wg.Done()
		}
	})

	time.Sleep(100 * time.Millisecond)

	// Connect
	deviceA.Connect("device-b-uuid")
	time.Sleep(100 * time.Millisecond)

	// Subscribe to two characteristics
	deviceA.SubscribeCharacteristic("device-b-uuid", "service-1", "char-1")
	deviceA.SubscribeCharacteristic("device-b-uuid", "service-1", "char-2")
	time.Sleep(100 * time.Millisecond)

	// Send notifications on both characteristics
	deviceB.NotifyCharacteristic("device-a-uuid", "service-1", "char-1", []byte("Notification 1"))
	deviceB.NotifyCharacteristic("device-a-uuid", "service-1", "char-2", []byte("Notification 2"))

	// Wait for both notifications
	wg.Wait()

	// Verify both notifications received
	mu.Lock()
	defer mu.Unlock()

	if len(receivedNotifications) != 2 {
		t.Errorf("Expected 2 notifications, got %d", len(receivedNotifications))
	}

	if string(receivedNotifications["char-1"]) != "Notification 1" {
		t.Errorf("Char-1 notification incorrect: %s", string(receivedNotifications["char-1"]))
	}

	if string(receivedNotifications["char-2"]) != "Notification 2" {
		t.Errorf("Char-2 notification incorrect: %s", string(receivedNotifications["char-2"]))
	}
}

// TestNotificationWithoutSubscription verifies that notifications can be sent without explicit subscribe
// (This tests the deprecated API path)
func TestNotificationWithoutSubscription(t *testing.T) {
	util.SetRandom()
	deviceA := NewWire("device-a-uuid")
	deviceB := NewWire("device-b-uuid")

	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Failed to start device B: %v", err)
	}
	defer deviceB.Stop()

	var notificationReceived bool
	deviceA.SetGATTMessageHandler(func(peerUUID string, msg *GATTMessage) {
		if msg.Type == "gatt_notification" {
			notificationReceived = true
		}
	})

	time.Sleep(100 * time.Millisecond)

	// Connect without subscribing
	deviceA.Connect("device-b-uuid")
	time.Sleep(100 * time.Millisecond)

	// Peripheral sends notification anyway (old behavior)
	deviceB.NotifyCharacteristic("device-a-uuid", "service-1", "char-1", []byte("Notification"))

	time.Sleep(100 * time.Millisecond)

	// Notification should be delivered
	if !notificationReceived {
		t.Error("Notification not received (testing deprecated API)")
	}
}

// TestCCCDDescriptorWrite verifies CCCD descriptor write for enabling notifications
func TestCCCDDescriptorWrite(t *testing.T) {
	util.SetRandom()
	deviceA := NewWire("device-a-uuid")
	deviceB := NewWire("device-b-uuid")

	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Failed to start device B: %v", err)
	}
	defer deviceB.Stop()

	// Track CCCD writes on peripheral side
	var cccdWriteReceived bool
	var cccdValue []byte
	deviceB.SetGATTMessageHandler(func(peerUUID string, msg *GATTMessage) {
		if msg.CharacteristicUUID == "00002902-0000-1000-8000-00805f9b34fb" {
			cccdWriteReceived = true
			cccdValue = msg.Data
		}
	})

	time.Sleep(100 * time.Millisecond)

	// Connect
	deviceA.Connect("device-b-uuid")
	time.Sleep(100 * time.Millisecond)

	// Write CCCD descriptor to enable notifications (0x01 0x00)
	cccdUUID := "00002902-0000-1000-8000-00805f9b34fb"
	enableNotificationValue := []byte{0x01, 0x00}

	err := deviceA.WriteCharacteristic("device-b-uuid", "service-1", cccdUUID, enableNotificationValue)
	if err != nil {
		t.Fatalf("Failed to write CCCD: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify peripheral received CCCD write
	if !cccdWriteReceived {
		t.Error("Peripheral did not receive CCCD write")
	}

	if len(cccdValue) != 2 || cccdValue[0] != 0x01 || cccdValue[1] != 0x00 {
		t.Errorf("CCCD value incorrect: %v", cccdValue)
	}
}

// TestCCCDDescriptorWriteDisable verifies CCCD descriptor write for disabling notifications
func TestCCCDDescriptorWriteDisable(t *testing.T) {
	util.SetRandom()
	deviceA := NewWire("device-a-uuid")
	deviceB := NewWire("device-b-uuid")

	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Failed to start device B: %v", err)
	}
	defer deviceB.Stop()

	var cccdValue []byte
	deviceB.SetGATTMessageHandler(func(peerUUID string, msg *GATTMessage) {
		if msg.CharacteristicUUID == "00002902-0000-1000-8000-00805f9b34fb" {
			cccdValue = msg.Data
		}
	})

	time.Sleep(100 * time.Millisecond)

	deviceA.Connect("device-b-uuid")
	time.Sleep(100 * time.Millisecond)

	// Write CCCD descriptor to disable notifications (0x00 0x00)
	cccdUUID := "00002902-0000-1000-8000-00805f9b34fb"
	disableValue := []byte{0x00, 0x00}

	deviceA.WriteCharacteristic("device-b-uuid", "service-1", cccdUUID, disableValue)
	time.Sleep(100 * time.Millisecond)

	// Verify correct disable value received
	if len(cccdValue) != 2 || cccdValue[0] != 0x00 || cccdValue[1] != 0x00 {
		t.Errorf("CCCD disable value incorrect: %v", cccdValue)
	}
}

// TestNotificationLargePayload verifies notifications with large data payloads
func TestNotificationLargePayload(t *testing.T) {
	util.SetRandom()
	deviceA := NewWire("device-a-uuid")
	deviceB := NewWire("device-b-uuid")

	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Failed to start device B: %v", err)
	}
	defer deviceB.Stop()

	var receivedData []byte
	var wg sync.WaitGroup
	wg.Add(1)

	deviceA.SetGATTMessageHandler(func(peerUUID string, msg *GATTMessage) {
		if msg.Type == "gatt_notification" {
			receivedData = msg.Data
			wg.Done()
		}
	})

	time.Sleep(100 * time.Millisecond)

	deviceA.Connect("device-b-uuid")
	time.Sleep(100 * time.Millisecond)

	// Send large notification
	largePayload := make([]byte, 512)
	for i := range largePayload {
		largePayload[i] = byte(i % 256)
	}

	deviceB.NotifyCharacteristic("device-a-uuid", "service-1", "char-1", largePayload)

	wg.Wait()

	// Verify entire payload received
	if len(receivedData) != 512 {
		t.Errorf("Expected 512 bytes, got %d", len(receivedData))
	}

	// Verify payload content
	for i, b := range receivedData {
		if b != byte(i%256) {
			t.Errorf("Byte %d incorrect: expected %d, got %d", i, i%256, b)
			break
		}
	}
}

// TestNotificationToMultipleCentrals verifies peripheral can notify multiple subscribed centrals
func TestNotificationToMultipleCentrals(t *testing.T) {
	util.SetRandom()
	deviceA := NewWire("device-a-uuid") // Central 1
	deviceB := NewWire("device-b-uuid") // Central 2
	deviceC := NewWire("device-c-uuid") // Peripheral

	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Failed to start device B: %v", err)
	}
	defer deviceB.Stop()

	if err := deviceC.Start(); err != nil {
		t.Fatalf("Failed to start device C: %v", err)
	}
	defer deviceC.Stop()

	var wg sync.WaitGroup
	wg.Add(2)

	var receivedByA bool
	var receivedByB bool

	deviceA.SetGATTMessageHandler(func(peerUUID string, msg *GATTMessage) {
		if msg.Type == "gatt_notification" {
			receivedByA = true
			wg.Done()
		}
	})

	deviceB.SetGATTMessageHandler(func(peerUUID string, msg *GATTMessage) {
		if msg.Type == "gatt_notification" {
			receivedByB = true
			wg.Done()
		}
	})

	time.Sleep(100 * time.Millisecond)

	// Both centrals connect to peripheral
	deviceA.Connect("device-c-uuid")
	deviceB.Connect("device-c-uuid")
	time.Sleep(100 * time.Millisecond)

	// Peripheral sends notification to both
	notificationData := []byte("Broadcast notification")
	deviceC.NotifyCharacteristic("device-a-uuid", "service-1", "char-1", notificationData)
	deviceC.NotifyCharacteristic("device-b-uuid", "service-1", "char-1", notificationData)

	wg.Wait()

	// Verify both centrals received notification
	if !receivedByA {
		t.Error("Central A did not receive notification")
	}
	if !receivedByB {
		t.Error("Central B did not receive notification")
	}
}
