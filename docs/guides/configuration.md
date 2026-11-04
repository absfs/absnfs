# Configuration

All export settings are passed through `ExportOptions` when calling `absnfs.New()`.
Fields that are not set use sensible defaults.

## Basic

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `ReadOnly` | `bool` | `false` | Export as read-only |
| `Async` | `bool` | `false` | Allow async writes |
| `MaxFileSize` | `int64` | `0` (unlimited) | Maximum file size in bytes |
| `TransferSize` | `int` | `65536` (64KB) | Max read/write transfer size per RPC |

## Security

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `Secure` | `bool` | `false` | Require privileged source ports (<1024) |
| `AllowedIPs` | `[]string` | `nil` (all allowed) | IP addresses or CIDR ranges allowed to connect |
| `AllowedUIDs` | `[]uint32` | `nil` (any uid) | AUTH_SYS uids served; the uid is the client's claim (see [Security](security.md#limiting-uids)) |
| `Squash` | `string` | `""` (none) | UID mapping: `"root"`, `"all"`, or `"none"` |

## Caching

Changes made through the server are seen by the next request: every
procedure invalidates what it changed, including everything below a renamed
directory. Entries are keyed by the name as spelled, so on a case-insensitive
host filesystem (APFS and NTFS by default) an entry cached under another
spelling of the same name (`/F` for `/f`) is seen to change only once its
timeout has passed. Changes made to the exported filesystem by anything else (another
process, another server over the same files) are seen once the relevant
timeout has passed: `AttrCacheTimeout` for attributes, `NegativeCacheTimeout`
for a name that did not exist, `DirCacheTimeout` for listings. A changed
timeout applies to entries already cached.

### Attribute Cache

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `AttrCacheTimeout` | `time.Duration` | `5s` | How long file attributes are cached |
| `AttrCacheSize` | `int` | `10000` | Maximum entries in the attribute cache |
| `CacheNegativeLookups` | `bool` | `false` | Cache "file not found" results |
| `NegativeCacheTimeout` | `time.Duration` | `5s` | TTL for negative cache entries |

### Directory Cache

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `EnableDirCache` | `bool` | `false` | Enable directory listing cache |
| `DirCacheTimeout` | `time.Duration` | `10s` | How long directory listings are cached |
| `DirCacheMaxEntries` | `int` | `1000` | Maximum cached directories |
| `DirCacheMaxDirSize` | `int` | `10000` | Max entries per directory before skipping cache |

## Performance

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `MaxWorkers` | `int` | `runtime.NumCPU() * 4` | Worker pool goroutines |
| `MaxConnections` | `int` | `100` | Maximum concurrent client connections |
| `IdleTimeout` | `time.Duration` | `5m` | Time before idle connections are closed |

### TCP Tuning

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `TCPKeepAlive` | `bool` | `true` | Enable TCP keep-alive probes |
| `TCPNoDelay` | `bool` | `true` | Disable Nagle's algorithm |
| `SendBufferSize` | `int` | `262144` (256KB) | TCP send buffer size |
| `ReceiveBufferSize` | `int` | `262144` (256KB) | TCP receive buffer size |

## Timeouts

Set via the `Timeouts` field (`*TimeoutConfig`). When `nil`, all defaults apply.

| Field | Default | Description |
|-------|---------|-------------|
| `ReadTimeout` | `30s` | Read operations |
| `WriteTimeout` | `60s` | Write operations |
| `LookupTimeout` | `10s` | File/directory lookups |
| `ReaddirTimeout` | `30s` | Directory listings |
| `CreateTimeout` | `15s` | File/directory creation |
| `RemoveTimeout` | `15s` | Delete operations |
| `RenameTimeout` | `20s` | Rename operations |
| `HandleTimeout` | `5s` | File handle operations |
| `DefaultTimeout` | `30s` | Fallback for unspecified operations |

## Rate Limiting

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `EnableRateLimiting` | `bool` | `false` | Enable rate limiting |
| `RateLimitConfig` | `*RateLimiterConfig` | (see below) | Detailed rate limit settings |

Default `RateLimiterConfig` values:

| Field | Default |
|-------|---------|
| `GlobalRequestsPerSecond` | `10000` |
| `PerIPRequestsPerSecond` | `1000` |
| `PerIPBurstSize` | `500` |
| `PerConnectionRequestsPerSecond` | `500` |
| `PerConnectionBurstSize` | `100` |
| `ReadLargeOpsPerSecond` | `100` |
| `WriteLargeOpsPerSecond` | `50` |
| `ReaddirOpsPerSecond` | `50` |
| `MountOpsPerMinute` | `10` |
| `FileHandlesPerIP` | `10000` |
| `FileHandlesGlobal` | `1000000` |
| `CleanupInterval` | `5m` |

## TLS

Set via the `TLS` field (`*TLSConfig`). When `nil`, TLS is disabled.
See the [TLS guide](tls.md) for details.

## Logging

Set via the `Log` field (`*LogConfig`). When `nil`, logging is disabled.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `Level` | `string` | `"info"` | Minimum log level (`"debug"`, `"info"`, `"warn"`, `"error"`) |
| `Format` | `string` | `"text"` | Output format (`"json"` or `"text"`) |
| `Output` | `string` | `"stderr"` | Destination (`"stdout"`, `"stderr"`, or a file path) |
| `LogClientIPs` | `bool` | `false` | Include client IPs in log entries |
| `LogOperations` | `bool` | `false` | Log each NFS operation with timing |
| `LogFileAccess` | `bool` | `false` | Log file open/close patterns |

## Example: Custom Configuration

```go
nfs, err := absnfs.New(fs, absnfs.ExportOptions{
	ReadOnly:         true,
	Secure:           true,
	AllowedIPs:       []string{"192.168.1.0/24", "10.0.0.5"},
	Squash:           "root",
	TransferSize:     131072,
	AttrCacheTimeout: 10 * time.Second,
	EnableDirCache:   true,
	MaxConnections:   50,
	Timeouts: &absnfs.TimeoutConfig{
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 120 * time.Second,
	},
	Log: &absnfs.LogConfig{
		Level:  "info",
		Format: "json",
		Output: "/var/log/nfs.log",
	},
})
```
