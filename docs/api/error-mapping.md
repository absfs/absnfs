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
| `os.ErrNotExist` | `NFSERR_NOENT` | File or directory not found |
| `os.ErrPermission` | `NFSERR_ACCES` | Permission denied |
| `os.ErrExist` | `NFSERR_EXIST` | File already exists |
| Error from read-only filesystem | `NFSERR_ROFS` | Read-only file system |

### File Type Errors

| ABSFS/Go Error | NFS Error | Description |
|----------------|-----------|-------------|
| `syscall.EISDIR` | `NFSERR_ISDIR` | Is a directory |
| `syscall.ENOTDIR` | `NFSERR_NOTDIR` | Not a directory |

### Resource Errors

| ABSFS/Go Error | NFS Error | Description |
|----------------|-----------|-------------|
| `syscall.ENOSPC` | `NFSERR_NOSPC` | No space left on device |
| `syscall.EDQUOT` | `NFSERR_DQUOT` | Disk quota exceeded |
| File size limit errors | `NFSERR_FBIG` | File too large |

### Directory Errors

| ABSFS/Go Error | NFS Error | Description |
|----------------|-----------|-------------|
| `syscall.ENOTEMPTY` | `NFSERR_NOTEMPTY` | Directory not empty |
| `syscall.ENAMETOOLONG` | `NFSERR_NAMETOOLONG` | Filename too long |

### Operation Errors

| ABSFS/Go Error | NFS Error | Description |
|----------------|-----------|-------------|
| Generic I/O error | `NFSERR_IO` | I/O error |
| `os.ErrInvalid` | `NFSERR_INVAL` | Invalid argument |
| Other errors | `NFSERR_IO` | Default error mapping |

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
func mapToNFSError(err error, op string) uint32 {
    if err == nil {
        return NFS_OK
    }

    // Check for standard error conditions
    switch {
    case errors.Is(err, fs.ErrNotExist) || os.IsNotExist(err):
        return NFSERR_NOENT
    case errors.Is(err, fs.ErrPermission) || os.IsPermission(err):
        return NFSERR_ACCES
    case errors.Is(err, fs.ErrExist) || os.IsExist(err):
        return NFSERR_EXIST
    case errors.Is(err, os.ErrInvalid):
        return NFSERR_INVAL
    case errors.Is(err, syscall.EISDIR):
        return NFSERR_ISDIR
    case errors.Is(err, syscall.ENOTDIR):
        return NFSERR_NOTDIR
    case errors.Is(err, syscall.ENOTEMPTY):
        return NFSERR_NOTEMPTY
    case errors.Is(err, syscall.EROFS):
        return NFSERR_ROFS
    case errors.Is(err, syscall.ENOSPC):
        return NFSERR_NOSPC
    case errors.Is(err, syscall.ENAMETOOLONG):
        return NFSERR_NAMETOOLONG
    // ... other cases ...
    }

    // Operation-specific mappings
    switch op {
    case "READ":
        // Special handling for read operations
        if errors.Is(err, io.EOF) {
            return NFS_OK // EOF is not an error for NFS reads
        }
    case "LOOKUP":
        // Special handling for lookup operations
    // ... other operations ...
    }

    // Default to I/O error for unrecognized errors
    return NFSERR_IO
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