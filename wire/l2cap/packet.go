package l2cap

import (
	"encoding/binary"
	"fmt"
)

// L2CAP Channel IDs
const (
	ChannelNULL      uint16 = 0x0000 // Reserved/Null
	ChannelSignaling uint16 = 0x0001 // ACL-U signaling
	ChannelConnless  uint16 = 0x0002 // Connectionless
	ChannelAMP       uint16 = 0x0003 // AMP Manager
	ChannelATT       uint16 = 0x0004 // Attribute Protocol
	ChannelLESignal  uint16 = 0x0005 // LE L2CAP Signaling
	ChannelSMP       uint16 = 0x0006 // Security Manager Protocol
	ChannelBR        uint16 = 0x0007 // BR/EDR Security Manager
)

// Default MTU sizes
const (
	DefaultMTU    = 23   // Default ATT MTU (23 bytes)
	MinMTU        = 23   // Minimum allowed MTU
	MaxMTU        = 517  // Maximum ATT MTU
	L2CAPHeaderLen = 4   // Length (2 bytes) + Channel ID (2 bytes)
)

// Packet represents an L2CAP packet
// Format: [Length: 2 bytes] [Channel ID: 2 bytes] [Payload: N bytes]
type Packet struct {
	Length    uint16 // Length of the payload (not including L2CAP header)
	ChannelID uint16 // L2CAP channel identifier
	Payload   []byte // The actual data (ATT/SMP/etc.)
}

// Encode serializes an L2CAP packet to binary format
func (p *Packet) Encode() []byte {
	buf := make([]byte, L2CAPHeaderLen+len(p.Payload))

	// Set length field (payload length only)
	binary.LittleEndian.PutUint16(buf[0:2], uint16(len(p.Payload)))

	// Set channel ID
	binary.LittleEndian.PutUint16(buf[2:4], p.ChannelID)

	// Copy payload
	copy(buf[4:], p.Payload)

	return buf
}

// Decode parses binary data into an L2CAP packet
func Decode(data []byte) (*Packet, error) {
	if len(data) < L2CAPHeaderLen {
		return nil, fmt.Errorf("l2cap: packet too short (need at least %d bytes, got %d)", L2CAPHeaderLen, len(data))
	}

	length := binary.LittleEndian.Uint16(data[0:2])
	channelID := binary.LittleEndian.Uint16(data[2:4])

	// Validate that we have enough data for the claimed payload length
	if len(data) < L2CAPHeaderLen+int(length) {
		return nil, fmt.Errorf("l2cap: incomplete packet (claimed length %d, got %d)", length, len(data)-L2CAPHeaderLen)
	}

	payload := make([]byte, length)
	copy(payload, data[4:4+length])

	return &Packet{
		Length:    length,
		ChannelID: channelID,
		Payload:   payload,
	}, nil
}

// NewATTPacket creates an L2CAP packet for the ATT channel
func NewATTPacket(payload []byte) *Packet {
	return &Packet{
		Length:    uint16(len(payload)),
		ChannelID: ChannelATT,
		Payload:   payload,
	}
}

// NewSMPPacket creates an L2CAP packet for the SMP channel
func NewSMPPacket(payload []byte) *Packet {
	return &Packet{
		Length:    uint16(len(payload)),
		ChannelID: ChannelSMP,
		Payload:   payload,
	}
}

// Fragment splits a large payload into multiple L2CAP packets if needed
// Each fragment must fit within the MTU (including L2CAP header)
func Fragment(payload []byte, channelID uint16, mtu int) ([]*Packet, error) {
	if mtu < MinMTU {
		return nil, fmt.Errorf("l2cap: MTU too small (%d < %d)", mtu, MinMTU)
	}

	// Calculate max payload per packet (MTU - L2CAP header)
	maxPayloadPerPacket := mtu - L2CAPHeaderLen

	// If payload fits in one packet, return single packet
	if len(payload) <= maxPayloadPerPacket {
		return []*Packet{{
			Length:    uint16(len(payload)),
			ChannelID: channelID,
			Payload:   payload,
		}}, nil
	}

	// Split into multiple packets
	var packets []*Packet
	for offset := 0; offset < len(payload); offset += maxPayloadPerPacket {
		end := offset + maxPayloadPerPacket
		if end > len(payload) {
			end = len(payload)
		}

		fragment := make([]byte, end-offset)
		copy(fragment, payload[offset:end])

		packets = append(packets, &Packet{
			Length:    uint16(len(fragment)),
			ChannelID: channelID,
			Payload:   fragment,
		})
	}

	return packets, nil
}

// Reassemble combines multiple L2CAP packet payloads into one
func Reassemble(packets []*Packet) ([]byte, error) {
	if len(packets) == 0 {
		return nil, fmt.Errorf("l2cap: no packets to reassemble")
	}

	// Verify all packets use the same channel
	channelID := packets[0].ChannelID
	totalLen := 0
	for i, pkt := range packets {
		if pkt.ChannelID != channelID {
			return nil, fmt.Errorf("l2cap: channel ID mismatch at packet %d (expected %d, got %d)", i, channelID, pkt.ChannelID)
		}
		totalLen += len(pkt.Payload)
	}

	// Concatenate all payloads
	result := make([]byte, 0, totalLen)
	for _, pkt := range packets {
		result = append(result, pkt.Payload...)
	}

	return result, nil
}
