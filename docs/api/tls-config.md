# TLS Configuration API

The TLS configuration API enables secure, encrypted NFS communication using TLS/SSL.

## Overview

ABSNFS supports TLS/SSL encryption to protect NFS traffic from eavesdropping and man-in-the-middle attacks. This is particularly important when NFS is used over untrusted networks or the internet.

## Configuration

TLS is configured through the `ExportOptions` struct:

```go
options := absnfs.ExportOptions{
    TLSEnabled: true,
    TLSCertFile: "/path/to/server.crt",
    TLSKeyFile: "/path/to/server.key",
    TLSClientAuth: absnfs.TLSClientAuthNone, // Optional client cert verification
}

server, err := absnfs.New(fs, options)
```

## TLS Options

### TLSEnabled
- **Type**: `bool`
- **Default**: `false`
- **Description**: Enable TLS/SSL encryption for NFS connections

### TLSCertFile
- **Type**: `string`
- **Required**: When TLSEnabled is true
- **Description**: Path to the server's TLS certificate file (PEM format)
- **Example**: `"/etc/nfs/server.crt"`

### TLSKeyFile
- **Type**: `string`
- **Required**: When TLSEnabled is true
- **Description**: Path to the server's private key file (PEM format)
- **Example**: `"/etc/nfs/server.key"`

### TLSClientAuth
- **Type**: `TLSClientAuthType`
- **Default**: `TLSClientAuthNone`
- **Description**: Client certificate authentication mode
- **Values**:
  - `TLSClientAuthNone`: No client certificate required
  - `TLSClientAuthRequest`: Request client certificate but don't require it
  - `TLSClientAuthRequire`: Require valid client certificate

### TLSClientCAs
- **Type**: `string`
- **Optional**: Required when TLSClientAuth is not None
- **Description**: Path to CA certificate file for verifying client certificates
- **Example**: `"/etc/nfs/ca.crt"`

### TLSMinVersion
- **Type**: `uint16`
- **Default**: `tls.VersionTLS12`
- **Description**: Minimum TLS version to accept
- **Values**:
  - `tls.VersionTLS10`: TLS 1.0 (not recommended)
  - `tls.VersionTLS11`: TLS 1.1 (not recommended)
  - `tls.VersionTLS12`: TLS 1.2 (recommended minimum)
  - `tls.VersionTLS13`: TLS 1.3 (most secure)

## Certificate Generation

### Self-Signed Certificates (Development)

For testing and development:

```bash
# Generate private key
openssl genrsa -out server.key 2048

# Generate self-signed certificate
openssl req -new -x509 -key server.key -out server.crt -days 365 \
    -subj "/CN=nfs-server.local"
```

### Production Certificates

For production, use certificates from a trusted CA:

```bash
# Generate certificate signing request
openssl req -new -key server.key -out server.csr \
    -subj "/CN=nfs-server.example.com"

# Submit CSR to your CA and receive signed certificate
# Place signed certificate in server.crt
```

### Let's Encrypt

For publicly accessible servers:

```bash
# Using certbot
certbot certonly --standalone -d nfs.example.com

# Certificates will be in /etc/letsencrypt/live/nfs.example.com/
# Use fullchain.pem as TLSCertFile
# Use privkey.pem as TLSKeyFile
```

## Example Configurations

### Basic TLS (Server Authentication Only)

```go
package main

import (
    "log"
    "github.com/absfs/absnfs"
    "github.com/absfs/memfs"
)

func main() {
    fs, _ := memfs.NewFS()

    options := absnfs.ExportOptions{
        TLSEnabled: true,
        TLSCertFile: "/etc/nfs/server.crt",
        TLSKeyFile: "/etc/nfs/server.key",
    }

    server, err := absnfs.New(fs, options)
    if err != nil {
        log.Fatal(err)
    }

    server.Export("/export/secure")
}
```

### Mutual TLS (Client and Server Authentication)

```go
import "crypto/tls"

options := absnfs.ExportOptions{
    TLSEnabled: true,
    TLSCertFile: "/etc/nfs/server.crt",
    TLSKeyFile: "/etc/nfs/server.key",
    TLSClientAuth: absnfs.TLSClientAuthRequire,
    TLSClientCAs: "/etc/nfs/client-ca.crt",
    TLSMinVersion: tls.VersionTLS12,
}
```

