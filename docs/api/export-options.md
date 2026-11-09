# ExportOptions

Defined in `options.go`. The user-facing configuration struct passed to `New()`. Internally split into `TuningOptions` (performance) and `PolicyOptions` (security) for runtime updates.

```go
type ExportOptions struct {
    // Security / Policy
    ReadOnly           bool
    Secure             bool
    AllowedIPs         []string
    Squash             string
    MaxFileSize        int64
    EnableRateLimiting bool
    RateLimitConfig    *RateLimiterConfig
    TLS                *TLSConfig
    AllowedUIDs        []uint32
    AllowAnySymlinkTarget bool

    // Performance / Tuning
    Async                bool
    TransferSize         int
    AttrCacheTimeout     time.Duration
    AttrCacheSize        int
    CacheNegativeLookups bool
    NegativeCacheTimeout time.Duration
    EnableDirCache       bool
    DirCacheTimeout      time.Duration
    DirCacheMaxEntries   int
    DirCacheMaxDirSize   int
    MaxWorkers           int
    MaxConnections       int
    IdleTimeout          time.Duration
    TCPKeepAlive         bool
    TCPNoDelay           bool
    SendBufferSize       int
    ReceiveBufferSize    int
    MaxHandles           int
    WriteVerifier        [8]byte

    // Logging and Timeouts
    Log      *LogConfig
    Timeouts *TimeoutConfig

    // Ownership (fixed at New)
    DefaultOwner *FileOwner
    ForceOwner   bool

    // File handles across restarts (fixed at New)
    HandleStateDir   string
    HandleGeneration string

    // Names (fixed at New)
    CaseInsensitive CaseInsensitivity
}
```

## Security Fields

| Field                   | Type                 | Default           | Description                                       |
| ----------------------- | -------------------- | ----------------- | ------------------------------------------------- |
| `ReadOnly`              | `bool`               | `false`           | Reject all write operations                       |
| `Secure`                | `bool`               | `false`           | Require privileged source ports (< 1024)          |
| `AllowedIPs`            | `[]string`           | `nil` (allow all) | IP addresses or CIDR subnets permitted to connect |
| `Squash`                | `string`             | `""` (none)       | UID/GID mapping: `"root"`, `"all"`, or `"none"`   |
| `MaxFileSize`           | `int64`              | `0`               | Maximum file size in bytes (0 = unlimited)        |
| `EnableRateLimiting`    | `bool`               | `false`           | Enable per-IP and global rate limiting            |
| `RateLimitConfig`       | `*RateLimiterConfig` | default config    | Detailed rate limiting parameters                 |
| `TLS`                   | `*TLSConfig`         | `nil` (disabled)  | TLS/mTLS configuration                            |
| `AllowedUIDs`           | `[]uint32`           | `nil` (any uid)   | AUTH_SYS uids served (see below)                  |
| `AllowAnySymlinkTarget` | `bool`               | `false`           | Let SYMLINK make absolute and `..` targets        |

**Note on AllowedUIDs:** when not empty, only AUTH_SYS requests whose credential carries a listed uid (as sent, before squashing) reach NFS procedures; other uids and AUTH_NONE are refused with `AUTH_ERROR`. NULL and the MOUNT protocol are answered for any uid (macOS `mount_nfs` sends MNT as uid 0), but an unlisted uid may MNT only the export's root; any other path answers `MNT3ERR_ACCES` whether it exists or not. AUTH_SYS uids are asserted by the client, so this limits accidents and casual misuse, not a local user who forges credentials. Changeable at runtime. See the [security guide](../guides/security.md#limiting-uids).

**Note on RateLimitConfig:** When `RateLimitConfig` is nil, `New()` creates a default config so that rate limiting is ready if enabled later at runtime. Rate limiting itself is off unless `EnableRateLimiting` is explicitly set to `true`.

### Squash Modes

| Value            | Behavior                                             |
| ---------------- | ---------------------------------------------------- |
| `"none"` or `""` | No mapping. Client UIDs/GIDs used as-is.             |
| `"root"`         | UID 0 mapped to nobody (65534). GID 0 also squashed. |
| `"all"`          | All UIDs/GIDs mapped to nobody (65534).              |

