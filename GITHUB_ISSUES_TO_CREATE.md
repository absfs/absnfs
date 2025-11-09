# GitHub Issues to Create for ABSNFS

This document contains ready-to-create GitHub issues identified during the November 2025 review.

## How to Use This Document

Each section below can be directly copied into a new GitHub issue. The format is:
```
Title: [Clear, actionable title]
Labels: [Suggested labels]
Priority: [Critical/High/Medium/Low]
[Detailed description]
```

---

## CRITICAL PRIORITY ISSUES

### Issue #1: Update absfs dependency to fix critical bugs (5+ years outdated)

**Labels**: `dependencies`, `critical`, `security`
**Priority**: Critical
**Milestone**: v0.2.0

#### Description

The absnfs project is using a severely outdated version of absfs from June 2, 2020 (over 5 years old). The latest version from November 2025 includes critical bug fixes and improvements.

#### Current Versions
```
github.com/absfs/absfs v0.0.0-20200602175035-e49edc9fef15 (June 2, 2020)
github.com/absfs/memfs v0.0.0-20230318170722-e8d59e67c8b1 (March 2023)
github.com/absfs/inode v0.0.0-20190804195220-b7cd14cdd0dc (August 2019)
```

#### Available Updates
```
github.com/absfs/absfs v0.0.0-20251109181304-77e2f9ac4448 (November 9, 2025)
github.com/absfs/memfs v0.0.0-20251109184305-4de1ff55a67e (November 9, 2025)
github.com/absfs/inode v0.0.1 (proper semver)
```

#### Critical Bugs Fixed in New absfs

1. **Permission constant bug**: `OS_ALL_RWX` was granting incorrect execute permissions
2. **Windows path handling**: Fixed MkdirAll to handle forward slashes correctly
3. **RemoveAll error handling**: Improved error handling and code clarity
4. **Test coverage**: Increased from 22.7% to 89.1%

#### Impact

- Currently shipping with known permission bugs
- Missing critical Windows compatibility fixes
- Missing 5+ years of bug fixes and improvements

#### Recommended Action

```bash
go get github.com/absfs/absfs@latest
go get github.com/absfs/memfs@latest
go get github.com/absfs/inode@latest
go mod tidy
```

#### Testing Requirements

- Run full test suite on Linux, macOS, Windows
- Specifically test file permission handling
- Test Windows path handling
- Verify no regressions

#### References

- absfs PATH_HANDLING.md documentation
- absfs ARCHITECTURE.md
- absfs test improvements

---

### Issue #2: Path traversal vulnerability in Lookup, Remove, and Rename operations

**Labels**: `security`, `critical`, `vulnerability`
**Priority**: Critical
**Milestone**: v0.2.0

#### Description

The `Lookup`, `Remove`, and `Rename` operations in `operations.go` do not validate paths for traversal attacks. An attacker could potentially access files outside the NFS export directory.

#### Vulnerable Code

**File**: `operations.go:231`
```go
path := dir.path + "/" + name  // name could be "../../../etc/passwd"
```

**Affected Functions**:
- `Lookup` (operations.go:48-94)
- `Remove` (operations.go:395-415)
- `Rename` (operations.go:418-442)

#### Attack Scenario

```go
// Attacker sends NFS LOOKUP request with:
name = "../../../etc/passwd"

// Current code would construct:
path = "/export/data" + "/" + "../../../etc/passwd"
// = "/etc/passwd" (after path resolution)
```

#### Impact

- Unauthorized file access
- Ability to read/write/delete files outside export directory
- Complete security bypass
- **CVE-worthy vulnerability**

#### Solution

1. Validate all path components:
   ```go
   func validatePathComponent(name string) error {
       if strings.Contains(name, "..") {
           return errInvalidPath
       }
       if strings.Contains(name, "/") {
           return errInvalidPath
       }
       if name == "" || name == "." {
           return errInvalidPath
       }
       return nil
   }
   ```

2. Use `filepath.Clean()` and verify result:
   ```go
   func safePath(base, name string) (string, error) {
       if err := validatePathComponent(name); err != nil {
           return "", err
       }

       path := filepath.Join(base, name)
       cleaned := filepath.Clean(path)

       // Verify result is still under base
       if !strings.HasPrefix(cleaned, filepath.Clean(base)) {
           return "", errPathTraversal
       }

       return cleaned, nil
   }
   ```

#### Testing Requirements

- Add test attempting to access `../` paths
- Add test for absolute paths
- Add test for null bytes in paths
- Add test for special characters
- Verify error codes match NFSv3 spec

#### References

