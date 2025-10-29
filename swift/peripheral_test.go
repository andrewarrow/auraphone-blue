package swift

import (
	"fmt"
	"testing"
	"time"

	"github.com/user/auraphone-blue/util"
	"github.com/user/auraphone-blue/wire"
)

func TestCBPeripheral_WriteQueue(t *testing.T) {
	util.SetRandom()

	w := wire.NewWire("test-uuid")
	if err := w.Start(); err != nil {
		t.Fatalf("Failed to start wire: %v", err)
	}
	defer w.Stop()

	remoteWire := wire.NewWire("remote-uuid")
	if err := remoteWire.Start(); err != nil {
		t.Fatalf("Failed to start remote wire: %v", err)
	}
	defer remoteWire.Stop()

	time.Sleep(100 * time.Millisecond)

	// Connect
	if err := w.Connect("remote-uuid"); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	delegate := &testPeripheralDelegate{
		t:             t,
		didWriteValue: make(chan *CBCharacteristic, 20),
	}

	peripheral := &CBPeripheral{
		UUID:       "remote-uuid",
		Name:       "Remote Device",
		wire:       w,
		remoteUUID: "remote-uuid",
		Delegate:   delegate,
		Services: []*CBService{
			{
				UUID:      "service-uuid",
				IsPrimary: true,
				Characteristics: []*CBCharacteristic{
					{
						UUID:       "char-uuid",
						Properties: []string{"write"},
					},
				},
			},
		},
	}

	// Start write queue
	peripheral.StartWriteQueue()
	defer peripheral.StopWriteQueue()

	// Send multiple writes rapidly (queue should handle them)
	numWrites := 10
	for i := 0; i < numWrites; i++ {
		data := []byte(fmt.Sprintf("Write %d", i))
		char := peripheral.GetCharacteristic("service-uuid", "char-uuid")
		err := peripheral.WriteValue(data, char, CBCharacteristicWriteWithResponse)
		if err != nil {
			t.Errorf("Write %d failed: %v", i, err)
		}
	}

	// Verify all writes completed
	receivedCount := 0
	timeout := time.After(5 * time.Second)

	for receivedCount < numWrites {
		select {
		case <-delegate.didWriteValue:
			receivedCount++
		case <-timeout:
			t.Fatalf("Timeout: only %d/%d writes completed", receivedCount, numWrites)
		}
	}

	if receivedCount != numWrites {
		t.Errorf("Expected %d write confirmations, got %d", numWrites, receivedCount)
	}

	t.Logf("✅ Write queue handled %d writes successfully", numWrites)
}

func TestCBPeripheral_WriteWithoutResponse(t *testing.T) {
	util.SetRandom()

	w := wire.NewWire("test-uuid")
	if err := w.Start(); err != nil {
		t.Fatalf("Failed to start wire: %v", err)
	}
	defer w.Stop()

	remoteWire := wire.NewWire("remote-uuid")
	if err := remoteWire.Start(); err != nil {
		t.Fatalf("Failed to start remote wire: %v", err)
	}
	defer remoteWire.Stop()

	time.Sleep(100 * time.Millisecond)

	// Connect
	if err := w.Connect("remote-uuid"); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	delegate := &testPeripheralDelegate{
		t:             t,
		didWriteValue: make(chan *CBCharacteristic, 1),
	}

	peripheral := &CBPeripheral{
		UUID:       "remote-uuid",
		Name:       "Remote Device",
		wire:       w,
		remoteUUID: "remote-uuid",
		Delegate:   delegate,
		Services: []*CBService{
			{
				UUID:      "service-uuid",
				IsPrimary: true,
				Characteristics: []*CBCharacteristic{
					{
						UUID:       "char-uuid",
						Properties: []string{"write_without_response"},
					},
				},
			},
		},
	}

	peripheral.StartWriteQueue()
	defer peripheral.StopWriteQueue()

	// Write without response (should complete immediately)
	char := peripheral.GetCharacteristic("service-uuid", "char-uuid")
	testData := []byte("Fast write")

	start := time.Now()
	err := peripheral.WriteValue(testData, char, CBCharacteristicWriteWithoutResponse)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Wait for callback (should be immediate)
	select {
	case <-delegate.didWriteValue:
		elapsed := time.Since(start)
		t.Logf("✅ Write without response completed in %v", elapsed)
		if elapsed > 100*time.Millisecond {
			t.Errorf("Write without response took too long: %v", elapsed)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timeout waiting for write without response callback")
	}
}
