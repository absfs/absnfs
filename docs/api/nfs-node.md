---
layout: default
title: NFSNode
---

# NFSNode

The `NFSNode` type represents a file or directory in the NFS filesystem hierarchy. It maps between NFS file handles and the actual files and directories in the underlying ABSFS filesystem.

**NFSNode implements the `absfs.File` interface**, providing all standard file operations for NFS clients.

## Type Definition

```go
type NFSNode struct {
    // contains filtered or unexported fields
}
```

## Purpose

The `NFSNode` serves several important purposes in the ABSNFS system:

1. **absfs.File Implementation**: Provides the full absfs.File interface for NFS operations
2. **File Handle Mapping**: Maps between NFS file handles and filesystem paths
3. **Attribute Caching**: Caches file attributes for performance
4. **Hierarchy Management**: Maintains parent-child relationships for directories
5. **Reference Counting**: Tracks usage to manage resource cleanup

## Interface Implementation

NFSNode implements the complete `absfs.File` interface with the following methods:

### File I/O Operations

#### Close

```go
func (n *NFSNode) Close() error
```

Closes the node. Currently a no-op as NFSNode manages underlying file handles internally.

#### Read

```go
func (n *NFSNode) Read(p []byte) (int, error)
```

Reads data from the file into the provided byte slice. Opens the file, performs the read, and closes it.

#### ReadAt

```go
func (n *NFSNode) ReadAt(p []byte, off int64) (int, error)
```

Reads data from the file at the specified offset into the provided byte slice.

#### Write

```go
func (n *NFSNode) Write(p []byte) (int, error)
```

Writes data from the provided byte slice to the file. Invalidates the attribute cache after writing.

#### WriteAt

```go
func (n *NFSNode) WriteAt(p []byte, off int64) (int, error)
```

Writes data from the provided byte slice to the file at the specified offset. Invalidates the attribute cache after writing.

#### WriteString

```go
func (n *NFSNode) WriteString(s string) (int, error)
```

Writes a string to the file. Equivalent to Write([]byte(s)).

#### Seek

```go
func (n *NFSNode) Seek(offset int64, whence int) (int64, error)
```

Sets the offset for the next Read or Write operation. The whence parameter can be:
- 0 (io.SeekStart): relative to the start of the file
- 1 (io.SeekCurrent): relative to the current offset
- 2 (io.SeekEnd): relative to the end of the file

### Directory Operations

#### Readdir

```go
func (n *NFSNode) Readdir(count int) ([]os.FileInfo, error)
```

Reads directory entries. If count > 0, reads at most count entries. If count <= 0, reads all entries. Filters out "." and ".." entries.

#### Readdirnames

```go
func (n *NFSNode) Readdirnames(count int) ([]string, error)
```

Reads directory entry names. If count > 0, reads at most count names. If count <= 0, reads all names. Filters out "." and ".." entries.

### Metadata Operations

#### Name

```go
func (n *NFSNode) Name() string
```

Returns the base name of the file or directory. For the root directory, returns "/".

#### Stat

```go
func (n *NFSNode) Stat() (os.FileInfo, error)
```

Returns file information (os.FileInfo) for the node, including:
- File type (regular file, directory, symlink, etc.)
- File size
- Timestamps (modification, access)
- Permissions
- Owner and group

#### Sync

```go
func (n *NFSNode) Sync() error
```

Ensures that any written data is committed to stable storage. Currently validates that the file exists.

#### Truncate

```go
func (n *NFSNode) Truncate(size int64) error
```

Changes the size of the file. If the file is larger than size, the extra data is lost. If the file is smaller, it is extended with zero bytes. Invalidates the attribute cache after truncating.

### Permission and Ownership Operations

#### Chdir

```go
func (n *NFSNode) Chdir() error
```

Changes the current working directory to this node's path. Only valid for directory nodes.

#### Chmod

```go
func (n *NFSNode) Chmod(mode os.FileMode) error
```

Changes the permissions of the file or directory. Invalidates the attribute cache after changing permissions.

#### Chown

```go
func (n *NFSNode) Chown(uid, gid int) error
```

Changes the owner and group of the file or directory. Invalidates the attribute cache after changing ownership.

#### Chtimes

```go
func (n *NFSNode) Chtimes(atime time.Time, mtime time.Time) error
```

Changes the access and modification times of the file or directory. Invalidates the attribute cache after changing times.

## Key Properties

### Path

Each `NFSNode` is associated with a path in the underlying filesystem. This path is used to perform operations on the file or directory.

### Attributes

The `NFSNode` caches file attributes (metadata) such as:
- File type (regular file, directory, symlink, etc.)
- File size
- Timestamps (creation, modification, access)
- Permissions
- Owner and group

### Children

For directory nodes, the `NFSNode` maintains references to child nodes that have been accessed. This helps with efficient lookups and navigation of the filesystem hierarchy.

## Lifecycle

The lifecycle of an `NFSNode` is managed automatically by the `AbsfsNFS` system:

1. **Creation**: Nodes are created when a file or directory is first accessed
2. **Reference Counting**: References are tracked to manage the node's lifetime
3. **Caching**: Nodes are cached to improve performance for repeated access
4. **Release**: Nodes are released when no longer referenced

## Implementation Details

### Thread Safety

`NFSNode` operations are thread-safe, allowing concurrent access from multiple clients. A mutex protects the attribute cache and other shared state.

### Cache Validation

Attribute caching includes validation logic to ensure that cached attributes are not stale. Write operations (Write, WriteAt, Truncate, Chmod, Chown, Chtimes) automatically invalidate the cache.

### Path Normalization

Paths are normalized to ensure consistent lookup and comparison.

### Reference Counting

A reference counting system ensures that nodes are properly cleaned up when no longer needed.

## Performance Considerations

The `NFSNode` type is optimized for performance in several ways:

1. **Attribute Caching**: Reduces the need to query the filesystem for frequently accessed attributes
2. **Hierarchy Caching**: Maintains a cache of recently accessed nodes to speed up lookups
3. **Lazy Loading**: Child nodes are loaded on demand rather than all at once
4. **Reference Counting**: Efficient management of node lifecycle to avoid memory leaks
5. **File Handle Management**: Opens and closes underlying files as needed to avoid resource exhaustion

## Relation to Other Types

`NFSNode` interacts closely with several other types in the ABSNFS system:

- **AbsfsNFS**: Coordinates overall NFS operations and manages the node lifecycle
- **FileHandleMap**: Maps between file handles and nodes
- **AttrCache**: Caches file attributes for performance
- **absfs.FileSystem**: Provides the underlying filesystem operations
