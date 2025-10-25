package phone

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
)

// HardwareUUIDManager manages the allocation of hardware UUIDs from testdata/hardware_uuids.txt
// These UUIDs represent the unchanging Bluetooth radio hardware identifiers
type HardwareUUIDManager struct {
	availableUUIDs []string
	usedUUIDs      map[string]bool
	mu             sync.Mutex
}

var (
	globalHardwareManager *HardwareUUIDManager
	managerOnce           sync.Once
)

// GetHardwareUUIDManager returns the singleton hardware UUID manager
func GetHardwareUUIDManager() *HardwareUUIDManager {
	managerOnce.Do(func() {
		manager, err := NewHardwareUUIDManager("testdata/hardware_uuids.txt")
		if err != nil {
			fmt.Printf("ERROR: Failed to load hardware UUIDs: %v\n", err)
			// Create empty manager as fallback
			globalHardwareManager = &HardwareUUIDManager{
				availableUUIDs: []string{},
				usedUUIDs:      make(map[string]bool),
			}
			return
		}
		globalHardwareManager = manager
	})
	return globalHardwareManager
}

// NewHardwareUUIDManager creates a new hardware UUID manager
func NewHardwareUUIDManager(filePath string) (*HardwareUUIDManager, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open hardware UUIDs file: %w", err)
	}
	defer file.Close()

	var uuids []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			uuids = append(uuids, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read hardware UUIDs file: %w", err)
	}

	if len(uuids) == 0 {
		return nil, fmt.Errorf("no hardware UUIDs found in file")
	}

	return &HardwareUUIDManager{
		availableUUIDs: uuids,
		usedUUIDs:      make(map[string]bool),
	}, nil
}

// AllocateNextUUID returns the next available hardware UUID
// Returns error if all 12 UUIDs are already allocated
func (m *HardwareUUIDManager) AllocateNextUUID() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Find the first unused UUID
	for _, uuid := range m.availableUUIDs {
		if !m.usedUUIDs[uuid] {
			m.usedUUIDs[uuid] = true
			return uuid, nil
		}
	}

	return "", fmt.Errorf("all %d hardware UUIDs are already allocated (max 12 phones)", len(m.availableUUIDs))
}

// ReleaseUUID marks a hardware UUID as available again
func (m *HardwareUUIDManager) ReleaseUUID(uuid string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.usedUUIDs, uuid)
}

// GetAllocatedCount returns the number of currently allocated UUIDs
func (m *HardwareUUIDManager) GetAllocatedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.usedUUIDs)
}
