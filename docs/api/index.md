---
layout: default
title: API Reference
---

# API Reference

This section provides detailed documentation of the types and functions in the ABSNFS package.

## Core Types

- [AbsfsNFS](./absfsnfs.md): The main type that wraps an ABSFS filesystem and exports it as an NFS server
- [ExportOptions](./export-options.md): Configuration options for the NFS export
- [Server](./server.md): Network server handling NFS protocol requests
- [NFSNode](./nfs-node.md): Representation of files and directories in the exported filesystem

## Key Functions

- [New](./new.md): Creates a new NFS server instance
- [Export](./export.md): Exports a filesystem at a specified mount point and port
- [Unexport](./unexport.md): Stops exporting a filesystem

## Error Handling

- [Error Codes](./error-codes.md): NFS error codes and their meaning
- [Error Mapping](./error-mapping.md): How filesystem errors are mapped to NFS errors

## Security and Configuration

- [TLSConfig](./tls-config.md): TLS/SSL encryption configuration
- [RateLimiter](./rate-limiter.md): Rate limiting and DoS protection
- [Logging](./logging.md): Structured logging configuration

## Advanced Topics

- [FileHandleMap](./filehandle-map.md): How file handles are managed
- [AttrCache](./attr-cache.md): Caching system for file attributes
- [ReadAheadBuffer](./readahead-buffer.md): Performance optimization for read operations