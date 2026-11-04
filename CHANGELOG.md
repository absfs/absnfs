# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

Planned as v1.2.0: it adds exported API (below) and changes three behaviours
existing deployments can see (see "Changed"). Fixes found serving workspaces
to the macOS NFS client, which is stricter than Linux's.

### Added

- `ExportOptions.CaseInsensitive` (`CaseInsensitivity`: `CaseInsensitiveAuto`,
  the default, `CaseInsensitiveTrue`, `CaseInsensitiveFalse`): what PATHCONF
  reports as `case_insensitive`. Auto probes, read-only, the directory of
  each handle (a few names looked up with their ASCII letters' case swapped,
  compared by device and inode, memfs inode, or type, size and time), falls
  back to the nearest ancestor that can be probed and then false, and caches
  the answer per directory for `AttrCacheTimeout`, dropping it when the
  server removes, renames or replaces the directory. Explicit values apply
  to every handle. Fixed at `New`. Exports with a case-sensitive root over
  folding content (a memfs root listing switchfs routes to host directories
  on APFS) should set `CaseInsensitiveTrue`: the macOS client takes the
  volume's answer from the root. The probe detects only ASCII case folding;
  Unicode normalisation and non-ASCII case are left to the filesystem.
  The answer does not make a case-only rename work: the client sends that
  RENAME whatever PATHCONF says. A union over basefs after v1.0.0 (which
  wraps `FileInfo`) returned success for `Rename("/README.md", "/Readme.md")`
  and changed nothing, whether `README.md` was in the lower layer or copied
  up; unionfs fixed that (absfs/unionfs#11), and absnfs now requires that
  unionfs (see "Changed").
- A macOS end-to-end test, `TestMacOSCaseOnlyRename`, run with
  `ABSNFS_MACOS_MOUNT=1`: `mv README.md Readme.md` over the mount in a host
  directory, a union of host directories (with `README.md` in the lower
  layer, and copied up), and union workspaces under a memfs root (in one
  workspace with `README.md` in the lower layer, in another after an edit
  over the mount copied it up), and `pathconf(_PC_CASE_SENSITIVE)` on each
  and on a memfs export. A unit test, `TestRenameCaseOnlyUnion`, does the
  same renames through the Go API where the temporary directory folds case.
  Both run over basefs confined to its root by `os.Root` (osfs `OpenRoot`)
  where the build supports it, Go 1.25 or later on macOS, as c4d serves
  workspaces, and fail if basefs is confined anywhere else or not there.
- `ExportOptions.AllowedUIDs` (and `PolicyOptions.AllowedUIDs`): when not
  empty, only AUTH_SYS requests carrying a listed uid reach NFS procedures;
  other uids and AUTH_NONE get `AUTH_ERROR`. NULL and MOUNT are answered for
  any uid (macOS `mount_nfs` sends MNT as uid 0), but an unlisted uid may
  MNT only the export's root: any other path answers `MNT3ERR_ACCES`
  whether it exists or not. AUTH_SYS uids are the client's claim, so this
  limits accidents and casual misuse, not forged credentials. Changeable at
  runtime.
- README "Security" section and security guide sections on who can use an
  export (any local user, as any uid, on a loopback bind), limiting uids,
  and exports of a host directory.
- `ExportOptions.MaxHandles` (and `TuningOptions.MaxHandles`): the capacity
  of the file handle cache, default 100,000 (<= 0). It bounds memory and the
  miss rate only; changeable at runtime, and a smaller value evicts at once.
- `ExportOptions.HandleStateDir`: keeps the handle key and a journal of the
  server's REMOVEs, RMDIRs and RENAMEs in a directory (locked against a
  second server), so file handles survive a restart with exactly their old
  meaning. Fixed at `New`. It assumes the namespace is stable across
  restarts: the same path names the same object, and route names and
  backends are not reused for different content.
- `ExportOptions.HandleGeneration` (`string`): a version of the exported
  namespace, folded into the keys handles are tagged with, for callers that
  cannot keep the namespace stable across restarts (switchfs routes named
  `/w1`, `/w2`... per run would otherwise let a persisted handle resolve into
  another workspace). Handles issued under one generation are
  `NFS3ERR_STALE` under another, by their tag (no filesystem call for an
  inline handle). Kept in the state directory as `handle.gen`; a start with
  a different generation makes the key and journal afresh. The empty
  generation (default) derives the keys as before, so existing state
  directories keep their handles. Fixed at `New`.
