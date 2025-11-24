---
layout: default
title: Internals
---

# ABSNFS Internals

This section provides detailed information about the internal architecture and design of ABSNFS. It's intended for developers who want to understand how ABSNFS works, contribute to the project, or build custom functionality on top of it.

## Architecture

- [System Architecture](./architecture.md): Overview of the ABSNFS architecture and key components

## NFS Protocol Implementation

- [RPC Implementation](./rpc-implementation.md): How ABSNFS implements the RPC protocol, XDR encoding/decoding, and request handling

## Core Components

- [File Handle Management](./file-handle-management.md): Simple and efficient file handle management with min-heap reuse