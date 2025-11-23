# TLS-Enabled NFS Server Example

This example demonstrates how to create an NFS server with TLS encryption for secure file transfers over untrusted networks.

## Basic TLS Server

```go
package main

import (
    "crypto/tls"
    "log"
    "os"
    "os/signal"
    "syscall"

    "github.com/absfs/absnfs"
    "github.com/absfs/osfs"
)

func main() {
    // Create filesystem (export /tmp directory)
    fs := osfs.NewFileSystem()

    // Configure TLS
    tlsConfig := absnfs.DefaultTLSConfig()
    tlsConfig.Enabled = true
    tlsConfig.CertFile = "server.crt"
    tlsConfig.KeyFile = "server.key"
    tlsConfig.MinVersion = tls.VersionTLS12  // Minimum TLS 1.2
    tlsConfig.MaxVersion = tls.VersionTLS13  // Allow TLS 1.3

    // Create NFS server with TLS
    options := absnfs.ExportOptions{
        ReadOnly:    false,
        Secure:      false, // Don't require privileged ports
        AllowedIPs:  []string{"192.168.1.0/24"}, // Allow local network
        TLS:         tlsConfig,
    }

    nfs, err := absnfs.New(fs, options)
    if err != nil {
        log.Fatalf("Failed to create NFS handler: %v", err)
    }
    defer nfs.Close()

    // Create server
    server, err := absnfs.NewServer(absnfs.ServerOptions{
        Port:     2049,
        Hostname: "0.0.0.0",
        Debug:    true,
    })
    if err != nil {
        log.Fatalf("Failed to create server: %v", err)
    }

    server.SetHandler(nfs)

    // Start server
    log.Println("Starting NFS server with TLS encryption on port 2049")
    log.Printf("TLS Configuration: MinVersion=%s, MaxVersion=%s",
        absnfs.TLSVersionString(tlsConfig.MinVersion),
        absnfs.TLSVersionString(tlsConfig.MaxVersion))

    if err := server.Listen(); err != nil {
        log.Fatalf("Failed to start server: %v", err)
    }

    // Wait for interrupt signal
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
    <-sigChan

    log.Println("Shutting down NFS server...")
    server.Shutdown()
}
```

## Mutual TLS (mTLS) Server

This example shows a server that requires and validates client certificates.

```go
package main

import (
    "crypto/tls"
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
    // Create in-memory filesystem for secure data
    fs := memfs.NewMemFS()

    // Pre-populate with some test data
    if err := fs.WriteFile("/secure-data.txt", []byte("Confidential information"), 0644); err != nil {
        log.Fatal(err)
    }

    // Configure TLS with client certificate verification
    tlsConfig := absnfs.DefaultTLSConfig()
    tlsConfig.Enabled = true
    tlsConfig.CertFile = "server.crt"
    tlsConfig.KeyFile = "server.key"
    tlsConfig.CAFile = "ca.crt"  // CA for verifying client certificates
    tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert  // Mutual TLS
    tlsConfig.MinVersion = tls.VersionTLS12

    // Create NFS server with strict security
    options := absnfs.ExportOptions{
        ReadOnly:           false,
        Secure:             true,  // Require privileged ports
        AllowedIPs:         []string{"10.0.0.0/8", "192.168.0.0/16"},
        TLS:                tlsConfig,
        EnableRateLimiting: true,
    }

    nfs, err := absnfs.New(fs, options)
    if err != nil {
        log.Fatalf("Failed to create NFS handler: %v", err)
    }
    defer nfs.Close()

    // Create server
    server, err := absnfs.NewServer(absnfs.ServerOptions{
        Port:     2049,
        Hostname: "0.0.0.0",
        Debug:    true,
    })
    if err != nil {
        log.Fatalf("Failed to create server: %v", err)
    }

    server.SetHandler(nfs)

    // Start metrics reporting
    go reportMetrics(nfs)

    // Start server
    log.Println("Starting secure NFS server with mutual TLS")
    log.Println("Client certificates will be verified against CA")

    if err := server.Listen(); err != nil {
        log.Fatalf("Failed to start server: %v", err)
    }

    // Wait for interrupt signal
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
    <-sigChan

    log.Println("Shutting down NFS server...")
    server.Shutdown()
}

func reportMetrics(nfs *absnfs.AbsfsNFS) {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()

    for range ticker.C {
        metrics := nfs.GetMetrics()

        log.Println("=== TLS Metrics ===")
        log.Printf("Total Connections: %d", metrics.TotalConnections)
        log.Printf("Active Connections: %d", metrics.ActiveConnections)
        log.Printf("TLS Handshakes: %d (Failures: %d)",
            metrics.TLSHandshakes, metrics.TLSHandshakeFailures)
        log.Printf("Client Certs: Provided=%d, Validated=%d, Rejected=%d",
            metrics.TLSClientCertProvided,
            metrics.TLSClientCertValidated,
            metrics.TLSClientCertRejected)
        log.Printf("TLS Versions: TLS1.2=%d, TLS1.3=%d",
            metrics.TLSVersion12, metrics.TLSVersion13)
        log.Println("==================")
    }
}
```

