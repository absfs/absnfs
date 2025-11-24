---
layout: default
title: NFS Server with Logging Example
---

# NFS Server with Logging

This example demonstrates how to set up production-ready logging for an ABSNFS server. It shows various logging configurations for different environments.

## Basic Logging Example

```go
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/absfs/absnfs"
	"github.com/absfs/memfs"
)

func main() {
	// Create an in-memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		log.Fatalf("Failed to create filesystem: %v", err)
	}

	// Create NFS server with logging enabled
	server, err := absnfs.New(fs, absnfs.ExportOptions{
		Log: &absnfs.LogConfig{
			Level:  "info",
			Format: "text",
			Output: "stdout",
		},
	})
	if err != nil {
		log.Fatalf("Failed to create NFS server: %v", err)
	}
	defer server.Close()

	// Set up graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Export the filesystem
	fmt.Println("Starting NFS server with logging...")
	if err := server.Export("/export/memfs", 2049); err != nil {
		log.Fatalf("Failed to export filesystem: %v", err)
	}

	fmt.Println("NFS server running. Press Ctrl+C to stop.")

	// Wait for shutdown signal
	<-sigChan
	fmt.Println("\nShutting down...")
}
```

## Development Environment Configuration

For development, you typically want verbose logging with all features enabled:

```go
options := absnfs.ExportOptions{
	Log: &absnfs.LogConfig{
		// Debug level for detailed diagnostics
		Level: "debug",

		// Text format is easier to read during development
		Format: "text",

		// Output to console for immediate feedback
		Output: "stdout",

		// Enable all logging features for development
		LogClientIPs:  true,
		LogOperations: true,
		LogFileAccess: true,
	},
}

server, err := absnfs.New(fs, options)
```

## Production Environment Configuration

For production, you want structured JSON logs with appropriate verbosity:

```go
options := absnfs.ExportOptions{
	Log: &absnfs.LogConfig{
		// Info level balances detail with performance
		Level: "info",

		// JSON format for log aggregation systems
		Format: "json",

		// Write to file for persistence
		Output: "/var/log/nfs/server.log",

		// Disable client IPs for privacy (GDPR compliance)
		LogClientIPs: false,

		// Disable operation logs to reduce volume
		LogOperations: false,

		// Enable file access for security auditing
		LogFileAccess: true,

		// Configure log rotation
		MaxSize:    100,  // 100 MB per file
		MaxBackups: 10,   // Keep 10 old files
		MaxAge:     90,   // Keep logs for 90 days
		Compress:   true, // Compress rotated files
	},
}

server, err := absnfs.New(fs, options)
```

## High-Traffic Environment Configuration

For high-traffic servers, minimize logging overhead:

```go
options := absnfs.ExportOptions{
	Log: &absnfs.LogConfig{
		// Only log warnings and errors
		Level: "warn",

		// JSON for efficient parsing
		Format: "json",

		// Write to file
		Output: "/var/log/nfs/server.log",

		// Disable all detailed logging
		LogClientIPs:  false,
		LogOperations: false,
		LogFileAccess: false,

		// Aggressive log rotation
		MaxSize:    50,  // Smaller files
		MaxBackups: 5,   // Fewer backups
		MaxAge:     30,  // Shorter retention
		Compress:   true,
	},
}

server, err := absnfs.New(fs, options)
```

## Debugging Configuration

When troubleshooting issues, enable maximum verbosity:

```go
options := absnfs.ExportOptions{
	Log: &absnfs.LogConfig{
		// Maximum verbosity
		Level: "debug",

		// JSON for analysis tools
		Format: "json",

		// Separate debug log file
		Output: "/var/log/nfs/debug.log",

		// Enable all logging features
		LogClientIPs:  true,
		LogOperations: true,
		LogFileAccess: true,

		// Keep detailed logs
		MaxSize:    200,  // Larger files for more data
		MaxBackups: 20,   // More backups for analysis
		MaxAge:     7,    // Shorter retention (debug logs are large)
		Compress:   true,
	},
}

server, err := absnfs.New(fs, options)
```

