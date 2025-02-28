---
layout: default
title: ABSNFS - NFS Server for Abstract Filesystems
---

# ABSNFS

## NFS Server Adapter for the ABSFS Ecosystem

ABSNFS is a Go package that implements an NFS (Network File System) server adapter for the [ABSFS](https://github.com/absfs/absfs) abstract filesystem interface. It allows any filesystem that implements the absfs.FileSystem interface to be exported as an NFS share over a network.

## What is ABSNFS?

ABSNFS bridges the gap between virtual filesystems and network sharing, enabling you to:

- Export any ABSFS-compatible filesystem as an NFS share
- Make in-memory filesystems accessible over the network
- Create custom filesystems accessible via standard NFS clients
- Build specialized network storage solutions with custom behaviors

## Key Features

- **Universal Compatibility**: Export any filesystem that implements the ABSFS interface
- **Full NFS Support**: Implements NFSv3 protocol with comprehensive operation support
- **Flexible Configuration**: Extensive export options including read-only mode, IP restrictions
- **Performance Optimized**: Includes attribute caching and read-ahead buffering
- **Security Features**: IP restrictions, read-only mode, and user identity mapping

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

    // Create test content
    f, _ := fs.Create("/hello.txt")
    f.Write([]byte("Hello from NFS!"))
    f.Close()

    // Create NFS server with default options
    server, err := absnfs.New(fs, absnfs.ExportOptions{})
    if err != nil {
        log.Fatal(err)
    }

    // Export the filesystem on the default port
    if err := server.Export("/export/test", 2049); err != nil {
        log.Fatal(err)
    }

    log.Println("NFS server running at localhost:2049/export/test")
    
    // Wait forever
    select {}
}
```

## Documentation Sections

- [API Reference](./api/): Detailed documentation of types and functions
- [Guides](./guides/): How-to guides for common tasks
- [Tutorials](./tutorials/): Step-by-step tutorials for getting started
- [Examples](./examples/): Complete working examples
- [Internals](./internals/): How ABSNFS works under the hood
- [Testing](./testing/): Testing approach and code quality information

## Project Status

ABSNFS is a new project under active development. While it has comprehensive test coverage, it has not yet been extensively deployed in production environments. We are transparent about code quality and test coverage to help users make informed decisions about adoption.

## License

This project is licensed under the MIT License.