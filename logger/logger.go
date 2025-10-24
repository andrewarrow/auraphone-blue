package logger

import (
	"fmt"
	"os"
	"strings"
	"sync"
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
	currentLevel LogLevel = INFO
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
