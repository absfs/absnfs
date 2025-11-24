package absnfs

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// Logger defines the interface for logging in ABSNFS.
// Applications can provide their own implementation to integrate with existing logging systems.
type Logger interface {
	// Debug logs a debug-level message with optional structured fields
	Debug(msg string, fields ...LogField)

	// Info logs an info-level message with optional structured fields
	Info(msg string, fields ...LogField)

	// Warn logs a warning-level message with optional structured fields
	Warn(msg string, fields ...LogField)

	// Error logs an error-level message with optional structured fields
	Error(msg string, fields ...LogField)
}

// LogField represents a structured logging field with a key-value pair
type LogField struct {
	Key   string
	Value interface{}
}

// SlogLogger is the default Logger implementation using Go's stdlib slog package (Go 1.21+)
type SlogLogger struct {
	logger *slog.Logger
	mu     sync.Mutex
	writer io.WriteCloser // Optional writer for file logging
}

// NewSlogLogger creates a new SlogLogger with the provided configuration
func NewSlogLogger(config *LogConfig) (*SlogLogger, error) {
	if config == nil {
		return nil, fmt.Errorf("log config cannot be nil")
	}

	// Parse log level
	level := parseLogLevel(config.Level)

	// Determine output writer
	var writer io.Writer
	var closer io.WriteCloser

	switch config.Output {
	case "", "stderr":
		writer = os.Stderr
	case "stdout":
		writer = os.Stdout
	default:
		// Treat as file path
		// Ensure directory exists
		dir := filepath.Dir(config.Output)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create log directory: %w", err)
		}

		// Open file for writing
		file, err := os.OpenFile(config.Output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open log file: %w", err)
		}
		writer = file
		closer = file
	}

	// Create handler based on format
	var handler slog.Handler
	opts := &slog.HandlerOptions{
		Level: level,
	}

	switch config.Format {
	case "json":
		handler = slog.NewJSONHandler(writer, opts)
	case "text", "":
		handler = slog.NewTextHandler(writer, opts)
	default:
		if closer != nil {
			closer.Close()
		}
		return nil, fmt.Errorf("unsupported log format: %s", config.Format)
	}

	return &SlogLogger{
		logger: slog.New(handler),
		writer: closer,
	}, nil
}

// parseLogLevel converts a string log level to slog.Level
func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "info", "":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Debug logs a debug-level message
func (l *SlogLogger) Debug(msg string, fields ...LogField) {
	l.log(slog.LevelDebug, msg, fields...)
}

// Info logs an info-level message
func (l *SlogLogger) Info(msg string, fields ...LogField) {
	l.log(slog.LevelInfo, msg, fields...)
}

// Warn logs a warning-level message
func (l *SlogLogger) Warn(msg string, fields ...LogField) {
	l.log(slog.LevelWarn, msg, fields...)
}

// Error logs an error-level message
func (l *SlogLogger) Error(msg string, fields ...LogField) {
	l.log(slog.LevelError, msg, fields...)
}

// log is the internal method that performs the actual logging
func (l *SlogLogger) log(level slog.Level, msg string, fields ...LogField) {
	if l == nil || l.logger == nil {
		return
	}

	// Convert LogField slice to slog.Attr slice
	attrs := make([]slog.Attr, 0, len(fields))
	for _, field := range fields {
		attrs = append(attrs, slog.Any(field.Key, field.Value))
	}

	// Use LogAttrs for efficient structured logging
	l.logger.LogAttrs(context.Background(), level, msg, attrs...)
}

// Close closes the logger and any associated resources
func (l *SlogLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.writer != nil {
		return l.writer.Close()
	}
	return nil
}

// noopLogger is a logger that does nothing (for when logging is disabled)
type noopLogger struct{}

func (n *noopLogger) Debug(msg string, fields ...LogField) {}
func (n *noopLogger) Info(msg string, fields ...LogField)  {}
func (n *noopLogger) Warn(msg string, fields ...LogField)  {}
func (n *noopLogger) Error(msg string, fields ...LogField) {}

// NewNoopLogger creates a logger that discards all log messages
func NewNoopLogger() Logger {
	return &noopLogger{}
}
