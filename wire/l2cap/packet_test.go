package l2cap

import (
	"bytes"
	"testing"

	"github.com/user/auraphone-blue/util"
)

func TestPacketEncodeDecoded(t *testing.T) {
	util.SetRandom()

	tests := []struct {
		name      string
		packet    *Packet
		wantBytes []byte
	}{
		{
			name: "empty payload",
			packet: &Packet{
				Length:    0,
				ChannelID: ChannelATT,
				Payload:   []byte{},
			},
			wantBytes: []byte{0x00, 0x00, 0x04, 0x00},
		},
		{
			name: "small ATT payload",
			packet: &Packet{
				Length:    3,
				ChannelID: ChannelATT,
				Payload:   []byte{0x01, 0x02, 0x03},
			},
			wantBytes: []byte{0x03, 0x00, 0x04, 0x00, 0x01, 0x02, 0x03},
		},
		{
			name: "SMP channel",
			packet: &Packet{
				Length:    2,
				ChannelID: ChannelSMP,
				Payload:   []byte{0xAA, 0xBB},
			},
			wantBytes: []byte{0x02, 0x00, 0x06, 0x00, 0xAA, 0xBB},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test encoding
			encoded := tt.packet.Encode()
			if !bytes.Equal(encoded, tt.wantBytes) {
				t.Errorf("Encode() = %v, want %v", encoded, tt.wantBytes)
			}

			// Test decoding
			decoded, err := Decode(encoded)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}

			if decoded.Length != tt.packet.Length {
				t.Errorf("Length = %d, want %d", decoded.Length, tt.packet.Length)
			}
			if decoded.ChannelID != tt.packet.ChannelID {
				t.Errorf("ChannelID = %d, want %d", decoded.ChannelID, tt.packet.ChannelID)
			}
			if !bytes.Equal(decoded.Payload, tt.packet.Payload) {
				t.Errorf("Payload = %v, want %v", decoded.Payload, tt.packet.Payload)
			}
		})
	}
}

func TestDecodeErrors(t *testing.T) {
	util.SetRandom()

	tests := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{
			name:    "too short",
			data:    []byte{0x01, 0x02},
			wantErr: true,
		},
		{
			name:    "incomplete payload",
			data:    []byte{0x0A, 0x00, 0x04, 0x00, 0x01}, // claims 10 bytes, only 1 present
			wantErr: true,
		},
		{
			name:    "valid minimum packet",
			data:    []byte{0x00, 0x00, 0x04, 0x00},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("Decode() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestFragmentation(t *testing.T) {
	util.SetRandom()

	tests := []struct {
		name        string
		payload     []byte
		mtu         int
		wantPackets int
		wantErr     bool
	}{
		{
			name:        "fits in one packet",
			payload:     make([]byte, 10),
			mtu:         23,
			wantPackets: 1,
			wantErr:     false,
		},
		{
			name:        "requires two packets",
			payload:     make([]byte, 30),
			mtu:         23,
			wantPackets: 2,
			wantErr:     false,
		},
		{
			name:        "requires three packets",
			payload:     make([]byte, 50),
			mtu:         23,
			wantPackets: 3,
			wantErr:     false,
		},
		{
			name:        "MTU too small",
			payload:     make([]byte, 10),
			mtu:         10,
			wantPackets: 0,
			wantErr:     true,
		},
		{
			name:        "empty payload",
			payload:     []byte{},
			mtu:         23,
			wantPackets: 1,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			packets, err := Fragment(tt.payload, ChannelATT, tt.mtu)

			if (err != nil) != tt.wantErr {
				t.Errorf("Fragment() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			if len(packets) != tt.wantPackets {
				t.Errorf("Fragment() produced %d packets, want %d", len(packets), tt.wantPackets)
			}

			// Verify all packets are within MTU
			for i, pkt := range packets {
				encoded := pkt.Encode()
				if len(encoded) > tt.mtu {
					t.Errorf("Packet %d exceeds MTU: %d > %d", i, len(encoded), tt.mtu)
				}
			}

			// Verify reassembly produces original payload
			reassembled, err := Reassemble(packets)
			if err != nil {
				t.Fatalf("Reassemble() error = %v", err)
			}

			if !bytes.Equal(reassembled, tt.payload) {
				t.Errorf("Reassembled payload doesn't match original")
			}
		})
	}
}

func TestReassembleErrors(t *testing.T) {
	util.SetRandom()

	tests := []struct {
		name    string
		packets []*Packet
		wantErr bool
	}{
		{
			name:    "empty packet list",
			packets: []*Packet{},
			wantErr: true,
		},
		{
			name: "channel ID mismatch",
			packets: []*Packet{
				{ChannelID: ChannelATT, Payload: []byte{0x01}},
				{ChannelID: ChannelSMP, Payload: []byte{0x02}},
			},
			wantErr: true,
		},
		{
			name: "valid single packet",
			packets: []*Packet{
				{ChannelID: ChannelATT, Payload: []byte{0x01, 0x02}},
			},
			wantErr: false,
		},
		{
			name: "valid multiple packets",
			packets: []*Packet{
				{ChannelID: ChannelATT, Payload: []byte{0x01}},
				{ChannelID: ChannelATT, Payload: []byte{0x02}},
				{ChannelID: ChannelATT, Payload: []byte{0x03}},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Reassemble(tt.packets)
			if (err != nil) != tt.wantErr {
				t.Errorf("Reassemble() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewATTPacket(t *testing.T) {
	util.SetRandom()

	payload := []byte{0x01, 0x02, 0x03}
	pkt := NewATTPacket(payload)

	if pkt.ChannelID != ChannelATT {
		t.Errorf("ChannelID = %d, want %d", pkt.ChannelID, ChannelATT)
	}

	if pkt.Length != uint16(len(payload)) {
		t.Errorf("Length = %d, want %d", pkt.Length, len(payload))
	}

	if !bytes.Equal(pkt.Payload, payload) {
		t.Errorf("Payload = %v, want %v", pkt.Payload, payload)
	}
}

func TestNewSMPPacket(t *testing.T) {
	util.SetRandom()

	payload := []byte{0xAA, 0xBB}
	pkt := NewSMPPacket(payload)

	if pkt.ChannelID != ChannelSMP {
		t.Errorf("ChannelID = %d, want %d", pkt.ChannelID, ChannelSMP)
	}

	if pkt.Length != uint16(len(payload)) {
		t.Errorf("Length = %d, want %d", pkt.Length, len(payload))
	}

	if !bytes.Equal(pkt.Payload, payload) {
		t.Errorf("Payload = %v, want %v", pkt.Payload, payload)
	}
}
