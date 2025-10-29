package swift

import "testing"

// Test delegate implementations

type testCentralDelegate struct {
	t               *testing.T
	didConnect      chan *CBPeripheral
	didDisconnect   chan *CBPeripheral
	didDiscoverSvcs chan *CBPeripheral
	didWriteValue   chan *CBCharacteristic
	didUpdateValue  chan []byte
}

func (d *testCentralDelegate) DidUpdateCentralState(central CBCentralManager) {}

func (d *testCentralDelegate) DidDiscoverPeripheral(central CBCentralManager, peripheral CBPeripheral, advertisementData map[string]interface{}, rssi float64) {
}

func (d *testCentralDelegate) DidConnectPeripheral(central CBCentralManager, peripheral CBPeripheral) {
	if d.didConnect != nil {
		d.didConnect <- &peripheral
	}
}

func (d *testCentralDelegate) DidFailToConnectPeripheral(central CBCentralManager, peripheral CBPeripheral, err error) {
	d.t.Errorf("Failed to connect: %v", err)
}

func (d *testCentralDelegate) DidDisconnectPeripheral(central CBCentralManager, peripheral CBPeripheral, err error) {
	if d.didDisconnect != nil {
		d.didDisconnect <- &peripheral
	}
}

type testPeripheralDelegate struct {
	t               *testing.T
	didConnect      chan *CBPeripheral
	didDiscoverSvcs chan []*CBService
	didWriteValue   chan *CBCharacteristic
	didUpdateValue  chan []byte
}

func (d *testPeripheralDelegate) DidDiscoverServices(peripheral *CBPeripheral, services []*CBService, err error) {
	if err != nil {
		d.t.Errorf("Service discovery error: %v", err)
		return
	}
	if d.didDiscoverSvcs != nil {
		d.didDiscoverSvcs <- services
	}
}

func (d *testPeripheralDelegate) DidDiscoverCharacteristics(peripheral *CBPeripheral, service *CBService, err error) {
}

func (d *testPeripheralDelegate) DidDiscoverDescriptorsForCharacteristic(peripheral *CBPeripheral, characteristic *CBCharacteristic, err error) {
	// Stub for new descriptor discovery method
}

func (d *testPeripheralDelegate) DidWriteValueForCharacteristic(peripheral *CBPeripheral, characteristic *CBCharacteristic, err error) {
	if err != nil {
		d.t.Errorf("Write error: %v", err)
		return
	}
	if d.didWriteValue != nil {
		d.didWriteValue <- characteristic
	}
}

func (d *testPeripheralDelegate) DidWriteValueForDescriptor(peripheral *CBPeripheral, descriptor *CBDescriptor, err error) {
	// Stub for new descriptor write method
}

func (d *testPeripheralDelegate) DidUpdateValueForCharacteristic(peripheral *CBPeripheral, characteristic *CBCharacteristic, err error) {
	if err != nil {
		d.t.Errorf("Update value error: %v", err)
		return
	}
	if d.didUpdateValue != nil && characteristic.Value != nil {
		// Make a copy of the data
		dataCopy := make([]byte, len(characteristic.Value))
		copy(dataCopy, characteristic.Value)
		d.didUpdateValue <- dataCopy
	}
}

func (d *testPeripheralDelegate) DidUpdateValueForDescriptor(peripheral *CBPeripheral, descriptor *CBDescriptor, err error) {
	// Stub for new descriptor update method
}

type testPeripheralManagerDelegate struct {
	t                 *testing.T
	didReceiveWrite   chan []byte
	didReceiveRead    chan bool
	didSubscribe      chan string
	didUnsubscribe    chan string
	didStartAdvertise chan bool
}

func (d *testPeripheralManagerDelegate) DidUpdatePeripheralState(peripheralManager *CBPeripheralManager) {
}

func (d *testPeripheralManagerDelegate) DidStartAdvertising(peripheralManager *CBPeripheralManager, err error) {
	if err != nil {
		d.t.Errorf("Advertising error: %v", err)
		return
	}
	if d.didStartAdvertise != nil {
		d.didStartAdvertise <- true
	}
}

func (d *testPeripheralManagerDelegate) DidReceiveReadRequest(peripheralManager *CBPeripheralManager, request *CBATTRequest) {
	if d.didReceiveRead != nil {
		d.didReceiveRead <- true
	}
}

func (d *testPeripheralManagerDelegate) DidReceiveWriteRequests(peripheralManager *CBPeripheralManager, requests []*CBATTRequest) {
	if d.didReceiveWrite != nil && len(requests) > 0 {
		// Make a copy of the data
		dataCopy := make([]byte, len(requests[0].Value))
		copy(dataCopy, requests[0].Value)
		d.didReceiveWrite <- dataCopy
	}
}

func (d *testPeripheralManagerDelegate) CentralDidSubscribe(peripheralManager *CBPeripheralManager, central CBCentral, characteristic *CBMutableCharacteristic) {
	if d.didSubscribe != nil {
		d.didSubscribe <- characteristic.UUID
	}
}

func (d *testPeripheralManagerDelegate) CentralDidUnsubscribe(peripheralManager *CBPeripheralManager, central CBCentral, characteristic *CBMutableCharacteristic) {
	if d.didUnsubscribe != nil {
		d.didUnsubscribe <- characteristic.UUID
	}
}