- `NFSMetrics` handle cache counters: `HandleCacheSize`,
  `HandleCacheCapacity`, `HandleCacheMisses`, `HandleTailWalkFailures`,
  `HandleTombstoneFolds`, `HandleRedirectsDropped`, `HandleInflightTimeouts`.
- `cmd/fhgen`, which generates the frozen Huffman table of version 1 handles
  and measures how many paths of a tree fit in a handle.
- `ExportOptions.DefaultOwner` (`*FileOwner`): the uid/gid reported for files
  whose `os.FileInfo` records no owner. Defaults to the user running the
  server (0/0 on Windows, where `os.Getuid` returns -1).
- `ExportOptions.ForceOwner`: report `DefaultOwner` for every file, even those
  with a real owner.
- `FileOwner` type. `GetExportOptions` reports the owner in effect.
- `ExportOptions.AllowAnySymlinkTarget`: let clients create symbolic links
  with absolute or `..` targets (`ln -s ../README.md docs/up`), for
  filesystems that resolve links within their own namespace. Off by default,
  so exports of a host directory keep refusing links that could lead out of
  the export. It is a policy option: `UpdateExportOptions` can change it.
- `AbsfsNFS.ExportPort`, the port an `Export` listens on, so a caller that
  passed port 0 can tell clients where to mount.
