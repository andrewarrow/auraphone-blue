package wire

import (
	"testing"

	"github.com/user/auraphone-blue/util"
)

// TestGATTTableReadWrite verifies that GATT tables can be written and read
func TestGATTTableReadWrite(t *testing.T) {
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

	// Device A writes GATT table
	gattTable := &GATTTable{
		Services: []GATTService{
			{
				UUID: "service-uuid-1",
				Type: "primary",
				Characteristics: []GATTCharacteristic{
					{
						UUID:       "char-uuid-1",
						Properties: []string{"read", "write", "notify"},
					},
					{
						UUID:       "char-uuid-2",
						Properties: []string{"read"},
					},
				},
			},
			{
				UUID: "service-uuid-2",
				Type: "secondary",
				Characteristics: []GATTCharacteristic{
					{
						UUID:       "char-uuid-3",
						Properties: []string{"write", "indicate"},
					},
				},
			},
		},
	}

	err := deviceA.WriteGATTTable(gattTable)
	if err != nil {
		t.Fatalf("Failed to write GATT table: %v", err)
	}

	// Device B reads GATT table from A
	readTable, err := deviceB.ReadGATTTable("device-a-uuid")
	if err != nil {
		t.Fatalf("Failed to read GATT table: %v", err)
	}

	// Verify table structure
	if len(readTable.Services) != 2 {
		t.Errorf("Expected 2 services, got %d", len(readTable.Services))
	}

	// Verify first service
	if readTable.Services[0].UUID != "service-uuid-1" {
		t.Errorf("Expected service UUID 'service-uuid-1', got '%s'", readTable.Services[0].UUID)
	}

	if readTable.Services[0].Type != "primary" {
		t.Errorf("Expected service type 'primary', got '%s'", readTable.Services[0].Type)
	}

	if len(readTable.Services[0].Characteristics) != 2 {
		t.Errorf("Expected 2 characteristics in first service, got %d", len(readTable.Services[0].Characteristics))
	}

	// Verify characteristic properties
	char1 := readTable.Services[0].Characteristics[0]
	if len(char1.Properties) != 3 {
		t.Errorf("Expected 3 properties for char1, got %d", len(char1.Properties))
	}

	// Verify second service
	if readTable.Services[1].UUID != "service-uuid-2" {
		t.Errorf("Expected service UUID 'service-uuid-2', got '%s'", readTable.Services[1].UUID)
	}

	if readTable.Services[1].Type != "secondary" {
		t.Errorf("Expected service type 'secondary', got '%s'", readTable.Services[1].Type)
	}
}

// TestGATTTableEmptyServices verifies handling of empty GATT tables
func TestGATTTableEmptyServices(t *testing.T) {
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

	// Write empty GATT table
	emptyTable := &GATTTable{
		Services: []GATTService{},
	}

	err := deviceA.WriteGATTTable(emptyTable)
	if err != nil {
		t.Fatalf("Failed to write empty GATT table: %v", err)
	}

	// Read it back
	readTable, err := deviceB.ReadGATTTable("device-a-uuid")
	if err != nil {
		t.Fatalf("Failed to read GATT table: %v", err)
	}

	if len(readTable.Services) != 0 {
		t.Errorf("Expected 0 services, got %d", len(readTable.Services))
	}
}

// TestGATTTableDefaultForNonExistentDevice verifies default GATT table for non-existent devices
func TestGATTTableDefaultForNonExistentDevice(t *testing.T) {
	util.SetRandom()
	deviceA := NewWire("device-a-uuid")

	if err := deviceA.Start(); err != nil {
		t.Fatalf("Failed to start device A: %v", err)
	}
	defer deviceA.Stop()

	// Read GATT table for non-existent device
	readTable, err := deviceA.ReadGATTTable("device-nonexistent-uuid")
	if err != nil {
		t.Fatalf("Expected no error for non-existent device, got: %v", err)
	}

	// Should return empty table
	if len(readTable.Services) != 0 {
		t.Errorf("Expected empty table for non-existent device, got %d services", len(readTable.Services))
	}
}

