# Enterprise NFS Design for absnfs

## Overview

This document describes the changes needed to make absnfs work as an enterprise-grade NFSv3 server that is compatible with standard NFS clients (macOS, Linux, Windows).

## Current State

The current absnfs implementation has:
- NFSv3 operations (LOOKUP, READ, WRITE, CREATE, etc.)
- MOUNT protocol handlers
- XDR encoding/decoding
- Authentication (AUTH_SYS)
- Rate limiting, caching, and other enterprise features

### What's Missing

The implementation is missing critical protocol-level features that prevent it from working with standard NFS clients:

1. **RPC Record Marking (RFC 1831)** - NFS over TCP requires a 4-byte fragment header
2. **Portmapper/rpcbind (port 111)** - Standard RPC service discovery
3. **Proper TCP framing** - Current implementation reads raw XDR without fragment headers

## Protocol Requirements

### 1. RPC Record Marking (RFC 1831 Section 10)

NFS over TCP uses "record marking" where each RPC message is preceded by a 4-byte header:

```
+--------+--------+--------+--------+
|  Last  |     Fragment Length      |
|  Frag  |     (31 bits)            |
+--------+--------+--------+--------+
```

- Bit 31 (MSB): Last fragment flag (1 = last fragment)
- Bits 0-30: Fragment length in bytes

Example for a 100-byte message:
```
0x80000064  (0x80000000 | 100)
```

### 2. Portmapper/rpcbind (Port 111)

Standard NFS clients expect to query portmapper to discover:
- MOUNT daemon port (program 100005)
- NFS daemon port (program 100003)

Portmapper protocol (program 100000, version 2):

| Procedure | Name      | Description                    |
|-----------|-----------|--------------------------------|
| 0         | NULL      | Null procedure                 |
| 1         | SET       | Register a service             |
| 2         | UNSET     | Unregister a service           |
| 3         | GETPORT   | Get port for program/version   |
| 4         | DUMP      | List all registered services   |
| 5         | CALLIT    | Call procedure indirectly      |

### 3. Service Architecture

Enterprise NFS servers typically run:

```
Port 111  - Portmapper (rpcbind)
Port 2049 - NFS daemon (nfsd)
Port 635  - Mount daemon (mountd) - or dynamic port registered with portmapper
```

## Implementation Plan

### Phase 1: RPC Record Marking

Add record marking wrapper around TCP connections:

**New Files:**
- `rpc_transport.go` - TCP transport with record marking

**Changes:**
- `server.go` - Use new transport wrapper
- `rpc_types.go` - Update encode/decode functions

```go
// RecordMarkingReader wraps a reader with RPC record marking support
type RecordMarkingReader struct {
    r            io.Reader
    fragmentBuf  []byte
    fragmentPos  int
    fragmentLen  int
    lastFragment bool
}

// RecordMarkingWriter wraps a writer with RPC record marking support
type RecordMarkingWriter struct {
    w           io.Writer
    maxFragment int
}
```

### Phase 2: Portmapper Service

Implement a minimal portmapper that registers MOUNT and NFS services:

**New Files:**
- `portmapper.go` - Portmapper server implementation

**Key Functions:**
```go
// StartPortmapper starts the portmapper service on port 111
func (s *Server) StartPortmapper() error

// RegisterService registers an RPC service with portmapper
func (pm *Portmapper) RegisterService(prog, vers, prot, port uint32) error

// HandleGetPort handles GETPORT requests
func (pm *Portmapper) HandleGetPort(prog, vers, prot uint32) uint32
```

### Phase 3: Separate Mount Daemon

Move MOUNT protocol to a separate listener:

**Changes:**
- `server.go` - Add separate listener for mountd
- `mount_handlers.go` - Update to work with separate daemon

**Configuration:**
```go
type ServerOptions struct {
    // ... existing fields ...
    MountPort    int  // Port for mount daemon (0 = dynamic)
    UsePortmapper bool // Whether to use portmapper (default: true)
}
```

## Message Flow

### Current (Broken) Flow
```
Client                           Server
  |                                |
  |------ mount_nfs query -------->| (Port 111 - no response)
  |                                |
  X (Connection refused)           |
```

### New (Working) Flow
```
Client                           Server
  |                                |
  |-- GETPORT(MOUNT) ------------>| (Port 111 - Portmapper)
  |<-- Port 635 ------------------|
  |                                |
  |-- MNT "/" ------------------->| (Port 635 - Mountd)
  |<-- File Handle ---------------|
  |                                |
  |-- NFS Operations ------------>| (Port 2049 - NFSd)
  |<-- Responses -----------------|
```

## File Changes Summary

| File | Changes |
|------|---------|
| `rpc_transport.go` | **NEW** - Record marking transport |
| `portmapper.go` | **NEW** - Portmapper service |
| `server.go` | Add portmapper, separate mountd |
| `rpc_types.go` | Use record marking for encode/decode |
| `types.go` | Add MountPort, UsePortmapper options |

## Testing

1. **Unit Tests:**
   - Record marking read/write
   - Portmapper GETPORT responses
   - Fragment handling

2. **Integration Tests:**
   - `showmount -e localhost`
   - `rpcinfo -p localhost`
   - `sudo mount_nfs localhost:/ /mnt`

3. **Platform Tests:**
   - macOS: `mount_nfs`
   - Linux: `mount.nfs`
   - Windows: NFS client

## macOS-Specific Notes

macOS NFS client (`mount_nfs`) requires:
1. Portmapper on port 111 OR explicit `mountport=` and `port=` options
2. Proper record marking on TCP connections
3. `resvport` option for privileged port requirement
4. NFSv3 (vers=3) for best compatibility

Manual mount without portmapper:
```bash
sudo mount_nfs -o resvport,nolocks,vers=3,mountport=635,port=2049 localhost:/ /mnt
```

## References

- RFC 1831 - RPC: Remote Procedure Call Protocol Specification Version 2
- RFC 1833 - Binding Protocols for ONC RPC Version 2
- RFC 1094 - NFS: Network File System Protocol Specification
- RFC 1813 - NFS Version 3 Protocol Specification
