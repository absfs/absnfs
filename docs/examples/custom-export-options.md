---
layout: default
title: Using Custom Export Options
---

# Using Custom Export Options

This example demonstrates how to configure custom export options for your ABSNFS server to optimize for specific requirements. You'll learn how to fine-tune security, performance, caching, and other parameters.

## Complete Example

```go
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/absfs/absnfs"
	"github.com/absfs/memfs"
)

func main() {
	// Create an in-memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		log.Fatalf("Failed to create filesystem: %v", err)
	}

	// Create some test content
	createTestContent(fs)

	// Configure custom export options
	options := absnfs.ExportOptions{
		// Security options
		ReadOnly: false,                          // Allow writes (default)
		Secure: true,                             // Enable security features
		AllowedIPs: []string{                     // Restrict access by IP
			"127.0.0.1",                           // Local access
			"192.168.1.0/24",                      // Local network
		},
		Squash: "root",                           // Map root users to anonymous (default)
		
		// Performance options
		EnableReadAhead: true,                    // Enable read-ahead buffering
		ReadAheadSize: 524288,                    // 512KB read-ahead buffer
		TransferSize: 262144,                     // 256KB transfer size
		
		// Caching options
		AttrCacheTimeout: 15 * time.Second,       // Cache attributes for 15 seconds
		AttrCacheSize: 10000,                     // Cache up to 10000 entries
		
		// Connection options
		MaxConnections: 100,                      // Maximum simultaneous connections
		IdleTimeout: 5 * time.Minute,             // Close idle connections after 5 minutes
	}

	// Create NFS server with custom options
	server, err := absnfs.New(fs, options)
	if err != nil {
		log.Fatalf("Failed to create NFS server: %v", err)
	}

	// Set up graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Export the filesystem
	mountPath := "/export/custom"
	port := 2049

	fmt.Printf("Starting NFS server on port %d...\n", port)
	if err := server.Export(mountPath, port); err != nil {
		log.Fatalf("Failed to export filesystem: %v", err)
	}

	// Display configuration summary
	fmt.Println("\nNFS server configuration:")
	fmt.Printf("  Mount path: %s\n", mountPath)
	fmt.Printf("  Port: %d\n", port)
	fmt.Printf("  Read-only: %v\n", options.ReadOnly)
	fmt.Printf("  Security: %v\n", options.Secure)
	fmt.Printf("  Allowed IPs: %v\n", options.AllowedIPs)
	fmt.Printf("  User mapping: %s\n", options.Squash)
	fmt.Printf("  Read-ahead: %v (%d bytes)\n", options.EnableReadAhead, options.ReadAheadSize)
	fmt.Printf("  Transfer size: %d bytes\n", options.TransferSize)
	fmt.Printf("  Attribute cache timeout: %v\n", options.AttrCacheTimeout)
	
	// Display mount commands
	fmt.Println("\nMount commands:")
	fmt.Println("  Linux:   sudo mount -t nfs localhost:/export/custom /mnt/nfs")
	fmt.Println("  macOS:   sudo mount -t nfs -o resvport localhost:/export/custom /mnt/nfs")
	fmt.Println("  Windows: mount -o anon \\\\localhost\\export\\custom Z:")
	
	fmt.Println("\nPress Ctrl+C to stop the server")

	// Wait for shutdown signal
	<-sigChan
	fmt.Println("\nShutting down NFS server...")
	
	if err := server.Unexport(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
	
	fmt.Println("NFS server stopped")
}

// Helper function to create test content
func createTestContent(fs absfs.FileSystem) {
	// Create directories
	dirs := []string{
		"/docs",
		"/images",
		"/data",
		"/config",
	}
	
	for _, dir := range dirs {
		err := fs.Mkdir(dir, 0755)
		if err != nil {
			log.Printf("Warning: couldn't create directory %s: %v", dir, err)
		}
	}
	
	// Create README.txt
	readme, err := fs.Create("/README.txt")
	if err != nil {
		log.Printf("Warning: couldn't create README: %v", err)
		return
	}
	
	readmeContent := `Welcome to the Custom Options Example

This NFS server has been configured with custom export options
to demonstrate how to optimize ABSNFS for specific requirements.

Key configurations:
- Security features enabled
- IP-based access control
- Read-ahead buffering for better sequential read performance
- Attribute caching for improved metadata operations
- Connection limits and timeouts for better resource management

