# NFS Server Architecture and Rate Limiting Analysis

## Executive Summary

This is an NFS (Network File System) v3 implementation in Go that exports a filesystem over the network. The server has recent security enhancements including authentication enforcement, path traversal protection, and integer overflow fixes. Currently, there is **no rate limiting or DDoS protection mechanism** in place, making this an ideal area for enhancement.

---

## 1. How the NFS Server Handles Incoming Requests

### Request Flow Diagram
```
Client Request → TCP Connection → RPC Decoder → Auth Validation → 
Procedure Handler → NFS Operation → Response Encoder → Reply
```

### Detailed Request Handling Process

#### Step 1: Accept Connection (server.go:238-331)
- **Location:** `acceptLoop()` in `server.go`
- **What happens:**
  - Server listens on TCP port (default 2049)
  - Accepts incoming connections with 1-second timeout
  - Registers connection in active connections map
  - Applies TCP settings (keepalive, nodelay, buffer sizes)
  - Spawns a goroutine to handle the connection

#### Step 2: Read RPC Call (server.go:333-434)
- **Location:** `handleConnection()` in `server.go`
- **Process:**
  - Reads RPC call header and body from TCP connection
  - Sets read deadline (5 seconds)
  - Extracts client IP and port for authentication context
  - Updates connection activity timestamp

#### Step 3: Authentication Validation (nfs_handlers.go:27-125)
- **Location:** `HandleCall()` in `nfs_handlers.go`
- **Security Checks:**
  1. Validates client IP against allowed IPs (CIDR support)
  2. Checks for secure port requirement (< 1024 if Secure=true)
  3. Validates credential flavor (AUTH_NONE, AUTH_SYS)
  4. Applies user squashing (root, all, none)
  5. Records authentication failures in metrics

- **Code Location:** `auth.go` (lines 25-83)
```go
ValidateAuthentication(ctx *AuthContext, options ExportOptions) *AuthResult
```

#### Step 4: Procedure Dispatch (nfs_handlers.go:71-85)
- Routes to either:
  - `handleMountCall()` (MOUNT protocol v3)
  - `handleNFSCall()` (NFS protocol v3)

#### Step 5: Operation Execution
- **NFS Operations:** GETATTR, SETATTR, LOOKUP, READ, WRITE, CREATE, REMOVE, RENAME, READDIR, etc.
- **Mount Operations:** MNT, DUMP, UMNT
- **Worker Pool:** If enabled, task executed via worker pool with 50ms timeout
- **Direct Execution:** Falls back to direct execution if pool is full

#### Step 6: Response Encoding & Sending (server.go:422-428)
- Encodes RPC reply with XDR format
- Sets write deadline (5 seconds)
- Sends response back to client

#### Step 7: Connection Cleanup
- Unregisters connection from active connections map
- Closes network socket
- Decrements connection counter

---

## 2. Main Entry Points for Request Processing

### Server Entry Points

| Entry Point | File | Function | Purpose |
|---|---|---|---|
| **TCP Listen** | server.go | `NewServer()` → `Listen()` | Creates server and starts listening |
| **Accept Loop** | server.go:238 | `acceptLoop()` | Main connection acceptance loop |
| **Connection Handler** | server.go:333 | `handleConnection()` | Processes requests from a single connection |
| **RPC Decoder** | rpc_types.go | `DecodeRPCCall()` | Decodes incoming RPC messages |
| **Auth Validator** | auth.go | `ValidateAuthentication()` | Validates credentials |
| **Mount Handler** | mount_handlers.go | `handleMountCall()` | Processes MOUNT protocol calls |
| **NFS Handler** | nfs_handlers.go | `handleNFSCall()` | Processes NFS protocol calls |
| **Procedure Dispatcher** | nfs_handlers.go:22-1661 | switch statement | Routes to specific NFS operations |

### Key Handler Signatures

**Primary Request Handler:**
```go
// nfs_handlers.go:27
func (h *NFSProcedureHandler) HandleCall(
    call *RPCCall, 
    body io.Reader, 
    authCtx *AuthContext
) (*RPCReply, error)
```

**Mount Call Handler:**
```go
// mount_handlers.go:10
func (h *NFSProcedureHandler) handleMountCall(
    call *RPCCall, 
    body io.Reader, 
    reply *RPCReply
) (*RPCReply, error)
```

**NFS Call Handler:**
```go
// nfs_operations.go:12
func (h *NFSProcedureHandler) handleNFSCall(
    call *RPCCall, 
    body io.Reader, 
    reply *RPCReply
) (*RPCReply, error)
```

