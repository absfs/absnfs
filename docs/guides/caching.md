---
layout: default
title: Caching
---

# Caching

This guide explains the caching mechanisms in ABSNFS and how to configure them for optimal performance and reliability.

## Understanding NFS Caching

Caching is essential for NFS performance. Without caching, every operation would require a network round-trip, resulting in poor performance. ABSNFS implements several types of caching:

1. **Attribute Caching**: Caches file metadata (size, permissions, timestamps, etc.)
2. **Negative Caching**: Caches lookup failures to avoid repeated filesystem queries for non-existent files
3. **Read-Ahead Buffering**: Prefetches data for sequential reads
4. **Directory Entry Caching**: Caches directory listings
5. **File Handle Caching**: Caches mappings between file handles and filesystem objects

Each type of caching has different implications for performance, consistency, and resource usage.

## Attribute Caching

Attribute caching stores file metadata to avoid repeated filesystem calls.

### How Attribute Caching Works

1. When a client requests file attributes, ABSNFS retrieves them from the filesystem
2. The attributes are stored in the cache with a timestamp
3. Subsequent requests for the same attributes are served from the cache if it's still valid
4. The cache entry expires after a configurable timeout
5. Certain operations (like writes) invalidate affected cache entries

### Configuring Attribute Caching

```go
options := absnfs.ExportOptions{
    // How long to cache attributes (in seconds)
    AttrCacheTimeout: 10 * time.Second,

    // Maximum number of cached entries
    AttrCacheSize: 10000,
}
```

### Attribute Caching Tradeoffs

- **Longer timeouts** improve performance but may cause clients to see stale data
- **Shorter timeouts** ensure freshness but increase filesystem operations
- **Larger cache sizes** improve hit rates but consume more memory

### Monitoring Attribute Cache Performance

To monitor attribute cache performance, check:

- **Hit rate**: Percentage of attribute requests served from cache
- **Miss rate**: Percentage of attribute requests requiring filesystem operations
- **Eviction rate**: How often entries are removed due to cache size limits
- **Invalidation rate**: How often entries are invalidated due to modifications

## Negative Caching

Negative caching stores lookup failures for non-existent files, reducing repeated filesystem queries.

### How Negative Caching Works

1. When a lookup fails (file not found), the server stores a negative cache entry
2. Subsequent lookups for the same path are served from the negative cache
3. The cache entry expires after a configurable timeout (typically shorter than positive entries)
4. Certain operations (like CREATE, MKDIR, RENAME) invalidate related negative cache entries

### Configuring Negative Caching

```go
options := absnfs.ExportOptions{
    // Enable negative lookup caching
    CacheNegativeLookups: true,

    // How long to cache negative entries (typically shorter than positive)
    NegativeCacheTimeout: 5 * time.Second,
}
```

### Negative Caching Tradeoffs

- **Enabling** reduces filesystem load for repeated lookups of non-existent files
- **Shorter timeouts** reduce the chance of stale negative entries
- **Disabling** ensures immediate visibility of newly created files but increases load

### When to Use Negative Caching

Negative caching is particularly beneficial for:

1. **Build systems**: Often probe for many files that may not exist
2. **Package managers**: Check for dependencies in multiple locations
3. **Application servers**: May look for configuration files in various paths
4. **Development environments**: IDEs and tools frequently check for files

### Monitoring Negative Cache Performance

To monitor negative cache performance, check:

- **Negative cache hit rate**: Percentage of not-found lookups served from cache
- **Negative cache size**: Number of negative entries currently cached
- **Invalidation rate**: How often negative entries are cleared due to file creation

### Example: Enabling for Development

For development environments with many probing lookups:

```go
options := absnfs.ExportOptions{
    CacheNegativeLookups: true,
    NegativeCacheTimeout: 5 * time.Second,  // Short timeout
    AttrCacheTimeout: 10 * time.Second,     // Normal timeout
    AttrCacheSize: 20000,
}
```

### Example: Balancing Freshness and Performance

For a typical workload with some modifications:

```go
options := absnfs.ExportOptions{
    AttrCacheTimeout: 5 * time.Second,  // Moderate timeout
    AttrCacheSize: 20000,               // Large enough for most workloads
}
```

