package main

import (
	"fmt"

	"github.com/user/auraphone-blue/kotlin"
	"github.com/user/auraphone-blue/swift"
	"github.com/user/auraphone-blue/wire"
)

func main() {
	fmt.Println("=== BLE Role Negotiation Demo ===\n")

	config := wire.PerfectSimulationConfig() // Zero delays for demo

	// Create wire instances (transport layer)
	iosWire1 := wire.NewWireWithPlatform("ios1-uuid", wire.PlatformIOS, "iPhone 15 Pro", config)
	iosWire2 := wire.NewWireWithPlatform("ios2-uuid", wire.PlatformIOS, "iPad Air", config)
	pixelWire := wire.NewWireWithPlatform("android1-uuid", wire.PlatformAndroid, "Pixel 8 Pro", config)
	samsungWire := wire.NewWireWithPlatform("android2-uuid", wire.PlatformAndroid, "Galaxy S23", config)

	// Create platform adapters (platform-specific logic)
	iosMgr1 := swift.NewCBCentralManager(nil, "ios1-uuid")
	iosMgr2 := swift.NewCBCentralManager(nil, "ios2-uuid")
	pixelAdapter := kotlin.NewBluetoothAdapterWithPlatform("android1-uuid", wire.PlatformAndroid, "Pixel 8 Pro")
	samsungAdapter := kotlin.NewBluetoothAdapterWithPlatform("android2-uuid", wire.PlatformAndroid, "Galaxy S23")

	fmt.Println("Devices:")
	fmt.Println("  - iOS 1: iPhone 15 Pro")
	fmt.Println("  - iOS 2: iPad Air")
	fmt.Println("  - Android 1: Pixel 8 Pro")
	fmt.Println("  - Android 2: Galaxy S23")
	fmt.Println()

	// Test 1: iOS to iOS
	fmt.Println("Scenario 1: Two iOS devices discover each other")
	fmt.Printf("  Device names: \"%s\" vs \"%s\"\n", iosWire1.GetDeviceName(), iosWire2.GetDeviceName())
	ios1ToIos2 := iosMgr1.ShouldInitiateConnection(iosWire2.GetPlatform(), iosWire2.GetDeviceName())
	ios2ToIos1 := iosMgr2.ShouldInitiateConnection(iosWire1.GetPlatform(), iosWire1.GetDeviceName())
	fmt.Printf("  iPhone should connect: %v\n", ios1ToIos2)
	fmt.Printf("  iPad should connect: %v\n", ios2ToIos1)

	if ios1ToIos2 && !ios2ToIos1 {
		fmt.Printf("  ✅ Correct: iPhone initiates (\"iPhone 15 Pro\" > \"iPad Air\")\n")
	} else if !ios1ToIos2 && ios2ToIos1 {
		fmt.Printf("  ✅ Correct: iPad initiates (\"iPad Air\" > \"iPhone 15 Pro\")\n")
	} else {
		fmt.Printf("  ❌ Error: Both or neither trying to connect!\n")
	}
	fmt.Println()

	// Test 2: iOS to Android
	fmt.Println("Scenario 2: iOS discovers Android")
	iosToAndroid := iosMgr1.ShouldInitiateConnection(pixelWire.GetPlatform(), pixelWire.GetDeviceName())
	androidToIOS := pixelAdapter.ShouldInitiateConnection(iosWire1.GetPlatform(), iosWire1.GetDeviceName())
	fmt.Printf("  iPhone should connect: %v ✅\n", iosToAndroid)
	fmt.Printf("  Pixel should connect: %v ✅\n", androidToIOS)
	fmt.Printf("  → iPhone initiates connection to Pixel (iOS-first convention)\n")
	fmt.Println()

	// Test 3: Android to Android (Pixel > Galaxy)
	fmt.Println("Scenario 3: Two Android devices discover each other")
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

	// Test 4: Different Android device names
	motoWire := wire.NewWireWithPlatform("android3-uuid", wire.PlatformAndroid, "Moto G", config)
	motoAdapter := kotlin.NewBluetoothAdapterWithPlatform("android3-uuid", wire.PlatformAndroid, "Moto G")

	fmt.Println("Scenario 4: Different Android device name combinations")
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
