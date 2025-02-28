---
layout: default
title: Performance Tuning
---

# Performance Tuning

This guide covers techniques for optimizing ABSNFS performance to achieve the best possible throughput, latency, and resource utilization for your specific workloads.

## Understanding NFS Performance Factors

NFS performance is influenced by several factors:

1. **Network Bandwidth & Latency**: The capacity and delay of the network connection
2. **Filesystem Performance**: The speed of the underlying filesystem
3. **Client Concurrency**: The number of simultaneous clients and operations
4. **Caching Strategy**: How effectively data and metadata are cached
5. **Operation Patterns**: Read vs. write, sequential vs. random access
6. **Resource Constraints**: CPU, memory, and I/O limitations

## Basic Performance Configuration

ABSNFS provides several configuration options that affect performance:

```go
options := absnfs.ExportOptions{
    // Read-ahead buffering for sequential reads
    EnableReadAhead: true,
    ReadAheadSize: 524288, // 512KB
    
    // Attribute caching for faster metadata operations
    AttrCacheTimeout: 10 * time.Second,
    
    // Transfer size for read/write operations
    TransferSize: 262144, // 256KB
    
    // Connection handling
    MaxConnections: 100,
    IdleTimeout: 5 * time.Minute,
}
```

## Optimizing for Different Workloads

### Read-Heavy Workloads

For workloads with primarily read operations:

```go
options := absnfs.ExportOptions{
    // Aggressive read-ahead for sequential access
    EnableReadAhead: true,
    ReadAheadSize: 1048576, // 1MB
    
    // Longer attribute caching
    AttrCacheTimeout: 30 * time.Second,
    
    // Larger transfer size
    TransferSize: 524288, // 512KB
}
```

This configuration:
- Prefetches more data to optimize sequential reads
- Caches attributes longer to reduce filesystem operations
- Uses larger transfers to reduce protocol overhead

### Write-Heavy Workloads

For workloads with significant write operations:

```go
options := absnfs.ExportOptions{
    // Disable read-ahead (not useful for writes)
    EnableReadAhead: false,
    
    // Shorter attribute caching (to see updates sooner)
    AttrCacheTimeout: 5 * time.Second,
    
    // Larger transfer size for writes
    TransferSize: 524288, // 512KB
    
    // More connections for concurrent writes
    MaxConnections: 200,
}
```

This configuration:
- Focuses resources on write operations
- Keeps attribute cache fresh for frequently changing files
- Allows more concurrent clients

### Mixed Workloads

For balanced workloads with both reads and writes:

```go
options := absnfs.ExportOptions{
    // Moderate read-ahead
    EnableReadAhead: true,
    ReadAheadSize: 262144, // 256KB
    
    // Moderate attribute caching
    AttrCacheTimeout: 10 * time.Second,
    
    // Balanced transfer size
    TransferSize: 262144, // 256KB
}
```

This configuration provides a good balance for mixed operations.

### Small File Workloads

For workloads with many small files:

```go
options := absnfs.ExportOptions{
    // Small read-ahead (small files don't benefit from large read-ahead)
    EnableReadAhead: true,
    ReadAheadSize: 65536, // 64KB
    
    // Aggressive attribute caching (metadata operations dominate)
    AttrCacheTimeout: 60 * time.Second,
    AttrCacheSize: 100000, // Cache more entries
    
    // Smaller transfer size (matching typical file size)
    TransferSize: 65536, // 64KB
}
```

This configuration:
- Optimizes for metadata operations
- Caches more file attributes
- Uses smaller transfers matching the typical file size

### Large File Workloads

For workloads with large files:

```go
options := absnfs.ExportOptions{
    // Large read-ahead for sequential access to large files
    EnableReadAhead: true,
    ReadAheadSize: 4194304, // 4MB
    
    // Minimal attribute caching (few files, mostly data operations)
    AttrCacheTimeout: 5 * time.Second,
    
    // Very large transfer size
    TransferSize: 1048576, // 1MB
}
```

This configuration:
- Maximizes throughput for large files
- Focuses resources on data transfer rather than metadata

## Memory Optimization

Memory usage is a critical factor in NFS server performance. Here's how to optimize it:

