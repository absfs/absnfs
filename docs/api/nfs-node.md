---
layout: default
title: NFSNode
---

# NFSNode

The `NFSNode` type represents a file or directory in the NFS filesystem. It implements the `absfs.File` interface, providing a wrapper around a filesystem path that can be used with NFS operations.

## Type Definition

```go
type NFSNode struct {
    absfs.FileSystem
    path     string
    fileId   uint64
    attrs    *NFSAttrs
    children map[string]*NFSNode
}
```

The `NFSNode` type is primarily used internally by the `AbsfsNFS` type and is not typically created or manipulated directly by users.

## Purpose

The `NFSNode` serves several important purposes in the ABSNFS system:

1. **absfs.File Implementation**: Implements the `absfs.File` interface for compatibility with the FileHandleMap
2. **Path Management**: Wraps a filesystem path and provides file operations
3. **Attribute Caching**: Caches file attributes for performance
4. **Directory Hierarchy**: Maintains references to child nodes for directory navigation

## absfs.File Interface Implementation

`NFSNode` implements all 18 methods of the `absfs.File` interface:

### Read Operations

#### Close

```go
func (n *NFSNode) Close() error
```

Closes the node. For NFSNode, this is a no-op as the node doesn't maintain an open file descriptor.

#### Read

```go
func (n *NFSNode) Read(p []byte) (int, error)
```

Reads data from the file into the provided byte slice. Opens the file, reads data, and closes it.

#### ReadAt

```go
func (n *NFSNode) ReadAt(p []byte, off int64) (int, error)
```

Reads data from the file at the specified offset. Opens the file, reads at the offset, and closes it.

#### Seek

```go
func (n *NFSNode) Seek(offset int64, whence int) (int64, error)
```

Seeks to a position in the file. Opens the file, performs the seek, and closes it.

#### Stat

```go
func (n *NFSNode) Stat() (os.FileInfo, error)
```

Returns file information for the node's path.

### Write Operations

#### Write

```go
func (n *NFSNode) Write(p []byte) (int, error)
```

Writes data to the file. Opens the file for writing, writes data, invalidates cached attributes, and closes it.

#### WriteAt

```go
func (n *NFSNode) WriteAt(p []byte, off int64) (int, error)
```

Writes data to the file at the specified offset. Opens the file, writes at offset, invalidates cached attributes, and closes it.

#### WriteString

```go
func (n *NFSNode) WriteString(s string) (int, error)
```

Writes a string to the file. Converts the string to bytes and calls `Write()`.

#### Truncate

```go
func (n *NFSNode) Truncate(size int64) error
```

Truncates or extends the file to the specified size. Invalidates cached attributes.

#### Sync

```go
func (n *NFSNode) Sync() error
```

Syncs file changes to disk. For NFSNode, this checks if the file exists.

### Directory Operations

#### Readdir

```go
func (n *NFSNode) Readdir(count int) ([]os.FileInfo, error)
```

Reads directory entries. Opens the directory, reads entries, filters out "." and "..", and closes it.

#### Readdirnames

```go
func (n *NFSNode) Readdirnames(count int) ([]string, error)
```

Reads directory entry names. Calls `Readdir()` and extracts the names.

### Metadata Operations

#### Name

```go
func (n *NFSNode) Name() string
```

Returns the base name of the file or directory. Returns "/" for the root path.

#### Chdir

```go
func (n *NFSNode) Chdir() error
```

Changes the current directory to this node's path.

#### Chmod

```go
func (n *NFSNode) Chmod(mode os.FileMode) error
```

Changes the file mode/permissions. Invalidates cached attributes.

#### Chown

```go
func (n *NFSNode) Chown(uid, gid int) error
```

Changes the file owner and group. Invalidates cached attributes.

#### Chtimes

```go
func (n *NFSNode) Chtimes(atime time.Time, mtime time.Time) error
```

Changes the access and modification times. Invalidates cached attributes.

## Implementation Details

The `NFSNode` implementation includes several important details:

### Stateless Operations

Unlike traditional file handles, `NFSNode` doesn't maintain open file descriptors. Each operation opens the file, performs the operation, and closes it. This approach:
- Simplifies resource management
- Prevents file descriptor exhaustion
- Ensures operations always see the current file state

### Attribute Invalidation

Write operations automatically invalidate cached attributes by calling `attrs.Invalidate()`. This ensures that subsequent attribute queries return fresh data after modifications.

### Directory Filtering

The `Readdir()` method automatically filters out "." and ".." entries, which are not typically needed in NFS operations and can cause confusion.

### Path Management

The node stores its path and uses the embedded `FileSystem` to perform operations. The `Name()` method returns the base name, with special handling for the root path ("/").

## Usage Pattern

`NFSNode` instances are typically created and stored in the `FileHandleMap`:

```go
// Create a node for a path
node := &NFSNode{
    FileSystem: fs,
    path:       "/path/to/file",
    attrs:      &NFSAttrs{},
}

// Allocate a handle
handle := fileHandleMap.Allocate(node)

// Later, retrieve and use the node
file, ok := fileHandleMap.Get(handle)
if ok {
    // file is the NFSNode, which implements absfs.File
    data, err := file.ReadAt(buffer, offset)
}
```

## Performance Considerations

The `NFSNode` design makes trade-offs for simplicity and correctness:

**Advantages:**
1. **No file descriptor leaks**: Files are opened and closed for each operation
2. **Consistent state**: Always operates on the current file state
3. **Simple lifecycle**: No complex cleanup or resource tracking

**Trade-offs:**
1. **Open/close overhead**: Each operation incurs file open/close costs
2. **No seek state**: Cannot maintain a file position between operations (uses ReadAt/WriteAt instead)

For performance-critical scenarios, the read-ahead buffer and attribute cache mitigate these costs.

## Relation to Other Components

`NFSNode` interacts closely with several other components in ABSNFS:

- **absfs.FileSystem**: Embedded to provide filesystem operations
- **FileHandleMap**: Stores NFSNode instances mapped to handles
- **NFSAttrs**: Caches file attributes with invalidation support
- **AbsfsNFS**: Creates and manages NFSNode instances