- Owner override for WRITE: the owner of a file may write it even when its
  mode denies writing, so a client that creates a file read-only and then
  writes it (git's 0444 object files) works over a stateless protocol.
  Overrides are serialized so parallel WRITEs to one file all succeed.
- A macOS end-to-end test, `TestMacOSUserMount`, run with
  `ABSNFS_MACOS_MOUNT=1`: mounts osfs and memfs exports as the current user
  and checks listing, ownership, symlinks (`chmod -h` and `touch -h` on a
  link fail without touching its target), a directory swapped for a link
  behind the server's back (nothing outside the export changes), file
  operations, locking with `locallocks`, removing a large directory while
  listing it, a git commit, and an `AllowedUIDs` export. It mounts with
  `locallocks,noatime`.
- macOS end-to-end file handle tests, run with `ABSNFS_MACOS_MOUNT=1`:
  `TestMacOSHandlesSurviveEviction` (git in one workspace with a 64-handle
  cache) and `TestMacOSWorkspacesWarmStatus` (four unionfs workspaces under
  a switchfs; warm `git status` in the oldest must stay within 3x of the
  newest).
- README sections on mounting from macOS as a normal user and on file
  ownership. The recipe uses `locallocks` (with `nolocks`, `go build` fails to
  lock `go.mod`) and `noatime` (without it the macOS client sets the access
  time of every file read through mmap, which makes a union copy it up).

### Fixed

- **A case-only RENAME of a non-empty directory failed with
  `NFS3ERR_NOTEMPTY`** on a filesystem that wraps its `os.FileInfo` over a
  case-insensitive host (basefs after v1.0.0 over osfs on APFS): the check
  that the target is the source itself used `os.SameFile`, which cannot see
  through a wrapper. It now compares device and inode numbers, or memfs
  inodes, as the PATHCONF probe does.
- **Type decisions that gate an operation use the object as it is now.**
  LOOKUP, READDIR, READDIRPLUS, REMOVE and RMDIR decided whether a handle
  named a directory from the attributes it was issued with, and CREATE,
  MKDIR, SYMLINK and RENAME did not check at all, so a directory replaced by
  a symbolic link behind the server's back was still used as a directory:
  over osfs or basefs the host followed the link, and a client could list,
  read, create, remove and rename files outside the export. READ, WRITE and
  SETATTR likewise opened or truncated a file that had become a link to one
  outside. Each now checks a fresh `Lstat`: `NFS3ERR_NOTDIR` for a directory
  argument that is not one, `NFS3ERR_ISDIR` or `NFS3ERR_INVAL` for READ,
  WRITE and SETATTR size on anything but a regular file (as Linux knfsd),
  `NFS3ERR_INVAL` for READLINK on what is no longer a link. Every directory
  above a handle's object is checked too: a handle below a directory
  replaced by a link answers `NFS3ERR_STALE` instead of reaching, read and
  write, the subtree the link leads to.
- **UNCHECKED CREATE of a symbolic link truncated its target.** It opened an
  existing name with `O_TRUNC`, which the host resolves through a link, even
  one leading out of the export, and answered `NFS3_OK`. A name that is a
  link, a directory or anything but a regular file now answers
  `NFS3ERR_EXIST` without being opened, as Linux knfsd does; `Create`
  returns an `os.ErrExist` error for it.
- **MNT walked symbolic links.** It looked up any client path, through links
  that lead out of the export. Every component must now be a directory:
  `MNT3ERR_NOENT` on the way, `MNT3ERR_NOTDIR` for the last.
- **SETATTR on a symbolic link changed its target.** Chmod, Chtimes and
  Truncate were applied to the link's path, which a host follows, and the
  owner was set with Chown. The owner is now set with `Lchown`, on the link;
  mode, size and times are refused with `NFS3ERR_INVAL` before anything
  changes (`chmod -h` and `touch -h` report "Invalid argument"), as absnfs4
  refuses them with `NFS4ERR_INVAL`. `SetAttr` refuses them too.
- The `StaleHandles` metric was never incremented: nothing recorded STALE
  errors. Every `NFS3ERR_STALE` reply is now counted.
- A handle whose object was removed behind the server's back answered
  `NFS3ERR_NOENT` (LOOKUP, CREATE, READDIR, READ, READLINK and others), which
  tells a client a name is missing from a directory it still trusts. It now
  answers `NFS3ERR_STALE`, as RFC 1813 defines; NOENT is kept for a missing
  name. `Lookup`-based Go API errors still match `os.ErrNotExist`.
- The attribute cache never answered GETATTR (or ACCESS, or any procedure
  reporting pre- or post-operation attributes): it dropped the validity time
  when handing out a copy, so every request stat'ed the filesystem. It now
  serves entries within `AttrCacheTimeout`, and a client's GETATTR, ACCESS
  and LOOKUP traffic for a cached file costs no filesystem stat at all.
- Cached attributes, misses and listings could outlive the change they
  describe. RENAME of a directory left everything cached below the old and
  new names (a file looked up under the old name was still found, a miss
  below the new name hid the moved files, and listings stayed stale); MKDIR
  invalidated nothing, so with negative caching it failed with
  `NFS3ERR_NOENT` after creating the directory, and with the directory
  cache the parent's listing lacked it; a SETATTR or WRITE that failed
  partway kept the old attributes; and a LOOKUP, GETATTR or listing that
  read the filesystem just before a concurrent change cached the old answer
  after it. Every procedure now invalidates exactly what it changed (the
  whole subtree for RENAME), after changing it, and a result read before an
  invalidation is not cached.
- Decisions that must not use cached attributes no longer do. SETATTR
  compares the request with the file's current attributes, so a field
  changed behind the server's back since it was cached is still set, and the
  `sattrguard3` ctime check returns `NFS3ERR_NOT_SYNC` for a file changed
  since the client's ctime. WRITE's owner override goes to the file's current
  owner, read with the mode it checks.
- Changes made through an `NFSNode` from `Lookup` (`Write`, `WriteAt`,
  `WriteString`, `Truncate`, `Chmod`, `Chown`, `Chtimes`) invalidate the
  server's cached attributes, so the next `GetAttr` reports them.
- Handles named paths and ignored RENAME: after renaming a directory, a
  client's handles for the files below it returned `NFS3ERR_NOENT`, and a
  file made again under the old name was given the moved file's handle, so
  a client holding it reached a different file. Handles now follow RENAME;
  the handle of an object RENAME replaced or REMOVE/RMDIR deleted reports
  `NFS3ERR_STALE`, and its number is never reused.
- Disabling negative caching at run time dropped no entries, and MKDIR or
  SYMLINK failing with `NFS3ERR_EXIST` kept a cached miss for the name, so
  LOOKUP kept answering `NFS3ERR_NOENT` for a name that existed.
- LOOKUP replies carried the directory's attributes from when its handle was
  made, never refreshed, so clients could keep a stale directory mtime.
- Lowering `AttrCacheTimeout`, `NegativeCacheTimeout` or `DirCacheTimeout`
  now also bounds entries cached under the old value.
- READLINK refused (`NFS3ERR_IO`) any relative target with a `..` component,
  so a link such as `docs/up -> ../README.md` read as EIO through a mount, and
  `git add`, `git status` and `rg` failed on trees containing one. Targets are
  now returned as stored: the client resolves them itself, so their text gives
  it nothing it could not reach.

- READDIRPLUS reported fileid 0 for every entry, and macOS hid them all, so
  `ls` showed an empty directory. SETATTR replies and the export root also
  reported fileid 0. Attributes are now built in one place
  (`statAttrs`/`attrsFromInfo`), so every procedure reports the same non-zero
  fileid for an object.
- READDIRPLUS followed symlinks and cached the target's attributes, so links
  were listed, and then looked up, as regular files or directories. It now
  uses Lstat, as do SETATTR, WRITE and RMDIR when they describe an object.
- RMDIR on a symlink to a directory removed the link; it now returns
  `NFS3ERR_NOTDIR`.
- Every file was reported as owned by uid 0, so git refused the export
  ("detected dubious ownership") and the mounting user got only the "other"
  permission bits. absnfs now reports the real owner from `*syscall.Stat_t`
  where the filesystem provides one, and `DefaultOwner` otherwise.
- GETATTR no longer inherits uid/gid from whatever a previous request stored
  on the node; ownership always comes from the filesystem or the export.
- SETATTR of only atime (or only mtime) no longer resets the other time.
- CREATE now gives new files the caller's uid/gid (best effort), as MKDIR and
  SYMLINK already did.
- RENAME onto an existing name now replaces it as RFC 1813 requires, also on
  filesystems whose Rename refuses an existing target (memfs). Incompatible
  targets return `NFS3ERR_ISDIR`, `NFS3ERR_NOTDIR` or `NFS3ERR_NOTEMPTY`.
  The server checks these itself before calling the filesystem, so the
  status no longer depends on the host's rename rules or errno spelling (on
  Windows `ENOTDIR` is the errno for a missing path, so a directory renamed
  over a file was reported as `NFS3ERR_NOENT`). A directory replacing an
  empty directory falls back to remove-and-rename on any backend error, and
  puts the empty directory back if the rename still fails.
