---
layout: default
title: Managing Exports
---

# Managing Exports

This guide covers strategies for creating, managing, and monitoring NFS exports using ABSNFS. You'll learn how to handle multiple exports, dynamically manage them, and monitor their usage.

## Basic Export Management

### Creating an Export

To export a filesystem, you first create an NFS server instance and then call the `Export` method:

```go
// Create NFS server
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
```

### Stopping an Export

To stop serving an export, call the `Unexport` method:

```go
// Stop the export
if err := nfsServer.Unexport(); err != nil {
    log.Printf("Error unexporting: %v", err)
}
```

This will:
1. Stop accepting new connections
2. Close existing connections gracefully
3. Release network resources
4. Unregister from the portmapper (if applicable)

### Changing Export Behavior

Export options are set when creating the NFS server and cannot be changed dynamically. To change export behavior, you need to unexport the filesystem, create a new server instance with different options, and export again:

```go
// Unexport current filesystem
if err := nfsServer.Unexport(); err != nil {
    log.Printf("Error unexporting: %v", err)
}

// Create new server with different options
newOptions := absnfs.ExportOptions{
    ReadOnly: true, // Switch to read-only mode
}
nfsServer, err := absnfs.New(fs, newOptions)
if err != nil {
    log.Fatal(err)
}

// Export with new options
if err := nfsServer.Export(mountPath, port); err != nil {
    log.Fatal(err)
}
```

Note that this will disconnect all clients, who will need to reconnect.

## Multiple Exports

### Exporting Multiple Filesystems

To export multiple filesystems, create multiple NFS server instances:

```go
// Create first filesystem
fs1, _ := memfs.NewFS()
// Populate fs1...

// Create second filesystem
fs2, _ := osfs.NewFS("/path/to/data")

// Create and export first NFS server
server1, _ := absnfs.New(fs1, absnfs.ExportOptions{})
server1.Export("/export/memfs", 2049)

// Create and export second NFS server
server2, _ := absnfs.New(fs2, absnfs.ExportOptions{})
server2.Export("/export/data", 2050) // Use a different port
```

To shut down, unexport each server:

```go
server1.Unexport()
server2.Unexport()
```

### Exporting Same Filesystem at Different Paths

You can export the same filesystem at multiple mount points:

```go
// Create filesystem
fs, _ := memfs.NewFS()
// Populate fs...

// Create NFS server
server, _ := absnfs.New(fs, absnfs.ExportOptions{})

// Export at multiple paths
server.Export("/export/v1", 2049)
server.Export("/export/latest", 2049) // Same filesystem, different path
```

This is useful for providing different entry points to the same data.

## Dynamic Export Management

### Creating Exports on Demand

For dynamic export creation:

```go
package main

import (
    "fmt"
    "log"
    "net/http"
    "sync"

    "github.com/absfs/absnfs"
    "github.com/absfs/memfs"
)

func main() {
    exports := make(map[string]*absnfs.AbsfsNFS)
    var mutex sync.Mutex

    // HTTP handler to create exports
    http.HandleFunc("/create", func(w http.ResponseWriter, r *http.Request) {
        name := r.URL.Query().Get("name")
        if name == "" {
            http.Error(w, "Name is required", http.StatusBadRequest)
            return
        }

        mutex.Lock()
        defer mutex.Unlock()

        // Check if export already exists
        if _, exists := exports[name]; exists {
            http.Error(w, "Export already exists", http.StatusConflict)
            return
        }

        // Create filesystem
        fs, err := memfs.NewFS()
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }

        // Create NFS server
        server, err := absnfs.New(fs, absnfs.ExportOptions{})
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }

        // Calculate port (2050 + index for each export)
        port := 2050 + len(exports)
        mountPath := "/export/" + name

        // Export filesystem
        if err := server.Export(mountPath, port); err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }

        // Store the server
        exports[name] = server

        // Respond with success
        w.Write([]byte("Export created at " + mountPath + " on port " + fmt.Sprint(port)))
    })

    // HTTP handler to delete exports
    http.HandleFunc("/delete", func(w http.ResponseWriter, r *http.Request) {
        name := r.URL.Query().Get("name")
        if name == "" {
            http.Error(w, "Name is required", http.StatusBadRequest)
            return
        }

        mutex.Lock()
        defer mutex.Unlock()

        // Check if export exists
        server, exists := exports[name]
        if !exists {
            http.Error(w, "Export not found", http.StatusNotFound)
            return
        }

        // Unexport
        if err := server.Unexport(); err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }

        // Remove from map
        delete(exports, name)

        // Respond with success
        w.Write([]byte("Export deleted"))
    })

    // Start HTTP server
    log.Println("Export management server listening on :8080")
    log.Fatal(http.ListenAndServe(":8080", nil))
}
```

This example creates a simple HTTP API to dynamically create and delete exports.

### Monitoring Export Status

To monitor export status:

