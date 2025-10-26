package wire

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// TestDualRoleMessageDelivery verifies that messages are correctly delivered
// in a dual-role scenario where both devices act as Central and Peripheral
func TestDualRoleMessageDelivery(t *testing.T) {
	// Use perfect simulation config for deterministic testing
	config := PerfectSimulationConfig()

	// Create two devices
	device1UUID := "11111111-1111-1111-1111-111111111111"
	device2UUID := "22222222-2222-2222-2222-222222222222"

	wire1 := NewWireWithPlatform(device1UUID, PlatformIOS, "Device 1", config)
	wire2 := NewWireWithPlatform(device2UUID, PlatformAndroid, "Device 2", config)

	defer wire1.Cleanup()
	defer wire2.Cleanup()

	// Initialize devices (creates sockets and starts listeners)
	if err := wire1.InitializeDevice(); err != nil {
		t.Fatalf("Failed to initialize device1: %v", err)
	}
	if err := wire2.InitializeDevice(); err != nil {
		t.Fatalf("Failed to initialize device2: %v", err)
	}

	// Track messages received
	var device1ReceivedWrites int
	var device1ReceivedNotifies int
	var device2ReceivedWrites int
	var device2ReceivedNotifies int

	// Stop channels to cleanly shut down polling goroutines
	stop1 := make(chan bool)
	stop2 := make(chan bool)
	done := make(chan bool, 2)

	// Start listening on device1 (simulates CBPeripheralManager and CBPeripheral polling)
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-stop1:
				done <- true
				return
			case <-ticker.C:
				// Peripheral inbox (receives writes from centrals)
				peripheralMsgs, _ := wire1.ReadAndConsumeCharacteristicMessagesFromInbox("peripheral_inbox")
				device1ReceivedWrites += len(peripheralMsgs)

				// Central inbox (receives notifications from peripherals)
				centralMsgs, _ := wire1.ReadAndConsumeCharacteristicMessagesFromInbox("central_inbox")
				device1ReceivedNotifies += len(centralMsgs)
			}
		}
	}()

	// Start listening on device2
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-stop2:
				done <- true
				return
			case <-ticker.C:
				peripheralMsgs, _ := wire2.ReadAndConsumeCharacteristicMessagesFromInbox("peripheral_inbox")
				device2ReceivedWrites += len(peripheralMsgs)

				centralMsgs, _ := wire2.ReadAndConsumeCharacteristicMessagesFromInbox("central_inbox")
				device2ReceivedNotifies += len(centralMsgs)
			}
		}
	}()

	// Establish dual connections
	if err := wire1.Connect(device2UUID); err != nil {
		t.Fatalf("Failed to connect device1 to device2: %v", err)
	}

	// Wait for connections to establish
	time.Sleep(100 * time.Millisecond)

	// Device1 acts as Central, writes to Device2's characteristic (Device2 is Peripheral)
	serviceUUID := "service-uuid-1"
	charUUID := "char-uuid-1"
	testData := []byte("test gossip data from device1")

	if err := wire1.WriteCharacteristic(device2UUID, serviceUUID, charUUID, testData); err != nil {
		t.Fatalf("Device1 failed to write: %v", err)
	}

	// Device2 acts as Central, writes to Device1's characteristic (Device1 is Peripheral)
	testData2 := []byte("test gossip data from device2")

	if err := wire2.WriteCharacteristic(device1UUID, serviceUUID, charUUID, testData2); err != nil {
		t.Fatalf("Device2 failed to write: %v", err)
	}

	// Wait for messages to be processed
	time.Sleep(200 * time.Millisecond)

	// Stop the polling goroutines
	close(stop1)
	close(stop2)
	<-done
	<-done

	// Verify Device2 received the write from Device1
	if device2ReceivedWrites == 0 {
		t.Error("Device2 should have received write message from Device1 in its peripheral_inbox")
	}

	// Verify Device1 received the write from Device2
	if device1ReceivedWrites == 0 {
		t.Error("Device1 should have received write message from Device2 in its peripheral_inbox")
	}

	// Verify no cross-contamination (writes shouldn't appear in central_inbox)
	if device1ReceivedNotifies > 0 {
		t.Errorf("Device1 central_inbox should not have received write messages (got %d)", device1ReceivedNotifies)
	}

	if device2ReceivedNotifies > 0 {
		t.Errorf("Device2 central_inbox should not have received write messages (got %d)", device2ReceivedNotifies)
	}

	t.Logf("✅ Device1: received %d writes, %d notifies", device1ReceivedWrites, device1ReceivedNotifies)
	t.Logf("✅ Device2: received %d writes, %d notifies", device2ReceivedWrites, device2ReceivedNotifies)
}

