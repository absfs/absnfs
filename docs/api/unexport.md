---
layout: default
title: Unexport
---

# Unexport

The `Unexport` method stops exporting a filesystem, shutting down the NFS server and releasing associated resources.

## Method Signature

```go
func (nfs *AbsfsNFS) Unexport() error
```

## Return Value

```go
error
```

An error if the filesystem could not be unexported. Possible errors include:
- Network errors (e.g., failed to close connections)
- Server shutdown errors

## Purpose

The `Unexport` method is used to:
1. Stop the NFS server gracefully
2. Release network resources (ports, connections)
3. Unregister from the portmapper service (if applicable)
4. Clean up internal resources

## Example Usage

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

    // Create an NFS server
    nfsServer, err := absnfs.New(fs, absnfs.ExportOptions{})
    if err != nil {
        log.Fatal(err)
    }

    // Export the filesystem
    if err := nfsServer.Export("/export/memfs", 2049); err != nil {
        log.Fatalf("Failed to export filesystem: %v", err)
    }
    
    log.Println("NFS server running. Press Ctrl+C to stop.")

    // Set up signal handling for graceful shutdown
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    
    // Wait for shutdown signal
    <-sigChan
    
    log.Println("Shutting down NFS server...")
    
    // Unexport the filesystem
    if err := nfsServer.Unexport(); err != nil {
        log.Printf("Error during shutdown: %v", err)
    }
    
    log.Println("NFS server stopped")
}
```

## Client Impact

When you call `Unexport`:

1. Existing client connections will be closed
2. Clients with mounted filesystems will experience I/O errors
3. Clients may see messages like "NFS server not responding" or "Stale NFS handle"
4. Clients will need to unmount and remount if the service is later restarted

## Implementation Details

The `Unexport` method performs several steps:

1. Stops the RPC server
2. Closes all open connections
3. Releases port bindings
4. Unregisters from the portmapper service (if applicable)
5. Cleans up internal resources
6. Signals to any waiting operations that the server is no longer available

## Best Practices

- Always call `Unexport` before your program exits to ensure clean shutdown
- Set up signal handling to catch termination signals and unexport gracefully
- In long-running services, consider adding a shutdown delay to allow client operations to complete
- Log any errors returned by `Unexport`

## Unexport with Timeout

For applications that need to ensure clients have time to finish operations, you can implement a timeout before unexporting:

```go
import (
    "context"
    "log"
    "os/signal"
    "syscall"
    "time"

    "github.com/absfs/absnfs"
    "github.com/absfs/memfs"
)

func main() {
    // Create and export as before...

    // Set up signal handling
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    
    // Wait for shutdown signal
    <-sigChan
    
    log.Println("Shutdown signal received. Waiting for clients to disconnect...")
    
    // Create a context with timeout
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    
    // Create a channel to signal completion
    done := make(chan struct{})
    
    // Unexport in a goroutine
    go func() {
        if err := nfsServer.Unexport(); err != nil {
            log.Printf("Error during shutdown: %v", err)
        }
        close(done)
    }()
    
    // Wait for either completion or timeout
    select {
    case <-done:
        log.Println("NFS server stopped successfully")
    case <-ctx.Done():
        log.Println("Shutdown timed out, forcing exit")
    }
}
```

## Error Handling

Errors returned by `Unexport` should be logged but typically don't require additional action. The most common errors are:

- Network errors when closing connections
- Timeout errors when waiting for operations to complete
- Portmapper unregistration errors