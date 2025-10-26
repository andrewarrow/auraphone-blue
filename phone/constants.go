package phone

// Aura BLE Service and Characteristic UUIDs
// These are the stable identifiers for our BLE GATT table
const (
	// Service UUID - the main Aura service
	AuraServiceUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5F"

	// Characteristic UUIDs
	AuraProtocolCharUUID = "E621E1F8-C36C-495A-93FC-0C247A3E6E5D" // Binary protobuf messages (handshake, gossip)
	AuraPhotoCharUUID    = "E621E1F8-C36C-495A-93FC-0C247A3E6E5E" // Photo transfer (chunked binary data)
	AuraProfileCharUUID  = "E621E1F8-C36C-495A-93FC-0C247A3E6E5C" // Profile messages (protobuf)
)