### Timeout Mechanisms Already in Place

1. **Operation Timeout:** 2 seconds (nfs_handlers.go:29)
2. **Read Timeout:** 5 seconds (server.go:338)
3. **Write Timeout:** 5 seconds (server.go:417)
4. **Worker Pool Task Timeout:** 50ms (worker_pool.go:127)
5. **Idle Connection Timeout:** Configurable (default 5 minutes)

---

## 3. Current Security Mechanisms in Place

### 3.1 Authentication & Authorization (auth.go)

**IP-Based Access Control:**
- Whitelist of allowed client IPs (CIDR notation supported)
- Blocks connections from unauthorized IPs
- Example: `AllowedIPs: []string{"192.168.1.0/24", "10.0.0.5"}`

**Credential Validation:**
- AUTH_NONE: Mapped to nobody/nobody (UID 65534, GID 65534)
- AUTH_SYS: Unix-style credentials with UID/GID parsing
- Unsupported flavors: Rejected

**Secure Port Enforcement:**
- Option: `Secure: true`
- Requires clients to connect from privileged ports (< 1024)

**User ID Squashing:**
- `root`: Maps UID 0 to nobody
- `all`: Maps all users to nobody
- `none`: No mapping

### 3.2 Connection Management (server.go:72-151)

**Connection Limits:**
- `MaxConnections`: Configurable limit (default 100)
- Rejects new connections when limit reached

**Idle Connection Cleanup:**
- `IdleTimeout`: Default 5 minutes
- Periodic cleanup loop (every 30 seconds or IdleTimeout/2)
- Closes connections inactive beyond timeout

**TCP Configuration:**
- `TCPKeepAlive`: Detects dead connections (enabled by default)
- `TCPNoDelay`: Reduces latency (enabled by default)
- `SendBufferSize`: Default 256KB
- `ReceiveBufferSize`: Default 256KB

### 3.3 Path Security (operations.go:49-85)

**Path Traversal Protection:**
- Function: `sanitizePath(basePath, name string)`
- Rejects path separators in names
- Rejects ".." and "." references
- Uses `filepath.Clean()` and prefix validation
- Prevents directory traversal attacks

**Applied to:**
- CREATE operation
- REMOVE operation  
- RENAME operation
- READDIR operation

### 3.4 Input Validation (nfs_operations.go:278-284)

**Integer Overflow Protection:**
- Validates `offset + count` doesn't overflow uint64
- Applied to READ and WRITE operations
- Prevents malicious large requests

**XDR String Validation:**
- Validates string length in RPC encoding
- Prevents DoS via oversized strings

### 3.5 Caching & Memory Management

**Attribute Cache:**
- `AttrCacheTimeout`: 5 seconds (prevents stale data)
- `AttrCacheSize`: 10,000 entries max
- Cache invalidation on writes

**Memory Monitoring:**
- `AdaptToMemoryPressure`: Optional memory monitoring
- `MemoryHighWatermark`: 80% (triggers reduction)
- `MemoryLowWatermark`: 60% (target during pressure)
- Automatic cache eviction on memory pressure

**Read-Ahead Buffering:**
- `ReadAheadMaxFiles`: 100 files max
- `ReadAheadMaxMemory`: 100MB max
- LRU eviction policy

### 3.6 Metrics & Monitoring (metrics.go)

**Operation Tracking:**
- Counts: READ, WRITE, LOOKUP, GETATTR, CREATE, REMOVE, RENAME, MKDIR, RMDIR, READDIR, ACCESS
- Total operations counter

**Error Tracking:**
- Auth failures counter
- Access violations counter
- Stale handle counter
- Resource errors counter

**Performance Metrics:**
- Read/write latency (avg, max, p95)
- Cache hit rates (attr cache, read-ahead, directory)
- Active connections count
- Rejected connections count

**Health Status:**
- `IsHealthy()`: Checks error rate and latency
- Marked unhealthy if: error rate > 50% OR p95 latency > 5 seconds

---

## 4. Where Rate Limiting Would Be Most Effective

### 4.1 Vulnerability Analysis: DDoS Attack Vectors

#### Vector 1: Connection Flooding
**Current Protection:** `MaxConnections` limit (100 by default)
**Gap:** No per-IP connection limit
**Risk Level:** MEDIUM
- Attacker can exhaust all 100 connections from one IP
- Other legitimate clients are then blocked

#### Vector 2: Request Flooding (Single Connection)
**Current Protection:** None
**Gap:** No rate limiting per client IP or per connection
**Risk Level:** HIGH
- Attacker opens 1 connection and sends rapid requests
- Each request consumes minimal resources
- Can saturate worker pool or CPU