```go
package main

import (
    "encoding/json"
    "log"
    "net/http"
    "sync"
    "time"

    "github.com/absfs/absnfs"
    "github.com/absfs/memfs"
)

type ExportStatus struct {
    Name      string    `json:"name"`
    MountPath string    `json:"mountPath"`
    Port      int       `json:"port"`
    ReadOnly  bool      `json:"readOnly"`
    CreatedAt time.Time `json:"createdAt"`
}

func main() {
    exports := make(map[string]*absnfs.AbsfsNFS)
    status := make(map[string]ExportStatus)
    var mutex sync.Mutex

    // Create an export
    fs, _ := memfs.NewFS()
    server, _ := absnfs.New(fs, absnfs.ExportOptions{})
    server.Export("/export/test", 2049)
    
    exports["test"] = server
    status["test"] = ExportStatus{
        Name:      "test",
        MountPath: "/export/test",
        Port:      2049,
        ReadOnly:  false,
        CreatedAt: time.Now(),
    }

    // HTTP handler to list exports
    http.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
        mutex.Lock()
        defer mutex.Unlock()

        // Convert to JSON
        jsonData, err := json.MarshalIndent(status, "", "  ")
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }

        // Respond with JSON
        w.Header().Set("Content-Type", "application/json")
        w.Write(jsonData)
    })

    // Start HTTP server
    log.Println("Status server listening on :8080")
    log.Fatal(http.ListenAndServe(":8080", nil))
}
```

## Export Lifecycle Management

### Graceful Shutdown

For graceful shutdown with signal handling:

```go
package main

import (
    "log"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/absfs/absnfs"
    "github.com/absfs/memfs"
)

func main() {
    // Create filesystem
    fs, _ := memfs.NewFS()
    
    // Create NFS server
    server, _ := absnfs.New(fs, absnfs.ExportOptions{})
    server.Export("/export/test", 2049)
    
    log.Println("NFS server running. Press Ctrl+C to stop.")

    // Set up signal handling
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    
    // Wait for signal
    <-sigChan
    log.Println("Shutdown signal received")
    
    // Unexport with timeout
    log.Println("Unexporting... (waiting for clients to disconnect)")
    
    // Create a timeout channel
    timeout := time.After(30 * time.Second)
    done := make(chan struct{})
    
    // Unexport in a goroutine
    go func() {
        if err := server.Unexport(); err != nil {
            log.Printf("Error during unexport: %v", err)
        }
        close(done)
    }()
    
    // Wait for unexport or timeout
    select {
    case <-done:
        log.Println("Unexport completed successfully")
    case <-timeout:
        log.Println("Unexport timed out, forcing exit")
    }
    
    log.Println("Shutdown complete")
}
```

### Automatic Restart

To automatically restart an export if it fails:

```go
package main

import (
    "log"
    "time"

    "github.com/absfs/absnfs"
    "github.com/absfs/memfs"
)

func main() {
    // Create filesystem
    fs, _ := memfs.NewFS()
    
    // Create NFS server
    server, _ := absnfs.New(fs, absnfs.ExportOptions{})
    
    // Export with automatic restart
    go manageExport(server)
    
    // Wait forever
    select {}
}

func manageExport(server *absnfs.AbsfsNFS) {
    mountPath := "/export/test"
    port := 2049
    
    for {
        log.Printf("Exporting filesystem at %s on port %d", mountPath, port)
        
        err := server.Export(mountPath, port)
        if err != nil {
            log.Printf("Export failed: %v", err)
            log.Printf("Retrying in 10 seconds...")
            time.Sleep(10 * time.Second)
            continue
        }
        
        // Wait for export to fail (or be explicitly stopped)
        // This is a simplification - in a real implementation, you would
        // need a mechanism to detect when the export fails
        time.Sleep(60 * time.Second)
        
        // Try to unexport cleanly before restarting
        server.Unexport()
    }
}
```

## Best Practices

### Export Naming Conventions

Follow these guidelines for export paths:

1. Use descriptive names that indicate content
2. Include version information if applicable
3. Use consistent naming patterns across exports
4. Avoid special characters
5. Start paths with `/export/` for clarity

Examples:
- `/export/docs` - Documentation
- `/export/data/v1` - Version 1 of data
- `/export/services/auth` - Authentication service data

### Port Management

When managing multiple exports:

1. Use the standard port (2049) for the primary export
2. Use consecutive ports for additional exports
3. Document port assignments
4. Consider using a port registry to avoid conflicts
5. Check port availability before binding

### Client Management

To manage client connections:

1. Log client IP addresses
2. Implement connection limits
3. Use timeouts to release abandoned connections
4. Consider rate limiting for high-traffic exports
5. Monitor and log client activity

### High Availability

For high availability:

1. Implement health checks
2. Set up automatic failover
3. Use load balancing across multiple servers
4. Implement retry logic in clients
5. Monitor and alert on export failures

## Troubleshooting

### Common Export Problems

1. **Port binding failures**:
   - Check if the port is already in use
   - Verify you have sufficient privileges
   - Try a different port

2. **Client connection failures**:
   - Check firewall rules
   - Verify network connectivity
   - Ensure the server is running
   - Check client NFS configuration

3. **Performance issues**:
   - Monitor resource usage
   - Check for network bottlenecks
   - Adjust cache and buffer settings
   - Consider load distribution

### Monitoring and Debugging

To effectively monitor exports:

1. Use the built-in metrics system:
   ```go
   metrics := nfsServer.GetMetrics()
   log.Printf("Total operations: %d", metrics.TotalOperations)
   log.Printf("Active connections: %d", metrics.ActiveConnections)
   ```

2. Implement health endpoints:
   ```go
   http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
       // Check if exports are functioning
       // ...
       w.Write([]byte("OK"))
   })
   ```

3. Use system monitoring tools:
   - Check network connections with `netstat`
   - Monitor filesystem usage with `df`
   - Track NFS statistics with `nfsstat`

## Next Steps

Now that you understand how to manage exports, you may want to explore:

1. [Performance Tuning](./performance-tuning.md): Optimize your NFS server
2. [Security](./security.md): Secure your exports
3. [Client Compatibility](./client-compatibility.md): Ensure compatibility with different clients