- CWE-22: Improper Limitation of a Pathname to a Restricted Directory
- OWASP Path Traversal

---

### Issue #3: Missing XDR string length validation enables DoS attacks

**Labels**: `security`, `critical`, `dos`
**Priority**: Critical
**Milestone**: v0.2.0

#### Description

The `xdrDecodeString` function in `rpc_types.go` does not validate maximum string length before allocation. A malicious client could request allocation of up to 4GB of memory, causing denial of service.

#### Vulnerable Code

**File**: `rpc_types.go:93-106`
```go
func xdrDecodeString(r io.Reader) (string, error) {
    length, err := xdrDecodeUint32(r)  // Could be 0xFFFFFFFF (4GB)
    if err != nil {
        return "", err
    }

    buf := make([]byte, length)  // ❌ No validation - allocates up to 4GB!
    _, err = io.ReadFull(r, buf)
    if err != nil {
        return "", err
    }

    return string(buf), nil
}
```

#### Attack Scenario

```
1. Attacker sends NFS request with path string
2. XDR length field = 0xFFFFFFFF (4,294,967,295 bytes)
3. Server allocates 4GB of memory
4. Server crashes or experiences OOM
5. Repeat to completely DoS the server
```

#### Impact

- Memory exhaustion
- Server crash
- Denial of Service
- No rate limiting makes it trivially exploitable

#### Solution

Add maximum length validation:

```go
const (
    MaxPathLength   = 4096      // NFS paths limited to 4KB
    MaxFilename     = 255       // Standard filesystem limit
    MaxDataLength   = 1048576   // 1MB max for data transfers
)

func xdrDecodeString(r io.Reader) (string, error) {
    length, err := xdrDecodeUint32(r)
    if err != nil {
        return "", err
    }

    // ✅ Validate length before allocation
    if length > MaxPathLength {
        return "", fmt.Errorf("string length %d exceeds maximum %d",
            length, MaxPathLength)
    }

    buf := make([]byte, length)
    _, err = io.ReadFull(r, buf)
    if err != nil {
        return "", err
    }

    return string(buf), nil
}
```

#### Additional Improvements

1. Add context-specific limits:
   - Paths: 4096 bytes
   - Filenames: 255 bytes
   - Data buffers: Match TransferSize option

2. Add metrics for rejected oversized requests

3. Add configuration option for limits

#### Testing Requirements

- Test with length = 0
- Test with length = MaxPathLength
- Test with length = MaxPathLength + 1
- Test with length = 0xFFFFFFFF
- Verify error codes

---

### Issue #4: Integer overflow in Read/Write operations

**Labels**: `security`, `critical`, `bug`
**Priority**: Critical
**Milestone**: v0.2.0

#### Description

Read and Write operations do not validate that `offset + count` doesn't overflow, potentially causing buffer overflows or incorrect file access.

#### Vulnerable Code

**File**: `nfs_operations.go:262-276` (READ)
**File**: `nfs_operations.go:342-361` (WRITE)

```go
var offset uint64
var count uint32
// ❌ No validation: offset=0xFFFFFFFFFFFFFFFF + count=1 wraps to 0
```

#### Attack Scenario

```
1. Attacker sends READ request:
   offset = 0xFFFFFFFFFFFFFFFF (max uint64)
   count = 1

2. offset + count = 0 (wraps around)

3. Server reads from offset 0 instead of error

4. Could bypass access controls or cause data corruption
```

#### Impact

- Buffer overflow potential
- Incorrect file access
- Data corruption
- Security vulnerability

#### Solution

Add overflow validation:

```go
func validateReadParams(offset uint64, count uint32) error {
    // Check for overflow
    if offset > math.MaxUint64 - uint64(count) {
        return errNFS3ERR_INVAL
    }

    // Additional validations
    if count > MaxTransferSize {
        return errNFS3ERR_INVAL
    }

    return nil
}

// In READ handler:
if err := validateReadParams(offset, count); err != nil {
    // Return error response
}
```

#### Testing Requirements

- Test offset + count overflow
- Test offset = MaxUint64
- Test count = MaxUint32
- Test count > TransferSize
- Verify error codes match NFSv3 spec

---

### Issue #5: Race condition in AttrCache causing potential corruption

**Labels**: `bug`, `critical`, `concurrency`
**Priority**: Critical
**Milestone**: v0.2.0

#### Description

The `AttrCache.Get` method has a race condition where it releases the read lock before acquiring a write lock to update the access log. State can change between these operations, leading to cache corruption.

#### Vulnerable Code

