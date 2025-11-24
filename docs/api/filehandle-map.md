---
layout: default
title: FileHandleMap
---

# FileHandleMap

The `FileHandleMap` is a critical internal component of ABSNFS that manages the mapping between numeric file handles and open `absfs.File` instances.

## Purpose

In NFS, clients identify open files and directories using numeric file handles rather than paths. The `FileHandleMap` serves several important purposes:

1. **Handle Allocation**: Creates unique numeric handles for open files and directories
2. **Handle-to-File Mapping**: Maps numeric handles to `absfs.File` instances
3. **Handle Release**: Manages the lifecycle of handles, closing files and releasing resources when appropriate
4. **Reference Tracking**: Tracks active file handles to manage resource cleanup

## Type Definition

```go
type FileHandleMap struct {
    sync.RWMutex
    handles    map[uint64]absfs.File
    lastHandle uint64
}
```

The `FileHandleMap` type is used internally by the `AbsfsNFS` type and is not typically created or manipulated directly by users.

## Handle Format

In this implementation, file handles are simple `uint64` integers that are allocated sequentially. The map maintains:

- A mapping from handle ID to the corresponding `absfs.File` instance
- The last allocated handle ID for efficient allocation
- Thread-safe access through read-write locks

## Key Operations

The `FileHandleMap` provides several key operations:

### Allocate

```go
func (fm *FileHandleMap) Allocate(f absfs.File) uint64
```

Creates a new file handle for the given `absfs.File`. The method finds the smallest available handle ID (starting from 1) and associates it with the provided file. Returns the allocated handle ID.

**Parameters:**
- `f`: The `absfs.File` instance to associate with the new handle

**Returns:**
- `uint64`: The allocated handle ID

### Get

```go
func (fm *FileHandleMap) Get(handle uint64) (absfs.File, bool)
```

Retrieves the `absfs.File` associated with the given handle ID. Returns the file and a boolean indicating whether the handle exists.

**Parameters:**
- `handle`: The handle ID to look up

**Returns:**
- `absfs.File`: The file associated with the handle (or nil if not found)
- `bool`: `true` if the handle exists, `false` otherwise

### Release

```go
func (fm *FileHandleMap) Release(handle uint64)
```

Releases a file handle, closing the associated file and removing it from the map. This should be called when the client is done with the file.

**Parameters:**
- `handle`: The handle ID to release

### ReleaseAll

```go
func (fm *FileHandleMap) ReleaseAll()
```

Closes and removes all file handles. This is typically called during server shutdown to ensure all files are properly closed.

### Count

```go
func (fm *FileHandleMap) Count() int
```

Returns the number of active file handles currently in the map.

**Returns:**
- `int`: The number of active handles

## Handle Lifecycle

File handles follow a simple lifecycle:

1. **Allocation**: When a client opens a file or directory, `Allocate()` is called with the `absfs.File` instance
2. **Usage**: The handle ID is used in subsequent NFS operations to reference the open file
3. **Release**: When the client closes the file, `Release()` is called to close the file and free the handle

## Implementation Details

The `FileHandleMap` implementation includes several important details:

### Thread Safety

The `FileHandleMap` is thread-safe through the use of `sync.RWMutex`, allowing concurrent access from multiple clients. Read operations (like `Get()` and `Count()`) use read locks, while write operations (like `Allocate()`, `Release()`, and `ReleaseAll()`) use write locks.

### Handle Allocation Strategy

File handles are allocated using a simple sequential strategy:
- Handles start at 1 (handle 0 is reserved/invalid)
- The allocator finds the smallest available handle ID
- This approach reuses handle IDs when files are closed
- The `lastHandle` field tracks the highest allocated handle for optimization

### Resource Management

The `FileHandleMap` manages file resources by:
- Automatically closing files when handles are released
- Ensuring all files are closed during `ReleaseAll()` (typically on server shutdown)
- Preventing resource leaks through proper cleanup

## Performance Considerations

The `FileHandleMap` is optimized for performance in several ways:

1. **Efficient Lookup**: Map-based lookups provide O(1) access time
2. **Concurrent Access**: Read-write locks allow multiple concurrent reads
3. **Handle Reuse**: Released handles can be reused, preventing unbounded growth
4. **Minimal Allocation**: Simple integer handles avoid complex serialization

## Example Usage

While users don't typically interact with `FileHandleMap` directly, here's an example of how it's used internally:

```go
// When a client opens a file
func (nfs *AbsfsNFS) handleOpen(path string, flags int) (uint64, error) {
    // Open the file using the underlying filesystem
    file, err := nfs.fs.OpenFile(path, flags, 0644)
    if err != nil {
        return 0, err
    }

    // Allocate a handle for the open file
    handle := nfs.fileHandleMap.Allocate(file)

    return handle, nil
}

// When a client reads from a file
func (nfs *AbsfsNFS) handleRead(handle uint64, offset int64, count int) ([]byte, error) {
    // Get the file from the handle
    file, ok := nfs.fileHandleMap.Get(handle)
    if !ok {
        return nil, os.ErrNotExist
    }

    // Read from the file
    buf := make([]byte, count)
    n, err := file.ReadAt(buf, offset)
    if err != nil && err != io.EOF {
        return nil, err
    }

    return buf[:n], nil
}

// When a client closes a file
func (nfs *AbsfsNFS) handleClose(handle uint64) error {
    // Release the handle (this also closes the file)
    nfs.fileHandleMap.Release(handle)
    return nil
}

// During server shutdown
func (nfs *AbsfsNFS) Shutdown() error {
    // Close all open files
    nfs.fileHandleMap.ReleaseAll()
    return nil
}
```

## Relation to Other Components

The `FileHandleMap` interacts closely with several other components in ABSNFS:

- **AbsfsNFS**: Uses the map to track open files for NFS operations
- **absfs.File**: The interface that all open files implement
- **Server**: Indirectly uses the map through NFS operations