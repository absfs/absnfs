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

The handle value is a key of `FileHandleMap.handles`, a `map[uint64]*handleEntry`
that maps handle IDs to `NFSNode` references (plus the entry's eviction state).

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

2. **ID assignment**: the new handle is the next value of `lastHandle`, one
   counter shared by every `FileHandleMap` in the process. IDs of released,
   evicted or retired handles are not reused: reusing one would let a client
   still holding the old handle reach whatever object the ID was given to
   next.

3. **Eviction**: If the handle count exceeds `maxHandles` (default 100,000),
   10% of `maxHandles` handles are evicted, chosen by the clock
   (second-chance) algorithm. A lookup (`Get`, `GetOrError`, or an
   `Allocate` of a path that already has a handle) marks a handle used. The
   eviction hand walks the handles from the oldest; a used one has its mark
   cleared and goes to the back, an unused one is evicted. So a handle a
   client keeps using, such as the directory it is listing, stays. Eviction
   cleans up path mappings and closes the associated files. Each step is
   amortized O(1): IDs released or retired before the hand reaches them are
   skipped, and the queue is compacted once they make up most of it. A new
   handle joins the queue only after the eviction its allocation caused, so
   that eviction never drops the handle about to be returned. Requests with an evicted handle get `NFSERR_STALE`; the client recovers
   by looking the name up again from a handle it still holds, ultimately the
   mount root.

4. **Pinning**: the handles MNT gives out (`allocateMount`) are pinned and
   never evicted, so a client can always look names up again from its
   mount's root. At most 10% of `maxHandles` (at least one) is pinned; MNT
   handles past that are ordinary. The export root's handle (path `/`) is
   the exception: it is pinned however it was allocated (`Allocate` or
   `allocateMount`) and even when subdirectory mounts have used up the cap,
   so clients that mounted the export never lose it to eviction. That adds
   at most one pin, so pins stay bounded at 10% of `maxHandles` plus one.
   Pins also never fill the last place in the table, so eviction always has
   an unpinned handle to drop; this only matters for a `maxHandles` below 3
   (at 1, nothing is pinned). A pinned handle still goes stale when its object is removed or replaced,
   or on `Release`.

## Handle IDs Across Restarts

Handles live only in memory, and every handle from before a restart reports
`NFSERR_STALE`, the mount root included: clients must remount.

- **Within a process** (an embedding program closes a server and creates a
  new one), the new server draws from the same counter, so it never repeats
  an ID, however quickly it starts and whatever the clock's resolution.
- **Across processes**, the counter starts at the process's start time in
  nanoseconds. A later process starts above every ID an earlier one gave out
  unless the clock was set back between them, which includes a machine with
  no real-time clock that boots at 1970 until NTP syncs, or unless the
  earlier process gave out more handles than nanoseconds passed between the
  two starts, which a real restart cannot do.

The 64-bit counter cannot wrap in practice: it grows by one per allocation,
and wrapping 2^64 from a start in nanoseconds would take more allocations than
there are nanoseconds until about the year 2554. The wire format is
unchanged: 8 bytes, well inside NFS3's 64-byte limit.

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
handle map, the path deduplication map and the pins. The counter is shared
and only grows, so handles given out afterwards still get new IDs. This is called by `Unexport` and
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
