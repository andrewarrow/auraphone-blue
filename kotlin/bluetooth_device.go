
package kotlin

import (
	"fmt"
)

type BluetoothDevice struct {
	Name    string
	Address string
}

func (d *BluetoothDevice) ConnectGatt(context interface{}, autoConnect bool, callback BluetoothGattCallback) *BluetoothGatt {
	fmt.Println("Connecting to GATT...")
	return &BluetoothGatt{}
}
