package kotlin

// testGattCallback is a test implementation of BluetoothGattCallback
// Shared across all test files
type testGattCallback struct {
	onConnectionStateChange func(gatt *BluetoothGatt, status int, newState int)
	onServicesDiscovered    func(gatt *BluetoothGatt, status int)
	onCharacteristicRead    func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int)
	onCharacteristicWrite   func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int)
	onCharacteristicChanged func(gatt *BluetoothGatt, char *BluetoothGattCharacteristic)
}

func (c *testGattCallback) OnConnectionStateChange(gatt *BluetoothGatt, status int, newState int) {
	if c.onConnectionStateChange != nil {
		c.onConnectionStateChange(gatt, status, newState)
	}
}

func (c *testGattCallback) OnServicesDiscovered(gatt *BluetoothGatt, status int) {
	if c.onServicesDiscovered != nil {
		c.onServicesDiscovered(gatt, status)
	}
}

func (c *testGattCallback) OnCharacteristicRead(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int) {
	if c.onCharacteristicRead != nil {
		c.onCharacteristicRead(gatt, char, status)
	}
}

func (c *testGattCallback) OnCharacteristicWrite(gatt *BluetoothGatt, char *BluetoothGattCharacteristic, status int) {
	if c.onCharacteristicWrite != nil {
		c.onCharacteristicWrite(gatt, char, status)
	}
}

func (c *testGattCallback) OnCharacteristicChanged(gatt *BluetoothGatt, char *BluetoothGattCharacteristic) {
	if c.onCharacteristicChanged != nil {
		c.onCharacteristicChanged(gatt, char)
	}
}
