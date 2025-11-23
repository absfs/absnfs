# TLS Encryption Guide

## Overview

ABSNFS supports TLS/SSL encryption to secure NFS traffic over untrusted networks. This guide covers how to configure and use TLS encryption, including:

- Server-side TLS configuration
- Client certificate authentication (mutual TLS)
- Certificate management and rotation
- Security best practices
- Troubleshooting

## Why Use TLS with NFS?

Traditional NFSv3 transmits all data in cleartext, making it vulnerable to:

- **Eavesdropping**: Sensitive file content can be intercepted
- **Man-in-the-middle attacks**: Attackers can modify data in transit
- **Credential theft**: Authentication credentials can be captured
- **Replay attacks**: Captured requests can be replayed

TLS encryption provides:

- **Confidentiality**: All data is encrypted end-to-end
- **Integrity**: Data tampering is detected and prevented
- **Authentication**: Server identity verification (and optionally client verification)
- **Forward secrecy**: Past communications remain secure even if keys are compromised

## Quick Start

### 1. Generate Certificates

First, generate a server certificate and private key:

```bash
# Generate server private key
openssl genrsa -out server.key 2048

# Generate self-signed certificate (valid for 365 days)
openssl req -new -x509 -key server.key -out server.crt -days 365 \
  -subj "/CN=nfs-server.example.com/O=Example Organization"
```

### 2. Configure TLS in Your Server

```go
package main

import (
    "crypto/tls"
    "log"
    "github.com/absfs/absnfs"
    "github.com/absfs/memfs"
)

func main() {
    // Create filesystem
    fs := memfs.NewMemFS()

    // Configure TLS
    tlsConfig := absnfs.DefaultTLSConfig()
    tlsConfig.Enabled = true
    tlsConfig.CertFile = "/path/to/server.crt"
    tlsConfig.KeyFile = "/path/to/server.key"
    tlsConfig.MinVersion = tls.VersionTLS12
    tlsConfig.MaxVersion = tls.VersionTLS13

    // Create NFS server with TLS
    options := absnfs.ExportOptions{
        TLS: tlsConfig,
    }

    nfs, err := absnfs.New(fs, options)
    if err != nil {
        log.Fatal(err)
    }

    // Create and start server
    server, err := absnfs.NewServer(absnfs.ServerOptions{
        Port: 2049,
    })
    if err != nil {
        log.Fatal(err)
    }

    server.SetHandler(nfs)

    log.Println("Starting NFS server with TLS encryption on port 2049")
    if err := server.Listen(); err != nil {
        log.Fatal(err)
    }

    select {} // Keep server running
}
```

## TLS Configuration Options

### Basic Configuration

| Field | Type | Description | Default |
|-------|------|-------------|---------|
| `Enabled` | `bool` | Enable/disable TLS | `false` |
| `CertFile` | `string` | Path to server certificate (PEM) | Required when enabled |
| `KeyFile` | `string` | Path to server private key (PEM) | Required when enabled |
| `MinVersion` | `uint16` | Minimum TLS version | `TLS 1.2` |
| `MaxVersion` | `uint16` | Maximum TLS version | `TLS 1.3` |

### Client Authentication (Mutual TLS)

| Field | Type | Description | Default |
|-------|------|-------------|---------|
| `ClientAuth` | `tls.ClientAuthType` | Client certificate policy | `NoClientCert` |
| `CAFile` | `string` | CA certificate for client verification | Optional |

### Security Options

| Field | Type | Description | Default |
|-------|------|-------------|---------|
| `CipherSuites` | `[]uint16` | Allowed cipher suites | Secure defaults |
| `PreferServerCipherSuites` | `bool` | Prefer server's cipher order | `true` |
| `InsecureSkipVerify` | `bool` | Skip certificate verification (testing only) | `false` |

## Client Authentication Modes

### NoClientCert (Default)

Server does not request client certificates. Only server authentication is performed.

```go
tlsConfig.ClientAuth = tls.NoClientCert
```

### RequestClientCert

Server requests client certificates but doesn't require them.

```go
tlsConfig.ClientAuth = tls.RequestClientCert
```

### RequireAnyClientCert

Server requires client to present a certificate, but doesn't verify it.

```go
tlsConfig.ClientAuth = tls.RequireAnyClientCert
```

### VerifyClientCertIfGiven

If client presents a certificate, server verifies it against the CA.

```go
tlsConfig.ClientAuth = tls.VerifyClientCertIfGiven
tlsConfig.CAFile = "/path/to/ca.crt"
```

