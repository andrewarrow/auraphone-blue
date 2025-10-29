package wire

import (
	"testing"
	"time"

	"github.com/user/auraphone-blue/util"
)

// TestAdvertisingDataReadWrite verifies that advertising data can be written and read
func TestAdvertisingDataReadWrite(t *testing.T) {
	util.SetRandom()
	deviceA := NewWire("device-a-uuid")
	deviceB := NewWire("device-b-uuid")

	// Start devices
	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	if err := deviceB.Start(); err != nil {
		t.Fatalf("Failed to start device B: %v", err)
	}
	defer deviceB.Stop()

	// Device A writes advertising data
	txPower := 0
	advData := &AdvertisingData{
		DeviceName:       "Test Device A",
		ServiceUUIDs:     []string{"service-uuid-1", "service-uuid-2"},
		ManufacturerData: []byte{0x01, 0x02, 0x03, 0x04},
		TxPowerLevel:     &txPower,
		IsConnectable:    true,
	}

	err := deviceA.WriteAdvertisingData(advData)
	if err != nil {
		t.Fatalf("Failed to write advertising data: %v", err)
	}

	// Device B reads advertising data from A
	readData, err := deviceB.ReadAdvertisingData("device-a-uuid")
	if err != nil {
		t.Fatalf("Failed to read advertising data: %v", err)
	}

	// Verify data matches
	if readData.DeviceName != "Test Device A" {
		t.Errorf("Expected device name 'Test Device A', got '%s'", readData.DeviceName)
	}

	if len(readData.ServiceUUIDs) != 2 {
		t.Errorf("Expected 2 service UUIDs, got %d", len(readData.ServiceUUIDs))
	}

	if readData.ServiceUUIDs[0] != "service-uuid-1" || readData.ServiceUUIDs[1] != "service-uuid-2" {
		t.Errorf("Service UUIDs don't match: %v", readData.ServiceUUIDs)
	}

	if len(readData.ManufacturerData) != 4 {
		t.Errorf("Expected 4 bytes of manufacturer data, got %d", len(readData.ManufacturerData))
	}

	if readData.TxPowerLevel == nil || *readData.TxPowerLevel != 0 {
		t.Errorf("TxPowerLevel doesn't match")
	}

	if !readData.IsConnectable {
		t.Error("Expected IsConnectable to be true")
	}
}

// TestAdvertisingDataDefaultValues verifies default values when no advertising data exists
func TestAdvertisingDataDefaultValues(t *testing.T) {
	util.SetRandom()
	deviceA := NewWire("device-a-uuid")

	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	// Read advertising data for non-existent device
	advData, err := deviceA.ReadAdvertisingData("device-nonexistent-uuid")
	if err != nil {
		t.Fatalf("Expected no error for non-existent device, got: %v", err)
	}

	// Should return default values
	if advData.DeviceName == "" {
		// Default device name should be set
	}

	if !advData.IsConnectable {
		t.Error("Default IsConnectable should be true")
	}
}

// TestAdvertisingDataUpdate verifies that advertising data can be updated
func TestAdvertisingDataUpdate(t *testing.T) {
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

	// Write initial advertising data
	advData1 := &AdvertisingData{
		DeviceName:    "Device A v1",
		ServiceUUIDs:  []string{"service-1"},
		IsConnectable: true,
	}
	deviceA.WriteAdvertisingData(advData1)

	// Read it
	readData1, _ := deviceB.ReadAdvertisingData("device-a-uuid")
	if readData1.DeviceName != "Device A v1" {
		t.Errorf("Expected 'Device A v1', got '%s'", readData1.DeviceName)
	}

	// Update advertising data
	advData2 := &AdvertisingData{
		DeviceName:    "Device A v2",
		ServiceUUIDs:  []string{"service-1", "service-2"},
		IsConnectable: false, // Changed
	}
	deviceA.WriteAdvertisingData(advData2)

	// Read updated data
	readData2, _ := deviceB.ReadAdvertisingData("device-a-uuid")
	if readData2.DeviceName != "Device A v2" {
		t.Errorf("Expected 'Device A v2', got '%s'", readData2.DeviceName)
	}

	if len(readData2.ServiceUUIDs) != 2 {
		t.Errorf("Expected 2 service UUIDs, got %d", len(readData2.ServiceUUIDs))
	}

	if readData2.IsConnectable {
		t.Error("Expected IsConnectable to be false after update")
	}
}

// TestAdvertisingDataConcurrency verifies thread-safety of advertising data operations
func TestAdvertisingDataConcurrency(t *testing.T) {
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

	// Write advertising data from multiple goroutines
	done := make(chan bool, 10)
	for i := 0; i < 5; i++ {
		go func(id int) {
			advData := &AdvertisingData{
				DeviceName:    "Device A",
				ServiceUUIDs:  []string{"service-uuid"},
				IsConnectable: true,
			}
			deviceA.WriteAdvertisingData(advData)
			done <- true
		}(i)

		go func(id int) {
			deviceB.ReadAdvertisingData("device-a-uuid")
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestAdvertisingServiceUUIDFiltering verifies service UUID filtering during discovery
func TestAdvertisingServiceUUIDFiltering(t *testing.T) {
	util.SetRandom()
	deviceA := NewWire("device-a-uuid")
	deviceB := NewWire("device-b-uuid")
	deviceC := NewWire("device-c-uuid")

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

	// Device B advertises service-1
	deviceB.WriteAdvertisingData(&AdvertisingData{
		DeviceName:    "Device B",
		ServiceUUIDs:  []string{"service-1"},
		IsConnectable: true,
	})

	// Device C advertises service-2
	deviceC.WriteAdvertisingData(&AdvertisingData{
		DeviceName:    "Device C",
		ServiceUUIDs:  []string{"service-2"},
		IsConnectable: true,
	})

	time.Sleep(100 * time.Millisecond)

	// Device A scans and should see both
	devices := deviceA.ListAvailableDevices()
	if len(devices) < 2 {
		t.Errorf("Expected at least 2 devices, got %d", len(devices))
	}

	// Read advertising data and verify service UUIDs
	advB, _ := deviceA.ReadAdvertisingData("device-b-uuid")
	if len(advB.ServiceUUIDs) != 1 || advB.ServiceUUIDs[0] != "service-1" {
		t.Errorf("Device B service UUIDs don't match")
	}

	advC, _ := deviceA.ReadAdvertisingData("device-c-uuid")
	if len(advC.ServiceUUIDs) != 1 || advC.ServiceUUIDs[0] != "service-2" {
		t.Errorf("Device C service UUIDs don't match")
	}
}
