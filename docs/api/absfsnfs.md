---
layout: default
title: AbsfsNFS
---

# AbsfsNFS

The `AbsfsNFS` type is the core component of the ABSNFS package. It wraps an ABSFS-compatible filesystem and exposes it as an NFS server.

## Type Definition

```go
type AbsfsNFS struct {
    // contains filtered or unexported fields
}
```

`AbsfsNFS` is not intended to be created directly. Instead, use the [New](./new.md) function to create and configure an instance.

## Constructor

```go
func New(fs absfs.FileSystem, options ExportOptions) (*AbsfsNFS, error)
```

Creates a new NFS server adapter for the provided filesystem with the specified options.

## Methods

### Export

```go
func (nfs *AbsfsNFS) Export(mountPath string, port int) error
```

Exports the filesystem at the specified mount path and port. This makes the filesystem accessible to NFS clients.

### Unexport

```go
func (nfs *AbsfsNFS) Unexport() error
```

Stops exporting the filesystem and shuts down the NFS server.

### Close

```go
func (nfs *AbsfsNFS) Close() error
```

Closes the NFS server and releases all resources. This should be called when shutting down the server.

### GetMetrics

```go
func (nfs *AbsfsNFS) GetMetrics() NFSMetrics
```

Returns current performance and operational metrics for the NFS server, including operation counts, cache hit rates, and error statistics.

### IsHealthy

```go
func (nfs *AbsfsNFS) IsHealthy() bool
```

Performs a health check on the NFS server and returns true if the server is operating normally.

## Example Usage

```go
package main

import (
    "log"

    "github.com/absfs/absnfs"
    "github.com/absfs/memfs"
)

func main() {
    // Create any absfs filesystem
    fs, err := memfs.NewFS()
    if err != nil {
        log.Fatal(err)
    }

    // Create test content
    f, _ := fs.Create("/hello.txt")
    f.Write([]byte("Hello from NFS!"))
    f.Close()

    // Configure NFS export options
    options := absnfs.ExportOptions{
        ReadOnly: true,
        Secure: true,
        AllowedIPs: []string{"192.168.1.0/24"},
    }

    // Create NFS server
    server, err := absnfs.New(fs, options)
    if err != nil {
        log.Fatal(err)
    }

    // Export the filesystem
    if err := server.Export("/export/test", 2049); err != nil {
        log.Fatal(err)
    }

    log.Println("NFS server running. Press Ctrl+C to stop.")
    
    // Wait for shutdown signal
    // In a real application, you'd wait for an OS signal
    select {}
}
```

## Implementation Notes

The `AbsfsNFS` type maintains several internal components:

- File handle mapping for translating between NFS file handles and filesystem paths
- Attribute cache for improving performance of metadata operations
- Read-ahead buffer for optimizing read performance
- Root node representation of the filesystem hierarchy

These components work together to provide efficient NFS protocol handling while presenting a standard NFS interface to clients.