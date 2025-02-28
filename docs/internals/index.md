---
layout: default
title: Internals
---

# ABSNFS Internals

This section provides detailed information about the internal architecture and design of ABSNFS. It's intended for developers who want to understand how ABSNFS works, contribute to the project, or build custom functionality on top of it.

## Architecture

- [System Architecture](./architecture.md): Overview of the ABSNFS architecture
- [Component Interactions](./component-interactions.md): How the different components interact
- [Request Flow](./request-flow.md): How requests flow through the system

## NFS Protocol Implementation

- [NFS Protocol Overview](./nfs-protocol.md): Overview of the NFS protocol
- [RPC Implementation](./rpc-implementation.md): How ABSNFS implements the RPC protocol
- [NFS Operations](./nfs-operations.md): Implementation of NFS operations

## Core Components

- [File Handle Management](./file-handle-management.md): How file handles are created and managed
- [Attribute Caching](./attribute-caching.md): How file attributes are cached for performance
- [Read-Ahead Buffering](./read-ahead-buffering.md): How read-ahead buffering works
- [Error Mapping](./error-mapping.md): How errors are mapped between ABSFS and NFS