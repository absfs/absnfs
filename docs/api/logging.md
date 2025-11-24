# Logging API

ABSNFS provides production-ready structured logging infrastructure using Go's standard library `log/slog` package (Go 1.21+). This document describes the logging API and its configuration options.

## Overview

The logging system provides:

- **Structured logging** with key-value pairs for better parsing and analysis
- **Multiple output formats** (text and JSON)
- **Configurable log levels** (debug, info, warn, error)
- **Flexible output destinations** (stdout, stderr, or file)
- **Optional detailed logging** for operations, file access, and client IPs
- **Custom logger support** through the Logger interface
- **Nil-safe operations** - logging is optional and can be disabled

## Logger Interface

The `Logger` interface defines the contract for logging in ABSNFS:

```go
type Logger interface {
    Debug(msg string, fields ...LogField)
    Info(msg string, fields ...LogField)
    Warn(msg string, fields ...LogField)
    Error(msg string, fields ...LogField)
}
```

### LogField

Structured log fields are represented by the `LogField` type:

```go
type LogField struct {
    Key   string
    Value interface{}
}
```

## Default Implementation: SlogLogger

ABSNFS provides a default `SlogLogger` implementation using Go's `log/slog` package:

```go
type SlogLogger struct {
    logger *slog.Logger
    mu     sync.Mutex
    writer io.WriteCloser
}
```

### Creating a Logger

```go
config := &absnfs.LogConfig{
    Level:  "info",
    Format: "json",
    Output: "/var/log/nfs.log",
}

logger, err := absnfs.NewSlogLogger(config)
if err != nil {
    log.Fatal(err)
}
defer logger.Close()
```

## LogConfig

The `LogConfig` struct defines logging configuration:

```go
type LogConfig struct {
    // Core configuration
    Level  string  // "debug", "info", "warn", "error" (default: "info")
    Format string  // "json", "text" (default: "text")
    Output string  // "stdout", "stderr", or file path (default: "stderr")

    // Feature flags
    LogClientIPs  bool  // Log client IP addresses (default: false)
    LogOperations bool  // Log NFS operations with timing (default: false)
    LogFileAccess bool  // Log file access patterns (default: false)

    // File rotation (when Output is a file path)
    MaxSize    int   // Max size in MB before rotation (default: 100)
    MaxBackups int   // Max number of old log files to keep (default: 3)
    MaxAge     int   // Max days to retain old log files (default: 28)
    Compress   bool  // Compress rotated logs with gzip (default: false)
}
```

### Log Levels

- **debug**: Detailed diagnostic information including cache hits/misses
- **info**: General informational messages (connection events, operations)
- **warn**: Warning conditions (rate limits, rejected connections)
- **error**: Error conditions that need attention

### Output Formats

#### Text Format
Human-readable format suitable for development and console output:

```
time=2025-01-24T10:30:45.123-05:00 level=INFO msg="connection accepted" total_connections=5 client_addr=192.168.1.100:54321
```

#### JSON Format
Machine-readable format suitable for log aggregation and analysis:

```json
{"time":"2025-01-24T10:30:45.123-05:00","level":"INFO","msg":"connection accepted","total_connections":5,"client_addr":"192.168.1.100:54321"}
```

## Usage with AbsfsNFS

### Enable Logging at Server Creation

```go
fs, _ := memfs.NewFS()

options := absnfs.ExportOptions{
    Log: &absnfs.LogConfig{
        Level:         "info",
        Format:        "json",
        Output:        "/var/log/nfs.log",
        LogClientIPs:  true,
        LogOperations: false,
        LogFileAccess: true,
    },
}

server, err := absnfs.New(fs, options)
if err != nil {
    log.Fatal(err)
}
defer server.Close()
```

### Change Logger After Creation

```go
// Create a new logger configuration
config := &absnfs.LogConfig{
    Level:  "debug",
    Format: "text",
    Output: "stdout",
}

newLogger, err := absnfs.NewSlogLogger(config)
if err != nil {
    log.Fatal(err)
}

// Update the server's logger
if err := server.SetLogger(newLogger); err != nil {
    log.Fatal(err)
}
```

### Disable Logging

```go
// Option 1: Don't provide Log config (uses no-op logger by default)
server, _ := absnfs.New(fs, absnfs.ExportOptions{})

// Option 2: Set logger to nil after creation
server.SetLogger(nil)
```

## Logged Events

### Connection Events (Info Level)

When `LogClientIPs` is enabled:
```
connection accepted: total_connections=5, client_addr=192.168.1.100:54321
connection closed: total_connections=4, client_addr=192.168.1.100:54321
```

### Authentication Events (Warn Level)

