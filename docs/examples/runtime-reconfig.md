# Example: Runtime Reconfiguration

Changing server options while the server is running, without restarting.

```go
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/absfs/absnfs/v2"
	"github.com/absfs/memfs"
)

func main() {
	fs, err := memfs.NewFS()
	if err != nil {
		log.Fatal(err)
	}

	nfs, err := absnfs.New(fs, absnfs.ExportOptions{
		AllowedIPs: []string{"127.0.0.1"},
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
	fmt.Println("NFS server running on port 2049")

	// Simulate a configuration change after 10 seconds
	go func() {
		time.Sleep(10 * time.Second)

		// Option 1: Update everything at once via UpdateExportOptions.
		// Tuning fields apply immediately; policy fields use drain-and-swap.
		err := nfs.UpdateExportOptions(absnfs.ExportOptions{
			ReadOnly:         true,
			AllowedIPs:       []string{"127.0.0.1", "10.0.0.0/8"},
			AttrCacheTimeout: 30 * time.Second,
			MaxWorkers:       32,
		})
		if err != nil {
			log.Printf("UpdateExportOptions failed: %v", err)
			return
		}
		fmt.Println("Switched to read-only, expanded AllowedIPs")

		// Option 2: Update only tuning (no drain, no client disruption).
		nfs.UpdateTuningOptions(func(t *absnfs.TuningOptions) {
			t.AttrCacheSize = 50000
			t.EnableDirCache = true
			t.DirCacheTimeout = 20 * time.Second
		})
		fmt.Println("Enlarged caches")

		// Option 3: Update only policy (drain-and-swap).
		err = nfs.UpdatePolicyOptions(absnfs.PolicyOptions{
			ReadOnly:           true,
			Secure:             true,
			AllowedIPs:         []string{"127.0.0.1", "10.0.0.0/8"},
			EnableRateLimiting: true,
		})
		if err != nil {
			log.Printf("UpdatePolicyOptions failed: %v", err)
			return
		}
		fmt.Println("Enabled Secure port requirement")

		// Read back current configuration
		opts := nfs.GetExportOptions()
		fmt.Printf("Current config: ReadOnly=%v, Secure=%v, CacheSize=%d\n",
			opts.ReadOnly, opts.Secure, opts.AttrCacheSize)
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	server.Stop()
}
```

## What This Demonstrates

- **`UpdateExportOptions`**: The simplest approach. Pass a full `ExportOptions`
  and the server routes fields to tuning or policy updates automatically.
- **`UpdateTuningOptions`**: Targeted performance changes with zero client
  disruption. The mutation function receives a copy; the result is stored
  atomically.
- **`UpdatePolicyOptions`**: Targeted security changes with drain-and-swap.
  In-flight requests finish, then the new policy takes effect.
- **`GetExportOptions`**: Read back a snapshot of current configuration.
