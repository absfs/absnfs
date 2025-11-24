# TLS Configuration API

The TLS configuration API enables secure, encrypted NFS communication using TLS/SSL.

## Overview

ABSNFS supports TLS/SSL encryption to protect NFS traffic from eavesdropping and man-in-the-middle attacks. This is particularly important when NFS is used over untrusted networks or the internet.

TLS is configured via the `TLS` field in `ExportOptions`, which accepts a pointer to a `TLSConfig` struct. This nested structure provides fine-grained control over all TLS parameters.

## Configuration

TLS is configured through the `TLS` field in `ExportOptions`:

```go
options := absnfs.ExportOptions{
    TLS: &absnfs.TLSConfig{
        Enabled:    true,
        CertFile:   "/path/to/server.crt",
        KeyFile:    "/path/to/server.key",
        MinVersion: tls.VersionTLS12,
    },
}

server, err := absnfs.New(fs, options)
```

## TLSConfig Structure

The `TLSConfig` struct contains all TLS-related settings:

```go
type TLSConfig struct {
    Enabled                  bool
    CertFile                 string
    KeyFile                  string
    CAFile                   string
    ClientAuth               tls.ClientAuthType
    MinVersion               uint16
    MaxVersion               uint16
    CipherSuites             []uint16
    PreferServerCipherSuites bool
    InsecureSkipVerify       bool
}
```

### Field Reference

#### Enabled
- **Type**: `bool`
- **Default**: `false`
- **Description**: Enable TLS/SSL encryption for NFS connections
- **Example**:
  ```go
  TLS: &absnfs.TLSConfig{
      Enabled: true,
      // ... other fields
  }
  ```

#### CertFile
- **Type**: `string`
- **Required**: When `Enabled` is `true`
- **Description**: Path to the server's TLS certificate file in PEM format
- **Example**: `"/etc/nfs/server.crt"`
- **Notes**: File must exist and be readable; validated on server startup

#### KeyFile
- **Type**: `string`
- **Required**: When `Enabled` is `true`
- **Description**: Path to the server's private key file in PEM format
- **Example**: `"/etc/nfs/server.key"`
- **Notes**: File must exist and be readable; should have restrictive permissions (600)

#### CAFile
- **Type**: `string`
- **Optional**: Required when `ClientAuth` requires client certificate verification
- **Description**: Path to CA certificate file for verifying client certificates
- **Example**: `"/etc/nfs/ca.crt"`
- **Notes**: Used when `ClientAuth` is set to `VerifyClientCertIfGiven` or `RequireAndVerifyClientCert`

#### ClientAuth
- **Type**: `tls.ClientAuthType` (from `crypto/tls`)
- **Default**: `tls.NoClientCert`
- **Description**: Client certificate authentication policy
- **Values**:
  - `tls.NoClientCert`: No client certificate required (server auth only)
  - `tls.RequestClientCert`: Request client certificate but don't require it
  - `tls.RequireAnyClientCert`: Require a client certificate (not validated)
  - `tls.VerifyClientCertIfGiven`: Verify client certificate if provided
  - `tls.RequireAndVerifyClientCert`: Require and verify client certificate (mutual TLS)
- **Example**:
  ```go
  import "crypto/tls"

  TLS: &absnfs.TLSConfig{
      ClientAuth: tls.RequireAndVerifyClientCert,
      CAFile:     "/etc/nfs/ca.crt",
      // ... other fields
  }
  ```

#### MinVersion
- **Type**: `uint16`
- **Default**: `tls.VersionTLS12`
- **Description**: Minimum TLS version to accept
- **Values**:
  - `tls.VersionTLS10`: TLS 1.0 (not recommended, deprecated)
  - `tls.VersionTLS11`: TLS 1.1 (not recommended, deprecated)
  - `tls.VersionTLS12`: TLS 1.2 (recommended minimum)
  - `tls.VersionTLS13`: TLS 1.3 (most secure, best performance)
