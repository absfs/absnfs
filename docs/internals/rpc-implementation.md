---
layout: default
title: RPC Implementation
---

# RPC Implementation

This document describes the actual RPC (Remote Procedure Call) implementation in ABSNFS.

## Introduction to RPC in NFS

Network File System (NFS) relies on the Remote Procedure Call (RPC) protocol to enable clients to invoke procedures on the server. ABSNFS implements the RPC protocol using simple function-based XDR encoding/decoding and struct-based message handling.

## RPC Constants

The implementation defines key constants for RPC message types and status codes:

```go
// RPC message types
const (
    RPC_CALL  = 0
    RPC_REPLY = 1
)

// RPC reply status
const (
    MSG_ACCEPTED  = 0
    MSG_DENIED    = 1
    PROG_UNAVAIL  = 1
    PROG_MISMATCH = 2
    PROC_UNAVAIL  = 3
    GARBAGE_ARGS  = 4
    ACCESS_DENIED = 5
)

// RPC program numbers
const (
    MOUNT_PROGRAM = 100005
    NFS_PROGRAM   = 100003
)

// RPC versions
const (
    MOUNT_V3 = 3
    NFS_V3   = 3
)
```

## NFS Procedure Constants

The implementation defines procedure numbers for NFSv3 operations:

```go
const (
    NFSPROC3_NULL        = 0
    NFSPROC3_GETATTR     = 1
    NFSPROC3_SETATTR     = 2
    NFSPROC3_LOOKUP      = 3
    NFSPROC3_ACCESS      = 4
    NFSPROC3_READLINK    = 5
    NFSPROC3_READ        = 6
    NFSPROC3_WRITE       = 7
    NFSPROC3_CREATE      = 8
    NFSPROC3_MKDIR       = 9
    NFSPROC3_SYMLINK     = 10
    NFSPROC3_MKNOD       = 11
    NFSPROC3_REMOVE      = 12
    NFSPROC3_RMDIR       = 13
    NFSPROC3_RENAME      = 14
    NFSPROC3_LINK        = 15
    NFSPROC3_READDIR     = 16
    NFSPROC3_READDIRPLUS = 17
    NFSPROC3_FSSTAT      = 18
    NFSPROC3_FSINFO      = 19
    NFSPROC3_PATHCONF    = 20
    NFSPROC3_COMMIT      = 21
)
```

## Core Data Structures

### RPCMsgHeader

The message header structure:

```go
type RPCMsgHeader struct {
    Xid        uint32
    MsgType    uint32
    RPCVersion uint32
    Program    uint32
    Version    uint32
    Procedure  uint32
}
```

### RPCCall

Represents an incoming RPC call:

```go
type RPCCall struct {
    Header     RPCMsgHeader
    Credential RPCCredential
    Verifier   RPCVerifier
}
```

### RPCCredential and RPCVerifier

Authentication structures:

```go
type RPCCredential struct {
    Flavor uint32
    Body   []byte
}

type RPCVerifier struct {
    Flavor uint32
    Body   []byte
}
```

### RPCReply

Represents an RPC reply message:

```go
type RPCReply struct {
    Header   RPCMsgHeader
    Status   uint32
    Verifier RPCVerifier
    Data     interface{}
}
```

## XDR Helper Functions

ABSNFS implements simple XDR encoding/decoding helper functions:

### Basic Integer Encoding/Decoding

```go
func xdrEncodeUint32(w io.Writer, v uint32) error {
    return binary.Write(w, binary.BigEndian, v)
}

func xdrDecodeUint32(r io.Reader) (uint32, error) {
    var v uint32
    err := binary.Read(r, binary.BigEndian, &v)
    return v, err
}
```

**Key points:**
- Uses big-endian byte order (network byte order)
- Simple wrappers around Go's binary package
- Error handling for I/O operations

### String Encoding/Decoding

```go
func xdrEncodeString(w io.Writer, s string) error {
    if err := xdrEncodeUint32(w, uint32(len(s))); err != nil {
        return err
    }
    _, err := w.Write([]byte(s))
    return err
}

func xdrDecodeString(r io.Reader) (string, error) {
    length, err := xdrDecodeUint32(r)
    if err != nil {
        return "", err
    }

    buf := make([]byte, length)
    _, err = io.ReadFull(r, buf)
    if err != nil {
        return "", err
    }

    return string(buf), nil
}
```

**Key points:**
- Strings are length-prefixed (4-byte length, then data)
- Uses io.ReadFull for reliable reading
- Simple, no padding handling (simplified implementation)

## RPC Message Decoding

### DecodeRPCCall

The `DecodeRPCCall` function decodes an RPC call from a reader:

