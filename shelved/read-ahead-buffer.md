# ReadAheadBuffer

## What it did

ReadAheadBuffer (~300 LOC in `cache.go`, ~420 LOC in `read_ahead_test.go`) was a per-file prefetch buffer for sequential reads. After a successful read that did not hit EOF, it would read ahead an additional chunk (`ReadAheadSize`, default 256KB) and store it in a buffer keyed by file path. Subsequent reads at the buffered offset would be served from memory.

The buffer tracked up to `ReadAheadMaxFiles` (default 100) files with a total memory cap of `ReadAheadMaxMemory` (default 100MB), using LRU eviction when limits were reached.

## Why it was built

The idea was to reduce latency for sequential file reads by prefetching the next chunk before the client requested it. This is a common optimization in file servers.

## Current state at removal

- Disabled by default (`EnableReadAhead` flag, default `false`)
- The buffer operated at the absfs layer, below the OS page cache, so it was effectively a second layer of read-ahead on top of what the OS already provides
- Each read operation checked the buffer first, adding overhead even for random-access patterns
- Write operations had to clear the buffer for the affected path, adding write-path complexity
- The `ClearPath` call was spread across `operations.go` (writes) and `nfs_proc_attr.go` (truncate)

## What would need to be true to reconsider

- Benchmarks on real workloads showing measurable latency reduction from the buffer
- Evidence that the absfs layer (not the OS page cache) is the actual bottleneck for sequential reads
- A design that avoids penalizing random-access patterns and write operations
