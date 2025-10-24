package swift

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/user/auraphone-blue/wire"
)

type CBPeripheralDelegate interface {
	DidDiscoverServices(peripheral CBPeripheral, err error)
	DidWriteValueForCharacteristic(peripheral CBPeripheral, err error)
	DidUpdateValueForCharacteristic(peripheral CBPeripheral, data []byte, err error)
}

type CBPeripheral struct {
	Delegate   CBPeripheralDelegate
	Name       string
	UUID       string
	wire       *wire.Wire
	remoteUUID string
	stopChan   chan struct{}
}

func (p *CBPeripheral) DiscoverServices(serviceUUIDs []string) {
	fmt.Println("Discovering services...")
}

func (p *CBPeripheral) WriteValue(data []byte) error {
	if p.wire == nil {
		return fmt.Errorf("peripheral not connected")
	}

	filename := fmt.Sprintf("msg_%s.bin", uuid.New().String())
	err := p.wire.SendToDevice(p.remoteUUID, data, filename)
	if err != nil {
		if p.Delegate != nil {
			p.Delegate.DidWriteValueForCharacteristic(*p, err)
		}
		return err
	}

	if p.Delegate != nil {
		p.Delegate.DidWriteValueForCharacteristic(*p, nil)
	}
	return nil
}

func (p *CBPeripheral) StartListening() {
	if p.wire == nil {
		return
	}

	p.stopChan = make(chan struct{})

	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-p.stopChan:
				return
			case <-ticker.C:
				files, err := p.wire.ListInbox()
				if err != nil {
					continue
				}

				for _, filename := range files {
					data, err := p.wire.ReadData(filename)
					if err != nil {
						continue
					}

					if p.Delegate != nil {
						p.Delegate.DidUpdateValueForCharacteristic(*p, data, nil)
					}

					// Delete after reading
					p.wire.DeleteInboxFile(filename)
				}
			}
		}
	}()
}

func (p *CBPeripheral) StopListening() {
	if p.stopChan != nil {
		close(p.stopChan)
		p.stopChan = nil
	}
}