- CREATE in GUARDED or EXCLUSIVE mode truncated an existing file and
  reported success, so `open(O_CREAT|O_EXCL)` could not protect a lock file.
  Both modes now fail with `NFS3ERR_EXIST` and leave the file alone. EXCLUSIVE
  stores the client's verifier in the new file's times, so a retransmitted
  request still succeeds (RFC 1813 section 3.3.8).
- `ENOTEMPTY` maps to `NFS3ERR_NOTEMPTY` instead of `NFS3ERR_EXIST`.
- RENAME of a directory into its own subtree (`/a` to `/a/b`) returns
  `NFS3ERR_INVAL`. On memfs it used to succeed and detach the whole subtree.
- READDIR and READDIRPLUS cookies were list positions, so a client that
  removed entries while paging through a directory (an incremental `rm -rf`)
  skipped entries and left a non-empty directory. Cookies are now derived
  from entry paths and entries are returned in cookie order, so resuming
  after a cookie is unaffected by removals.
- An UNCHECKED CREATE of an existing file no longer changes its mode or
  owner (it still truncates it, as `open(O_CREAT|O_TRUNC)` does), and a
  failure after opening it no longer deletes it.
- `UpdateExportOptions` returns an error when asked to change `DefaultOwner`
  or `ForceOwner` instead of silently ignoring the change, and validates
  `Squash` before applying anything rather than after the tuning update.
  `syscall.EINVAL` now maps to `NFS3ERR_INVAL`.
- **Evicted handles went stale, and stale handles could reach other
  objects.** Handles were 8-byte numbers that meant something only while
  they sat in an in-memory table of 100,000 entries. A client holding more
  (four `git` workspaces of the Kubernetes tree, about 150,000 paths) got
  "Stale NFS file handle" for evicted ones, and warm `git status` in older
  workspaces took 6-7 s instead of 0.4-0.7 s because eviction defeated the
  client's attribute cache. Released numbers were also reused, and a
  restarted server numbered from 1 again, so an old handle could reach
  another file. Handles are now path handles (version 1, 10 to 64 bytes): a
  keyed tag, the object's type, its path below the export root (Huffman
  coded, or with hashed trailing components when long), and a change-log
  epoch. The server resolves any handle it issued without the table, which
  is now only a cache, so eviction does not report `NFS3ERR_STALE` for an
  object that still exists (except in documented residual cases: handles of
  paths about 45 or more components deep, a tail walk past its caps, a rare
  spurious STALE after the tombstone table folds, each counted in a metric),
  and the same object keeps the same bytes. A log of the server's own
  REMOVEs, RMDIRs and RENAMEs (bounded, about 30 MB, briefly about 58 MB
  while its oldest records fold) makes the handle of a removed or replaced
  object stale and lets a renamed object's handle follow it, including every
  handle below a renamed directory, in O(1) instead of a scan of the table. Forged, garbled,
  legacy and empty handles are `NFS3ERR_STALE` and cost no filesystem I/O,
  except a forged tail handle, whose tag is checked at the end of a capped
  directory walk (at most 64 listings). A handle whose path has a REMOVE,
  RMDIR or RENAME in flight for over 5 seconds is answered
  `NFS3ERR_JUKEBOX`, so the client retries, not `NFS3ERR_STALE`.
  See `docs/internals/file-handles.md`.