**File**: `cache.go:44-52`
```go
func (c *AttrCache) Get(path string) (NFSAttrs, bool) {
    c.mu.RLock()
    cached, ok := c.cache[path]
    if ok && time.Now().Before(cached.expireAt) {
        c.mu.RUnlock()  // ❌ Lock released

        // ⚠️ RACE WINDOW: Another goroutine could:
        // - Invalidate this entry
        // - Modify access log
        // - Change cache state

        c.mu.Lock()  // ❌ Acquires different lock
        c.updateAccessLog(path)  // May operate on stale data
        c.mu.Unlock()

        return cached.attrs, true
    }
    c.mu.RUnlock()
    return NFSAttrs{}, false
}
```

#### Race Scenario

```
Time  | Goroutine 1 (Get)         | Goroutine 2 (Invalidate)
------|---------------------------|---------------------------
T1    | RLock acquired            |
T2    | Reads cached[path]        |
T3    | RUnlock released          |
T4    |                           | Lock acquired
T5    |                           | delete(cache, path)
T6    |                           | Lock released
T7    | Lock acquired             |
T8    | updateAccessLog(path)     | <- INVALID: path deleted!
T9    | Lock released             |
```

#### Impact

- Cache corruption
- Inconsistent state
- Potential panics (accessing deleted entries)
- Incorrect access log ordering
- Memory leaks in access log

#### Solution Options

**Option 1: Atomic lock upgrade (preferred)**
```go
func (c *AttrCache) Get(path string) (NFSAttrs, bool) {
    c.mu.Lock()  // Use write lock from start
    defer c.mu.Unlock()

    cached, ok := c.cache[path]
    if ok && time.Now().Before(cached.expireAt) {
        c.updateAccessLog(path)
        return cached.attrs, true
    }

    return NFSAttrs{}, false
}
```

**Option 2: Revalidate after lock upgrade**
```go
func (c *AttrCache) Get(path string) (NFSAttrs, bool) {
    c.mu.RLock()
    cached, ok := c.cache[path]
    if !ok || time.Now().After(cached.expireAt) {
        c.mu.RUnlock()
        return NFSAttrs{}, false
    }
    c.mu.RUnlock()

    c.mu.Lock()
    // ✅ Revalidate after acquiring write lock
    cached, ok = c.cache[path]
    if ok && time.Now().Before(cached.expireAt) {
        c.updateAccessLog(path)
        c.mu.Unlock()
        return cached.attrs, true
    }
    c.mu.Unlock()

    return NFSAttrs{}, false
}
```

**Option 3: Separate lock for access log**
```go
type AttrCache struct {
    cacheMu    sync.RWMutex
    accessMu   sync.Mutex
    cache      map[string]cachedAttr
    accessLog  []string
    // ...
}
```

#### Related Issues

- Same pattern exists in ReadAheadBuffer (cache.go:334-414)
- Should fix both with consistent approach

#### Testing Requirements

- Add race detector test
- Add concurrent access test (1000 goroutines)
- Test with rapid invalidation
- Run with `go test -race`
- Stress test for extended periods

---

### Issue #6: Goroutine leak in HandleCall on context timeout

**Labels**: `bug`, `critical`, `resource-leak`
**Priority**: Critical
**Milestone**: v0.2.0

#### Description

The `HandleCall` method creates a goroutine that may continue running after the context times out, potentially leaking goroutines under high load.

#### Vulnerable Code

**File**: `nfs_handlers.go:27-94`
```go
func (h *NFSHandler) HandleCall(ctx context.Context, call *RPCCall, body []byte) (*RPCReply, error) {
    ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()

    replyChan := make(chan *RPCReply, 1)
    errChan := make(chan error, 1)

    go func() {
        // ❌ Goroutine doesn't check ctx.Done()
        // ❌ May continue running after timeout
        reply, err := h.processCall(call, body)
        replyChan <- reply
        errChan <- err
    }()

    select {
    case <-ctx.Done():
        // ⚠️ Return immediately, but goroutine still running!
        return nil, ctx.Err()
    case reply := <-replyChan:
        err := <-errChan
        return reply, err
    }
}
```

#### Leak Scenario

```
1. Client sends request
2. HandleCall creates goroutine
3. processCall blocks on slow filesystem operation
4. Context times out after 30s
5. HandleCall returns error to client
6. Goroutine continues processing for minutes/hours
7. Eventually writes to channels (which are no longer read)
8. Goroutine finally exits
9. Resources leaked for entire operation duration

Under high load: 1000s of leaked goroutines
```

#### Impact

- Goroutine leak under high load
- Memory leak (goroutines + their stack space)
- File descriptor leak (if goroutines have files open)
- Server instability
- Eventual crash

