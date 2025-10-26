package wire

import (
	"testing"
	"time"
)

// TestMessageFilteringByInboxType verifies that messages are correctly filtered
// based on their operation type when reading from different inbox types.
// This is critical for dual-role devices (iOS/Android) that act as both Central and Peripheral.
func TestMessageFilteringByInboxType(t *testing.T) {
	config := PerfectSimulationConfig() // Zero delays, deterministic
	wire1 := NewWireWithPlatform("device1-uuid", PlatformIOS, "Device 1", config)
	defer wire1.Cleanup()

	// Queue messages of different types that would come from different roles
	testMessages := []*CharacteristicMessage{
		// Messages for peripheral_inbox (from centrals)
		{
			Operation:   "write",
			ServiceUUID: "service-1",
			CharUUID:    "char-1",
			Data:        []byte("write data"),
			SenderUUID:  "sender-1",
			Timestamp:   time.Now().UnixNano(),
		},
		{
			Operation:   "write_no_response",
			ServiceUUID: "service-1",
			CharUUID:    "char-1",
			Data:        []byte("write no response data"),
			SenderUUID:  "sender-2",
			Timestamp:   time.Now().UnixNano(),
		},
		{
			Operation:   "subscribe",
			ServiceUUID: "service-1",
			CharUUID:    "char-1",
			SenderUUID:  "sender-3",
			Timestamp:   time.Now().UnixNano(),
		},
		{
			Operation:   "read",
			ServiceUUID: "service-1",
			CharUUID:    "char-1",
			SenderUUID:  "sender-4",
			Timestamp:   time.Now().UnixNano(),
		},
		// Messages for central_inbox (from peripherals)
		{
			Operation:   "notify",
			ServiceUUID: "service-1",
			CharUUID:    "char-1",
			Data:        []byte("notification data"),
			SenderUUID:  "sender-5",
			Timestamp:   time.Now().UnixNano(),
		},
		{
			Operation:   "indicate",
			ServiceUUID: "service-1",
			CharUUID:    "char-1",
			Data:        []byte("indication data"),
			SenderUUID:  "sender-6",
			Timestamp:   time.Now().UnixNano(),
		},
	}

	// Manually add messages to queue (simulating what would happen in dispatchMessage)
	wire1.queueMutex.Lock()
	wire1.messageQueue = append(wire1.messageQueue, testMessages...)
	wire1.queueMutex.Unlock()

	// Test 1: Read from peripheral_inbox should only get write/read/subscribe operations
	peripheralMessages, err := wire1.ReadAndConsumeCharacteristicMessagesFromInbox("peripheral_inbox")
	if err != nil {
		t.Fatalf("Failed to read from peripheral_inbox: %v", err)
	}

	expectedPeripheralCount := 4 // write, write_no_response, subscribe, read
	if len(peripheralMessages) != expectedPeripheralCount {
		t.Errorf("Expected %d messages in peripheral_inbox, got %d", expectedPeripheralCount, len(peripheralMessages))
	}

	// Verify operations are correct
	peripheralOps := make(map[string]bool)
	for _, msg := range peripheralMessages {
		peripheralOps[msg.Operation] = true
	}

	expectedPeripheralOps := []string{"write", "write_no_response", "subscribe", "read"}
	for _, op := range expectedPeripheralOps {
		if !peripheralOps[op] {
			t.Errorf("Expected operation %s in peripheral_inbox, but not found", op)
		}
	}

	// Test 2: Read from central_inbox should only get notify/indicate operations
	centralMessages, err := wire1.ReadAndConsumeCharacteristicMessagesFromInbox("central_inbox")
	if err != nil {
		t.Fatalf("Failed to read from central_inbox: %v", err)
	}

	expectedCentralCount := 2 // notify, indicate
	if len(centralMessages) != expectedCentralCount {
		t.Errorf("Expected %d messages in central_inbox, got %d", expectedCentralCount, len(centralMessages))
	}

	// Verify operations are correct
	centralOps := make(map[string]bool)
	for _, msg := range centralMessages {
		centralOps[msg.Operation] = true
	}

	expectedCentralOps := []string{"notify", "indicate"}
	for _, op := range expectedCentralOps {
		if !centralOps[op] {
			t.Errorf("Expected operation %s in central_inbox, but not found", op)
		}
	}

	// Test 3: Queue should now be empty
	wire1.queueMutex.Lock()
	remainingCount := len(wire1.messageQueue)
	wire1.queueMutex.Unlock()

	if remainingCount != 0 {
		t.Errorf("Expected queue to be empty after consuming all messages, but %d messages remain", remainingCount)
	}
}