```go
func DecodeRPCCall(r io.Reader) (*RPCCall, error) {
    call := &RPCCall{}

    // Decode header
    xid, err := xdrDecodeUint32(r)
    if err != nil {
        return nil, fmt.Errorf("failed to decode XID: %v", err)
    }
    call.Header.Xid = xid

    msgType, err := xdrDecodeUint32(r)
    if err != nil {
        return nil, fmt.Errorf("failed to decode message type: %v", err)
    }
    if msgType != RPC_CALL {
        return nil, fmt.Errorf("expected RPC call, got message type %d", msgType)
    }
    call.Header.MsgType = msgType

    // Decode RPC version, program, version, and procedure
    if call.Header.RPCVersion, err = xdrDecodeUint32(r); err != nil {
        return nil, fmt.Errorf("failed to decode RPC version: %v", err)
    }
    if call.Header.Program, err = xdrDecodeUint32(r); err != nil {
        return nil, fmt.Errorf("failed to decode program: %v", err)
    }
    if call.Header.Version, err = xdrDecodeUint32(r); err != nil {
        return nil, fmt.Errorf("failed to decode version: %v", err)
    }
    if call.Header.Procedure, err = xdrDecodeUint32(r); err != nil {
        return nil, fmt.Errorf("failed to decode procedure: %v", err)
    }

    // Decode credential
    if call.Credential.Flavor, err = xdrDecodeUint32(r); err != nil {
        return nil, fmt.Errorf("failed to decode credential flavor: %v", err)
    }
    credLen, err := xdrDecodeUint32(r)
    if err != nil {
        return nil, fmt.Errorf("failed to decode credential length: %v", err)
    }
    call.Credential.Body = make([]byte, credLen)
    if _, err = io.ReadFull(r, call.Credential.Body); err != nil {
        return nil, fmt.Errorf("failed to read credential body: %v", err)
    }

    // Decode verifier
    if call.Verifier.Flavor, err = xdrDecodeUint32(r); err != nil {
        return nil, fmt.Errorf("failed to decode verifier flavor: %v", err)
    }
    verLen, err := xdrDecodeUint32(r)
    if err != nil {
        return nil, fmt.Errorf("failed to decode verifier length: %v", err)
    }
    call.Verifier.Body = make([]byte, verLen)
    if _, err = io.ReadFull(r, call.Verifier.Body); err != nil {
        return nil, fmt.Errorf("failed to read verifier body: %v", err)
    }

    return call, nil
}
```

**Flow:**
1. Read and validate XID (transaction ID)
2. Read and validate message type (must be RPC_CALL)
3. Read RPC version, program, version, and procedure numbers
4. Read credential flavor and body
5. Read verifier flavor and body
6. Return populated RPCCall struct

## RPC Message Encoding

### EncodeRPCReply

The `EncodeRPCReply` function encodes an RPC reply to a writer:

```go
func EncodeRPCReply(w io.Writer, reply *RPCReply) error {
    // Encode header
    if err := xdrEncodeUint32(w, reply.Header.Xid); err != nil {
        return fmt.Errorf("failed to encode XID: %v", err)
    }
    if err := xdrEncodeUint32(w, RPC_REPLY); err != nil {
        return fmt.Errorf("failed to encode message type: %v", err)
    }

    // Encode reply status
    if err := xdrEncodeUint32(w, reply.Status); err != nil {
        return fmt.Errorf("failed to encode reply status: %v", err)
    }

    // Encode verifier
    if err := xdrEncodeUint32(w, reply.Verifier.Flavor); err != nil {
        return fmt.Errorf("failed to encode verifier flavor: %v", err)
    }
    if err := xdrEncodeUint32(w, uint32(len(reply.Verifier.Body))); err != nil {
        return fmt.Errorf("failed to encode verifier length: %v", err)
    }
    if _, err := w.Write(reply.Verifier.Body); err != nil {
        return fmt.Errorf("failed to write verifier body: %v", err)
    }

    // Encode reply data based on procedure type
    if reply.Data != nil {
        switch data := reply.Data.(type) {
        case []byte:
            // Raw byte data (pre-encoded)
            _, err := w.Write(data)
            return err
        case *NFSAttrs:
            // File attributes
            return encodeFileAttributes(w, data)
        case string:
            // String data (mainly for error messages)
            return xdrEncodeString(w, data)
        case uint32:
            // Status or error code
            return xdrEncodeUint32(w, data)
        default:
            // If no specific encoding is provided, assume data is already encoded
            if dataBytes, ok := data.([]byte); ok {
                _, err := w.Write(dataBytes)
                return err
            }
        }
    }

    return nil
}
```

**Flow:**
1. Write XID and message type (RPC_REPLY)
2. Write reply status
3. Write verifier flavor, length, and body
4. Write reply data based on type:
   - Raw bytes: write directly
   - NFSAttrs: encode using encodeFileAttributes
   - String: encode using xdrEncodeString
   - uint32: encode using xdrEncodeUint32
   - Default: assume pre-encoded and write directly

## Design Characteristics

### Function-Based Approach
- Simple helper functions rather than classes
- Direct encoding/decoding without complex abstractions
- Easy to understand and maintain

### Struct-Based Messages
- Uses Go structs for message representation
- Type-safe message handling
- Clear data structure definitions

### Minimal Abstractions
- No complex encoder/decoder classes
- No buffering or pooling in the RPC layer
- Direct I/O operations

### Flexible Data Handling
- Uses interface{} for reply data
- Type switching for different data types
- Support for pre-encoded data

## Integration with NFS

The RPC implementation integrates with NFS protocol handling:

- **Server**: Uses DecodeRPCCall to parse incoming requests
- **Handlers**: Process NFS operations based on procedure numbers
- **Response**: Uses EncodeRPCReply to send results back to clients

## Error Handling

The implementation includes comprehensive error handling:

- Detailed error messages with context
- Validation of message types and fields
- Proper error propagation through the call stack
- Format strings for clear error reporting

## Summary

ABSNFS's RPC implementation is:

- **Simple**: Function-based helpers and struct-based messages
- **Direct**: Minimal abstractions over binary I/O
- **Clear**: Easy to understand data flow
- **Practical**: Focuses on NFSv3 needs without over-engineering

This straightforward approach makes the code easy to maintain and debug while providing all the functionality needed for NFS protocol support.
