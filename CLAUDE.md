  main.go to use structs from kotlin and swift packages. 
 swift/cb_central_manager.go and kotlin/bluetooth_manager.go and these structs should
 implement a fake bluetooth system here in go but work just like iOS CoreBluetooth  
 CBCentralManager and android's BluetoothManager work. This is a big task so just do the    
 first part. Make the minimum needed structs and "CBPeripheral" or "BluetoothDevice" or     
 "BluetoothGatt" and CBPeripheral+delegates etc go structs so that main.go can make a fake  
 ios device and fake android device and make one see the other in a list of nearby devices. 
  To simulate sending data down the wire, use the filesystem. Use random uuid for each fake 
  device and make a dir for each. Inside that dir should be inbox and output dir where      
 files are writen with binary data the other side will pickup.      

always the the real names from swift or kotlin bluetooth libraries when making names. The 
goal is to have the swift package to completely capture how ios bluetooth really works.
and the kotlin package to completely capture how android bluetooth really works.