#### Solution

Pass context to goroutine and check cancellation:

```go
func (h *NFSHandler) HandleCall(ctx context.Context, call *RPCCall, body []byte) (*RPCReply, error) {
    ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()

    replyChan := make(chan *RPCReply, 1)
    errChan := make(chan error, 1)

    go func() {
        // ✅ Check context before expensive operations
        select {
        case <-ctx.Done():
            errChan <- ctx.Err()
            return
        default:
        }

        // ✅ Pass context to processCall
        reply, err := h.processCallWithContext(ctx, call, body)

        // ✅ Check context before sending results
        select {
        case <-ctx.Done():
            return  // Don't send to channels if cancelled
        case replyChan <- reply:
            errChan <- err
        }
    }()

    select {
    case <-ctx.Done():
        return nil, ctx.Err()
    case reply := <-replyChan:
        err := <-errChan
        return reply, err
    }
}

// Update signature to accept context
func (h *NFSHandler) processCallWithContext(ctx context.Context, call *RPCCall, body []byte) (*RPCReply, error) {
    // Check ctx.Done() before each expensive operation
    // Pass ctx to filesystem operations
}
```

#### Testing Requirements

- Test goroutine count before/after timeout
- Use `runtime.NumGoroutine()` in test
- Send 1000 requests that timeout
- Verify goroutines are cleaned up
- Monitor with `runtime.ReadMemStats()`
- Add pprof goroutine profile analysis

#### Metrics to Add

- Track active goroutines in HandleCall
- Track timeout events
- Track goroutine cleanup time

---

### Issue #7: File descriptor leak in NFSNode and FileHandleMap

**Labels**: `bug`, `critical`, `resource-leak`
**Priority**: Critical
**Milestone**: v0.2.0

#### Description

Multiple file descriptor leaks exist:
1. NFSNode methods ignore close errors
2. FileHandleMap doesn't track open FDs
3. No limits on concurrent file opens
4. Defer panic could prevent close

#### Vulnerable Code Locations

**File**: `nfs_node.go:20-69`
```go
func (n *NFSNode) Read(offset uint64, size uint32) ([]byte, error) {
    f, err := n.fs.Open(n.path)
    if err != nil {
        return nil, err
    }
    defer f.Close()  // ❌ Error ignored
    // ❌ If panic before defer, file never closed
    // ...
}
```

**File**: `filehandle.go:43,54`
```go
func (fm *FileHandleMap) Release(handle uint64) error {
    // ...
    if f, ok := fm.handles[handle]; ok {
        f.Close()  // ❌ Error completely ignored
        delete(fm.handles, handle)
    }
    return nil
}
```

#### Leak Scenarios

**Scenario 1: High load**
```
1. Client opens 10,000 files
2. Each allocates file descriptor
3. No limit checking
4. System runs out of FDs (ulimit)
5. Server can't open new files
6. Operations fail with "too many open files"
```

**Scenario 2: Panic during operation**
```
1. Open file (allocates FD)
2. Panic during processing (before defer)
3. Defer never executes
4. File never closed
5. FD leaked permanently
```

**Scenario 3: Ignored close errors**
```
1. Filesystem has issue (disk full, network error)
2. Close() fails
3. Error silently ignored
4. Application unaware of problem
5. Data corruption possible
```

#### Impact

- Server crashes with "too many open files"
- Resource exhaustion
- Service degradation
- Data corruption if close errors ignored
- Memory leak (file objects)

#### Solution

**Part 1: Track open FDs and enforce limits**
```go
type FileHandleMap struct {
    mu         sync.RWMutex
    handles    map[uint64]absfs.File
    openCount  int
    maxOpen    int  // New: configurable limit
}

func (fm *FileHandleMap) Allocate(file absfs.File, path string) (uint64, error) {
    fm.mu.Lock()
    defer fm.mu.Unlock()

    // ✅ Check limit before allocating
    if fm.openCount >= fm.maxOpen {
        return 0, errTooManyOpenFiles
    }

    // ... existing allocation code ...

    fm.openCount++
    return handle, nil
}

func (fm *FileHandleMap) Release(handle uint64) error {
    fm.mu.Lock()
    defer fm.mu.Unlock()

    f, ok := fm.handles[handle]
    if !ok {
        return errInvalidHandle
    }

    // ✅ Check close error
    if err := f.Close(); err != nil {
        // Log error but still delete handle
        log.Printf("Error closing file handle %d: %v", handle, err)
        // Track in metrics
    }

    delete(fm.handles, handle)
    fm.openCount--
    return nil
}
```

