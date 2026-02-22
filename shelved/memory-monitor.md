# MemoryMonitor

## What it did

MemoryMonitor (246 LOC in `memory_monitor.go`, ~340 LOC in `memory_monitor_test.go`) ran a background goroutine that periodically sampled Go runtime memory stats (`runtime.ReadMemStats`). When heap usage exceeded a high watermark, it reduced cache sizes (attribute cache, directory cache, read-ahead buffer) by a calculated reduction factor until usage fell below a low watermark.

## Why it was built

The goal was to prevent the NFS server from consuming too much memory under load by automatically shrinking caches when the Go process approached memory limits.

## Current state at removal

- Disabled by default (`AdaptToMemoryPressure` flag, default `false`)
- Configured via `MemoryHighWatermark` (default 0.8), `MemoryLowWatermark` (default 0.6), and `MemoryCheckInterval` (default 30s)
- The reduction strategy was proportional: it computed a reduction factor from current vs target usage and shrank caches by that factor
- With the read-ahead buffer also removed, the primary cache to manage is the attribute cache, which has its own bounded LRU eviction

## What would need to be true to reconsider

- Real-world evidence of memory pressure causing issues in deployments (OOM kills, excessive GC pause times)
- Benchmarks showing the monitor's eviction strategy helps rather than hurts cache hit rates (aggressive eviction can cause thrashing)
- Consideration of whether Go's built-in GC and the bounded LRU caches are sufficient without external monitoring