### Controlling Cache Size

```go
options := absnfs.ExportOptions{
    // Limit attribute cache size
    AttrCacheSize: 10000,
    
    // Limit read-ahead buffer memory
    ReadAheadMaxFiles: 100,
    ReadAheadMaxMemory: 104857600, // 100MB total
}
```

### Memory Pressure Detection

ABSNFS can adapt to memory pressure:

```go
options := absnfs.ExportOptions{
    // Enable memory pressure detection
    AdaptToMemoryPressure: true,
    
    // Set high and low watermarks
    MemoryHighWatermark: 0.8, // 80% of available memory
    MemoryLowWatermark: 0.6,  // 60% of available memory
}
```

When memory usage exceeds the high watermark, ABSNFS will:
1. Reduce cache sizes
2. Trim read-ahead buffers
3. Force garbage collection
4. Delay accepting new connections

## CPU Optimization

CPU usage can be optimized with these settings:

```go
options := absnfs.ExportOptions{
    // Control number of worker goroutines
    MaxWorkers: runtime.NumCPU() * 4,
    
    // Batch operations when possible
    BatchOperations: true,
    MaxBatchSize: 10,
}
```

This balances:
- Parallelism (utilizing multiple CPU cores)
- Overhead (avoiding excessive goroutine creation)
- Batching (reducing per-operation overhead)

## Network Optimization

Network configuration significantly impacts NFS performance:

```go
options := absnfs.ExportOptions{
    // TCP configurations
    TCPKeepAlive: true,
    TCPNoDelay: true,
    
    // Connection limits
    MaxConnections: 500,
    
    // Buffer sizes
    SendBufferSize: 262144, // 256KB
    ReceiveBufferSize: 262144, // 256KB
}
```

These settings:
- Keep connections alive to avoid reconnection overhead
- Disable Nagle's algorithm for lower latency
- Control the number of simultaneous connections
- Configure network buffer sizes for optimal throughput

## Filesystem-Specific Optimizations

Different filesystem implementations have different performance characteristics:

### In-Memory Filesystem (memfs)

```go
// Create memory filesystem with tuned parameters
memfsOptions := memfs.Options{
    EnableMmap: true,
    PreallocateFiles: true,
    DefaultBlockSize: 65536, // 64KB
}
fs, _ := memfs.NewFSWithOptions(memfsOptions)

// NFS options optimized for in-memory filesystem
nfsOptions := absnfs.ExportOptions{
    // Memory filesystems are very fast, so optimize for throughput
    EnableReadAhead: true,
    ReadAheadSize: 2097152, // 2MB
    TransferSize: 1048576, // 1MB
}
```

### OS Filesystem (osfs)

```go
// Create OS filesystem
fs, _ := osfs.NewFS("/path/to/data")

// NFS options optimized for OS filesystem
nfsOptions := absnfs.ExportOptions{
    // OS filesystems benefit from caching
    EnableReadAhead: true,
    ReadAheadSize: 524288, // 512KB
    AttrCacheTimeout: 15 * time.Second,
    
    // Use moderate transfer size
    TransferSize: 262144, // 256KB
}
```

## Performance Measurement

To effectively tune performance, you need to measure it:

### Built-in Metrics

ABSNFS provides metrics that can help identify bottlenecks:

```go
// Get metrics snapshot
metrics := nfsServer.GetMetrics()

fmt.Printf("Total operations: %d\n", metrics.TotalOperations)
fmt.Printf("Read operations: %d\n", metrics.ReadOperations)
fmt.Printf("Write operations: %d\n", metrics.WriteOperations)
fmt.Printf("Cache hit rate: %.2f%%\n", metrics.CacheHitRate*100)
fmt.Printf("Average read latency: %v\n", metrics.AvgReadLatency)
fmt.Printf("Average write latency: %v\n", metrics.AvgWriteLatency)
```

### External Benchmarking

Use standard NFS benchmarking tools to measure performance:

```bash
# On Linux client
cd /mnt/nfs  # Your NFS mount point
bonnie++ -d . -u nobody

# Or use IOzone
iozone -a -n 512m -g 4g -i 0 -i 1 -i 2 -f /mnt/nfs/testfile
```