#### Vector 3: Large Payload Attacks
**Current Protection:** `TransferSize` limit (64KB by default)
**Gap:** Can still send multiple large reads/writes
**Risk Level:** MEDIUM
- Attacker can exhaust memory or I/O bandwidth
- No limit on total bytes per time window

#### Vector 4: Expensive Operations
**Current Protection:** 2-second operation timeout
**Gap:** Multiple operations combined still consume resources
**Risk Level:** MEDIUM-HIGH
- Operations like READDIR, bulk LOOKUP can be CPU intensive
- No differentiation between operation types

#### Vector 5: Authentication/Mount Flooding
**Current Protection:** None for mount operations
**Gap:** MOUNT procedure (MNT, DUMP, UMNT) not rate limited
**Risk Level:** MEDIUM
- Repeated mount/unmount requests can allocate file handles
- File handle table not bounded

### 4.2 Recommended Rate Limiting Points (Priority Order)

#### Priority 1: Per-IP Request Rate Limiting (server.go)
**Location:** `handleConnection()` function
**Strategy:** Token bucket per client IP
**Implementation Point:**
- After client IP extraction (server.go:369-385)
- Before authentication validation (server.go:41)
- Check: X requests per Y seconds per IP

```
Recommended Limits:
- Global: 10,000 ops/sec
- Per-IP: 1,000 ops/sec  
- Per-Connection: 100 ops/sec
```

**Benefits:**
- Prevents single IP from flooding server
- Protects against distributed attacks when they concentrate
- Minimal performance impact

**Challenges:**
- Needs distributed state if using load balancer
- Must handle connection handoff

#### Priority 2: Per-Connection Rate Limiting (server.go)
**Location:** `handleConnection()` or `handleCall()`
**Strategy:** Sliding window per TCP connection
**Threshold:** ~100 requests per second per connection

**Benefits:**
- Fairness between concurrent clients
- Prevents single connection from dominating
- Easy to implement per goroutine

**Challenges:**
- Legitimate high-performance clients may be affected
- Needs configurable limits

#### Priority 3: Per-Operation Type Rate Limiting (nfs_handlers.go)
**Location:** Before operation handler execution
**Strategy:** Exponential backoff for expensive operations
**Target Operations:**
- READ with large counts (> 64KB)
- WRITE with large counts
- READDIR on large directories
- Expensive AUTH_SYS parsing

```
Recommended Limits:
- READ (>64KB): 100 ops/sec per IP
- WRITE (>64KB): 50 ops/sec per IP
- READDIR: 20 ops/sec per IP
- LOOKUP: No limit (cheap operation)
```

**Benefits:**
- Targets expensive operations
- Allows cheap operations to flow
- Granular control

#### Priority 4: File Handle Allocation Rate Limiting (types.go)
**Location:** `FileHandleMap.Allocate()`
**Strategy:** Limit handles per IP or global pool
**Current Issue:** Unbounded file handle allocation

```
Recommended Limits:
- Per-IP: 10,000 handles max
- Global: 1,000,000 handles max
```

**Benefits:**
- Prevents handle table exhaustion
- Protects memory

#### Priority 5: Mount Operation Rate Limiting (mount_handlers.go)
**Location:** `handleMountCall()` function
**Strategy:** Rate limit MNT/UMNT operations
**Threshold:** 10 mount operations per IP per minute

**Benefits:**
- Prevents rapid mount/unmount cycling
- Protects against mount table DoS

### 4.3 Integration Points for Rate Limiting Middleware

#### Option A: Server Level (server.go:333)
- In `handleConnection()` after client IP extraction
- Affects all requests on a connection
- Most efficient for global limits

#### Option B: Handler Level (nfs_handlers.go:27)
- In `HandleCall()` after auth validation
- Can use authenticated user info in limits
- Better for per-user limits

#### Option C: Worker Pool Level (worker_pool.go:107)
- In `Submit()` method
- Limits concurrent operation execution
- Already has queue with 50ms timeout

### 4.4 Monitoring & Observability Needed

**Metrics to Add:**
- `RateLimitedRequests` counter (by IP)
- `RequestsPerSecond` gauge (per IP, per op type)
- `RateLimitExceededErrors` counter
- `BucketFillRate` gauge (for token bucket)

**Logging:**
- Log when rate limit exceeded (client IP, operation, limit)
- Log sustained attacks (IP with repeated violations)
- Alert thresholds for suspicious patterns

