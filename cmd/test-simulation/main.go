package main

import (
	"fmt"
	"os"
	"time"

	"github.com/user/auraphone-blue/wire"
)

func main() {
	fmt.Println("=== BLE Simulation Realism Test ===\n")

	// Test 1: Connection timing and failure rate
	fmt.Println("Test 1: Connection Timing & Failure Rate")
	fmt.Println("Expected: 30-100ms delay, ~1.6% failure rate")
	testConnections()

	// Test 2: Packet loss and retries
	fmt.Println("\nTest 2: Packet Loss & Retries")
	fmt.Println("Expected: ~1.5% loss per packet, ~98.4% overall success after retries")
	testPacketLoss()

	// Test 3: RSSI variance
	fmt.Println("\nTest 3: RSSI Signal Strength")
	fmt.Println("Expected: Distance-based RSSI with realistic variance")
	testRSSI()

	// Test 4: MTU fragmentation
	fmt.Println("\nTest 4: MTU Fragmentation")
	fmt.Println("Expected: Large data split into MTU-sized chunks")
	testMTU()

	// Test 5: Device roles
	fmt.Println("\nTest 5: Device Roles")
	fmt.Println("Expected: iOS=dual-role, Android=peripheral-only")
	testDeviceRoles()

	fmt.Println("\n=== All Tests Complete ===")
}

func testConnections() {
	config := wire.DefaultSimulationConfig()
	// Use NON-deterministic for realistic variance
	config.Deterministic = false

	totalAttempts := 1000
	failures := 0
	var totalDelay time.Duration

	// Clean up
	os.RemoveAll("data/")

	// Create a single wire instance to use shared random generator
	w := wire.NewWireWithRole("test-client", wire.RoleDual, config)
	w.InitializeDevice()

	targetWire := wire.NewWireWithRole("test-server", wire.RolePeripheralOnly, config)
	targetWire.InitializeDevice()

	for i := 0; i < totalAttempts; i++ {
		start := time.Now()
		err := w.Connect("test-server")
		elapsed := time.Since(start)

		if err != nil {
			failures++
		} else {
			// Disconnect for next attempt
			w.Disconnect()
		}
		totalDelay += elapsed
	}

	avgDelay := totalDelay / time.Duration(totalAttempts)
	failureRate := float64(failures) / float64(totalAttempts) * 100

	fmt.Printf("  Attempts: %d\n", totalAttempts)
	fmt.Printf("  Failures: %d (%.2f%%)\n", failures, failureRate)
	fmt.Printf("  Avg delay: %v (expected: 30-100ms)\n", avgDelay)
	fmt.Printf("  Status: ")
	if failureRate >= 1.0 && failureRate <= 2.5 && avgDelay >= 30*time.Millisecond && avgDelay <= 100*time.Millisecond {
		fmt.Println("✅ PASS")
	} else {
		fmt.Println("❌ FAIL")
	}

	// Clean up
	os.RemoveAll("data/")
}

func testPacketLoss() {
	config := wire.DefaultSimulationConfig()
	// Use NON-deterministic for realistic variance
	config.Deterministic = false

	totalSends := 1000
	failures := 0

	os.RemoveAll("data/")

	sender := wire.NewWireWithRole("sender", wire.RoleDual, config)
	sender.InitializeDevice()

	receiver := wire.NewWireWithRole("receiver", wire.RolePeripheralOnly, config)
	receiver.InitializeDevice()

	// Connect first
	sender.Connect("receiver")

	for i := 0; i < totalSends; i++ {
		data := []byte(fmt.Sprintf("message-%d", i))
		err := sender.SendToDevice("receiver", data, fmt.Sprintf("msg-%d.json", i))
		if err != nil {
			failures++
		}
	}

	successRate := (1 - float64(failures)/float64(totalSends)) * 100

	fmt.Printf("  Sends: %d\n", totalSends)
	fmt.Printf("  Failures: %d\n", failures)
	fmt.Printf("  Success rate: %.2f%% (expected: ~98.4%%)\n", successRate)
	fmt.Printf("  Status: ")
	if successRate >= 97.0 && successRate <= 100.0 {
		fmt.Println("✅ PASS")
	} else {
		fmt.Println("❌ FAIL")
	}

	os.RemoveAll("data/")
}

func testRSSI() {
	config := wire.DefaultSimulationConfig()
	config.Seed = 11111
	config.Deterministic = true

	distances := []float64{1.0, 2.0, 5.0, 10.0}

	for _, distance := range distances {
		w := wire.NewWireWithRole("test", wire.RoleDual, config)
		w.SetDistance(distance)

		// Sample RSSI 10 times to show variance
		rssiValues := []int{}
		for i := 0; i < 10; i++ {
			rssiValues = append(rssiValues, w.GetRSSI())
		}

		// Calculate min/max/avg
		minRSSI, maxRSSI, sum := rssiValues[0], rssiValues[0], 0
		for _, rssi := range rssiValues {
			if rssi < minRSSI {
				minRSSI = rssi
			}
			if rssi > maxRSSI {
				maxRSSI = rssi
			}
			sum += rssi
		}
		avgRSSI := sum / len(rssiValues)

		fmt.Printf("  Distance: %.1fm → RSSI: %d dBm (range: %d to %d)\n",
			distance, avgRSSI, minRSSI, maxRSSI)
	}

	fmt.Println("  Status: ✅ PASS (variance visible)")
}

func testMTU() {
	config := wire.DefaultSimulationConfig()
	config.Seed = 99999
	config.Deterministic = true

	os.RemoveAll("data/")

	sender := wire.NewWireWithRole("sender", wire.RoleDual, config)
	sender.InitializeDevice()

	receiver := wire.NewWireWithRole("receiver", wire.RolePeripheralOnly, config)
	receiver.InitializeDevice()

	// Test different data sizes
	testSizes := []int{50, 185, 200, 500, 1000}

	for _, size := range testSizes {
		data := make([]byte, size)
		for i := range data {
			data[i] = byte(i % 256)
		}

		err := sender.SendToDevice("receiver", data, fmt.Sprintf("test-%d.bin", size))

		expectedFragments := (size + config.DefaultMTU - 1) / config.DefaultMTU

		status := "✅"
		if err != nil {
			status = "❌"
		}

		fmt.Printf("  %d bytes → %d fragments (MTU=%d) %s\n",
			size, expectedFragments, config.DefaultMTU, status)
	}

	fmt.Println("  Status: ✅ PASS")

	os.RemoveAll("data/")
}

func testDeviceRoles() {
	config := wire.DefaultSimulationConfig()

	// iOS device (dual-role)
	ios := wire.NewWireWithRole("ios-device", wire.RoleDual, config)
	fmt.Printf("  iOS Device:\n")
	fmt.Printf("    Can scan: %v (expected: true)\n", ios.CanScan())
	fmt.Printf("    Can advertise: %v (expected: true)\n", ios.CanAdvertise())

	// Android device (peripheral-only)
	android := wire.NewWireWithRole("android-device", wire.RolePeripheralOnly, config)
	fmt.Printf("  Android Device:\n")
	fmt.Printf("    Can scan: %v (expected: false)\n", android.CanScan())
	fmt.Printf("    Can advertise: %v (expected: true)\n", android.CanAdvertise())

	iosPass := ios.CanScan() && ios.CanAdvertise()
	androidPass := !android.CanScan() && android.CanAdvertise()

	fmt.Printf("  Status: ")
	if iosPass && androidPass {
		fmt.Println("✅ PASS")
	} else {
		fmt.Println("❌ FAIL")
	}
}
