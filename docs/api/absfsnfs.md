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

### Close

```go
func (nfs *AbsfsNFS) Close() error
```

Releases resources and stops any background processes including memory monitoring, worker pools, and batch processors. This should be called when the NFS adapter is no longer needed.

### ExecuteWithWorker

```go
func (nfs *AbsfsNFS) ExecuteWithWorker(task func() interface{}) interface{}
```

Runs a task in the worker pool. If the worker pool is not available (disabled or full), it executes the task directly. This method is used internally for concurrent operation handling.

### GetExportOptions

```go
func (nfs *AbsfsNFS) GetExportOptions() ExportOptions
```

Returns a thread-safe copy of the current export options. The returned copy is independent and modifications to it will not affect the server's configuration. This is useful for inspecting the current server configuration.

**Example:**

```go
opts := server.GetExportOptions()
log.Printf("ReadOnly: %v, AttrCacheSize: %d", opts.ReadOnly, opts.AttrCacheSize)
```

### GetMetrics

```go
func (nfs *AbsfsNFS) GetMetrics() NFSMetrics
```

Returns a snapshot of the current NFS server metrics including operation counts, latency statistics, cache metrics, connection metrics, and error counts.

### IsHealthy

```go
func (nfs *AbsfsNFS) IsHealthy() bool
```

Returns whether the server is in a healthy state based on error rates and latency metrics. The server is considered unhealthy if the error rate exceeds 50% or if the 95th percentile read/write latency exceeds 5 seconds.

### UpdateExportOptions

```go
func (nfs *AbsfsNFS) UpdateExportOptions(newOptions ExportOptions) error
```

Updates the server's export options at runtime without requiring a restart. This method is thread-safe and can be called while the server is actively serving requests.

**Safe to update at runtime:**
- `ReadOnly` - Export read-only mode
- `Async` - Asynchronous write mode
- `AllowedIPs` - List of allowed client IPs/subnets
- `AttrCacheSize` - Maximum number of cached attributes
- `AttrCacheTimeout` - Time-to-live for cached attributes
- `ReadAheadMaxMemory` - Maximum memory for read-ahead buffers
- `ReadAheadMaxFiles` - Maximum number of read-ahead file buffers
- `MemoryHighWatermark` - Memory pressure high threshold
- `MemoryLowWatermark` - Memory pressure low threshold
- `MaxWorkers` - Number of worker goroutines
- `BatchOperations` - Enable/disable operation batching
- `MaxBatchSize` - Maximum batch size
- `Log` - Logging configuration

**Requires restart (returns error if changed):**
- `Squash` - User mapping mode (affects all operations)
- `Port` - Network port (requires new listener)
- `TLS` - TLS configuration (requires new listener)

**Example:**

```go
// Update cache settings at runtime
newOpts := server.GetExportOptions()
newOpts.AttrCacheSize = 20000
newOpts.AttrCacheTimeout = 10 * time.Second
newOpts.MaxWorkers = 16

if err := server.UpdateExportOptions(newOpts); err != nil {
    log.Printf("Failed to update options: %v", err)
}
```

**Notes:**
- When updating cache sizes, LRU entries are automatically evicted if the new size is smaller than current usage
- When updating worker pool size, the pool is gracefully restarted
- When updating logging configuration, the old logger is properly closed before the new one is initialized
- All updates are atomic and thread-safe

## Example Usage

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

    // Create NFS adapter
    nfs, err := absnfs.New(fs, options)
    if err != nil {
        log.Fatal(err)
    }
    defer nfs.Close()

    // Create and configure server
    server, err := absnfs.NewServer(absnfs.ServerOptions{
        Port: 2049,
        ReadOnly: true,
    })
    if err != nil {
        log.Fatal(err)
    }

    server.SetHandler(nfs)

    // Start the server
    if err := server.Listen(); err != nil {
        log.Fatal(err)
    }

    log.Println("NFS server running. Press Ctrl+C to stop.")

    // Wait for shutdown signal
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
    <-sigChan

    log.Println("Shutting down...")
}
```

## Architecture Notes

The `AbsfsNFS` type is an adapter that wraps an ABSFS filesystem and provides NFS protocol operations. It is separate from the `Server` type which handles network connections and protocol handling.

### Key Components

The `AbsfsNFS` adapter maintains several internal components:

- **File handle mapping**: Translates between NFS file handles and filesystem paths
- **Attribute cache**: Improves performance of metadata operations with configurable timeout and size
- **Read-ahead buffer**: Optimizes sequential read performance with prefetching
- **Worker pool**: Handles concurrent operations using a configurable number of goroutines
- **Batch processor**: Groups similar operations together for improved throughput
- **Memory monitor**: Optionally monitors system memory and adjusts cache sizes under pressure
- **Rate limiter**: Provides DoS protection with per-IP and global request limits
- **Metrics collector**: Tracks operations, latency, cache performance, and errors

### Usage Pattern

1. Create an ABSFS filesystem implementation
2. Create an `AbsfsNFS` adapter with `New()`, passing the filesystem and export options
3. Create a `Server` with `NewServer()` and configure it
4. Set the adapter as the server's handler with `SetHandler()`
5. Start the server with `Listen()`
6. Clean up resources with `Close()` when done