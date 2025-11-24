---
layout: default
title: System Architecture
---

# System Architecture

ABSNFS is designed with a layered architecture that separates concerns and promotes maintainability. This document provides a high-level overview of the system architecture.

## Architectural Layers

ABSNFS is organized into the following layers, from highest to lowest level:

![ABSNFS Architecture Diagram](/assets/images/architecture.png)

### 1. NFS Client Interface

At the highest level, ABSNFS presents a standard NFSv3 interface to clients. This ensures compatibility with any standard NFS client, including those built into operating systems.

Components:
- NFS protocol implementation (NFSv3)
- RPC server for handling client requests
- XDR (eXternal Data Representation) encoding/decoding

### 2. ABSNFS Core

The core layer implements the NFS protocol operations and manages state, caching, and file handles. This is where the ABSFS interface is adapted to the NFS protocol.

Components:
- `AbsfsNFS`: Main type that coordinates all components
- `NFSNode`: Representation of files and directories
- `FileHandleMap`: Management of file handles
- `AttrCache`: Caching of file attributes
- `ReadAheadBuffer`: Optimization for sequential reads

### 3. ABSFS Adapter

This layer adapts between NFS operations and ABSFS operations. It translates operations, errors, and attributes between the two systems.

Components:
- Operation adapters (read, write, etc.)
- Error mapping
- Attribute conversion

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
- Implements NFS operations
- Coordinates caching and performance optimizations

### NFSNode

`NFSNode` represents a file or directory in the NFS filesystem. It:

- Contains metadata about files and directories
- Maps between NFS file handles and ABSFS paths
- Manages child relationships for directories

### Server

The `Server` component handles network communication and RPC protocol details. It:

- Listens for incoming connections
- Decodes RPC requests
- Routes requests to appropriate handlers
- Encodes and sends responses

### FileHandleMap

The `FileHandleMap` manages mappings between NFS file handles and filesystem objects. It:

- Maps uint64 handles to absfs.File objects
- Reuses freed handles efficiently using a min-heap
- Provides thread-safe operations with read-write mutexes
- Handles creation, lookups, and release operations

### AttrCache

The `AttrCache` caches file attributes to improve performance. It:

- Stores recently accessed file attributes
- Validates cached attributes against TTL settings
- Refreshes attributes when needed

### ReadAheadBuffer

The `ReadAheadBuffer` improves read performance for sequential access patterns. It:

- Detects sequential read patterns
- Prefetches data ahead of client requests
- Manages buffer lifecycle and eviction

### WorkerPool

The `WorkerPool` manages concurrent request processing. It:

- Maintains a pool of worker goroutines
- Queues incoming tasks for processing
- Provides configurable concurrency limits
- Tracks active workers and queue depth
- Supports dynamic resizing

### BatchProcessor

The `BatchProcessor` groups similar operations for efficiency. It:

- Batches read, write, and getattr operations
- Groups requests by file handle
- Processes batches when they reach a size threshold or timeout
- Reduces overhead for multiple small operations

### RateLimiter

The `RateLimiter` prevents denial-of-service attacks. It:

- Implements token bucket rate limiting
- Enforces global, per-IP, and per-connection limits
- Provides operation-specific limits (e.g., large reads/writes)
- Tracks file handle allocation limits
- Supports sliding window rate limiting for mount operations

### MemoryMonitor

The `MemoryMonitor` manages memory usage. It:

- Tracks system memory usage
- Detects memory pressure conditions
- Automatically reduces cache sizes when needed
- Triggers garbage collection during high memory usage
- Provides memory statistics

### TLSConfig

The `TLSConfig` manages TLS/SSL encryption. It:

- Configures server certificates and keys
- Supports client certificate authentication
- Enforces minimum TLS versions (1.2+)
- Manages cipher suite selection
- Supports certificate reloading for rotation

## Request Flow

A typical NFS request flows through the system as follows:

1. Client sends an NFS request to the server
2. Server decodes the RPC request and identifies the NFS operation
3. Server routes the request to the appropriate handler in AbsfsNFS
4. AbsfsNFS looks up the file handle and gets the corresponding NFSNode
5. AbsfsNFS performs the operation on the underlying ABSFS filesystem
6. Results are processed, encoded, and sent back to the client

## Component Interactions

The following diagram illustrates the interactions between components for a typical read operation:

```
Client -> Server -> AbsfsNFS -> FileHandleMap -> NFSNode -> ABSFS (Read Operation) 
                                          |
                                          v
Client <- Server <- AbsfsNFS <- ReadAheadBuffer
```

## Design Principles

ABSNFS was designed with the following principles in mind:

1. **Compatibility**: Support standard NFS clients without modifications
2. **Adaptability**: Work with any ABSFS-compatible filesystem
3. **Performance**: Optimize common operations through caching and buffering
4. **Correctness**: Correctly implement the NFS protocol
5. **Robustness**: Handle errors and edge cases gracefully

## Limitations

The current architecture has some limitations:

1. **NFSv3 Only**: Only NFSv3 is currently supported, not newer versions
2. **Authentication**: Limited authentication mechanisms (typical of NFSv3)
3. **Locking**: Limited support for advisory file locking (NLM not implemented)

## Future Architecture Enhancements

Planned architectural improvements include:

1. **NFSv4 Support**: Adding support for the newer NFSv4 protocol
2. **Enhanced Security**: Additional security mechanisms
3. **Distributed Architecture**: Support for clustered/distributed NFS servers
4. **Performance Optimizations**: Additional caching and performance improvements