package absnfs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/absfs/memfs"
)

// TestLoggerInterface verifies the Logger interface is properly defined
func TestLoggerInterface(t *testing.T) {
	var _ Logger = (*SlogLogger)(nil)
	var _ Logger = (*noopLogger)(nil)
}

// TestNewSlogLogger_NilConfig tests that NewSlogLogger returns error with nil config
func TestNewSlogLogger_NilConfig(t *testing.T) {
	_, err := NewSlogLogger(nil)
	if err == nil {
		t.Fatal("expected error for nil config, got nil")
	}
	if !strings.Contains(err.Error(), "log config cannot be nil") {
		t.Errorf("expected error message about nil config, got: %v", err)
	}
}

// TestNewSlogLogger_DefaultStderr tests creating logger with default stderr output
func TestNewSlogLogger_DefaultStderr(t *testing.T) {
	config := &LogConfig{
		Level:  "info",
		Format: "text",
		Output: "stderr",
	}

	logger, err := NewSlogLogger(config)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer logger.Close()

	if logger.logger == nil {
		t.Error("logger.logger is nil")
	}
}

// TestNewSlogLogger_StdoutOutput tests creating logger with stdout output
func TestNewSlogLogger_StdoutOutput(t *testing.T) {
	config := &LogConfig{
		Level:  "info",
		Format: "text",
		Output: "stdout",
	}

	logger, err := NewSlogLogger(config)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	defer logger.Close()

	if logger.logger == nil {
		t.Error("logger.logger is nil")
	}
}

// TestNewSlogLogger_FileOutput tests creating logger with file output
func TestNewSlogLogger_FileOutput(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	config := &LogConfig{
		Level:  "debug",
		Format: "json",
		Output: logFile,
	}

	logger, err := NewSlogLogger(config)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	// Log some messages
	logger.Debug("debug message", LogField{Key: "test", Value: "value"})
	logger.Info("info message")
	logger.Warn("warn message", LogField{Key: "code", Value: 123})
	logger.Error("error message")

	// Close logger to flush
	if err := logger.Close(); err != nil {
		t.Fatalf("failed to close logger: %v", err)
	}

	// Verify file exists and has content
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "debug message") {
		t.Error("log file missing debug message")
	}
	if !strings.Contains(content, "info message") {
		t.Error("log file missing info message")
	}
	if !strings.Contains(content, "warn message") {
		t.Error("log file missing warn message")
	}
	if !strings.Contains(content, "error message") {
		t.Error("log file missing error message")
	}

	// Verify JSON format
	lines := strings.Split(strings.TrimSpace(content), "\n")
	for _, line := range lines {
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Errorf("invalid JSON in log line: %v", err)
		}
	}
}

// TestNewSlogLogger_InvalidFormat tests error handling for invalid format
func TestNewSlogLogger_InvalidFormat(t *testing.T) {
	config := &LogConfig{
		Level:  "info",
		Format: "invalid",
		Output: "stderr",
	}

	_, err := NewSlogLogger(config)
	if err == nil {
		t.Fatal("expected error for invalid format, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported log format") {
		t.Errorf("expected error about invalid format, got: %v", err)
	}
}

// TestNewSlogLogger_LogLevels tests different log levels
func TestNewSlogLogger_LogLevels(t *testing.T) {
	tests := []struct {
		name     string
		level    string
		logDebug bool
		logInfo  bool
		logWarn  bool
		logError bool
	}{
		{"debug", "debug", true, true, true, true},
		{"info", "info", false, true, true, true},
		{"warn", "warn", false, false, true, true},
		{"error", "error", false, false, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			logFile := filepath.Join(tmpDir, "test.log")

			config := &LogConfig{
				Level:  tt.level,
				Format: "text",
				Output: logFile,
			}

			logger, err := NewSlogLogger(config)
			if err != nil {
				t.Fatalf("failed to create logger: %v", err)
			}

			// Log messages at all levels
			logger.Debug("debug msg")
			logger.Info("info msg")
			logger.Warn("warn msg")
			logger.Error("error msg")

			logger.Close()

			// Read log file
			data, err := os.ReadFile(logFile)
			if err != nil {
				t.Fatalf("failed to read log file: %v", err)
			}

			content := string(data)

			// Check which messages are present based on log level
			if tt.logDebug && !strings.Contains(content, "debug msg") {
				t.Error("expected debug message in log")
			}
			if !tt.logDebug && strings.Contains(content, "debug msg") {
				t.Error("unexpected debug message in log")
			}

			if tt.logInfo && !strings.Contains(content, "info msg") {
				t.Error("expected info message in log")
			}
			if !tt.logInfo && strings.Contains(content, "info msg") {
				t.Error("unexpected info message in log")
			}

			if tt.logWarn && !strings.Contains(content, "warn msg") {
				t.Error("expected warn message in log")
			}
			if !tt.logWarn && strings.Contains(content, "warn msg") {
				t.Error("unexpected warn message in log")
			}

			if tt.logError && !strings.Contains(content, "error msg") {
				t.Error("expected error message in log")
			}
			if !tt.logError && strings.Contains(content, "error msg") {
				t.Error("unexpected error message in log")
			}
		})
	}
}

// TestNoopLogger tests that noop logger doesn't produce output or errors
func TestNoopLogger(t *testing.T) {
	logger := NewNoopLogger()

	// Should not panic or error
	logger.Debug("debug")
	logger.Info("info")
	logger.Warn("warn")
	logger.Error("error")
}

