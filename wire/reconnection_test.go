package wire

import (
	"sync"
	"testing"
	"time"

	"github.com/user/auraphone-blue/util"
)

// TestReconnectAfterDisconnect verifies that devices can reconnect after disconnection
func TestReconnectAfterDisconnect(t *testing.T) {
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

	time.Sleep(100 * time.Millisecond)

	// Initial connection
	err := deviceA.Connect("device-b-uuid")
	if err != nil {
		t.Fatalf("Failed initial connect: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if !deviceA.IsConnected("device-b-uuid") {
		t.Fatal("Devices should be connected")
	}

	// Disconnect
	deviceA.Disconnect("device-b-uuid")
	time.Sleep(100 * time.Millisecond)

	if deviceA.IsConnected("device-b-uuid") {
		t.Fatal("Devices should be disconnected")
	}

	// Reconnect
	err = deviceA.Connect("device-b-uuid")
	if err != nil {
		t.Fatalf("Failed to reconnect: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify reconnection successful
	if !deviceA.IsConnected("device-b-uuid") {
		t.Error("Devices should be reconnected")
	}
	if !deviceB.IsConnected("device-a-uuid") {
		t.Error("Device B should see A as connected")
	}
}

// TestReconnectAfterStop verifies reconnection after device restart
func TestReconnectAfterStop(t *testing.T) {
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

	time.Sleep(100 * time.Millisecond)

	// Initial connection
	deviceA.Connect("device-b-uuid")
	time.Sleep(100 * time.Millisecond)

	// Stop device B (simulates crash or power off)
	deviceB.Stop()
	time.Sleep(100 * time.Millisecond)

	// Device A should detect disconnect
	if deviceA.IsConnected("device-b-uuid") {
		t.Error("Device A should detect B's disconnect")
	}

	// Restart device B
	deviceB = NewWire("device-b-uuid")
	if err := deviceB.Start(); err != nil {
		t.Fatalf("Failed to restart device B: %v", err)
	}
	defer deviceB.Stop()

	time.Sleep(100 * time.Millisecond)

	// Device A reconnects
	err := deviceA.Connect("device-b-uuid")
	if err != nil {
		t.Fatalf("Failed to reconnect after restart: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify reconnection successful
	if !deviceA.IsConnected("device-b-uuid") {
		t.Error("Device A should be reconnected to B")
	}
	if !deviceB.IsConnected("device-a-uuid") {
		t.Error("Device B should see A as connected")
	}
}

// TestDisconnectCallbackOnStop verifies disconnect callbacks fire when device stops
func TestDisconnectCallbackOnStop(t *testing.T) {
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

	var wg sync.WaitGroup
	wg.Add(1)

	var disconnectCalled bool
	deviceA.SetDisconnectCallback(func(peerUUID string) {
		if peerUUID == "device-b-uuid" {
			disconnectCalled = true
			wg.Done()
		}
	})

	time.Sleep(100 * time.Millisecond)

	// Connect
	deviceA.Connect("device-b-uuid")
	time.Sleep(100 * time.Millisecond)

	// Stop device B
	deviceB.Stop()

	// Wait for disconnect callback
	wg.Wait()

	if !disconnectCalled {
		t.Error("Disconnect callback was not called")
	}
}

// TestMultipleReconnectCycles verifies stability over multiple connect/disconnect cycles
func TestMultipleReconnectCycles(t *testing.T) {
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

	time.Sleep(100 * time.Millisecond)

	// Perform 5 connect/disconnect cycles
	for i := 0; i < 5; i++ {
		// Connect
		err := deviceA.Connect("device-b-uuid")
		if err != nil {
			t.Fatalf("Failed to connect on cycle %d: %v", i, err)
		}

		time.Sleep(50 * time.Millisecond)

		if !deviceA.IsConnected("device-b-uuid") {
			t.Errorf("Not connected on cycle %d", i)
		}

		// Disconnect
		deviceA.Disconnect("device-b-uuid")
		time.Sleep(50 * time.Millisecond)

		if deviceA.IsConnected("device-b-uuid") {
			t.Errorf("Still connected after disconnect on cycle %d", i)
		}
	}
}

// TestConnectToNonExistentDevice verifies error handling for connecting to non-existent device
func TestConnectToNonExistentDevice(t *testing.T) {
	util.SetRandom()
	deviceA := NewWire("device-a-uuid")

	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	time.Sleep(100 * time.Millisecond)

	// Try to connect to non-existent device
	err := deviceA.Connect("device-nonexistent-uuid")
	if err == nil {
		t.Error("Expected error when connecting to non-existent device")
	}

	// Should not be marked as connected
	if deviceA.IsConnected("device-nonexistent-uuid") {
		t.Error("Should not be connected to non-existent device")
	}
}

// TestDisconnectWhenNotConnected verifies error handling for disconnecting when not connected
func TestDisconnectWhenNotConnected(t *testing.T) {
	util.SetRandom()
	deviceA := NewWire("device-a-uuid")

	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	// Try to disconnect when not connected
	err := deviceA.Disconnect("device-b-uuid")
	if err == nil {
		t.Error("Expected error when disconnecting from non-connected device")
	}
}

// TestSendMessageToDisconnectedDevice verifies error handling for sending messages to disconnected device
func TestSendMessageToDisconnectedDevice(t *testing.T) {
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

	time.Sleep(100 * time.Millisecond)

	// Connect then disconnect
	deviceA.Connect("device-b-uuid")
	time.Sleep(100 * time.Millisecond)

	deviceA.Disconnect("device-b-uuid")
	time.Sleep(100 * time.Millisecond)

	// Try to send message after disconnect
	msg := &GATTMessage{
		Type:               "gatt_request",
		Operation:          "write",
		ServiceUUID:        "service-1",
		CharacteristicUUID: "char-1",
		Data:               []byte("test"),
	}

	err := deviceA.SendGATTMessage("device-b-uuid", msg)
	if err == nil {
		t.Error("Expected error when sending message to disconnected device")
	}
}

// TestConcurrentReconnections verifies thread-safety of concurrent reconnections
func TestConcurrentReconnections(t *testing.T) {
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

	time.Sleep(100 * time.Millisecond)

	// Try multiple concurrent connect attempts
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			deviceA.Connect("device-b-uuid")
		}()
	}

	wg.Wait()
	time.Sleep(200 * time.Millisecond)

	// Should have exactly one connection
	peers := deviceA.GetConnectedPeers()
	if len(peers) != 1 {
		t.Errorf("Expected exactly 1 connection, got %d", len(peers))
	}

	if !deviceA.IsConnected("device-b-uuid") {
		t.Error("Should be connected to device B")
	}
}

// TestMessagePersistenceAfterReconnect verifies messages work after reconnection
func TestMessagePersistenceAfterReconnect(t *testing.T) {
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

	var receivedMessage1 *GATTMessage
	var receivedMessage2 *GATTMessage
	var wg sync.WaitGroup
	wg.Add(2)

	deviceB.SetGATTMessageHandler(func(peerUUID string, msg *GATTMessage) {
		if string(msg.Data) == "Message 1" {
			receivedMessage1 = msg
			wg.Done()
		} else if string(msg.Data) == "Message 2" {
			receivedMessage2 = msg
			wg.Done()
		}
	})

	time.Sleep(100 * time.Millisecond)

	// Initial connection and message
	deviceA.Connect("device-b-uuid")
	time.Sleep(100 * time.Millisecond)

	msg1 := &GATTMessage{
		Type:               "gatt_request",
		Operation:          "write",
		ServiceUUID:        "service-1",
		CharacteristicUUID: "char-1",
		Data:               []byte("Message 1"),
	}
	deviceA.SendGATTMessage("device-b-uuid", msg1)

	// Disconnect
	deviceA.Disconnect("device-b-uuid")
	time.Sleep(100 * time.Millisecond)

	// Reconnect
	deviceA.Connect("device-b-uuid")
	time.Sleep(100 * time.Millisecond)

	// Send message after reconnect
	msg2 := &GATTMessage{
		Type:               "gatt_request",
		Operation:          "write",
		ServiceUUID:        "service-1",
		CharacteristicUUID: "char-1",
		Data:               []byte("Message 2"),
	}
	deviceA.SendGATTMessage("device-b-uuid", msg2)

	wg.Wait()

	// Verify both messages were received
	if receivedMessage1 == nil {
		t.Error("First message was not received")
	}
	if receivedMessage2 == nil {
		t.Error("Second message after reconnect was not received")
	}
}

// TestGetConnectionRoleAfterReconnect verifies role persistence after reconnect
func TestGetConnectionRoleAfterReconnect(t *testing.T) {
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

	time.Sleep(100 * time.Millisecond)

	// Initial connection - A is Central
	deviceA.Connect("device-b-uuid")
	time.Sleep(100 * time.Millisecond)

	role1, _ := deviceA.GetConnectionRole("device-b-uuid")
	if role1 != RoleCentral {
		t.Errorf("Expected Central role, got %s", role1)
	}

	// Disconnect and reconnect
	deviceA.Disconnect("device-b-uuid")
	time.Sleep(100 * time.Millisecond)

	deviceA.Connect("device-b-uuid")
	time.Sleep(100 * time.Millisecond)

	// Role should be same (Central) after reconnect
	role2, _ := deviceA.GetConnectionRole("device-b-uuid")
	if role2 != RoleCentral {
		t.Errorf("Expected Central role after reconnect, got %s", role2)
	}
}

// TestStopIdempotency verifies that Stop() can be called multiple times safely
func TestStopIdempotency(t *testing.T) {
	util.SetRandom()
	deviceA := NewWire("device-a-uuid")

	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Call Stop multiple times
	deviceA.Stop()
	deviceA.Stop()
	deviceA.Stop()

	// Should not panic or error
}
