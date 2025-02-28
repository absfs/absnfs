---
layout: default
title: ExportOptions
---

# ExportOptions

The `ExportOptions` type provides configuration settings for an NFS export. These options control security, performance, and behavior of the NFS server.

## Type Definition

```go
type ExportOptions struct {
    // ReadOnly specifies whether the export is read-only
    ReadOnly bool

    // Secure enables additional security checks
    Secure bool

    // AllowedIPs restricts access to specific IP addresses or ranges
    AllowedIPs []string

    // Squash controls how user identities are mapped
    // Valid values: "none", "root", "all"
    Squash string

    // TransferSize controls the maximum size of read/write transfers
    TransferSize int

    // EnableReadAhead enables read-ahead buffering for improved performance
    EnableReadAhead bool

    // ReadAheadSize controls the size of the read-ahead buffer
    ReadAheadSize int

    // AttrCacheTimeout controls how long file attributes are cached
    AttrCacheTimeout time.Duration
    
    // contains other optional configuration fields
}
```

## Field Descriptions

### ReadOnly

```go
ReadOnly bool
```

When set to `true`, the NFS server will reject all write operations. This provides a simple way to ensure that the exported filesystem cannot be modified by clients.

**Default:** `false`

### Secure

```go
Secure bool
```

When set to `true`, the NFS server performs additional security checks:

- Enforces IP restrictions defined in `AllowedIPs`
- Validates file paths to prevent directory traversal attacks
- Performs stricter permission checking

**Default:** `true`

### AllowedIPs

```go
AllowedIPs []string
```

Restricts access to the NFS server to specific IP addresses or ranges. IP ranges can be specified in CIDR notation (e.g., "192.168.1.0/24"). When empty, access is unrestricted.

**Default:** `[]` (empty, unrestricted)

### Squash

```go
Squash string
```

Controls how user identities are mapped between NFS clients and the server:

- `"none"`: No identity mapping, client UIDs/GIDs are used as-is
- `"root"`: Root (UID 0) is mapped to the anonymous user, other users are unchanged
- `"all"`: All users are mapped to the anonymous user

**Default:** `"root"`

### TransferSize

```go
TransferSize int
```

Controls the maximum size in bytes of read/write transfers. Larger values may improve performance but require more memory.

**Default:** `65536` (64KB)

### EnableReadAhead

```go
EnableReadAhead bool
```

Enables read-ahead buffering for improved sequential read performance. When a client reads a file sequentially, the server will prefetch additional data to reduce latency.

**Default:** `true`

### ReadAheadSize

```go
ReadAheadSize int
```

Controls the size in bytes of the read-ahead buffer when `EnableReadAhead` is `true`.

**Default:** `262144` (256KB)

### AttrCacheTimeout

```go
AttrCacheTimeout time.Duration
```

Controls how long file attributes are cached by the server. Longer timeouts improve performance but may cause clients to see stale data if files are modified outside of the NFS server.

**Default:** `5 * time.Second`

## Example Usage

```go
options := absnfs.ExportOptions{
    ReadOnly: true,
    Secure: true,
    AllowedIPs: []string{"192.168.1.0/24", "10.0.0.5"},
    Squash: "root",
    TransferSize: 131072, // 128KB
    EnableReadAhead: true,
    ReadAheadSize: 524288, // 512KB
    AttrCacheTimeout: 10 * time.Second,
}

server, err := absnfs.New(fs, options)
```

## Default Options

If you don't need to customize the export options, you can use the default options:

```go
server, err := absnfs.New(fs, absnfs.ExportOptions{})
```

This will create an NFS server with reasonable default settings suitable for most use cases.