Squash mode cannot be changed at runtime. Attempting to change it via `UpdatePolicyOptions` or `UpdateExportOptions` returns an error.

## Ownership Fields

| Field          | Type         | Default                                               | Description                                                                                                         |
| -------------- | ------------ | ----------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| `DefaultOwner` | `*FileOwner` | `nil` (the serving process's uid/gid; 0/0 on Windows) | Owner reported for files whose `os.FileInfo.Sys()` is not a `*syscall.Stat_t` (memfs, virtual filesystems, Windows) |
| `ForceOwner`   | `bool`       | `false`                                               | Report `DefaultOwner` for every file, even those with a real OS owner                                               |

Files from the operating system's own filesystem (osfs on unix, and wrappers
such as basefs that pass `Sys()` through) report their real owner. Everything
else reports `DefaultOwner`, so by default the user who runs the server owns
the export, which keeps clients such as git ("detected dubious ownership")
working. Both fields are fixed by `New()`: `UpdateExportOptions` returns an
error if either is changed (a nil `DefaultOwner` means "unchanged"), and
`GetExportOptions` reports the values in effect, so its result can always be
passed back.

`FileOwner` is `struct { UID, GID uint32 }`.

## Transfer and I/O Fields

| Field               | Type   | Default           | Description                   |
| ------------------- | ------ | ----------------- | ----------------------------- |
| `TransferSize`      | `int`  | `65536` (64 KB)   | Max bytes per read/write RPC  |
| `Async`             | `bool` | `false`           | Allow async (unstable) writes |
| `SendBufferSize`    | `int`  | `262144` (256 KB) | TCP send buffer size          |
| `ReceiveBufferSize` | `int`  | `262144` (256 KB) | TCP receive buffer size       |

## Cache Fields

| Field                  | Type            | Default | Description                                     |
| ---------------------- | --------------- | ------- | ----------------------------------------------- |
| `AttrCacheTimeout`     | `time.Duration` | `5s`    | TTL for cached file attributes                  |
| `AttrCacheSize`        | `int`           | `10000` | Max entries in the attribute cache (LRU)        |
| `CacheNegativeLookups` | `bool`          | `false` | Cache "file not found" results                  |
| `NegativeCacheTimeout` | `time.Duration` | `5s`    | TTL for negative cache entries                  |
| `EnableDirCache`       | `bool`          | `false` | Cache directory listings                        |
| `DirCacheTimeout`      | `time.Duration` | `10s`   | TTL for cached directory entries                |
| `DirCacheMaxEntries`   | `int`           | `1000`  | Max directories in cache                        |
| `DirCacheMaxDirSize`   | `int`           | `10000` | Max entries per directory before skipping cache |

Changes made through the server are seen by the next request: every
procedure invalidates what it changed, including everything below a renamed
directory. Entries are keyed by the name as spelled, so on a case-insensitive
host filesystem (APFS and NTFS by default) an entry cached under another
spelling of the same name (`/F` for `/f`) is seen to change only once its
timeout has passed. Changes made to the exported filesystem by anything else (another
process, another server over the same files) are seen once the relevant
timeout has passed: `AttrCacheTimeout` for attributes, `NegativeCacheTimeout`
for a name that did not exist, `DirCacheTimeout` for listings. A changed
timeout applies to entries already cached.

## Write Durability Fields

| Field           | Type      | Default                   | Description                                                              |
| --------------- | --------- | ------------------------- | ------------------------------------------------------------------------ |
| `WriteVerifier` | `[8]byte` | random at `New` (if zero) | The write verifier WRITE and COMMIT replies carry; changeable at runtime |

WRITE replies `UNSTABLE` to a client that asks for UNSTABLE, and syncs the
file (`File.Sync`) before replying `FILE_SYNC` to one that asks for
`FILE_SYNC` or `DATA_SYNC`. COMMIT calls `File.Sync` and answers its
error, never `NFS3_OK`, when it fails. The data of UNSTABLE WRITEs goes to
one file the server holds open for the path across WRITEs and READs (closed
after 5 seconds idle, when more than 64 are open, before the server removes
or renames the path, and at `Unexport` and `Close`), so a backend sees one
open and one close per burst of writes, and `Sync` on the file it wrote. A
backend whose `Sync` does nothing still works; its data is then as durable
as its `Close` makes it.

