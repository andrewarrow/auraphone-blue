package logger

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// LogLevel represents the severity of a log message
type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
)

var (
	currentLevel LogLevel = DEBUG
	mu           sync.RWMutex
)

// SetLevel sets the global log level
func SetLevel(level LogLevel) {
	mu.Lock()
	defer mu.Unlock()
	currentLevel = level
}

// GetLevel returns the current log level
func GetLevel() LogLevel {
	mu.RLock()
	defer mu.RUnlock()
	return currentLevel
}

// ParseLevel converts a string to a LogLevel
func ParseLevel(level string) LogLevel {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return DEBUG
	case "INFO":
		return INFO
	case "WARN":
		return WARN
	case "ERROR":
		return ERROR
	default:
		return INFO
	}
}

func log(level LogLevel, prefix, format string, args ...interface{}) {
	if level < GetLevel() {
		return
	}

	var levelStr string
	switch level {
	case DEBUG:
		levelStr = "DEBUG"
	case INFO:
		levelStr = "INFO "
	case WARN:
		levelStr = "WARN "
	case ERROR:
		levelStr = "ERROR"
	}

	msg := fmt.Sprintf(format, args...)
	if prefix != "" {
		fmt.Fprintf(os.Stdout, "[%s %s] %s\n", prefix, levelStr, msg)
	} else {
		fmt.Fprintf(os.Stdout, "[%s] %s\n", levelStr, msg)
	}
}

// Debug logs a debug message
func Debug(prefix, format string, args ...interface{}) {
	log(DEBUG, prefix, format, args...)
}

// Info logs an info message
func Info(prefix, format string, args ...interface{}) {
	log(INFO, prefix, format, args...)
}

// Warn logs a warning message
func Warn(prefix, format string, args ...interface{}) {
	log(WARN, prefix, format, args...)
}

// Error logs an error message
func Error(prefix, format string, args ...interface{}) {
	log(ERROR, prefix, format, args...)
}

// ToJSON converts any value to a pretty-printed JSON string for logging
func ToJSON(v interface{}) string {
	// Check if it's a protobuf message
	if msg, ok := v.(proto.Message); ok {
		// Use protojson for protobuf messages (handles field names correctly)
		marshaler := protojson.MarshalOptions{
			Multiline:       true,
			Indent:          "  ",
			EmitUnpopulated: false, // Only show fields with values
		}
		jsonBytes, err := marshaler.Marshal(msg)
		if err != nil {
			return fmt.Sprintf("<error: %v>", err)
		}
		return string(jsonBytes)
	}

	// For regular structs, use standard JSON
	jsonBytes, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("<error: %v>", err)
	}
	return string(jsonBytes)
}

// DebugJSON logs a debug message with a JSON representation
func DebugJSON(prefix, label string, v interface{}) {
	if GetLevel() > DEBUG {
		return
	}
	jsonStr := ToJSON(v)
	log(DEBUG, prefix, "%s:\n%s", label, jsonStr)
}