### RequireAndVerifyClientCert (Mutual TLS)

Server requires and verifies client certificates. Recommended for production.

```go
tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
tlsConfig.CAFile = "/path/to/ca.crt"
```

## Mutual TLS (mTLS) Setup

Mutual TLS provides the strongest security by authenticating both server and client.

### 1. Create Certificate Authority (CA)

```bash
# Generate CA private key
openssl genrsa -out ca.key 4096

# Generate CA certificate
openssl req -new -x509 -key ca.key -out ca.crt -days 3650 \
  -subj "/CN=NFS CA/O=Example Organization"
```

### 2. Generate Server Certificate Signed by CA

```bash
# Generate server private key
openssl genrsa -out server.key 2048

# Generate certificate signing request
openssl req -new -key server.key -out server.csr \
  -subj "/CN=nfs-server.example.com/O=Example Organization"

# Sign server certificate with CA
openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key \
  -CAcreateserial -out server.crt -days 365
```

### 3. Generate Client Certificate Signed by CA

```bash
# Generate client private key
openssl genrsa -out client.key 2048

# Generate client certificate signing request
openssl req -new -key client.key -out client.csr \
  -subj "/CN=client-user/O=Example Organization"

# Sign client certificate with CA
openssl x509 -req -in client.csr -CA ca.crt -CAkey ca.key \
  -CAcreateserial -out client.crt -days 365
```

### 4. Configure Server for Mutual TLS

```go
tlsConfig := absnfs.DefaultTLSConfig()
tlsConfig.Enabled = true
tlsConfig.CertFile = "/path/to/server.crt"
tlsConfig.KeyFile = "/path/to/server.key"
tlsConfig.CAFile = "/path/to/ca.crt"
tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
```

## TLS Version Configuration

### Setting Specific TLS Versions

```go
import "crypto/tls"

// Require TLS 1.3 only
tlsConfig.MinVersion = tls.VersionTLS13
tlsConfig.MaxVersion = tls.VersionTLS13

// Allow TLS 1.2 and 1.3 (recommended)
tlsConfig.MinVersion = tls.VersionTLS12
tlsConfig.MaxVersion = tls.VersionTLS13
```

### Using String-Based Configuration

```go
minVersion, err := absnfs.ParseTLSVersion("1.2")
if err != nil {
    log.Fatal(err)
}
tlsConfig.MinVersion = minVersion
```

Supported version strings:
- `"1.0"`, `"TLS1.0"`, `"TLSv1.0"` → TLS 1.0 (not recommended)
- `"1.1"`, `"TLS1.1"`, `"TLSv1.1"` → TLS 1.1 (not recommended)
- `"1.2"`, `"TLS1.2"`, `"TLSv1.2"` → TLS 1.2 (recommended minimum)
- `"1.3"`, `"TLS1.3"`, `"TLSv1.3"` → TLS 1.3 (recommended)

## Cipher Suite Configuration

The default configuration uses secure cipher suites. For custom requirements:

```go
import "crypto/tls"

tlsConfig.CipherSuites = []uint16{
    // TLS 1.3 cipher suites (automatically used for TLS 1.3)
    tls.TLS_AES_128_GCM_SHA256,
    tls.TLS_AES_256_GCM_SHA384,
    tls.TLS_CHACHA20_POLY1305_SHA256,

    // TLS 1.2 cipher suites
    tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
    tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
    tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
    tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
}
```

## Certificate Rotation

Reload certificates without restarting the server:

```go
// In a goroutine or scheduled task
err := tlsConfig.ReloadCertificates()
if err != nil {
    log.Printf("Failed to reload certificates: %v", err)
}
```

