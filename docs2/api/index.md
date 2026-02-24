# API Reference Index

## Core Types

| Type | File | Description |
|------|------|-------------|
| [`AbsfsNFS`](absnfs.md) | types.go | NFS server instance wrapping an absfs filesystem |
| [`ExportOptions`](export-options.md) | options.go | User-facing configuration for an NFS export |
| [`TuningOptions`](tuning-policy.md#tuningoptions) | options.go | Performance settings (atomic swap at runtime) |
| [`PolicyOptions`](tuning-policy.md#policyoptions) | options.go | Security settings (drain-and-swap at runtime) |
| [`RequestOptions`](tuning-policy.md#requestoptions) | options.go | Per-request snapshot of tuning + policy |

## Configuration Types

| Type | File | Description |
|------|------|-------------|
| `TimeoutConfig` | options.go | Per-operation timeout durations |
| `LogConfig` | options.go | Structured logging settings (level, format, output) |
| `TLSConfig` | tls_config.go | TLS/mTLS certificate and version settings |
| `RateLimiterConfig` | rate_limiter.go | Token bucket rate limiting parameters |
| `ServerOptions` | server.go | Low-level TCP server configuration |

## Node and Attribute Types

| Type | File | Description |
|------|------|-------------|
| `NFSNode` | types.go | File or directory in the NFS tree |
| `NFSAttrs` | types.go | Cached file attributes (mode, size, uid, gid, times) |
| `FileHandleMap` | types.go | Handle-to-file mapping with path deduplication |

## Authentication Types

| Type | File | Description |
|------|------|-------------|
| `AuthContext` | auth.go | Per-request client identity (IP, credentials, cert) |
| `AuthResult` | auth.go | Authentication outcome (allowed, effective UID/GID) |

## Interfaces

| Interface | File | Description |
|-----------|------|-------------|
| `Logger` | logger.go | Structured logging (Debug, Info, Warn, Error methods) |
| `LogField` | logger.go | Key-value pair for structured log fields |

## Constructor and Lifecycle

| Function/Method | Signature | Description |
|----------------|-----------|-------------|
| [`New`](absnfs.md#new) | `New(fs absfs.SymlinkFileSystem, options ExportOptions) (*AbsfsNFS, error)` | Create server instance |
| [`Close`](absnfs.md#close) | `(n *AbsfsNFS) Close() error` | Release all resources |
| [`Export`](absnfs.md#export) | `(s *AbsfsNFS) Export(mountPath string, port int) error` | Start serving NFS |
| [`Unexport`](absnfs.md#unexport) | `(s *AbsfsNFS) Unexport() error` | Stop serving, release handles |

## Runtime Configuration

| Method | Signature | Description |
|--------|-----------|-------------|
| [`UpdateTuningOptions`](tuning-policy.md#updatetuningoptions) | `(n *AbsfsNFS) UpdateTuningOptions(fn func(*TuningOptions))` | Atomic swap of performance settings |
| [`UpdatePolicyOptions`](tuning-policy.md#updatepolicyoptions) | `(n *AbsfsNFS) UpdatePolicyOptions(newPolicy PolicyOptions) error` | Drain-and-swap of security settings |
| [`UpdateExportOptions`](absnfs.md#updateexportoptions) | `(n *AbsfsNFS) UpdateExportOptions(newOptions ExportOptions) error` | Update both tuning and policy |
| [`GetExportOptions`](absnfs.md#getexportoptions) | `(n *AbsfsNFS) GetExportOptions() ExportOptions` | Snapshot current configuration |
| [`SetLogger`](absnfs.md#setlogger) | `(n *AbsfsNFS) SetLogger(logger Logger) error` | Replace structured logger |

## NFS Status Constants

See [Error Codes](error-codes.md) for the full list of `NFS_OK`, `NFSERR_*` constants and the `mapError()` mapping.

## ACCESS3 Constants

```go
const (
    ACCESS3_READ    = 0x0001
    ACCESS3_LOOKUP  = 0x0002
    ACCESS3_MODIFY  = 0x0004
    ACCESS3_EXTEND  = 0x0008
    ACCESS3_DELETE  = 0x0010
    ACCESS3_EXECUTE = 0x0020
)
```