**Part 2: Handle panics gracefully**
```go
func (n *NFSNode) Read(offset uint64, size uint32) (data []byte, err error) {
    f, err := n.fs.Open(n.path)
    if err != nil {
        return nil, err
    }

    // ✅ Ensure close happens even on panic
    defer func() {
        closeErr := f.Close()
        if closeErr != nil && err == nil {
            err = closeErr
        }
    }()

    // ... rest of function ...
}
```

**Part 3: Add metrics and monitoring**
```go
type FileHandleMetrics struct {
    OpenFiles     int64
    CloseErrors   int64
    MaxOpenSeen   int64
}

func (fm *FileHandleMap) GetMetrics() FileHandleMetrics {
    fm.mu.RLock()
    defer fm.mu.RUnlock()

    return FileHandleMetrics{
        OpenFiles:   int64(fm.openCount),
        MaxOpenSeen: atomic.LoadInt64(&fm.maxOpenSeen),
        CloseErrors: atomic.LoadInt64(&fm.closeErrors),
    }
}
```

#### Configuration

Add to ExportOptions:
```go
type ExportOptions struct {
    // ...

    // MaxOpenFiles limits concurrent open file handles
    // Prevents file descriptor exhaustion
    // Default: 1000
    MaxOpenFiles int
}
```

#### Testing Requirements

- Test opening MaxOpenFiles files
- Test opening MaxOpenFiles + 1 (should fail)
- Test close error handling
- Test panic recovery
- Test FD count with `lsof` or `/proc/self/fd`
- Stress test with 10,000 open/close cycles
- Test concurrent access

#### Metrics to Add

- Current open file count
- Maximum open files seen
- Close error count
- File handle allocation failures

---

### Issue #8: No authentication enforcement allows unrestricted access

**Labels**: `security`, `critical`, `authentication`
**Priority**: Critical
**Milestone**: v0.2.0

#### Description

The NFS server does not enforce any authentication. While it defines `AllowedIPs` in `ExportOptions`, this field is never checked. Only AUTH_NULL credential flavor is verified, with no actual authentication.

#### Current State

**File**: `nfs_handlers.go:41-45`
```go
// Only checks that flavor is 0 (AUTH_NULL)
if call.Credential.Flavor != 0 {
    reply.Status = MSG_DENIED
    return reply, nil
}
// ❌ No actual authentication!
```

**File**: `types.go:35`
```go
type ExportOptions struct {
    // ...
    AllowedIPs  []string  // ❌ Defined but NEVER checked anywhere
    // ...
}
```

#### Impact

- **Any client can mount and access files**
- No IP-based access control
- No user authentication
- Complete security bypass
- Data breach potential

#### Attack Scenario

```
1. Attacker discovers NFS server on port 2049
2. Sends MOUNT request
3. Server accepts without checking client IP
4. Attacker receives file handle
5. Attacker can read/write/delete any file
6. No logging of unauthorized access
```

#### Solution

Implement multi-layer authentication:

**Part 1: IP-based access control**
```go
func (h *NFSHandler) checkClientIP(remoteAddr string) error {
    if len(h.options.AllowedIPs) == 0 {
        return nil  // No restrictions configured
    }

    host, _, err := net.SplitHostPort(remoteAddr)
    if err != nil {
        return err
    }

    clientIP := net.ParseIP(host)
    if clientIP == nil {
        return errInvalidIP
    }

    for _, allowed := range h.options.AllowedIPs {
        // Support both individual IPs and CIDR ranges
        if strings.Contains(allowed, "/") {
            _, network, err := net.ParseCIDR(allowed)
            if err != nil {
                continue
            }
            if network.Contains(clientIP) {
                return nil
            }
        } else {
            allowedIP := net.ParseIP(allowed)
            if clientIP.Equal(allowedIP) {
                return nil
            }
        }
    }

    return errUnauthorizedIP
}
```

**Part 2: Implement AUTH_SYS (AUTH_UNIX)**
```go
type AuthSysCred struct {
    Stamp    uint32
    Hostname string
    UID      uint32
    GID      uint32
    GIDs     []uint32
}

func (h *NFSHandler) parseAuthSys(data []byte) (*AuthSysCred, error) {
    // Parse AUTH_SYS credential structure
    // Verify UID/GID against allowed list
}

func (h *NFSHandler) HandleCall(ctx context.Context, call *RPCCall, body []byte, remoteAddr string) (*RPCReply, error) {
    // ✅ Check IP whitelist
    if err := h.checkClientIP(remoteAddr); err != nil {
        return deniedReply("Unauthorized IP"), nil
    }

    // ✅ Verify credentials
    switch call.Credential.Flavor {
    case AUTH_NULL:
        if !h.options.AllowNullAuth {
            return deniedReply("NULL auth not allowed"), nil
        }
    case AUTH_SYS:
        cred, err := h.parseAuthSys(call.Credential.Body)
        if err != nil {
            return deniedReply("Invalid AUTH_SYS credential"), nil
        }
        if err := h.checkAuthSys(cred); err != nil {
            return deniedReply("Unauthorized user"), nil
        }
    default:
        return deniedReply("Unsupported auth flavor"), nil
    }

    // ... continue with request processing ...
}
```

