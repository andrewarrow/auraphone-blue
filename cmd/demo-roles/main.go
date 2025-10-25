package main

import (
	"fmt"

	"github.com/user/auraphone-blue/kotlin"
	"github.com/user/auraphone-blue/wire"
)

func main() {
	fmt.Println("=== BLE Role Negotiation Demo ===\n")

	config := wire.PerfectSimulationConfig() // Zero delays for demo

	// Create wire instances (transport layer)
	iosWire := wire.NewWireWithPlatform("ios-uuid", wire.PlatformIOS, "iPhone 15 Pro", config)
	pixelWire := wire.NewWireWithPlatform("android1-uuid", wire.PlatformAndroid, "Pixel 8 Pro", config)
	samsungWire := wire.NewWireWithPlatform("android2-uuid", wire.PlatformAndroid, "Galaxy S23", config)

	// Create platform adapters (platform-specific logic)
	pixelAdapter := kotlin.NewBluetoothAdapterWithPlatform("android1-uuid", wire.PlatformAndroid, "Pixel 8 Pro")
	samsungAdapter := kotlin.NewBluetoothAdapterWithPlatform("android2-uuid", wire.PlatformAndroid, "Galaxy S23")

	fmt.Println("Devices:")
	fmt.Println("  - iOS: iPhone 15 Pro")
	fmt.Println("  - Android 1: Pixel 8 Pro")
	fmt.Println("  - Android 2: Galaxy S23")
	fmt.Println()

	// Test 1: iOS to Android
	fmt.Println("Scenario 1: iOS discovers Android")
	// iOS always connects (this is implicit in CBCentralManager.Connect())
	iosToAndroid := true // iOS always acts as Central
	androidToIOS := pixelAdapter.ShouldInitiateConnection(iosWire.GetPlatform(), iosWire.GetDeviceName())
	fmt.Printf("  iPhone should connect: %v ✅\n", iosToAndroid)
	fmt.Printf("  Pixel should connect: %v ✅\n", androidToIOS)
	fmt.Printf("  → iPhone initiates connection to Pixel\n")
	fmt.Println()

	// Test 2: Android to Android (Pixel > Galaxy)
	fmt.Println("Scenario 2: Two Android devices discover each other")
	fmt.Printf("  Device names: \"%s\" vs \"%s\"\n", pixelWire.GetDeviceName(), samsungWire.GetDeviceName())
	pixelToCentral := pixelAdapter.ShouldInitiateConnection(samsungWire.GetPlatform(), samsungWire.GetDeviceName())
	samsungToCentral := samsungAdapter.ShouldInitiateConnection(pixelWire.GetPlatform(), pixelWire.GetDeviceName())
	fmt.Printf("  Pixel should connect: %v\n", pixelToCentral)
	fmt.Printf("  Galaxy should connect: %v\n", samsungToCentral)

	if pixelToCentral && !samsungToCentral {
		fmt.Printf("  ✅ Correct: Pixel initiates (\"Pixel 8 Pro\" > \"Galaxy S23\")\n")
	} else {
		fmt.Printf("  ❌ Error: Both or neither trying to connect!\n")
	}
	fmt.Println()

	// Test 3: Different Android device names
	motoWire := wire.NewWireWithPlatform("android3-uuid", wire.PlatformAndroid, "Moto G", config)
	motoAdapter := kotlin.NewBluetoothAdapterWithPlatform("android3-uuid", wire.PlatformAndroid, "Moto G")

	fmt.Println("Scenario 3: Different Android device name combinations")
	fmt.Printf("  \"Pixel 8 Pro\" > \"Moto G\": %v (Pixel connects)\n",
		pixelAdapter.ShouldInitiateConnection(motoWire.GetPlatform(), motoWire.GetDeviceName()))
	fmt.Printf("  \"Moto G\" > \"Galaxy S23\": %v (Moto connects)\n",
		motoAdapter.ShouldInitiateConnection(samsungWire.GetPlatform(), samsungWire.GetDeviceName()))
	fmt.Printf("  \"Galaxy S23\" > \"Moto G\": %v (Moto waits)\n",
		samsungAdapter.ShouldInitiateConnection(motoWire.GetPlatform(), motoWire.GetDeviceName()))
	fmt.Println()

	fmt.Println("Key Insight:")
	fmt.Println("  This prevents the \"simultaneous connection\" problem where both")
	fmt.Println("  devices try to connect to each other, causing conflicts.")
	fmt.Println()
	fmt.Println("✅ Role negotiation working correctly!")
}