A client keeps the data of its UNSTABLE WRITEs until a COMMIT returns the
verifier those WRITEs returned; **when the verifier has changed, it sends
that data again.** The verifier must therefore change whenever data
written UNSTABLE but not yet committed may have been lost:

- The default, a random verifier per server instance, covers a restart.
- An owner that discards uncommitted data while the server runs (garbage
  collection of a backend's unsynced writes) sets a new verifier with
  `UpdateExportOptions` or `UpdateTuningOptions` first, then discards
  only data written before the change. A WRITE reads the verifier before
  writing and a COMMIT after syncing, so this order never lets a COMMIT
  vouch for discarded data.
- The server sets a new one itself when a `Sync` fails, or the `Close` of a
  file written since its last `Sync` fails, as Linux knfsd does.

Reuse a value across restarts only when every byte written under it
survives them. Zero in an update means unchanged.

## File Handle Fields

| Field              | Type     | Default            | Description                                                            |
| ------------------ | -------- | ------------------ | ---------------------------------------------------------------------- |
| `MaxHandles`       | `int`    | `100000` (if <= 0) | Capacity of the file handle cache; changeable at runtime               |
| `HandleStateDir`   | `string` | `""` (none)        | Directory keeping the handle key and journal across restarts           |
| `HandleGeneration` | `string` | `""`               | Namespace version handles are issued for; another one makes them stale |

File handles name their objects by path and are checked against a log of the
server's own REMOVEs, RMDIRs and RENAMEs, so the server resolves any handle it
issued whether or not it is cached (see
[File Handle System](../internals/file-handles.md)). `MaxHandles` therefore
bounds memory (about 300 bytes a handle) and the cost of misses (a decode, a
tag check and a stat), not correctness: a client does not see
`NFS3ERR_STALE` for an object that still exists, however many handles it
holds, except in the documented residual cases (handles of paths about 45 or
more components deep, a tail walk past its caps, a rare spurious STALE after
the tombstone table folds), each counted in a metric. Size it above the number of files and directories clients keep using,
so their requests stay cache hits; four `git` workspaces of the Kubernetes
tree use about 200,000. A smaller value set with `UpdateExportOptions` evicts
at once. `GetExportOptions` reports the value in effect.

Without `HandleStateDir`, handles are valid for the life of the server, and
clients remount after a restart. With it, the server keeps its key
(`handle.key`) and a journal of its REMOVEs, RMDIRs and RENAMEs
(`handle.log`, written before each reply and synced every second) in the
directory, locked against a second server, so handles mean exactly what they
did after a restart and clients carry on. Deleting the directory, or giving a
new one, invalidates every handle. It is fixed at `New`: `UpdateExportOptions`
rejects a different non-empty value (empty means unchanged).

**`HandleStateDir` assumes a stable namespace.** Handles name paths, so after
a restart a handle reaches whatever is at its path then. The journal covers
changes made through the server; it cannot cover a namespace rebuilt while
the server was down. If a path can name a different object after a restart
(route names reassigned at start, such as switchfs routes numbered `/w1`,
`/w2`... per run; a backend replaced by one with other content), an old
handle silently resolves to the new object: not `NFS3ERR_STALE`, the wrong
file. Route names and backends must not be reused for different content
across restarts. A caller that cannot guarantee that sets `HandleGeneration`
and changes it whenever the namespace changes: per server run, or per
version of the route set.

`HandleGeneration` is an opaque byte string, compared exactly, folded into
the keys handles are tagged with. A handle issued under one generation is
`NFS3ERR_STALE` under any other, decided by its tag alone: no filesystem call
for an inline handle (all but paths too long to spell in 64 bytes; a tail
handle costs at most its capped walk, as a forged one does). The generation
is recorded in the state directory (`handle.gen`, absent for the empty
generation). A start with a different generation makes the key and journal
afresh rather than refusing, since bumping the generation per run is the
intended use; so handles of a generation stay stale even if it comes back
later. A start with the same generation keeps handles valid. The empty
generation (the default) tags handles exactly as before the option existed,
so existing state directories keep their handles. Without `HandleStateDir`
the generation has no effect: every restart already makes every handle stale.
It is fixed at `New`: `UpdateExportOptions` rejects a different non-empty
value (empty means unchanged); to change it, close the server and make a new
one.

## Case-Insensitive Names

| Field             | Type                | Default                     | Description                                             |
| ----------------- | ------------------- | --------------------------- | ------------------------------------------------------- |
| `CaseInsensitive` | `CaseInsensitivity` | `CaseInsensitiveAuto` (`0`) | What PATHCONF reports in `case_insensitive` (see below) |

`CaseInsensitivity` has three values:

| Value                  | PATHCONF `case_insensitive`                            | FSINFO `FSF3_HOMOGENEOUS` |
| ---------------------- | ------------------------------------------------------ | ------------------------- |
| `CaseInsensitiveAuto`  | Probed per handle: what the handle's filesystem does   | not set                   |
| `CaseInsensitiveTrue`  | `true` for every handle                                | set                       |
| `CaseInsensitiveFalse` | `false` for every handle (what absnfs reported before) | set                       |

`case_preserving` is always `true`.

**Auto** answers for the directory a handle is (or, for anything else, is
in). It probes reading only: it reads up to 64 names of the directory, looks
up to 4 of them up with the case of their letters swapped (`README.md` as
`readme.MD`; see Unicode below for names without an ASCII letter), and
compares what it finds with the entry: device and inode
numbers (also through wrappers such as basefs, which `os.SameFile` cannot
see through), a memfs inode, else type, size and modification time. A
swapped name that finds the same file means names fold there; one that finds
nothing or another file, or two listed names differing only in case, mean
they do not. Any probed name that folds decides it, since a union may fold
names in one layer and not in another. A directory with no name to try
(empty, or no name with a letter that has another case) takes the answer
of its nearest ancestor that has one, and the export's when none does (see
Unicode below). Answers, undecided ones too, are cached per
directory for `AttrCacheTimeout` (at most 4,096 directories) and dropped
when the server removes, renames or replaces the directory, so a backend
swapped behind the server's back (a switchfs route) is seen within the
timeout.

