
package kotlin

import (
	"fmt"

	"github.com/user/auraphone-blue/wire"
)

type BluetoothDevice struct {
	Name    string
	Address string
	wire    *wire.Wire
}

func (d *BluetoothDevice) SetWire(w *wire.Wire) {
	d.wire = w
}

func (d *BluetoothDevice) ConnectGatt(context interface{}, autoConnect bool, callback BluetoothGattCallback) *BluetoothGatt {
	fmt.Printf("Connecting to GATT for device %s...\n", d.Address)

	gatt := &BluetoothGatt{
		callback:   callback,
		wire:       d.wire,
		remoteUUID: d.Address,
	}

	// Simulate connection success
	callback.OnConnectionStateChange(gatt, 0, 2) // STATE_CONNECTED = 2

	return gatt
}
