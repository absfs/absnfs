# Rate Limiting Implementation - Code Locations & Snippets

## 1. Primary Integration Point: Server Connection Handler

### File: `server.go`
### Function: `handleConnection()` (line 333)

**Current Flow:**
```go
func (s *Server) handleConnection(conn net.Conn, procHandler *NFSProcedureHandler) {
    defer conn.Close()
    
    for {
        select {
        case <-s.ctx.Done():
            return
        default:
            // Line 348: Set read deadline
            if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
                return
            }
            
            // Line 353: Read RPC call
            call, body, readErr := s.readRPCCall(conn)
            // ...
            
            // Line 363: Update connection activity
            s.updateConnectionActivity(conn)
            
            // Line 365-385: Extract client IP for auth context
            authCtx := &AuthContext{
                Credential: &call.Credential,
            }
            if remoteAddr := conn.RemoteAddr(); remoteAddr != nil {
                if tcpAddr, ok := remoteAddr.(*net.TCPAddr); ok {
                    authCtx.ClientIP = tcpAddr.IP.String()
                    authCtx.ClientPort = tcpAddr.Port
                }
            }
            
            // *** RATE LIMITING POINT #1 SHOULD GO HERE ***
            // After IP extraction, before HandleCall
            
            // Line 393: Process with worker pool or directly
            reply, handleErr := procHandler.HandleCall(call, body, authCtx)
            // ...
        }
    }
}
```

**Rate Limiter Integration:**
```go
// After line 385, before calling HandleCall:

// Check rate limit for this client IP
if !s.rateLimiter.Allow(authCtx.ClientIP) {
    // Send rate limit error response
    reply := &RPCReply{
        Header: call.Header,
        Status: MSG_DENIED, // or custom rate-limit status
    }
    s.writeRPCReply(conn, reply)
    continue
}
```

---

## 2. Secondary Integration Point: RPC Handler

### File: `nfs_handlers.go`
### Function: `HandleCall()` (line 27)

**Current Flow:**
```go
func (h *NFSProcedureHandler) HandleCall(call *RPCCall, body io.Reader, 
    authCtx *AuthContext) (*RPCReply, error) {
    
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()
    
    reply := &RPCReply{
        Header: call.Header,
        Status: MSG_ACCEPTED,
        // ...
    }
    
    // Line 42: Auth validation
    authResult := ValidateAuthentication(authCtx, h.server.handler.options)
    if !authResult.Allowed {
        reply.Status = MSG_DENIED
        // ...
        return reply, nil
    }
    
    // *** RATE LIMITING POINT #2 COULD GO HERE ***
    // Per-operation type limiting
    
    // Line 71: Dispatch to handler
    switch call.Header.Program {
    case MOUNT_PROGRAM:
        result, err = h.handleMountCall(call, body, reply)
    case NFS_PROGRAM:
        result, err = h.handleNFSCall(call, body, reply)
    // ...
    }
}
```

**Rate Limiter Integration for Operation Types:**
```go
// Before line 71 (dispatch), after auth validation:

// Check per-operation-type rate limit
opType := getOperationType(call.Header.Program, call.Header.Procedure)
if !h.operationRateLimiter.Allow(authCtx.ClientIP, opType) {
    reply.Status = MSG_DENIED
    return reply, nil
}
```

---

## 3. Connection Management State

### File: `server.go`
### Struct: `Server` (line 27)

**Current State:**
```go
type Server struct {
    options        ServerOptions
    handler        *AbsfsNFS
    listener       net.Listener
    logger         *log.Logger
    ctx            context.Context
    cancel         context.CancelFunc
    wg             sync.WaitGroup
    acceptErrs     int
    
    // Connection management (line 38-40)
    connMutex      sync.Mutex
    activeConns    map[net.Conn]time.Time
    connCount      int
    
    // *** SHOULD ADD: ***
    // rateLimiter   *RateLimiter
    // ipConnCounts  map[string]int      // per-IP connection counter
}
```

---

## 4. Authentication Context

### File: `auth.go`
### Struct: `AuthContext` (line 10)

