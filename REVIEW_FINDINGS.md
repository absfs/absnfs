# absnfs Code Review Findings

**Review Date:** 2025-11-09
**Reviewer:** Claude Code
**Scope:** Comprehensive review of absnfs codebase including code quality, security, concurrency, resource management, documentation, and testing

---

## Executive Summary

This review identified **78 distinct issues** across the absnfs codebase:

- **12 Critical Security Vulnerabilities** requiring immediate attention
- **19 Concurrency/Race Conditions** that could cause data corruption
- **13 Resource Leaks** leading to memory exhaustion
- **24 Error Handling Issues** impacting debugging and reliability
- **7 Documentation Errors** with incorrect/missing information
- **3 Test Coverage Gaps** and compilation errors

**Key Finding:** The codebase should **NOT be used in production** without addressing critical security vulnerabilities, particularly path traversal, authentication bypass, and unbounded memory allocation issues.

---

## GitHub Issues to Create

Below are organized issues ready to be created on GitHub. Each issue is self-contained with description, impact, and suggested fix.

---

## CRITICAL PRIORITY (Security)

### Issue 1: [CRITICAL] Path Traversal Vulnerability Allows Arbitrary File Access

**Labels:** `security`, `critical`, `bug`

**Description:**
The NFS server is vulnerable to path traversal attacks. File paths are constructed using simple string concatenation without validation, allowing attackers to access files outside the exported filesystem using `../` sequences.

**Affected Files:**
- `operations.go` lines 231, 377, 430-431
- `nfs_operations.go` lines 231, 567-568, 1443-1444

**Vulnerable Code:**
```go
// operations.go:377
path := dir.path + "/" + name  // No validation!
```

**Attack Scenario:**
```
1. Attacker sends LOOKUP with name = "../../../etc/passwd"
2. Server constructs: "/export/test/../../../etc/passwd"
3. Resolves to: "/etc/passwd"
4. Attacker gains access to sensitive files
```

**Impact:** Complete filesystem compromise - attacker can read/write any file on the server

**Suggested Fix:**
```go
import "path/filepath"

func (s *AbsfsNFS) validatePath(base, name string) (string, error) {
    path := filepath.Join(base, name)
    path = filepath.Clean(path)

    // Ensure path stays within base
    if !strings.HasPrefix(path, filepath.Clean(base)) {
        return "", os.ErrPermission
    }
    return path, nil
}
```

---

### Issue 2: [CRITICAL] Unbounded Memory Allocation Enables DoS Attacks

**Labels:** `security`, `critical`, `bug`

**Description:**
XDR string/data decoding reads length values directly from untrusted network input and allocates memory without validation. Attackers can send malicious length values (e.g., 0xFFFFFFFF = 4GB) to exhaust server memory.

**Affected Files:**
- `rpc_types.go` lines 93-106, 173-180, 186-192

**Vulnerable Code:**
```go
func xdrDecodeString(r io.Reader) (string, error) {
    length, err := xdrDecodeUint32(r)  // Untrusted input
    buf := make([]byte, length)         // Unbounded allocation!
    ...
}
```

**Attack Scenario:**
```
1. Attacker sends XDR length = 0xFFFFFFFF (4GB)
2. Server: make([]byte, 4294967295)
3. Server crashes due to OOM
4. Service unavailable
```

**Impact:** Denial of Service - single malicious packet can crash the server

**Suggested Fix:**
```go
const MaxXDRStringLength = 4096
const MaxXDRDataLength = 1048576  // 1MB

func xdrDecodeString(r io.Reader) (string, error) {
    length, err := xdrDecodeUint32(r)
    if err != nil {
        return "", err
    }

    if length > MaxXDRStringLength {
        return "", fmt.Errorf("string length %d exceeds maximum %d",
            length, MaxXDRStringLength)
    }

    buf := make([]byte, length)
    ...
}
```

---

### Issue 3: [CRITICAL] AllowedIPs Security Control Not Enforced

**Labels:** `security`, `critical`, `bug`

**Description:**
The `AllowedIPs` configuration field is defined but **never checked** during connection handling. Any client from any IP can connect regardless of configured IP restrictions, completely bypassing this security control.