## Changing Logger at Runtime

You can change the logger configuration while the server is running:

```go
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/absfs/absnfs"
	"github.com/absfs/memfs"
)

func main() {
	fs, err := memfs.NewFS()
	if err != nil {
		log.Fatalf("Failed to create filesystem: %v", err)
	}

	// Start with basic logging
	server, err := absnfs.New(fs, absnfs.ExportOptions{
		Log: &absnfs.LogConfig{
			Level:  "info",
			Format: "text",
			Output: "stdout",
		},
	})
	if err != nil {
		log.Fatalf("Failed to create NFS server: %v", err)
	}
	defer server.Close()

	// Export the filesystem
	if err := server.Export("/export/memfs", 2049); err != nil {
		log.Fatalf("Failed to export filesystem: %v", err)
	}

	// Set up signal handlers
	sigChan := make(chan os.Signal, 1)
	debugChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	signal.Notify(debugChan, syscall.SIGUSR1) // Use SIGUSR1 to toggle debug mode

	fmt.Println("NFS server running.")
	fmt.Println("  Send SIGUSR1 to toggle debug mode")
	fmt.Println("  Press Ctrl+C to stop")

	debugMode := false

	for {
		select {
		case <-sigChan:
			fmt.Println("\nShutting down...")
			return

		case <-debugChan:
			// Toggle debug mode
			debugMode = !debugMode

			var newLogger absnfs.Logger
			if debugMode {
				fmt.Println("Switching to debug mode...")
				config := &absnfs.LogConfig{
					Level:         "debug",
					Format:        "json",
					Output:        "/tmp/nfs-debug.log",
					LogClientIPs:  true,
					LogOperations: true,
					LogFileAccess: true,
				}
				newLogger, err = absnfs.NewSlogLogger(config)
			} else {
				fmt.Println("Switching to normal mode...")
				config := &absnfs.LogConfig{
					Level:  "info",
					Format: "text",
					Output: "stdout",
				}
				newLogger, err = absnfs.NewSlogLogger(config)
			}

			if err != nil {
				log.Printf("Failed to create new logger: %v", err)
				continue
			}

			if err := server.SetLogger(newLogger); err != nil {
				log.Printf("Failed to set logger: %v", err)
			}
		}
	}
}
```

## Custom Logger Implementation

You can implement your own logger to integrate with existing logging systems:

```go
package main

import (
	"fmt"
	"log"

	"github.com/absfs/absnfs"
	"github.com/absfs/memfs"
)

// CustomLogger implements the absnfs.Logger interface
type CustomLogger struct {
	// Your logger implementation fields
	debugEnabled bool
}

func (l *CustomLogger) Debug(msg string, fields ...absnfs.LogField) {
	if !l.debugEnabled {
		return
	}
	fmt.Printf("[DEBUG] %s", msg)
	for _, field := range fields {
		fmt.Printf(" %s=%v", field.Key, field.Value)
	}
	fmt.Println()
}

func (l *CustomLogger) Info(msg string, fields ...absnfs.LogField) {
	fmt.Printf("[INFO] %s", msg)
	for _, field := range fields {
		fmt.Printf(" %s=%v", field.Key, field.Value)
	}
	fmt.Println()
}

func (l *CustomLogger) Warn(msg string, fields ...absnfs.LogField) {
	fmt.Printf("[WARN] %s", msg)
	for _, field := range fields {
		fmt.Printf(" %s=%v", field.Key, field.Value)
	}
	fmt.Println()
}

func (l *CustomLogger) Error(msg string, fields ...absnfs.LogField) {
	fmt.Printf("[ERROR] %s", msg)
	for _, field := range fields {
		fmt.Printf(" %s=%v", field.Key, field.Value)
	}
	fmt.Println()
}

func main() {
	fs, err := memfs.NewFS()
	if err != nil {
		log.Fatalf("Failed to create filesystem: %v", err)
	}

	// Create server without built-in logging
	server, err := absnfs.New(fs, absnfs.ExportOptions{})
	if err != nil {
		log.Fatalf("Failed to create NFS server: %v", err)
	}
	defer server.Close()

	// Set custom logger
	customLogger := &CustomLogger{
		debugEnabled: true,
	}

	if err := server.SetLogger(customLogger); err != nil {
		log.Fatalf("Failed to set logger: %v", err)
	}

	// Export the filesystem
	if err := server.Export("/export/memfs", 2049); err != nil {
		log.Fatalf("Failed to export filesystem: %v", err)
	}

	fmt.Println("NFS server running with custom logger...")
	select {} // Run forever
}
```