// TestGATTTableUpdate verifies that GATT tables can be updated
func TestGATTTableUpdate(t *testing.T) {
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

	// Write initial GATT table
	table1 := &GATTTable{
		Services: []GATTService{
			{
				UUID: "service-1",
				Type: "primary",
				Characteristics: []GATTCharacteristic{
					{
						UUID:       "char-1",
						Properties: []string{"read"},
					},
				},
			},
		},
	}
	deviceA.WriteGATTTable(table1)

	// Read it
	readTable1, _ := deviceB.ReadGATTTable("device-a-uuid")
	if len(readTable1.Services) != 1 {
		t.Errorf("Expected 1 service, got %d", len(readTable1.Services))
	}

	// Update GATT table (add another service)
	table2 := &GATTTable{
		Services: []GATTService{
			{
				UUID: "service-1",
				Type: "primary",
				Characteristics: []GATTCharacteristic{
					{
						UUID:       "char-1",
						Properties: []string{"read"},
					},
				},
			},
			{
				UUID: "service-2",
				Type: "primary",
				Characteristics: []GATTCharacteristic{
					{
						UUID:       "char-2",
						Properties: []string{"write", "notify"},
					},
				},
			},
		},
	}
	deviceA.WriteGATTTable(table2)

	// Read updated table
	readTable2, _ := deviceB.ReadGATTTable("device-a-uuid")
	if len(readTable2.Services) != 2 {
		t.Errorf("Expected 2 services after update, got %d", len(readTable2.Services))
	}
}

// TestGATTTableCharacteristicProperties verifies all property types
func TestGATTTableCharacteristicProperties(t *testing.T) {
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

	// Test all property combinations
	gattTable := &GATTTable{
		Services: []GATTService{
			{
				UUID: "service-1",
				Type: "primary",
				Characteristics: []GATTCharacteristic{
					{
						UUID:       "char-read",
						Properties: []string{"read"},
					},
					{
						UUID:       "char-write",
						Properties: []string{"write"},
					},
					{
						UUID:       "char-write-no-response",
						Properties: []string{"write_without_response"},
					},
					{
						UUID:       "char-notify",
						Properties: []string{"notify"},
					},
					{
						UUID:       "char-indicate",
						Properties: []string{"indicate"},
					},
					{
						UUID:       "char-combined",
						Properties: []string{"read", "write", "notify", "indicate"},
					},
				},
			},
		},
	}

	deviceA.WriteGATTTable(gattTable)

	// Read and verify all properties
	readTable, _ := deviceB.ReadGATTTable("device-a-uuid")
	if len(readTable.Services[0].Characteristics) != 6 {
		t.Errorf("Expected 6 characteristics, got %d", len(readTable.Services[0].Characteristics))
	}

	// Verify combined properties characteristic
	combinedChar := readTable.Services[0].Characteristics[5]
	if len(combinedChar.Properties) != 4 {
		t.Errorf("Expected 4 properties for combined characteristic, got %d", len(combinedChar.Properties))
	}
}

// TestGATTTableConcurrency verifies thread-safety of GATT table operations
func TestGATTTableConcurrency(t *testing.T) {
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

	// Write and read GATT tables from multiple goroutines
	done := make(chan bool, 10)
	for i := 0; i < 5; i++ {
		go func() {
			table := &GATTTable{
				Services: []GATTService{
					{
						UUID: "service-1",
						Type: "primary",
						Characteristics: []GATTCharacteristic{
							{
								UUID:       "char-1",
								Properties: []string{"read", "write"},
							},
						},
					},
				},
			}
			deviceA.WriteGATTTable(table)
			done <- true
		}()

		go func() {
			deviceB.ReadGATTTable("device-a-uuid")
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}
