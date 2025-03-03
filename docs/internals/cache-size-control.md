---
layout: default
title: Cache Size Control
---

# Cache Size Control

This document describes the implementation of the Cache Size Control feature for the ABSNFS project.

## Overview

The Cache Size Control feature enhances the NFS server's performance and reliability by managing memory usage through adaptive cache sizing. It enables configurable limits on attribute cache size and read-ahead buffers, while also implementing memory pressure detection that can automatically adjust cache sizes to prevent out-of-memory conditions.

## Key Components

1. **Configuration Options**:
   - `AttrCacheSize int`: Controls the maximum number of file attributes to cache
   - `ReadAheadMaxFiles int`: Maximum number of files with active read-ahead buffers
   - `ReadAheadMaxMemory int64`: Maximum memory to use for all read-ahead buffers
   - Default values: 10000, 100, 104857600 (100MB)

2. **Memory Pressure Detection**:
   - `AdaptToMemoryPressure bool`: Enables automatic cache reduction under memory pressure
   - `MemoryHighWatermark float64`: Memory usage threshold to trigger cache reduction
   - `MemoryLowWatermark float64`: Target memory usage after reduction
   - `MemoryCheckInterval time.Duration`: How often to check memory usage
   - Default values: false, 0.8, 0.6, 30s

3. **MemoryMonitor Type**:
   - Monitors system memory usage at regular intervals
   - Detects memory pressure conditions
   - Implements cache reduction strategies
   - Maintains memory usage statistics for monitoring

4. **Integration with AbsfsNFS**:
   - Added `memoryMonitor` field to AbsfsNFS struct
   - Memory monitor is initialized during server creation if enabled
   - Provides methods to start/stop monitoring and adjust cache sizes

## Implementation Details

1. **Memory Statistics Collection**:
   ```go
   // memoryStats holds the current memory usage statistics
   type memoryStats struct {
       // Total system memory (bytes)
       totalMemory uint64
       // Current system memory usage (bytes)
       usedMemory uint64
       // Memory usage as a fraction of total memory (0.0-1.0)
       usageFraction float64
       // Is the system under memory pressure?
       underPressure bool
       // Time when stats were last updated
       lastUpdated time.Time
   }
   ```

2. **Memory Pressure Detection**:
   - Memory usage is tracked as a percentage of available memory
   - When usage exceeds the high watermark, memory pressure is detected
   - When usage falls below the low watermark, pressure is considered resolved
   - A background goroutine periodically checks memory usage

3. **Cache Size Reduction**:
   - When memory pressure is detected, cache sizes are reduced
   - Reduction amount is calculated based on the difference between current usage and target
   - Both attribute cache and read-ahead buffers are adjusted
   - Minimum values ensure caches remain functional even under pressure

4. **Garbage Collection Integration**:
   - After reducing cache sizes, garbage collection is triggered
   - This ensures memory is actually reclaimed by the Go runtime
   - Cache entries are cleared and rebuilt rather than selectively removed

## Performance Benefits

1. **Resource Efficiency**: Prevents excessive memory usage by caches
2. **Stability**: Reduces risk of out-of-memory errors under load
3. **Adaptability**: Dynamically adjusts to system conditions
4. **Performance**: Maintains optimal cache sizes based on available resources
5. **Monitoring**: Provides memory usage insights through metrics

## Testing Strategy

The implementation includes comprehensive testing:

1. **Unit Tests**:
   - `TestMemoryMonitorCreation`: Tests memory monitor initialization
   - `TestMemoryMonitorDisabled`: Verifies behavior when disabled
   - `TestMemoryStatsUpdate`: Tests statistics collection

2. **Functional Tests**:
   - `TestCacheReductionCalculation`: Tests cache size reduction logic
   - `TestMemoryPressureHandling`: Tests pressure detection and response

3. **Performance Tests**:
   - `BenchmarkMemoryMonitor`: Measures overhead of memory monitoring

## Usage Example

```go
// Create an NFS server with cache size control
options := ExportOptions{
    // Attribute cache settings
    AttrCacheSize: 5000,
    
    // Read-ahead buffer settings
    ReadAheadMaxFiles: 50,
    ReadAheadMaxMemory: 50 * 1024 * 1024, // 50MB
    
    // Memory pressure adaptation
    AdaptToMemoryPressure: true,
    MemoryHighWatermark: 0.8,   // 80% memory usage triggers reduction
    MemoryLowWatermark: 0.6,    // Target 60% memory usage
    MemoryCheckInterval: 30 * time.Second,
}

// Create NFS server with cache size control
server, err := absnfs.New(fs, options)
```

## Next Steps

Future enhancements could include:

1. More sophisticated memory pressure detection algorithms
2. Per-file type or access pattern cache prioritization
3. Memory usage prediction based on request patterns
4. Integration with system memory pressure notifications where available
5. Gradual cache size adjustment to prevent thrashing