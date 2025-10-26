package android

import (
	"fmt"
	"sync"

	"github.com/user/auraphone-blue/phone"
)

// Implement phone.DeviceCommon interface for Android
// This allows Android to work with shared handlers in phone/ package

func (a *Android) GetHardwareUUID() string {
	return a.hardwareUUID
}

func (a *Android) GetDeviceID() string {
	return a.deviceID
}

func (a *Android) GetDeviceName() string {
	return a.deviceName
}

func (a *Android) GetPlatform() string {
	return "Android"
}

func (a *Android) GetPhotoHash() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.photoHash
}

func (a *Android) GetPhotoData() []byte {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.photoData
}

func (a *Android) GetLocalProfile() *phone.LocalProfile {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.localProfile
}

func (a *Android) GetConnManager() *phone.ConnectionManager {
	return a.connManager
}

func (a *Android) GetMeshView() *phone.MeshView {
	return a.meshView
}

func (a *Android) GetCacheManager() *phone.DeviceCacheManager {
	return a.cacheManager
}

func (a *Android) GetPhotoCoordinator() *phone.PhotoTransferCoordinator {
	return a.photoCoordinator
}

func (a *Android) GetUUIDToDeviceIDMap() map[string]string {
	return a.remoteUUIDToDeviceID
}

func (a *Android) GetMutex() *sync.RWMutex {
	return &a.mu
}

// DisconnectFromDevice implements platform-specific disconnect using Android BluetoothGatt
func (a *Android) DisconnectFromDevice(hardwareUUID string) error {
	a.mu.RLock()
	gatt, exists := a.connectedGatts[hardwareUUID]
	a.mu.RUnlock()

	if !exists {
		return fmt.Errorf("gatt connection %s not found", hardwareUUID[:8])
	}

	gatt.Disconnect()
	return nil
}