**What clients ask.** The macOS client sends PATHCONF for the root once, at
mount, and answers later `pathconf(2)` calls on the root from that. With
`FSF3_HOMOGENEOUS` set (true or false) it answers `pathconf(2)` on every
other object from it too. Without it (auto), for any object other than the
root it sends PATHCONF on each `pathconf(2)` call. Either way it takes the volume's capabilities
(`VOL_CAP_FMT_CASE_SENSITIVE`, as Finder and `getattrlist` see them) from
the root's answer at mount. So an export whose root is case-sensitive but
whose content folds, such as a memfs root listing switchfs routes to host
directories on a case-insensitive volume (c4d's workspaces), reports
case-sensitive for its root and volume under auto, and for a route
directory with no names yet. Auto also answers `false` for a union whose
probed names all come from a case-sensitive lower layer (a c4fs view) while
its writable layer folds. Such exports set `CaseInsensitiveTrue`. It is
harmless over the case-sensitive root: the client sends the names it is
given and the root answers them exactly.

**Unicode.** A name with an ASCII letter is probed with its ASCII letters
swapped, and nothing else, since every folding filesystem folds those
alike. A name with no ASCII letter is probed with each letter that has
exactly one other case swapped (`Фото` as `фОТО`, `Ü` as `ü`); letters whose
case is not one to one (`İ`, `ı`, `ß`, `ς`) are left alone, as filesystems
fold them differently. A directory whose names have no such letter (only
digits, or kana) takes its nearest ancestor's answer. Where no ancestor up
to the root has one, it takes the export's answer: that of the first probe
on the export that decided one, or failing that a guess from a name that
is spelled differently composed and decomposed (`é` as U+00E9, or as `e`
followed by U+0301; `が`), looked up the other way: a filesystem that finds
the same file (APFS, HFS+) is guessed to fold, one that does not (ext4,
NTFS) not to. Once the export has an answer it keeps it for the server's
lifetime, so fileids folded by it (see below) do not change when names are
added; with none, the answer is false. Undecided directories are cached
like decided ones. `case_insensitive` says nothing about normalisation:
PATHCONF has no field for it, and absnfs neither normalises nor folds names
it passes to the filesystem. It passes them as the client spelled them, and
the filesystem decides what they find (it folds them only into fileids, see
below).

The answer does not change what the server does with names: LOOKUP, CREATE
and RENAME pass names to the filesystem as spelled. File handles name paths
as spelled, so `README.md` and `Readme.md` have different handles even where
they are the same file, and the client sends a case-only RENAME (`mv
README.md Readme.md`) to the server whatever PATHCONF says.

**File ids.** Where the answer is true, every spelling of an object reports
one fileid, as every spelling reports one inode number on the host volume:
`docs`, `Docs` and `DOCS`, and READDIR's `docs` entry, all report the same.
Without that `/bin/mv docs Docs` over the mount stats `Docs`, sees another
inode, takes it for an existing directory and fails (`rename docs to
Docs/docs: Invalid argument`), and `cp` and `rsync` same-file checks are
misled. The fileid stays a hash of the path, with each name in a directory
whose answer is true replaced by its fold key: Unicode's canonical caseless
match (the decomposed full case folding of the decomposed name), which is
how APFS matches names, so `straße` and `STRASSE`, or `é` composed and
decomposed, also share one. With auto each name is folded by its own
directory's answer, so a case-sensitive directory keeps `A` and `a` apart
even with folding routes below it; a name that is its own key (lowercase
ASCII) needs no answer and costs nothing. Rare differences from the host:
Cherokee letters fold on APFS but keep a fileid per case here, and on NTFS,
which neither normalises nor folds `ß`, two files named `straße` and
`STRASSE` (or `é` composed and decomposed) report one fileid.

A directory that folds can still list two names with one fold key: a union
whose writable layer is case-sensitive (memfs) over a folding host layer
holds both `w1` and `W1` once a client creates both, and so does a
case-sensitive root under `CaseInsensitiveTrue`. Those names are two files,
so each keeps the fileid of its own spelling, as before folding, while the
directory's other names still fold; a spelling it does not list (`READme`
when it lists `Readme` and `README`) also reports its own. Finding the
shared keys reads the directory's whole listing, once per directory per
`AttrCacheTimeout` (the probe's listing serves when it read every name, 64
or fewer). When a second spelling of a name appears, the first one's fileid
changes from the folded one to that of its own spelling (unless it is
spelled as its key, as `w1` is), a change the directory's content makes.

