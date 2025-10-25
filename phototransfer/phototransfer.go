package phototransfer

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

const (
	MetadataSize   = 14
	ChunkHeaderSize = 8
	MagicByte0     = 0xDE
	MagicByte1     = 0xAD
	MagicByte2     = 0xBE
	MagicByte3     = 0xEF
	DefaultChunkSize = 502 // Matches iOS implementation

	// Acknowledgment magic bytes
	AckMagicByte0 = 0xAC
	AckMagicByte1 = 0xCE
	AckMagicByte2 = 0x55
	AckMagicByte3 = 0xED

	// Retransmit request magic bytes
	RetransMagicByte0 = 0x7E
	RetransMagicByte1 = 0x7F
	RetransMagicByte2 = 0x12
	RetransMagicByte3 = 0x34
)

// MetadataPacket represents the initial photo transfer metadata
type MetadataPacket struct {
	TotalSize   uint32
	TotalCRC    uint32
	TotalChunks uint16
}

// ChunkPacket represents a single photo data chunk
type ChunkPacket struct {
	Index uint16
	Size  uint16
	CRC   uint32
	Data  []byte
}

// CalculateCRC32 computes CRC32 checksum of data
func CalculateCRC32(data []byte) uint32 {
	return crc32.ChecksumIEEE(data)
}

// EncodeMetadata creates a metadata packet with optional first chunk data
func EncodeMetadata(totalSize uint32, totalCRC uint32, totalChunks uint16, firstChunkData []byte) []byte {
	packet := make([]byte, MetadataSize+len(firstChunkData))

	// Magic bytes
	packet[0] = MagicByte0
	packet[1] = MagicByte1
	packet[2] = MagicByte2
	packet[3] = MagicByte3

	// Total size (little-endian)
	binary.LittleEndian.PutUint32(packet[4:8], totalSize)

	// Total CRC (little-endian)
	binary.LittleEndian.PutUint32(packet[8:12], totalCRC)

	// Total chunks (little-endian)
	binary.LittleEndian.PutUint16(packet[12:14], totalChunks)

	// Optional first chunk data
	if len(firstChunkData) > 0 {
		copy(packet[14:], firstChunkData)
	}

	return packet
}

// DecodeMetadata parses a metadata packet
func DecodeMetadata(data []byte) (*MetadataPacket, []byte, error) {
	if len(data) < MetadataSize {
		return nil, nil, fmt.Errorf("data too short for metadata: %d bytes", len(data))
	}

	// Check magic bytes
	if data[0] != MagicByte0 || data[1] != MagicByte1 ||
	   data[2] != MagicByte2 || data[3] != MagicByte3 {
		return nil, nil, fmt.Errorf("invalid magic bytes")
	}

	meta := &MetadataPacket{
		TotalSize:   binary.LittleEndian.Uint32(data[4:8]),
		TotalCRC:    binary.LittleEndian.Uint32(data[8:12]),
		TotalChunks: binary.LittleEndian.Uint16(data[12:14]),
	}

	// Return remaining data as part of first chunk
	remainingData := []byte{}
	if len(data) > MetadataSize {
		remainingData = data[MetadataSize:]
	}

	return meta, remainingData, nil
}

// EncodeChunk creates a chunk packet
func EncodeChunk(index uint16, data []byte) []byte {
	chunkSize := uint16(len(data))
	chunkCRC := CalculateCRC32(data)

	packet := make([]byte, ChunkHeaderSize+len(data))

	// Chunk index (little-endian)
	binary.LittleEndian.PutUint16(packet[0:2], index)

	// Chunk size (little-endian)
	binary.LittleEndian.PutUint16(packet[2:4], chunkSize)

	// Chunk CRC (little-endian)
	binary.LittleEndian.PutUint32(packet[4:8], chunkCRC)

	// Chunk data
	copy(packet[8:], data)

	return packet
}

