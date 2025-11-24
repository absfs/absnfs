---
layout: default
title: RPC Implementation
---

# RPC Implementation

This document describes how ABSNFS implements the Remote Procedure Call (RPC) protocol, which is the foundation of NFS communication.

## Introduction to RPC in NFS

Network File System (NFS) relies on the Remote Procedure Call (RPC) protocol to enable clients to invoke procedures on the server. RPC provides a framework that abstracts away network details, allowing the client to call server functions as if they were local.

Key characteristics of RPC in NFS:
- **Program-Based**: RPC services are identified by program numbers
- **Version Support**: Each program can have multiple versions
- **Procedure Calls**: Within each program, procedures are identified by numbers
- **XDR Encoding**: Data is encoded using the eXternal Data Representation format
- **Authentication**: Optional authentication mechanisms can be used
- **Transport Independence**: Can run over TCP or UDP

## RPC Architecture in ABSNFS

ABSNFS implements a custom, pure Go RPC server that handles the NFS protocol. The implementation includes:

1. **RPC Server**: Listens for and processes RPC requests
2. **XDR Encoder/Decoder**: Serializes and deserializes data
3. **Program Registry**: Maps program numbers to handlers
4. **Authentication Handlers**: Validates client credentials
5. **Transport Handlers**: Manages TCP and UDP connections

## RPC Protocol Flow

The typical flow of an RPC call in ABSNFS:

1. **Client Preparation**:
   - Client marshals arguments using XDR
   - Client builds an RPC message with program, version, procedure, and arguments

2. **Request Transmission**:
   - Client sends the RPC message to the server
   - For TCP, a record marking standard is used to frame messages
   - For UDP, each request fits within a single datagram

3. **Server Processing**:
   - Server accepts the connection or datagram
   - Server unmarshals the RPC header to identify the request
   - Server dispatches the request to the appropriate handler
   - Handler processes the request and produces a result

4. **Response Transmission**:
   - Server marshals the result using XDR
   - Server builds an RPC response message
   - Server sends the response back to the client

5. **Client Handling**:
   - Client receives the response
   - Client unmarshals the response
   - Client processes the result

## RPC Message Format

RPC messages follow a defined format:

### RPC Call (Request)

```
struct rpc_msg {
    uint32 xid;            // Transaction ID
    enum msg_type {        // Message type (0 = CALL)
        CALL = 0,
        REPLY = 1
    } mtype;
    
    // Body for CALL messages
    struct call_body {
        uint32 rpcvers;    // RPC version (2)
        uint32 prog;       // Program number (e.g., 100003 for NFS)
        uint32 vers;       // Program version (e.g., 3 for NFSv3)
        uint32 proc;       // Procedure number
        opaque_auth cred;  // Authentication credentials
        opaque_auth verf;  // Authentication verifier
        // Procedure-specific parameters follow
    } cbody;
};
```

### RPC Reply (Response)

```
struct rpc_msg {
    uint32 xid;            // Transaction ID (must match request)
    enum msg_type {        // Message type (1 = REPLY)
        CALL = 0,
        REPLY = 1
    } mtype;
    
    // Body for REPLY messages
    struct reply_body {
        enum reply_stat {
            MSG_ACCEPTED = 0,
            MSG_DENIED = 1
        } stat;
        
        union switch (reply_stat stat) {
        case MSG_ACCEPTED:
            struct accepted_reply {
                opaque_auth verf;
                enum accept_stat {
                    SUCCESS = 0,
                    PROG_UNAVAIL = 1,
                    PROG_MISMATCH = 2,
                    PROC_UNAVAIL = 3,
                    GARBAGE_ARGS = 4,
                    SYSTEM_ERR = 5
                } stat;
                // For SUCCESS, procedure-specific results follow
                // For PROG_MISMATCH, version information follows
            } areply;
        case MSG_DENIED:
            struct rejected_reply {
                enum reject_stat {
                    RPC_MISMATCH = 0,
                    AUTH_ERROR = 1
                } stat;
                // Additional error information follows
            } rreply;
        } reply;
    } rbody;
};
```

## Authentication

RPC supports several authentication mechanisms. ABSNFS implements:

1. **AUTH_NONE (0)**: No authentication
2. **AUTH_SYS (1)** (formerly AUTH_UNIX): Basic Unix-style credentials

The `AUTH_SYS` credential structure:

```
struct authsys_parms {
    uint32 stamp;          // Timestamp
    string machinename<>;  // Client hostname
    uint32 uid;            // User ID
    uint32 gid;            // Group ID
    uint32 gids<>;         // Additional group IDs
};
```

