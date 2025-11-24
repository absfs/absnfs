---
layout: default
title: Error Codes
---

# Error Codes

ABSNFS maps between filesystem errors and NFS protocol error codes. This page documents the NFS error codes that can be returned to clients and how they map to Go error types.

## NFS Error Codes

The NFS protocol defines a set of error codes that are returned to clients. The following table lists the error codes implemented in ABSNFS and their meanings:

| NFS Error Code | Value | Description |
|----------------|-------|-------------|
| NFS_OK | 0 | Success |
| NFSERR_PERM | 1 | Not owner |
| NFSERR_NOENT | 2 | No such file or directory |
| NFSERR_IO | 5 | I/O error |
| NFSERR_NXIO | 6 | No such device or address |
| NFSERR_ACCES | 13 | Permission denied |
| NFSERR_EXIST | 17 | File exists |
| NFSERR_NODEV | 19 | No such device |
| NFSERR_NOTDIR | 20 | Not a directory |
| NFSERR_ISDIR | 21 | Is a directory |
| NFSERR_INVAL | 22 | Invalid argument |
| NFSERR_FBIG | 27 | File too large |
| NFSERR_NOSPC | 28 | No space left on device |
| NFSERR_ROFS | 30 | Read-only file system |
| NFSERR_NAMETOOLONG | 63 | Filename too long |
| NFSERR_NOTEMPTY | 66 | Directory not empty |
| NFSERR_DQUOT | 69 | Disk quota exceeded |
| NFSERR_STALE | 70 | Stale file handle |
| NFSERR_WFLUSH | 99 | Write cache flushed |
| NFSERR_BADHANDLE | 10001 | Invalid file handle |
| NFSERR_NOTSUPP | 10004 | Operation not supported |
| NFSERR_DELAY | 10013 | Server is temporarily busy (rate limit exceeded) |

## Error Mapping

ABSNFS maps Go error types to appropriate NFS error codes. The following table shows how common Go errors are mapped:

| Go Error | NFS Error Code | Description |
|----------|----------------|-------------|
| nil | NFS_OK | Success |
| os.ErrNotExist | NFSERR_NOENT | No such file or directory |
| os.ErrPermission | NFSERR_ACCES | Permission denied |
| os.ErrExist | NFSERR_EXIST | File exists |
| os.ErrInvalid | NFSERR_INVAL | Invalid argument |
| syscall.ENOTEMPTY | NFSERR_NOTEMPTY | Directory not empty |
| syscall.EISDIR | NFSERR_ISDIR | Is a directory |
| syscall.ENOTDIR | NFSERR_NOTDIR | Not a directory |
| syscall.ENAMETOOLONG | NFSERR_NAMETOOLONG | Filename too long |
| syscall.EROFS | NFSERR_ROFS | Read-only file system |
| syscall.ENOSPC | NFSERR_NOSPC | No space left on device |
| *StaleFileHandleError | NFSERR_STALE | Stale file handle |
| *InvalidFileHandleError | NFSERR_BADHANDLE | Invalid file handle |
| *NotSupportedError | NFSERR_NOTSUPP | Operation not supported |

Any error that doesn't match a specific mapping is mapped to `NFSERR_IO` (I/O error) depending on the context.

## Custom Error Types

ABSNFS defines custom error types to represent specific NFS error conditions:

### StaleFileHandleError

This error is returned when a client provides a file handle that was once valid but is no longer valid (e.g., the file was deleted).

```go
type StaleFileHandleError struct {
    Handle []byte
}
```

### InvalidFileHandleError

This error is returned when a client provides a file handle that is invalid or not found in the file handle map.

```go
type InvalidFileHandleError struct {
    Handle uint64
    Reason string
}
```

### NotSupportedError

This error is returned when a client requests an operation that is not supported by the NFS implementation (e.g., hard links).

```go
type NotSupportedError struct {
    Operation string
    Reason    string
}
```

## Error Handling in Custom Filesystems

When implementing a custom filesystem for use with ABSNFS, you should use standard Go error types where appropriate:

```go
// Example implementation of a Read method
func (f *MyFile) Read(p []byte) (n int, err error) {
    if f.closed {
        return 0, os.ErrClosed // Will be mapped to NFSERR_IO
    }

    if !f.readable {
        return 0, os.ErrPermission // Will be mapped to NFSERR_ACCES
    }

    // ... read data ...

    return n, nil // Will be mapped to NFS_OK
}
```

## Example: Client Perspective

When an NFS client encounters an error, it typically translates the NFS error code to a local error. For example:

- If a client tries to read a file that doesn't exist, it will receive `NFSERR_NOENT` and typically report "No such file or directory"
- If a client tries to write to a read-only filesystem, it will receive `NFSERR_ROFS` and typically report "Read-only file system"

## Example: Server Implementation

Here's how error mapping is typically implemented in ABSNFS:

```go
func mapError(err error) uint32 {
    // Check custom errors first
    var invalidHandle *InvalidFileHandleError
    var notSupported *NotSupportedError

    switch {
    case err == nil:
        return NFS_OK
    case errors.As(err, &invalidHandle):
        return NFSERR_BADHANDLE
    case errors.As(err, &notSupported):
        return NFSERR_NOTSUPP
    case os.IsNotExist(err):
        return NFSERR_NOENT
    case os.IsPermission(err):
        return NFSERR_ACCES
    case os.IsExist(err):
        return NFSERR_EXIST
    // ... other mappings ...
    default:
        return NFSERR_IO
    }
}
```