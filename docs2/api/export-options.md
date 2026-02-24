# ExportOptions

Defined in `options.go`. The user-facing configuration struct passed to `New()`. Internally split into `TuningOptions` (performance) and `PolicyOptions` (security) for runtime updates.

```go
type ExportOptions struct {
    // Security / Policy
    ReadOnly           bool
    Secure             bool
    AllowedIPs         []string
    Squash             string
    MaxFileSize        int64
    EnableRateLimiting bool
    RateLimitConfig    *RateLimiterConfig
    TLS                *TLSConfig

    // Performance / Tuning
    Async                bool
    TransferSize         int
    AttrCacheTimeout     time.Duration
    AttrCacheSize        int
    CacheNegativeLookups bool
    NegativeCacheTimeout time.Duration
    EnableDirCache       bool
    DirCacheTimeout      time.Duration
    DirCacheMaxEntries   int
    DirCacheMaxDirSize   int
    MaxWorkers           int
    MaxConnections       int
    IdleTimeout          time.Duration
    TCPKeepAlive         bool
    TCPNoDelay           bool
    SendBufferSize       int
    ReceiveBufferSize    int

    // Logging and Timeouts
    Log      *LogConfig
    Timeouts *TimeoutConfig
}
```

## Security Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `ReadOnly` | `bool` | `false` | Reject all write operations |
| `Secure` | `bool` | `false` | Require privileged source ports (< 1024) |
| `AllowedIPs` | `[]string` | `nil` (allow all) | IP addresses or CIDR subnets permitted to connect |
| `Squash` | `string` | `""` (none) | UID/GID mapping: `"root"`, `"all"`, or `"none"` |
| `MaxFileSize` | `int64` | `0` | Maximum file size in bytes (0 = unlimited) |
| `EnableRateLimiting` | `bool` | `true` (see note) | Enable per-IP and global rate limiting |
| `RateLimitConfig` | `*RateLimiterConfig` | default config | Detailed rate limiting parameters |
| `TLS` | `*TLSConfig` | `nil` (disabled) | TLS/mTLS configuration |

**Note on EnableRateLimiting:** When `RateLimitConfig` is nil (the zero-value case), `New()` sets `EnableRateLimiting = true` and creates a default config. To disable rate limiting, explicitly set `EnableRateLimiting: false` and provide a non-nil `RateLimitConfig`.

### Squash Modes

| Value | Behavior |
|-------|----------|
| `"none"` or `""` | No mapping. Client UIDs/GIDs used as-is. |
| `"root"` | UID 0 mapped to nobody (65534). GID 0 also squashed. |
| `"all"` | All UIDs/GIDs mapped to nobody (65534). |

Squash mode cannot be changed at runtime. Attempting to change it via `UpdatePolicyOptions` or `UpdateExportOptions` returns an error.

## Transfer and I/O Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `TransferSize` | `int` | `65536` (64 KB) | Max bytes per read/write RPC |
| `Async` | `bool` | `false` | Allow async (unstable) writes |
| `SendBufferSize` | `int` | `262144` (256 KB) | TCP send buffer size |
| `ReceiveBufferSize` | `int` | `262144` (256 KB) | TCP receive buffer size |

## Cache Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `AttrCacheTimeout` | `time.Duration` | `5s` | TTL for cached file attributes |
| `AttrCacheSize` | `int` | `10000` | Max entries in the attribute cache (LRU) |
| `CacheNegativeLookups` | `bool` | `false` | Cache "file not found" results |
| `NegativeCacheTimeout` | `time.Duration` | `5s` | TTL for negative cache entries |
| `EnableDirCache` | `bool` | `false` | Cache directory listings |
| `DirCacheTimeout` | `time.Duration` | `10s` | TTL for cached directory entries |
| `DirCacheMaxEntries` | `int` | `1000` | Max directories in cache |
| `DirCacheMaxDirSize` | `int` | `10000` | Max entries per directory before skipping cache |

## Connection Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `MaxWorkers` | `int` | `runtime.NumCPU() * 4` | Worker pool goroutines |
| `MaxConnections` | `int` | `100` | Simultaneous client connections (0 = unlimited) |
| `IdleTimeout` | `time.Duration` | `5m` | Close connections idle longer than this |
| `TCPKeepAlive` | `bool` | `true` | Enable TCP keep-alive probes |
| `TCPNoDelay` | `bool` | `true` | Disable Nagle's algorithm |

