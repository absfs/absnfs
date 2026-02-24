# Example: TLS-Encrypted NFS Server

An NFS server with TLS encryption and mutual client certificate verification.

```go
package main

import (
	"crypto/tls"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/absfs/absnfs/v2"
	"github.com/absfs/memfs"
)

func main() {
	fs, err := memfs.NewFS()
	if err != nil {
		log.Fatal(err)
	}

	// Create handler with TLS enabled
	nfs, err := absnfs.New(fs, absnfs.ExportOptions{
		TLS: &absnfs.TLSConfig{
			Enabled:    true,
			CertFile:   "/etc/nfs/server.crt",
			KeyFile:    "/etc/nfs/server.key",
			CAFile:     "/etc/nfs/ca.crt",
			ClientAuth: tls.RequireAndVerifyClientCert,
			MinVersion: tls.VersionTLS12,
			MaxVersion: tls.VersionTLS13,
		},
		Log: &absnfs.LogConfig{
			Level:        "info",
			Format:       "json",
			Output:       "stderr",
			LogClientIPs: true,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer nfs.Close()

	server, err := absnfs.NewServer(absnfs.ServerOptions{
		Port:             2049,
		MountPort:        2049,
		UseRecordMarking: true,
		Debug:            true,
	})
	if err != nil {
		log.Fatal(err)
	}
	server.SetHandler(nfs)

	if err := server.Listen(); err != nil {
		log.Fatal(err)
	}
	fmt.Println("TLS NFS server running on port 2049")
	fmt.Println("Requires client certificate signed by /etc/nfs/ca.crt")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	server.Stop()
}
```

## Using DefaultTLSConfig

For a quicker setup with secure defaults:

```go
tlsConfig := absnfs.DefaultTLSConfig()
tlsConfig.Enabled = true
tlsConfig.CertFile = "/etc/nfs/server.crt"
tlsConfig.KeyFile = "/etc/nfs/server.key"

nfs, err := absnfs.New(fs, absnfs.ExportOptions{
	TLS: tlsConfig,
})
```

## What This Demonstrates

- `TLSConfig.Enabled = true` switches the server to a TLS listener
- `ClientAuth: tls.RequireAndVerifyClientCert` enforces mutual TLS
- `CAFile` specifies the CA used to verify client certificates
- `LogConfig` enables structured JSON logging with client IP tracking
- `Debug: true` on ServerOptions enables verbose connection logging
