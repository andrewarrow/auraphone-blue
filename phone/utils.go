package phone

import (
	"encoding/hex"
)

// TruncateHash safely truncates a hash string to the first n characters
// Returns the full string if it's shorter than n
func TruncateHash(hash string, n int) string {
	if len(hash) <= n {
		return hash
	}
	return hash[:n]
}

// HashStringToBytes converts a hex hash string to raw bytes for protobuf
func HashStringToBytes(hashStr string) []byte {
	if hashStr == "" {
		return nil
	}
	bytes, err := hex.DecodeString(hashStr)
	if err != nil {
		return nil
	}
	return bytes
}

// HashBytesToString converts raw bytes from protobuf to hex hash string
func HashBytesToString(hashBytes []byte) string {
	if len(hashBytes) == 0 {
		return ""
	}
	return hex.EncodeToString(hashBytes)
}
