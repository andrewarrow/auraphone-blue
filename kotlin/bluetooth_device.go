
package kotlin

import (
	"github.com/user/auraphone-blue/wire"
)

// Connection state constants (matches Android BluetoothProfile)
const (
	STATE_DISCONNECTED  = 0
	STATE_CONNECTING    = 1
	STATE_CONNECTED     = 2
	STATE_DISCONNECTING = 3
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
		if deviceUUID == gatt.remoteUUID {
			if callback != nil {
				callback.OnConnectionStateChange(gatt, 0, STATE_DISCONNECTED) // status = 0 (not an error)
			}

			// Android auto-reconnect: if autoConnect=true, retry in background
			if gatt.autoConnect {
				go gatt.attemptReconnect()
			}
		}
	})

	// Attempt realistic connection with timing and potential failure
	go func() {
		if callback != nil {
			callback.OnConnectionStateChange(gatt, 0, STATE_CONNECTING)
		}

		err := d.wire.Connect(d.Address)
		if err != nil {
			// Connection failed
			if callback != nil {
				callback.OnConnectionStateChange(gatt, 1, STATE_DISCONNECTED) // status=1 (GATT_FAILURE)
			}

			// Android auto-reconnect: if autoConnect=true, retry in background
			if autoConnect {
				go gatt.attemptReconnect()
			}
			return
		}

		// Connection succeeded
		if callback != nil {
			callback.OnConnectionStateChange(gatt, 0, STATE_CONNECTED)
		}
	}()

	return gatt
}

// NewBluetoothGattFromConnection creates a GATT client for an existing connection
// Used when we're acting as Peripheral and a Central connected to us
func NewBluetoothGattFromConnection(device *BluetoothDevice, callback BluetoothGattCallback, w *wire.Wire) *BluetoothGatt {
	gatt := &BluetoothGatt{
		callback:    callback,
		wire:        w,
		remoteUUID:  device.Address,
		autoConnect: false, // No auto-reconnect for reverse connections
	}

	// Connection already exists, so we're immediately in connected state
	// But we need to discover services to make this usable

	return gatt
}