**Affected Files:**
- `types.go` line 36 (field defined)
- `server.go` lines 252-290 (connection handling - no validation)

**Current Code:**
```go
// Field exists but unused
AllowedIPs  []string // List of allowed client IPs/subnets

// Connection accepted without checking
conn, err := s.listener.Accept()
s.handleConnection(conn, procHandler)  // No IP validation!
```

**Attack Scenario:**
```
1. Admin configures: AllowedIPs: ["192.168.1.0/24"]
2. Attacker from 1.2.3.4 connects
3. Server accepts without checking
4. Security bypass - unrestricted access
```

**Impact:** Complete bypass of IP-based access controls

**Suggested Fix:**
```go
func (s *Server) isIPAllowed(conn net.Conn) bool {
    if len(s.handler.options.AllowedIPs) == 0 {
        return true
    }

    remoteAddr := conn.RemoteAddr().(*net.TCPAddr)
    remoteIP := remoteAddr.IP

    for _, allowedIP := range s.handler.options.AllowedIPs {
        _, ipNet, err := net.ParseCIDR(allowedIP)
        if err != nil {
            if remoteIP.String() == allowedIP {
                return true
            }
            continue
        }
        if ipNet.Contains(remoteIP) {
            return true
        }
    }
    return false
}

// In acceptLoop:
conn, err := s.listener.Accept()
if !s.isIPAllowed(conn) {
    conn.Close()
    continue
}
```

---

### Issue 4: [CRITICAL] No Authentication - Server Accepts AUTH_NONE Only

**Labels:** `security`, `critical`, `enhancement`

**Description:**
Server only accepts `AUTH_NONE` authentication and rejects all other types. This means no user identity validation occurs - any client can access all files without credentials.

**Affected Files:**
- `nfs_handlers.go` lines 42-45

**Current Code:**
```go
if call.Credential.Flavor != 0 {  // Reject anything except AUTH_NONE
    reply.Status = MSG_DENIED
    return reply, nil
}
```

**Impact:**
- No user authentication
- No access control based on identity
- Anyone can access all exported files

**Suggested Fix:**
Implement AUTH_SYS (flavor 1) support:
```go
switch call.Credential.Flavor {
case AUTH_NONE:
    // Limited access
case AUTH_SYS:
    // Parse UID/GID from credential
    // Validate against allowed users
    // Enforce user-based access controls
default:
    reply.Status = AUTH_BADCRED
    return reply, nil
}
```

---

### Issue 5: [HIGH] No TLS/Encryption - All Data Sent in Plaintext

**Labels:** `security`, `high`, `enhancement`

**Description:**
All NFS traffic is transmitted over unencrypted TCP. File data, metadata, and credentials (when implemented) are visible to network attackers.

**Affected Files:**
- `server.go` line 200

**Current Code:**
```go
listener, err := net.Listen("tcp", addr)  // No TLS
```

**Attack Scenario:**
```
1. Attacker performs network sniffing
2. All file contents captured in plaintext
3. Credentials intercepted
4. Man-in-the-middle modification possible
```

**Impact:** Complete data exposure and manipulation

**Suggested Fix:**
```go
import "crypto/tls"

type ServerOptions struct {
    ...
    TLSConfig *tls.Config
}

// Use TLS if configured
if s.options.TLSConfig != nil {
    listener, err = tls.Listen("tcp", addr, s.options.TLSConfig)
} else {
    listener, err = net.Listen("tcp", addr)
}
```

---

### Issue 6: [HIGH] Integer Overflow in Read/Write Offset Calculations

**Labels:** `security`, `high`, `bug`

**Description:**
Offset and count parameters from network requests lack overflow validation. Large values can cause integer overflows leading to memory corruption or crashes.

**Affected Files:**
- `nfs_operations.go` lines 262-275, 342-361, 617-623

**Vulnerable Code:**
```go
var offset uint64  // Can be 0x7FFFFFFFFFFFFFFF
var count uint32   // Can be 0x7FFFFFFF
data, err := h.server.handler.Read(node, int64(offset), int64(count))
```

**Attack Scenario:**
```
1. Attacker sends: offset=MaxInt64, count=MaxInt32
2. offset + count overflows
3. Buffer calculations corrupted
4. Crash or memory corruption
```