// TestNotificationDelivery verifies that notifications flow correctly in the opposite direction
func TestNotificationDelivery(t *testing.T) {
	config := PerfectSimulationConfig()

	device1UUID := "33333333-3333-3333-3333-333333333333"
	device2UUID := "44444444-4444-4444-4444-444444444444"

	wire1 := NewWireWithPlatform(device1UUID, PlatformIOS, "Device 1", config)
	wire2 := NewWireWithPlatform(device2UUID, PlatformAndroid, "Device 2", config)

	defer wire1.Cleanup()
	defer wire2.Cleanup()

	// Initialize devices (creates sockets and starts listeners)
	if err := wire1.InitializeDevice(); err != nil {
		t.Fatalf("Failed to initialize device1: %v", err)
	}
	if err := wire2.InitializeDevice(); err != nil {
		t.Fatalf("Failed to initialize device2: %v", err)
	}

	// Track notifications
	var device1Notifications int
	var device2Notifications int

	// Stop channels to cleanly shut down polling goroutines
	stop1 := make(chan bool)
	stop2 := make(chan bool)
	done := make(chan bool, 2)

	// Listening loops
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-stop1:
				done <- true
				return
			case <-ticker.C:
				centralMsgs, _ := wire1.ReadAndConsumeCharacteristicMessagesFromInbox("central_inbox")
				device1Notifications += len(centralMsgs)
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-stop2:
				done <- true
				return
			case <-ticker.C:
				centralMsgs, _ := wire2.ReadAndConsumeCharacteristicMessagesFromInbox("central_inbox")
				device2Notifications += len(centralMsgs)
			}
		}
	}()

	// Establish connections
	if err := wire1.Connect(device2UUID); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Characteristic UUIDs for the test
	serviceUUID := "service-uuid-1"
	charUUID := "char-uuid-1"

	// IMPORTANT: In real BLE, Central must subscribe before Peripheral can send notifications
	// Device2 acts as Central and subscribes to Device1's characteristic
	if err := wire2.SubscribeCharacteristic(device1UUID, serviceUUID, charUUID); err != nil {
		t.Fatalf("Device2 failed to subscribe to Device1: %v", err)
	}

	// Device1 acts as Central and subscribes to Device2's characteristic
	if err := wire1.SubscribeCharacteristic(device2UUID, serviceUUID, charUUID); err != nil {
		t.Fatalf("Device1 failed to subscribe to Device2: %v", err)
	}

	// Wait for subscriptions to be processed
	time.Sleep(50 * time.Millisecond)

	// Now Device1 can send notification to Device2 (Device1 is Peripheral, Device2 is Central)
	notifyData := []byte("notification from device1")

	if err := wire1.NotifyCharacteristic(device2UUID, serviceUUID, charUUID, notifyData); err != nil {
		t.Fatalf("Device1 failed to notify: %v", err)
	}

	// Device2 sends notification to Device1
	notifyData2 := []byte("notification from device2")

	if err := wire2.NotifyCharacteristic(device1UUID, serviceUUID, charUUID, notifyData2); err != nil {
		t.Fatalf("Device2 failed to notify: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Stop the polling goroutines
	close(stop1)
	close(stop2)
	<-done
	<-done

	// Verify both devices received notifications
	if device1Notifications == 0 {
		t.Error("Device1 should have received notification from Device2 in its central_inbox")
	}

	if device2Notifications == 0 {
		t.Error("Device2 should have received notification from Device1 in its central_inbox")
	}

	t.Logf("✅ Device1: received %d notifications", device1Notifications)
	t.Logf("✅ Device2: received %d notifications", device2Notifications)
}

// TestConcurrentPollingDoesNotLoseMessages verifies that when both
// CBPeripheral and CBPeripheralManager poll simultaneously, no messages are lost
func TestConcurrentPollingDoesNotLoseMessages(t *testing.T) {
	config := PerfectSimulationConfig()

	device1UUID := "55555555-5555-5555-5555-555555555555"
	device2UUID := "66666666-6666-6666-6666-666666666666"

	wire1 := NewWireWithPlatform(device1UUID, PlatformIOS, "Device 1", config)
	wire2 := NewWireWithPlatform(device2UUID, PlatformAndroid, "Device 2", config)

	defer wire1.Cleanup()
	defer wire2.Cleanup()

	// Initialize devices (creates sockets and starts listeners)
	if err := wire1.InitializeDevice(); err != nil {
		t.Fatalf("Failed to initialize device1: %v", err)
	}
	if err := wire2.InitializeDevice(); err != nil {
		t.Fatalf("Failed to initialize device2: %v", err)
	}

	// Establish connections
	if err := wire1.Connect(device2UUID); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Characteristic UUIDs for the test
	serviceUUID := "service-uuid"
	charUUID := "char-uuid"

	// Subscribe so notifications can be sent
	// Device2 subscribes to Device1's characteristic (Device2 is Central, Device1 is Peripheral)
	if err := wire2.SubscribeCharacteristic(device1UUID, serviceUUID, charUUID); err != nil {
		t.Fatalf("Device2 failed to subscribe: %v", err)
	}

	// Wait for subscription to be processed
	time.Sleep(50 * time.Millisecond)

	// Send 100 messages alternating between writes and notifies
	for i := 0; i < 50; i++ {
		// Send write (should go to peripheral_inbox)
		writeData := []byte(fmt.Sprintf("write message %d", i))
		if err := wire2.WriteCharacteristic(device1UUID, serviceUUID, charUUID, writeData); err != nil {
			t.Errorf("Failed to send write %d: %v", i, err)
		}

		// Send notification (should go to central_inbox)
		notifyData := []byte(fmt.Sprintf("notify message %d", i))
		if err := wire1.NotifyCharacteristic(device2UUID, serviceUUID, charUUID, notifyData); err != nil {
			t.Errorf("Failed to send notify %d: %v", i, err)
		}
	}

	// Wait for all messages to be queued
	time.Sleep(200 * time.Millisecond)

	// Poll from both inboxes simultaneously (simulates real scenario)
	var writesReceived, notifiesReceived int
	done := make(chan bool, 2)

	// Peripheral inbox polling (like CBPeripheralManager)
	go func() {
		for i := 0; i < 20; i++ {
			msgs, _ := wire1.ReadAndConsumeCharacteristicMessagesFromInbox("peripheral_inbox")
			writesReceived += len(msgs)
			time.Sleep(5 * time.Millisecond)
		}
		done <- true
	}()

	// Central inbox polling (like CBPeripheral)
	go func() {
		for i := 0; i < 20; i++ {
			msgs, _ := wire2.ReadAndConsumeCharacteristicMessagesFromInbox("central_inbox")
			notifiesReceived += len(msgs)
			time.Sleep(5 * time.Millisecond)
		}
		done <- true
	}()

	// Wait for all polling loops to complete
	<-done
	<-done

	// Verify all messages were received
	if writesReceived != 50 {
		t.Errorf("Expected to receive 50 writes, got %d", writesReceived)
	}

	if notifiesReceived != 50 {
		t.Errorf("Expected to receive 50 notifies, got %d", notifiesReceived)
	}

	t.Logf("✅ Successfully received all messages: %d writes, %d notifies", writesReceived, notifiesReceived)
}

// TestMain handles cleanup after all tests
func TestMain(m *testing.M) {
	code := m.Run()

	// Cleanup test socket files
	os.RemoveAll("/tmp/auraphone-*.sock")

	os.Exit(code)
}