## Certificate Rotation Example

This example shows how to implement automatic certificate rotation.

```go
package main

import (
    "crypto/tls"
    "log"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/absfs/absnfs"
    "github.com/absfs/osfs"
)

func main() {
    // Create filesystem
    fs := osfs.NewFileSystem()

    // Configure TLS
    tlsConfig := absnfs.DefaultTLSConfig()
    tlsConfig.Enabled = true
    tlsConfig.CertFile = "server.crt"
    tlsConfig.KeyFile = "server.key"
    tlsConfig.MinVersion = tls.VersionTLS12

    // Create NFS server
    options := absnfs.ExportOptions{
        TLS: tlsConfig,
    }

    nfs, err := absnfs.New(fs, options)
    if err != nil {
        log.Fatalf("Failed to create NFS handler: %v", err)
    }
    defer nfs.Close()

    // Create server
    server, err := absnfs.NewServer(absnfs.ServerOptions{
        Port:  2049,
        Debug: true,
    })
    if err != nil {
        log.Fatalf("Failed to create server: %v", err)
    }

    server.SetHandler(nfs)

    // Start certificate rotation monitor
    go monitorCertificates(tlsConfig)

    // Start server
    log.Println("Starting NFS server with automatic certificate rotation")
    if err := server.Listen(); err != nil {
        log.Fatalf("Failed to start server: %v", err)
    }

    // Wait for interrupt signal
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
    <-sigChan

    log.Println("Shutting down NFS server...")
    server.Shutdown()
}

func monitorCertificates(tlsConfig *absnfs.TLSConfig) {
    ticker := time.NewTicker(1 * time.Hour)
    defer ticker.Stop()

    var lastModTime time.Time

    for range ticker.C {
        // Check if certificate file has been modified
        fileInfo, err := os.Stat(tlsConfig.CertFile)
        if err != nil {
            log.Printf("Failed to check certificate file: %v", err)
            continue
        }

        if fileInfo.ModTime().After(lastModTime) {
            log.Printf("Certificate file modified, reloading...")

            // Reload certificates
            if err := tlsConfig.ReloadCertificates(); err != nil {
                log.Printf("Failed to reload certificates: %v", err)
            } else {
                log.Printf("Certificates reloaded successfully")
                lastModTime = fileInfo.ModTime()
            }
        }
    }
}
```

## Environment-Based Configuration Example

This example shows how to configure TLS using environment variables.