**Existing Metrics Infrastructure:**
- Already have `MetricsCollector` in metrics.go
- Already tracking operation counts
- Can extend to track per-IP statistics

---

## 5. Architecture Summary

### Request Processing Pipeline

```
┌─────────────────┐
│  TCP Listen     │ (Port 2049)
└────────┬────────┘
         │
         ▼
┌─────────────────────────────────────┐
│ Accept Connection                   │ (server.go:238)
│ - Register in active connections    │
│ - Configure TCP socket              │
│ - Spawn handler goroutine           │
└────────┬────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────┐
│ Handle Connection Loop              │ (server.go:333)
│ - Set read deadline (5s)            │
│ - Read RPC call                     │
│ - Extract client IP:port            │
│ - Update activity timestamp         │
└────────┬────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────┐
│ [RATE LIMITING POINT #1]            │ ◄── PRIMARY LOCATION
│ Per-IP request rate limiting        │
└────────┬────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────┐
│ Validate Authentication             │ (auth.go:26)
│ - Check IP whitelist                │
│ - Validate secure port              │
│ - Parse credentials                 │
│ - Apply user squashing              │
└────────┬────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────┐
│ [RATE LIMITING POINT #2]            │ ◄── AUTH LEVEL
│ Record auth metrics                 │
└────────┬────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────┐
│ Dispatch to Handler                 │ (nfs_handlers.go:71)
│ - MOUNT: handleMountCall()          │
│ - NFS:   handleNFSCall()            │
└────────┬────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────┐
│ [RATE LIMITING POINT #3]            │ ◄── OPERATION TYPE
│ Per-operation rate limiting         │
└────────┬────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────┐
│ Execute Operation                   │
│ - Validate input                    │
│ - Check permissions                 │
│ - Execute filesystem operation      │
│ - Record metrics                    │
└────────┬────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────┐
│ [RATE LIMITING POINT #4]            │ ◄── FILE HANDLE
│ Handle allocation rate limiting     │
└────────┬────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────┐
│ Encode Response                     │
│ - XDR encode reply data             │
│ - Set write deadline (5s)           │
│ - Send to client                    │
└────────┬────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────┐
│ Cleanup                             │
│ - Unregister connection if done     │
│ - Update metrics                    │
└─────────────────────────────────────┘
```

### Component Relationships

```
Server (server.go)
├── NFSProcedureHandler (nfs_handlers.go)
│   ├── handleMountCall() → Mount operations
│   └── handleNFSCall() → NFS operations
│
├── AbsfsNFS (types.go)
│   ├── FileHandleMap (types.go)
│   ├── AttrCache (cache.go)
│   ├── ReadAheadBuffer (cache.go)
│   ├── WorkerPool (worker_pool.go) ◄── Task queue, can add rate limiting
│   ├── BatchProcessor (batch.go)
│   ├── MemoryMonitor (memory_monitor.go)
│   └── MetricsCollector (metrics.go) ◄── Metrics tracking
│
└── AuthContext (auth.go)
    └── ValidateAuthentication() ◄── Auth validation point
```

---

## Key Files Reference

| File | LOC | Purpose |
|---|---|---|
| server.go | 516 | TCP server, connection handling, main loop |
| nfs_handlers.go | 125 | RPC handler, auth validation, call routing |
| nfs_operations.go | 1661 | NFS protocol implementation (READ, WRITE, etc) |
| mount_handlers.go | 81 | MOUNT protocol implementation |
| auth.go | 142 | Authentication & authorization |
| worker_pool.go | 222 | Worker pool for concurrent task execution |
| metrics.go | 406 | Metrics collection & health checks |
| types.go | 401 | Data structures & configuration |
| cache.go | 421 | Attribute & read-ahead caching |
| batch.go | 468 | Batch operation processing |
| memory_monitor.go | 176 | Memory pressure monitoring |

---

## Conclusion

The NFS server has a solid foundation with:
- ✅ Connection limits
- ✅ Per-connection timeout handling
- ✅ IP-based access control
- ✅ Path traversal protection
- ✅ Integer overflow protection
- ✅ Metrics & monitoring

However, it lacks:
- ❌ Per-IP rate limiting
- ❌ Per-operation type rate limiting
- ❌ Token bucket/sliding window mechanisms
- ❌ DDoS-specific protections

**Recommended next steps:**
1. Implement per-IP request rate limiting (Priority 1)
2. Add per-connection rate limiting (Priority 2)
3. Implement per-operation type rate limiting (Priority 3)
4. Add file handle allocation limits (Priority 4)
5. Add mount operation rate limiting (Priority 5)

