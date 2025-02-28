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

eXternal Data Representation (XDR) is a standard for serializing data, enabling communication between different architectures. ABSNFS implements XDR encoding and decoding in pure Go:

```go
// Example XDR encoder implementation (simplified)
type XDREncoder struct {
    buffer []byte
    offset int
}

func (e *XDREncoder) EncodeUint32(value uint32) {
    binary.BigEndian.PutUint32(e.buffer[e.offset:], value)
    e.offset += 4
}

func (e *XDREncoder) EncodeString(value string) {
    // Encode length
    length := uint32(len(value))
    e.EncodeUint32(length)
    
    // Encode string data
    copy(e.buffer[e.offset:], value)
    e.offset += int(length)
    
    // Pad to 4-byte boundary
    padding := (4 - (length % 4)) % 4
    e.offset += int(padding)
}

// Similar methods for other data types...
```

```go
// Example XDR decoder implementation (simplified)
type XDRDecoder struct {
    buffer []byte
    offset int
}

func (d *XDRDecoder) DecodeUint32() (uint32, error) {
    if d.offset+4 > len(d.buffer) {
        return 0, errors.New("buffer overflow")
    }
    value := binary.BigEndian.Uint32(d.buffer[d.offset:])
    d.offset += 4
    return value, nil
}

func (d *XDRDecoder) DecodeString() (string, error) {
    // Decode length
    length, err := d.DecodeUint32()
    if err != nil {
        return "", err
    }
    
    // Decode string data
    if d.offset+int(length) > len(d.buffer) {
        return "", errors.New("buffer overflow")
    }
    value := string(d.buffer[d.offset:d.offset+int(length)])
    d.offset += int(length)
    
    // Skip padding
    padding := (4 - (length % 4)) % 4
    d.offset += int(padding)
    
    return value, nil
}

// Similar methods for other data types...
```

## Program Registry

ABSNFS maintains a registry of RPC programs and handlers:

```go
// Program handler interface
type RPCHandler interface {
    // Handle an RPC call and return the result
    HandleCall(proc uint32, params []byte) ([]byte, error)
    
    // Get the program details
    GetProgramInfo() ProgramInfo
}

// Program registry
type ProgramRegistry struct {
    programs map[uint32]map[uint32]RPCHandler // prog -> vers -> handler
}

// Register a program handler
func (r *ProgramRegistry) Register(prog, vers uint32, handler RPCHandler) {
    if _, ok := r.programs[prog]; !ok {
        r.programs[prog] = make(map[uint32]RPCHandler)
    }
    r.programs[prog][vers] = handler
}

// Look up a program handler
func (r *ProgramRegistry) Lookup(prog, vers uint32) (RPCHandler, error) {
    if progMap, ok := r.programs[prog]; ok {
        if handler, ok := progMap[vers]; ok {
            return handler, nil
        }
        return nil, fmt.Errorf("program version not supported: %d.%d", prog, vers)
    }
    return nil, fmt.Errorf("program not supported: %d", prog)
}
```

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

ABSNFS manages RPC connections with several strategies:

1. **Connection Pooling**: Reuses TCP connections for better performance
2. **Timeouts**: Implements read and write timeouts to prevent hangs
3. **Graceful Shutdown**: Properly closes connections during shutdown
4. **Error Handling**: Recovers from connection errors and continues serving

