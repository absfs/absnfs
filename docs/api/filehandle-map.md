# FileHandleMap

Maps NFS file handles (uint64) to `absfs.File` objects with path deduplication and LRU eviction.

## Types

### FileHandleMap

```go
type FileHandleMap struct {
    sync.RWMutex
    handles     map[uint64]absfs.File
    pathHandles map[string]uint64  // path -> handle deduplication
    nextHandle  uint64
    freeHandles *uint64MinHeap     // min-heap for handle ID reuse
    maxHandles  int                // 0 means DefaultMaxHandles (100,000)
}
```

The struct embeds `sync.RWMutex` directly, so callers use `fm.Lock()`/`fm.RLock()` etc.

### DefaultMaxHandles

```go
const DefaultMaxHandles = 100000
```

The maximum number of file handles before eviction occurs, used when `maxHandles` is 0.

## Functions

### Allocate

```go
func (fm *FileHandleMap) Allocate(f absfs.File) uint64
```

Creates a new file handle for the given file and returns the handle ID.

**Path deduplication**: If `f` is an `*NFSNode` with a non-empty path, and a handle already exists for that path, the existing handle's file reference is updated and the existing handle ID is returned. This prevents unbounded handle growth from repeated LOOKUP and READDIRPLUS calls on the same path.

**Handle ID allocation**: Freed handle IDs are recycled via a min-heap (smallest available ID first, O(log n)). If no freed handles exist, a monotonically increasing counter provides the next ID (O(1)).

**Eviction**: When the handle count exceeds `maxHandles` (or `DefaultMaxHandles` if unset), 10% of the oldest handles (lowest IDs) are evicted. Evicted files are closed and their path mappings removed.

### Get

```go
func (fm *FileHandleMap) Get(handle uint64) (absfs.File, bool)
```

Returns the `absfs.File` for the given handle. Returns `(nil, false)` if the handle does not exist. Uses a read lock.

### GetOrError

```go
func (fm *FileHandleMap) GetOrError(handle uint64) (absfs.File, error)
```

Like `Get` but returns an `InvalidFileHandleError` instead of a boolean when the handle is not found.

### Release

```go
func (fm *FileHandleMap) Release(handle uint64)
```

Closes the file associated with the handle, removes the handle from the map, cleans up the path deduplication mapping, and returns the handle ID to the free list for reuse.

### ReleaseAll

```go
func (fm *FileHandleMap) ReleaseAll()
```

Closes all files, clears all handle mappings, resets the path deduplication map, and creates a fresh free list. Used during server shutdown.

### Count

```go
func (fm *FileHandleMap) Count() int
```

Returns the number of active file handles.