## Performance Tuning Process

Follow this process to optimize performance:

1. **Measure Baseline Performance**:
   - Establish current performance metrics
   - Identify bottlenecks using monitoring tools

2. **Apply Targeted Optimizations**:
   - Start with workload-specific configurations
   - Make one change at a time
   - Focus on the most significant bottleneck first

3. **Measure Impact**:
   - Repeat performance tests with the same workload
   - Compare to baseline measurements
   - Verify that the changes improved performance

4. **Iterate**:
   - If performance improved, keep the change
   - If not, revert and try a different approach
   - Move to the next bottleneck

5. **Monitor in Production**:
   - Implement ongoing monitoring
   - Adjust as workloads change
   - Periodically re-evaluate performance

## Common Bottlenecks and Solutions

### Slow Read Performance

If read performance is poor:

1. **Increase read-ahead buffer size**:
   ```go
   options.EnableReadAhead = true
   options.ReadAheadSize = 1048576 // 1MB
   ```

2. **Increase transfer size**:
   ```go
   options.TransferSize = 524288 // 512KB
   ```

3. **Verify network performance**:
   ```bash
   # On Linux
   iperf -c server_ip
   ```

### Slow Write Performance

If write performance is poor:

1. **Increase transfer size**:
   ```go
   options.TransferSize = 524288 // 512KB
   ```

2. **Optimize underlying filesystem**:
   ```go
   // For OS filesystem, consider mounting with optimized options
   // e.g., noatime, data=writeback, etc.
   ```

3. **Ensure network isn't the bottleneck**:
   ```bash
   # On Linux
   iperf -c server_ip
   ```

### High Latency for Metadata Operations

If metadata operations (stat, lookup, etc.) are slow:

1. **Increase attribute cache timeout**:
   ```go
   options.AttrCacheTimeout = 30 * time.Second
   ```

2. **Increase attribute cache size**:
   ```go
   options.AttrCacheSize = 50000
   ```

### Connection Limitations

If you're hitting connection limits:

1. **Increase maximum connections**:
   ```go
   options.MaxConnections = 1000
   ```

2. **Tune idle timeout**:
   ```go
   options.IdleTimeout = 2 * time.Minute // Shorter timeout to free connections
   ```

## Real-World Optimization Examples

### Media Streaming Server

```go
options := absnfs.ExportOptions{
    // Large files, sequential access
    EnableReadAhead: true,
    ReadAheadSize: 8388608, // 8MB
    
    // Media files rarely change, cache attributes longer
    AttrCacheTimeout: 60 * time.Second,
    
    // Large transfers for high throughput
    TransferSize: 1048576, // 1MB
    
    // Many concurrent readers
    MaxConnections: 500,
}
```

### Development Server

```go
options := absnfs.ExportOptions{
    // Small files, mixed access patterns
    EnableReadAhead: true,
    ReadAheadSize: 262144, // 256KB
    
    // Files change frequently, shorter cache
    AttrCacheTimeout: 5 * time.Second,
    
    // Moderate transfer size
    TransferSize: 131072, // 128KB
    
    // Fewer connections but longer idle timeout
    MaxConnections: 50,
    IdleTimeout: 10 * time.Minute,
}
```

### Big Data Processing

```go
options := absnfs.ExportOptions{
    // Very large files, mostly sequential
    EnableReadAhead: true,
    ReadAheadSize: 16777216, // 16MB
    
    // Moderate attribute caching
    AttrCacheTimeout: 15 * time.Second,
    
    // Very large transfers
    TransferSize: 4194304, // 4MB
    
    // Many workers, many connections
    MaxConnections: 1000,
    MaxWorkers: runtime.NumCPU() * 8,
}
```

## Conclusion

Performance tuning is an iterative process that requires measurement, adjustment, and verification. By understanding your workload characteristics and systematically addressing bottlenecks, you can significantly improve ABSNFS performance.

Remember these key principles:
1. Measure before and after changes
2. Make one change at a time
3. Target the most significant bottleneck first
4. Consider the entire system, not just the NFS server
5. Different workloads require different optimizations

With proper tuning, ABSNFS can deliver excellent performance across a wide range of workloads and use cases.