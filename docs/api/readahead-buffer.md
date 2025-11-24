---
layout: default
title: ReadAheadBuffer
---

# ReadAheadBuffer

The `ReadAheadBuffer` is a performance optimization component that improves sequential read performance by prefetching data before it is requested by clients.

## Purpose

Sequential read performance is critical for many NFS workloads. The `ReadAheadBuffer` serves several important purposes:

1. **Latency Reduction**: Reduces the perceived latency for sequential reads
2. **Throughput Improvement**: Increases overall read throughput
3. **I/O Optimization**: Allows for larger, more efficient I/O operations
4. **Network Utilization**: Better utilizes network bandwidth by having data ready when requested

## Type Definition

```go
type ReadAheadBuffer struct {
    // contains filtered or unexported fields
}
```

The `ReadAheadBuffer` type is used internally by the `AbsfsNFS` type and is not typically created or manipulated directly by users.

## How Read-Ahead Works

The read-ahead mechanism works through the following process:

1. **Detection**: Identifies sequential read patterns
2. **Prefetching**: Reads ahead of the client's current position
3. **Buffering**: Stores prefetched data in memory
4. **Serving**: Fulfills client requests from the buffer when possible
5. **Adaptive Behavior**: Adjusts prefetching based on observed access patterns

## Key Operations

The `ReadAheadBuffer` provides several key operations:

### NewReadAheadBuffer

```go
func NewReadAheadBuffer(size int) *ReadAheadBuffer
```

Creates a new read-ahead buffer with the specified buffer size per file. Default limits are 100 files and 100MB total memory.

### Configure

```go
func (b *ReadAheadBuffer) Configure(maxFiles int, maxMemory int64)
```

Sets the maximum number of files that can have buffers and the maximum total memory usage. Enforces limits immediately by evicting buffers if necessary.

### Read

```go
func (b *ReadAheadBuffer) Read(path string, offset int64, count int, server ...*AbsfsNFS) ([]byte, bool)
```

Attempts to fulfill a read request from the buffer. Returns the data and a boolean indicating whether the data was found in the buffer. Optionally accepts an `AbsfsNFS` server instance for recording cache hit/miss metrics.

### Fill

```go
func (b *ReadAheadBuffer) Fill(path string, data []byte, offset int64)
```

Fills the buffer for a file with data from the given offset. Creates a new buffer if one doesn't exist, enforcing memory limits with LRU eviction.

### ClearPath

```go
func (b *ReadAheadBuffer) ClearPath(path string)
```

Clears the buffer for a specific file, typically called when the file is modified.

### Clear

```go
func (b *ReadAheadBuffer) Clear()
```

Clears all read-ahead buffers, resetting memory usage to zero.

### Size

```go
func (b *ReadAheadBuffer) Size() int64
```

Returns the current memory usage of all read-ahead buffers.

### Stats

```go
func (b *ReadAheadBuffer) Stats() (int, int64)
```

Returns the number of files with buffers and the total memory usage as a tuple.

## Buffer Lifecycle

Read-ahead buffers follow a lifecycle:

1. **Creation**: When sequential access to a file is detected
2. **Population**: When data is prefetched into the buffer
3. **Usage**: When data is served from the buffer
4. **Invalidation**: When a file is modified or access becomes non-sequential
5. **Eviction**: When buffer resources need to be reclaimed

## Implementation Details

The `ReadAheadBuffer` implementation includes several important details:

### Sequential Access Detection

The buffer detects sequential access patterns by:
- Tracking recent read offsets and sizes
- Analyzing the pattern of reads
- Identifying sequential forward progress
- Adapting to client read sizes

### Memory Management

The buffer implements memory management strategies:
- Per-file buffer limits
- Global buffer pool with size limits
- LRU (Least Recently Used) eviction
- Explicit invalidation for modified files

### Thread Safety

The `ReadAheadBuffer` is thread-safe, allowing concurrent access from multiple clients.

### Adaptive Behavior

The buffer adapts its behavior based on observed access patterns:
- Increases read-ahead size for consistently sequential access
- Decreases or disables read-ahead for random access
- Adjusts to client read sizes and frequencies

## Performance Considerations

The `ReadAheadBuffer` is optimized for performance in several ways:

1. **Zero-Copy Design**: Minimizes data copying when possible
2. **Buffer Reuse**: Reuses buffer memory to reduce allocations
3. **Asynchronous Prefetching**: Performs prefetching in background goroutines
4. **Intelligent Prefetching**: Only prefetches when beneficial

## Configuration Options

The `ReadAheadBuffer` can be configured through the `ExportOptions`:

```go
options := absnfs.ExportOptions{
    // Whether to enable read-ahead buffering
    EnableReadAhead: true,
    
    // Size of read-ahead buffer (per file)
    ReadAheadSize: 262144, // 256KB
    
    // Maximum number of files to buffer simultaneously
    ReadAheadMaxFiles: 100,
    
    // Maximum total memory usage for read-ahead buffers
    ReadAheadMaxMemory: 104857600, // 100MB
}
```

## Example Usage

While users don't typically interact with `ReadAheadBuffer` directly, here's an example of how it's used internally:

```go
// When a client requests file data
func (nfs *AbsfsNFS) handleRead(handle uint64, offset int64, count int) ([]byte, error) {
    // Get the file for the handle
    file, exists := nfs.fileHandleMap.Get(handle)
    if !exists {
        return nil, errors.New("invalid file handle")
    }

    path := file.Name()

    // Try to read from the read-ahead buffer first
    data, found := nfs.readAheadBuffer.Read(path, offset, count, nfs)
    if found {
        // Data was successfully read from the buffer
        return data, nil
    }

    // If not in buffer, fall back to direct read
    data = make([]byte, count)
    n, err := file.ReadAt(data, offset)
    if err != nil && err != io.EOF {
        return nil, err
    }

    // Fill buffer with read data for future reads
    nfs.readAheadBuffer.Fill(path, data[:n], offset)

    return data[:n], nil
}

// When a client writes to a file
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

    // Invalidate read-ahead buffer since file changed
    nfs.readAheadBuffer.ClearPath(file.Name())

    return n, nil
}
```

## Buffer Statistics

The `ReadAheadBuffer` maintains statistics to help with monitoring and tuning:

1. **Hit Rate**: Percentage of reads served from the buffer
2. **Prefetch Count**: Number of prefetch operations performed
3. **Buffer Size**: Current memory usage of the buffer
4. **File Count**: Number of files currently being buffered

## When to Use Read-Ahead

Read-ahead buffering is most beneficial for:

1. **Sequential Access**: Files that are read sequentially from beginning to end
2. **Large Files**: Files that are too large to cache entirely in memory
3. **Network-Bound Workloads**: Where network latency is a significant factor
4. **Multiple Clients**: Where multiple clients access the same files

It may be less beneficial or even counterproductive for:

1. **Random Access**: Files accessed in a non-sequential pattern
2. **Small Files**: Files small enough to cache entirely
3. **Low-Memory Environments**: Where memory is at a premium
4. **Write-Heavy Workloads**: Where files are frequently modified

## Relation to Other Components

The `ReadAheadBuffer` interacts closely with several other components in ABSNFS:

- **AbsfsNFS**: Coordinates overall NFS operations and provides metrics recording
- **FileHandleMap**: Maps file handles to files whose paths are used for buffer lookups
- **FileBuffer**: Internal structure that holds the buffered data for each file
- **Uint64MinHeap**: Similar LRU eviction pattern as used in other caching components