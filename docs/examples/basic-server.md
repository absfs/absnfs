# Example: Basic Server

A complete working NFS server exporting an in-memory filesystem.

```go
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/absfs/absnfs"
	"github.com/absfs/memfs"
)

func main() {
	// Create an in-memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		log.Fatal(err)
	}

	// Seed it with some content
	if err := fs.Mkdir("/data", 0755); err != nil {
		log.Fatal(err)
	}
	f, err := fs.Create("/data/hello.txt")
	if err != nil {
		log.Fatal(err)
	}
	f.Write([]byte("Hello from absnfs!\n"))
	f.Close()

	// Create the NFS handler with default options
	nfs, err := absnfs.New(fs, absnfs.ExportOptions{})
	if err != nil {
		log.Fatal(err)
	}
	defer nfs.Close()

	// Create and configure the server
	server, err := absnfs.NewServer(absnfs.ServerOptions{
		Port:             2049,
		MountPort:        2049,
		Hostname:         "localhost",
		UseRecordMarking: true,
	})
	if err != nil {
		log.Fatal(err)
	}
	server.SetHandler(nfs)

	// Start listening
	if err := server.Listen(); err != nil {
		log.Fatal(err)
	}
	fmt.Println("NFS server running on localhost:2049")
	fmt.Println("Mount with:")
	fmt.Println("  macOS:  sudo mount_nfs -o resvport,nolocks,vers=3,tcp,port=2049,mountport=2049 localhost:/ /Volumes/test")
	fmt.Println("  Linux:  sudo mount -t nfs -o vers=3,tcp,port=2049,mountport=2049,nolock localhost:/ /mnt/test")

	// Graceful shutdown on Ctrl+C
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	fmt.Println("\nShutting down...")
	server.Stop()
}
```

## What This Demonstrates

- Creating a `memfs` filesystem and populating it with files
- Using `absnfs.New()` with default `ExportOptions`
- Setting up a `Server` with `UseRecordMarking: true` for standard NFS clients
- Graceful shutdown on SIGINT/SIGTERM