Created: ` + time.Now().Format(time.RFC1123)
	
	_, err = readme.Write([]byte(readmeContent))
	if err != nil {
		log.Printf("Warning: couldn't write README content: %v", err)
	}
	readme.Close()
	
	// Create a configuration sample file
	configFile, err := fs.Create("/config/sample.conf")
	if err != nil {
		log.Printf("Warning: couldn't create config file: %v", err)
		return
	}
	
	configContent := `# Sample configuration file
server_name = nfs-example
log_level = info
data_dir = /data
max_connections = 100
read_timeout = 30s
write_timeout = 30s
enable_cache = true
cache_size = 256MB
`
	
	_, err = configFile.Write([]byte(configContent))
	if err != nil {
		log.Printf("Warning: couldn't write config content: %v", err)
	}
	configFile.Close()
	
	// Create a large file for read-ahead demonstration
	largeFile, err := fs.Create("/data/large_file.bin")
	if err != nil {
		log.Printf("Warning: couldn't create large file: %v", err)
		return
	}
	
	// Create 1MB of repeating data
	pattern := []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	buffer := make([]byte, 1024*1024) // 1MB
	for i := 0; i < len(buffer); i += len(pattern) {
		copy(buffer[i:min(i+len(pattern), len(buffer))], pattern)
	}
	
	_, err = largeFile.Write(buffer)
	if err != nil {
		log.Printf("Warning: couldn't write large file content: %v", err)
	}
	largeFile.Close()
}

// Helper function for Go versions before 1.21
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
```

## Key Components

Let's break down the key components of this example:

### Security Options

```go
// Security options
ReadOnly: false,                          // Allow writes (default)
Secure: true,                             // Enable security features
AllowedIPs: []string{                     // Restrict access by IP
    "127.0.0.1",                           // Local access
    "192.168.1.0/24",                      // Local network
},
Squash: "root",                           // Map root users to anonymous (default)
```

These options control the security of your NFS server:

- **ReadOnly**: When `true`, clients can't modify files
- **Secure**: Enables path validation and other security checks
- **AllowedIPs**: Restricts access to specific IP addresses or ranges
- **Squash**: Controls how client user identities are mapped to server users

### Performance Options

```go
// Performance options
EnableReadAhead: true,                    // Enable read-ahead buffering
ReadAheadSize: 524288,                    // 512KB read-ahead buffer
TransferSize: 262144,                     // 256KB transfer size
```

These options optimize performance:

- **EnableReadAhead**: Prefetches data for sequential reads
- **ReadAheadSize**: How much data to prefetch
- **TransferSize**: Maximum size of individual read/write operations

### Caching Options

```go
// Caching options
AttrCacheTimeout: 15 * time.Second,       // Cache attributes for 15 seconds
AttrCacheSize: 10000,                     // Cache up to 10000 entries
```

These options control attribute caching:

- **AttrCacheTimeout**: How long to cache file attributes
- **AttrCacheSize**: Maximum number of cached attributes

### Connection Options

```go
// Connection options
MaxConnections: 100,                      // Maximum simultaneous connections
IdleTimeout: 5 * time.Minute,             // Close idle connections after 5 minutes
```

These options manage client connections:

- **MaxConnections**: Limits the number of simultaneous clients
- **IdleTimeout**: Closes inactive connections to free resources


## Using the Configuration

To use this custom configuration, compile and run the example:

```bash
go run custom_export_options.go
```

You'll see output showing the server's configuration and mount instructions.

## Testing Different Options

### Testing Read-Ahead Performance

To test how read-ahead affects performance:

1. Mount the NFS share
2. Test sequential read performance with a large file:

```bash
# With read-ahead enabled
time dd if=/mnt/nfs/data/large_file.bin of=/dev/null bs=4k

# Change the configuration
options.EnableReadAhead = false
# or
options.ReadAheadSize = 65536 // 64KB

# Test again after remounting
time dd if=/mnt/nfs/data/large_file.bin of=/dev/null bs=4k
```

### Testing Attribute Caching

To test attribute caching:

1. Mount the NFS share
2. Use `stat` to check file attributes multiple times:

```bash
# First call will go to the server
time stat /mnt/nfs/README.txt

# Subsequent calls within the cache timeout will use cached data
time stat /mnt/nfs/README.txt
time stat /mnt/nfs/README.txt

# Wait longer than the cache timeout
sleep 16

# This call will go to the server again
time stat /mnt/nfs/README.txt
```

### Testing IP Restrictions

To test IP-based access control:

1. Set `AllowedIPs` to include only specific addresses
2. Try to mount from an allowed IP address (should succeed)
3. Try to mount from a disallowed IP address (should fail)

## Optimizing for Different Scenarios

### High-Performance Configuration

