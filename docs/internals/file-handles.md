# File Handle System

NFS file handles are opaque identifiers that clients use to reference files and
directories across requests. absnfs implements handles as sequential `uint64`
values managed by `FileHandleMap`. A handle number is never given out twice, so
a handle a client keeps after the server dropped it reports `NFSERR_STALE` and
never reaches another object.

## Handle Representation

Each handle is an 8-byte unsigned integer. On the wire, handles are encoded as
NFS3 opaque data: a 4-byte length (always 8) followed by the 8-byte big-endian
handle value.

The handle value is an index into `FileHandleMap.handles`, a `map[uint64]absfs.File`
that maps handle IDs to `NFSNode` references.

## NFSNode

An `NFSNode` is a path reference, not an open file descriptor. It holds:

- `path`: The absolute path within the virtual filesystem.
- `attrs`: Cached `NFSAttrs` (mode, size, timestamps, uid, gid) protected by
  a `sync.RWMutex`.
- `children`: Map of child nodes (populated for directories).
- An embedded `absfs.SymlinkFileSystem` for filesystem operations.

When an NFS operation needs to read or write file data, it opens the file via
`absfs.SymlinkFileSystem.OpenFile` using the node's path, performs the I/O, and
closes it. The handle does not keep a file descriptor open between requests.

## Handle Allocation

`FileHandleMap.Allocate` assigns handles to NFSNode references:

1. **Path deduplication**: If the file is an `NFSNode` with a non-empty path,
   `pathHandles` is checked for an existing handle for that path. If found, the
   existing handle is returned (with the file reference updated). This prevents
   unbounded handle growth from repeated LOOKUP or READDIRPLUS calls for the
   same path.

2. **ID assignment**: the new handle is `nextHandle`, which is then
   incremented. IDs of released, evicted or retired handles are not reused:
   reusing one would let a client still holding the old handle reach whatever
   object the ID was given to next.

3. **Eviction**: If the handle count exceeds `maxHandles` (default 100,000),
   the oldest handles (lowest IDs) are evicted. Eviction removes 10% of
   `maxHandles` entries at a time, cleans up path mappings and closes the
   associated files. Requests with an evicted handle get `NFSERR_STALE`; a
   client recovers by looking the name up again.

## Handle IDs Across Restarts

Handles live only in memory. A new `FileHandleMap` starts counting at the
current time in nanoseconds, and each allocation takes longer than a
nanosecond, so every ID one run of the server gave out is below the first ID
of a later run, unless the system clock is set back between them. A client
holding a handle from before a restart therefore gets `NFSERR_STALE` (and
remounts) instead of reaching an unrelated object. The 64-bit counter cannot
wrap in practice: it starts below 2^61 and grows by one per allocation. The
wire format is unchanged: 8 bytes, well inside NFS3's 64-byte limit.

## Handles Across RENAME and REMOVE

Handles name paths, so the server moves them when a path changes:

- After a RENAME, the handles of the old name and of everything below it are
  moved to the same names below the new one (`FileHandleMap.renamed`). A
  client's handle keeps reaching the object it named, and a name made again
  under the old path gets a new handle. Each moved handle gets a new
  `NFSNode`; the old one is left untouched for requests in flight.
- The handles of what a RENAME replaced, and of an object REMOVE or RMDIR
  deleted, are retired (`renamed`, `removed`): requests with them get
  `NFSERR_STALE`, and, like every handle ID, theirs is never handed out
  again, so it cannot come to name another object.

File ids are derived from paths, so an object reached through a moved handle
reports a new fileid; a client that compares fileids may then treat the handle
as stale and look the name up again.

## Handle Lookup

`FileHandleMap.Get` retrieves the `absfs.File` (actually `NFSNode`) for a handle
ID using a read lock. If the handle is not found, the caller returns
`NFSERR_STALE` to the client.

Most procedure handlers use `decodeAndLookupHandle`, which decodes the file
handle from the XDR body and looks up the node in one step, returning
`GARBAGE_ARGS` for decode errors and `NFSERR_STALE` for missing handles.

## Handle Release

`FileHandleMap.Release` removes a handle, cleans up the path mapping and
closes the associated file. The ID is not reused.

`FileHandleMap.ReleaseAll` closes and removes all handles, clearing both the
handle map and the path deduplication map. `nextHandle` is kept, so handles
given out afterwards still get new IDs. This is called by `Unexport` and
`Close`.

## Path Deduplication

The `pathHandles` map (`map[string]uint64`) provides a reverse lookup from
filesystem path to handle ID. This is the key mechanism preventing handle
exhaustion:

- NFS clients typically call LOOKUP for every path component when traversing
  a directory tree, and READDIRPLUS allocates handles for every entry returned.
- Without deduplication, the same path would accumulate many handles over time.
- With deduplication, `Allocate` returns the existing handle for a path and
  updates the NFSNode reference (which may have newer attributes).

The path mapping is maintained in sync with the handle map: entries are added
during Allocate, removed during Release, and removed during eviction.

## Concurrency

`FileHandleMap` embeds `sync.RWMutex`:

- `Allocate`, `Release`, and `ReleaseAll` acquire the write lock.
- `Get` and `Count` acquire the read lock.

This allows concurrent handle lookups (the common case during request processing)
while serializing allocations and releases.

## Wire Format Validation

`xdrDecodeFileHandle` in `rpc_types.go` validates handles on the wire:

- The length field must not exceed 64 bytes (NFS3 maximum).
- The length must be exactly 8 bytes for this implementation.
- Non-8-byte handles are consumed (to keep the stream in sync) and rejected.
- Invalid handles result in `GARBAGE_ARGS` or `NFSERR_STALE` depending on
  whether the error is a format error or a lookup miss.
