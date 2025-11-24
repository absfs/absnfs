# Rate Limiter API

The RateLimiter provides comprehensive DoS protection and request throttling for the NFS server through multiple layers of rate limiting.

## Overview

The rate limiter uses token bucket algorithms to control request rates at multiple levels:
- Global request limits across all clients
- Per-IP request limits
- Per-connection request limits
- Per-operation-type limits (large reads/writes, READDIR, MOUNT)
- File handle limits (per-IP and global)

## Configuration

Rate limiting is configured through two fields in the `ExportOptions` struct:

```go
options := absnfs.ExportOptions{
    EnableRateLimiting: true,
    RateLimitConfig: &absnfs.RateLimiterConfig{
        PerIPRequestsPerSecond: 1000,
        PerIPBurstSize: 100,
        // ... other fields
    },
}

server, err := absnfs.New(fs, options)
```

## RateLimiterConfig Fields

### Global Limits

#### GlobalRequestsPerSecond
- **Type**: `int`
- **Default**: `10000`
- **Description**: Maximum requests per second across all clients
- **Purpose**: Prevents total server resource exhaustion

### Per-IP Limits

#### PerIPRequestsPerSecond
- **Type**: `int`
- **Default**: `1000`
- **Description**: Maximum requests per second per client IP
- **Purpose**: Prevents individual clients from monopolizing server resources

#### PerIPBurstSize
- **Type**: `int`
- **Default**: `100`
- **Description**: Burst capacity allowing temporary spikes above the per-IP rate limit
- **Purpose**: Accommodates legitimate bursty workloads

### Per-Connection Limits

#### PerConnectionRequestsPerSecond
- **Type**: `int`
- **Default**: `100`
- **Description**: Maximum requests per second per TCP connection
- **Purpose**: Prevents single connection from exhausting per-IP quota

#### PerConnectionBurstSize
- **Type**: `int`
- **Default**: `10`
- **Description**: Burst capacity for per-connection limits
- **Purpose**: Allows brief spikes in connection activity

### Per-Operation-Type Limits

#### ReadLargeOpsPerSecond
- **Type**: `int`
- **Default**: `100`
- **Description**: Large read operations (>64KB) per second per IP
- **Purpose**: Prevents bandwidth exhaustion from read operations

#### WriteLargeOpsPerSecond
- **Type**: `int`
- **Default**: `50`
- **Description**: Large write operations (>64KB) per second per IP
- **Purpose**: Protects against write-intensive DoS attacks

#### ReaddirOpsPerSecond
- **Type**: `int`
- **Default**: `20`
- **Description**: READDIR operations per second per IP
- **Purpose**: Prevents directory enumeration attacks

#### MountOpsPerMinute
- **Type**: `int`
- **Default**: `10`
- **Description**: MOUNT operations per minute per IP
- **Purpose**: Prevents mount/unmount storms

### File Handle Limits

#### FileHandlesPerIP
- **Type**: `int`
- **Default**: `10000`
- **Description**: Maximum file handles a single IP can have open
- **Purpose**: Prevents per-IP file handle exhaustion

#### FileHandlesGlobal
- **Type**: `int`
- **Default**: `1000000`
- **Description**: Maximum file handles globally across all clients
- **Purpose**: Prevents total file handle exhaustion

### Cleanup

#### CleanupInterval
- **Type**: `time.Duration`
- **Default**: `5 * time.Minute`
- **Description**: How often to cleanup inactive rate limiter entries
- **Purpose**: Prevents memory leaks from tracking inactive clients

## Default Configuration

The `DefaultRateLimiterConfig()` function returns sensible defaults:

```go
config := absnfs.DefaultRateLimiterConfig()
// Returns:
// {
//     GlobalRequestsPerSecond: 10000,
//     PerIPRequestsPerSecond: 1000,
//     PerIPBurstSize: 100,
//     PerConnectionRequestsPerSecond: 100,
//     PerConnectionBurstSize: 10,
//     ReadLargeOpsPerSecond: 100,
//     WriteLargeOpsPerSecond: 50,
//     ReaddirOpsPerSecond: 20,
//     MountOpsPerMinute: 10,
//     FileHandlesPerIP: 10000,
//     FileHandlesGlobal: 1000000,
//     CleanupInterval: 5 * time.Minute,
// }
```

## Usage Examples

### Basic Configuration