**Part 3: Add configuration options**
```go
type ExportOptions struct {
    // Existing fields...

    // AllowedIPs restricts access to specific IP addresses or CIDR ranges
    // Empty list = no IP restrictions
    // Example: ["192.168.1.0/24", "10.0.0.5"]
    AllowedIPs []string

    // AllowNullAuth permits connections without authentication
    // Default: false (require authentication)
    AllowNullAuth bool

    // AllowedUIDs restricts access to specific user IDs
    // Only applies when using AUTH_SYS
    // Empty list = all UIDs allowed
    AllowedUIDs []uint32

    // AllowedGIDs restricts access to specific group IDs
    // Only applies when using AUTH_SYS
    // Empty list = all GIDs allowed
    AllowedGIDs []uint32

    // RequireSecurePort requires clients to use port < 1024
    // Provides weak assurance client is root
    // Default: false
    RequireSecurePort bool
}
```

#### Testing Requirements

- Test IP whitelist with allowed IP (should succeed)
- Test IP whitelist with denied IP (should fail)
- Test CIDR range matching
- Test AUTH_NULL when disabled
- Test AUTH_SYS credential parsing
- Test UID/GID restrictions
- Test secure port requirement
- Test with real NFS client

#### Documentation Needed

- Security best practices guide
- Authentication configuration examples
- Deployment recommendations
- Migration guide for existing deployments

#### Related Work

- Add audit logging for denied requests
- Add rate limiting per IP
- Add connection tracking
- Add metrics for auth failures

---

## HIGH PRIORITY ISSUES

### Issue #9: No rate limiting enables DoS attacks

**Labels**: `security`, `enhancement`, `dos`
**Priority**: High
**Milestone**: v0.2.0

#### Description

The server has no rate limiting on:
- Connection attempts
- Operations per connection
- Bandwidth per client
- Total concurrent operations

This makes it trivially easy to DoS the server.

#### Solution

Implement multi-level rate limiting:

```go
type RateLimiter struct {
    // Per-IP connection rate (connections/second)
    connLimiter *rate.Limiter

    // Per-IP operation rate (ops/second)
    opLimiter *rate.Limiter

    // Per-IP bandwidth (bytes/second)
    bwLimiter *rate.Limiter
}
```

Add to ExportOptions:
```go
type ExportOptions struct {
    // ...

    // MaxConnectionsPerIP limits concurrent connections from single IP
    // Default: 10
    MaxConnectionsPerIP int

    // ConnectionRatePerIP limits connection attempts per second from single IP
    // Default: 5
    ConnectionRatePerIP float64

    // OperationRatePerIP limits operations per second from single IP
    // Default: 1000
    OperationRatePerIP float64

    // BandwidthPerIP limits bytes per second from single IP
    // Default: 10MB/s
    BandwidthPerIP int64
}
```

---

### Issue #10: Inefficient file handle allocation causes scalability bottleneck

**Labels**: `performance`, `bug`
**Priority**: High
**Milestone**: v0.2.0

#### Description

File handle allocation uses linear search from 1 every time, resulting in O(n) allocation time.

**File**: `filehandle.go:8-26`

#### Current Code
```go
var handle uint64 = 1
for {
    if _, exists := fm.handles[handle]; !exists {
        break
    }
    handle++  // ❌ Linear search from 1 every time
}
```

#### Impact
- With 1000 open files, each allocation checks up to 1000 handles
- O(n) time complexity
- Scalability bottleneck
- Performance degrades with open file count