### High Security Configuration

```go
import "crypto/tls"

options := absnfs.ExportOptions{
    TLSEnabled: true,
    TLSCertFile: "/etc/nfs/server.crt",
    TLSKeyFile: "/etc/nfs/server.key",
    TLSClientAuth: absnfs.TLSClientAuthRequire,
    TLSClientCAs: "/etc/nfs/client-ca.crt",
    TLSMinVersion: tls.VersionTLS13,  // Require TLS 1.3

    // Combine with other security features
    AllowedIPs: []string{"10.0.0.0/8"},
    RateLimitEnabled: true,
    ReadOnly: true,
}
```

## Client Configuration

NFS clients must be configured to use TLS:

### Linux Client (NFSv3 with TLS)

Most standard NFS clients don't support TLS natively. You'll need to:

1. Use a TLS tunnel (e.g., stunnel)
2. Use a custom NFS client that supports TLS
3. Use SSH tunneling

### Using stunnel as TLS Wrapper

Server side already handles TLS. For clients without TLS support:

```bash
# Client-side stunnel configuration
# /etc/stunnel/nfs-client.conf
[nfs-client]
client = yes
accept = 127.0.0.1:2049
connect = nfs-server.example.com:2049
verify = 2
CAfile = /etc/nfs/ca.crt

# Mount via localhost
mount -t nfs 127.0.0.1:/export/secure /mnt/nfs
```

## Security Considerations

### Certificate Validation

**Always verify**:
- Certificate is not expired
- Certificate matches the server hostname
- Certificate is signed by a trusted CA (in production)

### Private Key Protection

**Protect the private key**:
```bash
# Set restrictive permissions
chmod 600 /etc/nfs/server.key
chown root:root /etc/nfs/server.key
```

### TLS Version

**Recommendations**:
- **Minimum**: TLS 1.2
- **Recommended**: TLS 1.3
- **Avoid**: TLS 1.0, TLS 1.1 (deprecated)

### Cipher Suites

The default Go TLS implementation uses secure cipher suites. For custom requirements, configure via environment or code.

## Performance Impact

### Latency

- **TLS Handshake**: 1-2 RTT overhead on connection establishment
- **Encryption/Decryption**: ~5-10% CPU overhead
- **Connection Reuse**: Minimal overhead after handshake

### Throughput

- **Modern CPUs with AES-NI**: Minimal impact (<5%)
- **Older CPUs**: 10-20% throughput reduction
- **Recommendation**: Use TLS 1.3 for best performance

### Optimization

```go
// Increase worker pool for TLS workloads
options.WorkerPoolSize = runtime.NumCPU() * 4
```

## Monitoring

### TLS Connection Metrics

Check server logs for TLS-related events:

```go
server.SetLogger(log.New(os.Stdout, "NFS-TLS: ", log.LstdFlags))
```

### Certificate Expiration

Monitor certificate expiration:

```bash
# Check certificate validity
openssl x509 -in /etc/nfs/server.crt -noout -enddate

# Example output: notAfter=Jan 1 00:00:00 2025 GMT
```

### Automated Renewal

For Let's Encrypt:

```bash
# Auto-renew with certbot
certbot renew --deploy-hook "systemctl reload nfs-server"
```

## Troubleshooting

### Certificate Errors

**Error**: "certificate verify failed"

**Solutions**:
- Check certificate file path
- Verify certificate is valid and not expired
- Ensure certificate and key match
- Check file permissions

### TLS Handshake Failures

**Error**: "TLS handshake failed"

**Check**:
- TLS version compatibility
- Cipher suite compatibility
- Client certificate issues (if mutual TLS)
- Firewall blocking TLS port

### Performance Issues

**Symptoms**: Slow NFS operations with TLS

**Solutions**:
- Verify CPU has AES-NI support
- Increase worker pool size
- Use TLS 1.3 for better performance
- Consider TLS session resumption

## See Also

- [TLS Encryption Guide](../guides/tls-encryption.md)
- [Security Guide](../guides/security.md)
- [TLS Example](../examples/tls-nfs-server.md)
- [Export Options API](./export-options.md)
- [Performance Tuning](../guides/performance-tuning.md)
