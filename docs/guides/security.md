# Security

absnfs provides several layers of access control: IP filtering, privileged port
enforcement, read-only mode, user ID squashing, a uid allow list, rate limiting,
and TLS encryption.

## Who Can Use an Export

Read this first. `Export` always binds `127.0.0.1`; it takes no bind
address. `NewServer` binds `ServerOptions.Hostname`, which defaults to
`localhost`. Either keeps other machines out, but every account on the
machine can connect to the port and speak NFS.

NFSv3 identifies callers with AUTH_SYS: the client puts a uid and gids in
each request and the server believes them. absnfs does too, like every
NFSv3 server without Kerberos. A local user who writes their own RPC
requests (or runs any NFS client library) can therefore claim to be the user
running the server, or root, and read and write whatever the server process
can reach through the export. On a machine shared with other users, a
running export gives all of them the server's access to it.

Limit it with:

1. **Bind to `127.0.0.1`**, not a name or an external address, and list
   remote clients in `AllowedIPs`. These limit machines, not users. `Export`
   does this already; with `NewServer`, set
   `ServerOptions.Hostname: "127.0.0.1"`, which is also how to bind any
   other address (`Export` cannot).
2. **`AllowedUIDs`** (below), so that only requests carrying the expected
   uids are served. It limits accidents and casual misuse, not a hostile
   local user, because the uid is still the client's claim.
3. **TLS with client certificates**, the only option that proves who the
   client is (see the [TLS guide](tls.md)).
4. **`Squash: "all"`** or **`ReadOnly`** where clients need less.

## Limiting UIDs

`AllowedUIDs` serves only AUTH_SYS requests whose credential carries one of
the listed uids, as sent by the client (before squashing). Requests with
another uid, and every AUTH_NONE request, are refused with `AUTH_ERROR`
(`AUTH_BADCRED`) and counted in `AuthFailures`. Empty (the default) means any
uid.

```go
nfs, err := absnfs.New(fs, absnfs.ExportOptions{
	// Serve only the user running the server.
	AllowedUIDs: []uint32{uint32(os.Getuid())},
})
```

The NFS NULL procedure and the MOUNT protocol are answered for any uid,
because clients probe with NULL (some with AUTH_NONE) and macOS `mount_nfs`
sends MNT as uid 0, since it runs setuid root. A uid that is not on the
list may MNT only the export's root (`host:/`), whose handle is of no use
without NFS requests: MNT of any other path answers `MNT3ERR_ACCES` without
looking it up, so it does not reveal whether the path exists. To mount a
subdirectory with `mount_nfs` while `AllowedUIDs` is set, list uid 0 too, or
mount the root. `AllowedUIDs` is a policy option: `UpdateExportOptions` can
change it.

What it is for: another account on the machine mounting your export as
itself, a client mounted by the wrong user, a script running as root by
mistake. What it is not: AUTH_SYS credentials are not authenticated, so a
user who crafts RPC requests can put an allowed uid in them. Use TLS client
certificates where clients must be proven.

## Exports of a Host Directory

When the exported filesystem is a host directory (osfs, or basefs over
osfs), the host follows symbolic links whenever the server opens a path.
absnfs keeps clients inside the export:

- SYMLINK refuses absolute targets and targets with `..` components unless
  `AllowAnySymlinkTarget` is set.
- SETATTR on a symbolic link sets only its owner, with `Lchown`. Its mode,
  size and times are refused with `NFS3ERR_INVAL` (`chmod -h` and `touch -h`
  report "Invalid argument" on macOS): absfs can set them only through the
  link, which would change its target.
- Every decision on an object's type that gates an operation is made from
  the object as it is now (a fresh `Lstat`), never from the attributes its
  handle was issued with or the attribute cache. A directory replaced by a
  symbolic link behind the server's back answers `NFS3ERR_NOTDIR` to LOOKUP,
  READDIR, READDIRPLUS, CREATE, MKDIR, SYMLINK, REMOVE, RMDIR and RENAME; a
  file replaced by one answers `NFS3ERR_INVAL` to READ, WRITE and SETATTR of
  its mode, size or times (an owner change applies to the link itself, with
  `Lchown`, and follows nothing); READLINK answers `NFS3ERR_INVAL` for an
  object that is no longer a link.
- Every directory above a handle's object, from the export's root down, is
  checked the same way. A handle for an object below a directory that was
  replaced by a link answers `NFS3ERR_STALE` to every procedure above
  instead of reaching, for reading and writing, the subtree the link leads
  to. A handle whose object is gone is stale too.
