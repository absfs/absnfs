---
layout: default
title: Exporting Filesystems
---

# Exporting Filesystems

This guide covers how to export different types of filesystems using ABSNFS, from simple in-memory filesystems to complex custom implementations.

## Understanding Filesystem Exports

In ABSNFS, "exporting" means making a filesystem accessible to NFS clients over the network. The process involves:

1. Creating or obtaining a filesystem that implements the ABSFS interface
2. Creating an NFS server with appropriate options
3. Exporting the filesystem at a specific mount path and port
4. Maintaining the server while clients access the filesystem

## Basic Export Process

The basic process for exporting any filesystem is:

```go
// Create or obtain an ABSFS-compatible filesystem
fs := /* filesystem implementation */

// Create the NFS server with options
options := absnfs.ExportOptions{
    // Configuration options...
}
nfsServer, err := absnfs.New(fs, options)
if err != nil {
    log.Fatal(err)
}

// Export the filesystem
mountPath := "/export/myfs"
port := 2049
if err := nfsServer.Export(mountPath, port); err != nil {
    log.Fatal(err)
}

// Keep the server running until shutdown
// ...

// When shutting down, unexport the filesystem
if err := nfsServer.Unexport(); err != nil {
    log.Printf("Error during shutdown: %v", err)
}
```

## Exporting Different Filesystem Types

### In-Memory Filesystem (memfs)

The simplest filesystem to export is an in-memory filesystem. This is useful for temporary data, testing, or cases where persistence isn't needed:

```go
package main

import (
    "log"
    "os/signal"
    "syscall"

    "github.com/absfs/absnfs"
    "github.com/absfs/memfs"
)

func main() {
    // Create an in-memory filesystem
    fs, err := memfs.NewFS()
    if err != nil {
        log.Fatal(err)
    }

    // Create some test content
    f, err := fs.Create("/hello.txt")
    if err != nil {
        log.Fatal(err)
    }
    f.Write([]byte("Hello from NFS!"))
    f.Close()

    // Create NFS server with default options
    nfsServer, err := absnfs.New(fs, absnfs.ExportOptions{})
    if err != nil {
        log.Fatal(err)
    }

    // Export the filesystem
    if err := nfsServer.Export("/export/memfs", 2049); err != nil {
        log.Fatal(err)
    }
    
    log.Println("NFS server running. Press Ctrl+C to stop.")

    // Wait for shutdown signal
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    <-sigChan
    
    // Unexport when shutting down
    if err := nfsServer.Unexport(); err != nil {
        log.Printf("Error during shutdown: %v", err)
    }
}
```

### Operating System Filesystem (osfs)

To export a directory from your local filesystem:

```go
package main

import (
    "log"
    "os/signal"
    "syscall"

    "github.com/absfs/absnfs"
    "github.com/absfs/osfs"
)

func main() {
    // Create an OS filesystem rooted at a specific directory
    fs, err := osfs.NewFS("/path/to/export")
    if err != nil {
        log.Fatal(err)
    }

    // Create NFS server
    nfsServer, err := absnfs.New(fs, absnfs.ExportOptions{})
    if err != nil {
        log.Fatal(err)
    }

    // Export the filesystem
    if err := nfsServer.Export("/export/local", 2049); err != nil {
        log.Fatal(err)
    }
    
    log.Println("NFS server running. Press Ctrl+C to stop.")

    // Wait for shutdown signal
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    <-sigChan
    
    // Unexport when shutting down
    if err := nfsServer.Unexport(); err != nil {
        log.Printf("Error during shutdown: %v", err)
    }
}
```

### Custom Filesystem Implementation

To export a custom filesystem, implement the ABSFS interface:

```go
package main

import (
    "log"
    "os/signal"
    "syscall"

    "github.com/absfs/absfs"
    "github.com/absfs/absnfs"
)

// MyFS is a custom filesystem implementation
type MyFS struct {
    // Your implementation details...
}

// Implement all required ABSFS interface methods:
// Open, Create, Mkdir, MkdirAll, OpenFile, Remove, RemoveAll, 
// Rename, Stat, Lstat, Chmod, Chtimes, etc.

func main() {
    // Create your custom filesystem
    fs := &MyFS{
        // Initialize your filesystem...
    }

    // Create NFS server
    nfsServer, err := absnfs.New(fs, absnfs.ExportOptions{})
    if err != nil {
        log.Fatal(err)
    }

    // Export the filesystem
    if err := nfsServer.Export("/export/custom", 2049); err != nil {
        log.Fatal(err)
    }
    
    log.Println("NFS server running. Press Ctrl+C to stop.")

    // Wait for shutdown signal
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    <-sigChan
    
    // Unexport when shutting down
    if err := nfsServer.Unexport(); err != nil {
        log.Printf("Error during shutdown: %v", err)
    }
}
```

### Layered/Composite Filesystems

You can also create and export composite filesystems by combining multiple filesystems:

