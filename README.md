# ABSFS NFS Server v1.0.4

> **Note:** This is the v1.x maintenance branch. No new features will be added.
> Active development has moved to [v2.0](https://github.com/absfs/absnfs).
> v1.0.4 is a security and protocol correctness release for users who cannot yet migrate to v2.

[![Go Reference](https://pkg.go.dev/badge/github.com/absfs/absnfs.svg)](https://pkg.go.dev/github.com/absfs/absnfs)
[![Go Report Card](https://goreportcard.com/badge/github.com/absfs/absnfs)](https://goreportcard.com/report/github.com/absfs/absnfs)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

An NFSv3 server adapter for [absfs](https://github.com/absfs/absfs). Any filesystem that implements `absfs.SymlinkFileSystem` can be exported as an NFS share.

## Features

- NFSv3 protocol implementation
- TLS/SSL encryption
- Symlink support (SYMLINK and READLINK operations)
- IP-based access control and user ID mapping (squash)
- Rate limiting and DoS protection
- Attribute and directory caching
- Worker pool for concurrent request handling

## Installation

```bash
go get github.com/absfs/absnfs
```

## Usage

```go
package main

import (
    "log"

    "github.com/absfs/absnfs"
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

    options := absnfs.ExportOptions{
        ReadOnly:   false,
        Secure:     true,
        AllowedIPs: []string{"192.168.1.0/24"},
    }

    server, err := absnfs.New(fs, options)
    if err != nil {
        log.Fatal(err)
    }

    if err := server.Export("/export/test", 2049); err != nil {
        log.Fatal(err)
    }

    <-make(chan struct{})
}
```

## NFS Operations

- LOOKUP, GETATTR, SETATTR
- READ, WRITE
- CREATE, REMOVE, RENAME
- READDIR, READDIRPLUS
- MKDIR, RMDIR
- SYMLINK, READLINK

## Security

- TLS/SSL encryption for connections
- Read-only export mode
- IP-based access control
- User ID mapping (root_squash, all_squash, none)
- Rate limiting to prevent DoS attacks

## Running with Privileges

NFS servers typically require elevated privileges for user ID mapping and RPC registration:

```bash
sudo ./yourprogram
```

To avoid root, use a non-privileged port (above 1024):

```go
server.Export("/export/test", 8049)
```

## Testing

```bash
go test -short -race ./...
```

## Migrating to v2

The v2 branch at [github.com/absfs/absnfs](https://github.com/absfs/absnfs) is where active development continues. Key differences:

- Simplified configuration surface
- Removed speculative subsystems (BatchProcessor, MemoryMonitor, ReadAheadBuffer)
- Improved protocol correctness and error mapping

See the v2 README and CHANGELOG for migration details.

## License

Apache License, Version 2.0 - see [LICENSE](LICENSE).
