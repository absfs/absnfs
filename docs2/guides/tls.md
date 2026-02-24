# TLS

absnfs supports TLS encryption for all NFS connections. When enabled, the server
uses a TLS listener instead of a plain TCP listener.

## TLSConfig Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `Enabled` | `bool` | `false` | Master switch for TLS |
| `CertFile` | `string` | (required) | Path to server certificate (PEM) |
| `KeyFile` | `string` | (required) | Path to server private key (PEM) |
| `CAFile` | `string` | `""` | Path to CA certificate for client verification |
| `ClientAuth` | `tls.ClientAuthType` | `tls.NoClientCert` | Client certificate policy |
| `MinVersion` | `uint16` | `tls.VersionTLS12` | Minimum TLS version |
| `MaxVersion` | `uint16` | `tls.VersionTLS13` | Maximum TLS version |
| `CipherSuites` | `[]uint16` | (secure defaults) | Allowed cipher suites |
| `PreferServerCipherSuites` | `bool` | `true` | Server chooses cipher |
| `InsecureSkipVerify` | `bool` | `false` | Skip verification (testing only) |

## Client Authentication Modes

| Value | String Alias | Behavior |
|-------|-------------|----------|
| `tls.NoClientCert` | `"none"` | No client certificate required |
| `tls.RequestClientCert` | `"request"` | Requests but does not require a client cert |
| `tls.RequireAnyClientCert` | `"require-any"` | Requires a client cert, no CA verification |
| `tls.VerifyClientCertIfGiven` | `"verify-if-given"` | Verifies against CA if a cert is provided |
| `tls.RequireAndVerifyClientCert` | `"require"` | Requires a client cert verified against CA |

Use `absnfs.ParseClientAuthType()` to convert string names to the `tls.ClientAuthType` constant.

## Generating Test Certificates

For development and testing, create a self-signed CA and server certificate:

```sh
# Generate CA key and certificate
openssl genrsa -out ca.key 4096
openssl req -new -x509 -key ca.key -out ca.crt -days 365 \
  -subj "/CN=Test NFS CA"

# Generate server key and certificate
openssl genrsa -out server.key 4096
openssl req -new -key server.key -out server.csr \
  -subj "/CN=localhost"
openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key \
  -CAcreateserial -out server.crt -days 365

# Generate client key and certificate (for mTLS)
openssl genrsa -out client.key 4096
openssl req -new -key client.key -out client.csr \
  -subj "/CN=nfs-client"
openssl x509 -req -in client.csr -CA ca.crt -CAkey ca.key \
  -CAcreateserial -out client.crt -days 365
```

## Server-Only TLS

Encrypt connections without requiring client certificates:

```go
nfs, err := absnfs.New(fs, absnfs.ExportOptions{
	TLS: &absnfs.TLSConfig{
		Enabled:  true,
		CertFile: "/etc/nfs/server.crt",
		KeyFile:  "/etc/nfs/server.key",
	},
})
```

## Mutual TLS (mTLS)

Require and verify client certificates against a CA:

```go
nfs, err := absnfs.New(fs, absnfs.ExportOptions{
	TLS: &absnfs.TLSConfig{
		Enabled:    true,
		CertFile:   "/etc/nfs/server.crt",
		KeyFile:    "/etc/nfs/server.key",
		CAFile:     "/etc/nfs/ca.crt",
		ClientAuth: tls.RequireAndVerifyClientCert,
	},
})
```

## Secure Defaults

`DefaultTLSConfig()` returns a TLSConfig with:
- TLS 1.2 minimum, TLS 1.3 maximum
- Server cipher suite preference enabled
- A curated list of cipher suites (ECDHE + AES-GCM and ChaCha20)
- Certificate verification enabled

```go
tlsConfig := absnfs.DefaultTLSConfig()
tlsConfig.Enabled = true
tlsConfig.CertFile = "/etc/nfs/server.crt"
tlsConfig.KeyFile = "/etc/nfs/server.key"

nfs, err := absnfs.New(fs, absnfs.ExportOptions{
	TLS: tlsConfig,
})
```

## Certificate Rotation

Reload certificates at runtime without restarting the server or dropping
connections:

```go
opts := nfs.GetExportOptions()
if opts.TLS != nil {
	if err := opts.TLS.ReloadCertificates(); err != nil {
		log.Printf("certificate reload failed: %v", err)
	}
}
```

The new certificate takes effect on the next TLS handshake. Existing connections
continue using the previous certificate until they reconnect.

## Validation

`TLSConfig.Validate()` checks that:
- `CertFile` and `KeyFile` are set and the files exist
- `CAFile` exists when client auth requires verification
- `MinVersion` is not greater than `MaxVersion`
- TLS versions below 1.2 are rejected

Validation runs automatically during `BuildConfig()`, so invalid configurations
are caught at server startup.

## Helper Functions

| Function | Purpose |
|----------|---------|
| `DefaultTLSConfig()` | Returns a TLSConfig with secure defaults |
| `ParseClientAuthType(s)` | Converts a string to `tls.ClientAuthType` |
| `ParseTLSVersion(s)` | Converts a string like `"1.3"` to a TLS version constant |
| `TLSVersionString(v)` | Converts a TLS version constant to a display string |
| `ExtractCertificateIdentity(cert)` | Gets the CN or SAN from a client certificate |
| `GetCertificateInfo(cert)` | Returns a human-readable certificate summary |