Fileids folded by an answer that later changes change with it. That
happens only where the answer was not the directory's own: an undecided
directory that later gets a name to probe by, when the export's answer
differed (on case-sensitive APFS, which matches composed and decomposed
names and so is guessed to fold, or on NTFS, which does not and so is
guessed not to, for names such as `が` before anything on the export
decided), or a union whose layers answer differently. Where the answer is
false, fileids are exactly what absnfs reported before.

It is fixed by `New()`: `UpdateExportOptions` rejects a different value, and
`GetExportOptions` reports the value in effect. `New()` rejects a value
other than the three above.

## Connection Fields

| Field            | Type            | Default                | Description                                     |
| ---------------- | --------------- | ---------------------- | ----------------------------------------------- |
| `MaxWorkers`     | `int`           | `runtime.NumCPU() * 4` | Worker pool goroutines                          |
| `MaxConnections` | `int`           | `100`                  | Simultaneous client connections (0 = unlimited) |
| `IdleTimeout`    | `time.Duration` | `5m`                   | Close connections idle longer than this         |
| `TCPKeepAlive`   | `bool`          | `true`                 | Enable TCP keep-alive probes                    |
| `TCPNoDelay`     | `bool`          | `true`                 | Disable Nagle's algorithm                       |

## TimeoutConfig