Enable rate limiting with default settings:

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
        EnableRateLimiting: true,
        RateLimitConfig: nil, // Uses defaults
    }

    server, err := absnfs.New(fs, options)
    if err != nil {
        log.Fatal(err)
    }

    server.Export("/export/myfs")
}
```

### Custom Configuration

Customize specific limits:

```go
options := absnfs.ExportOptions{
    EnableRateLimiting: true,
    RateLimitConfig: &absnfs.RateLimiterConfig{
        GlobalRequestsPerSecond: 5000,
        PerIPRequestsPerSecond: 500,
        PerIPBurstSize: 50,
        PerConnectionRequestsPerSecond: 100,
        PerConnectionBurstSize: 10,
        ReadLargeOpsPerSecond: 50,
        WriteLargeOpsPerSecond: 25,
        ReaddirOpsPerSecond: 10,
        MountOpsPerMinute: 5,
        FileHandlesPerIP: 5000,
        FileHandlesGlobal: 500000,
        CleanupInterval: 10 * time.Minute,
    },
}
```

### Conservative Configuration

For restricted access or sensitive data:

```go
options := absnfs.ExportOptions{
    EnableRateLimiting: true,
    RateLimitConfig: &absnfs.RateLimiterConfig{
        GlobalRequestsPerSecond: 1000,
        PerIPRequestsPerSecond: 100,
        PerIPBurstSize: 10,
        PerConnectionRequestsPerSecond: 50,
        PerConnectionBurstSize: 5,
        ReadLargeOpsPerSecond: 10,
        WriteLargeOpsPerSecond: 5,
        ReaddirOpsPerSecond: 2,
        MountOpsPerMinute: 2,
        FileHandlesPerIP: 1000,
        FileHandlesGlobal: 100000,
        CleanupInterval: 5 * time.Minute,
    },
    AllowedIPs: []string{"192.168.1.0/24"},
}
```

### High-Throughput Configuration

For high-performance scenarios:

```go
options := absnfs.ExportOptions{
    EnableRateLimiting: true,
    RateLimitConfig: &absnfs.RateLimiterConfig{
        GlobalRequestsPerSecond: 50000,
        PerIPRequestsPerSecond: 5000,
        PerIPBurstSize: 500,
        PerConnectionRequestsPerSecond: 1000,
        PerConnectionBurstSize: 100,
        ReadLargeOpsPerSecond: 500,
        WriteLargeOpsPerSecond: 250,
        ReaddirOpsPerSecond: 100,
        MountOpsPerMinute: 50,
        FileHandlesPerIP: 50000,
        FileHandlesGlobal: 5000000,
        CleanupInterval: 5 * time.Minute,
    },
}
```

### Partial Configuration

Override only specific values (others use defaults):

```go
// Start with defaults
config := absnfs.DefaultRateLimiterConfig()

// Customize specific fields
config.PerIPRequestsPerSecond = 2000
config.ReadLargeOpsPerSecond = 200
config.WriteLargeOpsPerSecond = 100

