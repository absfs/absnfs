# RateLimiter

Multi-layer rate limiting for NFS request throttling and DoS protection.

## Types

### RateLimiterConfig

```go
type RateLimiterConfig struct {
    // Global limits
    GlobalRequestsPerSecond int // Max requests/sec across all clients (default: 10,000)

    // Per-IP limits
    PerIPRequestsPerSecond int // Max requests/sec per IP (default: 1,000)
    PerIPBurstSize         int // Burst allowance per IP (default: 500)

    // Per-connection limits
    PerConnectionRequestsPerSecond int // Max requests/sec per connection (default: 500)
    PerConnectionBurstSize         int // Burst allowance per connection (default: 100)

    // Per-operation type limits
    ReadLargeOpsPerSecond  int // Large reads (>64KB)/sec per IP (default: 100)
    WriteLargeOpsPerSecond int // Large writes (>64KB)/sec per IP (default: 50)
    ReaddirOpsPerSecond    int // READDIR ops/sec per IP (default: 50)

    // Mount operation limits
    MountOpsPerMinute int // MOUNT ops/min per IP (default: 10)

    // File handle limits
    FileHandlesPerIP  int // Max handles per IP (default: 10,000)
    FileHandlesGlobal int // Max handles globally (default: 1,000,000)

    // Cleanup
    CleanupInterval time.Duration // Interval for pruning inactive entries (default: 5min)
}
```

### RateLimiter

```go
type RateLimiter struct {
    // (unexported fields)
}
```

Coordinates four rate limiting layers: global, per-IP, per-connection, and per-operation.

### OperationType

```go
type OperationType string

const (
    OpTypeReadLarge  OperationType = "read_large"
    OpTypeWriteLarge OperationType = "write_large"
    OpTypeReaddir    OperationType = "readdir"
    OpTypeMount      OperationType = "mount"
)
```

## Functions

### DefaultRateLimiterConfig

```go
func DefaultRateLimiterConfig() RateLimiterConfig
```

Returns a `RateLimiterConfig` with sensible defaults for general NFS workloads. Burst sizes are tuned for NFS client mount sequences which generate request bursts.

### NewRateLimiter

```go
func NewRateLimiter(config RateLimiterConfig) *RateLimiter
```

Creates a rate limiter with the given configuration. Initializes:
- A global token bucket
- A per-IP token bucket manager
- A per-operation token bucket manager

Per-connection limiters are created lazily on first request.

### AllowRequest

```go
func (rl *RateLimiter) AllowRequest(ip string, connID string) bool
```

Checks whether a request should be allowed. Tests three layers in order, short-circuiting on first rejection:

1. **Global limit** -- shared across all clients.
2. **Per-IP limit** -- per source IP address.
3. **Per-connection limit** -- per individual TCP connection (created lazily).

### AllowOperation

```go
func (rl *RateLimiter) AllowOperation(ip string, opType OperationType) bool
```

Checks whether a specific operation type should be allowed for the given IP. Used for fine-grained control over expensive operations (large reads/writes, directory listings, mounts).

### AllocateFileHandle

```go
func (rl *RateLimiter) AllocateFileHandle(ip string) bool
```

Attempts to allocate a file handle for an IP address. Checks both the global handle limit and per-IP handle limit. Returns false if either limit is reached.

### ReleaseFileHandle

```go
func (rl *RateLimiter) ReleaseFileHandle(ip string)
```

Releases a file handle allocation, decrementing both the global and per-IP counters.

### CleanupConnection

```go
func (rl *RateLimiter) CleanupConnection(connID string)
```

Removes the per-connection rate limiter for a closed connection. Called automatically when a connection's handling loop exits.

### GetStats

```go
func (rl *RateLimiter) GetStats() map[string]interface{}
```

Returns current rate limiter statistics including global token count, per-IP token counts, and global file handle count.

## Token Bucket Implementation

The underlying `TokenBucket` type uses a standard token bucket algorithm:
- Tokens refill continuously at a configured rate.
- Each allowed request consumes one token.
- Burst capacity determines the maximum stored tokens.
- `Allow()` refills based on elapsed time, then checks and consumes.
- `AllowN(n int)` consumes `n` tokens at once.

The `PerIPLimiter` manages one `TokenBucket` per IP with periodic cleanup (bounded to 100 deletions per pass) of fully-replenished buckets.

## Sliding Window

Mount operations use a `SlidingWindow` rate limiter that counts requests within a time window rather than using token buckets, providing stricter per-minute enforcement.
