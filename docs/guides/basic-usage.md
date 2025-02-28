---
layout: default
title: Basic Usage
---

# Basic Usage

This guide covers the basic usage patterns for ABSNFS, helping you get started with exporting filesystems via NFS.

## Core Workflow

The typical workflow for using ABSNFS consists of three main steps:

1. Create or obtain an ABSFS-compatible filesystem
2. Create an NFS server with appropriate options
3. Export the filesystem to make it accessible to clients

## Creating a Simple NFS Server

Here's a complete example of creating and running an NFS server with an in-memory filesystem:

```go
package main

import (
    "log"
    "os"
    "os/signal"
    "syscall"

    "github.com/absfs/absnfs"
    "github.com/absfs/memfs"
)

func main() {
    // Step 1: Create an ABSFS-compatible filesystem
    fs, err := memfs.NewFS()
    if err != nil {
        log.Fatalf("Failed to create filesystem: %v", err)
    }

    // Create some content
    if err := createTestContent(fs); err != nil {
        log.Fatalf("Failed to create test content: %v", err)
    }

    // Step 2: Create the NFS server
    nfsServer, err := absnfs.New(fs, absnfs.ExportOptions{})
    if err != nil {
        log.Fatalf("Failed to create NFS server: %v", err)
    }

    // Step 3: Export the filesystem
    mountPath := "/export/memfs"
    port := 2049
    
    log.Printf("Exporting filesystem at %s on port %d", mountPath, port)
    if err := nfsServer.Export(mountPath, port); err != nil {
        log.Fatalf("Failed to export filesystem: %v", err)
    }
    
    log.Printf("NFS server running. Mount with: mount -t nfs localhost:%s /mnt/nfs", mountPath)

    // Wait for shutdown signal
    sigs := make(chan os.Signal, 1)
    signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
    
    <-sigs
    log.Println("Shutting down NFS server...")
    
    // Unexport and clean up
    if err := nfsServer.Unexport(); err != nil {
        log.Printf("Error during shutdown: %v", err)
    }
    
    log.Println("NFS server stopped")
}

// Helper function to create test content
func createTestContent(fs absfs.FileSystem) error {
    // Create a directory
    if err := fs.Mkdir("/testdir", 0755); err != nil {
        return err
    }
    
    // Create a file
    f, err := fs.Create("/testdir/hello.txt")
    if err != nil {
        return err
    }
    defer f.Close()
    
    // Write some content
    if _, err := f.Write([]byte("Hello from NFS!")); err != nil {
        return err
    }
    
    return nil
}
```

## Using with Different Filesystems

ABSNFS works with any filesystem that implements the ABSFS interface. Here are a few examples:

### In-Memory Filesystem (memfs)

```go
import "github.com/absfs/memfs"

fs, err := memfs.NewFS()
if err != nil {
    log.Fatal(err)
}

nfsServer, err := absnfs.New(fs, absnfs.ExportOptions{})
```

### OS Filesystem

```go
import "github.com/absfs/osfs"

fs, err := osfs.NewFS("/path/to/directory")
if err != nil {
    log.Fatal(err)
}

nfsServer, err := absnfs.New(fs, absnfs.ExportOptions{})
```

### Custom Filesystems

You can also create custom filesystems by implementing the ABSFS interface:

```go
import "github.com/absfs/absfs"

// MyFS implements absfs.FileSystem
type MyFS struct {
    // implementation details
}

// Implement all required methods...

// Then use it with ABSNFS
fs := &MyFS{}
nfsServer, err := absnfs.New(fs, absnfs.ExportOptions{})
```

## Configuring Export Options

ABSNFS provides various options to customize the behavior of the NFS server:

```go
options := absnfs.ExportOptions{
    // Set to true for read-only access
    ReadOnly: false,
    
    // Security features
    Secure: true,
    AllowedIPs: []string{"192.168.1.0/24"},
    
    // User mapping
    Squash: "root", // Options: "none", "root", "all"
    
    // Performance tuning
    EnableReadAhead: true,
    ReadAheadSize: 524288, // 512KB
    AttrCacheTimeout: 10 * time.Second,
}

nfsServer, err := absnfs.New(fs, options)
```

## Mounting the NFS Export

Once your NFS server is running, clients can mount the exported filesystem using standard NFS client tools:

### Linux

```bash
# Create a mount point
sudo mkdir -p /mnt/nfs

# Mount the NFS export
sudo mount -t nfs server_ip:/export/memfs /mnt/nfs
```

### macOS

```bash
# Create a mount point
sudo mkdir -p /mnt/nfs

# Mount the NFS export
sudo mount -t nfs -o resvport server_ip:/export/memfs /mnt/nfs
```

### Windows

Windows users can mount NFS exports using the "Map Network Drive" feature or the command line:

```
mount -o anon \\server_ip\export\memfs Z:
```

## Error Handling

ABSNFS translates filesystem errors to appropriate NFS error codes. When implementing your own filesystem, ensure that you return standard Go error types, and ABSNFS will map them to the appropriate NFS errors.

```go
if _, err := fs.Stat("/nonexistent"); os.IsNotExist(err) {
    // This will be translated to NFS3ERR_NOENT
}
```

## Next Steps

Now that you understand the basics of using ABSNFS, you might want to explore:

- [Configuration options](./configuration.md) for more advanced setups
- [Security considerations](./security.md) for securing your NFS server
- [Performance tuning](./performance-tuning.md) for optimizing performance