#### Solution
```go
type FileHandleMap struct {
    mu         sync.RWMutex
    handles    map[uint64]absfs.File
    nextHandle uint64  // ✅ Track next available
    freeList   []uint64  // ✅ Reuse freed handles
}

func (fm *FileHandleMap) Allocate(file absfs.File, path string) (uint64, error) {
    fm.mu.Lock()
    defer fm.mu.Unlock()

    var handle uint64

    // ✅ Reuse freed handles first
    if len(fm.freeList) > 0 {
        handle = fm.freeList[len(fm.freeList)-1]
        fm.freeList = fm.freeList[:len(fm.freeList)-1]
    } else {
        // ✅ Allocate new handle
        fm.nextHandle++
        handle = fm.nextHandle
    }

    fm.handles[handle] = file
    return handle, nil
}

func (fm *FileHandleMap) Release(handle uint64) error {
    fm.mu.Lock()
    defer fm.mu.Unlock()

    // ... close file ...

    delete(fm.handles, handle)
    fm.freeList = append(fm.freeList, handle)  // ✅ Add to free list
    return nil
}
```

---

### Issue #11: Inefficient LRU cache implementation causes performance overhead

**Labels**: `performance`, `bug`
**Priority**: High
**Milestone**: v0.2.0

#### Description

AttrCache and ReadAheadBuffer use slice-based access logs requiring O(n) operations for LRU management.

**File**: `cache.go:88-104`, `cache.go:283-294`

#### Current Code
```go
func (c *AttrCache) removeFromAccessLog(path string) {
    for i, p := range c.accessLog {
        if p == path {
            c.accessLog = append(c.accessLog[:i], c.accessLog[i+1:]...)
            break
        }
    }
}
```

#### Impact
- O(n) search on every cache access
- O(n) removal on every cache access
- With 10,000 cache entries, up to 10,000 comparisons per access
- Significant CPU overhead

#### Solution

Use doubly-linked list for O(1) LRU:

```go
import "container/list"

type AttrCache struct {
    mu        sync.RWMutex
    cache     map[string]*list.Element
    lruList   *list.List
    maxSize   int
    timeout   time.Duration
}

type cacheEntry struct {
    path     string
    attrs    NFSAttrs
    expireAt time.Time
}

func (c *AttrCache) Get(path string) (NFSAttrs, bool) {
    c.mu.Lock()
    defer c.mu.Unlock()

    elem, ok := c.cache[path]
    if !ok {
        return NFSAttrs{}, false
    }

    entry := elem.Value.(*cacheEntry)
    if time.Now().After(entry.expireAt) {
        c.lruList.Remove(elem)
        delete(c.cache, path)
        return NFSAttrs{}, false
    }

    // ✅ O(1) move to front
    c.lruList.MoveToFront(elem)

    return entry.attrs, true
}

func (c *AttrCache) Put(path string, attrs NFSAttrs) {
    c.mu.Lock()
    defer c.mu.Unlock()

    entry := &cacheEntry{
        path:     path,
        attrs:    attrs,
        expireAt: time.Now().Add(c.timeout),
    }

    if elem, ok := c.cache[path]; ok {
        // Update existing
        c.lruList.MoveToFront(elem)
        elem.Value = entry
    } else {
        // Add new
        elem := c.lruList.PushFront(entry)
        c.cache[path] = elem
    }

    // ✅ Evict LRU if over capacity - O(1)
    for c.lruList.Len() > c.maxSize {
        oldest := c.lruList.Back()
        if oldest != nil {
            entry := oldest.Value.(*cacheEntry)
            delete(c.cache, entry.path)
            c.lruList.Remove(oldest)
        }
    }
}
```

---

### Issue #12: Bubble sort in metrics causes unnecessary CPU overhead

**Labels**: `performance`, `bug`
**Priority**: High
**Milestone**: v0.2.0

#### Description

Metrics calculation uses bubble sort (O(n²)) on 1000 samples for percentile calculation.

**File**: `metrics.go:372-382`

#### Current Code
```go
func sort(durations []time.Duration) {
    n := len(durations)
    for i := 0; i < n-1; i++ {
        for j := 0; j < n-i-1; j++ {
            if durations[j] > durations[j+1] {
                durations[j], durations[j+1] = durations[j+1], durations[j]
            }
        }
    }
}
```

#### Impact
- 1000 samples = ~500,000 comparisons
- O(n²) time complexity
- Metrics overhead impacts overall performance
- Unnecessary CPU usage

#### Solution
```go
import "sort"

func sortDurations(durations []time.Duration) {
    sort.Slice(durations, func(i, j int) bool {
        return durations[i] < durations[j]
    })
}
```

Reduces complexity from O(n²) to O(n log n).

---

