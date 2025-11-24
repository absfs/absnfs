---
layout: default
title: Configuration
---

# Configuration

This guide covers how to configure ABSNFS for different scenarios and optimize its behavior for your specific needs.

## Configuration Through ExportOptions

ABSNFS is primarily configured through the `ExportOptions` struct, which is passed to the `New` function when creating an NFS server:

```go
options := absnfs.ExportOptions{
    // Configuration options go here
}

nfsServer, err := absnfs.New(fs, options)
```

## Basic Configuration Options

### Read-Only Mode

To export a filesystem in read-only mode, preventing any modifications:

```go
options := absnfs.ExportOptions{
    ReadOnly: true,
}
```

This is useful for:
- Protecting important data from modification
- Exporting documentation or reference data
- Sharing public datasets

### Security Settings

To configure security settings:

```go
options := absnfs.ExportOptions{
    // Enable additional security checks
    Secure: true,
    
    // Restrict access to specific IP addresses or ranges
    AllowedIPs: []string{"192.168.1.0/24", "10.0.0.5"},
    
    // Control user identity mapping
    // Options: "none", "root", "all"
    Squash: "root",
}
```

These settings help you:
- Restrict access to trusted networks
- Prevent root users on clients from having root access on the server
- Add additional validation for file paths and operations

## Performance Configuration

### Attribute Caching

To configure attribute caching for improved performance:

```go
options := absnfs.ExportOptions{
    // How long attributes are cached
    AttrCacheTimeout: 10 * time.Second,

    // Maximum number of entries in the cache
    AttrCacheSize: 10000,
}
```

Attribute caching is particularly helpful for:
- Reducing load on the underlying filesystem
- Improving response times for metadata-heavy workloads
- Handling directories with many files

### Read-Ahead Buffering

To configure read-ahead buffering for improved sequential read performance:

```go
options := absnfs.ExportOptions{
    // Enable read-ahead buffering
    EnableReadAhead: true,
    
    // Size of read-ahead buffer
    ReadAheadSize: 524288, // 512KB
    
    // Maximum number of files to buffer simultaneously
    ReadAheadMaxFiles: 100,
}
```

Read-ahead buffering is beneficial for:
- Sequential file access patterns
- Large file streaming
- Predictable access patterns

### Transfer Size

To configure the maximum transfer size for read and write operations:

```go
options := absnfs.ExportOptions{
    // Maximum transfer size in bytes
    TransferSize: 131072, // 128KB
}
```

Adjusting the transfer size can help:
- Optimize for your network characteristics
- Balance memory usage and throughput
- Accommodate client limitations

## Advanced Configuration

### Timeouts

To configure various timeout values:

```go
options := absnfs.ExportOptions{
    // How long to wait for idle connections before closing
    IdleTimeout: 5 * time.Minute,
}
```

Timeouts help manage:
- Resource usage for idle connections
- Recovery from stuck operations
- Cleanup of abandoned file handles

### Logging

ABSNFS provides production-ready structured logging with configurable output, format, and verbosity levels:

```go
options := absnfs.ExportOptions{
    Log: &absnfs.LogConfig{
        // Log level: "debug", "info", "warn", "error"
        Level: "info",

        // Output format: "json" or "text"
        Format: "json",

        // Output destination: "stdout", "stderr", or file path
        Output: "/var/log/nfs/server.log",

        // Optional: Log client IP addresses (default: false for privacy)
        LogClientIPs: true,

        // Optional: Log detailed NFS operations (default: false for performance)
        LogOperations: false,

        // Optional: Log file access patterns (default: false)
        LogFileAccess: true,

        // File rotation settings (when Output is a file path)
        MaxSize:    100,  // MB
        MaxBackups: 5,    // Number of old log files to keep
        MaxAge:     30,   // Days to retain old logs
        Compress:   true, // Compress rotated logs
    },
}
```

Logging helps with:
- Debugging NFS issues with detailed operation logs
- Monitoring access patterns for security auditing
- Integration with log aggregation systems (ELK, Splunk, CloudWatch)
- Performance analysis with timing information

For more details, see the [Logging API documentation](../api/logging.md).

## Configuration Example: High-Performance

Here's an example configuration optimized for high performance:

```go
options := absnfs.ExportOptions{
    // Performance optimizations
    AttrCacheTimeout: 30 * time.Second,
    EnableReadAhead: true,
    ReadAheadSize: 1048576, // 1MB
    TransferSize: 262144, // 256KB
    
    // Reduce security slightly for performance
    Secure: true,
    AllowedIPs: []string{"192.168.0.0/16"}, // Trust local network
}
```

