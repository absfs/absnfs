# ABSFS NFS Server

This package implements an NFS server adapter for the [absfs](https://github.com/absfs/absfs) filesystem interface. It allows any filesystem that implements the absfs.FileSystem interface to be exported as an NFS share.

## Features

- Export any absfs-compatible filesystem via NFS
- Support for basic NFS operations (read, write, create, remove, etc.)
- Configurable export options (read-only, security, etc.)
- File handle management
- Error mapping between absfs and NFS error codes
- Attribute caching for improved performance

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
        ReadOnly: false,
        Secure: true,
        AllowedIPs: []string{"192.168.1.0/24"},
        Squash: "none",
    }

    // Create NFS server
    server, err := absnfs.New(fs, options)
    if err != nil {
        log.Fatal(err)
    }

    // Export the filesystem
    if err := server.Export("/export/test", 2049); err != nil {
        log.Fatal(err)
    }

    // Wait for shutdown signal
    <-make(chan struct{})
}
```

## Implementation Details

The package provides:

1. File Handle Management
   - Unique handle generation
   - Handle to file mapping
   - Automatic cleanup

2. NFS Operations
   - LOOKUP
   - GETATTR/SETATTR
   - READ/WRITE
   - CREATE/REMOVE
   - RENAME
   - READDIR

3. Security Features
   - Read-only mode
   - IP restrictions
   - User mapping

## Running with Privileges

Running an NFS server typically requires elevated privileges because:

1. NFS servers need to modify user IDs and file permissions
2. Administrative access may be needed for RPC registration

When running in production, use:

```bash
# Linux/macOS
sudo go run main.go

# Or
go build
sudo ./yourprogram
```

To use a non-privileged port (above 1024), specify it when exporting:

```go
// Export on port 8049 instead of the standard NFS port 2049
if err := nfsServer.Export("/export/test", 8049); err != nil {
    log.Fatal(err)
}
```

## Testing

The package includes comprehensive tests that verify:
- Basic file operations
- Directory operations
- Permission handling
- Error conditions
- Read-only mode
- Attribute caching

Run the complete test suite with:
```bash
go test -v ./...
```

For test coverage report:
```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
```

## Documentation

Comprehensive documentation is available in the `docs/` directory:

- [Getting Started](docs/guides/installation.md)
- [Basic Usage](docs/guides/basic-usage.md)
- [API Reference](docs/api/index.md)
- [Examples](docs/examples/index.md)
- [Internals](docs/internals/index.md)

## Contributing

Contributions are welcome! Please ensure:

1. Tests are included for new features
2. Documentation is updated
3. Code follows Go best practices and the project's coding style
4. All tests pass before submitting a pull request
5. Commit messages are clear and descriptive

See our [contributing guide](docs/compatibility/contributing.md) for more details.

## License

This project is licensed under the Apache License, Version 2.0 - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

- The [absfs](https://github.com/absfs/absfs) project for providing the filesystem interface
- Contributors to the Go NFS implementations that informed this design
