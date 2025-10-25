
package kotlin

import (
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
	gatt := &BluetoothGatt{
		callback:    callback,
		wire:        d.wire,
		remoteUUID:  d.Address,
		autoConnect: autoConnect, // Store autoConnect flag
	}

	// Set up disconnect callback for this specific connection
	d.wire.SetDisconnectCallback(func(deviceUUID string) {
		// Connection was randomly dropped
		if deviceUUID == gatt.remoteUUID && callback != nil {
			// STATE_DISCONNECTED = 0, status = 0 (not an error)
			callback.OnConnectionStateChange(gatt, 0, 0)

			// Android auto-reconnect: if autoConnect=true, retry in background
			if gatt.autoConnect {
				go gatt.attemptReconnect()
			}
		}
	})

	// Attempt realistic connection with timing and potential failure
	go func() {
		// STATE_CONNECTING = 1
		callback.OnConnectionStateChange(gatt, 0, 1)

		err := d.wire.Connect(d.Address)
		if err != nil {
			// Connection failed - STATE_DISCONNECTED = 0
			callback.OnConnectionStateChange(gatt, 1, 0) // status=1 (GATT_FAILURE)

			// Android auto-reconnect: if autoConnect=true, retry in background
			if autoConnect {
				go gatt.attemptReconnect()
			}
			return
		}

		// Connection succeeded - STATE_CONNECTED = 2
		callback.OnConnectionStateChange(gatt, 0, 2)
	}()

	return gatt
}
