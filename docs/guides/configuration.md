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

ABSNFS uses standard Go logging. To configure logging, use Go's standard `log` package or integrate with your preferred logging framework.

Logging helps with:
- Debugging NFS issues
- Monitoring access patterns
- Security auditing

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