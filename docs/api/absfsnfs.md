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

### SetLogger

```go
func (nfs *AbsfsNFS) SetLogger(logger Logger) error
```

Sets the logger for the NFS server. When `logger` is `nil`, a no-op logger is used (logging disabled). This method can be called at any time to change the logging configuration, including after the server has started.

Returns an error if the server instance is nil.

**Example:**

```go
// Create a custom logger
logger := NewDefaultLogger(LogLevelInfo, "json", os.Stdout)

// Set the logger on the server
if err := nfsServer.SetLogger(logger); err != nil {
    log.Fatalf("Failed to set logger: %v", err)
}

// To disable logging
if err := nfsServer.SetLogger(nil); err != nil {
    log.Fatalf("Failed to disable logging: %v", err)
}
```

### GetExportOptions

```go
func (nfs *AbsfsNFS) GetExportOptions() ExportOptions
```

Returns a deep copy of the current export options. The returned copy can be safely modified without affecting the server's configuration. Use `UpdateExportOptions` to apply changes.

**Example:**

```go
// Get current options
opts := nfsServer.GetExportOptions()

// Check current settings
fmt.Printf("ReadOnly: %v\n", opts.ReadOnly)
fmt.Printf("MaxConnections: %d\n", opts.MaxConnections)
```

### UpdateExportOptions

```go
func (nfs *AbsfsNFS) UpdateExportOptions(newOptions ExportOptions) error
```

Updates the export options for the running server. This is a convenience method that internally splits the provided options into tuning changes and policy changes:

- **Tuning changes** (cache sizes, worker counts, timeouts, etc.) are applied immediately via lock-free atomic swap
- **Policy changes** (ReadOnly, Secure, AllowedIPs, etc.) are applied via drain-and-swap, which waits for in-flight requests to finish before swapping

For fine-grained control, use `UpdateTuningOptions` and `UpdatePolicyOptions` directly.

The `Squash` mode cannot be changed at runtime and will return an error if you attempt to change it.

**Example:**

```go
// Get current options
opts := nfsServer.GetExportOptions()

// Modify options
opts.ReadOnly = true
opts.AttrCacheTimeout = 30 * time.Second
opts.MaxConnections = 200

// Apply updates
if err := nfsServer.UpdateExportOptions(opts); err != nil {
    log.Fatalf("Failed to update options: %v", err)
}

log.Println("Options updated successfully")
```

### UpdateTuningOptions

```go
func (nfs *AbsfsNFS) UpdateTuningOptions(fn func(*TuningOptions))
```

Applies a mutation function to the current tuning options (performance-related settings). The function receives a deep copy of the current tuning options; after the function returns, the modified copy is stored atomically.

Tuning changes take effect immediately without draining in-flight requests, since stale tuning reads only affect performance characteristics and cannot violate security invariants.

Side effects are applied automatically: caches are resized, worker pools adjusted, and logging reconfigured as needed.

**Example:**

```go
// Double the attribute cache size
nfsServer.UpdateTuningOptions(func(t *absnfs.TuningOptions) {
    t.AttrCacheSize = 20000
    t.AttrCacheTimeout = 30 * time.Second
})
```

### UpdatePolicyOptions

```go
func (nfs *AbsfsNFS) UpdatePolicyOptions(newPolicy PolicyOptions) error
```

Updates security and access policy using drain-and-swap. This method:

1. Stops accepting new NFS requests (new requests receive NFS3ERR_JUKEBOX, causing clients to retry)
2. Waits for all in-flight requests to complete under the old policy
3. Atomically swaps to the new policy
4. Resumes accepting requests under the new policy

This ensures that no request ever observes a mix of old and new policy settings. For example, a WRITE that passed a ReadOnly check under the old policy will complete before ReadOnly is set to true.

The `Squash` field is immutable and cannot be changed at runtime. Attempting to change it returns an error.

Returns an error if:
- An attempt is made to change the Squash mode
- The server instance is nil

**Example:**

```go
// Switch to read-only mode safely
policy := absnfs.PolicyOptions{
    ReadOnly:   true,
    Secure:     true,
    AllowedIPs: []string{"10.0.0.0/8"},
    Squash:     "root", // Must match the current Squash mode
}
if err := nfsServer.UpdatePolicyOptions(policy); err != nil {
    log.Fatalf("Failed to update policy: %v", err)
}
```

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
- **Directory cache**: Caches directory listings for improved READDIR/READDIRPLUS performance
- **Worker pool**: Handles concurrent operations using a configurable number of goroutines
- **Batch processor**: Groups similar operations together for improved throughput
- **Memory monitor**: Optionally monitors system memory and adjusts cache sizes under memory pressure
- **Rate limiter**: Provides DoS protection with per-IP and global request limits
- **Metrics collector**: Tracks operations, latency, cache performance, and errors

### Usage Pattern

1. Create an ABSFS filesystem implementation
2. Create an `AbsfsNFS` adapter with `New()`, passing the filesystem and export options
3. Create a `Server` with `NewServer()` and configure it
4. Set the adapter as the server's handler with `SetHandler()`
5. Start the server with `Listen()`
6. Clean up resources with `Close()` when done