For workloads requiring maximum performance:

```go
options := absnfs.ExportOptions{
    // Large read-ahead for sequential access
    EnableReadAhead: true,
    ReadAheadSize: 4194304, // 4MB
    
    // Large transfer size
    TransferSize: 1048576, // 1MB
    
    // Aggressive caching
    AttrCacheTimeout: 60 * time.Second,
    AttrCacheSize: 100000,
    
    // Minimal security (for trusted environments only)
    Secure: true,
    AllowedIPs: []string{"192.168.0.0/16"}, // Trust local network
    
    // Many connections
    MaxConnections: 500,
    IdleTimeout: 10 * time.Minute,
}
```

### Secure Configuration

For security-sensitive workloads:

```go
options := absnfs.ExportOptions{
    // Restrict write access
    ReadOnly: true,
    
    // Maximum security
    Secure: true,
    AllowedIPs: []string{"10.0.0.5", "10.0.0.6"}, // Only specific IPs
    Squash: "all", // Map all users to anonymous
    
    // Conservative caching
    AttrCacheTimeout: 5 * time.Second,
    
    // Limited connections
    MaxConnections: 20,
    IdleTimeout: 2 * time.Minute,
}
```

### Memory-Constrained Environment

For systems with limited memory:

```go
options := absnfs.ExportOptions{
    // Minimal read-ahead
    EnableReadAhead: true,
    ReadAheadSize: 65536, // 64KB
    
    // Smaller transfer size
    TransferSize: 65536, // 64KB
    
    // Limited caching
    AttrCacheTimeout: 10 * time.Second,
    AttrCacheSize: 1000,
    
    // Fewer connections
    MaxConnections: 20,
    IdleTimeout: 3 * time.Minute,
}
```

### Multi-User Collaborative Environment

For environments where many users need to see each other's changes quickly:

```go
options := absnfs.ExportOptions{
    // Shorter cache timeouts for better consistency
    AttrCacheTimeout: 3 * time.Second,
    
    // User mapping that preserves identities
    Squash: "none",
    
    // More connections for multiple users
    MaxConnections: 200,
    
    // Performance settings for typical office documents
    EnableReadAhead: true,
    ReadAheadSize: 262144, // 256KB
    TransferSize: 131072, // 128KB
}
```

## Dynamic Configuration Updates

You can update some export options dynamically without restarting the server:

```go
// Get current options
currentOptions := server.GetExportOptions()

// Update options
currentOptions.ReadOnly = true // Switch to read-only mode

// Apply updates
if err := server.UpdateExportOptions(currentOptions); err != nil {
    log.Printf("Failed to update options: %v", err)
}
```

Note that some options may require clients to reconnect to take effect.

## Monitoring Configuration Effects

To understand how your configuration affects performance, you can add monitoring:

```go
// Start a goroutine to periodically log stats
go func() {
    ticker := time.NewTicker(1 * time.Minute)
    defer ticker.Stop()
    
    for range ticker.C {
        // Get server metrics
        metrics := server.GetMetrics()
        
        // Log key metrics
        log.Printf("Cache hit rate: %.2f%%", metrics.CacheHitRate * 100)
        log.Printf("Read operations: %d", metrics.ReadOperations)
        log.Printf("Write operations: %d", metrics.WriteOperations)
        log.Printf("Active connections: %d", metrics.ActiveConnections)
    }
}()
```

## Best Practices

When configuring export options, follow these best practices:

1. **Start Conservative**: Begin with modest settings and tune as needed
2. **Benchmark Changes**: Measure performance before and after changes
3. **Consider Workload**: Optimize for your specific access patterns
4. **Balance Security and Performance**: More security often means less performance
5. **Monitor Resource Usage**: Watch memory, CPU, and network utilization
6. **Document Your Configuration**: Keep records of what works well for your use case
7. **Test with Real Workloads**: Synthetic benchmarks may not reflect real-world performance

## Conclusion

Custom export options allow you to fine-tune ABSNFS to meet your specific requirements. By understanding the available options and their effects, you can optimize for security, performance, resource usage, or a balance of all three.

This example demonstrated:
- How to configure comprehensive export options
- The effect of different options on behavior and performance
- How to optimize for different scenarios
- How to update configuration dynamically

With these tools, you can create an NFS server that's perfectly tailored to your needs.

## Next Steps

- [OS Filesystem Export](./os-filesystem-export.md): Export a directory from your local filesystem
- [Multi-Export Server](./multi-export-server.md): Export multiple filesystems from one server
- [High-Performance Configuration](./high-performance.md): Advanced performance optimization techniques