**Current Structure:**
```go
type AuthContext struct {
    ClientIP   string            // Client IP address
    ClientPort int               // Client port number
    Credential *RPCCredential    // RPC credential
    AuthSys    *AuthSysCredential
}
```

**Can be reused for rate limiting tracking:**
```go
// This struct is already passed through the request chain
// Can add rate limit state without changing signature
```

---

## 5. Export Options for Configuration

### File: `types.go`
### Struct: `ExportOptions` (line 33)

**Current Structure:**
```go
type ExportOptions struct {
    ReadOnly    bool
    Secure      bool
    AllowedIPs  []string
    Squash      string
    // ... other options (MaxConnections, IdleTimeout, etc.)
}
```

**Should Add:**
```go
// Rate limiting options
RateLimitingEnabled bool
// Global limits
GlobalRequestsPerSec int
// Per-IP limits
PerIPRequestsPerSec int
PerIPConnectionLimit int
// Per-operation limits
ReadLimitPerSec int
WriteLimitPerSec int
ReaddirLimitPerSec int
MountLimitPerMin int
// File handle limits
MaxHandlesPerIP int
GlobalMaxHandles int
```

---

## 6. Metrics Collector Extension

### File: `metrics.go`
### Struct: `MetricsCollector` (line 63)

**Current Structure:**
```go
type MetricsCollector struct {
    mutex          sync.RWMutex
    metrics        NFSMetrics
    attrCacheHits  uint64
    attrCacheMisses uint64
    // ...
}
```

**Should Add:**
```go
type MetricsCollector struct {
    // ... existing fields ...
    
    // Rate limiting metrics
    rateLimitedRequests  map[string]uint64  // per IP
    rateLimitExceeded    uint64              // global
    suspiciousIPs        map[string]int      // violation count per IP
    requestsPerSecond    map[string]float64  // tracked per IP
}
```

**Add Methods:**
```go
func (m *MetricsCollector) RecordRateLimitViolation(clientIP string, opType string) {
    // Increment counters
}

func (m *MetricsCollector) GetRequestsPerSecond(clientIP string) float64 {
    // Return rate for this IP
}
```

---

## 7. File Handle Allocation Point

### File: `types.go`
### Method: `FileHandleMap.Allocate()` (implied)

**Current Implementation (need to find exact location):**
```go
// In FileHandleMap struct
func (f *FileHandleMap) Allocate(file absfs.File) uint64 {
    f.Lock()
    defer f.Unlock()
    
    f.lastHandle++
    f.handles[f.lastHandle] = file
    
    return f.lastHandle
}
```

**Should Add Rate Limiting:**
```go
func (f *FileHandleMap) Allocate(file absfs.File, clientIP string) (uint64, error) {
    f.Lock()
    defer f.Unlock()
    
    // Check per-IP handle limit
    if f.ipHandleCounts[clientIP] >= f.maxHandlesPerIP {
        return 0, fmt.Errorf("file handle limit exceeded for IP %s", clientIP)
    }
    
    f.lastHandle++
    f.handles[f.lastHandle] = file
    f.ipHandleCounts[clientIP]++
    
    return f.lastHandle, nil
}
```

---

## 8. Mount Operation Handler

### File: `mount_handlers.go`
### Function: `handleMountCall()` (line 10)

**Current MNT Handler (line 21-49):**
```go
case 1: // MNT
    mountPath, err := xdrDecodeString(body)
    if err != nil {
        // ... error handling
        return reply, nil
    }
    
    // *** RATE LIMITING SHOULD GO HERE ***
    // Check: Is client IP rate-limited for MNT operations?
    // Suggested: Max 10 MNT operations per IP per minute
    
    node, err := h.server.handler.Lookup(mountPath)
    if err != nil {
        // ... error
    }
    
    handle := h.server.handler.fileMap.Allocate(node)
    // ... encode response
```