// DecodeChunk parses a chunk packet
func DecodeChunk(data []byte) (*ChunkPacket, int, error) {
	if len(data) < ChunkHeaderSize {
		return nil, 0, fmt.Errorf("data too short for chunk header: %d bytes", len(data))
	}

	chunk := &ChunkPacket{
		Index: binary.LittleEndian.Uint16(data[0:2]),
		Size:  binary.LittleEndian.Uint16(data[2:4]),
		CRC:   binary.LittleEndian.Uint32(data[4:8]),
	}

	totalChunkSize := ChunkHeaderSize + int(chunk.Size)

	if len(data) < totalChunkSize {
		return nil, 0, fmt.Errorf("data too short for chunk: have %d, need %d", len(data), totalChunkSize)
	}

	chunk.Data = data[8:totalChunkSize]

	// Verify CRC
	calculatedCRC := CalculateCRC32(chunk.Data)
	if calculatedCRC != chunk.CRC {
		return nil, 0, fmt.Errorf("chunk CRC mismatch: expected %08X, got %08X", chunk.CRC, calculatedCRC)
	}

	return chunk, totalChunkSize, nil
}

// SplitIntoChunks splits photo data into chunks
func SplitIntoChunks(data []byte, chunkSize int) [][]byte {
	var chunks [][]byte

	for offset := 0; offset < len(data); offset += chunkSize {
		end := offset + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunks = append(chunks, data[offset:end])
	}

	return chunks
}

// EncodeAck creates a transfer completion acknowledgment
// Format: [AC CE 55 ED] [CRC32:4bytes]
func EncodeAck(totalCRC uint32) []byte {
	packet := make([]byte, 8)
	packet[0] = AckMagicByte0
	packet[1] = AckMagicByte1
	packet[2] = AckMagicByte2
	packet[3] = AckMagicByte3
	binary.LittleEndian.PutUint32(packet[4:8], totalCRC)
	return packet
}

// DecodeAck parses an acknowledgment packet
func DecodeAck(data []byte) (uint32, error) {
	if len(data) < 8 {
		return 0, fmt.Errorf("data too short for ack: %d bytes", len(data))
	}
	if data[0] != AckMagicByte0 || data[1] != AckMagicByte1 ||
		data[2] != AckMagicByte2 || data[3] != AckMagicByte3 {
		return 0, fmt.Errorf("invalid ack magic bytes")
	}
	crc := binary.LittleEndian.Uint32(data[4:8])
	return crc, nil
}

// EncodeRetransmitRequest creates a request for missing chunks
// Format: [7E 7F 12 34] [ChunkCount:2bytes] [ChunkIndex:2bytes] [ChunkIndex:2bytes] ...
func EncodeRetransmitRequest(missingChunks []uint16) []byte {
	packet := make([]byte, 6+len(missingChunks)*2)
	packet[0] = RetransMagicByte0
	packet[1] = RetransMagicByte1
	packet[2] = RetransMagicByte2
	packet[3] = RetransMagicByte3
	binary.LittleEndian.PutUint16(packet[4:6], uint16(len(missingChunks)))

	for i, chunkIdx := range missingChunks {
		offset := 6 + i*2
		binary.LittleEndian.PutUint16(packet[offset:offset+2], chunkIdx)
	}
	return packet
}

// DecodeRetransmitRequest parses a retransmit request packet
func DecodeRetransmitRequest(data []byte) ([]uint16, error) {
	if len(data) < 6 {
		return nil, fmt.Errorf("data too short for retransmit request: %d bytes", len(data))
	}
	if data[0] != RetransMagicByte0 || data[1] != RetransMagicByte1 ||
		data[2] != RetransMagicByte2 || data[3] != RetransMagicByte3 {
		return nil, fmt.Errorf("invalid retransmit request magic bytes")
	}

	chunkCount := binary.LittleEndian.Uint16(data[4:6])
	expectedLen := 6 + int(chunkCount)*2
	if len(data) < expectedLen {
		return nil, fmt.Errorf("data too short for chunk indices: have %d, need %d", len(data), expectedLen)
	}

	missingChunks := make([]uint16, chunkCount)
	for i := 0; i < int(chunkCount); i++ {
		offset := 6 + i*2
		missingChunks[i] = binary.LittleEndian.Uint16(data[offset : offset+2])
	}

	return missingChunks, nil
}
