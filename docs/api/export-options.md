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

### AttrCacheSize

```go
AttrCacheSize int
```

Controls the maximum number of entries in the attribute cache. Larger values improve performance but consume more memory.

**Default:** `10000` (10,000 entries)

### Async

```go
Async bool
```

When set to `true`, allows asynchronous writes. This can improve write performance but may risk data loss in case of server crashes before data is flushed to disk.

**Default:** `false`

### MaxFileSize

```go
MaxFileSize int64
```

Controls the maximum allowed file size in bytes. Files larger than this limit cannot be created or extended beyond this size. Setting to 0 means no limit.

**Default:** `0` (no limit)

### ReadAheadMaxFiles

```go
ReadAheadMaxFiles int
```

Controls the maximum number of files that can have active read-ahead buffers simultaneously. This helps limit memory usage by read-ahead buffering.

**Default:** `100` (files)

### ReadAheadMaxMemory

```go
ReadAheadMaxMemory int64
```

Controls the maximum amount of memory in bytes that can be used for read-ahead buffers. Once this limit is reached, least recently used buffers will be evicted.

**Default:** `104857600` (100MB)

### AdaptToMemoryPressure

```go
AdaptToMemoryPressure bool
```

Enables automatic cache reduction when system memory is under pressure. When enabled, the server will periodically check system memory usage and reduce cache sizes when memory usage exceeds `MemoryHighWatermark`, until usage falls below `MemoryLowWatermark`.

**Default:** `false` (disabled)

### MemoryHighWatermark

```go
MemoryHighWatermark float64
```

Defines the threshold (as a fraction of total memory) at which memory pressure reduction actions will be triggered. Only applicable when `AdaptToMemoryPressure` is `true`. Valid range: 0.0 to 1.0 (0% to 100% of total memory).

**Default:** `0.8` (80% of total memory)

### MemoryLowWatermark

```go
MemoryLowWatermark float64
```

Defines the target memory usage (as a fraction of total memory) that the server will try to achieve when reducing cache sizes in response to memory pressure. Only applicable when `AdaptToMemoryPressure` is `true`. Valid range: 0.0 to `MemoryHighWatermark`.

**Default:** `0.6` (60% of total memory)

### MemoryCheckInterval

```go
MemoryCheckInterval time.Duration
```

Defines how frequently memory usage is checked for pressure detection. Only applicable when `AdaptToMemoryPressure` is `true`.

**Default:** `30 * time.Second`

### MaxWorkers

```go
MaxWorkers int
```

Controls the maximum number of goroutines used for handling concurrent operations. More workers can improve performance for concurrent workloads but consume more CPU resources.

**Default:** `runtime.NumCPU() * 4` (number of logical CPUs multiplied by 4)

### BatchOperations

```go
BatchOperations bool
```

Enables grouping of similar operations for improved performance. When enabled, the server will attempt to process multiple read/write operations together to reduce context switching and improve throughput.

**Default:** `true`

### MaxBatchSize

```go
MaxBatchSize int
```

Controls the maximum number of operations that can be included in a single batch. Larger batches can improve performance but may increase latency for individual operations. Only applicable when `BatchOperations` is `true`.

**Default:** `10` (operations)

### MaxConnections

```go
MaxConnections int
```

Limits the number of simultaneous client connections. Setting to 0 means unlimited connections (limited only by system resources).

**Default:** `100`

### IdleTimeout

```go
IdleTimeout time.Duration
```

Defines how long to keep inactive connections before closing them. This helps reclaim resources from abandoned connections.

**Default:** `5 * time.Minute`

### TCPKeepAlive

```go
TCPKeepAlive bool
```

Enables TCP keep-alive probes on NFS connections. Keep-alive helps detect dead connections when clients disconnect improperly.

**Default:** `true`

### TCPNoDelay

```go
TCPNoDelay bool
```

Disables Nagle's algorithm on TCP connections to reduce latency. This may improve performance for small requests at the cost of increased bandwidth usage.

**Default:** `true`

### SendBufferSize

```go
SendBufferSize int
```

Controls the size of the TCP send buffer in bytes. Larger buffers can improve throughput but consume more memory.

**Default:** `262144` (256KB)

### ReceiveBufferSize

```go
ReceiveBufferSize int
```

Controls the size of the TCP receive buffer in bytes. Larger buffers can improve throughput but consume more memory.

**Default:** `262144` (256KB)

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