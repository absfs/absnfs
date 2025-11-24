# Negative Lookup Caching Implementation

This document summarizes the implementation of negative lookup caching as described in issue #62.

## Overview

Negative lookup caching stores failed file lookups (NFSERR_NOENT) to reduce filesystem load when clients repeatedly query for non-existent files. This is particularly beneficial for build systems, package managers, and development environments that frequently probe for files.

## Implementation Details

### 1. AttrCache Extensions (cache.go)

**New Fields:**
- `enableNegative bool` - Toggle for negative caching
- `negativeTTL time.Duration` - TTL for negative entries (default: 5s)

**Modified Types:**
- `CachedAttrs.isNegative bool` - Flag to identify negative entries

**New Methods:**
- `ConfigureNegativeCaching(enable bool, ttl time.Duration)` - Configure negative caching
- `PutNegative(path string)` - Store a negative cache entry
- `NegativeStats() int` - Count of negative entries
- `InvalidateNegativeInDir(dirPath string)` - Clear negative entries in a directory

**Modified Behavior:**
- `Get()` now handles negative cache entries and records negative cache hits
- `Put()` explicitly marks entries as non-negative

### 2. Configuration Options (types.go)

**New ExportOptions Fields:**
```go
CacheNegativeLookups bool          // Enable negative caching (default: false)
NegativeCacheTimeout time.Duration // TTL for negative entries (default: 5s)
```

**Default Values:**
- Negative caching is disabled by default
- Default timeout is 5 seconds (shorter than positive cache TTL)

### 3. Metrics (metrics.go, metrics_api.go)

**New NFSMetrics Fields:**
```go
NegativeCacheSize    int     // Number of negative cache entries
NegativeCacheHitRate float64 // Hit rate for negative cache lookups
```

**New MetricsCollector Fields:**
```go
negativeCacheHits   uint64
negativeCacheMisses uint64
```

**New Methods:**
- `RecordNegativeCacheHit()`
- `RecordNegativeCacheMiss()`
- `updateNegativeCacheHitRate()`

### 4. Operations Instrumentation (operations.go)

**Modified Operations:**

1. **Lookup**: Stores negative cache entries on NFSERR_NOENT
2. **Create**: Invalidates negative cache entries in parent directory
3. **Rename**: Invalidates negative cache entries in both source and destination directories
4. **Symlink**: Invalidates negative cache entries in parent directory

**Cache Invalidation Strategy:**
- Invalidate specific path when creating a file
- Invalidate all negative entries in a directory on CREATE/MKDIR/RENAME
- Preserve positive cache entries to maintain performance

### 5. Testing (negative_cache_test.go)

**Test Coverage:**
- Basic negative caching functionality
- Disabled state verification
- Entry expiration
- Invalidation on CREATE
- Invalidation on RENAME
- LRU eviction behavior
- Metrics tracking
- Configuration options
- Helper function testing (isChildOf)

**Test Results:**
All tests pass successfully, including integration with existing test suite.

## Configuration Examples

### Enable for Development Environment
```go
server, _ := absnfs.New(fs, absnfs.ExportOptions{
    CacheNegativeLookups: true,
    NegativeCacheTimeout: 5 * time.Second,  // Short timeout for quick updates
    AttrCacheTimeout:     10 * time.Second,
    AttrCacheSize:        20000,
})
```

### Enable for Build Systems
```go
server, _ := absnfs.New(fs, absnfs.ExportOptions{
    CacheNegativeLookups: true,
    NegativeCacheTimeout: 10 * time.Second, // Longer timeout for build stability
    AttrCacheTimeout:     30 * time.Second,
    AttrCacheSize:        50000,
})
```

### Disabled (Default Behavior)
```go
server, _ := absnfs.New(fs, absnfs.ExportOptions{
    CacheNegativeLookups: false,  // Explicit disable (default)
    AttrCacheTimeout:     5 * time.Second,
    AttrCacheSize:        10000,
})
```

## Performance Characteristics

### Benefits
- Reduces filesystem load for repeated lookups of non-existent files
- Improves response time for negative lookups (cache hit vs filesystem query)
- Particularly effective for workloads with many failed lookups

### Memory Usage
- Negative entries share the AttrCacheSize limit with positive entries
- Each negative entry is smaller than a positive entry (no attributes stored)
- LRU eviction ensures bounded memory usage

### Cache Coherency
- Short TTL (5s default) reduces staleness window
- Automatic invalidation on file creation ensures correctness
- Directory-wide invalidation on RENAME/MKDIR ensures consistency

## Monitoring

Access negative cache statistics via metrics:
```go
metrics := server.GetMetrics()
fmt.Printf("Negative cache size: %d\n", metrics.NegativeCacheSize)
fmt.Printf("Negative cache hit rate: %.2f%%\n", metrics.NegativeCacheHitRate*100)
```

## Documentation

Updated documentation:
- `/docs/guides/caching.md` - Added negative caching section with examples
- `/docs/api/attr-cache.md` - Updated API documentation with new methods and configuration

## Design Decisions

1. **Disabled by Default**: Negative caching is opt-in to maintain backward compatibility and avoid surprising behavior
2. **Shorter TTL**: Default 5s timeout vs 30s for positive entries reduces staleness risk
3. **Shared Size Limit**: Uses AttrCacheSize for both positive and negative entries to simplify configuration
4. **Directory-Level Invalidation**: Invalidates all negative entries in a directory on file creation to ensure correctness
5. **No Special Handling for Symlinks**: Treats symlink creation like regular file creation

## Backward Compatibility

- Feature is disabled by default
- No breaking changes to existing APIs
- All existing tests continue to pass
- Configuration is additive (new optional fields)

## Future Enhancements

Potential future improvements:
1. Separate size limit for negative entries
2. Per-directory negative cache statistics
3. Adaptive TTL based on access patterns
4. Negative cache for directory lookups (READDIR)

## Testing

Run negative cache tests:
```bash
go test -v -run TestNegativeCache
```

Run full test suite:
```bash
go test ./...
```

All tests pass successfully with negative caching implementation.
