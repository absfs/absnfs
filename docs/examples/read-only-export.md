# Example: Read-Only Export with IP Filtering

A locked-down NFS server that only allows reads from a specific subnet.

```go
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/absfs/absnfs"
	"github.com/absfs/memfs"
)

func main() {
	fs, err := memfs.NewFS()
	if err != nil {
		log.Fatal(err)
	}

	// Populate the filesystem with read-only content
	fs.MkdirAll("/docs", 0755)
	f, _ := fs.Create("/docs/README.txt")
	f.Write([]byte("This export is read-only.\n"))
	f.Close()

	// Create handler with security options
	nfs, err := absnfs.New(fs, absnfs.ExportOptions{
		ReadOnly:   true,
		Secure:     true,
		Squash:     "root",
		AllowedIPs: []string{
			"127.0.0.1",
			"192.168.1.0/24",
		},
		AttrCacheTimeout: 30 * time.Second,
		EnableDirCache:   true,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer nfs.Close()

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
	fmt.Println("Read-only NFS server running on port 2049")
	fmt.Println("Allowed: 127.0.0.1, 192.168.1.0/24")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	server.Stop()
}
```

## What This Demonstrates

- `ReadOnly: true` rejects all write operations at the NFS protocol level
- `Secure: true` requires clients to use a privileged source port
- `Squash: "root"` maps UID 0 to nobody, preventing root-level access
- `AllowedIPs` restricts connections to localhost and the 192.168.1.0/24 subnet
- Longer `AttrCacheTimeout` and `EnableDirCache` improve performance for
  read-only workloads where staleness is acceptable
