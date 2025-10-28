package gui

import (
	"fmt"
	"os"
	"sync"
)

// HardwareUUIDManager manages allocation of hardware UUIDs from testdata/hardware_uuids.txt
// This is a simulation utility - real phones have actual hardware UUIDs
type HardwareUUIDManager struct {
	uuids          []string
	allocatedIdx   int
	allocatedUUIDs map[string]bool
	mu             sync.Mutex
}

var (
	globalManager     *HardwareUUIDManager
	globalManagerOnce sync.Once
)

// GetHardwareUUIDManager returns the singleton hardware UUID manager
func GetHardwareUUIDManager() *HardwareUUIDManager {
	globalManagerOnce.Do(func() {
		globalManager = &HardwareUUIDManager{
			allocatedUUIDs: make(map[string]bool),
		}
		// Load UUIDs from testdata/hardware_uuids.txt
		data, err := os.ReadFile("testdata/hardware_uuids.txt")
		if err != nil {
			panic(fmt.Sprintf("Failed to load hardware_uuids.txt: %v", err))
		}
		// Parse UUIDs (one per line)
		lines := string(data)
		for i := 0; i < len(lines); {
			// Find end of line
			end := i
			for end < len(lines) && lines[end] != '\n' {
				end++
			}
			line := lines[i:end]
			// Trim whitespace
			line = trimSpace(line)
			if len(line) > 0 {
				globalManager.uuids = append(globalManager.uuids, line)
			}
			i = end + 1
		}
		if len(globalManager.uuids) == 0 {
			panic("No hardware UUIDs found in testdata/hardware_uuids.txt")
		}
	})
	return globalManager
}

// AllocateNextUUID returns the next available UUID
func (m *HardwareUUIDManager) AllocateNextUUID() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.allocatedIdx >= len(m.uuids) {
		return "", fmt.Errorf("no more hardware UUIDs available (max: %d)", len(m.uuids))
	}

	uuid := m.uuids[m.allocatedIdx]
	m.allocatedIdx++
	m.allocatedUUIDs[uuid] = true
	return uuid, nil
}

// ReleaseUUID releases a UUID back to the pool
func (m *HardwareUUIDManager) ReleaseUUID(uuid string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.allocatedUUIDs, uuid)
}

// GetAllocatedCount returns the number of currently allocated UUIDs
func (m *HardwareUUIDManager) GetAllocatedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.allocatedUUIDs)
}

// trimSpace removes leading and trailing whitespace
func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r' || s[start] == '\n') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r' || s[end-1] == '\n') {
		end--
	}
	return s[start:end]
}