- **Example**:
  ```go
  import "crypto/tls"

  TLS: &absnfs.TLSConfig{
      MinVersion: tls.VersionTLS13,
      // ... other fields
  }
  ```

#### MaxVersion
- **Type**: `uint16`
- **Default**: `tls.VersionTLS13`
- **Description**: Maximum TLS version to accept
- **Notes**: Usually left at default; only set if you need to restrict to older TLS versions

#### CipherSuites
- **Type**: `[]uint16`
- **Default**: Secure cipher suites from `crypto/tls`
- **Description**: List of enabled cipher suites for TLS 1.2 connections
- **Notes**:
  - TLS 1.3 cipher suites are not configurable
  - If empty, Go's secure defaults are used
  - Only needed for custom security requirements
- **Example**:
  ```go
  import "crypto/tls"

  TLS: &absnfs.TLSConfig{
      CipherSuites: []uint16{
          tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
          tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
          tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
      },
      // ... other fields
  }
  ```

#### PreferServerCipherSuites
- **Type**: `bool`
- **Default**: `true` (from `DefaultTLSConfig()`)
- **Description**: Use server's cipher suite preferences instead of client's
- **Notes**: Recommended to keep as `true` for better server-side security control

#### InsecureSkipVerify
- **Type**: `bool`
- **Default**: `false`
- **Description**: Skip verification of client certificates
- **Warning**: Only for testing/development; never use in production
- **Example**:
  ```go
  // Testing only!
  TLS: &absnfs.TLSConfig{
      InsecureSkipVerify: true, // DO NOT USE IN PRODUCTION
      // ... other fields
  }
  ```

## Helper Functions

### DefaultTLSConfig()

Returns a `TLSConfig` with secure defaults:

```go
func DefaultTLSConfig() *TLSConfig
```

**Returns**: A pre-configured `TLSConfig` with:
- `Enabled: false`
- `MinVersion: tls.VersionTLS12`
- `MaxVersion: tls.VersionTLS13`
- `ClientAuth: tls.NoClientCert`
- Secure cipher suites for TLS 1.2
- `PreferServerCipherSuites: true`

**Example**:
```go
tlsConfig := absnfs.DefaultTLSConfig()
tlsConfig.Enabled = true
tlsConfig.CertFile = "/etc/nfs/server.crt"
tlsConfig.KeyFile = "/etc/nfs/server.key"

options := absnfs.ExportOptions{
    TLS: tlsConfig,
}
```

### ParseClientAuthType()

Parses a string into a `tls.ClientAuthType`:

```go
func ParseClientAuthType(s string) (tls.ClientAuthType, error)
```

**Supported values**:
- `"NoClientCert"`, `"none"`, or `""` → `tls.NoClientCert`
- `"RequestClientCert"`, `"request"` → `tls.RequestClientCert`
- `"RequireAnyClientCert"`, `"require-any"` → `tls.RequireAnyClientCert`
- `"VerifyClientCertIfGiven"`, `"verify-if-given"` → `tls.VerifyClientCertIfGiven`
- `"RequireAndVerifyClientCert"`, `"require-and-verify"`, `"require"` → `tls.RequireAndVerifyClientCert`

### ParseTLSVersion()

Parses a string into a TLS version constant:

```go
func ParseTLSVersion(s string) (uint16, error)
```

**Supported values**:
- `"1.0"`, `"TLS1.0"`, `"TLSv1.0"` → `tls.VersionTLS10`
- `"1.1"`, `"TLS1.1"`, `"TLSv1.1"` → `tls.VersionTLS11`
- `"1.2"`, `"TLS1.2"`, `"TLSv1.2"`, or `""` → `tls.VersionTLS12`
- `"1.3"`, `"TLS1.3"`, `"TLSv1.3"` → `tls.VersionTLS13`

### TLSVersionString()

Returns the string representation of a TLS version:

```go
func TLSVersionString(version uint16) string
```

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
# Use fullchain.pem as CertFile
# Use privkey.pem as KeyFile
```

### Client Certificates (Mutual TLS)

Generate client certificates for mutual TLS:

```bash
# Generate CA for signing client certs (if you don't have one)
openssl genrsa -out ca.key 4096
openssl req -new -x509 -key ca.key -out ca.crt -days 3650 \
    -subj "/CN=NFS-CA"

# Generate client key
openssl genrsa -out client.key 2048

# Generate client CSR
openssl req -new -key client.key -out client.csr \
    -subj "/CN=nfs-client"

# Sign client certificate with CA
openssl x509 -req -in client.csr -CA ca.crt -CAkey ca.key \
    -CAcreateserial -out client.crt -days 365
```

## Example Configurations

### Basic TLS (Server Authentication Only)

```go
package main

import (
    "crypto/tls"
    "log"

    "github.com/absfs/absnfs"
    "github.com/absfs/memfs"
)

func main() {
    fs, _ := memfs.NewFS()

    options := absnfs.ExportOptions{
        TLS: &absnfs.TLSConfig{
            Enabled:    true,
            CertFile:   "/etc/nfs/server.crt",
            KeyFile:    "/etc/nfs/server.key",
            MinVersion: tls.VersionTLS12,
        },
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
    TLS: &absnfs.TLSConfig{
        Enabled:    true,
        CertFile:   "/etc/nfs/server.crt",
        KeyFile:    "/etc/nfs/server.key",
        CAFile:     "/etc/nfs/client-ca.crt",
        ClientAuth: tls.RequireAndVerifyClientCert,
        MinVersion: tls.VersionTLS12,
    },
}
```

### High Security Configuration

```go
import "crypto/tls"

options := absnfs.ExportOptions{
    TLS: &absnfs.TLSConfig{
        Enabled:                  true,
        CertFile:                 "/etc/nfs/server.crt",
        KeyFile:                  "/etc/nfs/server.key",
        CAFile:                   "/etc/nfs/client-ca.crt",
        ClientAuth:               tls.RequireAndVerifyClientCert,
        MinVersion:               tls.VersionTLS13,
        PreferServerCipherSuites: true,
    },

    // Combine with other security features
    AllowedIPs:       []string{"10.0.0.0/8"},
    RateLimitEnabled: true,
    ReadOnly:         true,
}
```

### Using DefaultTLSConfig()

```go
import "crypto/tls"

tlsConfig := absnfs.DefaultTLSConfig()
tlsConfig.Enabled = true
tlsConfig.CertFile = "/etc/nfs/server.crt"
tlsConfig.KeyFile = "/etc/nfs/server.key"
tlsConfig.MinVersion = tls.VersionTLS13

options := absnfs.ExportOptions{
    TLS: tlsConfig,
}
```

### TLS with Custom Cipher Suites

```go
import "crypto/tls"

options := absnfs.ExportOptions{
    TLS: &absnfs.TLSConfig{
        Enabled:    true,
        CertFile:   "/etc/nfs/server.crt",
        KeyFile:    "/etc/nfs/server.key",
        MinVersion: tls.VersionTLS12,
        MaxVersion: tls.VersionTLS13,
        CipherSuites: []uint16{
            // TLS 1.2 cipher suites
            tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
            tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
            tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
            tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
        },
        PreferServerCipherSuites: true,
    },
}
```

## Client Configuration

NFS clients must be configured to use TLS. Since standard NFS clients don't natively support TLS, you'll need to use a TLS wrapper.

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

# For mutual TLS, add:
cert = /etc/nfs/client.crt
key = /etc/nfs/client.key

# Mount via localhost
mount -t nfs 127.0.0.1:/export/secure /mnt/nfs
```

### SSH Tunneling Alternative

```bash
# Create SSH tunnel
ssh -L 2049:localhost:2049 user@nfs-server.example.com

# Mount via localhost
mount -t nfs 127.0.0.1:/export/secure /mnt/nfs
```

## Certificate Management

### Reloading Certificates

The TLSConfig provides a method to reload certificates without restarting:

```go
// In your server code
func reloadCertificates(server *absnfs.Server) error {
    if server.Options.TLS != nil {
        return server.Options.TLS.ReloadCertificates()
    }
    return nil
}
```

### Monitoring Expiration

Monitor certificate expiration:

```bash
# Check certificate validity
openssl x509 -in /etc/nfs/server.crt -noout -enddate

# Example output: notAfter=Jan 1 00:00:00 2026 GMT

# Check with more detail
openssl x509 -in /etc/nfs/server.crt -noout -dates -subject -issuer
```

### Automated Renewal (Let's Encrypt)

```bash
# Auto-renew with certbot
certbot renew --deploy-hook "systemctl reload nfs-server"

# Or using the API
certbot renew --deploy-hook "curl -X POST http://localhost:8080/api/reload-certs"
```

## Security Considerations

### Certificate Validation

**Always verify**:
- Certificate is not expired
- Certificate matches the server hostname
- Certificate is signed by a trusted CA (in production)
- Certificate chain is valid

### Private Key Protection

**Protect the private key**:
```bash
# Set restrictive permissions
chmod 600 /etc/nfs/server.key
chown root:root /etc/nfs/server.key

# For client keys
chmod 600 /etc/nfs/client.key
chown nfs-user:nfs-user /etc/nfs/client.key
```

### TLS Version Recommendations

**Best practices**:
- **Minimum**: TLS 1.2 (`tls.VersionTLS12`)
- **Recommended**: TLS 1.3 (`tls.VersionTLS13`)
- **Avoid**: TLS 1.0, TLS 1.1 (both deprecated and insecure)

```go
// Good: TLS 1.3 only
TLS: &absnfs.TLSConfig{
    MinVersion: tls.VersionTLS13,
    MaxVersion: tls.VersionTLS13,
}

// Acceptable: TLS 1.2+
TLS: &absnfs.TLSConfig{
    MinVersion: tls.VersionTLS12,
}

// Bad: TLS 1.0/1.1 (DON'T DO THIS)
TLS: &absnfs.TLSConfig{
    MinVersion: tls.VersionTLS10, // INSECURE
}
```

### Cipher Suite Recommendations

The default cipher suites are secure. Only customize if you have specific requirements:

```go
// Secure cipher suites for TLS 1.2
TLS: &absnfs.TLSConfig{
    CipherSuites: []uint16{
        // ECDHE provides forward secrecy
        tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
        tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
        tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
        tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
        tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
        tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
    },
}
```

### Mutual TLS Best Practices

When using mutual TLS:
1. Use `tls.RequireAndVerifyClientCert` for maximum security
2. Maintain a proper PKI infrastructure
3. Implement certificate revocation (CRL or OCSP)
4. Rotate client certificates regularly
5. Monitor failed authentication attempts

## Performance Considerations

### TLS Overhead

**Latency**:
- TLS handshake: 1-2 RTT overhead on connection establishment
- TLS 1.3 handshake: Faster than TLS 1.2 (1-RTT vs 2-RTT)
- Encryption/Decryption: 5-10% CPU overhead with modern hardware

**Throughput**:
- Modern CPUs with AES-NI: Minimal impact (<5%)
- Older CPUs without AES-NI: 10-20% throughput reduction
- TLS 1.3: Better performance than TLS 1.2

### Optimization Tips

1. **Use TLS 1.3** for best performance:
   ```go
   TLS: &absnfs.TLSConfig{
       MinVersion: tls.VersionTLS13,
   }
   ```

2. **Increase worker pool** for TLS workloads:
   ```go
   import "runtime"

   options := absnfs.ExportOptions{
       TLS: &absnfs.TLSConfig{
           Enabled: true,
           // ... cert config
       },
       WorkerPoolSize: runtime.NumCPU() * 4,
   }
   ```

3. **Enable hardware acceleration**: Ensure CPU has AES-NI support:
   ```bash
   # Check for AES-NI support
   grep -m1 aes /proc/cpuinfo  # Linux
   sysctl machdep.cpu.features | grep AES  # macOS
   ```

4. **Connection pooling**: Reuse TLS connections to avoid handshake overhead

## Validation and Error Handling

The TLSConfig validates itself on server startup:

```go
options := absnfs.ExportOptions{
    TLS: &absnfs.TLSConfig{
        Enabled:  true,
        CertFile: "/etc/nfs/server.crt",
        KeyFile:  "/etc/nfs/server.key",
    },
}

server, err := absnfs.New(fs, options)
if err != nil {
    // Handle validation errors:
    // - Missing required fields (CertFile, KeyFile)
    // - File not found errors
    // - Invalid certificate/key pair
    // - TLS version conflicts
    log.Fatalf("TLS configuration error: %v", err)
}
```

Common validation errors:
- `"TLS certificate file is required when TLS is enabled"`
- `"TLS key file is required when TLS is enabled"`
- `"TLS certificate file not found: ..."`
- `"TLS key file not found: ..."`
- `"TLS min version (X) cannot be greater than max version (Y)"`

## Troubleshooting

### Certificate Errors

**Error**: `"failed to load server certificate and key"`

**Solutions**:
- Verify certificate file path is correct
- Check certificate file is readable
- Ensure certificate and key match:
  ```bash
  # Extract modulus from cert and key - they should match
  openssl x509 -noout -modulus -in server.crt | openssl md5
  openssl rsa -noout -modulus -in server.key | openssl md5
  ```
- Check file permissions (readable by NFS server process)

**Error**: `"certificate verify failed"`

**Solutions**:
- Verify certificate is valid and not expired:
  ```bash
  openssl x509 -in server.crt -noout -dates
  ```
- Check certificate chain is complete
- Ensure CA certificate is properly configured

### TLS Handshake Failures

**Error**: TLS handshake timeouts or failures

**Check**:
1. TLS version compatibility between client and server
2. Cipher suite compatibility
3. Client certificate issues (if using mutual TLS)
4. Firewall rules allowing TLS traffic
5. Certificate hostname mismatch

**Debug**:
```bash
# Test TLS connection
openssl s_client -connect nfs-server.example.com:2049 \
    -showcerts -tlsextdebug

# Test with specific TLS version
openssl s_client -connect nfs-server.example.com:2049 -tls1_2
openssl s_client -connect nfs-server.example.com:2049 -tls1_3
```

### Client Authentication Issues

**Error**: Client certificate verification failures

**Solutions**:
- Verify CA file path is correct
- Check client certificate is signed by the CA
- Ensure certificate is not expired
- Verify certificate common name or SAN
- Check that `ClientAuth` is set correctly:
  ```go
  TLS: &absnfs.TLSConfig{
      ClientAuth: tls.RequireAndVerifyClientCert,
      CAFile:     "/etc/nfs/ca.crt", // Must be set
  }
  ```

### Performance Issues

**Symptoms**: Slow NFS operations with TLS enabled

**Diagnosis**:
```bash
# Check CPU features
grep -m1 aes /proc/cpuinfo

# Monitor CPU usage during operations
top -p $(pgrep nfs-server)
```

**Solutions**:
1. Verify CPU has AES-NI support
2. Increase worker pool size
3. Use TLS 1.3 for better performance
4. Consider connection reuse/pooling
5. Profile the application:
   ```bash
   go tool pprof -http=:6060 http://localhost:6060/debug/pprof/profile
   ```

## See Also

- [Export Options API](./export-options.md) - Complete ExportOptions documentation
- [Security Guide](../guides/security.md) - Overall security best practices
- [TLS Example](../examples/tls-nfs-server.md) - Complete working examples
- [Performance Tuning](../guides/performance-tuning.md) - Performance optimization
- [Go crypto/tls Documentation](https://pkg.go.dev/crypto/tls) - Standard library TLS reference