## Configuration Example: High-Security

Here's an example configuration optimized for security:

```go
options := absnfs.ExportOptions{
    // Security settings
    ReadOnly: true,
    Secure: true,
    AllowedIPs: []string{"10.0.0.5", "10.0.0.6"}, // Only specific IPs
    Squash: "all", // Map all users to anonymous

    // Short timeouts for security
    IdleTimeout: 1 * time.Minute,
}
```

## Reading Configuration from Files

For production systems, you might want to read configuration from a file:

```go
package main

import (
    "encoding/json"
    "log"
    "os"

    "github.com/absfs/absnfs"
    "github.com/absfs/memfs"
)

func main() {
    // Read configuration from file
    configData, err := os.ReadFile("config.json")
    if err != nil {
        log.Fatalf("Error reading config file: %v", err)
    }
    
    // Parse configuration
    var options absnfs.ExportOptions
    if err := json.Unmarshal(configData, &options); err != nil {
        log.Fatalf("Error parsing config: %v", err)
    }
    
    // Create filesystem
    fs, err := memfs.NewFS()
    if err != nil {
        log.Fatalf("Error creating filesystem: %v", err)
    }
    
    // Create NFS server with loaded configuration
    nfsServer, err := absnfs.New(fs, options)
    if err != nil {
        log.Fatalf("Error creating NFS server: %v", err)
    }
    
    // Export the filesystem
    if err := nfsServer.Export("/export/memfs", 2049); err != nil {
        log.Fatalf("Error exporting filesystem: %v", err)
    }
    
    // Wait forever
    select {}
}
```

Example `config.json`:

```json
{
    "ReadOnly": false,
    "Secure": true,
    "AllowedIPs": ["192.168.1.0/24"],
    "Squash": "root",
    "AttrCacheTimeout": 10000000000,  // 10 seconds in nanoseconds
    "EnableReadAhead": true,
    "ReadAheadSize": 524288,
    "TransferSize": 131072
}
```

## Runtime Configuration Updates

ABSNFS supports updating most configuration options at runtime without requiring a server restart. This is useful for:
- Adapting to changing workload patterns
- Adjusting cache sizes based on memory pressure
- Enabling/disabling features for debugging
- Modifying access control lists

### Getting Current Configuration

To retrieve the current server configuration:

```go
// Get a copy of current configuration
currentOpts := nfsServer.GetExportOptions()

// Inspect configuration values
log.Printf("Current cache size: %d", currentOpts.AttrCacheSize)
log.Printf("Current worker count: %d", currentOpts.MaxWorkers)
log.Printf("Read-only mode: %v", currentOpts.ReadOnly)
```

The returned configuration is a deep copy, so modifying it won't affect the server's configuration.

### Updating Configuration at Runtime

To update configuration while the server is running:

```go
// Get current configuration
opts := nfsServer.GetExportOptions()

// Modify settings
opts.AttrCacheSize = 20000
opts.AttrCacheTimeout = 10 * time.Second
opts.MaxWorkers = 16
opts.ReadAheadMaxMemory = 200 * 1024 * 1024  // 200MB

// Apply the updates
if err := nfsServer.UpdateExportOptions(opts); err != nil {
    log.Printf("Failed to update configuration: %v", err)
}
```

### Fields That Can Be Updated

The following fields can be safely updated at runtime:

**Performance Settings:**
- `AttrCacheSize` - Attribute cache maximum entries
- `AttrCacheTimeout` - Attribute cache TTL
- `ReadAheadMaxMemory` - Read-ahead buffer memory limit
- `ReadAheadMaxFiles` - Read-ahead buffer file limit
- `MaxWorkers` - Worker pool size
- `BatchOperations` - Enable/disable batching
- `MaxBatchSize` - Maximum batch size

**Memory Management:**
- `MemoryHighWatermark` - High memory threshold
- `MemoryLowWatermark` - Low memory threshold

**Access Control:**
- `ReadOnly` - Enable/disable read-only mode
- `AllowedIPs` - Allowed client IP addresses
- `Async` - Asynchronous write mode

**Logging:**
- `Log` - Complete logging configuration

### Fields That Require Restart

Some fields cannot be changed at runtime and require a server restart:

- `Squash` - User mapping mode (affects all operations)
- `Port` - Network port (requires new listener)
- `TLS` - TLS configuration (requires new listener)

Attempting to change these fields will return an error:

```go
opts := nfsServer.GetExportOptions()
opts.Squash = "all"  // Was "root"

err := nfsServer.UpdateExportOptions(opts)
if err != nil {
    // Error: cannot change Squash mode at runtime (requires restart)
    log.Printf("Update failed: %v", err)
}
```

