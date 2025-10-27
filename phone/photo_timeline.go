package phone

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/user/auraphone-blue/logger"
)

// PhotoTimelineEvent represents a photo transfer lifecycle event
type PhotoTimelineEvent struct {
	Timestamp     int64  `json:"timestamp"`      // nanoseconds since epoch
	Event         string `json:"event"`          // photo_request_sent, chunk_received, photo_receive_complete, etc.
	PhotoHash     string `json:"photo_hash"`     // SHA-256 hash (first 8 chars for display)
	DeviceID      string `json:"device_id"`      // target/source device ID
	HardwareUUID  string `json:"hardware_uuid,omitempty"` // target/source hardware UUID
	Direction     string `json:"direction"`      // "send" or "receive"
	ChunkIndex    int    `json:"chunk_index,omitempty"`
	TotalChunks   int    `json:"total_chunks,omitempty"`
	BytesTransferred int `json:"bytes_transferred,omitempty"`
	ViaSocket     string `json:"via_socket,omitempty"` // "peripheral" or "central"
	Error         string `json:"error,omitempty"`
	Details       map[string]string `json:"details,omitempty"`
}

// PhotoTimelineLogger manages append-only logging of photo transfer events
type PhotoTimelineLogger struct {
	hardwareUUID string
	logPath      string
	mutex        sync.Mutex
	enabled      bool
}

// NewPhotoTimelineLogger creates a new timeline logger for a device
func NewPhotoTimelineLogger(hardwareUUID string, enabled bool) *PhotoTimelineLogger {
	if !enabled {
		return &PhotoTimelineLogger{enabled: false}
	}

	deviceDir := GetDeviceDir(hardwareUUID)
	logPath := filepath.Join(deviceDir, "photo_timeline.jsonl")

	return &PhotoTimelineLogger{
		hardwareUUID: hardwareUUID,
		logPath:      logPath,
		enabled:      true,
	}
}

// Log writes a photo timeline event to the JSONL file
func (ptl *PhotoTimelineLogger) Log(event PhotoTimelineEvent) {
	if !ptl.enabled {
		return
	}

	// Set timestamp if not already set
	if event.Timestamp == 0 {
		event.Timestamp = time.Now().UnixNano()
	}

	ptl.mutex.Lock()
	defer ptl.mutex.Unlock()

	// Open file in append mode
	f, err := os.OpenFile(ptl.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		logger.Warn(fmt.Sprintf("%s photo_timeline", ptl.hardwareUUID[:8]),
			"Failed to open photo timeline log: %v", err)
		return
	}
	defer f.Close()

	// Marshal and write JSON line
	data, err := json.Marshal(event)
	if err != nil {
		logger.Warn(fmt.Sprintf("%s photo_timeline", ptl.hardwareUUID[:8]),
			"Failed to marshal photo timeline event: %v", err)
		return
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		logger.Warn(fmt.Sprintf("%s photo_timeline", ptl.hardwareUUID[:8]),
			"Failed to write photo timeline event: %v", err)
	}
}

// Helper methods for common events

func (ptl *PhotoTimelineLogger) LogPhotoRequestSent(deviceID, hardwareUUID, photoHash, viaSocket string) {
	ptl.Log(PhotoTimelineEvent{
		Event:        "photo_request_sent",
		PhotoHash:    photoHash,
		DeviceID:     deviceID,
		HardwareUUID: hardwareUUID,
		Direction:    "receive",
		ViaSocket:    viaSocket,
	})
}

func (ptl *PhotoTimelineLogger) LogPhotoRequestReceived(deviceID, hardwareUUID, photoHash string) {
	ptl.Log(PhotoTimelineEvent{
		Event:        "photo_request_received",
		PhotoHash:    photoHash,
		DeviceID:     deviceID,
		HardwareUUID: hardwareUUID,
		Direction:    "send",
	})
}

func (ptl *PhotoTimelineLogger) LogSendStarted(deviceID, hardwareUUID, photoHash string, totalChunks int) {
	ptl.Log(PhotoTimelineEvent{
		Event:        "send_started",
		PhotoHash:    photoHash,
		DeviceID:     deviceID,
		HardwareUUID: hardwareUUID,
		Direction:    "send",
		TotalChunks:  totalChunks,
	})
}

func (ptl *PhotoTimelineLogger) LogReceiveStarted(deviceID, hardwareUUID, photoHash string, totalChunks int) {
	ptl.Log(PhotoTimelineEvent{
		Event:        "receive_started",
		PhotoHash:    photoHash,
		DeviceID:     deviceID,
		HardwareUUID: hardwareUUID,
		Direction:    "receive",
		TotalChunks:  totalChunks,
	})
}

