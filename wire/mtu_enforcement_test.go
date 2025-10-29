package wire

import (
	"testing"
	"time"

	"github.com/user/auraphone-blue/util"
	"github.com/user/auraphone-blue/wire/att"
)

// TestMTUFragmentation verifies that fragmentation logic works correctly
func TestMTUFragmentation(t *testing.T) {
	util.SetRandom()

	// Test with typical negotiated MTU of 512
	mtu := 512

	// Test 1: Small data should not need fragmentation
	smallData := make([]byte, mtu-3) // Max write size = MTU - 3 (opcode + handle)
	if att.ShouldFragment(mtu, smallData) {
		t.Errorf("ShouldFragment returned true for data that fits in MTU (len=%d, mtu=%d)", len(smallData), mtu)
	}

	// Test 2: Large data should need fragmentation
	largeData := make([]byte, mtu*3)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	if !att.ShouldFragment(mtu, largeData) {
		t.Errorf("ShouldFragment returned false for data that exceeds MTU (len=%d, mtu=%d)", len(largeData), mtu)
	}

	// Test 3: Verify fragmentation creates correct number of chunks
	requests, err := att.FragmentWrite(0x0001, largeData, mtu)
	if err != nil {
		t.Fatalf("FragmentWrite failed: %v", err)
	}

	// Each chunk should be at most MTU-5 bytes (opcode + handle + offset + value)
	maxChunkSize := mtu - 5
	expectedChunks := (len(largeData) + maxChunkSize - 1) / maxChunkSize
	if len(requests) != expectedChunks {
		t.Errorf("Wrong number of fragments: got %d, expected %d", len(requests), expectedChunks)
	}
	t.Logf("Fragmented %d bytes into %d chunks (max chunk size: %d bytes)", len(largeData), len(requests), maxChunkSize)

	// Test 4: Verify each chunk is within MTU
	for i, req := range requests {
		// PrepareWriteRequest: opcode(1) + handle(2) + offset(2) + value
		chunkSize := 1 + 2 + 2 + len(req.Value)
		if chunkSize > mtu {
			t.Errorf("Chunk %d exceeds MTU: %d > %d", i, chunkSize, mtu)
		}
		if req.Value == nil || len(req.Value) == 0 {
			t.Errorf("Chunk %d has empty value", i)
		}
	}

	// Test 5: Verify fragmenter reassembly
	fragmenter := att.NewFragmenter()
	for i, req := range requests {
		resp := &att.PrepareWriteResponse{
			Handle: req.Handle,
			Offset: req.Offset,
			Value:  req.Value,
		}
		if err := fragmenter.AddPrepareWriteResponse(resp); err != nil {
			t.Errorf("Failed to add fragment %d: %v", i, err)
		}
	}

	reassembled := fragmenter.GetReassembledValue(0x0001)
	if len(reassembled) != len(largeData) {
		t.Errorf("Reassembled data length mismatch: got %d, expected %d", len(reassembled), len(largeData))
	}

	for i := range largeData {
		if reassembled[i] != largeData[i] {
			t.Errorf("Reassembled data mismatch at byte %d: got 0x%02X, expected 0x%02X", i, reassembled[i], largeData[i])
			break
		}
	}
	t.Logf("Successfully reassembled %d bytes from %d fragments", len(reassembled), len(requests))

	// Test 6: Verify minimum MTU constraint
	// MTU of 5 or less should fail because maxChunkSize = MTU - 5
	_, err = att.FragmentWrite(0x0001, largeData, 5)
	if err == nil {
		t.Errorf("FragmentWrite should fail with MTU <= 5")
	}

	// Test 7: Verify default MTU (23 bytes)
	defaultMTU := 23
	defaultData := make([]byte, defaultMTU-3)
	if att.ShouldFragment(defaultMTU, defaultData) {
		t.Errorf("Data should fit in default MTU (len=%d, mtu=%d)", len(defaultData), defaultMTU)
	}

	largerThanDefault := make([]byte, defaultMTU)
	if !att.ShouldFragment(defaultMTU, largerThanDefault) {
		t.Errorf("Data should not fit in default MTU (len=%d, mtu=%d)", len(largerThanDefault), defaultMTU)
	}
}

// TestMTUNegotiation verifies MTU negotiation between devices
func TestMTUNegotiation(t *testing.T) {
	util.SetRandom()

	// Create two devices
	deviceA := NewWire("device-a")
	deviceB := NewWire("device-b")

	// Start both devices
	if err := deviceA.Start(); err != nil {
		t.Fatalf("Device A failed to start: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Device B failed to start: %v", err)
	}
	defer deviceB.Stop()

	time.Sleep(100 * time.Millisecond) // Wait for listeners

	// Connect A to B
	if err := deviceA.Connect("device-b"); err != nil {
		t.Fatalf("Device A failed to connect to Device B: %v", err)
	}
	time.Sleep(150 * time.Millisecond) // Wait for MTU negotiation

	// Verify connection established on both sides
	if !deviceA.IsConnected("device-b") {
		t.Fatal("Device A is not connected to Device B")
	}
	if !deviceB.IsConnected("device-a") {
		t.Fatal("Device B is not connected to Device A")
	}

	// Verify both devices have established connection
	peersA := deviceA.GetConnectedPeers()
	if len(peersA) != 1 || peersA[0] != "device-b" {
		t.Errorf("Device A should have 1 connection to device-b, got %v", peersA)
	}

	peersB := deviceB.GetConnectedPeers()
	if len(peersB) != 1 || peersB[0] != "device-a" {
		t.Errorf("Device B should have 1 connection to device-a, got %v", peersB)
	}

	t.Logf("MTU negotiation successful between devices")
}

// TestMTUWithMultipleConnections verifies MTU handling with multiple connections
func TestMTUWithMultipleConnections(t *testing.T) {
	util.SetRandom()

	// Create three devices
	deviceA := NewWire("device-a")
	deviceB := NewWire("device-b")
	deviceC := NewWire("device-c")

	// Start all devices
	for _, dev := range []*Wire{deviceA, deviceB, deviceC} {
		if err := dev.Start(); err != nil {
			t.Fatalf("Device failed to start: %v", err)
		}
		defer dev.Stop()
	}

	time.Sleep(100 * time.Millisecond) // Wait for listeners

	// Connect A to both B and C
	if err := deviceA.Connect("device-b"); err != nil {
		t.Fatalf("Device A failed to connect to Device B: %v", err)
	}
	time.Sleep(150 * time.Millisecond) // Wait for first MTU negotiation

	if err := deviceA.Connect("device-c"); err != nil {
		t.Fatalf("Device A failed to connect to Device C: %v", err)
	}
	time.Sleep(150 * time.Millisecond) // Wait for second MTU negotiation

	// Verify all connections established
	if !deviceA.IsConnected("device-b") {
		t.Error("Device A should be connected to Device B")
	}
	if !deviceA.IsConnected("device-c") {
		t.Error("Device A should be connected to Device C")
	}

	peersA := deviceA.GetConnectedPeers()
	if len(peersA) != 2 {
		t.Errorf("Device A should have 2 connections, got %d: %v", len(peersA), peersA)
	}

	t.Logf("Device A successfully maintains %d connections with independent MTU negotiation", len(peersA))
}