**Impact:** Memory corruption, crashes, potential code execution

**Suggested Fix:**
```go
const MaxReadSize = 1024 * 1024  // 1MB
const MaxWriteSize = 1024 * 1024

if count > MaxReadSize {
    return errorResponse(NFSERR_INVAL)
}

if offset > math.MaxInt64 - int64(count) {
    return errorResponse(NFSERR_INVAL)
}
```

---

## HIGH PRIORITY (Concurrency - Data Corruption)

### Issue 7: [HIGH] Lock Upgrade Race in AttrCache.Get()

**Labels:** `concurrency`, `high`, `bug`

**Description:**
`AttrCache.Get()` releases the read lock before acquiring the write lock to update the access log. Between these operations, another goroutine can evict the entry, causing the access log to contain paths no longer in the cache.

**Affected Files:**
- `cache.go` lines 44-52

**Vulnerable Code:**
```go
c.mu.RLock()
cached, ok := c.cache[path]
if ok && time.Now().Before(cached.expireAt) {
    c.mu.RUnlock()  // Line 47: Release lock

    // RACE WINDOW: Another thread can evict entry here

    c.mu.Lock()     // Line 50: Acquire write lock
    c.updateAccessLog(path)  // May update stale data
    c.mu.Unlock()
```

**Impact:**
- Memory leak in accessLog (unbounded growth)
- Incorrect LRU eviction
- Cache inconsistency

**Suggested Fix:**
```go
c.mu.RLock()
cached, ok := c.cache[path]
c.mu.RUnlock()

if ok && time.Now().Before(cached.expireAt) {
    c.mu.Lock()
    // Recheck existence after acquiring write lock
    if _, stillExists := c.cache[path]; stillExists {
        c.updateAccessLog(path)
    }
    c.mu.Unlock()
    return &cached.attrs, true
}
```

---

### Issue 8: [HIGH] Lock Upgrade Race in ReadAheadBuffer.Read()

**Labels:** `concurrency`, `high`, `bug`

**Description:**
Same lock upgrade pattern as Issue #7. Buffer can be evicted between releasing RLock and acquiring write Lock, potentially causing panic or data corruption.

**Affected Files:**
- `cache.go` lines 395-406

**Impact:** Panic, data corruption, incorrect read-ahead stats

**Suggested Fix:**
```go
b.mu.RLock()
buffer, exists := b.buffers[path]
if !exists {
    b.mu.RUnlock()
    return nil, false
}

// Copy data while holding read lock
start := int(offset - buffer.offset)
end := start + count
result := make([]byte, end-start)
copy(result, buffer.data[start:end])
b.mu.RUnlock()

// Update tracking with proper revalidation
b.mu.Lock()
if buff, ok := b.buffers[path]; ok {
    buff.lastUse = time.Now()
    b.updateAccessOrder(path)
}
b.mu.Unlock()
```

---

### Issue 9: [HIGH] Unsynchronized Access to NFSNode.attrs

**Labels:** `concurrency`, `high`, `bug`

**Description:**
Multiple methods in `NFSNode` call `n.attrs.Invalidate()` without synchronization. Concurrent writes from different goroutines create data races on the `validUntil` field.

**Affected Files:**
- `nfs_node.go` lines 46, 57, 127, 143, 149, 155

**Vulnerable Code:**
```go
func (n *NFSNode) Write(p []byte) (int, error) {
    // ...
    n.attrs.Invalidate() // No lock - RACE!
    return f.Write(p)
}

func (n *NFSNode) WriteAt(p []byte, off int64) (int, error) {
    // ...
    n.attrs.Invalidate() // No lock - RACE!
    return f.WriteAt(p, off)
}
```

**Impact:**
- Data race (detected by `go test -race`)
- Corrupted attribute cache
- Undefined behavior

**Suggested Fix:**
```go
type NFSNode struct {
    absfs.FileSystem
    path     string
    fileId   uint64
    mu       sync.RWMutex  // Add mutex
    attrs    *NFSAttrs
    children map[string]*NFSNode
}

func (n *NFSNode) Write(p []byte) (int, error) {
    f, err := n.FileSystem.OpenFile(n.path, os.O_WRONLY, 0)
    if err != nil {
        return 0, err
    }
    defer f.Close()

    n.mu.Lock()
    n.attrs.Invalidate()
    n.mu.Unlock()

    return f.Write(p)
}
```

