# NFS Protocol Implementation

absnfs implements NFSv3 (RFC 1813) and the MOUNT protocol (RFC 1813 Appendix I)
over ONC RPC (RFC 1831) with XDR encoding (RFC 4506).

## NFS3 Procedures

All 22 NFSv3 procedures are implemented. Dispatch happens in `nfs_handlers.go`
via the `nfsHandlers` map, which maps procedure numbers to handler functions.

### Metadata Operations

| # | Procedure | Handler | Description |
|---|-----------|---------|-------------|
| 0 | NULL | `handleNull` | No-op, tests connectivity |
| 1 | GETATTR | `handleGetattr` | Returns `fattr3` for a file handle |
| 2 | SETATTR | `handleSetattr` | Sets mode, uid, gid, size, atime, mtime. Supports sattrguard3 (ctime check). Truncation (size=0) is applied before other attributes. |
| 4 | ACCESS | `handleAccess` | Checks read/write/execute/lookup/delete permissions using UNIX permission bits, effective UID/GID, and auxiliary groups |
| 18 | FSSTAT | `handleFsstat` | Returns filesystem space statistics (hardcoded: 10GB total, 5GB free) |
| 19 | FSINFO | `handleFsinfo` | Returns transfer sizes (rtmax/wtmax=1MB, preferred=64KB, mult=4KB), max file size (1TB), time delta (1ms), and properties (symlink + homogeneous + cansettime) |
| 20 | PATHCONF | `handlePathconf` | Returns path configuration (linkmax=1024, name_max=255, no_trunc=true, chown_restricted=true, case_preserving=true) |

### Name Resolution

| # | Procedure | Handler | Description |
|---|-----------|---------|-------------|
| 3 | LOOKUP | `handleLookup` | Resolves a filename within a directory to a file handle. Validates the filename, performs path join, calls `Lookup`, and allocates a handle via `fileMap.Allocate` (which deduplicates by path). |
| 5 | READLINK | `handleReadlink` | Reads the target of a symbolic link. Validates the node is a symlink before reading. |

### Data Transfer

| # | Procedure | Handler | Description |
|---|-----------|---------|-------------|
| 6 | READ | `handleRead` | Reads data from a file at a given offset. Validates offset+count does not overflow. Rate-limits large reads (>64KB). Returns data with EOF flag and post_op_attr. |
| 7 | WRITE | `handleWrite` | Writes data to a file. Checks read-only policy. Validates count against server's advertised write size. Always returns FILE_SYNC stable mode with the server's boot-unique write verifier. |
| 21 | COMMIT | `handleCommit` | Commits previously written data. Returns the write verifier so clients can detect server restarts (which invalidate uncommitted writes). |

### Object Creation

| # | Procedure | Handler | Description |
|---|-----------|---------|-------------|
| 8 | CREATE | `handleCreate` | Creates a regular file. Supports UNCHECKED (mode 0), GUARDED (mode 1), and EXCLUSIVE (mode 2) creation. For EXCLUSIVE, existing files return success (simplified idempotent behavior). New files inherit the caller's effective UID/GID. |
| 9 | MKDIR | `handleMkdir` | Creates a directory with the specified mode. Applies Chown with the caller's effective UID/GID. |
| 10 | SYMLINK | `handleSymlink` | Creates a symbolic link. Validates the target path: rejects absolute paths and paths containing ".." components to prevent escape from the export root. Uses Lchown to set ownership without following the link. |
| 11 | MKNOD | `handleMknod` | Stub: returns `NFSERR_NOTSUPP`. Consumes arguments to prevent stream desync. |

### Object Removal and Renaming

| # | Procedure | Handler | Description |
|---|-----------|---------|-------------|
| 12 | REMOVE | `handleRemove` | Removes a file from a directory. Validates the parent is a directory. |
| 13 | RMDIR | `handleRmdir` | Removes a directory. Verifies the target exists and is a directory. Maps "directory not empty" errors to `NFSERR_NOTEMPTY`. |
| 14 | RENAME | `handleRename` | Renames a file or directory. Validates both source and destination filenames. Returns double wcc_data (one for each parent directory). |
| 15 | LINK | `handleLink` | Stub: returns `NFSERR_NOTSUPP`. Hard links are not supported. Consumes arguments to prevent stream desync. FSINFO reports FSF3_LINK=0 to advertise this. |

