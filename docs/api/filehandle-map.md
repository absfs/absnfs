# FileHandleMap

Maps NFS file handles (uint64) to `absfs.File` objects with path deduplication and eviction of the handles least recently used. Handle numbers are never reused, so a stale handle reports `NFS3ERR_STALE` and never reaches another object.

## Types

### FileHandleMap

```go
type FileHandleMap struct {
    sync.RWMutex
    handles     map[uint64]*handleEntry // file, used mark, pinned flag
    pathHandles map[string]uint64       // path -> handle deduplication
    clock       []uint64                // eviction candidates in clock order
    hand        int                     // index in clock of the next candidate
    pinned      int                     // number of pinned handles
    maxHandles  int                     // 0 means DefaultMaxHandles (100,000)
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

**Handle ID allocation**: IDs come from one process-wide counter that starts at the process's start time in nanoseconds and only goes up (O(1)). Released, evicted and retired IDs are never given out again, by this map or by any other map in the process, so a server closed and created again in the same process never repeats an ID either.

**Eviction**: When the handle count exceeds `maxHandles` (or `DefaultMaxHandles` if unset), 10% of `maxHandles` handles are evicted, chosen by the clock (second-chance) algorithm: `Get`, `GetOrError` and a deduplicated `Allocate` mark a handle used; the eviction hand walks the handles from the oldest, clears the mark of a used one and moves it to the back, and evicts one not used. Handles given out by MNT are pinned (at most 10% of the limit) and never evicted; the export root's handle (path `/`) is pinned however it was allocated, even past that cap, so pins never exceed 10% of the limit plus one. Evicted files are closed and their path mappings removed. Each eviction step is amortized O(1).

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

Closes the file associated with the handle, removes the handle from the map, and cleans up the path deduplication mapping. The handle ID is not reused.

### ReleaseAll

```go
func (fm *FileHandleMap) ReleaseAll()
```

Closes all files, clears all handle mappings, resets the path deduplication map, and drops all pins. IDs given out afterwards are still new. Used during server shutdown.

### Count

```go
func (fm *FileHandleMap) Count() int
```

Returns the number of active file handles.
