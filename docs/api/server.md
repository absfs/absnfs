# Server

The `Server` type manages the NFS server lifecycle: TCP listening, TLS termination, connection tracking, and graceful shutdown.

## Types

### ServerOptions

```go
type ServerOptions struct {
    Name             string // Server name
    UID              uint32 // Server UID
    GID              uint32 // Server GID
    ReadOnly         bool   // Read-only mode
    Port             int    // Port to listen on (0 = random port)
    MountPort        int    // Port for mount daemon (0 = same as NFS port)
    Hostname         string // Hostname to bind to (default: "localhost")
    Debug            bool   // Enable debug logging
    UsePortmapper    bool   // Start portmapper service (requires root for port 111)
    UseRecordMarking bool   // Use RPC record marking (required for standard NFS clients)
}
```

### Server

```go
type Server struct {
    // (unexported fields)
}
```

Holds the listener, handler reference, connection state, and shutdown coordination. The server generates a unique write verifier per boot as required by RFC 1813.

## Functions

### NewServer

```go
func NewServer(options ServerOptions) (*Server, error)
```

Creates a new NFS server. Returns an error if `Port` is negative. If `Hostname` is empty, it defaults to `"localhost"`. Port 0 lets the OS assign a random port (useful for testing).

```go
server, err := absnfs.NewServer(absnfs.ServerOptions{
    Port:             2049,
    UseRecordMarking: true,
})
```

### SetHandler

```go
func (s *Server) SetHandler(handler *AbsfsNFS)
```

Sets the `AbsfsNFS` handler for this server. Must be called before `Listen()`. Not safe for concurrent use.

### Listen

```go
func (s *Server) Listen() error
```

Starts accepting connections. Returns an error if no handler is set. Behavior depends on the handler's `PolicyOptions`:

- If TLS is configured and enabled, creates a TLS listener.
- Otherwise creates a plain TCP listener.

If `TuningOptions.IdleTimeout` is set, starts a background goroutine that periodically closes idle connections.

Each accepted connection is checked against:
1. IP allow-list (`PolicyOptions.AllowedIPs`)
2. Connection limit (`TuningOptions.MaxConnections`)

Connections that pass both checks are dispatched to per-connection handling goroutines. TCP options (keepalive, no-delay, buffer sizes) are applied from `TuningOptions`.

### StartWithPortmapper

```go
func (s *Server) StartWithPortmapper() error
```

Starts the NFS server with an embedded portmapper. This is required for standard NFS clients that query portmapper to discover services. Automatically enables record marking. Requires root/administrator privileges for port 111.

Registers:
- NFS service (program 100003, version 3)
- MOUNT service (program 100005, versions 1 and 3)

### GetPort

```go
func (s *Server) GetPort() int
```

Returns the port the server is listening on. Useful when port 0 was specified to get the OS-assigned port.

### Stop

```go
func (s *Server) Stop() error
```

Gracefully shuts down the server:

1. Cancels the server context (signals all goroutines).
2. Stops the portmapper if running.
3. Closes the listener to stop accepting new connections.
4. Closes all active connections.
5. Waits up to 5 seconds for all goroutines to finish.

Returns an error if the 5-second shutdown timeout is exceeded.

## Connection Management

The server tracks active connections with per-connection state including last activity time. Connection lifecycle:

- **Registration**: Each new connection is registered with a `sync.Once`-guarded unregister mechanism to prevent double-cleanup races.
- **Activity tracking**: Updated on each RPC call read and reply write.
- **Idle cleanup**: A background loop (when `IdleTimeout > 0`) periodically closes connections whose last activity exceeds the timeout.
- **Limit enforcement**: When `MaxConnections > 0`, new connections beyond the limit are rejected immediately.

## Request Dispatch

Each connection runs a loop that:
1. Reads an RPC call (raw TCP or record-marking framed).
2. Extracts client IP and builds an `AuthContext`.
3. Checks the rate limiter (if enabled).
4. Dispatches to `HandleCall` via the worker pool (if configured) or directly.
5. Writes the RPC reply.

The `connIO` interface abstracts framing differences between raw TCP mode (5s timeouts) and record-marking mode (30s timeouts).