```go
// Example connection manager (simplified)
type ConnectionManager struct {
    tcpListener net.Listener
    udpConn     *net.UDPConn
    connections map[net.Conn]bool
    mutex       sync.Mutex
    wg          sync.WaitGroup
    shutdown    bool
}

// Accept and handle TCP connections
func (cm *ConnectionManager) acceptTCP() {
    for !cm.shutdown {
        conn, err := cm.tcpListener.Accept()
        if err != nil {
            if !cm.shutdown {
                log.Printf("TCP accept error: %v", err)
            }
            continue
        }
        
        cm.mutex.Lock()
        cm.connections[conn] = true
        cm.mutex.Unlock()
        
        cm.wg.Add(1)
        go func() {
            defer cm.wg.Done()
            defer conn.Close()
            defer func() {
                cm.mutex.Lock()
                delete(cm.connections, conn)
                cm.mutex.Unlock()
            }()
            
            cm.handleTCPConnection(conn)
        }()
    }
}

// Handle UDP packets
func (cm *ConnectionManager) handleUDP() {
    buffer := make([]byte, 65507)
    for !cm.shutdown {
        cm.udpConn.SetReadDeadline(time.Now().Add(1 * time.Second))
        n, addr, err := cm.udpConn.ReadFromUDP(buffer)
        if err != nil {
            if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
                continue
            }
            if !cm.shutdown {
                log.Printf("UDP read error: %v", err)
            }
            continue
        }
        
        // Handle the packet
        go cm.handleUDPPacket(buffer[:n], addr)
    }
}

// Shutdown connections
func (cm *ConnectionManager) Shutdown() error {
    cm.shutdown = true
    
    // Close listeners
    if cm.tcpListener != nil {
        cm.tcpListener.Close()
    }
    if cm.udpConn != nil {
        cm.udpConn.Close()
    }
    
    // Close all connections
    cm.mutex.Lock()
    for conn := range cm.connections {
        conn.Close()
    }
    cm.mutex.Unlock()
    
    // Wait for handlers to complete
    cm.wg.Wait()
    
    return nil
}
```

## RPC Handler Implementation

ABSNFS implements handlers for the required RPC programs:

### MOUNT Program (100005)

Handles mount requests from clients:

```go
type MountHandler struct {
    server      *Server
    exportPaths map[string]FileHandle
}

func (h *MountHandler) HandleCall(proc uint32, params []byte) ([]byte, error) {
    switch proc {
    case MOUNTPROC_NULL:
        return []byte{}, nil
        
    case MOUNTPROC_MNT:
        // Parse mount path
        decoder := NewXDRDecoder(params)
        path, err := decoder.DecodeString()
        if err != nil {
            return nil, err
        }
        
        // Check if path is exported
        handle, ok := h.exportPaths[path]
        if !ok {
            // Return error: not exported
            encoder := NewXDREncoder(bufferSize)
            encoder.EncodeUint32(MOUNTSTAT_NOTEXPORTED)
            return encoder.Bytes(), nil
        }
        
        // Return success with file handle
        encoder := NewXDREncoder(bufferSize)
        encoder.EncodeUint32(MOUNTSTAT_OK)
        encoder.EncodeBytes(handle)
        return encoder.Bytes(), nil
        
    case MOUNTPROC_DUMP:
        // Return list of mounted clients
        // ...
        
    case MOUNTPROC_UMNT:
        // Handle unmount request
        // ...
        
    case MOUNTPROC_UMNTALL:
        // Handle unmount all request
        // ...
        
    case MOUNTPROC_EXPORT:
        // Return list of exports
        // ...
        
    default:
        return nil, fmt.Errorf("unsupported MOUNT procedure: %d", proc)
    }
}
```

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

1. **Buffer Pooling**: Reuses buffers to reduce memory allocations
2. **Concurrent Handling**: Processes requests concurrently
3. **Batch Processing**: Batches multiple operations when possible
4. **Connection Reuse**: Keeps TCP connections alive for multiple requests
5. **Zero-Copy Design**: Minimizes data copying during processing

Example buffer pool implementation:

```go
// Buffer pool for XDR encoding/decoding
type BufferPool struct {
    pool sync.Pool
}

func NewBufferPool(size int) *BufferPool {
    return &BufferPool{
        pool: sync.Pool{
            New: func() interface{} {
                return make([]byte, size)
            },
        },
    }
}

func (p *BufferPool) Get() []byte {
    return p.pool.Get().([]byte)
}

func (p *BufferPool) Put(buf []byte) {
    p.pool.Put(buf)
}
```

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