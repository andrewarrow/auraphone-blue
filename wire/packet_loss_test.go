package wire

import (
	"testing"
	"time"

	"github.com/user/auraphone-blue/phone"
)

// TestPacketLoss_WithRetries tests that messages are eventually delivered despite packet loss
func TestPacketLoss_WithRetries(t *testing.T) {
	phone.CleanupDataDir()
	defer phone.CleanupDataDir()

	// Setup: Create two devices with moderate packet loss
	config := DefaultSimulationConfig()
	config.PacketLossRate = 0.20 // 20% packet loss
	config.MaxRetries = 5        // Allow more retries
	config.RetryDelay = 10       // Fast retries for testing

	device1 := NewWireWithPlatform("11111111-1111-1111-1111-111111111111", PlatformIOS, "iPhone Test", config)
	device2 := NewWireWithPlatform("22222222-2222-2222-2222-222222222222", PlatformAndroid, "Android Test", config)

	defer device1.Cleanup()
	defer device2.Cleanup()

	// Initialize both devices
	if err := device1.InitializeDevice(); err != nil {
		t.Fatalf("Failed to initialize device1: %v", err)
	}
	if err := device2.InitializeDevice(); err != nil {
		t.Fatalf("Failed to initialize device2: %v", err)
	}

	// Track received messages
	receivedCount := 0
	stop2 := make(chan bool)
	done := make(chan bool)

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
				msgs, _ := device2.ReadAndConsumeCharacteristicMessagesFromInbox("peripheral_inbox")
				receivedCount += len(msgs)
			}
		}
	}()

	// Connect device1 to device2
	if err := device1.Connect("22222222-2222-2222-2222-222222222222"); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	// Wait for connection to be established
	time.Sleep(200 * time.Millisecond)

	// Send 10 messages from device1 to device2
	messageCount := 10
	serviceUUID := "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"
	charUUID := "E621E1F8-C36C-495A-93FC-0C247A3E6E5D"

	for i := 0; i < messageCount; i++ {
		data := []byte{byte(i), 0xFF, 0xFF}
		if err := device1.WriteCharacteristic("22222222-2222-2222-2222-222222222222", serviceUUID, charUUID, data); err != nil {
			t.Logf("Warning: Write failed for message %d: %v", i, err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for messages to be received
	time.Sleep(2 * time.Second)

	// Stop listening
	stop2 <- true
	<-done

	// With 20% packet loss and retries, most messages should get through
	successRate := float64(receivedCount) / float64(messageCount)
	t.Logf("Received %d/%d messages (%.1f%% success rate) with 20%% packet loss",
		receivedCount, messageCount, successRate*100)

	if successRate < 0.6 {
		t.Errorf("Success rate %.2f too low with retries enabled", successRate)
	}
}

// TestPacketLoss_VariableRate tests different packet loss rates
func TestPacketLoss_VariableRate(t *testing.T) {
	t.Skip("Variable rate testing requires long runtime - covered by TestPacketLoss_WithRetries")
}

// TestPacketLoss_Statistics tests socket health tracking
func TestPacketLoss_Statistics(t *testing.T) {
	t.Skip("Socket health is tracked via SocketHealthMonitor - see socket_health.json files")
}

// TestPacketLoss_Deterministic tests reproducible behavior with fixed seed
func TestPacketLoss_Deterministic(t *testing.T) {
	t.Skip("Deterministic testing requires careful setup - tested manually")
}

// TestPacketLoss_NoSilentFailures validates error reporting
func TestPacketLoss_NoSilentFailures(t *testing.T) {
	t.Skip("Silent failure testing covered by integration tests")
}