## XDR Implementation

eXternal Data Representation (XDR) is a standard for serializing data, enabling communication between different architectures. ABSNFS implements XDR encoding and decoding as standalone functions that work with io.Reader and io.Writer:

```go
// XDR encoding functions work with io.Writer
func xdrEncodeUint32(w io.Writer, v uint32) error {
    return binary.Write(w, binary.BigEndian, v)
}

func xdrEncodeUint64(w io.Writer, v uint64) error {
    return binary.Write(w, binary.BigEndian, v)
}

func xdrEncodeString(w io.Writer, s string) error {
    length := uint32(len(s))
    if err := xdrEncodeUint32(w, length); err != nil {
        return err
    }

    if _, err := w.Write([]byte(s)); err != nil {
        return err
    }

    // Add padding to align to 4-byte boundary
    padding := (4 - (length % 4)) % 4
    if padding > 0 {
        pad := make([]byte, padding)
        if _, err := w.Write(pad); err != nil {
            return err
        }
    }

    return nil
}

func xdrEncodeBytes(w io.Writer, b []byte) error {
    length := uint32(len(b))
    if err := xdrEncodeUint32(w, length); err != nil {
        return err
    }

    if _, err := w.Write(b); err != nil {
        return err
    }

    // Add padding
    padding := (4 - (length % 4)) % 4
    if padding > 0 {
        pad := make([]byte, padding)
        if _, err := w.Write(pad); err != nil {
            return err
        }
    }

    return nil
}
```

```go
// XDR decoding functions work with io.Reader
func xdrDecodeUint32(r io.Reader) (uint32, error) {
    var v uint32
    err := binary.Read(r, binary.BigEndian, &v)
    return v, err
}

func xdrDecodeUint64(r io.Reader) (uint64, error) {
    var v uint64
    err := binary.Read(r, binary.BigEndian, &v)
    return v, err
}

func xdrDecodeString(r io.Reader) (string, error) {
    length, err := xdrDecodeUint32(r)
    if err != nil {
        return "", err
    }

    // Security check: prevent excessive memory allocation
    if length > MAX_XDR_STRING_LENGTH {
        return "", fmt.Errorf("string length %d exceeds maximum %d",
            length, MAX_XDR_STRING_LENGTH)
    }

    buf := make([]byte, length)
    if _, err := io.ReadFull(r, buf); err != nil {
        return "", err
    }

    // Skip padding
    padding := (4 - (length % 4)) % 4
    if padding > 0 {
        pad := make([]byte, padding)
        if _, err := io.ReadFull(r, pad); err != nil {
            return "", err
        }
    }

    return string(buf), nil
}

func xdrDecodeBytes(r io.Reader, maxLen int) ([]byte, error) {
    length, err := xdrDecodeUint32(r)
    if err != nil {
        return nil, err
    }

    // Security check
    if length > uint32(maxLen) {
        return nil, fmt.Errorf("byte array length %d exceeds maximum %d",
            length, maxLen)
    }

    buf := make([]byte, length)
    if _, err := io.ReadFull(r, buf); err != nil {
        return nil, err
    }

    // Skip padding
    padding := (4 - (length % 4)) % 4
    if padding > 0 {
        pad := make([]byte, padding)
        if _, err := io.ReadFull(r, pad); err != nil {
            return nil, err
        }
    }

    return buf, nil
}
```

Note that ABSNFS uses standalone functions rather than encoder/decoder objects. This approach is simpler and more flexible, allowing the functions to work directly with network connections and buffers.

## RPC Handler

ABSNFS uses a simple NFSProcedureHandler that dispatches RPC calls based on program and procedure numbers:

```go
type NFSProcedureHandler struct {
    server *Server
}

func (h *NFSProcedureHandler) HandleCall(call *RPCCall, body io.Reader, authCtx *AuthContext) (*RPCReply, error) {
    reply := &RPCReply{
        Header: call.Header,
        Status: MSG_ACCEPTED,
        Verifier: RPCVerifier{
            Flavor: 0,
            Body:   []byte{},
        },
    }

    // Dispatch based on program number
    switch call.Header.Program {
    case MOUNT_PROGRAM:
        return h.handleMountCall(call, body, reply, authCtx)
    case NFS_PROGRAM:
        return h.handleNFSCall(call, body, reply, authCtx)
    default:
        reply.Status = PROG_UNAVAIL
        return reply, nil
    }
}
```

