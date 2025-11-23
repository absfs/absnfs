# Rate Limiter API

The RateLimiter provides DoS protection and request throttling for the NFS server.

## Overview

The rate limiter uses a token bucket algorithm to control the rate of incoming requests per client IP address. This prevents resource exhaustion attacks and ensures fair resource allocation among clients.

## Configuration

Rate limiting is configured through the `ExportOptions` struct when creating an NFS server:

```go
options := absnfs.ExportOptions{
    RateLimitEnabled: true,           // Enable rate limiting
    RateLimitPerSecond: 100,          // Max requests per second per IP
    RateLimitBurst: 200,              // Burst capacity
}

server, err := absnfs.New(fs, options)
```

## Parameters

### RateLimitEnabled
- **Type**: `bool`
- **Default**: `false`
- **Description**: Enables or disables rate limiting

### RateLimitPerSecond
- **Type**: `int`
- **Default**: `100`
- **Description**: Maximum number of requests allowed per second per client IP
- **Recommended**: 100-1000 for normal use, lower for restricted access

### RateLimitBurst
- **Type**: `int`
- **Default**: `200`
- **Description**: Burst capacity allowing temporary spikes above the rate limit
- **Recommended**: 2x the RateLimitPerSecond value

## How It Works

The rate limiter uses a **token bucket algorithm**:

1. Each client IP address has its own bucket with a maximum capacity (burst size)
2. Tokens are added to the bucket at a fixed rate (requests per second)
3. Each request consumes one token
4. If no tokens are available, the request is rejected with `NFSERR_JUKEBOX`

### Token Bucket Characteristics

- **Refill Rate**: Tokens refill at the configured `RateLimitPerSecond` rate
- **Maximum Capacity**: Capped at `RateLimitBurst`
- **Request Cost**: Each NFS operation consumes 1 token

## Error Handling

When rate limit is exceeded:

- **NFS Status**: `NFSERR_JUKEBOX` (error code 10008)
- **Meaning**: "Server temporarily unable to service request"
- **Client Behavior**: Clients should retry after a short delay
- **Metrics**: Counter incremented in `RateLimitExceeded` metric

## Per-IP Tracking

Rate limits are enforced per client IP address:

```go
// Different IPs have independent rate limits
// Client 192.168.1.10 can make 100 req/sec
// Client 192.168.1.11 can also make 100 req/sec
```

### Cleanup

- Inactive client entries are automatically cleaned up
- Cleanup interval: Configurable (default: 5 minutes)
- Memory usage is bounded by active client count

## Example Usage

### Basic Rate Limiting

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
        RateLimitEnabled: true,
        RateLimitPerSecond: 100,
        RateLimitBurst: 200,
    }

    server, err := absnfs.New(fs, options)
    if err != nil {
        log.Fatal(err)
    }

    server.Export("/export/myfs")
}
```

### Conservative Rate Limiting

For restricted access or sensitive data:

```go
options := absnfs.ExportOptions{
    RateLimitEnabled: true,
    RateLimitPerSecond: 10,   // Very restrictive
    RateLimitBurst: 20,        // Small burst
    AllowedIPs: []string{"192.168.1.0/24"}, // Combined with IP restrictions
}
```

### High-Performance Configuration

For high-throughput scenarios:

```go
options := absnfs.ExportOptions{
    RateLimitEnabled: true,
    RateLimitPerSecond: 1000,  // High throughput
    RateLimitBurst: 2000,       // Large burst capacity
}
```

## Monitoring

Rate limit metrics are available through the metrics API:

```go
metrics := server.GetMetrics()
fmt.Printf("Rate limit violations: %d\n", metrics.RateLimitExceeded)
```

## Performance Considerations

### Memory Usage

- **Per-client overhead**: ~200 bytes per active client IP
- **Maximum clients**: Unbounded, but cleaned up when inactive
- **Recommended**: Monitor active client count in production

### CPU Impact

- **Token bucket operations**: O(1) per request
- **Cleanup operations**: O(n) where n is number of tracked IPs
- **Impact**: Minimal (<1% CPU overhead)

### Latency

- **Check latency**: <1 microsecond per request
- **Lock contention**: Minimal due to per-IP locking

## Best Practices

1. **Enable for public-facing servers**: Always use rate limiting for internet-exposed NFS servers
2. **Tune based on workload**: Adjust limits based on expected legitimate traffic
3. **Monitor violations**: Track `RateLimitExceeded` metric to detect attacks or misconfigurations
4. **Combine with IP restrictions**: Use both rate limiting and IP whitelisting for defense in depth
5. **Start conservative**: Begin with lower limits and increase based on monitoring

## Integration with Other Features

### IP Restrictions

Rate limiting works alongside IP restrictions:

```go
options := absnfs.ExportOptions{
    AllowedIPs: []string{"10.0.0.0/8"},  // Only allow internal network
    RateLimitEnabled: true,
    RateLimitPerSecond: 100,              // But still rate limit them
}
```

### Metrics

Rate limit violations are tracked in metrics:

```go
type NFSMetrics struct {
    // ...
    RateLimitExceeded uint64  // Total rate limit violations
}
```

### Worker Pool

Rate limiting occurs before worker pool allocation, preventing resource exhaustion.

## Troubleshooting

### Clients Getting Rate Limited

**Symptoms**: Clients receive `NFSERR_JUKEBOX` errors

**Solutions**:
1. Increase `RateLimitPerSecond`
2. Increase `RateLimitBurst` for bursty workloads
3. Check if client is making excessive requests
4. Verify legitimate traffic patterns

### Rate Limiting Not Working

**Check**:
1. `RateLimitEnabled` is set to `true`
2. `RateLimitPerSecond` is set to a reasonable value
3. Monitor `RateLimitExceeded` metric to verify it's tracking violations

## See Also

- [Security Guide](../guides/security.md)
- [Performance Tuning](../guides/performance-tuning.md)
- [Metrics API](./metrics.md)
- [Export Options API](./export-options.md)
