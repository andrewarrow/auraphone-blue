
package kotlin

import (
	"fmt"
)

type BluetoothGattCallback interface {
	OnConnectionStateChange(gatt *BluetoothGatt, status int, newState int)
	OnServicesDiscovered(gatt *BluetoothGatt, status int)
}

type BluetoothGatt struct {
	callback BluetoothGattCallback
}

func (g *BluetoothGatt) DiscoverServices() {
	fmt.Println("Discovering services...")
}
