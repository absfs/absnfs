# absnfs

NFSv3 server adapter for [absfs](https://github.com/absfs/absfs) filesystems.

Any filesystem implementing `absfs.SymlinkFileSystem` can be exported as a network-accessible NFS share. The server handles the NFSv3 wire protocol, caching, authentication, rate limiting, and connection management.

## Installation

```bash
go get github.com/absfs/absnfs
```

Requires Go 1.23 or later.

## Quick Start

```go
package main

import (
    "log"

    "github.com/absfs/absnfs"
    "github.com/absfs/memfs"
)

func main() {
    // Create an in-memory filesystem
    fs, err := memfs.NewFS()
    if err != nil {
        log.Fatal(err)
    }

    // Create the NFS server with default options
    server, err := absnfs.New(fs, absnfs.ExportOptions{})
    if err != nil {
        log.Fatal(err)
    }
    defer server.Close()

    // Serve NFS and MOUNT on one unprivileged port
    if err := server.Export("/", 12049); err != nil {
        log.Fatal(err)
    }

    // Mount from macOS, as yourself:
    //   mount_nfs -o vers=3,tcp,port=12049,mountport=12049,noresvport,nolocks,soft,intr localhost:/ ~/nfs
    // Linux (as root):
    //   mount -t nfs -o vers=3,tcp,port=12049,mountport=12049,nolock localhost:/ /mnt/test
    select {} // block forever
}
```

## Features

- **NFSv3 protocol** -- full implementation of RFC 1813 operations
- **Symlink support** -- SYMLINK and READLINK operations
- **TLS encryption** -- optional TLS/mTLS for secure connections
- **Rate limiting** -- per-IP, per-connection, and global request throttling
- **Attribute caching** -- LRU cache with configurable TTL for file attributes
- **Directory caching** -- optional caching of directory listings
- **Worker pool** -- concurrent request processing
- **IP filtering** -- allow/deny lists with CIDR support
- **UID/GID squashing** -- root, all, or none squash modes
- **Live reconfiguration** -- tuning and policy options updatable at runtime

## Requirements

The filesystem passed to `New()` must implement `absfs.SymlinkFileSystem`, not `absfs.FileSystem`. This interface adds `Symlink`, `Readlink`, and `Lstat` methods required for NFS symlink operations.

## API Reference

- [API Index](api/index.md) -- all exported types and functions
- [AbsfsNFS](api/absnfs.md) -- main server type: New, Close, Export, Unexport
- [ExportOptions](api/export-options.md) -- full configuration reference
- [TuningOptions / PolicyOptions](api/tuning-policy.md) -- runtime option updates
- [Error Codes](api/error-codes.md) -- NFS3 status codes and error mapping

## Version

Current version: `1.1.0`
