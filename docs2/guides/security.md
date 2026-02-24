# Security

absnfs provides several layers of access control: IP filtering, privileged port
enforcement, read-only mode, user ID squashing, rate limiting, and TLS encryption.

## IP Filtering

Restrict which clients can connect using `AllowedIPs`. Supports individual
addresses and CIDR ranges. When empty, all clients are allowed.

```go
nfs, err := absnfs.New(fs, absnfs.ExportOptions{
	AllowedIPs: []string{
		"192.168.1.0/24",  // Entire subnet
		"10.0.0.5",        // Single host
	},
})
```

Connections from IPs not in the list are rejected at the TCP accept stage before
any NFS processing occurs. IPv4-mapped IPv6 addresses (e.g., `::ffff:192.168.1.1`)
are normalized to their IPv4 form so they match correctly.

## Privileged Ports

Set `Secure: true` to require clients to connect from a privileged source port
(below 1024). On Unix systems, only root can bind privileged ports, so this
provides a basic authentication check.

```go
nfs, err := absnfs.New(fs, absnfs.ExportOptions{
	Secure: true,
})
```

macOS mounts use the `resvport` option to comply with this requirement.

## Read-Only Export

Prevent all write operations at the NFS protocol level:

```go
nfs, err := absnfs.New(fs, absnfs.ExportOptions{
	ReadOnly: true,
})
```

This can be toggled at runtime via `UpdateExportOptions` or `UpdatePolicyOptions`.

## Squash Modes

UID/GID squashing controls how client credentials are mapped on the server.

| Mode | Behavior |
|------|----------|
| `"none"` or `""` | No mapping. Client credentials are used as-is. |
| `"root"` | Maps UID 0 (root) to nobody (65534). Non-root users pass through. |
| `"all"` | Maps all users to nobody (65534). |

```go
nfs, err := absnfs.New(fs, absnfs.ExportOptions{
	Squash: "root",
})
```

The squash mode is **immutable** after creation. Attempting to change it at
runtime returns an error.

In `"root"` mode, GID 0 is also squashed (both primary and auxiliary GIDs).
`AUTH_NONE` connections always map to nobody regardless of squash mode.

## Rate Limiting

Rate limiting is enabled by default. It limits requests per-IP, per-connection,
and per-operation type to prevent abuse.

```go
config := absnfs.DefaultRateLimiterConfig()
config.PerIPRequestsPerSecond = 500
config.PerIPBurstSize = 200

nfs, err := absnfs.New(fs, absnfs.ExportOptions{
	EnableRateLimiting: true,
	RateLimitConfig:    &config,
})
```

To disable rate limiting:

```go
nfs, err := absnfs.New(fs, absnfs.ExportOptions{
	EnableRateLimiting: false,
})
```

When a client exceeds the rate limit, the server responds with `MSG_DENIED`
and the client retries after a backoff.

## TLS Encryption

See the dedicated [TLS guide](tls.md) for setup instructions.

## Combined Example

A hardened export combining multiple security features:

```go
nfs, err := absnfs.New(fs, absnfs.ExportOptions{
	ReadOnly:   true,
	Secure:     true,
	Squash:     "root",
	AllowedIPs: []string{"192.168.1.0/24"},
	TLS: &absnfs.TLSConfig{
		Enabled:    true,
		CertFile:   "/etc/nfs/server.crt",
		KeyFile:    "/etc/nfs/server.key",
		CAFile:     "/etc/nfs/ca.crt",
		ClientAuth: tls.RequireAndVerifyClientCert,
		MinVersion: tls.VersionTLS12,
		MaxVersion: tls.VersionTLS13,
	},
	EnableRateLimiting: true,
	MaxFileSize:        100 * 1024 * 1024, // 100MB
})
```