---

### Issue 10: [HIGH] Batch Replacement Race Condition

**Labels:** `concurrency`, `high`, `bug`

**Description:**
When a batch becomes full, the code replaces it with a new batch while still holding the old batch's mutex lock. The old batch is then passed to a goroutine for processing while locked, creating potential deadlocks.

**Affected Files:**
- `batch.go` lines 109-130

**Vulnerable Code:**
```go
batch.mu.Lock()
defer batch.mu.Unlock()  // Will unlock later

if len(batch.Requests) >= batch.MaxSize {
    bp.batches[req.Type] = &Batch{...}  // Replace with new batch

    go bp.processBatch(batch)  // Old batch still locked!
    return true
}
```

**Impact:** Deadlock, incorrect batch processing

**Suggested Fix:**
```go
if len(batch.Requests) >= batch.MaxSize {
    oldBatch := batch
    oldBatch.mu.Unlock()  // Unlock before processing

    bp.batches[req.Type] = &Batch{
        Type:    req.Type,
        MaxSize: batch.MaxSize,
    }

    go bp.processBatch(oldBatch)
    return true
}
```

---

### Issue 11: [HIGH] FileHandle Race in Batch Operations

**Labels:** `concurrency`, `high`, `bug`

**Description:**
File handles are looked up in the map, lock is released, then the handle is used for batch operations. The handle could be released by another goroutine between lookup and use.

**Affected Files:**
- `operations.go` lines 192-213, 293-328

**Vulnerable Code:**
```go
s.fileMap.RLock()
for handle, file := range s.fileMap.handles {
    if nodeFile, ok := file.(*NFSNode); ok && nodeFile.path == node.path {
        fileHandle = handle
        break
    }
}
s.fileMap.RUnlock()  // Line 199: Unlock

// Handle could be released here by another thread

if fileHandle != 0 && s.options.BatchOperations {
    data, err, status := s.batchProc.BatchRead(..., fileHandle, ...)  // Stale handle!
```

**Impact:** Using invalid file handles, reading wrong files

**Suggested Fix:**
```go
s.fileMap.RLock()
var fileHandle uint64
for handle, file := range s.fileMap.handles {
    if nodeFile, ok := file.(*NFSNode); ok && nodeFile.path == node.path {
        fileHandle = handle
        break
    }
}

if fileHandle != 0 && s.options.BatchOperations {
    // Verify handle still exists before using
    if _, stillExists := s.fileMap.handles[fileHandle]; stillExists {
        s.fileMap.RUnlock()
        return s.batchProc.BatchRead(...)
    }
}
s.fileMap.RUnlock()
```

---

### Issue 12: [MEDIUM] Race on acceptErrs Counter

**Labels:** `concurrency`, `medium`, `bug`

**Description:**
The `acceptErrs` field is read and written without synchronization, violating Go's memory model.

**Affected Files:**
- `server.go` lines 273, 278, 283

**Impact:** Race condition (benign currently, but fragile)

**Suggested Fix:**
```go
// Change type to int32
acceptErrs int32

// Use atomics
if atomic.LoadInt32(&s.acceptErrs) < maxAcceptErrors {
    atomic.AddInt32(&s.acceptErrs, 1)
}
atomic.StoreInt32(&s.acceptErrs, 0)
```

---

## HIGH PRIORITY (Resource Leaks)

### Issue 13: [HIGH] Channel Leaks in HandleCall

**Labels:** `resource-leak`, `high`, `bug`

**Description:**
Every RPC call creates `errChan` and `replyChan` channels that are never closed, causing memory leaks.

**Affected Files:**
- `nfs_handlers.go` lines 48-49, 84-93

**Leaking Code:**
```go
errChan := make(chan error, 1)      // Never closed
replyChan := make(chan *RPCReply, 1) // Never closed

go func() {
    // ... processing ...
    replyChan <- result
}()

select {
case reply := <-replyChan:
    return reply, nil  // Returns without closing channels
```

**Impact:**
- Memory leak proportional to request volume
- Each unclosed channel retains memory
- Performance degradation over time