## Understanding Log Output

### Text Format Example

```
time=2025-01-24T10:30:45.123-05:00 level=INFO msg="connection accepted" total_connections=1
time=2025-01-24T10:30:45.234-05:00 level=DEBUG msg="LOOKUP operation" path=/export/data duration_ms=2
time=2025-01-24T10:30:45.345-05:00 level=DEBUG msg="READ operation" path=/export/data/file.txt offset=0 count=8192 duration_ms=5
time=2025-01-24T10:30:50.123-05:00 level=INFO msg="connection closed" total_connections=0
```

### JSON Format Example

```json
{"time":"2025-01-24T10:30:45.123-05:00","level":"INFO","msg":"connection accepted","total_connections":1}
{"time":"2025-01-24T10:30:45.234-05:00","level":"DEBUG","msg":"LOOKUP operation","path":"/export/data","duration_ms":2}
{"time":"2025-01-24T10:30:45.345-05:00","level":"DEBUG","msg":"READ operation","path":"/export/data/file.txt","offset":0,"count":8192,"duration_ms":5}
{"time":"2025-01-24T10:30:50.123-05:00","level":"INFO","msg":"connection closed","total_connections":0}
```

## Integration with Log Aggregation

### Using with Elasticsearch/ELK

```yaml
# filebeat.yml
filebeat.inputs:
- type: log
  paths:
    - /var/log/nfs/*.log
  json.keys_under_root: true
  json.add_error_key: true

output.elasticsearch:
  hosts: ["localhost:9200"]
  index: "nfs-logs-%{+yyyy.MM.dd}"
```

### Using with Splunk

```bash
# Configure Splunk to monitor the log directory
splunk add monitor /var/log/nfs/server.log \
  -sourcetype _json \
  -index nfs
```

### Using with CloudWatch Logs

Configure the CloudWatch agent with:

```json
{
  "logs": {
    "logs_collected": {
      "files": {
        "collect_list": [
          {
            "file_path": "/var/log/nfs/server.log",
            "log_group_name": "/nfs/server",
            "log_stream_name": "{instance_id}",
            "timezone": "UTC"
          }
        ]
      }
    }
  }
}
```

## Best Practices

1. **Start with info level**: Use `info` level in production and only enable `debug` when troubleshooting
2. **Use JSON in production**: JSON format is easier to parse and analyze
3. **Configure log rotation**: Prevent disk space issues with appropriate rotation settings
4. **Consider privacy**: Disable `LogClientIPs` if you need to comply with GDPR or similar regulations
5. **Monitor log volume**: `LogOperations` can generate high volumes on busy servers
6. **Separate debug logs**: Use a different file path for debug logs to avoid mixing with production logs
7. **Test your configuration**: Verify logs are being written correctly before deploying
8. **Set up log monitoring**: Configure alerts for error-level messages

## Next Steps

Now that you understand logging, you might want to explore:

1. [Configuration Guide](../guides/configuration.md) - Complete configuration options
2. [Monitoring Guide](../guides/monitoring.md) - Metrics and monitoring
3. [Security Guide](../guides/security.md) - Security best practices
4. [Logging API](../api/logging.md) - Detailed logging API documentation
