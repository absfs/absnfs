---
layout: default
title: System Architecture
---

# System Architecture

ABSNFS is designed with a layered architecture that separates concerns and promotes maintainability. This document provides a high-level overview of the system architecture based on the actual implementation.

## Architectural Layers

ABSNFS is organized into the following layers, from highest to lowest level:

### 1. NFS Client Interface

At the highest level, ABSNFS presents a standard NFSv3 interface to clients. This ensures compatibility with any standard NFS client, including those built into operating systems.

Components:
- NFS protocol implementation (NFSv3)
- RPC message handling (DecodeRPCCall, EncodeRPCReply)
- XDR (eXternal Data Representation) encoding/decoding functions

### 2. ABSNFS Core

The core layer implements the NFS protocol operations and manages state, caching, and file handles. This is where the ABSFS interface is adapted to the NFS protocol.

Components:
- `AbsfsNFS`: Main server type that coordinates all components
- `NFSNode`: Representation of files and directories in the NFS tree
- `FileHandleMap`: Management of file handles
- `AttrCache`: Caching of file attributes
- `ReadAheadBuffer`: Optimization for sequential reads
- `WorkerPool`: Concurrent operation handling
- `BatchProcessor`: Batched operation processing
- `MemoryMonitor`: Memory pressure monitoring and adaptation
- `MetricsCollector`: Performance metrics collection

### 3. ABSFS Adapter

This layer adapts between NFS operations and ABSFS operations. It translates operations, errors, and attributes between the two systems.

Components:
- Operation handlers (mount_handlers.go, nfs_handlers.go)
- Attribute conversion (attributes.go)
- NFS operation implementations (nfs_operations.go)

### 4. ABSFS Interface

The bottom layer is the ABSFS interface itself, which is implemented by various filesystem implementations.

Components:
- `absfs.FileSystem` interface
- `absfs.File` interface
- Concrete filesystem implementations (e.g., memfs, osfs)

## Key Components

### AbsfsNFS

`AbsfsNFS` is the central component that coordinates all other components. It:

- Maintains the root node of the filesystem
- Manages the file handle map
- Implements NFS operations through handlers
- Coordinates caching and performance optimizations
- Controls worker pool and batch processing
- Monitors memory pressure and adapts cache sizes

Structure:
```go
type AbsfsNFS struct {
    fs            absfs.FileSystem
    root          *NFSNode
    logger        *log.Logger
    fileMap       *FileHandleMap
    mountPath     string
    options       ExportOptions
    attrCache     *AttrCache
    readBuf       *ReadAheadBuffer
    memoryMonitor *MemoryMonitor
    workerPool    *WorkerPool
    batchProc     *BatchProcessor
    metrics       *MetricsCollector
}
```

### NFSNode

`NFSNode` represents a file or directory in the NFS filesystem. It:

- Contains metadata about files and directories
- Embeds the absfs.FileSystem interface
- Maintains a path within the filesystem
- Manages child relationships for directories

Structure:
```go
type NFSNode struct {
    absfs.FileSystem
    path     string
    fileId   uint64
    attrs    *NFSAttrs
    children map[string]*NFSNode
}
```

### FileHandleMap

The `FileHandleMap` manages mappings between NFS file handles and filesystem objects. It:

- Maintains a simple map[uint64]absfs.File
- Allocates handles sequentially starting from 1
- Provides Get/Release operations
- Uses RWMutex for thread-safe access

Structure:
```go
type FileHandleMap struct {
    sync.RWMutex
    handles    map[uint64]absfs.File
    lastHandle uint64
}
```

### AttrCache

The `AttrCache` caches file attributes to improve performance. It:

- Stores recently accessed file attributes
- Uses configurable TTL for cache validity
- Supports size limits to control memory usage
- Adapts to memory pressure when enabled

### ReadAheadBuffer

The `ReadAheadBuffer` improves read performance for sequential access patterns. It:

- Detects sequential read patterns
- Prefetches data ahead of client requests
- Manages buffer lifecycle and eviction
- Limits memory usage through configurable bounds

### WorkerPool

The `WorkerPool` handles concurrent operations efficiently. It:

- Maintains a pool of worker goroutines
- Queues and distributes work across workers
- Provides synchronous and asynchronous task execution
- Configurable worker count based on system resources

### BatchProcessor

The `BatchProcessor` groups similar operations for improved performance. It:

- Collects similar operations (reads, writes)
- Processes them in batches
- Reduces context switching overhead
- Improves overall throughput

### MemoryMonitor

The `MemoryMonitor` adapts cache sizes based on memory pressure. It:

- Periodically checks system memory usage
- Triggers cache reduction when memory is high
- Targets memory usage below watermark thresholds
- Coordinates with AttrCache and ReadAheadBuffer

### MetricsCollector

The `MetricsCollector` tracks performance metrics. It:

- Collects operation counts and timings
- Tracks cache hit/miss rates
- Monitors memory usage
- Provides metrics via API

## Request Flow

A typical NFS request flows through the system as follows:

1. Client sends an NFS request to the server (TCP/UDP)
2. Server decodes the RPC request using DecodeRPCCall
3. Server identifies the NFS program, version, and procedure
4. Server routes the request to the appropriate handler (mount or NFS)
5. Handler looks up the file handle using FileHandleMap
6. Handler performs the operation on the underlying ABSFS filesystem
7. Results are encoded using EncodeRPCReply
8. Response is sent back to the client

## Component Interactions

The following diagram illustrates the interactions between components for a typical read operation:

```
Client -> Server -> Handler -> FileHandleMap -> absfs.File (Read)
                                    |
                                    v
                            ReadAheadBuffer (check cache)
                                    |
                                    v
Client <- Server <- Handler <- Encode Result
```

For cached attribute lookups:

```
Client -> Server -> Handler -> AttrCache (hit/miss)
                                    |
                                    v (miss)
                            absfs.FileSystem (Stat)
                                    |
                                    v
Client <- Server <- Handler <- Encode Attributes
```

## Configuration and Options

The `ExportOptions` structure controls server behavior:

- **Performance**: TransferSize, MaxWorkers, BatchOperations
- **Caching**: AttrCacheTimeout, AttrCacheSize, EnableReadAhead
- **Memory**: AdaptToMemoryPressure, MemoryHighWatermark, MemoryLowWatermark
- **Networking**: MaxConnections, IdleTimeout, TCPKeepAlive, TCPNoDelay
- **Security**: ReadOnly, Secure, AllowedIPs

## Design Principles

ABSNFS was designed with the following principles in mind:

1. **Compatibility**: Support standard NFS clients without modifications
2. **Adaptability**: Work with any ABSFS-compatible filesystem
3. **Performance**: Optimize common operations through caching and buffering
4. **Correctness**: Correctly implement the NFS protocol
5. **Robustness**: Handle errors and edge cases gracefully
6. **Simplicity**: Prefer simple, maintainable implementations over complex abstractions

## Limitations

The current architecture has some limitations:

1. **NFSv3 Only**: Only NFSv3 is currently supported, not newer versions
2. **Authentication**: Limited authentication mechanisms (typical of NFSv3)
3. **Locking**: Limited support for advisory file locking (NLM not implemented)
4. **Handle Persistence**: File handles don't persist across server restarts

## Future Architecture Enhancements

Planned architectural improvements include:

1. **NFSv4 Support**: Adding support for the newer NFSv4 protocol
2. **Enhanced Security**: Additional security mechanisms
3. **Handle Persistence**: Optional file handle persistence
4. **Performance Optimizations**: Additional caching and performance improvements
5. **Distributed Architecture**: Support for clustered/distributed NFS servers