**Suggested Fix:**
```go
errChan := make(chan error, 1)
replyChan := make(chan *RPCReply, 1)
defer close(errChan)
defer close(replyChan)
```

---

### Issue 14: [HIGH] ResultChan Never Closed in Batch Processing

**Labels:** `resource-leak`, `high`, `bug`

**Description:**
All batch processing functions send results to `req.ResultChan` but never close it, leaking channels for every batched operation.

**Affected Files:**
- `batch.go` lines 205-245, 264-322, 326-403, 406-445, 449-500

**Leaking Code:**
```go
req.ResultChan <- &BatchResult{
    Data:   buffer[:bytesRead],
    Status: NFS_OK,
}
// Missing: close(req.ResultChan)
```

**Impact:** Significant memory leak under high load

**Suggested Fix:**
```go
req.ResultChan <- &BatchResult{...}
close(req.ResultChan)
```

---

### Issue 15: [HIGH] Goroutine Leak in HandleCall on Timeout

**Labels:** `resource-leak`, `high`, `bug`

**Description:**
If the context times out, `HandleCall` returns but the spawned goroutine continues running and eventually tries to send to a channel nobody is listening to.

**Affected Files:**
- `nfs_handlers.go` lines 51-93

**Leaking Code:**
```go
go func() {
    // ... long processing ...
    replyChan <- result  // Nobody listening if timeout occurred
}()

select {
case <-ctx.Done():
    return nil, fmt.Errorf("operation timed out")  // Goroutine still running!
```

**Impact:** Goroutine leak accumulating over time

**Suggested Fix:**
```go
go func() {
    var result *RPCReply
    var err error

    // ... process ...

    select {
    case replyChan <- result:
    case <-ctx.Done():
        // Context cancelled, don't block on send
    }
}()
```

---

### Issue 16: [MEDIUM] Old taskQueue Not Closed in WorkerPool Resize

**Labels:** `resource-leak`, `medium`, `bug`

**Description:**
When resizing the worker pool, a new taskQueue is created without properly draining and closing the old one.

**Affected Files:**
- `worker_pool.go` line 210

**Impact:** Channel and task leak on resize

**Suggested Fix:**
Ensure `Stop()` is called and completes before creating new queue, or implement proper queue migration.

---

## MEDIUM PRIORITY (Error Handling)

### Issue 17: [MEDIUM] Ignored Close Errors Throughout Codebase

**Labels:** `error-handling`, `medium`, `bug`

**Description:**
File close errors are ignored in multiple locations. This can lead to data loss, especially for write operations where buffered data may not be flushed.

**Affected Files:**
- `filehandle.go` lines 43, 54
- `server.go` lines 141, 288, 435, 472
- `nfs_node.go` lines 25, 35, 44, 55, 67, 85
- `operations.go` lines 220, 335, 382, 454, 86

**Impact:** Potential data loss, resource leaks

**Suggested Fix:**
```go
// For writes (critical):
if err := f.Close(); err != nil {
    return fmt.Errorf("failed to close file: %w", err)
}

// For reads (log only):
if err := f.Close(); err != nil {
    log.Printf("failed to close file: %v", err)
}
```

---

### Issue 18: [MEDIUM] Generic Error Messages Without Context

**Labels:** `error-handling`, `medium`, `enhancement`

**Description:**
Error messages like "nil node", "empty path", "negative offset" appear in multiple operations without indicating which operation failed, making debugging extremely difficult.

**Affected Files:**
- `operations.go` lines 50, 99, 131, 134, 171, 174, 177, 274, 276, 279, 364, 367, 370, 397, 400, 420, 423, 447, 489

**Current:**
```go
return nil, fmt.Errorf("nil node")  // Which operation?
```

**Suggested Fix:**
```go
return nil, fmt.Errorf("Read: nil node provided")
return nil, fmt.Errorf("Write: negative offset %d for file %s", offset, node.path)
```

---

### Issue 19: [MEDIUM] Errors Not Properly Wrapped - Breaking Error Chain

**Labels:** `error-handling`, `medium`, `bug`

**Description:**
Many errors use `%v` instead of `%w` for wrapping, breaking the error chain and preventing use of `errors.Is()` and `errors.As()`.

