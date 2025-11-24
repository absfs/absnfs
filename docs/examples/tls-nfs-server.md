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
    fs, err := osfs.NewFS("/tmp")
    if err != nil {
        log.Fatalf("Failed to create filesystem: %v", err)
    }

    // Configure TLS
    tlsConfig := &absnfs.TLSConfig{
        Enabled:    true,
        CertFile:   "server.crt",
        KeyFile:    "server.key",
        MinVersion: tls.VersionTLS12,  // Minimum TLS 1.2
        MaxVersion: tls.VersionTLS13,  // Allow TLS 1.3
    }

    // Create NFS server with TLS
    options := absnfs.ExportOptions{
        ReadOnly:   false,
        Secure:     false, // Don't require privileged ports
        AllowedIPs: []string{"192.168.1.0/24"}, // Allow local network
        TLS:        tlsConfig,
    }

    nfs, err := absnfs.New(fs, options)
    if err != nil {
        log.Fatalf("Failed to create NFS handler: %v", err)
    }

    // Start server
    log.Println("Starting NFS server with TLS encryption on port 2049")
    log.Printf("TLS Configuration: MinVersion=TLS1.2, MaxVersion=TLS1.3")

    if err := nfs.Export("/export", 2049); err != nil {
        log.Fatalf("Failed to export filesystem: %v", err)
    }

    // Wait for interrupt signal
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
    <-sigChan

    log.Println("Shutting down NFS server...")
    if err := nfs.Unexport(); err != nil {
        log.Printf("Error during shutdown: %v", err)
    }
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
    fs, err := memfs.NewFS()
    if err != nil {
        log.Fatal(err)
    }

    // Pre-populate with some test data
    file, err := fs.Create("/secure-data.txt")
    if err != nil {
        log.Fatal(err)
    }
    _, err = file.Write([]byte("Confidential information"))
    file.Close()
    if err != nil {
        log.Fatal(err)
    }

    // Configure TLS with client certificate verification
    tlsConfig := &absnfs.TLSConfig{
        Enabled:    true,
        CertFile:   "server.crt",
        KeyFile:    "server.key",
        CAFile:     "ca.crt",  // CA for verifying client certificates
        ClientAuth: tls.RequireAndVerifyClientCert,  // Mutual TLS
        MinVersion: tls.VersionTLS12,
    }

    // Create NFS server with strict security
    options := absnfs.ExportOptions{
        ReadOnly:   false,
        Secure:     true,  // Require privileged ports
        AllowedIPs: []string{"10.0.0.0/8", "192.168.0.0/16"},
        TLS:        tlsConfig,
    }

    nfs, err := absnfs.New(fs, options)
    if err != nil {
        log.Fatalf("Failed to create NFS handler: %v", err)
    }

    // Start server
    log.Println("Starting secure NFS server with mutual TLS")
    log.Println("Client certificates will be verified against CA")

    if err := nfs.Export("/export", 2049); err != nil {
        log.Fatalf("Failed to export filesystem: %v", err)
    }

    // Wait for interrupt signal
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
    <-sigChan

    log.Println("Shutting down NFS server...")
    if err := nfs.Unexport(); err != nil {
        log.Printf("Error during shutdown: %v", err)
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
    fs, err := osfs.NewFS("/tmp")
    if err != nil {
        log.Fatalf("Failed to create filesystem: %v", err)
    }

    // Configure TLS
    tlsConfig := &absnfs.TLSConfig{
        Enabled:    true,
        CertFile:   "server.crt",
        KeyFile:    "server.key",
        MinVersion: tls.VersionTLS12,
    }

    // Create NFS server
    options := absnfs.ExportOptions{
        TLS: tlsConfig,
    }

    nfs, err := absnfs.New(fs, options)
    if err != nil {
        log.Fatalf("Failed to create NFS handler: %v", err)
    }

    // Start server
    log.Println("Starting NFS server with TLS")
    if err := nfs.Export("/export", 2049); err != nil {
        log.Fatalf("Failed to export filesystem: %v", err)
    }

    // Wait for interrupt signal
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
    <-sigChan

    log.Println("Shutting down NFS server...")
    if err := nfs.Unexport(); err != nil {
        log.Printf("Error during shutdown: %v", err)
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
    fs, err := memfs.NewFS()
    if err != nil {
        log.Fatalf("Failed to create filesystem: %v", err)
    }

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

    port := getEnvInt("NFS_PORT", 2049)
    log.Println("Starting NFS server with environment-based TLS configuration")
    if err := nfs.Export("/export", port); err != nil {
        log.Fatalf("Failed to export filesystem: %v", err)
    }

    // Wait for interrupt signal
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
    <-sigChan

    log.Println("Shutting down NFS server...")
    if err := nfs.Unexport(); err != nil {
        log.Printf("Error during shutdown: %v", err)
    }
}

func loadTLSConfigFromEnv() (*absnfs.TLSConfig, error) {
    // Check if TLS is enabled
    if os.Getenv("TLS_ENABLED") != "true" {
        return &absnfs.TLSConfig{Enabled: false}, nil
    }

    config := &absnfs.TLSConfig{
        Enabled:  true,
        CertFile: getEnv("TLS_CERT_FILE", "server.crt"),
        KeyFile:  getEnv("TLS_KEY_FILE", "server.key"),
        CAFile:   os.Getenv("TLS_CA_FILE"), // Optional
    }

    // Parse TLS version
    if minVer := os.Getenv("TLS_MIN_VERSION"); minVer != "" {
        switch minVer {
        case "1.2":
            config.MinVersion = tls.VersionTLS12
        case "1.3":
            config.MinVersion = tls.VersionTLS13
        default:
            return nil, fmt.Errorf("invalid TLS_MIN_VERSION: %s (use 1.2 or 1.3)", minVer)
        }
    } else {
        config.MinVersion = tls.VersionTLS12 // Default
    }

    // Parse client auth mode
    if authMode := os.Getenv("TLS_CLIENT_AUTH"); authMode != "" {
        switch authMode {
        case "none":
            config.ClientAuth = tls.NoClientCert
        case "request":
            config.ClientAuth = tls.RequestClientCert
        case "require-any":
            config.ClientAuth = tls.RequireAnyClientCert
        case "verify-if-given":
            config.ClientAuth = tls.VerifyClientCertIfGiven
        case "require-and-verify":
            config.ClientAuth = tls.RequireAndVerifyClientCert
        default:
            return nil, fmt.Errorf("invalid TLS_CLIENT_AUTH: %s", authMode)
        }
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