// TestMessageFilteringDoesNotCrossContaminate ensures that reading from one inbox
// doesn't affect messages meant for the other inbox
func TestMessageFilteringDoesNotCrossContaminate(t *testing.T) {
	config := PerfectSimulationConfig()
	wire1 := NewWireWithPlatform("device1-uuid", PlatformAndroid, "Device 1", config)
	defer wire1.Cleanup()

	// Queue one write message and one notify message
	wire1.queueMutex.Lock()
	wire1.messageQueue = []*CharacteristicMessage{
		{
			Operation:  "write",
			CharUUID:   "char-1",
			Data:       []byte("write data"),
			SenderUUID: "sender-1",
			Timestamp:  time.Now().UnixNano(),
		},
		{
			Operation:  "notify",
			CharUUID:   "char-2",
			Data:       []byte("notify data"),
			SenderUUID: "sender-2",
			Timestamp:  time.Now().UnixNano(),
		},
	}
	wire1.queueMutex.Unlock()

	// Read from central_inbox first (should only get notify)
	centralMessages, err := wire1.ReadAndConsumeCharacteristicMessagesFromInbox("central_inbox")
	if err != nil {
		t.Fatalf("Failed to read from central_inbox: %v", err)
	}

	if len(centralMessages) != 1 {
		t.Fatalf("Expected 1 message from central_inbox, got %d", len(centralMessages))
	}

	if centralMessages[0].Operation != "notify" {
		t.Errorf("Expected notify operation, got %s", centralMessages[0].Operation)
	}

	// Verify write message is still in queue
	wire1.queueMutex.Lock()
	queueSize := len(wire1.messageQueue)
	wire1.queueMutex.Unlock()

	if queueSize != 1 {
		t.Errorf("Expected 1 message remaining in queue (the write), got %d", queueSize)
	}

	// Now read from peripheral_inbox (should get write)
	peripheralMessages, err := wire1.ReadAndConsumeCharacteristicMessagesFromInbox("peripheral_inbox")
	if err != nil {
		t.Fatalf("Failed to read from peripheral_inbox: %v", err)
	}

	if len(peripheralMessages) != 1 {
		t.Fatalf("Expected 1 message from peripheral_inbox, got %d", len(peripheralMessages))
	}

	if peripheralMessages[0].Operation != "write" {
		t.Errorf("Expected write operation, got %s", peripheralMessages[0].Operation)
	}

	// Queue should now be empty
	wire1.queueMutex.Lock()
	finalQueueSize := len(wire1.messageQueue)
	wire1.queueMutex.Unlock()

	if finalQueueSize != 0 {
		t.Errorf("Expected queue to be empty, got %d messages", finalQueueSize)
	}
}

// TestEmptyQueueReturnsNil verifies that reading from an empty queue returns nil
func TestEmptyQueueReturnsNil(t *testing.T) {
	config := PerfectSimulationConfig()
	wire1 := NewWireWithPlatform("device1-uuid", PlatformIOS, "Device 1", config)
	defer wire1.Cleanup()

	// Read from empty queue
	messages, err := wire1.ReadAndConsumeCharacteristicMessagesFromInbox("central_inbox")
	if err != nil {
		t.Fatalf("Expected no error from empty queue, got: %v", err)
	}

	if messages != nil {
		t.Errorf("Expected nil from empty queue, got %d messages", len(messages))
	}

	messages, err = wire1.ReadAndConsumeCharacteristicMessagesFromInbox("peripheral_inbox")
	if err != nil {
		t.Fatalf("Expected no error from empty queue, got: %v", err)
	}

	if messages != nil {
		t.Errorf("Expected nil from empty queue, got %d messages", len(messages))
	}
}

// TestMultiplePollingLoopsDoNotInterfere simulates the real scenario where
// both CBPeripheral (central mode) and CBPeripheralManager (peripheral mode)
// are polling the queue simultaneously on the same device
func TestMultiplePollingLoopsDoNotInterfere(t *testing.T) {
	config := PerfectSimulationConfig()
	wire1 := NewWireWithPlatform("device1-uuid", PlatformIOS, "Device 1", config)
	defer wire1.Cleanup()

	// Queue 100 messages alternating between write and notify
	wire1.queueMutex.Lock()
	for i := 0; i < 100; i++ {
		if i%2 == 0 {
			wire1.messageQueue = append(wire1.messageQueue, &CharacteristicMessage{
				Operation:  "write",
				CharUUID:   "char-1",
				Data:       []byte("write data"),
				SenderUUID: "sender-1",
				Timestamp:  time.Now().UnixNano(),
			})
		} else {
			wire1.messageQueue = append(wire1.messageQueue, &CharacteristicMessage{
				Operation:  "notify",
				CharUUID:   "char-2",
				Data:       []byte("notify data"),
				SenderUUID: "sender-2",
				Timestamp:  time.Now().UnixNano(),
			})
		}
	}
	wire1.queueMutex.Unlock()

	// Simulate two polling loops running concurrently
	done := make(chan bool)
	centralCount := 0
	peripheralCount := 0

	// Central polling loop (reads notifications)
	go func() {
		for i := 0; i < 10; i++ {
			messages, _ := wire1.ReadAndConsumeCharacteristicMessagesFromInbox("central_inbox")
			centralCount += len(messages)
			time.Sleep(5 * time.Millisecond)
		}
		done <- true
	}()

	// Peripheral polling loop (reads writes)
	go func() {
		for i := 0; i < 10; i++ {
			messages, _ := wire1.ReadAndConsumeCharacteristicMessagesFromInbox("peripheral_inbox")
			peripheralCount += len(messages)
			time.Sleep(5 * time.Millisecond)
		}
		done <- true
	}()

	// Wait for both to complete
	<-done
	<-done

	// Verify all messages were consumed
	if centralCount != 50 {
		t.Errorf("Expected central loop to receive 50 notify messages, got %d", centralCount)
	}

	if peripheralCount != 50 {
		t.Errorf("Expected peripheral loop to receive 50 write messages, got %d", peripheralCount)
	}

	// Queue should be empty
	wire1.queueMutex.Lock()
	finalQueueSize := len(wire1.messageQueue)
	wire1.queueMutex.Unlock()

	if finalQueueSize != 0 {
		t.Errorf("Expected queue to be empty after both loops consumed all messages, got %d", finalQueueSize)
	}
}
