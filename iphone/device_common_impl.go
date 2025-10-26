package iphone

import (
	"fmt"
	"sync"

	"github.com/user/auraphone-blue/phone"
)

// Implement phone.DeviceCommon interface for iPhone
// This allows iPhone to work with shared handlers in phone/ package

func (ip *iPhone) GetHardwareUUID() string {
	return ip.hardwareUUID
}

func (ip *iPhone) GetDeviceID() string {
	return ip.deviceID
}

func (ip *iPhone) GetDeviceName() string {
	return ip.deviceName
}

func (ip *iPhone) GetPlatform() string {
	return "iOS"
}

func (ip *iPhone) GetPhotoHash() string {
	ip.mu.RLock()
	defer ip.mu.RUnlock()
	return ip.photoHash
}

func (ip *iPhone) GetPhotoData() []byte {
	ip.mu.RLock()
	defer ip.mu.RUnlock()
	return ip.photoData
}

func (ip *iPhone) GetLocalProfile() *phone.LocalProfile {
	ip.mu.RLock()
	defer ip.mu.RUnlock()
	return ip.localProfile
}

func (ip *iPhone) GetConnManager() *phone.ConnectionManager {
	return ip.connManager
}

func (ip *iPhone) GetMeshView() *phone.MeshView {
	return ip.meshView
}

func (ip *iPhone) GetCacheManager() *phone.DeviceCacheManager {
	return ip.cacheManager
}

func (ip *iPhone) GetPhotoCoordinator() *phone.PhotoTransferCoordinator {
	return ip.photoCoordinator
}

func (ip *iPhone) GetUUIDToDeviceIDMap() map[string]string {
	return ip.peripheralToDeviceID
}

func (ip *iPhone) GetMutex() *sync.RWMutex {
	return &ip.mu
}

// DisconnectFromDevice implements platform-specific disconnect using iOS CoreBluetooth
func (ip *iPhone) DisconnectFromDevice(hardwareUUID string) error {
	ip.mu.RLock()
	peripheral, exists := ip.connectedPeripherals[hardwareUUID]
	ip.mu.RUnlock()

	if !exists {
		return fmt.Errorf("peripheral %s not found", hardwareUUID[:8])
	}

	ip.manager.CancelPeripheralConnection(peripheral)
	return nil
}