### Runtime Update Behavior

When you update configuration at runtime:

1. **Cache Resizing**: If you reduce cache sizes, LRU entries are automatically evicted
2. **Worker Pool Resizing**: The pool is gracefully stopped and restarted with the new size
3. **Memory Limits**: Read-ahead buffers are evicted if necessary to meet new limits
4. **Logger Updates**: The old logger is properly closed before initializing the new one
5. **Thread Safety**: All updates are atomic and thread-safe

### Example: Dynamic Cache Adjustment

Here's an example of adjusting cache settings based on workload:

```go
// Monitor metrics and adjust cache size
ticker := time.NewTicker(1 * time.Minute)
defer ticker.Stop()

for range ticker.C {
    metrics := nfsServer.GetMetrics()
    opts := nfsServer.GetExportOptions()

    // If cache hit rate is low, increase cache size
    hitRate := float64(metrics.AttrCacheHits) / float64(metrics.AttrCacheHits + metrics.AttrCacheMisses)

    if hitRate < 0.7 && opts.AttrCacheSize < 50000 {
        opts.AttrCacheSize = int(float64(opts.AttrCacheSize) * 1.5)
        if err := nfsServer.UpdateExportOptions(opts); err != nil {
            log.Printf("Failed to increase cache size: %v", err)
        } else {
            log.Printf("Increased cache size to %d (hit rate: %.2f%%)", opts.AttrCacheSize, hitRate*100)
        }
    }
}
```

### Example: Responding to Memory Pressure

Adjust configuration when system memory is under pressure:

```go
import "runtime"

func adjustForMemoryPressure(nfsServer *absnfs.AbsfsNFS) {
    var m runtime.MemStats
    runtime.ReadMemStats(&m)

    // If using more than 80% of allocated memory
    usagePercent := float64(m.Alloc) / float64(m.TotalAlloc)

    if usagePercent > 0.8 {
        opts := nfsServer.GetExportOptions()

        // Reduce cache sizes by 50%
        opts.AttrCacheSize = opts.AttrCacheSize / 2
        opts.ReadAheadMaxMemory = opts.ReadAheadMaxMemory / 2
        opts.ReadAheadMaxFiles = opts.ReadAheadMaxFiles / 2

        if err := nfsServer.UpdateExportOptions(opts); err != nil {
            log.Printf("Failed to reduce cache sizes: %v", err)
        } else {
            log.Printf("Reduced cache sizes due to memory pressure")
        }
    }
}
```

### Example: Hot-Reloading Configuration Files

Reload configuration from a file without restarting:

```go
import (
    "encoding/json"
    "os"
    "os/signal"
    "syscall"
)

func watchConfigFile(nfsServer *absnfs.AbsfsNFS, configPath string) {
    // Set up signal handler for SIGHUP (reload config)
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGHUP)

    for range sigChan {
        log.Println("Reloading configuration...")

        // Read new configuration
        configData, err := os.ReadFile(configPath)
        if err != nil {
            log.Printf("Error reading config file: %v", err)
            continue
        }

        // Parse configuration
        var newOpts absnfs.ExportOptions
        if err := json.Unmarshal(configData, &newOpts); err != nil {
            log.Printf("Error parsing config: %v", err)
            continue
        }

        // Apply new configuration
        if err := nfsServer.UpdateExportOptions(newOpts); err != nil {
            log.Printf("Error updating configuration: %v", err)
        } else {
            log.Println("Configuration reloaded successfully")
        }
    }
}

// Usage:
// go watchConfigFile(nfsServer, "config.json")
// ... then send SIGHUP to reload: kill -HUP <pid>
```

## Configuration Best Practices

1. **Start Simple**: Begin with default options and adjust as needed
2. **Test Thoroughly**: Test configuration changes in a non-production environment
3. **Monitor Performance**: Use monitoring to identify bottlenecks and optimize
4. **Security First**: Prioritize security settings over performance for sensitive data
5. **Document Your Configuration**: Maintain documentation of your configuration choices
6. **Regular Review**: Periodically review and update your configuration
7. **Use Runtime Updates**: Take advantage of runtime updates to avoid service interruptions
8. **Validate Before Applying**: Always check for errors when updating configuration
9. **Log Configuration Changes**: Track when and why configuration is changed

## Next Steps

Now that you understand how to configure ABSNFS, you may want to explore:

1. [Performance Tuning](./performance-tuning.md): Detailed performance optimization
2. [Security](./security.md): In-depth security configuration
3. [Monitoring](./monitoring.md): Monitoring ABSNFS performance and health