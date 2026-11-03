# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

Fixes found serving workspaces to the macOS NFS client, which is stricter than
Linux's.

### Added

- `ExportOptions.DefaultOwner` (`*FileOwner`): the uid/gid reported for files
  whose `os.FileInfo` records no owner. Defaults to the user running the
  server (0/0 on Windows, where `os.Getuid` returns -1).
- `ExportOptions.ForceOwner`: report `DefaultOwner` for every file, even those
  with a real owner.
- `FileOwner` type. `GetExportOptions` reports the owner in effect.
- Owner override for WRITE: the owner of a file may write it even when its
  mode denies writing, so a client that creates a file read-only and then
  writes it (git's 0444 object files) works over a stateless protocol.
  Overrides are serialized so parallel WRITEs to one file all succeed.
- A macOS end-to-end test, `TestMacOSUserMount`, run with
  `ABSNFS_MACOS_MOUNT=1`: mounts osfs and memfs exports as the current user
  and checks listing, ownership, symlinks, file operations, locking with
  `locallocks`, and a git commit.
- README sections on mounting from macOS as a normal user and on file
  ownership.

### Fixed

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
- CREATE in GUARDED or EXCLUSIVE mode truncated an existing file and
  reported success, so `open(O_CREAT|O_EXCL)` could not protect a lock file.
  Both modes now fail with `NFS3ERR_EXIST` and leave the file alone. EXCLUSIVE
  stores the client's verifier in the new file's times, so a retransmitted
  request still succeeds (RFC 1813 section 3.3.8).
- `ENOTEMPTY` maps to `NFS3ERR_NOTEMPTY` instead of `NFS3ERR_EXIST`.
- `Export` now uses RPC record marking and serves MOUNT on the same port, so
  kernel NFS clients can mount it; before, mount_nfs got "connection reset".

### Changed

- The macOS mount instructions no longer use `sudo` or `resvport`.

## [1.1.0]

Earlier history is in the git log.