### Directory Listing

| # | Procedure | Handler | Description |
|---|-----------|---------|-------------|
| 16 | READDIR | `handleReaddir` | Lists directory entries (fileId + name + cookie). Respects the client's `count` limit for reply size. Uses cookie-based pagination. |
| 17 | READDIRPLUS | `handleReaddirplus` | Like READDIR but also returns full `fattr3` and file handles for each entry, reducing follow-up LOOKUP/GETATTR round trips. Allocates handles for each entry via `fileMap.Allocate`. |

## Error Reply Formats

Different procedures require different error reply structures per RFC 1813:

| Format | Helper | Used By |
|--------|--------|---------|
| status only | `nfsErrorReply` | GETATTR |
| status + post_op_attr | `nfsErrorWithPostOp` | LOOKUP, ACCESS, READ, READLINK, READDIR, READDIRPLUS, FSSTAT, FSINFO, PATHCONF |
| status + wcc_data | `nfsErrorWithWcc` | SETATTR, WRITE, CREATE, MKDIR, SYMLINK, MKNOD, REMOVE, RMDIR, COMMIT |
| status + post_op_attr + wcc_data | `nfsErrorWithPostOpAndWcc` | LINK |
| status + double wcc_data | `nfsErrorWithDoubleWcc` | RENAME |

## MOUNT Protocol

The MOUNT protocol (`mount_handlers.go`) handles export discovery and initial
file handle acquisition. Both MOUNT v1 and v3 are accepted for client compatibility.

| # | Procedure | Description |
|---|-----------|-------------|
| 0 | NULL | No-op |
| 1 | MNT | Mount an export. Validates the mount path, performs a Lookup, allocates the root file handle, and returns it with AUTH_SYS as the supported auth flavor. |
| 2 | DUMP | Lists active mounts (returns empty list). |
| 3 | UMNT | Unmount (no-op, consumes the path argument). |
| 4 | UMNTALL | Unmount all (no-op). |
| 5 | EXPORT | Lists available exports (returns "/" with no group restrictions). |

## RPC Framing

### Wire Format (RFC 1831)

RPC calls and replies are encoded using XDR (External Data Representation):

- All integers are big-endian, 4-byte aligned.
- Strings and opaque data are length-prefixed and padded to 4-byte boundaries.
- File handles are 8-byte opaque values (length prefix + uint64 handle ID).

An RPC call contains:
```
XID (4) | msg_type=CALL (4) | RPC version (4) | program (4) |
version (4) | procedure (4) | credential (flavor + opaque body) |
verifier (flavor + opaque body) | procedure-specific arguments
```

An RPC reply contains:
```
XID (4) | msg_type=REPLY (4) | reply_stat (4) |
[if ACCEPTED: verifier + accept_stat + procedure-specific results]
[if DENIED: reject_stat + error info]
```

### Record Marking (RFC 1831 Section 10)

When `UseRecordMarking` is enabled (required for standard NFS clients), each
RPC message is wrapped in record marking framing:

- Each fragment has a 4-byte header: bit 31 = last-fragment flag, bits 0-30 = length.
- A complete record consists of one or more fragments, with the last fragment
  having the last-fragment flag set.
- `RecordMarkingReader` reassembles fragments with a configurable maximum record
  size (default 1MB) to prevent unbounded memory growth.
- `RecordMarkingWriter` splits large messages into fragments at `DefaultMaxFragmentSize`
  (1MB).

### Input Validation

All XDR decoding functions enforce bounds:

- `MAX_XDR_STRING_LENGTH` (8KB) limits string allocations.
- `MAX_RPC_AUTH_LENGTH` (400 bytes, per RFC 1831) limits credential/verifier sizes.
- File handle lengths are capped at 64 bytes (NFS3 maximum).
- XDR strings reject embedded NUL bytes.
- Write data is bounded by the server's advertised `TransferSize`.
- Record marking total size is bounded by `DefaultMaxRecordSize` (1MB).

## Portmapper

`StartWithPortmapper` starts a portmapper service (RFC 1833, port 111) that
registers the NFS program (100003) and MOUNT program (100005) for both v1 and v3.
Standard NFS clients query the portmapper to discover which port the NFS and
MOUNT services are running on.
