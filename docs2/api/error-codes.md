# NFS3 Error Codes

Defined in `nfs_types.go`. These constants follow RFC 1813 (NFSv3 Protocol Specification).

## Status Constants

| Constant | Value | Meaning |
|----------|-------|---------|
| `NFS_OK` | 0 | Success |
| `NFSERR_PERM` | 1 | Not owner / permission denied |
| `NFSERR_NOENT` | 2 | No such file or directory |
| `NFSERR_IO` | 5 | I/O error |
| `NFSERR_NXIO` | 6 | No such device or address |
| `NFSERR_ACCES` | 13 | Access denied |
| `NFSERR_EXIST` | 17 | File exists |
| `NFSERR_NODEV` | 19 | No such device |
| `NFSERR_NOTDIR` | 20 | Not a directory |
| `NFSERR_ISDIR` | 21 | Is a directory |
| `NFSERR_INVAL` | 22 | Invalid argument |
| `NFSERR_FBIG` | 27 | File too large |
| `NFSERR_NOSPC` | 28 | No space left on device |
| `NFSERR_ROFS` | 30 | Read-only filesystem |
| `NFSERR_NAMETOOLONG` | 63 | Filename too long |
| `NFSERR_NOTEMPTY` | 66 | Directory not empty |
| `NFSERR_DQUOT` | 69 | Disk quota exceeded |
| `NFSERR_STALE` | 70 | Stale file handle |
| `NFSERR_WFLUSH` | 99 | Write cache flushed |
| `NFSERR_BADHANDLE` | 10001 | Invalid file handle |
| `NFSERR_NOT_SYNC` | 10002 | Update synchronization mismatch (sattrguard3) |
| `NFSERR_NOTSUPP` | 10004 | Operation not supported |
| `NFSERR_JUKEBOX` | 10008 | Server busy, retry later (used during policy drain) |
| `NFSERR_DELAY` | 10013 | Temporarily busy (rate limit or timeout) |

`ACCESS_DENIED` is an alias for `NFSERR_ACCES`.

## mapError()

Defined in `operations.go`. Converts Go errors to NFS status codes. This is an unexported function used internally by all NFS procedure handlers.

```go
func mapError(err error) uint32
```

### Mapping Table

| Go Error | NFS Status | Notes |
|----------|------------|-------|
| `nil` | `NFS_OK` | |
| `*InvalidFileHandleError` | `NFSERR_BADHANDLE` | Checked via `errors.As` |
| `*NotSupportedError` | `NFSERR_NOTSUPP` | Checked via `errors.As` |
| `context.DeadlineExceeded` | `NFSERR_DELAY` | Timeout |
| `ErrTimeout` | `NFSERR_DELAY` | Package-level timeout sentinel |
| `os.ErrNotExist`, `syscall.ENOENT` | `NFSERR_NOENT` | |
| `os.ErrPermission`, `syscall.EACCES`, `syscall.EPERM` | `NFSERR_PERM` | |
| `os.ErrExist`, `syscall.EEXIST` | `NFSERR_EXIST` | |
| `os.ErrInvalid` | `NFSERR_INVAL` | |
| `syscall.ENOTDIR` | `NFSERR_NOTDIR` | |
| `syscall.EISDIR` | `NFSERR_ISDIR` | |
| `syscall.ENOSPC` | `NFSERR_NOSPC` | |
| `syscall.EFBIG` | `NFSERR_FBIG` | |
| `syscall.ENAMETOOLONG` | `NFSERR_NAMETOOLONG` | |
| any other error | `NFSERR_IO` | Catch-all |

### Custom Error Types

```go
// InvalidFileHandleError -> NFSERR_BADHANDLE
type InvalidFileHandleError struct {
    Handle uint64
    Reason string
}

// NotSupportedError -> NFSERR_NOTSUPP
type NotSupportedError struct {
    Operation string
    Reason    string
}
```

Both are checked using `errors.As`, so wrapped errors are matched correctly.

### ErrTimeout

```go
var ErrTimeout = errors.New("operation timed out")
```

Package-level sentinel returned when an operation exceeds its configured timeout from `TimeoutConfig`. Maps to `NFSERR_DELAY`.

## ACCESS3 Permission Bits

Used by the NFS ACCESS procedure to check specific permissions on a file:

```go
const (
    ACCESS3_READ    = 0x0001 // Read data or list directory
    ACCESS3_LOOKUP  = 0x0002 // Look up name in directory
    ACCESS3_MODIFY  = 0x0004 // Rewrite existing data or modify directory
    ACCESS3_EXTEND  = 0x0008 // Write new data or add to directory
    ACCESS3_DELETE  = 0x0010 // Delete entry from directory
    ACCESS3_EXECUTE = 0x0020 // Execute file or search directory
)
```
