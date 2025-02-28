---
layout: default
title: NFSNode
---

# NFSNode

The `NFSNode` type represents a file or directory in the NFS filesystem hierarchy. It maps between NFS file handles and the actual files and directories in the underlying ABSFS filesystem.

## Type Definition

```go
type NFSNode struct {
    // contains filtered or unexported fields
}
```

The `NFSNode` type is primarily used internally by the `AbsfsNFS` type and is not typically created or manipulated directly by users.

## Purpose

The `NFSNode` serves several important purposes in the ABSNFS system:

1. **File Handle Mapping**: Maps between NFS file handles and filesystem paths
2. **Attribute Caching**: Caches file attributes for performance
3. **Hierarchy Management**: Maintains parent-child relationships for directories
4. **Reference Counting**: Tracks usage to manage resource cleanup

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

## Methods

Most methods of `NFSNode` are internal and not exposed as part of the public API. The key operations include:

### Lookup

Looks up a child node by name within a directory node.

### GetAttributes

Retrieves the cached attributes for the node, refreshing them if necessary.

### SetAttributes

Updates the attributes of a node (and the underlying file).

### ReleaseNode

Decreases the reference count for a node, potentially releasing resources when no longer needed.

## Lifecycle

The lifecycle of an `NFSNode` is managed automatically by the `AbsfsNFS` system:

1. **Creation**: Nodes are created when a file or directory is first accessed
2. **Reference Counting**: References are tracked to manage the node's lifetime
3. **Caching**: Nodes are cached to improve performance for repeated access
4. **Release**: Nodes are released when no longer referenced

## Example Internal Usage

While users don't typically interact with `NFSNode` directly, here's an example of how it's used internally:

```go
// When a client requests a file
func (nfs *AbsfsNFS) handleLookup(handle NFSFileHandle, name string) (NFSNode, error) {
    // Look up the parent node
    parentNode, err := nfs.fileHandleMap.GetNode(handle)
    if err != nil {
        return nil, err
    }
    
    // Look up the child node
    childNode, err := parentNode.Lookup(name)
    if err != nil {
        return nil, err
    }
    
    // Return the child node
    return childNode, nil
}
```

## Implementation Details

The `NFSNode` implementation includes several important details:

### Thread Safety

`NFSNode` operations are thread-safe, allowing concurrent access from multiple clients.

### Cache Validation

Attribute caching includes validation logic to ensure that cached attributes are not stale.

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

## Relation to Other Types

`NFSNode` interacts closely with several other types in the ABSNFS system:

- **AbsfsNFS**: Coordinates overall NFS operations
- **FileHandleMap**: Maps between file handles and nodes
- **AttrCache**: Caches file attributes for performance