# absnfs

[![Go Reference](https://pkg.go.dev/badge/github.com/absfs/absnfs/v2.svg)](https://pkg.go.dev/github.com/absfs/absnfs/v2)
[![Go Report Card](https://goreportcard.com/badge/github.com/absfs/absnfs/v2)](https://goreportcard.com/report/github.com/absfs/absnfs/v2)
[![CI](https://github.com/absfs/absnfs/v2/actions/workflows/ci.yml/badge.svg)](https://github.com/absfs/absnfs/v2/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

NFSv3 server adapter for [absfs](https://github.com/absfs/absfs) filesystems. Any filesystem implementing `absfs.SymlinkFileSystem` can be exported as a network-accessible NFS share.

## Installation

```bash
go get github.com/absfs/absnfs/v2
```

Requires Go 1.23 or later.

## Quick Start

```go
package main

import (
    "log"

    "github.com/absfs/absnfs/v2"
    "github.com/absfs/memfs"
)

func main() {
    fs, err := memfs.NewFS()
    if err != nil {
        log.Fatal(err)
    }

    f, _ := fs.Create("/hello.txt")
    f.Write([]byte("Hello from NFS!"))
    f.Close()

    server, err := absnfs.New(fs, absnfs.ExportOptions{})
    if err != nil {
        log.Fatal(err)
    }
    defer server.Close()

    if err := server.Export("/", 2049); err != nil {
        log.Fatal(err)
    }

    select {} // block forever
}
```

Mount from a client:

```bash
# macOS
sudo mount_nfs -o resvport,nolocks,vers=3,tcp,port=2049,mountport=2049 localhost:/ /Volumes/test

# Linux
sudo mount -t nfs -o vers=3,tcp,port=2049,mountport=2049,nolock localhost:/ /mnt/test
```

## Features

- **NFSv3 protocol** -- RFC 1813 operations including SYMLINK/READLINK
- **TLS encryption** -- optional TLS/mTLS for secure connections
- **Rate limiting** -- per-IP, per-connection, and global request throttling
- **Attribute caching** -- LRU cache with configurable TTL
- **Directory caching** -- optional caching of directory listings
- **Worker pool** -- concurrent request processing
- **IP filtering** -- allow/deny lists with CIDR support
- **UID/GID squashing** -- root, all, or none squash modes
- **Live reconfiguration** -- tuning and policy options updatable at runtime
- **Connection management** -- idle timeout and TCP tuning

## NFS Operations

LOOKUP, GETATTR, SETATTR, READ, WRITE, CREATE, REMOVE, RENAME, READDIR, READDIRPLUS, MKDIR, RMDIR, SYMLINK, READLINK, FSINFO, FSSTAT, PATHCONF, ACCESS, COMMIT

## Documentation

- [Quick Start](docs/guides/quick-start.md)
- [Configuration](docs/guides/configuration.md)
- [Security](docs/guides/security.md)
- [TLS Encryption](docs/guides/tls.md)
- [Runtime Reconfiguration](docs/guides/runtime-config.md)
- [API Reference](docs/api/index.md)
- [Examples](docs/examples/basic-server.md)

## Testing

```bash
go test -race ./...
```

## License

Apache License, Version 2.0 -- see [LICENSE](LICENSE).
