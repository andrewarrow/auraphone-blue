package wire

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// DeviceInfo represents a discovered device on the wire
type DeviceInfo struct {
	UUID string
	Name string
	Rssi int
}

// Wire manages the filesystem-based communication between fake devices
type Wire struct {
	localUUID string
	basePath  string
}

// NewWire creates a new wire instance for a device
func NewWire(deviceUUID string) *Wire {
	return &Wire{
		localUUID: deviceUUID,
		basePath:  ".",
	}
}

// SetBasePath sets the base directory for device communication
func (w *Wire) SetBasePath(path string) {
	w.basePath = path
}

// InitializeDevice creates the inbox and outbox directories for this device
func (w *Wire) InitializeDevice() error {
	devicePath := filepath.Join(w.basePath, w.localUUID)
	inboxPath := filepath.Join(devicePath, "inbox")
	outboxPath := filepath.Join(devicePath, "outbox")

	if err := os.MkdirAll(inboxPath, 0755); err != nil {
		return fmt.Errorf("failed to create inbox: %w", err)
	}
	if err := os.MkdirAll(outboxPath, 0755); err != nil {
		return fmt.Errorf("failed to create outbox: %w", err)
	}

	return nil
}

// DiscoverDevices scans for other devices (UUID directories) in the base path
func (w *Wire) DiscoverDevices() ([]string, error) {
	files, err := os.ReadDir(w.basePath)
	if err != nil {
		return nil, err
	}

	var devices []string
	for _, file := range files {
		if file.IsDir() {
			deviceName := file.Name()
			// Check if it's a valid UUID and not our own device
			if _, err := uuid.Parse(deviceName); err == nil {
				if deviceName != w.localUUID {
					devices = append(devices, deviceName)
				}
			}
		}
	}

	return devices, nil
}

// StartDiscovery continuously scans for devices and calls the callback when found
func (w *Wire) StartDiscovery(callback func(deviceUUID string)) chan struct{} {
	stopChan := make(chan struct{})

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				return
			case <-ticker.C:
				devices, err := w.DiscoverDevices()
				if err != nil {
					continue
				}

				for _, deviceUUID := range devices {
					callback(deviceUUID)
				}
			}
		}
	}()

	return stopChan
}

// WriteData writes binary data to this device's outbox for another device to read
func (w *Wire) WriteData(data []byte, filename string) error {
	outboxPath := filepath.Join(w.basePath, w.localUUID, "outbox")
	filePath := filepath.Join(outboxPath, filename)

	return os.WriteFile(filePath, data, 0644)
}

// ReadData reads binary data from this device's inbox
func (w *Wire) ReadData(filename string) ([]byte, error) {
	inboxPath := filepath.Join(w.basePath, w.localUUID, "inbox")
	filePath := filepath.Join(inboxPath, filename)

	return os.ReadFile(filePath)
}

// ListInbox lists all files in this device's inbox
func (w *Wire) ListInbox() ([]string, error) {
	inboxPath := filepath.Join(w.basePath, w.localUUID, "inbox")
	files, err := os.ReadDir(inboxPath)
	if err != nil {
		return nil, err
	}

	var filenames []string
	for _, file := range files {
		if !file.IsDir() {
			filenames = append(filenames, file.Name())
		}
	}

	return filenames, nil
}

// ListOutbox lists all files in this device's outbox
func (w *Wire) ListOutbox() ([]string, error) {
	outboxPath := filepath.Join(w.basePath, w.localUUID, "outbox")
	files, err := os.ReadDir(outboxPath)
	if err != nil {
		return nil, err
	}

	var filenames []string
	for _, file := range files {
		if !file.IsDir() {
			filenames = append(filenames, file.Name())
		}
	}

	return filenames, nil
}

// SendToDevice writes data to the target device's inbox
func (w *Wire) SendToDevice(targetUUID string, data []byte, filename string) error {
	targetInboxPath := filepath.Join(w.basePath, targetUUID, "inbox")
	filePath := filepath.Join(targetInboxPath, filename)

	return os.WriteFile(filePath, data, 0644)
}

// DeleteInboxFile removes a file from this device's inbox (after processing)
func (w *Wire) DeleteInboxFile(filename string) error {
	inboxPath := filepath.Join(w.basePath, w.localUUID, "inbox")
	filePath := filepath.Join(inboxPath, filename)

	return os.Remove(filePath)
}

// DeleteOutboxFile removes a file from this device's outbox
func (w *Wire) DeleteOutboxFile(filename string) error {
	outboxPath := filepath.Join(w.basePath, w.localUUID, "outbox")
	filePath := filepath.Join(outboxPath, filename)

	return os.Remove(filePath)
}
