# AttrCache

LRU cache for NFS file attributes with TTL expiration and optional negative caching.

## Types

### AttrCache

```go
type AttrCache struct {
    // (unexported fields)
}
```

Thread-safe attribute cache backed by a hash map and doubly-linked list for O(1) LRU operations.

The server keeps it coherent with its own changes: every procedure that
changes an object invalidates what it changed (a RENAME the whole subtree
below both names), and a lookup that read the filesystem before such an
invalidation does not cache its result afterwards. Changes made to the
filesystem by anything other than the server are seen once an entry's TTL
has passed.

### CachedAttrs

```go
type CachedAttrs struct {
    // (unexported fields)
}
```

Internal entry type. When `isNegative` is true, the entry records that a path was confirmed non-existent. When false, `attrs` holds a deep copy of the file's `NFSAttrs`.

## Functions

### NewAttrCache

```go
func NewAttrCache(ttl time.Duration, maxSize int) *AttrCache
```

Creates a new attribute cache. If `maxSize <= 0`, defaults to 10,000 entries. Negative caching is disabled by default; enable it with `ConfigureNegativeCaching`.

```go
cache := absnfs.NewAttrCache(5*time.Second, 10000)
```

### Get

```go
func (c *AttrCache) Get(path string, server ...*AbsfsNFS) (*NFSAttrs, bool)
```

Retrieves cached attributes for a path. The return values distinguish three cases:

| Return | Meaning |
|--------|---------|
| `(attrs, true)` | Positive hit -- `attrs` contains the cached file attributes. |
| `(nil, true)` | Negative hit -- path is confirmed non-existent. |
| `(nil, false)` | Cache miss -- path is not in the cache. |

An entry is served only while it is younger than the TTL in force at the time of the `Get`. Positive hits are copies whose `IsValid` reports true until the entry expires. The optional `server` parameter enables metrics recording (cache hit/miss counters). Expired entries are lazily removed on access. Each hit updates the LRU position.

### Put

```go
func (c *AttrCache) Put(path string, attrs *NFSAttrs)
```

Stores a deep copy of `attrs` for the given path. If the cache is at capacity and the path is new, the least recently used entry is evicted first (O(1) via linked list back pointer). Updates the entry's TTL and LRU position.

### PutNegative

```go
func (c *AttrCache) PutNegative(path string)
```

Stores a negative cache entry indicating the path does not exist. Only takes effect if negative caching has been enabled via `ConfigureNegativeCaching`. Uses the negative TTL (default 5 seconds) which is typically shorter than the positive TTL.

### Invalidate

```go
func (c *AttrCache) Invalidate(path string)
```

Removes a single entry (positive or negative) from the cache. O(1) operation.

### InvalidateNegativeInDir

```go
func (c *AttrCache) InvalidateNegativeInDir(dirPath string)
```

Removes all negative cache entries that are direct children of `dirPath`. Only checks one level deep (not recursive). The server itself invalidates just the names a procedure changes.

### Clear

```go
func (c *AttrCache) Clear()
```

Removes all entries from the cache and resets the LRU list.

### ConfigureNegativeCaching

```go
func (c *AttrCache) ConfigureNegativeCaching(enable bool, ttl time.Duration)
```

Enables or disables negative caching and sets the negative entry TTL. If `ttl <= 0`, the existing negative TTL is preserved.

### Resize

```go
func (c *AttrCache) Resize(newSize int)
```

Changes the maximum cache capacity. If `newSize` is smaller than the current entry count, LRU entries are evicted until the cache fits. If `newSize <= 0`, defaults to 10,000.

### UpdateTTL

```go
func (c *AttrCache) UpdateTTL(newTTL time.Duration)
```

Changes the TTL. It applies to every entry, including those already cached, so lowering it also bounds entries stored under the old value. If `newTTL <= 0`, defaults to 5 seconds. The negative TTL set by `ConfigureNegativeCaching` likewise applies to existing negative entries.

### Size

```go
func (c *AttrCache) Size() int
```

Returns the current number of entries (positive and negative).

### MaxSize

```go
func (c *AttrCache) MaxSize() int
```

Returns the maximum capacity.

### Stats

```go
func (c *AttrCache) Stats() (int, int)
```

Returns `(currentSize, maxSize)`.

### NegativeStats

```go
func (c *AttrCache) NegativeStats() int
```

Returns the count of negative cache entries. Iterates all entries, so not O(1).
