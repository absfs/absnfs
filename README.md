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
mount_nfs -o vers=3,tcp,port=12049,mountport=12049,noresvport,nolocks localhost:/ ~/nfs
ls -l ~/nfs
umount ~/nfs

# Linux (mounting needs root there)
sudo mount -t nfs -o vers=3,tcp,port=12049,mountport=12049,nolock localhost:/ /mnt/test
```

For a different bind address, a portmapper, or TLS, create the server yourself
with `NewServer(ServerOptions{...})`, `SetHandler`, and `Listen`; see the
[quick start guide](docs/guides/quick-start.md).

## Mounting on macOS as a normal user

The macOS NFS client is stricter than Linux's, and its defaults assume a
system NFS server. This recipe needs no `sudo`, no portmapper and no System
Settings change:

```bash
mount_nfs -o vers=3,tcp,port=12049,mountport=12049,noresvport,nolocks,soft,intr,timeo=10,retrans=2 \
    localhost:/ ~/nfs
```

| Option                         | Why                                                                                                                                                                                                      |
| ------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `vers=3,tcp`                   | absnfs speaks NFSv3 over TCP.                                                                                                                                                                            |
| `port=P`                       | Skip the portmapper lookup for NFS. The portmapper lives on port 111, which only root can bind.                                                                                                          |
| `mountport=P`                  | Skip the portmapper lookup for the MOUNT protocol too. absnfs serves MOUNT on the same port as NFS.                                                                                                      |
| `noresvport`                   | Connect from an ordinary source port. A reserved port (below 1024) needs root; without this option an unprivileged `mount_nfs` fails with "Operation not permitted". Leave `ExportOptions.Secure` false. |
| `nolocks` or `locallocks`      | absnfs has no lock manager (NLM). `nolocks` disables locking; `locallocks` keeps locks on the client, which tools that lock files need (`go build` locks `go.mod`).                                      |
| `soft,intr,timeo=10,retrans=2` | Recommended while developing: if the server process exits under a live mount, a hard mount hangs every process that touches it; a soft mount returns errors instead.                                     |

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

Both are fixed when the export is created. `SETATTR` (chown) is passed to the
filesystem; on filesystems without an OS owner the reported owner stays
`DefaultOwner`. `CREATE`, `MKDIR` and `SYMLINK` try to give new objects the
caller's uid and gid; a server not running as root can only give them its own.

## Features

- **NFSv3** (RFC 1813): LOOKUP, GETATTR, SETATTR, ACCESS, READ, WRITE, CREATE,
  MKDIR, SYMLINK, READLINK, REMOVE, RMDIR, RENAME, READDIR, READDIRPLUS,
  FSSTAT, FSINFO, PATHCONF, COMMIT, plus the MOUNT protocol. LINK and MKNOD
  return `NFS3ERR_NOTSUPP`.
- **Stable file ids**: every procedure reports the same non-zero fileid for an
  object, derived from its path (file handles are path based too), so it
  survives a union filesystem copying the file up to another layer.
- **Symlinks reported as symlinks** everywhere, including directory listings.
- **Real ownership** with a configurable fallback (above).
- **Owner override**: the owner of a file may write it even when its mode does
  not allow writing, as with a local open file descriptor. A client that
  creates a file read-only and then writes it (git's 0444 object files) works.
- **POSIX rename**: RENAME replaces an existing target, even on filesystems
  whose own Rename refuses to.
- **Exclusive create**: GUARDED and EXCLUSIVE CREATE fail on an existing name
  without touching the file, so `O_EXCL` lock files work; a retransmitted
  EXCLUSIVE CREATE is recognised by its verifier.
- **Security**: IP allow lists with CIDR, read-only exports, root or all
  squashing, TLS and mutual TLS, rate limiting.
- **Performance**: attribute and directory caches, worker pool, TCP tuning.
- **Live reconfiguration**: tuning applies immediately; policy changes drain
  in-flight requests first.
- **Metrics and structured logging.**

## Caveats

- **AUTH_SYS only.** Clients assert their own uid and gid; absnfs trusts them,
  like any NFSv3 server without Kerberos. Use `AllowedIPs`, squashing and TLS
  to limit who can connect.
- **No NLM lock manager.** Mount with `nolocks` or `locallocks`.
- **No hard links** (LINK returns `NFS3ERR_NOTSUPP`).
- **UNCHECKED CREATE truncates an existing file.** Clients send it only after a
  lookup found no file, so this matters only when a create races another.
- **Owner override briefly changes the mode.** To let the owner write a 0444
  file, absnfs adds owner write permission while it opens the file. A chmod
  arriving in that instant is undone.
- **File ids are path based.** Renaming an object changes its fileid, and two
  hard links (where the backing filesystem has them) have different ids.
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
the current user, and checks listing, ownership, symlinks, file operations and
a git commit. It is skipped unless asked for:

```bash
ABSNFS_MACOS_MOUNT=1 go test -run TestMacOSUserMount -v
```

It refuses to run as root, mounts only into a fresh temporary directory with
`soft` options, and unmounts before stopping its server on every path. The
older `integration` build tag tests use `sudo` and `/Volumes`.

## License

Apache License, Version 2.0 -- see [LICENSE](LICENSE).
