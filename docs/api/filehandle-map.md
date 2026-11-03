# FileHandleMap

The file handle cache. File handles are byte strings of 10 to 64 bytes that
name their objects by path, carry a keyed tag, and are checked against the
server's change log, so the server resolves any handle it issued without the
cache (see [File Handle System](../internals/file-handles.md)).
`FileHandleMap` maps the bytes of handles used recently to the `NFSNode` they
resolved to, and each path to the bytes issued for it, so the same object
keeps getting the same bytes.

No `FileHandleMap` can be made outside the package; it is exported for its
methods' documentation and for `AbsfsNFS` internals.

## Types

### FileHandleMap

```go
type FileHandleMap struct {
    sync.RWMutex
    // handle bytes -> {node, path, epoch}, path -> handle bytes,
    // the clock eviction queue, the capacity, the codec and the change log
}
```

### DefaultMaxHandles

```go
const DefaultMaxHandles = 100000
```

The cache's capacity when `ExportOptions.MaxHandles` is not set (<= 0).

## Behaviour

- **Issue.** LOOKUP, READDIRPLUS, CREATE, MKDIR, SYMLINK and MNT return the
  bytes cached for the object's path while they still name it, otherwise new
  bytes for the path and its change-log epoch.
- **Resolve.** A cached handle costs a map lookup (and, once the server has
  removed or renamed anything, a change-log check of its path). A handle not
  cached is decoded, its tag checked, the change log consulted, and the
  object stat'ed; then it is cached.
- **Eviction.** Above the capacity, a tenth of it is evicted by the clock
  (second-chance) algorithm: entries used since the hand last passed get
  another round, others are dropped. A new entry is never evicted by its own
  insertion. Each step is amortized O(1). Eviction costs the next request
  with the handle a miss, never `NFS3ERR_STALE`.
- **RENAME** moves the source's cached entry to the destination, so a LOOKUP
  of the new name returns the bytes the client holds. **REMOVE** and
  **RMDIR** drop the removed object's entry.

## Functions

### Resize

```go
func (fm *FileHandleMap) Resize(maxHandles int)
```

Sets the capacity (<= 0 means `DefaultMaxHandles`) and evicts down to it at
once. `UpdateExportOptions` and `UpdateTuningOptions` call it when
`MaxHandles` changes.

### ReleaseAll

```go
func (fm *FileHandleMap) ReleaseAll()
```

Empties the cache. Every handle still resolves. Used by `Unexport` and
`Close`.

### Count

```go
func (fm *FileHandleMap) Count() int
```

Returns the number of cached handles.

### Allocate, Get, GetOrError, Release (deprecated)

```go
func (fm *FileHandleMap) Allocate(f absfs.File) uint64
func (fm *FileHandleMap) Get(handle uint64) (absfs.File, bool)
func (fm *FileHandleMap) GetOrError(handle uint64) (absfs.File, error)
func (fm *FileHandleMap) Release(handle uint64)
```

Kept from the 8-byte handle format. `Allocate` issues the handle of an
`*NFSNode` and returns its first 8 bytes as an identifier (0 for anything
else); `Get`, `GetOrError` and `Release` find a cached handle by that
identifier, scanning the cache. `Release` only drops the cache entry: the
handle still resolves.

## Concurrency

`FileHandleMap` embeds `sync.RWMutex`. Lookups take the read lock; issue,
insertion, eviction and the RENAME/REMOVE updates take the write lock. The
change log has its own lock, and filesystem I/O happens outside both.

## Wire Format

`xdrEncodeFileHandle` writes a handle as XDR `opaque<64>`.
`xdrDecodeFileHandle` accepts any length up to 64 bytes; a longer one is a
decode error (`GARBAGE_ARGS`) and is not read. Whether the bytes are a handle
this server issued is decided when they are resolved: any that is not
(including empty and legacy 8-byte handles) is `NFS3ERR_STALE`.
