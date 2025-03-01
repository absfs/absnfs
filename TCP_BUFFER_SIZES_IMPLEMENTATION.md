# TCP Buffer Sizes Implementation

## Overview

This document describes the implementation of the TCP Buffer Sizes feature for ABSNFS. This feature enhances network performance by allowing configuration of TCP socket options, including buffer sizes, TCP keepalive, and TCP_NODELAY (Nagle's algorithm).

## Design

The implementation adds four new configuration options to the `ExportOptions` struct:

1. `TCPKeepAlive bool` - Enables TCP keep-alive probes to detect dead connections
2. `TCPNoDelay bool` - Disables Nagle's algorithm to reduce latency
3. `SendBufferSize int` - Size in bytes for the TCP send buffer
4. `ReceiveBufferSize int` - Size in bytes for the TCP receive buffer

These options are set with sensible defaults and applied to each new TCP connection.

## Implementation Details

### Configuration Options

The TCP configuration options were added to the `ExportOptions` struct in `types.go`:

```go
// TCP socket configuration options
TCPKeepAlive bool
TCPNoDelay bool
SendBufferSize int
ReceiveBufferSize int
```

Default values are set in the `New()` function:

```go
// Set TCP socket options defaults
options.TCPKeepAlive = true // Default: enabled
options.TCPNoDelay = true   // Default: enabled

if options.SendBufferSize <= 0 {
    options.SendBufferSize = 262144 // Default: 256KB
}

if options.ReceiveBufferSize <= 0 {
    options.ReceiveBufferSize = 262144 // Default: 256KB
}
```

### Application of TCP Options

TCP options are applied to each new connection in the `acceptLoop` method of the `Server` type in `server.go`:

```go
// Configure TCP connection options if this is a TCP connection
if tcpConn, ok := conn.(*net.TCPConn); ok && s.handler != nil {
    // Apply TCP keepalive setting
    if s.handler.options.TCPKeepAlive {
        tcpConn.SetKeepAlive(true)
        tcpConn.SetKeepAlivePeriod(60 * time.Second) // Standard keepalive period
    }
    
    // Apply TCP no delay setting (disable Nagle's algorithm)
    if s.handler.options.TCPNoDelay {
        tcpConn.SetNoDelay(true)
    }
    
    // Apply buffer sizes
    if s.handler.options.SendBufferSize > 0 {
        tcpConn.SetWriteBuffer(s.handler.options.SendBufferSize)
    }
    
    if s.handler.options.ReceiveBufferSize > 0 {
        tcpConn.SetReadBuffer(s.handler.options.ReceiveBufferSize)
    }
}
```

### Testing

Tests were added to verify:
1. Default values are correctly applied
2. Custom values are respected
3. Server functions correctly with TCP options configured

The tests are in `tcp_options_test.go`.

## Performance Impact

Configuring TCP buffer sizes can have a significant impact on performance:

- **Larger buffer sizes**: Improve throughput for large transfers but consume more memory
- **TCP keepalive**: Helps detect and clean up stale connections
- **TCP_NODELAY**: Reduces latency for small requests at the cost of potentially increased bandwidth usage

Fine-tuning these parameters based on the specific workload can yield significant performance improvements:

- For large file transfers, larger buffer sizes (e.g., 1MB) can improve throughput
- For interactive workloads with many small requests, enabling TCP_NODELAY can reduce latency
- For environments with unstable connections, enabling TCP keepalive helps detect connection issues

## Future Improvements

Potential enhancements to consider in the future:

1. Dynamic buffer size adjustment based on connection characteristics
2. More granular control of TCP keepalive parameters (interval, count)
3. Per-export or per-client TCP configuration
4. Metrics to track effectiveness of TCP settings

## Conclusion

The TCP Buffer Sizes feature provides important network performance tuning options for ABSNFS, allowing administrators to optimize network performance for their specific workloads and network conditions.