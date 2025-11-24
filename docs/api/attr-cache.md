---
layout: default
title: AttrCache
---

# AttrCache

The `AttrCache` component provides caching of file attributes (metadata) to improve performance by reducing the number of filesystem operations required to retrieve file information.

## Purpose

File attribute retrieval is one of the most common operations in an NFS server. The `AttrCache` serves several important purposes:

1. **Performance Improvement**: Reduces the number of filesystem calls
2. **Latency Reduction**: Provides faster response times for attribute requests
3. **Load Reduction**: Decreases the load on the underlying filesystem
4. **Consistency Management**: Ensures clients see consistent attribute information

## Type Definition

```go
type AttrCache struct {
    // contains filtered or unexported fields
}
```

The `AttrCache` type is used internally by the `AbsfsNFS` type and is not typically created or manipulated directly by users.

## Cached Attributes

The `AttrCache` caches file attributes in an `NFSAttrs` structure:

1. **Basic Attributes**:
   - File mode (permissions)
   - File size

2. **Timestamps**:
   - Access time (Atime)
   - Modification time (Mtime)

3. **Ownership**:
   - User ID (Uid)
   - Group ID (Gid)

## Key Operations

The `AttrCache` provides several key operations:

### NewAttrCache

```go
func NewAttrCache(ttl time.Duration, maxSize int) *AttrCache
```

Creates a new attribute cache with the specified TTL (time-to-live) and maximum number of entries.

### Get

```go
func (c *AttrCache) Get(path string, server ...*AbsfsNFS) *NFSAttrs
```

Retrieves the cached attributes for a file or directory. If the attributes are not in the cache or are expired, returns nil. Optionally accepts an `AbsfsNFS` server instance for recording cache hit/miss metrics.

### Put

```go
func (c *AttrCache) Put(path string, attrs *NFSAttrs)
```

Adds or updates cached attributes for the specified path. Uses LRU eviction when the cache is full.

### Invalidate

```go
func (c *AttrCache) Invalidate(path string)
```

Removes the cached attributes for a file or directory, forcing the next `Get` call to return nil.

### Clear

```go
func (c *AttrCache) Clear()
```

Removes all entries from the cache.

### Size

```go
func (c *AttrCache) Size() int
```

Returns the current number of entries in the cache.

### MaxSize

```go
func (c *AttrCache) MaxSize() int
```

Returns the maximum size of the cache.

### Stats

```go
func (c *AttrCache) Stats() (int, int)
```

Returns the current size and capacity of the cache as a tuple.

## Cache Lifecycle

Cached attributes follow a lifecycle:

1. **Addition**: When attributes are first requested for a file or directory
2. **Usage**: When attributes are retrieved from the cache
3. **Invalidation**: When a file or directory is modified or the cache entry expires
4. **Refresh**: When fresh attributes are fetched after invalidation

## Implementation Details

The `AttrCache` implementation includes several important details:

### Cache Validation

Cached attributes are considered valid based on:
- A configurable time-to-live (TTL)
- Operation-specific invalidation (e.g., writes invalidate size and mtime)
- Explicit invalidation for operations like rename, remove, etc.

### Thread Safety

The `AttrCache` is thread-safe, allowing concurrent access from multiple clients.

### Memory Management

The cache implements memory management strategies:
- LRU (Least Recently Used) eviction to bound memory usage
- Size limits to prevent unbounded growth
- Targeted invalidation to maintain consistency

### Cache Coherency

To ensure cache coherency, the `AttrCache`:
- Invalidates entries affected by write operations
- Propagates modifications to related entries
- Respects the configured TTL for normal entries

## Performance Considerations

The `AttrCache` is optimized for performance in several ways:

1. **Fast Path**: Optimized code path for cache hits
2. **Concurrent Access**: Multiple readers can access the cache simultaneously
3. **Strategic Invalidation**: Only invalidates entries that are affected by modifications
4. **Batch Operations**: Supports batched attribute retrieval and update

## Configuration Options

The `AttrCache` can be configured through the `ExportOptions`:

```go
options := absnfs.ExportOptions{
    // How long attributes are considered valid in the cache
    AttrCacheTimeout: 5 * time.Second,
    
    // Maximum number of entries in the cache
    AttrCacheSize: 10000,
    
    // Whether to cache negative lookups
    AttrCacheNegative: true,
    
    // Timeout for negative cache entries
    AttrCacheNegativeTimeout: 1 * time.Second,
}
```

## Example Usage

While users don't typically interact with `AttrCache` directly, here's an example of how it's used internally:

```go
// When a client requests file attributes
func (nfs *AbsfsNFS) handleGetAttr(handle uint64) (*NFSAttrs, error) {
    // Get the file for the handle
    file, exists := nfs.fileHandleMap.Get(handle)
    if !exists {
        return nil, errors.New("invalid file handle")
    }

    // Get the attributes using the cache
    attrs := nfs.attrCache.Get(file.Name(), nfs)
    if attrs == nil {
        // Not in cache, need to fetch from filesystem
        return nil, errors.New("attributes not cached")
    }

    return attrs, nil
}

// When a client modifies a file
func (nfs *AbsfsNFS) handleWrite(handle uint64, offset int64, data []byte) (int, error) {
    // Get the file for the handle
    file, exists := nfs.fileHandleMap.Get(handle)
    if !exists {
        return 0, errors.New("invalid file handle")
    }

    // Write the data
    n, err := file.WriteAt(data, offset)
    if err != nil {
        return 0, err
    }

    // Invalidate the cached attributes since the file changed
    nfs.attrCache.Invalidate(file.Name())

    return n, nil
}
```

## Cache Statistics

The `AttrCache` maintains statistics to help with monitoring and tuning:

1. **Hit Count**: Number of successful cache hits
2. **Miss Count**: Number of cache misses requiring filesystem access
3. **Invalidation Count**: Number of cache invalidations
4. **Entry Count**: Current number of entries in the cache

## Relation to Other Components

The `AttrCache` interacts closely with several other components in ABSNFS:

- **AbsfsNFS**: Coordinates overall NFS operations and provides metrics recording
- **NFSAttrs**: The attribute structure that is cached
- **FileHandleMap**: Maps file handles to files whose paths are used for cache operations