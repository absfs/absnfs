# Security Architecture

absnfs implements defense in depth with multiple security layers that a request
must pass through before reaching the filesystem.

## IP Filtering

IP filtering happens at two levels:

### Connection Level (server.go)

When a TCP connection is accepted, `isIPAllowed` checks the client IP against
`PolicyOptions.AllowedIPs` before any data is read. If the list is empty, all
IPs are allowed. Supports both individual IPs and CIDR notation.

IPv4-mapped IPv6 addresses (e.g., `::ffff:192.168.1.1`) are normalized to their
IPv4 form via `normalizeIP` to prevent bypass.

### RPC Level (auth.go)

`ValidateAuthentication` re-checks the IP against the current policy snapshot.
This catches cases where the AllowedIPs list changed (via `UpdatePolicyOptions`)
after the connection was established but before the request was processed.

## Authentication

### AUTH_NONE (flavor 0)

Accepted per standard NFS server behavior. The client is mapped to nobody/nobody
(UID/GID 65534), restricting access to unprivileged operations.

### AUTH_SYS (flavor 1)

The credential body is parsed into `AuthSysCredential`:
- Stamp (arbitrary client ID)
- Machine name
- UID and GID
- Up to 16 auxiliary GIDs (enforced limit prevents DoS)

The parsed UID/GID are used as the effective identity, subject to squashing.
They are the client's claim: any process that can connect can send any uid,
so on a loopback bind every local user can act as any uid (see the security
guide).

### AllowedUIDs

When `PolicyOptions.AllowedUIDs` is not empty, `ValidateAuthentication`
refuses AUTH_SYS credentials whose uid (before squashing) is not listed, and
every AUTH_NONE credential. `HandleCall` applies it to NFS procedures other
than NULL: the MOUNT protocol and NULL reach no file, and macOS `mount_nfs`
sends MNT as uid 0. It limits accidents and casual misuse, not a client that
forges its credential.

### Unsupported Flavors

Any credential flavor other than AUTH_NONE or AUTH_SYS is rejected with a reason
string in the `AuthResult`.

## Secure Port Enforcement

When `PolicyOptions.Secure` is true, the client's source port must be less than
1024 (a privileged port). This is a traditional NFS security measure that requires
root privileges on the client to initiate connections, providing a weak form of
host authentication.

## UID/GID Squashing

Squashing maps client-provided credentials to restricted identities. Three modes
are available via `PolicyOptions.Squash`:

### `"none"` (default)

No squashing. Client credentials pass through as-is.

### `"root"`

Root squashing maps UID 0 to nobody (65534). Both UID and GID are squashed when
the UID is root. Non-root users with GID 0 have only their GID squashed.
Auxiliary GIDs of 0 are also squashed. This is the standard NFS export default.

### `"all"`

All-squash maps every UID and GID to nobody (65534), regardless of the client's
actual identity. All auxiliary GIDs are also squashed.

### Unknown Mode

Unknown squash modes fail closed by squashing all users to nobody.

### Implementation Note

Squashing copies the auxiliary GID slice before modifying it to avoid mutating
shared credential data across concurrent requests.

## Access Control (ACCESS Procedure)

The ACCESS handler (`nfs_proc_attr.go`) implements UNIX permission checking:

1. Determines which permission bits apply based on the effective UID/GID:
   - Owner bits if UID matches file UID.
   - Group bits if GID matches file GID, or any auxiliary GID matches.
   - Other bits otherwise.
2. Root (UID 0) gets all permissions.
3. Read-only policy suppresses MODIFY, EXTEND, and DELETE access bits regardless
   of file permissions.

## Path Traversal Prevention

Two functions prevent clients from escaping the export root:

### validateFilename (nfs_operations.go)

Applied to all filenames in CREATE, MKDIR, SYMLINK, REMOVE, RMDIR, RENAME, and
LOOKUP operations:

- Rejects empty names.
- Rejects names longer than 255 bytes.
- Rejects names containing NUL bytes.
- Rejects names containing `/` or `\`.
- Rejects `.` and `..`.
- On Windows, rejects reserved device names (CON, PRN, NUL, COM1-9, LPT1-9).

### sanitizePath (operations.go)

Applied when constructing full paths from directory + name:

- Rejects empty names.
- Rejects names containing path separators.
- Rejects `.` and `..`.
- Joins the directory path with the name using `filepath.Join`.
- Cleans the result with `filepath.Clean`.
- Verifies the cleaned path still starts with the base directory path.
- Rejects any path that still contains `..` components.

### Symlink Target Validation

Unless `AllowAnySymlinkTarget` is set, the SYMLINK handler rejects:
- Absolute symlink targets (paths starting with `/`).
- Targets containing `..` components.

READLINK returns targets as stored: the client resolves them itself.

### Type Decisions From the Object as It Is Now

A handle's node carries the attributes it was issued with, and the attribute
cache keeps attributes for `AttrCacheTimeout`; neither sees an object
replaced behind the server's back. A directory replaced by a symbolic link
would then be used as a directory, and a host-backed filesystem would follow
the link out of the export. So every type decision that gates an operation
on a path is made from a fresh `Lstat` (`freshAttrs`, `requireDir`,
`requireRegular` in operations.go):

| Procedure                                                       | Requires          | Otherwise                                |
| --------------------------------------------------------------- | ----------------- | ---------------------------------------- |
| LOOKUP, READDIR, READDIRPLUS, CREATE, MKDIR, SYMLINK, REMOVE, RMDIR, RENAME (both) | a directory | `NFS3ERR_NOTDIR`                         |
| READ, WRITE                                                     | a regular file    | `NFS3ERR_ISDIR` for a directory, else `NFS3ERR_INVAL` |
| SETATTR size                                                    | a regular file    | `NFS3ERR_ISDIR` for a directory, else `NFS3ERR_INVAL` |
| READLINK                                                        | a symbolic link   | `NFS3ERR_INVAL`                          |

LINK and MKNOD are not supported (`NFS3ERR_NOTSUPP`) and touch nothing. A
handle that has left the cache is resolved by path and is stale if the
object there is of another type.

SETATTR on a symbolic link applies only its owner, with `Lchown`. Its mode,
size and times are refused with `NFS3ERR_INVAL` before anything changes:
absfs's `Chmod`, `Truncate` and `Chtimes` follow the link on a host. (Linux
knfsd ignores a mode on a link and sets times on the link itself; absnfs4
refuses all three with `NFS4ERR_INVAL`.)

Residual gaps, with other local processes changing the exported directory:
the check and the operation are separate calls, so a swap in between is not
caught; and only the final component is checked, so a handle for an object
below a directory later replaced by a link is resolved through the link.
Closing either needs a backend that refuses to follow links itself (openat
with `O_NOFOLLOW` per component).

## XDR Input Bounds

All XDR decoding enforces maximum sizes to prevent memory exhaustion attacks:

| Bound | Value | Purpose |
|-------|-------|---------|
| `MAX_XDR_STRING_LENGTH` | 8,192 bytes | Limits string allocations (filenames, paths) |
| `MAX_RPC_AUTH_LENGTH` | 400 bytes | Per RFC 1831, limits credential and verifier sizes |
| File handle max length | 64 bytes | NFS3 maximum, prevents large allocations from malformed handles |
| Auxiliary GID max count | 16 | Limits AUTH_SYS auxiliary group array |
| `DefaultMaxRecordSize` | 1 MB | Limits total reassembled record size across fragments |
| Write data max | `TransferSize` | Limits write payload to server's advertised maximum |

## TLS Support

When `PolicyOptions.TLS` is configured and enabled:

- The TCP listener is wrapped in a TLS listener with the configured certificate,
  key, CA, and version constraints.
- `InsecureSkipVerify` is available but logged as a warning.
- Client certificate identity can be extracted via `ExtractCertificateIdentity`,
  which reads the Common Name, DNS SANs, or email addresses from the certificate.

TLS configuration is part of `PolicyOptions` and is updated via drain-and-swap,
so TLS changes require draining in-flight requests.

## Rate Limiting

When `PolicyOptions.EnableRateLimiting` is true, rate limiting applies at two
levels:

1. **Per-connection/per-IP** in the connection loop: `rateLimiter.AllowRequest`
   checks before dispatch.
2. **Per-operation** in individual handlers: READDIR, READDIRPLUS, READ (>64KB),
   WRITE (>64KB), and MOUNT operations call `rateLimiter.AllowOperation` with
   operation-specific rate types.

Rate-limited requests receive `MSG_DENIED` (connection level) or `NFSERR_DELAY`
(operation level, equivalent to JUKEBOX -- retry later).

## Read-Only Mode

When `PolicyOptions.ReadOnly` is true, all mutating operations (WRITE, CREATE,
MKDIR, SYMLINK, MKNOD, REMOVE, RMDIR, RENAME, LINK, SETATTR, COMMIT) return
`NFSERR_ROFS` (read-only filesystem) without performing any filesystem operation.
The ACCESS handler also suppresses write-related access bits.