This is useful for automated certificate renewal (e.g., with Let's Encrypt).

## Monitoring TLS Connections

The server automatically tracks TLS-related metrics:

```go
metrics := nfs.GetMetrics()

fmt.Printf("TLS Handshakes: %d\n", metrics.TLSHandshakes)
fmt.Printf("TLS Handshake Failures: %d\n", metrics.TLSHandshakeFailures)
fmt.Printf("Client Certs Provided: %d\n", metrics.TLSClientCertProvided)
fmt.Printf("Client Certs Validated: %d\n", metrics.TLSClientCertValidated)
fmt.Printf("Client Certs Rejected: %d\n", metrics.TLSClientCertRejected)
fmt.Printf("TLS Session Reused: %d\n", metrics.TLSSessionReused)
fmt.Printf("TLS 1.2 Connections: %d\n", metrics.TLSVersion12)
fmt.Printf("TLS 1.3 Connections: %d\n", metrics.TLSVersion13)
```

## Security Best Practices

### 1. Use Strong TLS Versions

```go
// Recommended: Require TLS 1.2 or higher
tlsConfig.MinVersion = tls.VersionTLS12
tlsConfig.MaxVersion = tls.VersionTLS13
```

### 2. Enable Mutual TLS for Production

```go
tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
tlsConfig.CAFile = "/path/to/ca.crt"
```

### 3. Use Valid Certificates

- Avoid self-signed certificates in production
- Use certificates from a trusted CA
- Set proper Subject Alternative Names (SANs)
- Monitor certificate expiration

### 4. Protect Private Keys

```bash
# Set restrictive permissions on private keys
chmod 600 server.key
chown nfs-user:nfs-group server.key
```

### 5. Regular Certificate Rotation

- Rotate certificates before expiration
- Use automated renewal (e.g., Let's Encrypt)
- Implement certificate monitoring

### 6. Disable Insecure Protocols

```go
// Never use in production
tlsConfig.InsecureSkipVerify = false
```

### 7. Prefer Server Cipher Suites

```go
tlsConfig.PreferServerCipherSuites = true
```

## Troubleshooting

### TLS Handshake Failures

**Symptom**: Clients cannot connect, handshake errors in logs

**Solutions**:
1. Verify certificate and key files exist and are readable
2. Check certificate and key match:
   ```bash
   openssl x509 -noout -modulus -in server.crt | openssl md5
   openssl rsa -noout -modulus -in server.key | openssl md5
   ```
3. Verify certificate validity:
   ```bash
   openssl x509 -in server.crt -text -noout
   ```

### Client Certificate Validation Failures

**Symptom**: Clients with certificates are rejected

**Solutions**:
1. Verify CA file is correct
2. Check client certificate is signed by the CA:
   ```bash
   openssl verify -CAfile ca.crt client.crt
   ```
3. Ensure client certificate has not expired

### Performance Issues

**Symptom**: Slow performance with TLS enabled

**Solutions**:
1. Check metrics for TLS session reuse
2. Increase buffer sizes for encrypted connections
3. Use TLS 1.3 for better performance
4. Consider hardware acceleration (AES-NI)

### Certificate Rotation Not Working

**Symptom**: New certificate not being used

**Solutions**:
1. Verify new certificate files are in the correct location
2. Check file permissions
3. Call `ReloadCertificates()` explicitly
4. Check logs for reload errors

## Integration with NFS Clients

### Linux NFS Client with TLS

The Linux NFS client does not natively support TLS. To use TLS:

1. Use a TLS proxy (e.g., stunnel, nginx)
2. Configure proxy to handle TLS termination
3. Point NFS client to proxy

### Custom Client Implementation

If implementing a custom NFS client with TLS support:

```go
import (
    "crypto/tls"
    "net"
)

// Client TLS configuration
clientConfig := &tls.Config{
    // Verify server certificate
    InsecureSkipVerify: false,

    // Client certificate for mutual TLS
    Certificates: []tls.Certificate{clientCert},

    // CA to verify server certificate
    RootCAs: caCertPool,

    // TLS versions
    MinVersion: tls.VersionTLS12,
    MaxVersion: tls.VersionTLS13,
}

// Connect with TLS
conn, err := tls.Dial("tcp", "nfs-server.example.com:2049", clientConfig)
```

## Advanced Topics

### Session Resumption

TLS session resumption improves performance by reusing previous handshake results. This is automatically enabled in the server.

### ALPN (Application-Layer Protocol Negotiation)

For advanced protocol negotiation:

```go
tlsConfig.NextProtos = []string{"nfs"}
```

### Client Certificate Identity Extraction

The server can extract identity from client certificates:

```go
import "crypto/x509"

// In your authentication handler
identity := absnfs.ExtractCertificateIdentity(clientCert)
info := absnfs.GetCertificateInfo(clientCert)
log.Printf("Client identity: %s, Info: %s", identity, info)
```

## Related Documentation

- [Security Guide](security.md) - General security practices
- [Configuration Guide](configuration.md) - Server configuration
- [Monitoring Guide](monitoring.md) - Metrics and monitoring

## References

- [RFC 8446 - The Transport Layer Security (TLS) Protocol Version 1.3](https://tools.ietf.org/html/rfc8446)
- [RFC 5246 - The Transport Layer Security (TLS) Protocol Version 1.2](https://tools.ietf.org/html/rfc5246)
- [OWASP TLS Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Transport_Layer_Protection_Cheat_Sheet.html)
