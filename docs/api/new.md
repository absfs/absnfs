---
layout: default
title: New
---

# New

The `New` function is the primary entry point for creating a new NFS server adapter for an ABSFS filesystem.

## Function Signature

```go
func New(fs absfs.FileSystem, options ExportOptions) (*AbsfsNFS, error)
```

## Parameters

### fs

```go
fs absfs.FileSystem
```

The filesystem to export. This must be an implementation of the `absfs.FileSystem` interface.

Examples of compatible filesystems include:
- `memfs.FS` (in-memory filesystem)
- `osfs.FS` (operating system filesystem)
- Any custom implementation of the `absfs.FileSystem` interface

### options

```go
options ExportOptions
```

Configuration options for the NFS export. See [ExportOptions](./export-options.md) for details.

If you don't need to customize the export options, you can use the default options:

```go
absnfs.New(fs, absnfs.ExportOptions{})
```

## Return Values

### *AbsfsNFS

A pointer to a new `AbsfsNFS` instance that can be used to export the filesystem.

### error

An error if the NFS server adapter could not be created. Possible errors include:
- Invalid filesystem
- Invalid export options
- Initialization failures

## Example Usage

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

    // Create some content
    f, err := fs.Create("/hello.txt")
    if err != nil {
        log.Fatal(err)
    }
    f.Write([]byte("Hello from NFS!"))
    f.Close()

    // Create an NFS server with default options
    nfsServer, err := absnfs.New(fs, absnfs.ExportOptions{})
    if err != nil {
        log.Fatal(err)
    }

    // Export the filesystem
    if err := nfsServer.Export("/export/test", 2049); err != nil {
        log.Fatal(err)
    }

    log.Println("NFS server running...")
    select {} // Wait forever
}
```

## With Custom Options

```go
// Create an NFS server with custom options
options := absnfs.ExportOptions{
    ReadOnly: true,
    Secure: true,
    AllowedIPs: []string{"192.168.1.0/24"},
    Squash: "root",
    EnableReadAhead: true,
    ReadAheadSize: 524288, // 512KB
}

nfsServer, err := absnfs.New(fs, options)
```

## Implementation Details

The `New` function performs several initialization steps:

1. Validates the provided filesystem
2. Validates and normalizes export options
3. Initializes the file handle map
4. Creates the root node for the filesystem
5. Sets up attribute caching
6. Initializes read-ahead buffering if enabled
7. Creates the RPC server infrastructure

## Error Handling

The `New` function may return errors in the following scenarios:

- If the provided filesystem is nil
- If the export options contain invalid values
- If the root of the filesystem cannot be accessed
- If there are initialization errors in internal components

## Best Practices

- Always check the error return value
- Use appropriate export options for your use case
- Ensure the provided filesystem is properly initialized
- Consider security implications when configuring export options

## Thread Safety

The returned `AbsfsNFS` instance is thread-safe and can be used concurrently from multiple goroutines.