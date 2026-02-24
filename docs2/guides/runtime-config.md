# Runtime Configuration

absnfs supports changing configuration while the server is running. The update
mechanism depends on whether the setting is a **tuning** option or a **policy**
option.

## Tuning vs Policy

**Tuning options** affect performance only. A stale read of a tuning value is
harmless -- it might use an old cache size or worker count, but nothing breaks.
Tuning changes are applied immediately via atomic swap with no coordination.

**Policy options** affect security. A stale read could allow a connection from
a revoked IP or permit writes on a now-read-only export. Policy changes use
**drain-and-swap**: new requests are temporarily rejected (clients get
`NFSERR_JUKEBOX` and retry), in-flight requests finish, then the new policy
takes effect atomically.

### Tuning Fields

`TransferSize`, `AttrCacheTimeout`, `AttrCacheSize`, `CacheNegativeLookups`,
`NegativeCacheTimeout`, `EnableDirCache`, `DirCacheTimeout`,
`DirCacheMaxEntries`, `DirCacheMaxDirSize`, `MaxWorkers`, `MaxConnections`,
`IdleTimeout`, `TCPKeepAlive`, `TCPNoDelay`, `SendBufferSize`,
`ReceiveBufferSize`, `Async`, `Log`, `Timeouts`.

### Policy Fields

`ReadOnly`, `Secure`, `AllowedIPs`, `Squash` (immutable after creation),
`MaxFileSize`, `EnableRateLimiting`, `RateLimitConfig`, `TLS`.

### Immutable Fields

`Squash` cannot be changed at runtime. Attempting to change it returns an error.

## UpdateExportOptions (Simple Path)

The simplest way to change configuration at runtime. Pass a full `ExportOptions`
and the server splits it into tuning and policy changes automatically.

```go
err := nfs.UpdateExportOptions(absnfs.ExportOptions{
	ReadOnly:         true,
	AllowedIPs:       []string{"10.0.0.0/8"},
	AttrCacheTimeout: 30 * time.Second,
	MaxWorkers:       32,
})
```

This applies tuning changes immediately and policy changes via drain-and-swap.

## UpdateTuningOptions (Targeted)

For performance changes that should take effect immediately with no client
disruption, use `UpdateTuningOptions` directly. It takes a mutation function
that receives a copy of the current tuning options.

```go
nfs.UpdateTuningOptions(func(t *absnfs.TuningOptions) {
	t.AttrCacheSize = 50000
	t.MaxWorkers = 64
	t.TransferSize = 131072
})
```

Side effects are applied automatically: caches are resized, the worker pool
is adjusted, and logging configuration is reloaded if changed.

## UpdatePolicyOptions (Targeted)

For security changes that must not have stale reads, use `UpdatePolicyOptions`.
This triggers drain-and-swap: in-flight requests complete, new requests are
temporarily held, then the new policy takes effect.

```go
err := nfs.UpdatePolicyOptions(absnfs.PolicyOptions{
	ReadOnly:   true,
	Secure:     true,
	AllowedIPs: []string{"192.168.1.0/24"},
})
```

During the drain window, NFS clients receive `NFSERR_JUKEBOX` and retry
automatically. The interruption is typically imperceptible.

## Reading Current Options

```go
opts := nfs.GetExportOptions()
fmt.Printf("Read-only: %v\n", opts.ReadOnly)
fmt.Printf("Cache size: %d\n", opts.AttrCacheSize)
```

`GetExportOptions()` returns a snapshot -- a copy of the current tuning and
policy values. It is safe to call from any goroutine.

## TLS Certificate Reload

TLS certificates can be reloaded without restarting the server or changing any
other TLS settings:

```go
opts := nfs.GetExportOptions()
if opts.TLS != nil {
	err := opts.TLS.ReloadCertificates()
}
```

The new certificate takes effect on the next TLS handshake. Existing connections
are not affected.
