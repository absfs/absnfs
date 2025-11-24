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

### Listen

```go
func (s *Server) Listen() error
```

Starts listening for incoming connections. The address is configured through the `ServerOptions` when the server is created.

### Stop

```go
func (s *Server) Stop() error
```

Stops the server, closing all active connections and releasing resources.

### SetHandler

```go
func (s *Server) SetHandler(handler *AbsfsNFS)
```

Sets the NFS handler for the server. This must be called before starting the server to register the handler that will process NFS requests.

## Example Usage

The `Server` type is not typically used directly. Instead, it's used through the `AbsfsNFS` type:

```go
// Create NFS server
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