---
layout: default
title: Server
---

# Server

The `Server` type is responsible for handling network connections and RPC requests for the NFS protocol.

## Type Definition

```go
type Server struct {
    // contains filtered or unexported fields
}
```

The `Server` type is not typically created directly by users. Instead, it's created and managed by the `AbsfsNFS` type.

## Key Responsibilities

The `Server` type is responsible for:

1. **Network Handling**: Listening for and accepting incoming connections
2. **RPC Protocol**: Decoding and encoding RPC messages
3. **Request Routing**: Routing RPC requests to appropriate handlers
4. **Error Handling**: Managing network and protocol errors
5. **Connection Management**: Handling client connection lifecycles

## Methods

### NewServer

```go
func NewServer(options ServerOptions) (*Server, error)
```

Creates a new NFS server with the specified options. The `ServerOptions` type includes:
- `Name`: Server name
- `UID`: Server UID
- `GID`: Server GID
- `ReadOnly`: Read-only mode
- `Port`: Port to listen on (default: 2049)
- `Hostname`: Hostname to bind to (default: "localhost")
- `Debug`: Enable debug logging

### SetHandler

```go
func (s *Server) SetHandler(handler *AbsfsNFS)
```

Sets the filesystem handler for the server. This must be called before calling `Listen()`.

### Listen

```go
func (s *Server) Listen() error
```

Starts listening for incoming connections. The server will listen on the hostname and port specified in `ServerOptions`. If the port is already in use and the default port (2049) was specified, it will automatically try a random port.

### Stop

```go
func (s *Server) Stop() error
```

Stops the server, closing all active connections and releasing resources. This method waits for all goroutines to finish with a 5-second timeout.

## Example Usage

The `Server` type is typically used internally by the `AbsfsNFS` type, but can be created directly:

```go
// Create server with options
serverOpts := absnfs.ServerOptions{
    Name:     "my-nfs-server",
    UID:      1000,
    GID:      1000,
    Port:     2049,
    Hostname: "localhost",
    Debug:    true,
}

server, err := absnfs.NewServer(serverOpts)
if err != nil {
    log.Fatal(err)
}

// Create filesystem handler
fs := ... // Your filesystem implementation
nfsHandler, err := absnfs.New(fs, absnfs.NFSOptions{})
if err != nil {
    log.Fatal(err)
}

// Set the handler
server.SetHandler(nfsHandler)

// Start listening
if err := server.Listen(); err != nil {
    log.Fatal(err)
}

// Later, stop the server
if err := server.Stop(); err != nil {
    log.Printf("Error during shutdown: %v", err)
}
```

In most cases, you will use the `AbsfsNFS` type which manages the server lifecycle for you:

```go
// Create NFS server (this creates and manages a Server internally)
nfsServer, err := absnfs.New(fs, options)
if err != nil {
    log.Fatal(err)
}

// Export the filesystem (this internally starts the server)
if err := nfsServer.Export("/export/test", 2049); err != nil {
    log.Fatal(err)
}

// Later, stop the server
if err := nfsServer.Unexport(); err != nil {
    log.Printf("Error during shutdown: %v", err)
}
```

## Implementation Details

The `Server` type handles several complex aspects of the NFS protocol:

### RPC Protocol Support

The server implements the RPC (Remote Procedure Call) protocol, which is the foundation of NFS. This includes:

- XDR (eXternal Data Representation) encoding and decoding
- RPC message framing
- RPC authentication
- Program and procedure dispatching

### Connection Management

The server manages client connections, including:

- Connection establishment and teardown
- Timeouts and keepalives
- Connection pooling (for TCP connections)
- UDP datagram handling

### Concurrency

The server handles concurrent requests from multiple clients by:

- Using a connection pool
- Processing requests concurrently
- Ensuring thread safety for shared resources

### Error Handling

The server implements robust error handling for:

- Network errors
- Protocol decoding errors
- Handler errors
- Resource exhaustion

## Performance Considerations

The `Server` type is optimized for performance in several ways:

1. **Connection Pooling**: Reuses connections to reduce overhead
2. **Buffer Pooling**: Reuses buffers to reduce memory allocations
3. **Concurrency**: Processes requests concurrently
4. **Timeout Management**: Implements appropriate timeouts to prevent resource leaks