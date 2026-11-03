# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

Planned as v1.2.0: it adds exported API (below) and changes two behaviours
existing deployments can see (see "Changed"). Fixes found serving workspaces
to the macOS NFS client, which is stricter than Linux's.

### Added

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
  and checks listing, ownership, symlinks, file operations, locking with
  `locallocks`, removing a large directory while listing it, and a git
  commit.
- README sections on mounting from macOS as a normal user and on file
  ownership. The recipe uses `locallocks` (with `nolocks`, `go build` fails to
  lock `go.mod`) and `noatime` (without it the macOS client sets the access
  time of every file read through mmap, which makes a union copy it up).

### Fixed

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

### Changed

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
