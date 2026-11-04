# AbsfsNFS

Defined in `types.go` and `absnfs.go`. The main server type that wraps an `absfs.SymlinkFileSystem` and exposes it as an NFSv3 share.

```go
type AbsfsNFS struct {
    // unexported fields
}
```

## New

```go
func New(fs absfs.SymlinkFileSystem, options ExportOptions) (*AbsfsNFS, error)
```

Creates a new NFS server instance. The filesystem must not be nil and must implement `absfs.SymlinkFileSystem`.

**Validation:**
- Returns `os.ErrInvalid` if `fs` is nil.
- Returns an error if `Squash` is not one of `""`, `"root"`, `"all"`, or `"none"` (case-insensitive).

**Defaults applied:** All zero-value fields in ExportOptions are replaced with defaults. See [ExportOptions](export-options.md) for the full default table.

**Resources created:**
- Attribute cache (always)
- Directory cache (if `EnableDirCache` is true)
- Worker pool (started immediately)
- Rate limiter (if `EnableRateLimiting` is true)
- Structured logger (from `LogConfig`, or no-op if nil)
- Metrics collector

```go
fs, _ := memfs.NewFS()
server, err := absnfs.New(fs, absnfs.ExportOptions{
    ReadOnly: true,
    Squash:   "root",
})
if err != nil {
    log.Fatal(err)
}
defer server.Close()
```

## Close

```go
func (n *AbsfsNFS) Close() error
```

Releases all resources held by the server:

1. Stops the export server (if `Export()` was called)
2. Stops the worker pool
3. Releases all file handles
4. Clears attribute and directory caches
5. Closes the structured logger

Returns an error only if the structured logger fails to close.

## Export

```go
func (s *AbsfsNFS) Export(mountPath string, port int) error
```

Starts serving the export on `127.0.0.1` (always; use `NewServer` for another
bind address) at the given TCP port, in the form
kernel NFS clients (Linux, macOS `mount_nfs`) expect:

- **One port for NFS and MOUNT.** The MOUNT protocol is served on the same
  listener as NFS.
- **RPC record marking** on the TCP stream (RFC 5531), which kernel clients
  require.
- **No portmapper.** Clients name the port explicitly:
  `mount_nfs -o vers=3,tcp,port=P,mountport=P,noresvport,locallocks,noatime localhost:/ ~/nfs`.
  See the README section "Mounting on macOS as a normal user".
- **TLS** from `ExportOptions.TLS` applies, as with `Server.Listen`.

Arguments:

- `mountPath` must not be empty, but is otherwise informational. Clients
  mount filesystem paths: `localhost:/` for the whole tree, `localhost:/sub`
  for a subdirectory.
- `port` must be >= 0. Use 0 to pick a free port and read it back with
  `ExportPort`. Ports below 1024 need root.
- Only one export can be active at a time per `AbsfsNFS` instance.

For another bind address or a portmapper, create a `Server` with
`NewServer`, call `SetHandler`, then `Listen`.

```go
if err := server.Export("/", 12049); err != nil {
    log.Fatal(err)
}
```

## ExportPort

```go
func (s *AbsfsNFS) ExportPort() int
```

Returns the TCP port of the export started by `Export`, or 0 when none is
running. Use it after `Export(path, 0)` to learn the port to mount.

```go
if err := server.Export("/", 0); err != nil {
    log.Fatal(err)
}
fmt.Printf("mount_nfs -o vers=3,tcp,port=%[1]d,mountport=%[1]d,noresvport,locallocks,noatime localhost:/ ~/nfs\n",
    server.ExportPort())
```

## Unexport

```go
func (s *AbsfsNFS) Unexport() error
```

Stops serving the NFS export:

1. Stops the internal server
2. Releases all open file handles
3. Clears all caches

The `AbsfsNFS` instance remains usable -- `Export()` can be called again.

## SetLogger

```go
func (n *AbsfsNFS) SetLogger(logger Logger) error
```

Replaces the structured logger at runtime. Pass nil to disable logging (uses a no-op logger). Closes the previous logger if it was a `SlogLogger`.

Returns an error if the receiver is nil.

The `Logger` interface:

```go
type Logger interface {
    Debug(msg string, fields ...LogField)
    Info(msg string, fields ...LogField)
    Warn(msg string, fields ...LogField)
    Error(msg string, fields ...LogField)
}

type LogField struct {
    Key   string
    Value interface{}
}
```

## GetExportOptions

```go
func (n *AbsfsNFS) GetExportOptions() ExportOptions
```

Returns a deep-copy snapshot of the current configuration by recombining the active TuningOptions and PolicyOptions. Thread-safe.

## UpdateExportOptions

```go
func (n *AbsfsNFS) UpdateExportOptions(newOptions ExportOptions) error
```

Updates both tuning and policy settings at runtime. Tuning changes apply immediately via atomic swap. Policy changes use drain-and-swap.

- Returns an error, and applies nothing, if `Squash`, `DefaultOwner` or `ForceOwner` differs from the current value (immutable at runtime). An empty `Squash` and a nil `DefaultOwner` mean "unchanged", so the result of `GetExportOptions` can always be passed back.
- If `newOptions.Timeouts` or `newOptions.Log` is nil, current values are preserved.

See [TuningOptions / PolicyOptions](tuning-policy.md) for the drain-and-swap mechanism.

```go
opts := server.GetExportOptions()
opts.ReadOnly = true
opts.AttrCacheSize = 20000
err := server.UpdateExportOptions(opts)
```

## Other Methods

| Method | Signature | Description |
|--------|-----------|-------------|
| `UpdateTuningOptions` | `(n *AbsfsNFS) UpdateTuningOptions(fn func(*TuningOptions))` | Atomic swap of performance settings |
| `UpdatePolicyOptions` | `(n *AbsfsNFS) UpdatePolicyOptions(newPolicy PolicyOptions) error` | Drain-and-swap of security settings |
| `GetAttrCacheSize` | `(n *AbsfsNFS) GetAttrCacheSize() int` | Current attribute cache capacity |
| `ExecuteWithWorker` | `(n *AbsfsNFS) ExecuteWithWorker(task func() interface{}) interface{}` | Run task in worker pool or inline |
