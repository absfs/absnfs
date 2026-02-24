# Architecture

absnfs is an NFSv3 server adapter for the [absfs](https://github.com/absfs/absfs)
filesystem interface. Any filesystem implementing `absfs.SymlinkFileSystem` can be
exported as an NFS share over TCP.

## Request Flow

```
TCP connection -> RPC decode -> HandleCall (policy lock + auth) ->
  procedure handler (XDR decode -> filesystem op -> XDR encode) -> RPC reply
```

1. A TCP connection arrives at the accept loop (`server.go`).
2. IP filtering rejects disallowed clients immediately.
3. The connection is registered (subject to `MaxConnections`) and handed to a
   connection loop, which reads RPC messages in a loop.
4. Each RPC message is decoded into an `RPCCall` (header + credentials + verifier).
5. `HandleCall` in `nfs_handlers.go` acquires the policy read lock via `TryRLock`,
   snapshots options, validates authentication, then dispatches to the correct
   program handler (NFS or MOUNT) inside a goroutine.
6. The procedure handler decodes XDR arguments from the request body, performs
   filesystem operations via `operations.go`, encodes XDR results, and returns
   an `RPCReply`.
7. The reply is written back through the connection's `connIO` interface, which
   handles record marking framing when enabled.

## Key Subsystems

### server.go -- TCP Lifecycle

Owns the `Server` type: listener setup, accept loop, per-connection goroutines,
connection tracking (register/unregister with `sync.Once`), idle timeout cleanup,
and graceful shutdown. The `connIO` interface abstracts framing so raw TCP and
record-marking connections share the same dispatch loop.

### nfs_handlers.go -- RPC Dispatch

Contains `NFSProcedureHandler.HandleCall`, which is the central dispatch point.
Responsibilities:

- Acquire the policy read lock (`TryRLock`), returning `NFSERR_JUKEBOX` on
  failure so clients retry during policy drain.
- Snapshot tuning and policy options for consistent reads within a single request.
- Validate authentication and apply UID/GID squashing.
- Route to NFS (`handleNFSCall`) or MOUNT (`handleMountCall`) based on the RPC
  program number.
- Run the handler in a goroutine that holds the policy read lock for its full
  duration, ensuring drain-and-swap blocks until real work finishes.

### nfs_proc_*.go -- NFS3 Procedure Handlers

Split by RFC 1813 category:

| File                    | Procedures                                             |
| ----------------------- | ------------------------------------------------------ |
| `nfs_proc_handlers.go`  | NULL, FSSTAT, FSINFO, PATHCONF + shared types (sattr3) |
| `nfs_proc_attr.go`      | GETATTR, SETATTR, ACCESS                               |
| `nfs_proc_lookup.go`    | LOOKUP, READLINK                                       |
| `nfs_proc_readwrite.go` | READ, WRITE, COMMIT                                    |
| `nfs_proc_create.go`    | CREATE, MKDIR, SYMLINK, MKNOD                          |
| `nfs_proc_dir.go`       | READDIR, READDIRPLUS                                   |
| `nfs_proc_remove.go`    | REMOVE, RMDIR, RENAME, LINK                            |

Each handler follows the pattern: decode file handle, look up NFSNode, perform
filesystem operation, encode XDR response with appropriate error reply format
(post_op_attr, wcc_data, or double wcc_data depending on procedure).

### operations.go -- Filesystem Bridge

Translates NFS semantics into `absfs.SymlinkFileSystem` calls. Provides
`Lookup`, `GetAttr`, `SetAttr`, `Read`, `Write`, `Create`, `Remove`, `Rename`,
`Symlink`, `Readlink`, `ReadDir`, and `ReadDirPlus`. Each operation:

- Checks read-only policy for mutating operations.
- Creates a context with operation-specific timeouts from `TimeoutConfig`.
- Calls the underlying absfs filesystem.
- Manages attribute and directory cache invalidation.
- Maps absfs errors to NFS status codes via `mapError`.

### cache.go -- AttrCache and DirCache

Two LRU caches with TTL expiration:

- **AttrCache**: Caches `NFSAttrs` by path. O(1) LRU tracking via a doubly-linked
  list. Supports negative caching (file-not-found entries with a shorter TTL).
  Configurable size, TTL, and negative caching behavior.
- **DirCache**: Caches `[]os.FileInfo` directory listings by path. Same LRU
  structure. Skips caching directories exceeding `DirCacheMaxDirSize`.

Both caches deep-copy data on get/put to prevent shared mutation.

### filehandle.go -- Handle Allocation

`FileHandleMap` assigns sequential `uint64` handles to `NFSNode` references.
Path deduplication via `pathHandles` prevents unbounded handle growth from
repeated LOOKUP/READDIRPLUS calls on the same path. A min-heap tracks freed
handle IDs for O(log n) reuse. When `maxHandles` is exceeded, the oldest
handles (lowest IDs) are evicted.

### auth.go -- Authentication and Access Control

`ValidateAuthentication` processes RPC credentials against `PolicyOptions`:

1. IP filtering (individual IPs and CIDR subnets).
2. Secure port enforcement (port < 1024 when `Secure=true`).
3. AUTH_SYS credential parsing (UID, GID, auxiliary GIDs).
4. UID/GID squashing (none, root, all modes).

AUTH_NONE is accepted and mapped to nobody (65534/65534). TLS client certificate
identity extraction is supported via `ExtractCertificateIdentity`.

### options.go -- Configuration

Two option types with different update semantics:

- **TuningOptions**: Cache sizes, timeouts, transfer size, worker pool, TCP
  settings. Updated via `UpdateTuningOptions` with a simple atomic swap. Stale
  reads only affect performance, never security.
- **PolicyOptions**: AllowedIPs, squash mode, read-only, rate limiting, TLS.
  Updated via `UpdatePolicyOptions` with drain-and-swap: the write lock is
  acquired (blocking new requests with JUKEBOX), in-flight requests finish,
  then the new policy is stored and the lock released.

This split exists because stale policy reads could violate security invariants
(allowing a revoked IP, using old squash settings), while stale tuning reads
only affect performance characteristics.

### rpc_types.go -- XDR and RPC Messages

XDR encoding/decoding helpers (uint32, uint64, string, opaque, file handle) and
RPC message types (`RPCCall`, `RPCReply`, `RPCCredential`, `AuthSysCredential`).
Includes bounds validation on all decoded lengths to prevent DoS via memory
exhaustion.

### rpc_transport.go -- Record Marking

RFC 1831 section 10 record fragment framing. `RecordMarkingConn` wraps a TCP
connection with `ReadRecord` (reassembles multi-fragment messages with total
size limit) and `WriteRecord` (splits large messages into fragments).