## TimeoutConfig

Passed via `ExportOptions.Timeouts`. When nil, `New()` populates all fields with defaults.

```go
type TimeoutConfig struct {
    ReadTimeout    time.Duration // default: 30s
    WriteTimeout   time.Duration // default: 60s
    LookupTimeout  time.Duration // default: 10s
    ReaddirTimeout time.Duration // default: 30s
    CreateTimeout  time.Duration // default: 15s
    RemoveTimeout  time.Duration // default: 15s
    RenameTimeout  time.Duration // default: 20s
    HandleTimeout  time.Duration // default: 5s
    DefaultTimeout time.Duration // default: 30s
}
```

## LogConfig

Passed via `ExportOptions.Log`. When nil, a no-op logger is used.

```go
type LogConfig struct {
    Level        string // "debug", "info", "warn", "error" (default: "info")
    Format       string // "json" or "text" (default: "text")
    Output       string // "stdout", "stderr", or file path (default: "stderr")
    LogClientIPs bool   // Include client IPs in logs (default: false)
    LogOperations bool  // Log each NFS operation with timing (default: false)
    LogFileAccess bool  // Log file open/close patterns (default: false)
    MaxSize      int    // Max log file MB before rotation (reserved, default: 100)
    MaxBackups   int    // Old log files to retain (reserved, default: 3)
    MaxAge       int    // Days to retain old logs (reserved, default: 28)
    Compress     bool   // Gzip rotated files (reserved, default: false)
}
```

**Note:** The `MaxSize`, `MaxBackups`, `MaxAge`, and `Compress` fields are reserved for future log rotation support. They are accepted but have no effect.

## RateLimiterConfig

Passed via `ExportOptions.RateLimitConfig`. Default values from `DefaultRateLimiterConfig()`:

```go
type RateLimiterConfig struct {
    GlobalRequestsPerSecond        int           // default: 10000
    PerIPRequestsPerSecond         int           // default: 1000
    PerIPBurstSize                 int           // default: 500
    PerConnectionRequestsPerSecond int           // default: 500
    PerConnectionBurstSize         int           // default: 100
    ReadLargeOpsPerSecond          int           // default: 100
    WriteLargeOpsPerSecond         int           // default: 50
    ReaddirOpsPerSecond            int           // default: 50
    MountOpsPerMinute              int           // default: 10
    FileHandlesPerIP               int           // default: 10000
    FileHandlesGlobal              int           // default: 1000000
    CleanupInterval                time.Duration // default: 5m
}
```

## TLSConfig

Passed via `ExportOptions.TLS`. When nil, connections are unencrypted.

```go
type TLSConfig struct {
    Enabled                  bool
    CertFile                 string           // PEM server certificate path
    KeyFile                  string           // PEM server key path
    CAFile                   string           // PEM CA certificate for client verification
    ClientAuth               tls.ClientAuthType
    MinVersion               uint16           // default: TLS 1.2
    MaxVersion               uint16           // default: TLS 1.3
    CipherSuites             []uint16         // empty = secure defaults
    PreferServerCipherSuites bool
    InsecureSkipVerify       bool             // testing only
}
```

## Example: Custom Configuration

```go
server, err := absnfs.New(fs, absnfs.ExportOptions{
    ReadOnly:   true,
    AllowedIPs: []string{"192.168.1.0/24", "10.0.0.5"},
    Squash:     "root",

    TransferSize:     131072, // 128 KB
    AttrCacheTimeout: 10 * time.Second,
    AttrCacheSize:    20000,
    EnableDirCache:   true,

    MaxConnections: 50,
    MaxWorkers:     32,
    IdleTimeout:    10 * time.Minute,

    Log: &absnfs.LogConfig{
        Level:         "info",
        Format:        "json",
        Output:        "/var/log/nfs.log",
        LogOperations: true,
    },

    Timeouts: &absnfs.TimeoutConfig{
        ReadTimeout:  60 * time.Second,
        WriteTimeout: 120 * time.Second,
    },
})
```
