# DirCache

LRU cache for directory listings with TTL expiration.

## Types

### DirCache

```go
type DirCache struct {
    // (unexported fields)
}
```

Thread-safe directory cache backed by a hash map and doubly-linked list for O(1) LRU operations. Tracks hits and misses via atomic counters.

### CachedDirEntry

```go
type CachedDirEntry struct {
    // (unexported fields)
}
```

Internal entry holding a copy of the `[]os.FileInfo` slice and its expiration time.

## Functions

### NewDirCache

```go
func NewDirCache(timeout time.Duration, maxEntries int, maxDirSize int) *DirCache
```

Creates a new directory cache.

| Parameter | Default | Description |
|-----------|---------|-------------|
| `timeout` | 10s | TTL for cached directory listings. |
| `maxEntries` | 1,000 | Maximum number of directories cached. |
| `maxDirSize` | 10,000 | Maximum entries per directory; directories larger than this are not cached. |

All three parameters fall back to their defaults if zero or negative.

```go
dirCache := absnfs.NewDirCache(10*time.Second, 1000, 10000)
```

### Get

```go
func (c *DirCache) Get(path string) ([]os.FileInfo, bool)
```

Retrieves cached directory entries for a path. Returns a copy of the entry slice and `true` on hit, or `nil, false` on miss. Expired entries are lazily removed. Each hit updates the LRU position and increments the atomic hit counter; misses increment the miss counter.

### Put

```go
func (c *DirCache) Put(path string, entries []os.FileInfo)
```

Stores a copy of `entries` for the given path. If `len(entries) > maxDirSize`, the directory is not cached. If the cache is at capacity and the path is new, the LRU entry is evicted first.

### Invalidate

```go
func (c *DirCache) Invalidate(path string)
```

Removes a single directory from the cache. O(1) operation.

### Clear

```go
func (c *DirCache) Clear()
```

Removes all entries and resets the LRU list.

### Resize

```go
func (c *DirCache) Resize(newMaxEntries int)
```

Changes the maximum number of cached directories. Evicts LRU entries if the new limit is smaller than the current count. Defaults to 1,000 if `newMaxEntries <= 0`.

### UpdateTTL

```go
func (c *DirCache) UpdateTTL(newTimeout time.Duration)
```

Changes the TTL for new entries. Does not retroactively update existing entries. Defaults to 10 seconds if `newTimeout <= 0`.

### Size

```go
func (c *DirCache) Size() int
```

Returns the current number of cached directories.

### Stats

```go
func (c *DirCache) Stats() (int, int64, int64)
```

Returns `(entryCount, hits, misses)`. Hit and miss counters are read atomically.