**Rate Limiter Integration:**
```go
// Before h.server.handler.Lookup(mountPath):

clientIP := authCtx.ClientIP  // already have this
if !h.server.mountRateLimiter.Allow(clientIP) {
    var buf bytes.Buffer
    xdrEncodeUint32(&buf, NFSERR_DQUOT) // Or appropriate error
    reply.Data = buf.Bytes()
    reply.Status = MSG_ACCEPTED
    return reply, nil
}
```

---

## 9. Operation Type Detection

### File: `nfs_operations.go`
### Switch Statement (line 22-1661)

**NFS Operations by Procedure Code:**
```go
switch call.Header.Procedure {
case NFSPROC3_NULL:       // 0 - NULL (no limit needed)
case NFSPROC3_GETATTR:    // 1 - GETATTR (cheap, low limit)
case NFSPROC3_SETATTR:    // 2 - SETATTR (moderate)
case NFSPROC3_LOOKUP:     // 3 - LOOKUP (cheap, no limit)
case NFSPROC3_READ:       // 6 - READ (rate limit if > 64KB)
case NFSPROC3_WRITE:      // 7 - WRITE (rate limit if > 64KB)
case NFSPROC3_CREATE:     // 8 - CREATE (rate limit)
case NFSPROC3_REMOVE:     // 12 - REMOVE (rate limit)
case NFSPROC3_RENAME:     // 14 - RENAME (rate limit)
case NFSPROC3_READDIR:    // 16 - READDIR (strict rate limit)
}
```

**Helper Function to Add:**
```go
func getOperationType(program, procedure uint32) string {
    if program == NFS_PROGRAM {
        switch procedure {
        case NFSPROC3_READ: return "READ"
        case NFSPROC3_WRITE: return "WRITE"
        case NFSPROC3_READDIR: return "READDIR"
        case NFSPROC3_LOOKUP: return "LOOKUP"
        case NFSPROC3_CREATE: return "CREATE"
        case NFSPROC3_REMOVE: return "REMOVE"
        case NFSPROC3_RENAME: return "RENAME"
        default: return "OTHER"
        }
    } else if program == MOUNT_PROGRAM {
        switch procedure {
        case 1: return "MNT"
        case 3: return "UMNT"
        default: return "MOUNT_OTHER"
        }
    }
    return "UNKNOWN"
}
```

---

## 10. Worker Pool Integration Point

### File: `worker_pool.go`
### Method: `Submit()` (line 107)

**Current Implementation:**
```go
func (p *WorkerPool) Submit(execute func() interface{}) chan interface{} {
    if atomic.LoadInt32(&p.running) == 0 {
        return nil
    }
    
    resultChan := make(chan interface{}, 1)
    task := Task{
        Execute:    execute,
        ResultChan: resultChan,
        startTime:  time.Now(),
    }
    
    // Try to submit with 50ms timeout
    select {
    case p.taskQueue <- task:
        return resultChan
    case <-time.After(50 * time.Millisecond):
        close(resultChan)
        return nil  // Task rejected due to queue full
    }
}
```

**Alternative Rate Limiting Point:**
```go
// Could add optional rate limiting here:
// - Check if client has queued too many tasks already
// - Implement per-client worker limits
// - But primary point (server.go) is better
```

---

## Implementation Checklist

- [ ] Create rate limiter implementation file: `rate_limiter.go`
- [ ] Add `RateLimiter` interface with:
  - `Allow(clientIP string) bool`
  - `AllowOperation(clientIP, opType string) bool`
  - `AllowFileHandleAllocation(clientIP string) bool`
- [ ] Implement token bucket rate limiter
- [ ] Add rate limiter to `Server` struct
- [ ] Add rate limiter to `AbsfsNFS` struct
- [ ] Integrate at connection handler (server.go:333)
- [ ] Integrate at operation dispatch (nfs_handlers.go:27)
- [ ] Integrate at mount handler (mount_handlers.go:10)
- [ ] Integrate at file handle allocation
- [ ] Add configuration options to `ExportOptions`
- [ ] Extend `MetricsCollector` for rate limit metrics
- [ ] Add tests for rate limiting behavior
- [ ] Add logging for rate limit violations
- [ ] Document rate limiting configuration

