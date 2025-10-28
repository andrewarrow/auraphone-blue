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

// Connection Management
// Limit connections to maintain a sparse mesh network for scalability
const (
	// MaxConnections is the maximum number of simultaneous BLE connections per device
	// Real iOS: 10-20 concurrent connections, Android: 4-7 connections
	// We use 5 as a conservative limit for mesh network efficiency
	MaxConnections = 5

	// GossipInterval is how often we send gossip messages (5 seconds)
	GossipIntervalSeconds = 5
)
