---
layout: default
title: Export
---

# Export

The `Export` method makes a filesystem accessible to NFS clients by starting an NFS server at the specified mount path and port.

## Method Signature

```go
func (nfs *AbsfsNFS) Export(mountPath string, port int) error
```

## Parameters

### mountPath

```go
mountPath string
```

The path that clients will use when mounting the NFS share. This is the path component of the NFS URL that clients use. For example, if `mountPath` is `/export/data`, clients would mount `server:/export/data`.

The mount path:
- Does not need to exist on the server's filesystem
- Is purely a naming convention for clients
- Should follow Unix path conventions (starting with a slash)
- Should be a simple path without special characters

### port

```go
port int
```

The TCP/UDP port that the NFS server will listen on. The standard port for NFS is 2049.

Note that:
- On Unix-like systems, binding to ports below 1024 (including the standard port 2049) requires root privileges
- You can use a non-standard port (e.g., 8049) if you don't have root privileges, but clients will need to specify this port explicitly
- The port must not be in use by another application

## Return Value

```go
error
```

An error if the filesystem could not be exported. Possible errors include:
- Network errors (e.g., port already in use)
- Permission errors (e.g., insufficient privileges to bind to the port)
- Configuration errors

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

    // Create some content
    f, err := fs.Create("/hello.txt")
    if err != nil {
        log.Fatal(err)
    }
    f.Write([]byte("Hello from NFS!"))
    f.Close()

    // Create an NFS server
    nfsServer, err := absnfs.New(fs, absnfs.ExportOptions{})
    if err != nil {
        log.Fatal(err)
    }

    // Export the filesystem at the standard NFS port
    mountPath := "/export/memfs"
    port := 2049
    
    log.Printf("Exporting filesystem at %s on port %d", mountPath, port)
    if err := nfsServer.Export(mountPath, port); err != nil {
        log.Fatalf("Failed to export filesystem: %v", err)
    }
    
    log.Printf("NFS server running. Mount with: mount -t nfs localhost:%s /mnt/nfs", mountPath)

    // Wait for shutdown signal
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    <-sigChan

    // Unexport when done
    if err := nfsServer.Unexport(); err != nil {
        log.Printf("Error during shutdown: %v", err)
    }
}
```

## Using a Non-Standard Port

```go
// Export the filesystem on a non-standard port (doesn't require root)
if err := nfsServer.Export("/export/memfs", 8049); err != nil {
    log.Fatalf("Failed to export filesystem: %v", err)
}

log.Println("NFS server running. Mount with: mount -t nfs localhost:/export/memfs:8049 /mnt/nfs")
```

## Implementation Details

The `Export` method performs the following steps:

1. Initializes the NFS server if not already initialized
2. Registers the mount path with internal routing
3. Starts listening on the specified port for both TCP and UDP
4. Registers with the portmapper service if available (on Unix-like systems)
5. Makes the filesystem available to incoming NFS client requests

## Client Connection Information

When a client connects to an exported filesystem:

1. The client first contacts the server's portmapper to find the NFS service (if using standard ports)
2. The client then sends a MOUNT request to get a file handle for the exported directory
3. Once mounted, the client sends NFS protocol requests to access files and directories

## Security Notes

- Exporting a filesystem makes it accessible to anyone who can reach the server's port
- Use the security options in `ExportOptions` to restrict access
- Consider using a firewall to limit access to trusted networks
- Be aware that standard NFS does not encrypt traffic between client and server

## Common Errors

Here are some common errors that might occur when exporting a filesystem:

1. **Permission denied**: When using the standard port (2049) without root privileges
   ```
   Failed to export filesystem: bind: permission denied
   ```

2. **Address already in use**: When the port is already being used by another application
   ```
   Failed to export filesystem: bind: address already in use
   ```

3. **Invalid mount path**: When the mount path is invalid
   ```
   Failed to export filesystem: invalid mount path
   ```