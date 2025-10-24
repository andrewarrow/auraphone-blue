
package kotlin

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/user/auraphone-blue/wire"
)

type BluetoothGattCallback interface {
	OnConnectionStateChange(gatt *BluetoothGatt, status int, newState int)
	OnServicesDiscovered(gatt *BluetoothGatt, status int)
	OnCharacteristicWrite(gatt *BluetoothGatt, status int)
	OnCharacteristicRead(gatt *BluetoothGatt, data []byte, status int)
}

type BluetoothGatt struct {
	callback   BluetoothGattCallback
	wire       *wire.Wire
	remoteUUID string
	stopChan   chan struct{}
}

func (g *BluetoothGatt) DiscoverServices() {
	fmt.Println("Discovering services...")
}

func (g *BluetoothGatt) WriteCharacteristic(data []byte) error {
	if g.wire == nil {
		return fmt.Errorf("gatt not connected")
	}

	filename := fmt.Sprintf("msg_%s.bin", uuid.New().String())
	err := g.wire.SendToDevice(g.remoteUUID, data, filename)
	if err != nil {
		if g.callback != nil {
			g.callback.OnCharacteristicWrite(g, 1) // GATT_FAILURE = 1
		}
		return err
	}

	if g.callback != nil {
		g.callback.OnCharacteristicWrite(g, 0) // GATT_SUCCESS = 0
	}
	return nil
}

func (g *BluetoothGatt) StartListening() {
	if g.wire == nil {
		return
	}

	g.stopChan = make(chan struct{})

	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-g.stopChan:
				return
			case <-ticker.C:
				files, err := g.wire.ListInbox()
				if err != nil {
					continue
				}

				for _, filename := range files {
					data, err := g.wire.ReadData(filename)
					if err != nil {
						continue
					}

					if g.callback != nil {
						g.callback.OnCharacteristicRead(g, data, 0) // GATT_SUCCESS = 0
					}

					// Delete after reading
					g.wire.DeleteInboxFile(filename)
				}
			}
		}
	}()
}

func (g *BluetoothGatt) StopListening() {
	if g.stopChan != nil {
		close(g.stopChan)
		g.stopChan = nil
	}
}
