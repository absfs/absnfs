---
layout: default
title: FileHandleMap
---

# FileHandleMap

The `FileHandleMap` is a critical internal component of ABSNFS that manages the mapping between NFS file handles and ABSFS filesystem paths and nodes.

## Purpose

In NFS, clients identify files and directories using opaque file handles rather than paths. The `FileHandleMap` serves several important purposes:

1. **Handle Generation**: Creates unique file handles for files and directories
2. **Handle-to-Node Mapping**: Maps file handles to internal `NFSNode` representations
3. **Handle Persistence**: Ensures file handles remain valid across server restarts
4. **Handle Cleanup**: Manages the lifecycle of handles, releasing resources when appropriate

## Type Definition

```go
type FileHandleMap struct {
    // contains filtered or unexported fields
}
```

The `FileHandleMap` type is used internally by the `AbsfsNFS` type and is not typically created or manipulated directly by users.

## NFS File Handles

NFS file handles are opaque identifiers that:

1. Are passed from the server to clients
2. Are presented by clients in subsequent operations
3. Must uniquely identify a file or directory
4. Should persist across server restarts if possible
5. Must be secure (cannot be easily forged)

In ABSNFS, file handles include:

- A unique identifier for the file or directory
- A generation number to detect stale handles
- Security information to prevent forgery
- Additional metadata for efficient lookup

## Key Operations

The `FileHandleMap` provides several key operations:

### GetNode

```go
func (fm *FileHandleMap) GetNode(handle FileHandle) (*NFSNode, error)
```

Looks up the `NFSNode` corresponding to a file handle. Returns an error if the handle is invalid or stale.

### CreateHandle

```go
func (fm *FileHandleMap) CreateHandle(node *NFSNode) (FileHandle, error)
```

Creates a new file handle for an `NFSNode`. This is called when a client accesses a file or directory for the first time.

### ReleaseHandle

```go
func (fm *FileHandleMap) ReleaseHandle(handle FileHandle) error
```

Releases a file handle, potentially freeing resources when the handle is no longer needed.

### InvalidateHandle

```go
func (fm *FileHandleMap) InvalidateHandle(handle FileHandle) error
```

Marks a file handle as invalid. This is called when a file or directory is deleted or renamed.

## Handle Lifecycle

File handles follow a lifecycle:

1. **Creation**: When a client first accesses a file or directory
2. **Usage**: When a client performs operations using the handle
3. **Invalidation**: When a file or directory is deleted or renamed
4. **Release**: When a client is done using the handle or the server needs to reclaim resources

## Implementation Details

The `FileHandleMap` implementation includes several important details:

### Thread Safety

The `FileHandleMap` is thread-safe, allowing concurrent access from multiple clients.

### Handle Generation

File handles are generated using:
- Cryptographic techniques to ensure uniqueness and security
- Metadata to aid in efficient lookup
- Generation numbers to detect stale handles

### Handle Persistence

For handles to persist across server restarts, the `FileHandleMap` can:
- Generate handles deterministically based on file metadata
- Store handle mappings in persistent storage
- Reconstruct mappings on server restart

### Handle Validity Checking

The `FileHandleMap` performs several validity checks on handles:
- Ensures the handle format is correct
- Verifies security information to prevent forgery
- Checks that the referenced file or directory still exists
- Validates generation numbers to detect stale handles

## Performance Considerations

The `FileHandleMap` is optimized for performance in several ways:

1. **Caching**: Frequently used handle mappings are cached
2. **Efficient Lookup**: Handle design enables efficient lookup
3. **Concurrency**: Handle operations can proceed concurrently
4. **Resource Management**: Handles are released when no longer needed

## Security Considerations

The `FileHandleMap` includes several security features:

1. **Handle Verification**: Handles are verified for authenticity
2. **Unpredictable Handles**: Handles are designed to be difficult to guess
3. **Stale Handle Detection**: Stale handles are detected and rejected
4. **Handle Invalidation**: Handles for deleted files are invalidated

## Example Usage

While users don't typically interact with `FileHandleMap` directly, here's an example of how it's used internally:

```go
// When a client wants to access a file
func (nfs *AbsfsNFS) handleLookup(dirHandle FileHandle, name string) (FileHandle, *NFSAttributes, error) {
    // Get the directory node
    dirNode, err := nfs.fileHandleMap.GetNode(dirHandle)
    if err != nil {
        return nil, nil, err
    }
    
    // Look up the child node
    childNode, err := dirNode.Lookup(name)
    if err != nil {
        return nil, nil, err
    }
    
    // Create a file handle for the child node
    childHandle, err := nfs.fileHandleMap.CreateHandle(childNode)
    if err != nil {
        return nil, nil, err
    }
    
    // Get the attributes for the child node
    attrs, err := childNode.GetAttributes()
    if err != nil {
        return nil, nil, err
    }
    
    return childHandle, attrs, nil
}
```

## Relation to Other Components

The `FileHandleMap` interacts closely with several other components in ABSNFS:

- **AbsfsNFS**: Coordinates overall NFS operations
- **NFSNode**: Represents files and directories in the filesystem
- **Server**: Handles network communication and client requests