For a read-heavy workload with few modifications:

```go
options := absnfs.ExportOptions{
    AttrCacheTimeout: 30 * time.Second, // Longer timeout
    AttrCacheSize: 50000,               // Very large cache
}
```

## Read-Ahead Buffering

Read-ahead buffering improves sequential read performance by prefetching data.

### How Read-Ahead Buffering Works

1. When a client performs a sequential read, ABSNFS detects the pattern
2. The server reads additional data beyond what was requested
3. This data is stored in a buffer
4. Subsequent read requests can be served from the buffer if the data is available
5. The process continues, staying ahead of the client's reading position

### Configuring Read-Ahead Buffering

```go
options := absnfs.ExportOptions{
    // Enable or disable read-ahead
    EnableReadAhead: true,

    // Size of the read-ahead buffer per file
    ReadAheadSize: 262144, // 256KB

    // Maximum number of files to buffer simultaneously
    ReadAheadMaxFiles: 100,

    // Maximum total memory for read-ahead buffers
    ReadAheadMaxMemory: 104857600, // 100MB
}
```

### Read-Ahead Buffering Tradeoffs

- **Larger buffers** improve sequential read performance but use more memory
- **More buffered files** help with concurrent access but increase memory usage
- **Enabling/disabling** can be toggled based on workload characteristics

### Monitoring Read-Ahead Performance

To monitor read-ahead performance, check:

- **Hit rate**: Percentage of read requests served from the buffer
- **Prefetch efficiency**: Ratio of prefetched data that was actually used
- **Buffer utilization**: How much of the allocated buffer space is in use
- **Memory usage**: Total memory consumed by read-ahead buffers

### Example: Optimizing for Video Streaming

For large sequential files like videos:

```go
options := absnfs.ExportOptions{
    EnableReadAhead: true,
    ReadAheadSize: 4194304, // 4MB
    ReadAheadMaxFiles: 20,  // Fewer files, larger buffers
}
```

### Example: Optimizing for Small Files

For many small files:

```go
options := absnfs.ExportOptions{
    EnableReadAhead: true,
    ReadAheadSize: 65536,   // 64KB
    ReadAheadMaxFiles: 200, // More files, smaller buffers
}
```

## File Handle Caching

File handle caching maintains mappings between NFS file handles and filesystem objects.

### How File Handle Caching Works

1. ABSNFS generates unique file handles for filesystem objects
2. These handles and their mappings to filesystem paths are cached
3. The cache allows quick translation between handles and filesystem objects
4. Handles remain valid for as long as the files exist
5. Handle cache entries are only invalidated when files are deleted or renamed

File handle caching is managed automatically by ABSNFS to ensure efficient handle-to-path mapping.

## Cache Consistency

Cache consistency is a critical consideration in NFS. ABSNFS implements several mechanisms to maintain consistency:

### Timestamp-Based Validation

Cached entries are tagged with timestamps and expire after a configurable timeout:

```go
options := absnfs.ExportOptions{
    AttrCacheTimeout: 5 * time.Second,
}
```

### Write-Through Invalidation

When files are modified, related cache entries are invalidated:

1. Write operations invalidate attribute cache entries for the affected file
2. Directory modifications (create, remove, rename) invalidate affected cache entries

### Close-To-Open Consistency

ABSNFS supports NFS's "close-to-open" consistency model, where:

1. When a client closes a file after writing, changes are committed to the server
2. When another client opens the file, it sees the updated content
3. This model provides reasonable consistency for most applications

### Cache Coherency Limitations

It's important to understand the limitations of NFS caching:

1. Changes may not be immediately visible to all clients due to caching
2. There is no built-in distributed locking in NFSv3 (separate NLM protocol)
3. Applications requiring strong consistency should use additional synchronization

## Advanced Caching Strategies

### Memory Pressure Handling

ABSNFS can respond to memory pressure by adjusting cache sizes:

