# Quick Start

Export any `absfs.SymlinkFileSystem` as an NFSv3 share in a few lines.

## Minimal Server

```go
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/absfs/absnfs/v2"
	"github.com/absfs/memfs"
)

func main() {
	// Create an in-memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		log.Fatal(err)
	}

	// Create the NFS handler (defaults are fine to start)
	nfs, err := absnfs.New(fs, absnfs.ExportOptions{})
	if err != nil {
		log.Fatal(err)
	}
	defer nfs.Close()

	// Create and start the server
	server, err := absnfs.NewServer(absnfs.ServerOptions{
		Port:             2049,
		MountPort:        2049,
		UseRecordMarking: true,
	})
	if err != nil {
		log.Fatal(err)
	}
	server.SetHandler(nfs)

	if err := server.Listen(); err != nil {
		log.Fatal(err)
	}
	fmt.Println("NFS server listening on port 2049")

	// Wait for Ctrl+C
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	server.Stop()
}
```

## Mounting

Once the server is running, mount from the same machine:

**macOS:**
```sh
sudo mkdir -p /Volumes/test
sudo mount_nfs -o resvport,nolocks,vers=3,tcp,port=2049,mountport=2049 \
  localhost:/ /Volumes/test
```

**Linux:**
```sh
sudo mkdir -p /mnt/test
sudo mount -t nfs -o vers=3,tcp,port=2049,mountport=2049,nolock \
  localhost:/ /mnt/test
```

## Key Types

| Type | Purpose |
|------|---------|
| `absnfs.ExportOptions` | All configuration for the NFS export |
| `absnfs.AbsfsNFS` | The NFS handler wrapping your filesystem |
| `absnfs.Server` | TCP server managing connections and RPC dispatch |
| `absnfs.ServerOptions` | Server-level settings (port, hostname, debug) |

## Using the Convenience Export Method

For simple cases, `AbsfsNFS` has an `Export` method that creates a server internally:

```go
nfs, err := absnfs.New(fs, absnfs.ExportOptions{})
if err != nil {
	log.Fatal(err)
}
defer nfs.Close()

// Export creates and starts a server on the given port
if err := nfs.Export("/", 2049); err != nil {
	log.Fatal(err)
}
```

Note that `Export` does not enable record marking or portmapper. For production
use or standard NFS clients, create a `Server` explicitly as shown in the
minimal example above.

## What Happens at Startup

1. `New()` validates options, applies defaults, initializes caches and the worker pool.
2. `NewServer()` creates a `Server` with connection management.
3. `SetHandler()` connects the `AbsfsNFS` handler to the server.
4. `Listen()` binds a TCP socket and starts the accept loop.

Shutdown reverses this: `Server.Stop()` stops the accept loop and drains
connections, then `AbsfsNFS.Close()` releases handles and stops the worker pool.
