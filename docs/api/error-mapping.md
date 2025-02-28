---
layout: default
title: Error Mapping
---

# Error Mapping

This page explains how errors are mapped between ABSFS filesystem errors and NFS protocol error codes in ABSNFS.

## Overview

Error mapping is a critical part of ABSNFS. It translates errors from the underlying filesystem into standardized NFS error codes that clients understand. This mapping ensures that NFS clients receive appropriate and meaningful error information.

## Mapping Process

When an error occurs during an NFS operation, ABSNFS follows these steps:

1. Capture the error from the underlying ABSFS filesystem
2. Analyze the error to determine its type and context
3. Map the error to the most appropriate NFS error code
4. Return the NFS error code to the client

## Error Mapping Logic

The error mapping logic considers several factors:

1. **Error Type**: The specific type of error (e.g., permission error, not found)
2. **Operation Context**: The operation being performed (e.g., read, write, lookup)
3. **File Type**: Whether the operation involves a file or directory
4. **Export Options**: Whether factors like read-only mode affect the error

## Common Error Mappings

### Filesystem Access Errors

| ABSFS/Go Error | NFS Error | Description |
|----------------|-----------|-------------|
| `os.ErrNotExist` | `NFS3ERR_NOENT` | File or directory not found |
| `os.ErrPermission` | `NFS3ERR_ACCES` | Permission denied |
| `os.ErrExist` | `NFS3ERR_EXIST` | File already exists |
| Error from read-only filesystem | `NFS3ERR_ROFS` | Read-only file system |

### File Type Errors

| ABSFS/Go Error | NFS Error | Description |
|----------------|-----------|-------------|
| Directory when file expected | `NFS3ERR_ISDIR` | Is a directory |
| File when directory expected | `NFS3ERR_NOTDIR` | Not a directory |

### Resource Errors

| ABSFS/Go Error | NFS Error | Description |
|----------------|-----------|-------------|
| Disk space error | `NFS3ERR_NOSPC` | No space left on device |
| Quota exceeded | `NFS3ERR_DQUOT` | Disk quota exceeded |
| File too large | `NFS3ERR_FBIG` | File too large |

### Handle and Reference Errors

| ABSFS/Go Error | NFS Error | Description |
|----------------|-----------|-------------|
| Invalid file handle | `NFS3ERR_BADHANDLE` | Invalid file handle |
| Stale file handle | `NFS3ERR_STALE` | Stale file handle |
| Invalid cookie | `NFS3ERR_BAD_COOKIE` | READDIR cookie is stale |

### Operation Errors

| ABSFS/Go Error | NFS Error | Description |
|----------------|-----------|-------------|
| Unsupported operation | `NFS3ERR_NOTSUPP` | Operation not supported |
| Generic I/O error | `NFS3ERR_IO` | I/O error |
| Invalid argument | `NFS3ERR_INVAL` | Invalid argument |

## Implementation Details

### Error Detection

ABSNFS uses several techniques to detect and categorize errors:

1. **Type Assertions**: Checking for specific error types
2. **Error Predicates**: Using helpers like `os.IsNotExist()` and `os.IsPermission()`
3. **Error Unwrapping**: Examining wrapped errors using `errors.Unwrap()`
4. **Custom Error Types**: Recognizing custom error types defined by ABSNFS

### Example Code

Here's a simplified example of how error mapping is implemented:

```go
func mapToNFSError(err error, op string) nfsv3.NFSStatus {
    if err == nil {
        return nfsv3.NFS3_OK
    }

    // Check for specific error types
    var invalidHandle *InvalidFileHandleError
    if errors.As(err, &invalidHandle) {
        return nfsv3.NFS3ERR_BADHANDLE
    }

    var staleHandle *StaleFileHandleError
    if errors.As(err, &staleHandle) {
        return nfsv3.NFS3ERR_STALE
    }

    // Check for standard error conditions
    switch {
    case errors.Is(err, fs.ErrNotExist) || os.IsNotExist(err):
        return nfsv3.NFS3ERR_NOENT
    case errors.Is(err, fs.ErrPermission) || os.IsPermission(err):
        return nfsv3.NFS3ERR_ACCES
    case errors.Is(err, fs.ErrExist) || os.IsExist(err):
        return nfsv3.NFS3ERR_EXIST
    case errors.Is(err, syscall.EISDIR):
        return nfsv3.NFS3ERR_ISDIR
    case errors.Is(err, syscall.ENOTDIR):
        return nfsv3.NFS3ERR_NOTDIR
    case errors.Is(err, syscall.ENOTEMPTY):
        return nfsv3.NFS3ERR_NOTEMPTY
    case errors.Is(err, syscall.EROFS):
        return nfsv3.NFS3ERR_ROFS
    // ... other cases ...
    }

    // Operation-specific mappings
    switch op {
    case "READ":
        // Special handling for read operations
        if errors.Is(err, io.EOF) {
            return nfsv3.NFS3_OK // EOF is not an error for NFS reads
        }
    case "LOOKUP":
        // Special handling for lookup operations
    // ... other operations ...
    }

    // Default to I/O error for unrecognized errors
    return nfsv3.NFS3ERR_IO
}
```

## Error Context Enrichment

In some cases, ABSNFS enriches error information to provide better context:

1. **Logging**: Additional context is logged to help with debugging
2. **Wrapping**: Errors are wrapped with additional context
3. **Correlation**: Error information is correlated with client information

## Custom Filesystem Considerations

When implementing a custom filesystem for use with ABSNFS, you should:

1. Use standard Go error types where appropriate (`os.ErrNotExist`, `os.ErrPermission`, etc.)
2. Return filesystem-specific errors by wrapping standard errors
3. Ensure errors provide sufficient context for accurate mapping

Example of good error practice in a custom filesystem:

```go
func (fs *MyFS) Open(name string) (absfs.File, error) {
    if !fs.exists(name) {
        return nil, fmt.Errorf("file %s does not exist: %w", name, os.ErrNotExist)
    }
    
    if !fs.hasPermission(name) {
        return nil, fmt.Errorf("no permission to access %s: %w", name, os.ErrPermission)
    }
    
    // ... open the file ...
}
```

## Error Recovery

ABSNFS includes error recovery mechanisms to handle certain error conditions:

1. **Retry Logic**: Some transient errors trigger retry attempts
2. **Graceful Degradation**: Some errors result in reduced functionality rather than complete failure
3. **Client Notification**: Serious errors are communicated to clients to allow for recovery

## Error Logging and Monitoring

For diagnosability, ABSNFS logs error information:

1. **Error Type**: The specific error type
2. **Operation**: The operation that caused the error
3. **Client Information**: Client IP and credentials
4. **Path Information**: The file or directory path involved

## Best Practices

When extending or modifying ABSNFS, follow these error mapping best practices:

1. **Be Specific**: Map errors to the most specific NFS error code possible
2. **Preserve Context**: Wrap errors with additional context
3. **Consistent Mapping**: Ensure similar errors are mapped consistently
4. **Graceful Handling**: Handle unexpected errors gracefully
5. **Detailed Logging**: Log detailed error information for debugging