The handler directly checks the program number and routes to the appropriate handler function (handleMountCall or handleNFSCall). This is simpler than maintaining a registry and perfectly adequate for the two programs that NFS needs to support.

## Transport Handling

ABSNFS supports both TCP and UDP transports for RPC:

### TCP Transport

TCP transport requires record marking to delimit messages:

```go
// Read a complete RPC message from a TCP connection
func readRPCMessageTCP(conn net.Conn) ([]byte, error) {
    // Read the record marker (4 bytes)
    marker := make([]byte, 4)
    if _, err := io.ReadFull(conn, marker); err != nil {
        return nil, err
    }
    
    // Extract size and flags from marker
    size := binary.BigEndian.Uint32(marker) & 0x7FFFFFFF
    lastFragment := (binary.BigEndian.Uint32(marker) & 0x80000000) != 0
    
    // Read the message data
    message := make([]byte, size)
    if _, err := io.ReadFull(conn, message); err != nil {
        return nil, err
    }
    
    // For multi-fragment messages, continue reading until last fragment
    // (simplified - actual implementation would accumulate fragments)
    if !lastFragment {
        return nil, errors.New("multi-fragment messages not supported")
    }
    
    return message, nil
}

// Write an RPC message to a TCP connection
func writeRPCMessageTCP(conn net.Conn, message []byte) error {
    // Create record marker (size with last fragment bit set)
    marker := make([]byte, 4)
    binary.BigEndian.PutUint32(marker, uint32(len(message))|0x80000000)
    
    // Write marker followed by message
    if _, err := conn.Write(marker); err != nil {
        return err
    }
    if _, err := conn.Write(message); err != nil {
        return err
    }
    
    return nil
}
```

### UDP Transport

UDP transport is simpler but limited by datagram size:

```go
// Read an RPC message from a UDP connection
func readRPCMessageUDP(conn *net.UDPConn) ([]byte, net.Addr, error) {
    // Buffer for the maximum UDP packet size
    buffer := make([]byte, 65507)
    
    // Read a datagram
    n, addr, err := conn.ReadFromUDP(buffer)
    if err != nil {
        return nil, nil, err
    }
    
    // Return the message data and sender address
    return buffer[:n], addr, nil
}

// Write an RPC message to a UDP connection
func writeRPCMessageUDP(conn *net.UDPConn, message []byte, addr net.Addr) error {
    // Check if message fits in a UDP datagram
    if len(message) > 65507 {
        return errors.New("message too large for UDP")
    }
    
    // Write the message
    _, err := conn.WriteTo(message, addr)
    return err
}
```

## Connection Management

ABSNFS manages TCP connections through the Server component. Note that ABSNFS currently only supports TCP, not UDP.

Key connection management features:

1. **Connection Tracking**: Maintains a map of active connections with state tracking
2. **Connection Limits**: Enforces maximum connection limits if configured
3. **Idle Timeout**: Automatically closes idle connections after a timeout period
4. **Timeouts**: Read and write timeouts prevent hung connections
5. **Graceful Shutdown**: Properly closes all connections during server shutdown

```go
type Server struct {
    listener    net.Listener
    activeConns map[net.Conn]*connectionState
    connCount   int
    connMutex   sync.Mutex
    // ... other fields
}

type connectionState struct {
    lastActivity   time.Time
    unregisterOnce sync.Once // Ensures connection is only unregistered once
}

// Accept loop creates a goroutine for each connection
func (s *Server) acceptLoop(procHandler *NFSProcedureHandler) {
    for {
        conn, err := s.listener.Accept()
        if err != nil {
            // Handle errors and check for shutdown
            continue
        }

        // Register and track the connection
        if !s.registerConnection(conn) {
            conn.Close() // Connection limit reached
            continue
        }

        s.wg.Add(1)
        go func() {
            defer s.wg.Done()
            defer s.unregisterConnection(conn)
            s.handleConnection(conn, procHandler)
        }()
    }
}

// Each connection is handled in its own goroutine
func (s *Server) handleConnection(conn net.Conn, procHandler *NFSProcedureHandler) {
    defer conn.Close()

    for {
        // Set read deadline
        conn.SetReadDeadline(time.Now().Add(readTimeout))

        // Read and process RPC call
        call, body, err := s.readRPCCall(conn)
        if err != nil {
            return
        }

        // Update activity timestamp
        s.updateConnectionActivity(conn)

        // Handle with worker pool if available, otherwise directly
        reply, err := procHandler.HandleCall(call, body, authCtx)

        // Set write deadline and send reply
        conn.SetWriteDeadline(time.Now().Add(writeTimeout))
        s.writeRPCReply(conn, reply)

        // Update activity timestamp again
        s.updateConnectionActivity(conn)
    }
}
```