```go
options := absnfs.ExportOptions{
    // Enable memory pressure detection
    AdaptToMemoryPressure: true,
    
    // Memory thresholds for adaptation
    MemoryHighWatermark: 0.8, // 80% of available memory
    MemoryLowWatermark: 0.6,  // 60% of available memory
}
```

## Cache Monitoring and Tuning

### Collecting Cache Statistics

ABSNFS provides statistics to help monitor cache performance:

```go
// Get metrics
metrics := server.GetMetrics()

fmt.Printf("Cache hit rate: %.2f%%\n",
    metrics.CacheHitRate*100)
fmt.Printf("Read-ahead hit rate: %.2f%%\n",
    metrics.ReadAheadHitRate*100)
fmt.Printf("Attribute cache size: %d entries\n",
    metrics.AttrCacheSize)
```

### Identifying Cache Performance Issues

Common cache-related performance issues include:

1. **Low hit rates**: Cache timeouts may be too short or cache sizes too small
2. **High memory usage**: Cache sizes may be too large for available memory
3. **Cache thrashing**: Frequent invalidations may indicate conflicting access patterns
4. **Stale data**: Long timeouts may cause clients to see outdated information

### Tuning Cache Parameters

Follow these steps to tune cache parameters:

1. **Measure baseline performance** with default settings
2. **Identify bottlenecks** using cache statistics
3. **Adjust parameters** to address specific issues
4. **Re-measure performance** to validate improvements
5. **Iterate** until achieving optimal performance

## Caching Recommendations by Workload

### Read-Heavy Workloads

For workloads with many reads and few writes:

```go
options := absnfs.ExportOptions{
    // Aggressive attribute caching
    AttrCacheTimeout: 30 * time.Second,
    AttrCacheSize: 50000,

    // Aggressive read-ahead
    EnableReadAhead: true,
    ReadAheadSize: 1048576, // 1MB
}
```

### Write-Heavy Workloads

For workloads with frequent writes:

```go
options := absnfs.ExportOptions{
    // Conservative attribute caching
    AttrCacheTimeout: 2 * time.Second,
    AttrCacheSize: 10000,

    // Minimal read-ahead
    EnableReadAhead: true,
    ReadAheadSize: 131072, // 128KB
}
```

### Mixed Workloads

For balanced workloads:

```go
options := absnfs.ExportOptions{
    // Moderate attribute caching
    AttrCacheTimeout: 5 * time.Second,
    AttrCacheSize: 20000,

    // Moderate read-ahead
    EnableReadAhead: true,
    ReadAheadSize: 262144, // 256KB
}
```

### Large File Streaming

For streaming large files (video, backup, etc.):

```go
options := absnfs.ExportOptions{
    // Minimal attribute caching (few files)
    AttrCacheTimeout: 10 * time.Second,
    AttrCacheSize: 1000,

    // Very aggressive read-ahead
    EnableReadAhead: true,
    ReadAheadSize: 8388608, // 8MB
    ReadAheadMaxFiles: 10,  // Fewer files, larger buffers
}
```

### Small File Access

For workloads with many small files:

```go
options := absnfs.ExportOptions{
    // Aggressive attribute caching
    AttrCacheTimeout: 15 * time.Second,
    AttrCacheSize: 100000,

    // Minimal read-ahead
    EnableReadAhead: true,
    ReadAheadSize: 65536, // 64KB
    ReadAheadMaxFiles: 500, // Many files, smaller buffers
}
```

## Conclusion

Proper cache configuration is essential for NFS performance. By understanding the different caching mechanisms and their tradeoffs, you can optimize ABSNFS for your specific workload.

Key takeaways:
1. **Attribute caching** reduces metadata operations
2. **Read-ahead buffering** improves sequential read performance
3. **File handle caching** maintains efficient handle-to-path mapping
4. **Cache consistency** mechanisms ensure data integrity
5. **Workload-specific tuning** provides the best performance

When configuring caching, always consider the balance between performance and freshness, memory usage, and the specific characteristics of your workload.

## Next Steps

- [Performance Tuning](./performance-tuning.md): Further optimize your NFS server
- [Monitoring](./monitoring.md): Set up comprehensive monitoring
- [Client Compatibility](./client-compatibility.md): Ensure compatibility with different clients