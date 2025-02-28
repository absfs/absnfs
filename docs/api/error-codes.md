---
layout: default
title: Error Codes
---

# Error Codes

ABSNFS maps between filesystem errors and NFS protocol error codes. This page documents the NFS error codes that can be returned to clients and how they map to Go error types.

## NFS Error Codes

The NFSv3 protocol defines a set of error codes that are returned to clients. The following table lists these error codes and their meanings:

| NFS Error Code | Value | Description |
|----------------|-------|-------------|
| NFS3_OK | 0 | Success |
| NFS3ERR_PERM | 1 | Not owner |
| NFS3ERR_NOENT | 2 | No such file or directory |
| NFS3ERR_IO | 5 | I/O error |
| NFS3ERR_NXIO | 6 | No such device or address |
| NFS3ERR_ACCES | 13 | Permission denied |
| NFS3ERR_EXIST | 17 | File exists |
| NFS3ERR_XDEV | 18 | Cross-device link |
| NFS3ERR_NODEV | 19 | No such device |
| NFS3ERR_NOTDIR | 20 | Not a directory |
| NFS3ERR_ISDIR | 21 | Is a directory |
| NFS3ERR_INVAL | 22 | Invalid argument |
| NFS3ERR_FBIG | 27 | File too large |
| NFS3ERR_NOSPC | 28 | No space left on device |
| NFS3ERR_ROFS | 30 | Read-only file system |
| NFS3ERR_MLINK | 31 | Too many hard links |
| NFS3ERR_NAMETOOLONG | 63 | Filename too long |
| NFS3ERR_NOTEMPTY | 66 | Directory not empty |
| NFS3ERR_DQUOT | 69 | Disk quota exceeded |
| NFS3ERR_STALE | 70 | Stale file handle |
| NFS3ERR_BADHANDLE | 10001 | Invalid file handle |
| NFS3ERR_NOT_SYNC | 10002 | Update synchronization mismatch |
| NFS3ERR_BAD_COOKIE | 10003 | READDIR or READDIRPLUS cookie is stale |
| NFS3ERR_NOTSUPP | 10004 | Operation not supported |
| NFS3ERR_TOOSMALL | 10005 | Buffer or request is too small |
| NFS3ERR_SERVERFAULT | 10006 | Server fault (undefined error) |
| NFS3ERR_BADTYPE | 10007 | Type not supported by server |
| NFS3ERR_JUKEBOX | 10008 | Request initiated, but will take time to complete |

## Error Mapping

ABSNFS maps Go error types to appropriate NFS error codes. The following table shows how common Go errors are mapped:

| Go Error | NFS Error Code | Description |
|----------|----------------|-------------|
| nil | NFS3_OK | Success |
| os.ErrNotExist | NFS3ERR_NOENT | No such file or directory |
| os.ErrPermission | NFS3ERR_ACCES | Permission denied |
| os.ErrExist | NFS3ERR_EXIST | File exists |
| os.ErrInvalid | NFS3ERR_INVAL | Invalid argument |
| syscall.ENOTEMPTY | NFS3ERR_NOTEMPTY | Directory not empty |
| syscall.EISDIR | NFS3ERR_ISDIR | Is a directory |
| syscall.ENOTDIR | NFS3ERR_NOTDIR | Not a directory |
| syscall.ENAMETOOLONG | NFS3ERR_NAMETOOLONG | Filename too long |
| syscall.EROFS | NFS3ERR_ROFS | Read-only file system |
| syscall.ENOSPC | NFS3ERR_NOSPC | No space left on device |
| *InvalidFileHandleError | NFS3ERR_BADHANDLE | Invalid file handle |
| *StaleFileHandleError | NFS3ERR_STALE | Stale file handle |

Any error that doesn't match a specific mapping is mapped to `NFS3ERR_IO` (I/O error) or `NFS3ERR_SERVERFAULT` (server fault) depending on the context.

## Custom Error Types

ABSNFS defines several custom error types to represent specific NFS error conditions:

### InvalidFileHandleError

This error is returned when a client provides an invalid file handle.

```go
type InvalidFileHandleError struct {
    Handle []byte
}
```

### StaleFileHandleError

This error is returned when a client provides a file handle that was once valid but is no longer valid (e.g., the file was deleted).

```go
type StaleFileHandleError struct {
    Handle []byte
}
```

### NotSupportedError

This error is returned when a client requests an operation that is not supported by the server.

```go
type NotSupportedError struct {
    Operation string
}
```

## Error Handling in Custom Filesystems

When implementing a custom filesystem for use with ABSNFS, you should use standard Go error types where appropriate:

```go
// Example implementation of a Read method
func (f *MyFile) Read(p []byte) (n int, err error) {
    if f.closed {
        return 0, os.ErrClosed // Will be mapped to NFS3ERR_IO
    }
    
    if !f.readable {
        return 0, os.ErrPermission // Will be mapped to NFS3ERR_ACCES
    }
    
    // ... read data ...
    
    return n, nil // Will be mapped to NFS3_OK
}
```

## Example: Client Perspective

When an NFS client encounters an error, it typically translates the NFS error code to a local error. For example:

- If a client tries to read a file that doesn't exist, it will receive `NFS3ERR_NOENT` and typically report "No such file or directory"
- If a client tries to write to a read-only filesystem, it will receive `NFS3ERR_ROFS` and typically report "Read-only file system"

## Example: Server Implementation

Here's how error mapping is typically implemented in ABSNFS:

```go
func mapError(err error) nfsv3.NFSStatus {
    if err == nil {
        return nfsv3.NFS3_OK
    }
    
    switch {
    case os.IsNotExist(err):
        return nfsv3.NFS3ERR_NOENT
    case os.IsPermission(err):
        return nfsv3.NFS3ERR_ACCES
    case os.IsExist(err):
        return nfsv3.NFS3ERR_EXIST
    // ... other mappings ...
    default:
        return nfsv3.NFS3ERR_IO
    }
}
```