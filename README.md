# absnfs

[![Go Reference](https://pkg.go.dev/badge/github.com/absfs/absnfs.svg)](https://pkg.go.dev/github.com/absfs/absnfs)
[![Go Report Card](https://goreportcard.com/badge/github.com/absfs/absnfs)](https://goreportcard.com/report/github.com/absfs/absnfs)
[![CI](https://github.com/absfs/absnfs/actions/workflows/ci.yml/badge.svg)](https://github.com/absfs/absnfs/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

An NFSv3 server for [absfs](https://github.com/absfs/absfs) filesystems, in pure Go.

Anything that implements `absfs.SymlinkFileSystem` can be served over NFS: an
in-memory tree (memfs), a folder on disk (osfs, basefs), a union of a writable
layer over a read-only one (unionfs), or a router that composes many of them
(switchfs). The operating system's own NFS client then mounts it like any
other volume, so every program on the machine (editors, compilers, git) can
use a filesystem that exists only inside your Go process.

No kernel extension, no FUSE, and on macOS no root: the server runs as an
ordinary user on a high port, and the user mounts it with `mount_nfs`.

## Installation

```bash
go get github.com/absfs/absnfs
```

Requires Go 1.23 or later.

## Quick start

```go
package main

import (
	"log"
	"os"
	"os/signal"

	"github.com/absfs/absnfs"
	"github.com/absfs/memfs"
)

func main() {
	fs, err := memfs.NewFS()
	if err != nil {
		log.Fatal(err)
	}
	f, _ := fs.Create("/hello.txt")
	f.Write([]byte("Hello from NFS!\n"))
	f.Close()

	nfs, err := absnfs.New(fs, absnfs.ExportOptions{})
	if err != nil {
		log.Fatal(err)
	}
	defer nfs.Close()

	// NFS and MOUNT on one unprivileged port, with RPC record marking.
	if err := nfs.Export("/", 12049); err != nil {
		log.Fatal(err)
	}
	log.Print("serving on localhost:12049")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
}
```

Then mount it, as yourself, into a folder you own:

```bash
# macOS
mkdir -p ~/nfs
mount_nfs -o vers=3,tcp,port=12049,mountport=12049,noresvport,locallocks,soft,intr localhost:/ ~/nfs
ls -l ~/nfs
umount ~/nfs   # before pressing Ctrl-C in the server

# Linux (mounting needs root there)
sudo mount -t nfs -o vers=3,tcp,port=12049,mountport=12049,nolock localhost:/ /mnt/test
```

`Export("/", 0)` picks a free port, and `nfs.ExportPort()` tells you which.
TLS configured in `ExportOptions.TLS` applies to `Export` as well. For a
different bind address or a portmapper, create the server yourself with
`NewServer(ServerOptions{...})`, `SetHandler`, and `Listen`; see the
[quick start guide](docs/guides/quick-start.md).

## Mounting on macOS as a normal user

The macOS NFS client is stricter than Linux's, and its defaults assume a
system NFS server. This recipe needs no `sudo`, no portmapper and no System
Settings change:

```bash
mount_nfs -o vers=3,tcp,port=12049,mountport=12049,noresvport,locallocks,noatime,soft,intr,timeo=10,retrans=2 \
    localhost:/ ~/nfs
```

| Option                         | Why                                                                                                                                                                                                                                                                                                                                                                                              |
| ------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `vers=3,tcp`                   | absnfs speaks NFSv3 over TCP.                                                                                                                                                                                                                                                                                                                                                                    |
| `port=P`                       | Skip the portmapper lookup for NFS. The portmapper lives on port 111, which only root can bind.                                                                                                                                                                                                                                                                                                  |
| `mountport=P`                  | Skip the portmapper lookup for the MOUNT protocol too. absnfs serves MOUNT on the same port as NFS.                                                                                                                                                                                                                                                                                              |
| `noresvport`                   | Connect from an ordinary source port. A reserved port (below 1024) needs root; without this option an unprivileged `mount_nfs` fails with "Operation not permitted". Leave `ExportOptions.Secure` false.                                                                                                                                                                                         |
| `locallocks`                   | absnfs has no lock manager (NLM). `locallocks` keeps locks on the client, which tools that lock files need: with `nolocks`, `go build` fails with "RLock go.mod: operation not supported". `nolocks` is fine for trees no tool locks.                                                                                                                                                            |
| `noatime`                      | Without it, the macOS client sends a SETATTR to set the access time after a program reads a file through `mmap` (git reads every object it uses that way). The server then changes the file's times: harmless on a plain directory, but a union filesystem (unionfs) copies each such file up to its writable layer, so `git log` on a lower layer's repository copies its objects and packs up. |
| `soft,intr,timeo=10,retrans=2` | Recommended while developing: if the server process exits under a live mount, a hard mount hangs every process that touches it; a soft mount returns errors instead.                                                                                                                                                                                                                             |

Unmount with `umount ~/nfs`, then `umount -f`, then `diskutil unmount force` if
it is busy. Always unmount before stopping the server.

## File ownership

NFS clients decide permissions from the uid and gid the server reports, and
some refuse to work in a tree owned by someone else: git stops with "detected
dubious ownership". Files owned by root also give the user only the "other"
permission bits, so they cannot be written. absnfs reports:

1. **The real owner** when the filesystem's `os.FileInfo.Sys()` is a
   `*syscall.Stat_t`: osfs on Linux, macOS and the BSDs, and wrappers such as
   basefs that pass `Sys()` through.
2. **Otherwise `ExportOptions.DefaultOwner`**, which defaults to the user
   running the server. memfs, other virtual filesystems, and every filesystem
   on Windows (where `os.Getuid` returns -1; 0 is used) fall in this group.

So the user who runs the server and mounts the export owns what they see.

```go
absnfs.ExportOptions{
	// Report this owner for files whose FileInfo records none.
	DefaultOwner: &absnfs.FileOwner{UID: 501, GID: 20},
	// Report DefaultOwner for every file, even ones with a real owner:
	// present a tree owned by another account as the mounting user's own.
	ForceOwner: true,
}
```

Both are fixed when the export is created; `UpdateExportOptions` returns an
error if asked to change them. `SETATTR` (chown) is passed to the filesystem;
on filesystems without an OS owner the reported owner stays `DefaultOwner`.
`CREATE`, `MKDIR` and `SYMLINK` try to give new objects the caller's uid and
gid; a server not running as root can only give them its own. An UNCHECKED
`CREATE` of an existing file keeps that file's owner and mode.

## Security

**Every local user can use a localhost export, as any uid.** `Export` and
`NewServer` bind `localhost` by default, which keeps other machines out but
not other accounts on the same machine: any local process can connect to the
port and speak NFS. NFSv3 clients identify themselves with AUTH_SYS, a uid
and gids the client simply asserts, and absnfs (like any NFSv3 server without
Kerberos) believes them. So a local user can claim the uid of the user
running the server, or root, and read and write everything the server
process can reach through the export. On a machine shared with other users,
treat a running export as giving all of them the server's access to it.

What limits it:

- **Bind to `127.0.0.1`** (`ServerOptions.Hostname: "127.0.0.1"`) rather than
  a name or a public address, and use `AllowedIPs` when clients come from
  elsewhere. Both limit which machines connect, not which users.
- **`ExportOptions.AllowedUIDs`** (opt in) serves only AUTH_SYS requests
  carrying one of the listed uids, for example only the uid of the user who
  serves the export: `AllowedUIDs: []uint32{uint32(os.Getuid())}`. Other
  uids and AUTH_NONE get `AUTH_ERROR`. This stops accidents and casual
  misuse (another account mounting the export as itself), **not a hostile
  local user**: the uid is still asserted by the client, and a program that
  writes its own RPC credentials can claim an allowed uid. NULL and the MOUNT
  protocol, which reach no file, are answered for any uid (macOS `mount_nfs`
  sends MNT as uid 0).
- **TLS with client certificates** (`ExportOptions.TLS`) is the only option
  that proves who the client is. `Squash: "all"` maps every client to nobody,
  and `ReadOnly` refuses every change.

**Exports of a host directory** (osfs, or basefs over osfs) are served by a
process whose host follows symbolic links. absnfs keeps clients inside the
export: SYMLINK refuses targets that could lead out of it (see Caveats),
SETATTR on a link sets only its owner (with `Lchown`) and refuses its mode,
size and times (`NFS3ERR_INVAL`; `chmod -h` and `touch -h` report "Invalid
argument") because setting them would change the link's target, and every
decision on an object's type that gates an operation (is this handle still
a directory? still a regular file?) is made from the object as it is now
(`Lstat`), never from the attributes cached when its handle was issued. A
directory replaced by a link behind the server's back answers
`NFS3ERR_NOTDIR`, a file replaced by a link `NFS3ERR_INVAL`; neither is
followed. Two gaps remain while other local processes can change the
exported directory: a handle for an object *below* a directory that is
replaced by a link is still resolved by its path, through the link (a client
that had a file in it open reaches the file of the same name where the link
leads); and a swap in the instant between the check and the operation is not
caught. Do not let untrusted local users write to a directory you export.

## Features

- **NFSv3** (RFC 1813): LOOKUP, GETATTR, SETATTR, ACCESS, READ, WRITE, CREATE,
  MKDIR, SYMLINK, READLINK, REMOVE, RMDIR, RENAME, READDIR, READDIRPLUS,
  FSSTAT, FSINFO, PATHCONF, COMMIT, plus the MOUNT protocol. LINK and MKNOD
  return `NFS3ERR_NOTSUPP`.
- **Stable file ids**: every procedure reports the same non-zero fileid for an
  object, derived from its path (file handles are path based too), so it
  survives a union filesystem copying the file up to another layer.
- **Symlinks reported as symlinks** everywhere, including directory listings.
  READLINK returns targets as stored, `..` components included; SYMLINK
  accepts any target with `ExportOptions.AllowAnySymlinkTarget` (see Caveats).
- **Real ownership** with a configurable fallback (above).
- **Owner override**: the owner of a file may write it even when its mode does
  not allow writing, as with a local open file descriptor. A client that
  creates a file read-only and then writes it (git's 0444 object files) works.
- **POSIX rename**: RENAME replaces an existing target, even on filesystems
  whose own Rename refuses to, and refuses (`NFS3ERR_INVAL`) to move a
  directory inside itself.
- **Stable directory cookies**: READDIR and READDIRPLUS cookies are derived
  from entry names, not positions, so a client that deletes entries while
  paging through a large directory (`rm -rf`) does not skip any.
- **Exclusive create**: GUARDED and EXCLUSIVE CREATE fail on an existing name
  without touching the file, so `O_EXCL` lock files work; a retransmitted
  EXCLUSIVE CREATE is recognised by its verifier.
- **Security**: IP allow lists with CIDR, read-only exports, root or all
  squashing, an opt-in uid allow list, TLS and mutual TLS, rate limiting
  (see [Security](#security) for what each protects against).
- **Performance**: attribute and directory caches, worker pool, TCP tuning.
  The caches are coherent with the server's own changes (see Caveats for
  changes made behind its back).
- **Live reconfiguration**: tuning applies immediately; policy changes drain
  in-flight requests first.
- **Metrics and structured logging.**

## Caveats

- **AUTH_SYS only.** Clients assert their own uid and gid; absnfs trusts them,
  like any NFSv3 server without Kerberos, so any local user can use a
  `localhost` export as any uid. See [Security](#security): bind
  `127.0.0.1`, and use `AllowedIPs`, `AllowedUIDs`, squashing and TLS to
  limit who can connect.
- **A symbolic link's mode and times cannot be set** (`NFS3ERR_INVAL`:
  `chmod -h` and `touch -h` fail with "Invalid argument"), because absfs can
  set them only through the link, which changes its target. `chown -h`
  works.
- **No NLM lock manager.** Mount with `locallocks` (or `nolocks`, if no
  tool locks files).
- **SYMLINK refuses absolute and `..` targets by default** (`NFS3ERR_ACCES`,
  "Permission denied" for `ln -s ../x y`), because a filesystem backed by a
  host directory follows links on the host, where such a link could lead out
  of the export. Set `ExportOptions.AllowAnySymlinkTarget` when the exported
  filesystem resolves links within its own namespace (memfs, c4fs, unionfs).
  READLINK returns every target as stored either way.
- **No hard links** (LINK returns `NFS3ERR_NOTSUPP`).
- **UNCHECKED CREATE truncates an existing file** (keeping its owner and
  mode), as `open(O_CREAT|O_TRUNC)` does. Clients send it only after a lookup
  found no file, so this matters only when a create races another.
- **Directory listings come in hash order**, not name order (`ls` sorts
  anyway). An entry added while a client pages through a directory may be
  missed by that listing, as on local filesystems with hashed directories.
- **Owner override briefly changes the mode.** To let the owner write a 0444
  file, absnfs adds owner write permission while it opens the file. A chmod
  arriving in that instant is undone.
- **File ids are path based.** Renaming an object changes its fileid, and two
  hard links (where the backing filesystem has them) have different ids. A
  client's handles follow a RENAME, but one that compares fileids may treat a
  moved object's handle as stale and look the name up again. The handle of a
  removed or replaced object reports `NFS3ERR_STALE`.
- **Handles name paths.** A handle locates its object by path and is checked
  against a log of the server's own REMOVEs, RMDIRs and RENAMEs, so it keeps
  working however many handles a client holds (`MaxHandles` only sizes the
  cache), follows a RENAME made through the server, and reports
  `NFS3ERR_STALE` once its object is removed or replaced through the server.
  A change made behind the server's back (a write to the backend, a switchfs
  route or unionfs layer change) is seen by path: the handle reaches whatever
  is at its path now. See [File Handle System](docs/internals/file-handles.md).
- **Handles survive a restart only with `HandleStateDir`.** Without it, a
  restarted server's handles differ, every handle from before reports
  `NFS3ERR_STALE`, and clients remount. With it, the server keeps its handle
  key and a journal of its REMOVEs, RMDIRs and RENAMEs there, and clients
  carry on. An operating system crash can lose the last second of the
  journal. Handles from servers before v1.2.0 (8 bytes) are always stale:
  remount once after upgrading.
- **Changes made behind the server's back are seen after a delay.** absnfs
  caches attributes for `AttrCacheTimeout` (5s by default), and, when
  enabled, misses for `NegativeCacheTimeout` and listings for
  `DirCacheTimeout`. Every change made through the server is seen at once,
  but one made to the exported filesystem directly (another process, a
  second server over the same files) can take that long to show. So can one
  made through the server under another spelling of the name, on a
  case-insensitive host filesystem (`/F` after changing `/f`).
- **The rename fallback is not atomic.** When the backing filesystem refuses
  to replace an existing target, absnfs removes the target and renames, so for
  a moment neither name exists. osfs renames atomically and never needs it.

## Documentation

- [Quick Start](docs/guides/quick-start.md)
- [Configuration](docs/guides/configuration.md)
- [Security](docs/guides/security.md)
- [TLS Encryption](docs/guides/tls.md)
- [Runtime Reconfiguration](docs/guides/runtime-config.md)
- [API Reference](docs/api/index.md), including [ExportOptions](docs/api/export-options.md)
- [Examples](docs/examples/basic-server.md)
- [Changelog](CHANGELOG.md)

## Testing

```bash
go test -race ./...
```

The macOS end-to-end test mounts real exports with the kernel NFS client, as
the current user, and checks listing, ownership, symlinks (SETATTR on a link
never reaching its target), a directory swapped for a link behind the
server's back, file operations, removing a large directory while listing it,
a git commit, and an export limited to the user's uid (`AllowedUIDs`). It is
skipped unless asked for:

```bash
ABSNFS_MACOS_MOUNT=1 go test -run TestMacOSUserMount -v
```

The file handle tests mount git workspaces with a handle cache far smaller
than the handles the client holds; `TestMacOSWorkspacesWarmStatus` serves four
unionfs workspaces under a switchfs and checks that a warm `git status` in
the oldest, whose handles were evicted, takes about as long as in the newest:

```bash
ABSNFS_MACOS_MOUNT=1 go test -run 'TestMacOSHandles|TestMacOSWorkspaces' -v
```

These tests refuse to run as root, mount only into a fresh temporary
directory with `soft` options, and unmount before stopping their server on
every path. The
older `integration` build tag tests use `sudo` and `/Volumes`.

## License

Apache License, Version 2.0 -- see [LICENSE](LICENSE).