(Continue with remaining high-priority issues #13-20...)

## MEDIUM PRIORITY ISSUES

### Issue #21: Add CI/CD pipelines for automated testing

**Labels**: `infrastructure`, `testing`
**Priority**: Medium
**Milestone**: v0.3.0

#### Description

Currently only one GitHub Actions workflow exists (validate-compatibility-docs.yml) which only validates documentation. No automated testing of code.

#### Needed Workflows

**1. Test Workflow** (.github/workflows/test.yml)
```yaml
name: Test

on: [push, pull_request]

jobs:
  test:
    strategy:
      matrix:
        os: [ubuntu-latest, macos-latest, windows-latest]
        go: ['1.21', '1.22']
    runs-on: ${{ matrix.os }}

    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
        with:
          go-version: ${{ matrix.go }}

      - name: Run tests
        run: go test -v -race -coverprofile=coverage.txt ./...

      - name: Upload coverage
        uses: codecov/codecov-action@v3
```

**2. Lint Workflow** (.github/workflows/lint.yml)
```yaml
name: Lint

on: [push, pull_request]

jobs:
  golangci:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: golangci/golangci-lint-action@v3
```

**3. Security Workflow** (.github/workflows/security.yml)
```yaml
name: Security

on: [push, pull_request]

jobs:
  gosec:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: securego/gosec@master
```

**4. Dependency Update** (dependabot.yml)
```yaml
version: 2
updates:
  - package-ecosystem: "gomod"
    directory: "/"
    schedule:
      interval: "weekly"
```

---

### Issue #22: Add Windows compatibility testing

**Labels**: `testing`, `windows`, `compatibility`
**Priority**: Medium
**Milestone**: v0.3.0

#### Description

No Windows-specific tests despite absfs having Windows path handling improvements.

#### Needed Tests

1. Windows path separator handling
2. Forward slash vs backslash
3. Drive letters
4. UNC paths
5. Case sensitivity differences
6. File permission mapping

---

### Issue #23: Fix README examples to match actual API

**Labels**: `documentation`, `bug`
**Priority**: Medium
**Milestone**: v0.2.0

#### Description

README.md shows incorrect API usage that won't compile.

**File**: README.md:59

#### Current (Wrong)
```go
if err := server.Export("/export/test"); err != nil {
```

#### Actual API
```go
func (s *AbsfsNFS) Export(mountPath string, port int) error
```

#### Fix
```go
if err := server.Export("/export/test", 2049); err != nil {
```

---

### Issue #24: Document thread safety requirements

**Labels**: `documentation`, `safety`
**Priority**: Medium
**Milestone**: v0.2.0

#### Description

absfs filesystems are NOT goroutine-safe. absnfs is safe because it uses absolute paths, but this is undocumented.

#### Needed Documentation

1. Add THREAD_SAFETY.md explaining:
   - absfs per-instance working directory
   - Why absolute paths are required
   - Goroutine safety guarantees

2. Add code comments enforcing absolute paths

3. Add linter check preventing relative paths

4. Add test verifying all paths are absolute

---

### Issue #25: Remove dead code and unused fields

**Labels**: `cleanup`, `maintenance`
**Priority**: Medium
**Milestone**: v0.3.0

#### Unused Items to Remove/Fix

- `FileAttribute` type (nfs_types.go:32-45) - completely unused
- `FSInfo` and `FSStats` types (nfs_types.go:104-123) - hardcoded values used instead
- `ExportOptions.Secure` - defined but never checked
- `ExportOptions.AllowedIPs` - see Issue #8 (implement, don't remove)
- `ExportOptions.Squash` - defined but never implemented
- `ExportOptions.Async` - defined but never implemented
- `ExportOptions.MaxFileSize` - defined but never checked
- `errChan` in HandleCall - created but never written to

#### Decision Needed

For each field, either:
1. Remove if truly unused
2. Implement if feature is wanted
3. Document as TODO/future work

---

(Continue with remaining medium-priority issues #26-45...)

## LOW PRIORITY ISSUES

### Issue #46-60

These issues cover:
- Missing edge case tests
- Missing integration tests
- Missing stress tests
- Documentation improvements
- Example improvements
- etc.

(Full details available in ISSUES_REVIEW.md)

---

## Summary

**Total Issues to Create**: 60+

**By Priority:**
- Critical: 8 issues
- High: 12 issues
- Medium: 25 issues
- Low: 15+ issues

**By Category:**
- Security: 15 issues
- Performance: 12 issues
- Testing: 10 issues
- Documentation: 8 issues
- Maintenance: 10 issues
- Infrastructure: 5 issues

**Recommended Approach:**
1. Create critical issues first (Issues #1-8)
2. Create high-priority issues (Issues #9-20)
3. Group medium/low issues into epics
4. Create project board for tracking
5. Assign milestones (v0.2.0 for critical/high, v0.3.0 for medium)