- READDIRPLUS replies counted only the size already written against the
  client's `maxcount`, so a long last entry could exceed it; each entry's
  real size (handle and name included) is now counted before it is added.

### Deprecated

- `NewUint64MinHeap`: absnfs no longer uses it. It will be removed in the
  next major version.

### Changed

- **Requires basefs with absfs/basefs#9 (a7e0a24, on master 79e9a01), osfs
  master (26d9c50) and unionfs with absfs/unionfs#11 (d70ffdc).** basefs
  master confines a basefs over osfs to its root with `os.Root` (osfs
  `OpenRoot`) on Go 1.25 or later on Unix. #9 returns the host's own
  `FileInfo` below the root again, so `os.SameFile` works through basefs.
  Without it, basefs master's wrapped infos record no file identity on
  Windows. Exports could then not tell one file from another there, and
  `TestSameFile` fails. unionfs#11 compares file identities through
  wrapped infos, so a case-only rename in a union over basefs renames the
  file whether it is in the lower layer or was copied up. All three are
  pinned at pseudo-versions of those commits until they are tagged. CI now
  runs on Go 1.25 and the latest release as well as 1.23, so the confined
  stack is tested.
- **PATHCONF reports case sensitivity truthfully by default**
  (`CaseInsensitiveAuto`): an export of a directory on a case-insensitive
  host volume (APFS, NTFS by default) now answers `case_insensitive` true,
  where absnfs always answered false. In this mode FSINFO no longer sets
  `FSF3_HOMOGENEOUS`, so the macOS client, which still asks about the root
  once at mount, sends PATHCONF for any other object on each `pathconf(2)`
  call instead of answering it from the root's answer. `CaseInsensitiveFalse`
  restores both.
- **UNCHECKED CREATE of an existing regular file sets only the size its
  `sattr3` asks for**, as Linux knfsd does: without one the file keeps its
  content, with size 0 it is truncated. Before, it was always truncated.
  The Go API's `Create` still truncates.
- **`Export` binds `127.0.0.1`** instead of `localhost` (which Go resolved
  to the same address). For another bind address use `NewServer`.
- **Handles are 10 to 64 bytes and name paths.** Handles from earlier
  versions (8 bytes) report `NFS3ERR_STALE`: clients remount once after
  upgrading (macOS: `umount -f`, then mount). Without `HandleStateDir`, a
  restart makes every client handle stale, the mount root's included, so
  mounted clients remount; before, handles were numbered from 1 on every
  start, so a client usually kept a working root handle (and other old
  handles could silently reach different objects). A change made behind the
  server's back (backend writes, a switchfs route swap, a unionfs layer
  change) is seen by path: the handle reaches what is at its path now.
- `FileHandleMap.Allocate`, `Get`, `GetOrError` and `Release` are
  deprecated: the `uint64` they use is a handle's first 8 bytes, an
  identifier for logs. Debug logs print handles in hex.
  `InvalidFileHandleError` keeps only that identifier (`Handle`) and gains
  no field with the full handle bytes, which `GetOrError`, the only place
  it is made, does not have.
- **MOUNT v1 MNT is refused** with `PROG_MISMATCH` (versions 3 to 3). Its
  reply has a fixed 32-byte `fhandle`, which the new handles do not fit;
  before, it got a v3-format reply no v1 client could read. NULL, DUMP,
  UMNT, UMNTALL and EXPORT still answer v1.
- **Default owner.** Files whose filesystem records no owner (memfs, virtual
  filesystems, Windows) are reported as owned by the user running the server
  rather than by root (0/0), and osfs on unix reports real owners. An export
  served by a non-root process therefore shows clients different permission
  bits than before. Set `DefaultOwner: &absnfs.FileOwner{}` (and
  `ForceOwner: true`) to keep reporting 0/0.
- **`Export` speaks RPC record marking** on its TCP stream, as RFC 5531 and
  every kernel NFS client require; before, mount_nfs got "connection reset".
  A raw-RPC client written against the old unframed `Export` stream must now
  frame its messages (or use `NewServer` without `UseRecordMarking`).
- **Directory listing order** is by cookie (a hash of the entry path), not
  the filesystem's order.
- The macOS mount instructions (README, docs, `cmd/testserver` and the
  examples) no longer use `sudo` or `resvport`.

## [1.1.0]

Earlier history is in the git log.