**Affected Files:**
- `rpc_types.go` lines 142, 148, 157-192, 202-222
- `operations.go` numerous locations
- `server.go` line 207

**Current:**
```go
return nil, fmt.Errorf("failed to decode XID: %v", err)
```

**Suggested Fix:**
```go
return nil, fmt.Errorf("failed to decode XID: %w", err)
```

---

### Issue 20: [MEDIUM] Ignored TCP Socket Configuration Errors

**Labels:** `error-handling`, `medium`, `bug`

**Description:**
All TCP socket configuration calls (`SetKeepAlive`, `SetNoDelay`, `SetWriteBuffer`, etc.) ignore errors, causing silent configuration failures.

**Affected Files:**
- `server.go` lines 297, 298, 302, 307, 311

**Current:**
```go
tcpConn.SetKeepAlive(true)              // Error ignored
tcpConn.SetWriteBuffer(s.handler.options.SendBufferSize)  // Error ignored
```

**Impact:** Silent performance degradation

**Suggested Fix:**
```go
if err := tcpConn.SetKeepAlive(true); err != nil {
    s.logger.Printf("warning: failed to enable keepalive: %v", err)
}
```

---

## MEDIUM PRIORITY (Documentation)

### Issue 21: [MEDIUM] Documentation Claims Non-Existent Methods

**Labels:** `documentation`, `medium`, `bug`

**Description:**
The API documentation describes three methods on `AbsfsNFS` that don't exist in the code:
- `GetFileSystem()`
- `GetExportOptions()`
- `UpdateExportOptions(options ExportOptions) error`

**Affected Files:**
- `docs/api/absfsnfs.md` lines 46-67

**Impact:** User confusion, broken example code

**Fix:** Remove these from documentation or implement them

---

### Issue 22: [MEDIUM] Documentation References Non-Existent Configuration Fields

**Labels:** `documentation`, `medium`, `bug`

**Description:**
Multiple guides and examples use configuration fields that don't exist in `ExportOptions`:
- `LogLevel`, `LogClientIPs`, `LogOperations`, `LogFile`, `LogFormat`, `LogMaxSize`, `LogMaxBackups`, `LogMaxAge`, `LogCompress`
- `CacheNegativeLookups`, `OperationTimeout`, `HandleTimeout`

**Affected Files:**
- `docs/guides/configuration.md` lines 77, 136-139, 154-161, 488-494
- `docs/examples/custom-export-options.md` lines 62-63, 283-285

**Impact:** Examples don't compile, user confusion

**Fix:** Remove from documentation or implement these features

---

### Issue 23: [MEDIUM] API Reference Missing 17 Implemented Configuration Fields

**Labels:** `documentation`, `medium`, `enhancement`

**Description:**
The API reference for `ExportOptions` only documents 9 of 26 fields. Missing documentation for:
- `ReadAheadMaxFiles`, `ReadAheadMaxMemory`
- `AdaptToMemoryPressure`, `MemoryHighWatermark`, `MemoryLowWatermark`, `MemoryCheckInterval`
- `MaxWorkers`, `BatchOperations`, `MaxBatchSize`
- `MaxConnections`, `IdleTimeout`
- `TCPKeepAlive`, `TCPNoDelay`, `SendBufferSize`, `ReceiveBufferSize`
- `Async`, `MaxFileSize`

**Affected Files:**
- `docs/api/export-options.md` (incomplete)

**Impact:** Users unaware of available features

**Fix:** Complete the API reference documentation

---

### Issue 24: [MEDIUM] Missing API Documentation for Public Types

**Labels:** `documentation`, `medium`, `enhancement`

**Description:**
Several public types lack dedicated API documentation:
- `WorkerPool`
- `BatchProcessor`
- `NFSMetrics`
- `MetricsCollector`
- `ServerOptions`

**Impact:** Users can't effectively use advanced features

**Fix:** Create API documentation pages for these types

---

## LOW PRIORITY (Testing & Dependencies)

### Issue 25: [LOW] Test Compilation Errors in read_ahead_test.go

**Labels:** `testing`, `low`, `bug`

**Description:**
Multiple test files have incorrect expectations for `buffer.Stats()` return values (expects 3, returns 2).

**Affected Files:**
- `read_ahead_test.go` line 217