```go
package main

import (
    "crypto/tls"
    "fmt"
    "log"
    "os"

    "github.com/absfs/absnfs"
    "github.com/absfs/memfs"
)

func main() {
    // Create filesystem
    fs := memfs.NewMemFS()

    // Load TLS configuration from environment
    tlsConfig, err := loadTLSConfigFromEnv()
    if err != nil {
        log.Fatalf("Failed to load TLS config: %v", err)
    }

    // Create NFS server
    options := absnfs.ExportOptions{
        TLS: tlsConfig,
    }

    nfs, err := absnfs.New(fs, options)
    if err != nil {
        log.Fatalf("Failed to create NFS handler: %v", err)
    }
    defer nfs.Close()

    // Create and start server
    server, err := absnfs.NewServer(absnfs.ServerOptions{
        Port: getEnvInt("NFS_PORT", 2049),
    })
    if err != nil {
        log.Fatalf("Failed to create server: %v", err)
    }

    server.SetHandler(nfs)

    log.Println("Starting NFS server with environment-based TLS configuration")
    if err := server.Listen(); err != nil {
        log.Fatalf("Failed to start server: %v", err)
    }

    select {}
}

func loadTLSConfigFromEnv() (*absnfs.TLSConfig, error) {
    config := absnfs.DefaultTLSConfig()

    // Check if TLS is enabled
    if os.Getenv("TLS_ENABLED") != "true" {
        config.Enabled = false
        return config, nil
    }

    config.Enabled = true
    config.CertFile = getEnv("TLS_CERT_FILE", "server.crt")
    config.KeyFile = getEnv("TLS_KEY_FILE", "server.key")
    config.CAFile = os.Getenv("TLS_CA_FILE") // Optional

    // Parse TLS version
    if minVer := os.Getenv("TLS_MIN_VERSION"); minVer != "" {
        version, err := absnfs.ParseTLSVersion(minVer)
        if err != nil {
            return nil, fmt.Errorf("invalid TLS_MIN_VERSION: %w", err)
        }
        config.MinVersion = version
    }

    // Parse client auth mode
    if authMode := os.Getenv("TLS_CLIENT_AUTH"); authMode != "" {
        auth, err := absnfs.ParseClientAuthType(authMode)
        if err != nil {
            return nil, fmt.Errorf("invalid TLS_CLIENT_AUTH: %w", err)
        }
        config.ClientAuth = auth
    }

    return config, nil
}

func getEnv(key, defaultValue string) string {
    if value := os.Getenv(key); value != "" {
        return value
    }
    return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
    if value := os.Getenv(key); value != "" {
        var result int
        if _, err := fmt.Sscanf(value, "%d", &result); err == nil {
            return result
        }
    }
    return defaultValue
}
```

### Usage

```bash
# Run with TLS disabled
./nfs-server

# Run with basic TLS
export TLS_ENABLED=true
export TLS_CERT_FILE=/etc/nfs/server.crt
export TLS_KEY_FILE=/etc/nfs/server.key
./nfs-server

# Run with mutual TLS
export TLS_ENABLED=true
export TLS_CERT_FILE=/etc/nfs/server.crt
export TLS_KEY_FILE=/etc/nfs/server.key
export TLS_CA_FILE=/etc/nfs/ca.crt
export TLS_CLIENT_AUTH=require-and-verify
export TLS_MIN_VERSION=1.3
./nfs-server
```

## Generating Test Certificates

Use this script to generate test certificates for development:

```bash
#!/bin/bash
# generate-test-certs.sh

set -e

echo "Generating test certificates..."

# Generate CA
openssl genrsa -out ca.key 4096
openssl req -new -x509 -key ca.key -out ca.crt -days 3650 \
  -subj "/CN=Test NFS CA/O=Test Organization"

# Generate server certificate
openssl genrsa -out server.key 2048
openssl req -new -key server.key -out server.csr \
  -subj "/CN=localhost/O=Test Organization"
openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key \
  -CAcreateserial -out server.crt -days 365

# Generate client certificate
openssl genrsa -out client.key 2048
openssl req -new -key client.key -out client.csr \
  -subj "/CN=test-client/O=Test Organization"
openssl x509 -req -in client.csr -CA ca.crt -CAkey ca.key \
  -CAcreateserial -out client.crt -days 365

# Set permissions
chmod 600 *.key
chmod 644 *.crt

echo "Certificates generated successfully!"
echo "Files created:"
echo "  - ca.crt, ca.key (Certificate Authority)"
echo "  - server.crt, server.key (Server certificate)"
echo "  - client.crt, client.key (Client certificate)"
```

## See Also

- [TLS Encryption Guide](../guides/tls-encryption.md) - Detailed TLS configuration
- [Security Guide](../guides/security.md) - Security best practices
- [Configuration Guide](../guides/configuration.md) - Server configuration options