options := absnfs.ExportOptions{
    EnableRateLimiting: true,
    RateLimitConfig: &config,
}
```

## How It Works

### Multi-Layer Rate Limiting

Requests pass through multiple layers of rate limiting in order:

1. **Global Rate Limit**: Checks `GlobalRequestsPerSecond` limit
2. **Per-IP Rate Limit**: Checks `PerIPRequestsPerSecond` limit for the client's IP
3. **Per-Connection Rate Limit**: Checks `PerConnectionRequestsPerSecond` for the specific connection
4. **Per-Operation Rate Limit**: For specific operations, checks operation-type limits

If any layer rejects the request, it is denied with `NFSERR_JUKEBOX`.

### Token Bucket Algorithm

Each rate limiter uses a token bucket algorithm:

1. Each bucket has a maximum capacity (burst size)
2. Tokens are added at a fixed rate (requests per second)
3. Each request consumes one token
4. If no tokens are available, the request is rejected

### Token Bucket Characteristics

- **Refill Rate**: Tokens refill continuously at the configured rate
- **Maximum Capacity**: Capped at the configured burst size
- **Request Cost**: Each NFS operation consumes 1 token
- **Graceful Degradation**: Clients receive `NFSERR_JUKEBOX` and can retry

### Operation-Specific Limiting

Certain operations have additional limits:

- **Large Reads** (>64KB): Limited by `ReadLargeOpsPerSecond`
- **Large Writes** (>64KB): Limited by `WriteLargeOpsPerSecond`
- **READDIR**: Limited by `ReaddirOpsPerSecond`
- **MOUNT**: Limited by `MountOpsPerMinute` (converted to per-second rate)

### File Handle Limiting

File handle allocation is tracked separately:

- **Per-IP Limit**: Each IP can have up to `FileHandlesPerIP` handles open
- **Global Limit**: Total handles across all IPs limited to `FileHandlesGlobal`
- **Allocation**: Checked when opening files
- **Release**: Decremented when closing files

### Memory Management

Rate limiters are cleaned up automatically:

- Cleanup runs every `CleanupInterval`
- Removes entries that are at max capacity (inactive clients)
- Prevents memory leaks from tracking many transient clients

## Error Handling

When rate limit is exceeded:

- **NFS Status**: `NFSERR_JUKEBOX` (error code 10008)
- **Meaning**: "Server temporarily unable to service request"
- **Client Behavior**: Clients should retry after a short delay
- **Metrics**: Counter incremented in `RateLimitExceeded` metric

## Performance Considerations

### Memory Usage

- **Per-client overhead**: ~200-500 bytes per active client IP
- **Per-connection overhead**: ~200 bytes per active connection
- **Maximum clients**: Unbounded, but cleaned up when inactive
- **Cleanup**: Runs every `CleanupInterval` to free memory

### CPU Impact

- **Token bucket operations**: O(1) per request
- **Cleanup operations**: O(n) where n is number of tracked IPs
- **Lock contention**: Minimal due to per-IP and per-connection locking
- **Overall impact**: Typically <1% CPU overhead

### Latency

- **Check latency**: <1 microsecond per layer
- **Total latency**: <5 microseconds for all layers
- **Lock contention**: Minimal due to fine-grained locking

## Best Practices

1. **Enable for public-facing servers**: Always use rate limiting for internet-exposed NFS servers
2. **Start with defaults**: Begin with `DefaultRateLimiterConfig()` and adjust based on monitoring
3. **Monitor violations**: Track `RateLimitExceeded` metric to detect attacks or misconfigurations
4. **Combine with IP restrictions**: Use both rate limiting and IP whitelisting for defense in depth
5. **Tune per-operation limits**: Adjust operation-specific limits based on workload characteristics
6. **Balance burst sizes**: Set burst sizes to 10-20% of rate limits for bursty workloads
7. **Monitor file handles**: Track file handle usage to prevent exhaustion

## Monitoring

Rate limit statistics are available through the `GetStats()` method:

```go
stats := rateLimiter.GetStats()
fmt.Printf("Global tokens: %v\n", stats["global_tokens"])
fmt.Printf("Per-IP stats: %v\n", stats["per_ip_stats"])
fmt.Printf("File handles: %v\n", stats["file_handles_global"])
```

Server metrics also track rate limit violations:

```go
metrics := server.GetMetrics()
fmt.Printf("Rate limit violations: %d\n", metrics.RateLimitExceeded)
```

## Integration with Other Features

### IP Restrictions

Rate limiting works alongside IP restrictions:

```go
options := absnfs.ExportOptions{
    AllowedIPs: []string{"10.0.0.0/8"},
    EnableRateLimiting: true,
    RateLimitConfig: &absnfs.RateLimiterConfig{
        PerIPRequestsPerSecond: 1000,
        PerIPBurstSize: 100,
    },
}
```

### Worker Pool

Rate limiting occurs before worker pool allocation, preventing resource exhaustion from queued requests.

### Metrics

Rate limit violations are tracked in metrics:

```go
type NFSMetrics struct {
    // ...
    RateLimitExceeded uint64  // Total rate limit violations
}
```

## Troubleshooting

### Clients Getting Rate Limited

**Symptoms**: Clients receive `NFSERR_JUKEBOX` errors frequently

**Solutions**:
1. Check which limit is being hit (global, per-IP, per-connection, or per-operation)
2. Increase the relevant `*PerSecond` values
3. Increase burst sizes for bursty workloads
4. Verify client request patterns are legitimate
5. Check for misconfigured clients making excessive requests

### File Handle Exhaustion

**Symptoms**: Clients can't open new files, receive errors

**Solutions**:
1. Increase `FileHandlesPerIP` for legitimate clients with many open files
2. Increase `FileHandlesGlobal` if many clients are active
3. Check for clients leaking file handles (not closing files)
4. Monitor file handle metrics to identify problematic clients

### Rate Limiting Not Working

**Check**:
1. `EnableRateLimiting` is set to `true` in `ExportOptions`
2. `RateLimitConfig` is not nil (or defaults are acceptable)
3. Monitor `RateLimitExceeded` metric to verify violations are tracked
4. Check logs for rate limiter initialization

### Memory Usage Growing

**Symptoms**: Server memory usage grows over time

**Solutions**:
1. Reduce `CleanupInterval` to cleanup more frequently
2. Check if many transient clients are connecting
3. Monitor number of tracked IPs/connections
4. Verify cleanup is running (check logs)

## Advanced Topics

### Custom Rate Limiter Implementation

For advanced use cases, you can implement custom rate limiting logic by:

1. Implementing your own token bucket or sliding window
2. Using the provided `TokenBucket` or `SlidingWindow` types
3. Extending `RateLimiter` with custom logic

### Connection-Specific Limits

Per-connection limits prevent a single client from opening many connections to bypass per-IP limits:

```go
config := absnfs.RateLimiterConfig{
    PerIPRequestsPerSecond: 1000,
    PerConnectionRequestsPerSecond: 100,  // Each connection limited to 100 req/s
}
```

A client opening 20 connections is still limited to 1000 req/s total (per-IP limit), but each connection can only do 100 req/s.

### Operation-Type Limiting

Different operation types can have different performance characteristics:

- **Large Reads**: Bandwidth-intensive
- **Large Writes**: Bandwidth and I/O intensive
- **READDIR**: Metadata-intensive, potential for enumeration attacks
- **MOUNT**: Connection overhead, potential for connection storms

Tune these independently based on your workload.

## See Also

- [Security Guide](../guides/security.md)
- [Performance Tuning](../guides/performance-tuning.md)
- [Metrics API](./metrics.md)
- [Export Options API](./export-options.md)