Passed via `ExportOptions.Timeouts`. When nil, `New()` populates all fields with defaults.

```go
type TimeoutConfig struct {
    ReadTimeout    time.Duration // default: 30s
    WriteTimeout   time.Duration // default: 60s
    LookupTimeout  time.Duration // default: 10s
    ReaddirTimeout time.Duration // default: 30s
    CreateTimeout  time.Duration // default: 15s
    RemoveTimeout  time.Duration // default: 15s
    RenameTimeout  time.Duration // default: 20s
    HandleTimeout  time.Duration // default: 5s
    DefaultTimeout time.Duration // default: 30s
}
```

## LogConfig

Passed via `ExportOptions.Log`. When nil, a no-op logger is used.

```go
type LogConfig struct {
    Level        string // "debug", "info", "warn", "error" (default: "info")
    Format       string // "json" or "text" (default: "text")
    Output       string // "stdout", "stderr", or file path (default: "stderr")
    LogClientIPs bool   // Include client IPs in logs (default: false)
    LogOperations bool  // Log each NFS operation with timing (default: false)
    LogFileAccess bool  // Log file open/close patterns (default: false)
    MaxSize      int    // Max log file MB before rotation (reserved, default: 100)
    MaxBackups   int    // Old log files to retain (reserved, default: 3)
    MaxAge       int    // Days to retain old logs (reserved, default: 28)
    Compress     bool   // Gzip rotated files (reserved, default: false)
}
```

**Note:** The `MaxSize`, `MaxBackups`, `MaxAge`, and `Compress` fields are reserved for future log rotation support. They are accepted but have no effect.

## RateLimiterConfig

Passed via `ExportOptions.RateLimitConfig`. Default values from `DefaultRateLimiterConfig()`:

```go
type RateLimiterConfig struct {
    GlobalRequestsPerSecond        int           // default: 10000
    PerIPRequestsPerSecond         int           // default: 1000
    PerIPBurstSize                 int           // default: 500
    PerConnectionRequestsPerSecond int           // default: 500
    PerConnectionBurstSize         int           // default: 100
    ReadLargeOpsPerSecond          int           // default: 100
    WriteLargeOpsPerSecond         int           // default: 50
    ReaddirOpsPerSecond            int           // default: 50
    MountOpsPerMinute              int           // default: 10
    FileHandlesPerIP               int           // default: 10000
    FileHandlesGlobal              int           // default: 1000000
    CleanupInterval                time.Duration // default: 5m
}
```

## TLSConfig

Passed via `ExportOptions.TLS`. When nil, connections are unencrypted.

```go
type TLSConfig struct {
    Enabled                  bool
    CertFile                 string           // PEM server certificate path
    KeyFile                  string           // PEM server key path
    CAFile                   string           // PEM CA certificate for client verification
    ClientAuth               tls.ClientAuthType
    MinVersion               uint16           // default: TLS 1.2
    MaxVersion               uint16           // default: TLS 1.3
    CipherSuites             []uint16         // empty = secure defaults
    PreferServerCipherSuites bool
    InsecureSkipVerify       bool             // testing only
}
```

## Example: Custom Configuration

```go
server, err := absnfs.New(fs, absnfs.ExportOptions{
    ReadOnly:   true,
    AllowedIPs: []string{"192.168.1.0/24", "10.0.0.5"},
    Squash:     "root",

    TransferSize:     131072, // 128 KB
    AttrCacheTimeout: 10 * time.Second,
    AttrCacheSize:    20000,
    EnableDirCache:   true,

    MaxConnections: 50,
    MaxWorkers:     32,
    IdleTimeout:    10 * time.Minute,

    Log: &absnfs.LogConfig{
        Level:         "info",
        Format:        "json",
        Output:        "/var/log/nfs.log",
        LogOperations: true,
    },

    Timeouts: &absnfs.TimeoutConfig{
        ReadTimeout:  60 * time.Second,
        WriteTimeout: 120 * time.Second,
    },
})
```