- UNCHECKED CREATE of a name that is a symbolic link, a directory or
  anything but a regular file answers `NFS3ERR_EXIST` (as Linux knfsd does)
  without opening it, so it never truncates what a link points to.
- MNT walks no symbolic link: every component of the path must be a
  directory.

Gaps remain when other local processes can change the exported directory.
A swap in the instant between a check and the operation it gates is not
caught; only a backend that refuses to follow links on its own could close
that. GETATTR and ACCESS, which change nothing, may report the attributes
of what a swapped directory's link leads to until the handle is next used
by a procedure above. Do not let untrusted local users write to an exported
host directory.

## IP Filtering

Restrict which clients can connect using `AllowedIPs`. Supports individual
addresses and CIDR ranges. When empty, all clients are allowed.

```go
nfs, err := absnfs.New(fs, absnfs.ExportOptions{
	AllowedIPs: []string{
		"192.168.1.0/24",  // Entire subnet
		"10.0.0.5",        // Single host
	},
})
```

Connections from IPs not in the list are rejected at the TCP accept stage before
any NFS processing occurs. IPv4-mapped IPv6 addresses (e.g., `::ffff:192.168.1.1`)
are normalized to their IPv4 form so they match correctly.

## Privileged Ports

Set `Secure: true` to require clients to connect from a privileged source port
(below 1024). On Unix systems, only root can bind privileged ports, so this
provides a basic authentication check.

```go
nfs, err := absnfs.New(fs, absnfs.ExportOptions{
	Secure: true,
})
```

macOS clients then have to mount with `resvport`, which needs `sudo`. Leave
`Secure` false to let users mount as themselves with `noresvport` (see the
README section "Mounting on macOS as a normal user").

## Read-Only Export

Prevent all write operations at the NFS protocol level:

```go
nfs, err := absnfs.New(fs, absnfs.ExportOptions{
	ReadOnly: true,
})
```

This can be toggled at runtime via `UpdateExportOptions` or `UpdatePolicyOptions`.

## Squash Modes

UID/GID squashing controls how client credentials are mapped on the server.

| Mode             | Behavior                                                          |
| ---------------- | ----------------------------------------------------------------- |
| `"none"` or `""` | No mapping. Client credentials are used as-is.                    |
| `"root"`         | Maps UID 0 (root) to nobody (65534). Non-root users pass through. |
| `"all"`          | Maps all users to nobody (65534).                                 |

```go
nfs, err := absnfs.New(fs, absnfs.ExportOptions{
	Squash: "root",
})
```

The squash mode is **immutable** after creation. Attempting to change it at
runtime returns an error.

In `"root"` mode, GID 0 is also squashed (both primary and auxiliary GIDs).
`AUTH_NONE` connections always map to nobody regardless of squash mode.

## Rate Limiting

Rate limiting is disabled by default. When enabled, it limits requests per-IP,
per-connection, and per-operation type to prevent abuse.

```go
config := absnfs.DefaultRateLimiterConfig()
config.PerIPRequestsPerSecond = 500
config.PerIPBurstSize = 200

nfs, err := absnfs.New(fs, absnfs.ExportOptions{
	EnableRateLimiting: true,
	RateLimitConfig:    &config,
})
```

To disable rate limiting:

```go
nfs, err := absnfs.New(fs, absnfs.ExportOptions{
	EnableRateLimiting: false,
})
```

When a client exceeds the rate limit, the server responds with `MSG_DENIED`
and the client retries after a backoff.

## TLS Encryption

See the dedicated [TLS guide](tls.md) for setup instructions.

## Combined Example

A hardened export combining multiple security features:

```go
nfs, err := absnfs.New(fs, absnfs.ExportOptions{
	ReadOnly:   true,
	Secure:     true,
	Squash:     "root",
	AllowedIPs: []string{"192.168.1.0/24"},
	AllowedUIDs: []uint32{1000, 1001},
	TLS: &absnfs.TLSConfig{
		Enabled:    true,
		CertFile:   "/etc/nfs/server.crt",
		KeyFile:    "/etc/nfs/server.key",
		CAFile:     "/etc/nfs/ca.crt",
		ClientAuth: tls.RequireAndVerifyClientCert,
		MinVersion: tls.VersionTLS12,
		MaxVersion: tls.VersionTLS13,
	},
	EnableRateLimiting: true,
	MaxFileSize:        100 * 1024 * 1024, // 100MB
})
```