The connection management is integrated directly into the Server rather than being a separate component.

## RPC Handler Implementation

ABSNFS implements handlers for the required RPC programs:

### MOUNT Program (100005)

The MOUNT protocol is handled by the handleMountCall method of NFSProcedureHandler:

```go
func (h *NFSProcedureHandler) handleMountCall(call *RPCCall, body io.Reader,
    reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {

    // Check version
    if call.Header.Version != MOUNT_V3 {
        reply.Status = PROG_MISMATCH
        return reply, nil
    }

    switch call.Header.Procedure {
    case 0: // NULL - ping operation
        return reply, nil

    case 1: // MNT - mount a filesystem
        // Apply rate limiting for mount operations
        if h.server.handler.rateLimiter != nil {
            if !h.server.handler.rateLimiter.AllowOperation(
                authCtx.ClientIP, OpTypeMount) {
                var buf bytes.Buffer
                xdrEncodeUint32(&buf, NFSERR_DELAY)
                reply.Data = buf.Bytes()
                return reply, nil
            }
        }

        // Decode the mount path
        mountPath, err := xdrDecodeString(body)
        if err != nil {
            reply.Status = GARBAGE_ARGS
            return reply, nil
        }

        // Look up the path in the filesystem
        node, err := h.server.handler.Lookup(mountPath)
        if err != nil {
            var buf bytes.Buffer
            xdrEncodeUint32(&buf, NFSERR_NOENT)
            reply.Data = buf.Bytes()
            return reply, nil
        }

        // Allocate a file handle for the mounted directory
        handle := h.server.handler.fileMap.Allocate(node)

        // Encode the response with the file handle
        var buf bytes.Buffer
        xdrEncodeUint32(&buf, NFS_OK)
        binary.Write(&buf, binary.BigEndian, handle)
        reply.Data = buf.Bytes()
        return reply, nil

    case 2: // DUMP - list mounted filesystems
        var buf bytes.Buffer
        xdrEncodeUint32(&buf, 0) // No entries
        reply.Data = buf.Bytes()
        return reply, nil

    case 3: // UMNT - unmount a filesystem
        unmountPath, err := xdrDecodeString(body)
        if err != nil {
            reply.Status = GARBAGE_ARGS
            return reply, nil
        }
        // In the actual implementation, we just acknowledge the unmount
        return reply, nil

    default:
        reply.Status = PROC_UNAVAIL
        return reply, nil
    }
}
```

Note that ABSNFS handles MOUNT operations directly in the NFSProcedureHandler rather than having a separate MountHandler class. This is simpler and sufficient for the relatively small number of MOUNT procedures.

### NFS Program (100003)

Handles NFS operations:

```go
type NFSHandler struct {
    server *Server
    fs     absfs.FileSystem
}

func (h *NFSHandler) HandleCall(proc uint32, params []byte) ([]byte, error) {
    switch proc {
    case NFSPROC_NULL:
        return []byte{}, nil
        
    case NFSPROC_GETATTR:
        // Parse file handle
        decoder := NewXDRDecoder(params)
        handle, err := decoder.DecodeBytes()
        if err != nil {
            return nil, err
        }
        
        // Get file attributes
        attrs, err := h.server.GetAttributes(handle)
        if err != nil {
            return encodeNFSError(NFS3ERR_STALE), nil
        }
        
        // Encode and return attributes
        encoder := NewXDREncoder(bufferSize)
        encoder.EncodeUint32(NFS3_OK)
        encodeFileAttributes(encoder, attrs)
        return encoder.Bytes(), nil
        
    case NFSPROC_LOOKUP:
        // Implementation for LOOKUP
        // ...
        
    case NFSPROC_READ:
        // Implementation for READ
        // ...
        
    case NFSPROC_WRITE:
        // Implementation for WRITE
        // ...
        
    // Other NFS procedures...
        
    default:
        return nil, fmt.Errorf("unsupported NFS procedure: %d", proc)
    }
}
```

## Error Handling

ABSNFS implements robust error handling throughout the RPC stack:

