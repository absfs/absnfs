---
layout: default
title: Creating Your First NFS Server
---

# Creating Your First NFS Server

This tutorial will guide you through creating a basic NFS server using ABSNFS. By the end, you'll have a working NFS server exporting a simple in-memory filesystem that clients can connect to.

## Prerequisites

- Go 1.21 or later
- Basic knowledge of Go programming
- An environment where you can run an NFS server (Linux, macOS, or Windows with admin privileges)
- An NFS client for testing (built into most operating systems)

## Step 1: Set Up Your Project

First, create a new Go project and install the required dependencies:

```bash
mkdir nfs-tutorial
cd nfs-tutorial
go mod init nfs-tutorial

# Install dependencies
go get github.com/absfs/absnfs
go get github.com/absfs/memfs
```

## Step 2: Create the Main Go File

Create a file named `main.go` with the following content:

```go
package main

import (
    "fmt"
    "log"
    "os"
    "os/signal"
    "syscall"

    "github.com/absfs/absnfs"
    "github.com/absfs/absfs"
    "github.com/absfs/memfs"
)

func main() {
    // Create an in-memory filesystem
    fs, err := memfs.NewFS()
    if err != nil {
        log.Fatalf("Failed to create filesystem: %v", err)
    }

    // Setup the filesystem with some content
    if err := setupFilesystem(fs); err != nil {
        log.Fatalf("Failed to setup filesystem: %v", err)
    }

    // Create the NFS server with default options
    server, err := absnfs.New(fs, absnfs.ExportOptions{})
    if err != nil {
        log.Fatalf("Failed to create NFS server: %v", err)
    }

    // Export the filesystem at the standard NFS port
    mountPath := "/export/tutorial"
    port := 2049

    fmt.Printf("Starting NFS server on port %d...\n", port)
    if err := server.Export(mountPath, port); err != nil {
        log.Fatalf("Failed to export filesystem: %v", err)
    }
    fmt.Printf("NFS server running. Mount with: mount -t nfs localhost:%s /mnt/nfs\n", mountPath)

    // Wait for interrupt signal to gracefully shutdown the server
    sigs := make(chan os.Signal, 1)
    signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
    
    fmt.Println("Press Ctrl+C to stop the server")
    <-sigs
    fmt.Println("Shutting down NFS server...")
    
    if err := server.Unexport(); err != nil {
        log.Printf("Error during shutdown: %v", err)
    }
    
    fmt.Println("NFS server stopped")
}

// setupFilesystem creates some example content in the filesystem
func setupFilesystem(fs absfs.FileSystem) error {
    // Create directories
    dirs := []string{
        "/docs",
        "/images",
        "/data",
    }
    
    for _, dir := range dirs {
        if err := fs.Mkdir(dir, 0755); err != nil {
            return fmt.Errorf("failed to create directory %s: %w", dir, err)
        }
    }
    
    // Create a README file
    readme, err := fs.Create("/README.txt")
    if err != nil {
        return fmt.Errorf("failed to create README: %w", err)
    }
    defer readme.Close()
    
    readmeContent := `Welcome to the NFS Tutorial!
This is a simple NFS server created with ABSNFS.
You can browse the following directories:
- /docs: Documentation files
- /images: Image files
- /data: Data files`
    
    if _, err := readme.Write([]byte(readmeContent)); err != nil {
        return fmt.Errorf("failed to write README content: %w", err)
    }
    
    // Create a documentation file
    docFile, err := fs.Create("/docs/getting-started.txt")
    if err != nil {
        return fmt.Errorf("failed to create doc file: %w", err)
    }
    defer docFile.Close()
    
    docContent := `Getting Started with ABSNFS
=======================

ABSNFS is a library that allows you to export any ABSFS-compatible filesystem via NFS.
This means you can create custom filesystems and make them accessible over the network.`
    
    if _, err := docFile.Write([]byte(docContent)); err != nil {
        return fmt.Errorf("failed to write doc content: %w", err)
    }
    
    return nil
}
```

## Step 3: Build and Run the Server

Build and run your NFS server:

```bash
go build
sudo ./nfs-tutorial  # You need root privileges to bind to port 2049
```

You should see output indicating that your NFS server is running.

## Step 4: Mount the NFS Share

Now, you can mount the NFS share from another terminal window or machine:

### Linux

```bash
# Create a mount point
sudo mkdir -p /mnt/nfs

# Mount the NFS share
sudo mount -t nfs localhost:/export/tutorial /mnt/nfs
```

### macOS

```bash
# Create a mount point
sudo mkdir -p /mnt/nfs

# Mount the NFS share
sudo mount -t nfs -o resvport localhost:/export/tutorial /mnt/nfs
```

### Windows

On Windows, you can use the "Map Network Drive" feature or the command line:

```
mount -o anon \\localhost\export\tutorial Z:
```

## Step 5: Test the NFS Share

Once mounted, you can browse and interact with the NFS share like any local filesystem:

```bash
# List the contents of the mount
ls -la /mnt/nfs

# Read the README file
cat /mnt/nfs/README.txt

# Check the docs directory
ls -la /mnt/nfs/docs
cat /mnt/nfs/docs/getting-started.txt
```

## Step 6: Create a New File (Client → Server)

Let's create a file on the NFS share from the client:

```bash
echo "This file was created by the NFS client" > /mnt/nfs/client-test.txt
```

You should be able to create and read back this file. This demonstrates that the NFS server is fully functional for both read and write operations.

## Step 7: Unmount the NFS Share

When you're done testing, unmount the NFS share:

### Linux/macOS

```bash
sudo umount /mnt/nfs
```

### Windows

```
umount Z:
```

## Step 8: Stop the Server

Return to the terminal where the NFS server is running and press Ctrl+C to stop it.

## What's Next?

Now that you have successfully created and tested a basic NFS server, you can explore more advanced features:

1. Experiment with different [export options](../api/export-options.md) like read-only mode or IP restrictions
2. Try exporting different types of filesystems, including custom implementations
3. Implement file monitoring or logging to track NFS client activity
4. Explore the [security guide](../guides/security.md) to secure your NFS server

## Troubleshooting

### Permission Denied

If you get "permission denied" errors, check:
- Are you running the server with appropriate privileges?
- Are file permissions set correctly in your filesystem?

### Connection Refused

If you get "connection refused" errors, check:
- Is the server running?
- Are any firewalls blocking port 2049?
- Are you using the correct hostname/IP address?

### NFS Server Not Responding

If the server doesn't respond, check:
- Are NFS services enabled on your system?
- Is the `rpcbind` service running (required on some systems)?