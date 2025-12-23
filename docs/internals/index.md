---
layout: default
title: Internals
---

# ABSNFS Internals

This section provides detailed information about the internal architecture and design of ABSNFS. It's intended for developers who want to understand how ABSNFS works, contribute to the project, or build custom functionality on top of it.

## Architecture

- [System Architecture](./architecture.md): Overview of the ABSNFS architecture and key components
- [Component Interactions](./component-interactions.md): How major components work together

## NFS Protocol Implementation

- [NFS Protocol](./nfs-protocol.md): NFSv3 protocol implementation details
- [RPC Implementation](./rpc-implementation.md): RPC protocol, XDR encoding/decoding, and request handling

## Core Components

- [File Handle Management](./file-handle-management.md): Efficient file handle management with min-heap reuse
- [Worker Pool Management](./worker-pool-management.md): Concurrent request handling
- [Operation Batching](./operation-batching.md): Grouping operations for performance

## Caching and Performance

- [Cache Size Control](./cache-size-control.md): Memory management and cache sizing
- [Metrics and Monitoring](./metrics-and-monitoring.md): Server metrics and observability

## Networking

- [Connection Management](./connection-management.md): TCP connection handling
- [TCP Buffer Sizes](./tcp-buffer-sizes.md): Buffer tuning for network performance