When `LogClientIPs` is enabled:
```
connection rejected: IP not allowed, client_ip=10.0.0.5
rate limit exceeded, client_ip=192.168.1.100
```

### NFS Operations (Debug Level)

When `LogOperations` is enabled:
```
LOOKUP operation: path=/exports/data, duration_ms=2
READ operation: path=/exports/data/file.txt, offset=0, count=8192, duration_ms=5
WRITE operation: path=/exports/data/file.txt, offset=0, size=4096, duration_ms=8
```

### File Access (Info Level)

When `LogFileAccess` is enabled:
```
CREATE operation: dir=/exports/data, name=newfile.txt, duration_ms=3
REMOVE operation: dir=/exports/data, name=oldfile.txt, duration_ms=2
```

### Cache Events (Debug Level)

Always logged at debug level:
```
attribute cache hit: path=/exports/data/file.txt
attribute cache miss: path=/exports/data/newfile.txt
read-ahead cache hit: path=/exports/data/large.bin, offset=8192, bytes_read=8192
read-ahead cache miss: path=/exports/data/random.dat, offset=1024, count=4096
```

## Custom Logger Implementation

You can provide your own logger implementation by satisfying the `Logger` interface:

```go
type CustomLogger struct {
    // Your logger implementation
}

func (l *CustomLogger) Debug(msg string, fields ...absnfs.LogField) {
    // Implement debug logging
}

func (l *CustomLogger) Info(msg string, fields ...absnfs.LogField) {
    // Implement info logging
}

func (l *CustomLogger) Warn(msg string, fields ...absnfs.LogField) {
    // Implement warn logging
}

func (l *CustomLogger) Error(msg string, fields ...absnfs.LogField) {
    // Implement error logging
}

// Use your custom logger
customLogger := &CustomLogger{}
server.SetLogger(customLogger)
```

## Best Practices

### Development Environment

```go
Log: &absnfs.LogConfig{
    Level:         "debug",
    Format:        "text",
    Output:        "stdout",
    LogClientIPs:  true,
    LogOperations: true,
    LogFileAccess: true,
}
```

### Production Environment

```go
Log: &absnfs.LogConfig{
    Level:         "info",
    Format:        "json",
    Output:        "/var/log/nfs/server.log",
    LogClientIPs:  false,  // For privacy
    LogOperations: false,  // High volume
    LogFileAccess: true,   // Security auditing
    MaxSize:       100,
    MaxBackups:    10,
    MaxAge:        90,
    Compress:      true,
}
```

### High-Traffic Environment

For high-traffic servers, minimize logging overhead:

```go
Log: &absnfs.LogConfig{
    Level:         "warn",
    Format:        "json",
    Output:        "/var/log/nfs/server.log",
    LogClientIPs:  false,
    LogOperations: false,
    LogFileAccess: false,
}
```

### Debugging Issues

When troubleshooting problems:

```go
Log: &absnfs.LogConfig{
    Level:         "debug",
    Format:        "json",
    Output:        "/var/log/nfs/debug.log",
    LogClientIPs:  true,
    LogOperations: true,
    LogFileAccess: true,
}
```

## Performance Considerations

- **Log Level**: Use `info` or higher in production. `debug` level generates significant log volume
- **LogOperations**: Generates one log entry per NFS operation - can be very high volume
- **LogFileAccess**: Moderate volume, useful for security auditing
- **Format**: JSON format has slightly higher overhead but is better for log aggregation
- **Output**: Writing to files is generally faster than stdout/stderr

## Integration with Log Aggregation

The JSON output format integrates well with log aggregation systems:

### Elasticsearch/ELK Stack

```bash
filebeat -e -c filebeat.yml
```

filebeat.yml:
```yaml
filebeat.inputs:
- type: log
  paths:
    - /var/log/nfs/*.log
  json.keys_under_root: true
  json.add_error_key: true
```

### Splunk

```bash
splunk add monitor /var/log/nfs/server.log -sourcetype _json
```

### CloudWatch Logs

Use the CloudWatch agent with JSON log parsing enabled.

## Thread Safety

All logging operations are thread-safe. The `SlogLogger` implementation uses appropriate locking to ensure concurrent logging from multiple goroutines is safe.

## Resource Management

Always close loggers when done to ensure logs are flushed:

```go
server, err := absnfs.New(fs, options)
if err != nil {
    log.Fatal(err)
}
defer server.Close()  // Automatically closes the logger
```

For standalone loggers:

```go
logger, err := absnfs.NewSlogLogger(config)
if err != nil {
    log.Fatal(err)
}
defer logger.Close()
```

## See Also

- [Configuration Guide](../guides/configuration.md) - Complete configuration options
- [Monitoring Guide](../guides/monitoring.md) - Metrics and monitoring
- [Security Guide](../guides/security.md) - Security best practices