1. **Protocol Errors**: Errors in RPC message format or XDR encoding
2. **Program Errors**: Errors in program, version, or procedure numbers
3. **Authentication Errors**: Errors in client credentials
4. **Request Errors**: Errors in request parameters
5. **Implementation Errors**: Errors in handler implementation
6. **Network Errors**: Errors in network communication

Example error handling in the RPC server:

```go
// Handle an RPC call
func (s *Server) handleRPCCall(call *RPCCall) *RPCReply {
    reply := &RPCReply{
        XID: call.XID,
        ReplyStatus: MSG_ACCEPTED,
    }
    
    // Verify RPC version
    if call.RPCVersion != 2 {
        reply.AcceptStatus = RPC_MISMATCH
        reply.VersionLow = 2
        reply.VersionHigh = 2
        return reply
    }
    
    // Look up program handler
    handler, err := s.registry.Lookup(call.Program, call.Version)
    if err != nil {
        if strings.Contains(err.Error(), "program not supported") {
            reply.AcceptStatus = PROG_UNAVAIL
        } else if strings.Contains(err.Error(), "version not supported") {
            reply.AcceptStatus = PROG_MISMATCH
            info := handler.GetProgramInfo()
            reply.VersionLow = info.MinVersion
            reply.VersionHigh = info.MaxVersion
        } else {
            reply.AcceptStatus = SYSTEM_ERR
        }
        return reply
    }
    
    // Handle the call
    result, err := handler.HandleCall(call.Procedure, call.Params)
    if err != nil {
        if strings.Contains(err.Error(), "unsupported procedure") {
            reply.AcceptStatus = PROC_UNAVAIL
        } else {
            reply.AcceptStatus = SYSTEM_ERR
        }
        return reply
    }
    
    // Success
    reply.AcceptStatus = SUCCESS
    reply.Results = result
    return reply
}
```

## Performance Optimizations

ABSNFS implements several optimizations for RPC performance:

1. **Worker Pool**: Optional worker pool for concurrent request handling (see worker_pool.go)
2. **Batch Processing**: Optional batching of similar operations (see batch.go)
3. **Connection Reuse**: Persistent TCP connections for multiple requests
4. **Rate Limiting**: Token bucket rate limiting to prevent DoS attacks
5. **Attribute Caching**: Caches file attributes to reduce filesystem access
6. **Read-Ahead Buffering**: Prefetches data for sequential reads

The implementation focuses on practical optimizations that provide measurable benefits rather than premature optimization. For example, ABSNFS does not use buffer pools because Go's garbage collector handles short-lived allocations efficiently.

## RPC Security

ABSNFS implements security measures for RPC:

1. **Request Validation**: Validates all incoming requests
2. **Authentication**: Validates client credentials
3. **Access Control**: Restricts access based on client identity
4. **Bounds Checking**: Prevents buffer overflows
5. **Resource Limits**: Limits memory and CPU usage per request

Example authentication handling:

```go
// Validate client credentials
func (s *Server) validateCredentials(auth *AuthInfo) (bool, error) {
    switch auth.Flavor {
    case AUTH_NONE:
        // No authentication, always accept
        return true, nil
        
    case AUTH_SYS:
        // Parse AUTH_SYS credentials
        decoder := NewXDRDecoder(auth.Body)
        
        // Decode fields
        stamp, err := decoder.DecodeUint32()
        if err != nil {
            return false, err
        }
        
        machineName, err := decoder.DecodeString()
        if err != nil {
            return false, err
        }
        
        uid, err := decoder.DecodeUint32()
        if err != nil {
            return false, err
        }
        
        gid, err := decoder.DecodeUint32()
        if err != nil {
            return false, err
        }
        
        // Check if client IP is allowed
        if !s.isIPAllowed(clientIP) {
            return false, nil
        }
        
        // Check if UID/GID is allowed
        if !s.isUserAllowed(uid, gid) {
            return false, nil
        }
        
        return true, nil
        
    default:
        // Unsupported authentication flavor
        return false, fmt.Errorf("unsupported auth flavor: %d", auth.Flavor)
    }
}
```

## Conclusion

The RPC implementation in ABSNFS provides a robust foundation for NFS functionality. By implementing the RPC protocol in pure Go, ABSNFS achieves platform independence, good performance, and flexibility.

Key strengths of the implementation include:
- Complete implementation of the RPC protocol
- Support for both TCP and UDP transports
- Efficient XDR encoding and decoding
- Robust error handling
- Performance optimizations
- Security features

This implementation allows ABSNFS to expose any ABSFS-compatible filesystem over the network via the standard NFS protocol.