func (ptl *PhotoTimelineLogger) LogChunkSent(deviceID, hardwareUUID, photoHash string, chunkIndex, totalChunks, bytes int) {
	ptl.Log(PhotoTimelineEvent{
		Event:            "chunk_sent",
		PhotoHash:        photoHash,
		DeviceID:         deviceID,
		HardwareUUID:     hardwareUUID,
		Direction:        "send",
		ChunkIndex:       chunkIndex,
		TotalChunks:      totalChunks,
		BytesTransferred: bytes,
	})
}

func (ptl *PhotoTimelineLogger) LogChunkReceived(deviceID, hardwareUUID, photoHash string, chunkIndex, totalChunks, bytes int) {
	ptl.Log(PhotoTimelineEvent{
		Event:            "chunk_received",
		PhotoHash:        photoHash,
		DeviceID:         deviceID,
		HardwareUUID:     hardwareUUID,
		Direction:        "receive",
		ChunkIndex:       chunkIndex,
		TotalChunks:      totalChunks,
		BytesTransferred: bytes,
	})
}

func (ptl *PhotoTimelineLogger) LogChunkAckReceived(deviceID, hardwareUUID, photoHash string, chunkIndex int) {
	ptl.Log(PhotoTimelineEvent{
		Event:        "chunk_ack_received",
		PhotoHash:    photoHash,
		DeviceID:     deviceID,
		HardwareUUID: hardwareUUID,
		Direction:    "send",
		ChunkIndex:   chunkIndex,
	})
}

func (ptl *PhotoTimelineLogger) LogChunkRetry(deviceID, hardwareUUID, photoHash string, chunkIndex, retryCount int) {
	ptl.Log(PhotoTimelineEvent{
		Event:        "chunk_retry",
		PhotoHash:    photoHash,
		DeviceID:     deviceID,
		HardwareUUID: hardwareUUID,
		Direction:    "send",
		ChunkIndex:   chunkIndex,
		Details:      map[string]string{"retry_count": fmt.Sprintf("%d", retryCount)},
	})
}

func (ptl *PhotoTimelineLogger) LogSendComplete(deviceID, hardwareUUID, photoHash string, totalBytes int, durationMs int64) {
	ptl.Log(PhotoTimelineEvent{
		Event:            "send_complete",
		PhotoHash:        photoHash,
		DeviceID:         deviceID,
		HardwareUUID:     hardwareUUID,
		Direction:        "send",
		BytesTransferred: totalBytes,
		Details:          map[string]string{"duration_ms": fmt.Sprintf("%d", durationMs)},
	})
}

func (ptl *PhotoTimelineLogger) LogReceiveComplete(deviceID, hardwareUUID, photoHash string, totalBytes int, durationMs int64) {
	ptl.Log(PhotoTimelineEvent{
		Event:            "receive_complete",
		PhotoHash:        photoHash,
		DeviceID:         deviceID,
		HardwareUUID:     hardwareUUID,
		Direction:        "receive",
		BytesTransferred: totalBytes,
		Details:          map[string]string{"duration_ms": fmt.Sprintf("%d", durationMs)},
	})
}

func (ptl *PhotoTimelineLogger) LogPhotoRequestTimeout(deviceID, hardwareUUID, photoHash string, waitedMs int64) {
	ptl.Log(PhotoTimelineEvent{
		Event:        "photo_request_timeout",
		PhotoHash:    photoHash,
		DeviceID:     deviceID,
		HardwareUUID: hardwareUUID,
		Direction:    "receive",
		Details:      map[string]string{"waited_ms": fmt.Sprintf("%d", waitedMs)},
	})
}

func (ptl *PhotoTimelineLogger) LogTransferStalled(deviceID, hardwareUUID, photoHash, direction string, chunksCompleted, totalChunks int) {
	ptl.Log(PhotoTimelineEvent{
		Event:        "transfer_stalled",
		PhotoHash:    photoHash,
		DeviceID:     deviceID,
		HardwareUUID: hardwareUUID,
		Direction:    direction,
		ChunkIndex:   chunksCompleted,
		TotalChunks:  totalChunks,
	})
}

func (ptl *PhotoTimelineLogger) LogTransferError(deviceID, hardwareUUID, photoHash, direction, errorMsg string) {
	ptl.Log(PhotoTimelineEvent{
		Event:        "transfer_error",
		PhotoHash:    photoHash,
		DeviceID:     deviceID,
		HardwareUUID: hardwareUUID,
		Direction:    direction,
		Error:        errorMsg,
	})
}