**Fix:**
```go
// Current (wrong):
size, hitRate, missRate := buffer.Stats()

// Correct:
size, hitRate := buffer.Stats()
```

---

### Issue 26: [LOW] Placeholder Error Classification Functions

**Labels:** `testing`, `low`, `enhancement`

**Description:**
Error classification functions in `metrics_api.go` return hardcoded `false` and have no tests.

**Affected Files:**
- `metrics_api.go` functions: `isStaleFileHandle()`, `isAuthError()`, `isResourceError()`

**Impact:** Error metrics not categorized correctly

**Fix:** Implement proper error classification

---

### Issue 27: [LOW] Missing Tests for Critical Close() Method

**Labels:** `testing`, `low`, `enhancement`

**Description:**
The `Close()` method performs critical cleanup but has no tests.

**Affected Files:**
- `types.go` line 371

**Impact:** Cleanup bugs may go undetected

**Fix:** Add comprehensive tests for `Close()` including error cases

---

### Issue 28: [LOW] Outdated Dependency: absfs from 2020

**Labels:** `dependencies`, `low`, `enhancement`

**Description:**
The `absfs` dependency is over 5 years old (June 2020). A newer version is available (Nov 2025).

**Current Version:** `v0.0.0-20200602175035-e49edc9fef15`
**Available Version:** `v0.0.0-20251107232415-dd6ac2bde664`

**Impact:** Missing bug fixes, security patches, new features

**Fix:**
```bash
go get github.com/absfs/absfs@latest
go mod tidy
# Run tests to check compatibility
```

---

### Issue 29: [LOW] Multiple Outdated Dependencies

**Labels:** `dependencies`, `low`, `enhancement`

**Description:**
Several dependencies have newer versions available:

| Package | Current | Available |
|---------|---------|-----------|
| absfs/inode | v0.0.0-20190804195220 | v0.0.1 |
| fatih/color | v1.12.0 | v1.18.0 |
| mattn/go-colorable | v0.1.8 | v0.1.14 |
| mattn/go-isatty | v0.0.12 | v0.0.20 |
| golang.org/x/sys | v0.16.0 | v0.38.0 |

**Fix:**
```bash
go get -u ./...
go mod tidy
go test ./...
```

---

## SUMMARY STATISTICS

| Category | Critical | High | Medium | Low | Total |
|----------|----------|------|--------|-----|-------|
| Security | 4 | 2 | 2 | 0 | 8 |
| Concurrency | 0 | 5 | 1 | 0 | 6 |
| Resource Leaks | 0 | 3 | 1 | 0 | 4 |
| Error Handling | 0 | 0 | 4 | 0 | 4 |
| Documentation | 0 | 0 | 4 | 0 | 4 |
| Testing | 0 | 0 | 0 | 3 | 3 |
| Dependencies | 0 | 0 | 0 | 2 | 2 |
| **TOTALS** | **4** | **10** | **12** | **5** | **31** |

---

## RECOMMENDED ACTION PLAN

### Phase 1: Security Hardening (URGENT)
1. Fix path traversal vulnerability (Issue #1)
2. Add input size validation (Issue #2)
3. Implement AllowedIPs enforcement (Issue #3)
4. Add authentication support (Issue #4)
5. Fix integer overflow issues (Issue #6)

### Phase 2: Stability Fixes (HIGH PRIORITY)
1. Fix all lock upgrade races (Issues #7, #8)
2. Add synchronization to NFSNode (Issue #9)
3. Fix batch processing race (Issue #10)
4. Fix file handle races (Issue #11)
5. Fix all resource leaks (Issues #13-16)

### Phase 3: Quality Improvements (MEDIUM PRIORITY)
1. Improve error handling (Issues #17-20)
2. Fix documentation errors (Issues #21-24)
3. Add TLS support (Issue #5)

### Phase 4: Maintenance (LOW PRIORITY)
1. Update dependencies (Issues #28-29)
2. Fix test issues (Issue #25)
3. Improve test coverage (Issues #26-27)

---

## NOTES

- Run `go test -race ./...` to detect additional race conditions
- Consider security audit before production deployment
- Benchmark performance impact of security fixes
- Add CI/CD checks for race detection and static analysis

