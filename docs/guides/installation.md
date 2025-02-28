---
layout: default
title: Installation
---

# Installation

This guide covers how to install and set up ABSNFS in your Go projects.

## Prerequisites

Before installing ABSNFS, ensure you have:

1. **Go**: ABSNFS requires Go 1.21 or later
2. **NFS Support**: Your operating system should support NFS for testing (most modern operating systems do)
3. **absfs Package**: ABSNFS builds on the absfs abstract filesystem interface

## Installing with Go Modules

The recommended way to install ABSNFS is using Go modules. In your project directory:

1. Initialize a Go module if you haven't already:

```bash
go mod init yourmodule
```

2. Add ABSNFS as a dependency:

```bash
go get github.com/absfs/absnfs
```

This will download the ABSNFS package and update your `go.mod` and `go.sum` files.

## Verifying the Installation

To verify that the installation was successful, create a simple test program:

```go
package main

import (
    "fmt"
    
    "github.com/absfs/absnfs"
)

func main() {
    fmt.Println("ABSNFS package imported successfully")
    fmt.Printf("Using ABSNFS version: %s\n", absnfs.Version)
}
```

Run the program:

```bash
go run main.go
```

You should see output indicating that the package was imported successfully.

## Installing Additional Dependencies

ABSNFS works with any filesystem that implements the absfs interface. Here are some common filesystem implementations you might want to install:

```bash
# In-memory filesystem
go get github.com/absfs/memfs

# Operating system filesystem
go get github.com/absfs/osfs
```

## Running with Privileges

Running an NFS server typically requires elevated privileges because:

1. Some NFS-related operations require privileged access
2. NFS servers often need to modify user IDs and file permissions

### Linux

On Linux, you can run your NFS server with the necessary privileges using:

```bash
# Using sudo
sudo go run main.go

# Or build and run as root
go build
sudo ./yourprogram
```

You can also use capabilities to grant specific privileges:

```bash
sudo setcap cap_net_bind_service=+ep ./yourprogram
```

### macOS

On macOS, you'll typically need to run as root:

```bash
sudo go run main.go
```

### Windows

On Windows, you'll need to run as Administrator:
1. Open Command Prompt as Administrator
2. Navigate to your project directory
3. Run your program

## Using Non-Privileged Ports

If you don't want to run with elevated privileges, you can use a non-standard port above 1024:

```go
// Export on port 8049 instead of the standard 2049
if err := nfsServer.Export("/export/test", 8049); err != nil {
    log.Fatal(err)
}
```

When mounting, clients will need to specify this port:

```bash
# Linux/macOS
mount -t nfs server:8049:/export/test /mnt/nfs

# Windows
mount -o anon \\server@8049\export\test Z:
```

## Installation Troubleshooting

### Import Errors

If you see import errors like:

```
cannot find package "github.com/absfs/absnfs" in any of:
    [list of directories]
```

Try these steps:
1. Run `go mod tidy` to ensure all dependencies are properly resolved
2. Check that your Go version is at least 1.21 with `go version`
3. Verify that your `go.mod` file contains the correct require statement

### Permission Errors

If you encounter permission errors when running your program:

```
bind: permission denied
```

Either:
1. Run with elevated privileges as described above
2. Use a non-privileged port (above 1024)

### RPC/NFS Service Errors

If you see errors related to RPC or NFS services:

```
portmap service not running
```

or

```
RPC: Program not registered
```

Ensure that:
1. The RPC portmapper (rpcbind) service is running on your system
2. NFS services are installed on your system

## Next Steps

Now that you have ABSNFS installed, you can proceed to:

1. [Basic Usage](./basic-usage.md): Learn the basic usage patterns
2. [Exporting Filesystems](./exporting-filesystems.md): Export different types of filesystems
3. [Configuration](./configuration.md): Configure ABSNFS for different scenarios

For a complete example, see the [Simple NFS Server Example](../examples/simple-nfs-server.md).