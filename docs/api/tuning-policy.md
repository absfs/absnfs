# TuningOptions, PolicyOptions, and Runtime Updates

Defined in `options.go`. ExportOptions is split internally into two categories for safe runtime updates:

- **TuningOptions** -- performance settings. Stale reads only affect throughput. Updated via atomic swap.
- **PolicyOptions** -- security settings. Stale reads could violate access control. Updated via drain-and-swap.

## TuningOptions

```go
type TuningOptions struct {
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
    TCPNoDelay            bool
    SendBufferSize       int
    ReceiveBufferSize    int
    Async                bool
    Log                  *LogConfig
    Timeouts             *TimeoutConfig
}
```

Fields match the corresponding `ExportOptions` fields. See [ExportOptions](export-options.md) for descriptions and defaults.

## PolicyOptions

```go
type PolicyOptions struct {
    ReadOnly           bool
    Secure             bool
    AllowedIPs         []string
    Squash             string
    MaxFileSize        int64
    EnableRateLimiting bool
    RateLimitConfig    *RateLimiterConfig
    TLS                *TLSConfig
}
```

**Immutable at runtime:** `Squash` cannot be changed after `New()`. Attempting to change it returns an error.

## RequestOptions

A per-request snapshot created at the start of each RPC call. Holds pointers to the current TuningOptions and PolicyOptions at the time the request was accepted.

```go
type RequestOptions struct {
    Tuning *TuningOptions
    Policy *PolicyOptions
}
```

This ensures a single request sees a consistent set of options throughout its lifetime, even if options change concurrently.

## UpdateTuningOptions

```go
func (n *AbsfsNFS) UpdateTuningOptions(fn func(*TuningOptions))
```

Applies a mutation function to a copy of the current tuning options, then stores the result atomically. No drain is needed because stale tuning reads only affect performance.

The mutation function receives a deep copy -- pointer fields (`Log`, `Timeouts`) are cloned before the function is called.

After the swap, side effects are applied automatically:
- Attribute cache resized if `AttrCacheSize` changed
- Attribute cache TTL updated if `AttrCacheTimeout` changed
- Negative caching reconfigured if `CacheNegativeLookups` or `NegativeCacheTimeout` changed
- Directory cache resized if `DirCacheMaxEntries` changed
- Directory cache TTL updated if `DirCacheTimeout` changed
- Worker pool resized if `MaxWorkers` changed
- Structured logger replaced if `Log` changed

```go
server.UpdateTuningOptions(func(t *absnfs.TuningOptions) {
    t.AttrCacheSize = 20000
    t.TransferSize = 131072
    t.MaxWorkers = 64
})
```

## UpdatePolicyOptions

```go
func (n *AbsfsNFS) UpdatePolicyOptions(newPolicy PolicyOptions) error
```

Replaces the current policy using drain-and-swap:

1. Acquires the policy write lock (`policyRWMu.Lock()`), which blocks until all in-flight requests finish. New requests that arrive during the drain receive `NFSERR_JUKEBOX`, causing NFS clients to retry.
2. Deep-copies and stores the new PolicyOptions atomically.
3. Updates the rate limiter if rate limiting settings changed.
4. Releases the write lock, resuming normal request processing.

Returns an error if `Squash` differs from the current value (Squash is immutable at runtime).

```go
err := server.UpdatePolicyOptions(absnfs.PolicyOptions{
    ReadOnly:   true,
    AllowedIPs: []string{"10.0.0.0/8"},
    Squash:     currentPolicy.Squash, // must match current
})
```

## UpdateExportOptions

```go
func (n *AbsfsNFS) UpdateExportOptions(newOptions ExportOptions) error
```

Convenience method that splits an ExportOptions into tuning and policy changes, then applies each through the appropriate path:

1. Tuning fields are applied immediately via `UpdateTuningOptions`.
2. Policy fields are applied via `UpdatePolicyOptions` (drain-and-swap).

If `newOptions.Timeouts` or `newOptions.Log` is nil, the current values are preserved (not zeroed).

Returns an error if the Squash mode differs from the current value.

## GetExportOptions

```go
func (n *AbsfsNFS) GetExportOptions() ExportOptions
```

Returns a snapshot of the current configuration by recombining the current TuningOptions and PolicyOptions into an ExportOptions. Thread-safe.

## Drain-and-Swap Explained

The policy update mechanism ensures no request ever sees a half-applied security change:

```
Normal operation:
  HandleCall() acquires policyRWMu.RLock()
  ... processes request with current policy snapshot ...
  HandleCall() releases policyRWMu.RUnlock()

Policy update:
  UpdatePolicyOptions() acquires policyRWMu.Lock()
    -> blocks until all RLock holders (in-flight requests) finish
    -> new HandleCall() attempts TryRLock(), fails, returns NFSERR_JUKEBOX
  ... swaps policy atomically ...
  UpdatePolicyOptions() releases policyRWMu.Unlock()
    -> new requests proceed with the updated policy
```

NFS clients handle `NFSERR_JUKEBOX` by retrying after a short delay, so the drain is transparent to clients.