```go
package main

import (
    "log"
    "os/signal"
    "syscall"

    "github.com/absfs/absnfs"
    "github.com/absfs/memfs"
    "github.com/absfs/osfs"
    "github.com/absfs/unionfs"  // Hypothetical union filesystem
)

func main() {
    // Create a memory filesystem for temporary files
    tempFS, err := memfs.NewFS()
    if err != nil {
        log.Fatal(err)
    }

    // Create an OS filesystem for persistent files
    dataFS, err := osfs.NewFS("/path/to/data")
    if err != nil {
        log.Fatal(err)
    }

    // Create a union filesystem that combines them
    // (Write to tempFS, read from both with tempFS taking precedence)
    fs, err := unionfs.New(tempFS, dataFS)
    if err != nil {
        log.Fatal(err)
    }

    // Create NFS server
    nfsServer, err := absnfs.New(fs, absnfs.ExportOptions{})
    if err != nil {
        log.Fatal(err)
    }

    // Export the filesystem
    if err := nfsServer.Export("/export/union", 2049); err != nil {
        log.Fatal(err)
    }
    
    log.Println("NFS server running. Press Ctrl+C to stop.")

    // Wait for shutdown signal
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    <-sigChan
    
    // Unexport when shutting down
    if err := nfsServer.Unexport(); err != nil {
        log.Printf("Error during shutdown: %v", err)
    }
}
```

## Export Options

When exporting filesystems, you can configure various options to control behavior:

### Read-Only Export

To export a filesystem in read-only mode:

```go
options := absnfs.ExportOptions{
    ReadOnly: true,
}
nfsServer, err := absnfs.New(fs, options)
```

### IP Restrictions

To restrict access to specific IP addresses or ranges:

```go
options := absnfs.ExportOptions{
    Secure: true,
    AllowedIPs: []string{"192.168.1.0/24", "10.0.0.5"},
}
nfsServer, err := absnfs.New(fs, options)
```

### Performance Optimization

To optimize for performance:

```go
options := absnfs.ExportOptions{
    // Enable read-ahead for better sequential performance
    EnableReadAhead: true,
    ReadAheadSize: 524288, // 512KB
    
    // Increase transfer size
    TransferSize: 262144, // 256KB
    
    // Longer attribute caching
    AttrCacheTimeout: 30 * time.Second,
}
nfsServer, err := absnfs.New(fs, options)
```

## Mount Path and Port Selection

When exporting a filesystem, you specify a mount path and port:

```go
nfsServer.Export("/export/myfs", 2049)
```

- **Mount Path**: The path that clients will use when mounting the filesystem (e.g., `server:/export/myfs`)
- **Port**: The TCP/UDP port on which the NFS server will listen (2049 is the standard NFS port)

You can export the same filesystem at multiple mount points:

```go
// Export the same filesystem at different mount points
nfsServer.Export("/export/myfs", 2049)
nfsServer.Export("/export/myfs2", 2049)
```

And you can use non-standard ports (useful when running without root privileges):

```go
// Export on a non-standard port (doesn't require root)
nfsServer.Export("/export/myfs", 8049)
```

## Client Mounting

Once a filesystem is exported, clients can mount it using standard NFS commands:

### Linux
```bash
mount -t nfs server:/export/myfs /mnt/nfs
```

### macOS
```bash
mount -t nfs -o resvport server:/export/myfs /mnt/nfs
```

### Windows
```
mount -o anon \\server\export\myfs Z:
```

For non-standard ports:

```bash
# Linux/macOS
mount -t nfs server:8049:/export/myfs /mnt/nfs

# Windows
mount -o anon \\server@8049\export\myfs Z:
```

## Best Practices

When exporting filesystems, follow these best practices:

1. **Use Meaningful Mount Paths**: Choose mount paths that clearly indicate the content or purpose of the filesystem
2. **Consider Security**: Use read-only mode for reference data and restrict access by IP where appropriate
3. **Monitor Performance**: Keep an eye on performance metrics to identify bottlenecks
4. **Implement Proper Shutdown**: Always unexport filesystems cleanly when shutting down
5. **Test Client Compatibility**: Test with different NFS clients to ensure compatibility
6. **Document Exports**: Maintain documentation of your exports for users and administrators

## Troubleshooting

### Export Failures

If exporting fails with "permission denied," either:
- Run the program with root privileges
- Use a non-standard port (above 1024)

### Client Mount Issues

If clients can't mount, check:
- The server is running and listening on the specified port
- Firewall rules allow NFS traffic
- The client is using the correct mount syntax
- Network connectivity between client and server

### Performance Issues

If performance is poor:
- Increase read-ahead buffer size for sequential access
- Increase attribute cache timeout
- Increase transfer size
- Check network performance between client and server

## Next Steps

Now that you understand how to export filesystems, you may want to explore:

1. [Managing Exports](./managing-exports.md): How to manage multiple exports
2. [Security](./security.md): Securing your NFS exports
3. [Performance Tuning](./performance-tuning.md): Optimizing performance
4. [Client Compatibility](./client-compatibility.md): Ensuring compatibility with different clients