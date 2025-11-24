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

### Connection Timeouts

To configure connection idle timeouts:

```go
options := absnfs.ExportOptions{
    // How long to wait for idle connections before closing
    IdleTimeout: 5 * time.Minute,
}
```

Connection timeouts help manage:
- Resource usage for idle connections
- Cleanup of abandoned connections

### Operation Timeouts

To configure operation-specific timeouts, use the `Timeouts` configuration:

```go
options := absnfs.ExportOptions{
    Timeouts: &absnfs.TimeoutConfig{
        // Read operations timeout
        ReadTimeout: 30 * time.Second,

        // Write operations timeout (longer for disk I/O)
        WriteTimeout: 60 * time.Second,

        // Path lookup operations timeout
        LookupTimeout: 10 * time.Second,

        // Directory listing operations timeout
        ReaddirTimeout: 30 * time.Second,

        // File creation operations timeout
        CreateTimeout: 15 * time.Second,

        // File deletion operations timeout
        RemoveTimeout: 15 * time.Second,

        // File rename operations timeout
        RenameTimeout: 20 * time.Second,

        // File handle operations timeout
        HandleTimeout: 5 * time.Second,

        // Default timeout for operations without specific timeout
        DefaultTimeout: 30 * time.Second,
    },
}
```

Operation timeouts help:
- Prevent operations from hanging indefinitely on slow filesystems
- Protect against misbehaving clients
- Free up resources from stuck operations
- Provide consistent response times to clients

When an operation times out, the server returns `NFSERR_DELAY` to the client, signaling that the server is temporarily busy and the client should retry the operation. Timeout metrics are tracked and can be monitored through the server's metrics API.

**Best Practices:**
- Set write timeouts longer than read timeouts due to disk I/O overhead
- Adjust timeouts based on your filesystem's performance characteristics
- For network-backed filesystems (like S3), use longer timeouts
- For local SSD/NVMe storage, shorter timeouts are appropriate
- Monitor timeout metrics to tune these values for your workload

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

## Configuration Best Practices

1. **Start Simple**: Begin with default options and adjust as needed
2. **Test Thoroughly**: Test configuration changes in a non-production environment
3. **Monitor Performance**: Use monitoring to identify bottlenecks and optimize
4. **Security First**: Prioritize security settings over performance for sensitive data
5. **Document Your Configuration**: Maintain documentation of your configuration choices
6. **Regular Review**: Periodically review and update your configuration

## Next Steps

Now that you understand how to configure ABSNFS, you may want to explore:

1. [Performance Tuning](./performance-tuning.md): Detailed performance optimization
2. [Security](./security.md): In-depth security configuration
3. [Monitoring](./monitoring.md): Monitoring ABSNFS performance and health