// TestAbsfsNFS_WithLogging tests AbsfsNFS integration with logging
func TestAbsfsNFS_WithLogging(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "nfs.log")

	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("failed to create memfs: %v", err)
	}

	// Create NFS server with logging
	options := ExportOptions{
		Log: &LogConfig{
			Level:  "info",
			Format: "json",
			Output: logFile,
		},
	}

	server, err := New(fs, options)
	if err != nil {
		t.Fatalf("failed to create NFS server: %v", err)
	}
	defer server.Close()

	// Verify structured logger is initialized
	if server.structuredLogger == nil {
		t.Error("structuredLogger is nil")
	}

	// Verify it's an SlogLogger
	if _, ok := server.structuredLogger.(*SlogLogger); !ok {
		t.Error("structuredLogger is not an SlogLogger")
	}
}

// TestAbsfsNFS_WithoutLogging tests AbsfsNFS without logging (no-op logger)
func TestAbsfsNFS_WithoutLogging(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("failed to create memfs: %v", err)
	}

	// Create NFS server without logging
	options := ExportOptions{}

	server, err := New(fs, options)
	if err != nil {
		t.Fatalf("failed to create NFS server: %v", err)
	}
	defer server.Close()

	// Verify structured logger is initialized (as no-op)
	if server.structuredLogger == nil {
		t.Error("structuredLogger is nil")
	}

	// Verify it's a noopLogger
	if _, ok := server.structuredLogger.(*noopLogger); !ok {
		t.Error("structuredLogger is not a noopLogger")
	}
}

// TestAbsfsNFS_SetLogger tests the SetLogger method
func TestAbsfsNFS_SetLogger(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("failed to create memfs: %v", err)
	}

	server, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatalf("failed to create NFS server: %v", err)
	}
	defer server.Close()

	// Create a new logger
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	config := &LogConfig{
		Level:  "debug",
		Format: "json",
		Output: logFile,
	}

	newLogger, err := NewSlogLogger(config)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	// Set the logger
	if err := server.SetLogger(newLogger); err != nil {
		t.Fatalf("failed to set logger: %v", err)
	}

	// Verify logger was set
	if _, ok := server.structuredLogger.(*SlogLogger); !ok {
		t.Error("structuredLogger is not an SlogLogger after SetLogger")
	}
}

// TestAbsfsNFS_SetLogger_Nil tests setting logger to nil (should use no-op)
func TestAbsfsNFS_SetLogger_Nil(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("failed to create memfs: %v", err)
	}

	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	server, err := New(fs, ExportOptions{
		Log: &LogConfig{
			Level:  "info",
			Format: "json",
			Output: logFile,
		},
	})
	if err != nil {
		t.Fatalf("failed to create NFS server: %v", err)
	}
	defer server.Close()

	// Verify logger is SlogLogger initially
	if _, ok := server.structuredLogger.(*SlogLogger); !ok {
		t.Fatal("structuredLogger is not an SlogLogger initially")
	}

	// Set logger to nil
	if err := server.SetLogger(nil); err != nil {
		t.Fatalf("failed to set logger to nil: %v", err)
	}

	// Verify logger is now noopLogger
	if _, ok := server.structuredLogger.(*noopLogger); !ok {
		t.Error("structuredLogger is not a noopLogger after SetLogger(nil)")
	}
}

// TestLogField tests LogField creation
func TestLogField(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value interface{}
	}{
		{"string", "key", "value"},
		{"int", "count", 123},
		{"bool", "enabled", true},
		{"float", "ratio", 3.14},
		{"nil", "empty", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field := LogField{Key: tt.key, Value: tt.value}
			if field.Key != tt.key {
				t.Errorf("expected key %s, got %s", tt.key, field.Key)
			}
			if field.Value != tt.value {
				t.Errorf("expected value %v, got %v", tt.value, field.Value)
			}
		})
	}
}

// TestSlogLogger_Close tests closing the logger multiple times
func TestSlogLogger_Close(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	config := &LogConfig{
		Level:  "info",
		Format: "text",
		Output: logFile,
	}

	logger, err := NewSlogLogger(config)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	// Close once
	if err := logger.Close(); err != nil {
		t.Fatalf("failed to close logger: %v", err)
	}

	// Close again - should not panic
	if err := logger.Close(); err == nil {
		// Multiple closes might return an error, which is acceptable
		t.Log("second close returned nil (acceptable)")
	}
}

// TestLogConfig_AllFields tests all LogConfig fields
func TestLogConfig_AllFields(t *testing.T) {
	config := LogConfig{
		Level:          "debug",
		Format:         "json",
		Output:         "/var/log/nfs.log",
		LogClientIPs:   true,
		LogOperations:  true,
		LogFileAccess:  true,
		MaxSize:        100,
		MaxBackups:     5,
		MaxAge:         30,
		Compress:       true,
	}

	// Verify all fields are accessible
	if config.Level != "debug" {
		t.Error("Level field mismatch")
	}
	if config.Format != "json" {
		t.Error("Format field mismatch")
	}
	if config.Output != "/var/log/nfs.log" {
		t.Error("Output field mismatch")
	}
	if !config.LogClientIPs {
		t.Error("LogClientIPs field mismatch")
	}
	if !config.LogOperations {
		t.Error("LogOperations field mismatch")
	}
	if !config.LogFileAccess {
		t.Error("LogFileAccess field mismatch")
	}
	if config.MaxSize != 100 {
		t.Error("MaxSize field mismatch")
	}
	if config.MaxBackups != 5 {
		t.Error("MaxBackups field mismatch")
	}
	if config.MaxAge != 30 {
		t.Error("MaxAge field mismatch")
	}
	if !config.Compress {
		t.Error("Compress field mismatch")
	}
}

// TestSlogLogger_NilSafety tests that nil logger doesn't panic
func TestSlogLogger_NilSafety(t *testing.T) {
	var logger *SlogLogger

	// Should not panic
	logger.Debug("test")
	logger.Info("test")
	logger.Warn("test")
	logger.Error("test")
}
