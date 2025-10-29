package debug

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/user/auraphone-blue/util"
	"github.com/user/auraphone-blue/wire/att"
	"github.com/user/auraphone-blue/wire/l2cap"
)

// DebugLogger writes human-readable JSON logs of binary BLE packets
// These files are WRITE-ONLY and never read by production code
type DebugLogger struct {
	deviceUUID string
	debugDir   string
	enabled    bool
	mu         sync.Mutex
}

// L2CAPPacketLog represents a logged L2CAP packet
type L2CAPPacketLog struct {
	Timestamp   string `json:"timestamp"`
	Direction   string `json:"direction"` // "tx" or "rx"
	PeerUUID    string `json:"peer_uuid"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	PayloadLen  int    `json:"payload_len"`
	PayloadHex  string `json:"payload_hex"`
}

// ATTPacketLog represents a logged ATT packet
type ATTPacketLog struct {
	Timestamp   string                 `json:"timestamp"`
	Direction   string                 `json:"direction"` // "tx" or "rx"
	PeerUUID    string                 `json:"peer_uuid"`
	Opcode      string                 `json:"opcode"`
	OpcodeName  string                 `json:"opcode_name"`
	Data        map[string]interface{} `json:"data,omitempty"`
	RawHex      string                 `json:"raw_hex"`
}

// GATTOperationLog represents a high-level GATT operation
type GATTOperationLog struct {
	Timestamp          string `json:"timestamp"`
	Direction          string `json:"direction"` // "tx" or "rx"
	PeerUUID           string `json:"peer_uuid"`
	Operation          string `json:"operation"` // "read", "write", "notify", etc.
	ServiceUUID        string `json:"service_uuid,omitempty"`
	CharacteristicUUID string `json:"characteristic_uuid,omitempty"`
	Handle             string `json:"handle,omitempty"`
	DataLen            int    `json:"data_len,omitempty"`
	DataHex            string `json:"data_hex,omitempty"`
}

// NewDebugLogger creates a new debug logger for a device
func NewDebugLogger(deviceUUID string, enabled bool) *DebugLogger {
	if !enabled {
		return &DebugLogger{enabled: false}
	}

	debugDir := filepath.Join(util.GetDeviceCacheDir(deviceUUID), "debug")
	os.MkdirAll(debugDir, 0755)

	return &DebugLogger{
		deviceUUID: deviceUUID,
		debugDir:   debugDir,
		enabled:    enabled,
	}
}

// LogL2CAPPacket logs an L2CAP packet to debug/l2cap_packets.jsonl
func (d *DebugLogger) LogL2CAPPacket(direction, peerUUID string, packet *l2cap.Packet) {
	if !d.enabled {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	channelName := getChannelName(packet.ChannelID)

	log := L2CAPPacketLog{
		Timestamp:   time.Now().Format(time.RFC3339Nano),
		Direction:   direction,
		PeerUUID:    peerUUID,
		ChannelID:   fmt.Sprintf("0x%04X", packet.ChannelID),
		ChannelName: channelName,
		PayloadLen:  len(packet.Payload),
		PayloadHex:  hex.EncodeToString(packet.Payload),
	}

	d.appendJSONL("l2cap_packets.jsonl", log)
}

// LogATTPacket logs an ATT packet to debug/att_packets.jsonl
func (d *DebugLogger) LogATTPacket(direction, peerUUID string, packet interface{}, rawBytes []byte) {
	if !d.enabled {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	opcode, opcodeName, data := decodeATTPacket(packet)

	log := ATTPacketLog{
		Timestamp:  time.Now().Format(time.RFC3339Nano),
		Direction:  direction,
		PeerUUID:   peerUUID,
		Opcode:     fmt.Sprintf("0x%02X", opcode),
		OpcodeName: opcodeName,
		Data:       data,
		RawHex:     hex.EncodeToString(rawBytes),
	}

	d.appendJSONL("att_packets.jsonl", log)
}

// LogGATTOperation logs a high-level GATT operation to debug/gatt_operations.jsonl
func (d *DebugLogger) LogGATTOperation(direction, peerUUID, operation, serviceUUID, charUUID, handle string, data []byte) {
	if !d.enabled {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	log := GATTOperationLog{
		Timestamp:          time.Now().Format(time.RFC3339Nano),
		Direction:          direction,
		PeerUUID:           peerUUID,
		Operation:          operation,
		ServiceUUID:        serviceUUID,
		CharacteristicUUID: charUUID,
		Handle:             handle,
		DataLen:            len(data),
	}

	if len(data) > 0 {
		log.DataHex = hex.EncodeToString(data)
	}

	d.appendJSONL("gatt_operations.jsonl", log)
}

// appendJSONL appends a JSON line to a file
func (d *DebugLogger) appendJSONL(filename string, data interface{}) {
	path := filepath.Join(d.debugDir, filename)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return // Silently fail - debug logging is best-effort
	}
	defer f.Close()

	line, err := json.Marshal(data)
	if err != nil {
		return
	}

	f.Write(line)
	f.Write([]byte("\n"))
}

// getChannelName returns the human-readable name for an L2CAP channel
func getChannelName(channelID uint16) string {
	switch channelID {
	case l2cap.ChannelNULL:
		return "NULL"
	case l2cap.ChannelSignaling:
		return "ACL-U Signaling"
	case l2cap.ChannelConnless:
		return "Connectionless"
	case l2cap.ChannelAMP:
		return "AMP Manager"
	case l2cap.ChannelATT:
		return "ATT"
	case l2cap.ChannelLESignal:
		return "LE L2CAP Signaling"
	case l2cap.ChannelSMP:
		return "SMP"
	case l2cap.ChannelBR:
		return "BR/EDR Security Manager"
	default:
		return "Unknown"
	}
}

// decodeATTPacket extracts opcode, name, and data from an ATT packet
func decodeATTPacket(packet interface{}) (opcode uint8, name string, data map[string]interface{}) {
	data = make(map[string]interface{})

	switch p := packet.(type) {
	case *att.ExchangeMTURequest:
		opcode = att.OpExchangeMTURequest
		name = "Exchange MTU Request"
		data["client_rx_mtu"] = p.ClientRxMTU

	case *att.ExchangeMTUResponse:
		opcode = att.OpExchangeMTUResponse
		name = "Exchange MTU Response"
		data["server_rx_mtu"] = p.ServerRxMTU

	case *att.ReadRequest:
		opcode = att.OpReadRequest
		name = "Read Request"
		data["handle"] = fmt.Sprintf("0x%04X", p.Handle)

	case *att.ReadResponse:
		opcode = att.OpReadResponse
		name = "Read Response"
		data["value_len"] = len(p.Value)
		data["value_hex"] = hex.EncodeToString(p.Value)

	case *att.WriteRequest:
		opcode = att.OpWriteRequest
		name = "Write Request"
		data["handle"] = fmt.Sprintf("0x%04X", p.Handle)
		data["value_len"] = len(p.Value)
		data["value_hex"] = hex.EncodeToString(p.Value)

	case *att.WriteResponse:
		opcode = att.OpWriteResponse
		name = "Write Response"

	case *att.WriteCommand:
		opcode = att.OpWriteCommand
		name = "Write Command"
		data["handle"] = fmt.Sprintf("0x%04X", p.Handle)
		data["value_len"] = len(p.Value)
		data["value_hex"] = hex.EncodeToString(p.Value)

	case *att.HandleValueNotification:
		opcode = att.OpHandleValueNotification
		name = "Handle Value Notification"
		data["handle"] = fmt.Sprintf("0x%04X", p.Handle)
		data["value_len"] = len(p.Value)
		data["value_hex"] = hex.EncodeToString(p.Value)

	case *att.HandleValueIndication:
		opcode = att.OpHandleValueIndication
		name = "Handle Value Indication"
		data["handle"] = fmt.Sprintf("0x%04X", p.Handle)
		data["value_len"] = len(p.Value)
		data["value_hex"] = hex.EncodeToString(p.Value)

	case *att.ErrorResponse:
		opcode = att.OpErrorResponse
		name = "Error Response"
		data["request_opcode"] = fmt.Sprintf("0x%02X", p.RequestOpcode)
		data["request_opcode_name"] = att.OpcodeNames[p.RequestOpcode]
		data["handle"] = fmt.Sprintf("0x%04X", p.Handle)
		data["error_code"] = fmt.Sprintf("0x%02X", p.ErrorCode)
		data["error_name"] = att.ErrorNames[p.ErrorCode]

	default:
		opcode = 0xFF
		name = "Unknown"
	}

	return
}
