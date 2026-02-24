# MetricsCollector

Internal metrics collection for operation counts, latencies, errors, cache hit rates, and connection tracking.

## Types

### NFSMetrics

```go
type NFSMetrics struct {
    // Operation counts
    TotalOperations   uint64
    ReadOperations    uint64
    WriteOperations   uint64
    LookupOperations  uint64
    GetAttrOperations uint64
    CreateOperations  uint64
    RemoveOperations  uint64
    RenameOperations  uint64
    MkdirOperations   uint64
    RmdirOperations   uint64
    ReaddirOperations uint64
    AccessOperations  uint64

    // Latency metrics
    AvgReadLatency  time.Duration
    AvgWriteLatency time.Duration
    MaxReadLatency  time.Duration
    MaxWriteLatency time.Duration
    P95ReadLatency  time.Duration
    P95WriteLatency time.Duration

    // Cache metrics
    CacheHitRate         float64
    AttrCacheSize        int
    AttrCacheCapacity    int
    DirCacheHitRate      float64
    NegativeCacheSize    int
    NegativeCacheHitRate float64

    // Connection metrics
    ActiveConnections   int
    TotalConnections    uint64
    RejectedConnections uint64

    // TLS metrics
    TLSHandshakes          uint64
    TLSHandshakeFailures   uint64
    TLSClientCertProvided  uint64
    TLSClientCertValidated uint64
    TLSClientCertRejected  uint64
    TLSSessionReused       uint64
    TLSVersion12           uint64
    TLSVersion13           uint64

    // Error metrics
    ErrorCount        uint64
    AuthFailures      uint64
    AccessViolations  uint64
    StaleHandles      uint64
    ResourceErrors    uint64
    RateLimitExceeded uint64

    // Timeout metrics
    ReadTimeouts    uint64
    WriteTimeouts   uint64
    LookupTimeouts  uint64
    ReaddirTimeouts uint64
    CreateTimeouts  uint64
    RemoveTimeouts  uint64
    RenameTimeouts  uint64
    HandleTimeouts  uint64
    TotalTimeouts   uint64

    // Time-based metrics
    StartTime     time.Time
    UptimeSeconds int64
}
```

### MetricsCollector

```go
type MetricsCollector struct {
    // (unexported fields)
}
```

Maintains metrics state using atomic operations for counters and ring buffers for latency samples. Uses a two-lock scheme: `mutex` protects the metrics snapshot, `latencyMutex` protects P95/max latency values and the ring buffers. Lock ordering invariant: `mutex` before `latencyMutex`.

## Functions

### NewMetricsCollector

```go
func NewMetricsCollector(server *AbsfsNFS) *MetricsCollector
```

Creates a new collector with 1,000-entry ring buffers for latency tracking and health monitoring. Records `StartTime` at creation.

### GetMetrics

```go
func (m *MetricsCollector) GetMetrics() NFSMetrics
```

Returns a consistent snapshot of all metrics. Before returning:
- Updates cache size/capacity from the live `AttrCache`.
- Computes current uptime from `StartTime`.
- Acquires both locks for a consistent read of latency values.

### Operation Recording

```go
func (m *MetricsCollector) IncrementOperationCount(opType string)
```

Atomically increments the counter for the given operation type. Valid `opType` values: `"READ"`, `"WRITE"`, `"LOOKUP"`, `"GETATTR"`, `"CREATE"`, `"REMOVE"`, `"RENAME"`, `"MKDIR"`, `"RMDIR"`, `"READDIR"`, `"ACCESS"`.

### Latency Recording

```go
func (m *MetricsCollector) RecordLatency(opType string, duration time.Duration)
```

Records a latency sample for `"READ"` or `"WRITE"` operations into a ring buffer (capacity 1,000). Updates `MaxReadLatency`/`MaxWriteLatency`, computes running average, and calculates P95 when at least 20 samples exist.

### Error Recording

```go
func (m *MetricsCollector) RecordError(errorType string)
```

Increments the total error count and the specific error counter. Valid `errorType` values: `"AUTH"`, `"ACCESS"`, `"STALE"`, `"RESOURCE"`, `"RATELIMIT"`.

```go
func (m *MetricsCollector) RecordRateLimitExceeded()
```

Shorthand for recording a rate limit rejection.

### Timeout Recording

```go
func (m *MetricsCollector) RecordTimeout(opType string)
```

Increments total and per-operation timeout counters. Valid `opType` values: `"READ"`, `"WRITE"`, `"LOOKUP"`, `"READDIR"`, `"CREATE"`, `"REMOVE"`, `"RENAME"`, `"HANDLE"`.

### Connection Recording

```go
func (m *MetricsCollector) RecordConnection()
func (m *MetricsCollector) RecordConnectionClosed()
func (m *MetricsCollector) RecordRejectedConnection()
```

Track active, total, and rejected connection counts.

### TLS Recording

```go
func (m *MetricsCollector) RecordTLSHandshake()
func (m *MetricsCollector) RecordTLSHandshakeFailure()
func (m *MetricsCollector) RecordTLSClientCert(validated bool)
func (m *MetricsCollector) RecordTLSSessionReused()
func (m *MetricsCollector) RecordTLSVersion(version uint16)
```

Track TLS handshake outcomes, client certificate validation, session reuse, and protocol version (TLS 1.2 = `0x0303`, TLS 1.3 = `0x0304`).

### Cache Recording

```go
func (m *MetricsCollector) RecordAttrCacheHit()
func (m *MetricsCollector) RecordAttrCacheMiss()
func (m *MetricsCollector) RecordDirCacheHit()
func (m *MetricsCollector) RecordDirCacheMiss()
func (m *MetricsCollector) RecordNegativeCacheHit()
func (m *MetricsCollector) RecordNegativeCacheMiss()
```

Each call atomically increments the relevant counter and recomputes the hit rate as `hits / (hits + misses)`.

### Health Check

```go
func (m *MetricsCollector) IsHealthy() bool
```

Returns false if:
- The windowed error rate exceeds 50% (based on a 1,000-entry ring buffer of recent operation results).
- P95 read or write latency exceeds 5 seconds.

```go
func (m *MetricsCollector) RecordOperationResult(isError bool)
```

Records a success (`false`) or error (`true`) into the health tracking ring buffer.
