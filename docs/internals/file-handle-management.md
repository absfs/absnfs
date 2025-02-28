---
layout: default
title: File Handle Management
---

# File Handle Management

This document explains how ABSNFS manages NFS file handles, which are crucial for the NFS protocol's operation. Understanding file handle management is essential for developers extending or troubleshooting ABSNFS.

## Introduction to NFS File Handles

In the NFS protocol, file handles are opaque identifiers that clients use to reference files and directories on the server. Unlike pathnames, which can change if files are renamed or moved, file handles provide a stable reference to filesystem objects.

Key characteristics of NFS file handles:

1. **Persistence**: Handles should remain valid as long as the corresponding file exists
2. **Opacity**: Clients treat handles as opaque data and do not interpret their contents
3. **Uniqueness**: Each file or directory has a unique handle
4. **Security**: Handles should be difficult to forge or guess
5. **Efficiency**: Handle lookup should be fast

## File Handle Structure in ABSNFS

In ABSNFS, file handles are carefully designed to balance these requirements:

```go
// File handle structure (internal representation)
type FileHandle struct {
    // A unique identifier
    ID uint64
    
    // Generation number (incremented when files are deleted and recreated)
    Generation uint32
    
    // Type of filesystem object (file, directory, symlink, etc.)
    Type uint8
    
    // Flags for special properties
    Flags uint8
    
    // Verification data to prevent forgery
    Verification [8]byte
}
```

This structure is serialized to a byte array for transmission to clients:

```go
// Serialize a file handle to bytes
func (fh *FileHandle) Serialize() []byte {
    buf := make([]byte, FILE_HANDLE_SIZE)
    
    // Write ID (8 bytes)
    binary.BigEndian.PutUint64(buf[0:8], fh.ID)
    
    // Write Generation (4 bytes)
    binary.BigEndian.PutUint32(buf[8:12], fh.Generation)
    
    // Write Type and Flags (1 byte each)
    buf[12] = fh.Type
    buf[13] = fh.Flags
    
    // Write Verification (8 bytes)
    copy(buf[14:22], fh.Verification[:])
    
    // Reserved/padding (10 bytes)
    // Left as zeros
    
    return buf
}
```

## The FileHandleMap Component

The `FileHandleMap` component is responsible for managing the mapping between file handles and filesystem objects:

```go
// Simplified FileHandleMap structure
type FileHandleMap struct {
    mu sync.RWMutex
    
    // Maps file handle IDs to node information
    handleToNode map[uint64]*NodeInfo
    
    // Maps filesystem paths to handle IDs
    pathToHandle map[string]uint64
    
    // Counter for generating unique IDs
    nextID uint64
    
    // Secret key for verification
    verificationKey [32]byte
    
    // Generation number tracking
    generations map[string]uint32
}
```

The `NodeInfo` structure contains information about a filesystem object:

```go
// Information about a filesystem node
type NodeInfo struct {
    // Path within the filesystem
    Path string
    
    // Type of the node (file, directory, etc.)
    Type uint8
    
    // Generation number
    Generation uint32
    
    // Reference count (for cleanup)
    RefCount int32
    
    // Last access time (for cache management)
    LastAccess time.Time
}
```

## File Handle Operations

### Handle Creation

When a client first accesses a file or directory, ABSNFS creates a new file handle:

```go
// Create a file handle for a path
func (fhm *FileHandleMap) CreateHandle(path string, fileType uint8) ([]byte, error) {
    fhm.mu.Lock()
    defer fhm.mu.Unlock()
    
    // Check if handle already exists for this path
    if id, exists := fhm.pathToHandle[path]; exists {
        node := fhm.handleToNode[id]
        node.RefCount++
        node.LastAccess = time.Now()
        
        // Return existing handle
        return fhm.generateHandle(id, node.Generation, node.Type), nil
    }
    
    // Get generation number
    gen := fhm.getGeneration(path)
    
    // Create a new handle ID
    id := fhm.nextID
    fhm.nextID++
    
    // Create node info
    node := &NodeInfo{
        Path:       path,
        Type:       fileType,
        Generation: gen,
        RefCount:   1,
        LastAccess: time.Now(),
    }
    
    // Store mappings
    fhm.handleToNode[id] = node
    fhm.pathToHandle[path] = id
    
    // Generate and return handle
    return fhm.generateHandle(id, gen, fileType), nil
}
```

The `generateHandle` function creates the actual handle bytes:

```go
// Generate serialized handle
func (fhm *FileHandleMap) generateHandle(id uint64, generation uint32, fileType uint8) []byte {
    fh := FileHandle{
        ID:         id,
        Generation: generation,
        Type:       fileType,
        Flags:      0,
    }
    
    // Generate verification bytes using HMAC
    data := make([]byte, 16)
    binary.BigEndian.PutUint64(data[0:8], id)
    binary.BigEndian.PutUint32(data[8:12], generation)
    data[12] = fileType
    data[13] = 0 // flags
    
    mac := hmac.New(sha256.New, fhm.verificationKey[:])
    mac.Write(data)
    sum := mac.Sum(nil)
    copy(fh.Verification[:], sum[:8])
    
    return fh.Serialize()
}
```

### Handle Lookup

When a client presents a file handle, ABSNFS must resolve it to the corresponding filesystem object:

```go
// Lookup a handle
func (fhm *FileHandleMap) LookupHandle(handleBytes []byte) (string, error) {
    // Deserialize handle
    fh, err := DeserializeHandle(handleBytes)
    if err != nil {
        return "", ErrInvalidHandle
    }
    
    // Verify handle integrity
    if !fhm.verifyHandle(fh) {
        return "", ErrForgedHandle
    }
    
    fhm.mu.RLock()
    defer fhm.mu.RUnlock()
    
    // Look up node info
    node, exists := fhm.handleToNode[fh.ID]
    if !exists {
        return "", ErrStaleHandle
    }
    
    // Check generation number to detect stale handles
    if node.Generation != fh.Generation {
        return "", ErrStaleHandle
    }
    
    // Update access time and reference count
    atomic.StoreInt32(&node.RefCount, node.RefCount+1)
    node.LastAccess = time.Now()
    
    return node.Path, nil
}
```

### Handle Release

When a client is done with a file handle, it should be released:

```go
// Release a handle
func (fhm *FileHandleMap) ReleaseHandle(handleBytes []byte) error {
    // Deserialize handle
    fh, err := DeserializeHandle(handleBytes)
    if err != nil {
        return ErrInvalidHandle
    }
    
    fhm.mu.Lock()
    defer fhm.mu.Unlock()
    
    // Look up node info
    node, exists := fhm.handleToNode[fh.ID]
    if !exists {
        return ErrStaleHandle
    }
    
    // Decrease reference count
    node.RefCount--
    
    // If reference count reaches zero and timeout has passed, remove handle
    if node.RefCount <= 0 && time.Since(node.LastAccess) > fhm.handleTimeout {
        delete(fhm.handleToNode, fh.ID)
        delete(fhm.pathToHandle, node.Path)
    }
    
    return nil
}
```

### Handle Invalidation

When files are deleted or renamed, their handles must be invalidated:

```go
// Invalidate handle for a path
func (fhm *FileHandleMap) InvalidateHandle(path string) {
    fhm.mu.Lock()
    defer fhm.mu.Unlock()
    
    // Look up handle ID
    id, exists := fhm.pathToHandle[path]
    if !exists {
        return
    }
    
    // Remove mappings
    delete(fhm.handleToNode, id)
    delete(fhm.pathToHandle, path)
    
    // Increment generation number for the path
    if gen, exists := fhm.generations[path]; exists {
        fhm.generations[path] = gen + 1
    }
}
```

## File Handle Security

ABSNFS implements several security measures for file handles:

### Verification

File handles include a verification hash derived from a server-side secret:

```go
// Verify handle integrity
func (fhm *FileHandleMap) verifyHandle(fh *FileHandle) bool {
    // Prepare data for verification
    data := make([]byte, 16)
    binary.BigEndian.PutUint64(data[0:8], fh.ID)
    binary.BigEndian.PutUint32(data[8:12], fh.Generation)
    data[12] = fh.Type
    data[13] = fh.Flags
    
    // Calculate expected verification bytes
    mac := hmac.New(sha256.New, fhm.verificationKey[:])
    mac.Write(data)
    sum := mac.Sum(nil)
    
    // Compare with actual verification bytes
    for i := 0; i < 8; i++ {
        if sum[i] != fh.Verification[i] {
            return false
        }
    }
    
    return true
}
```

### Generation Numbers

Generation numbers detect stale handles after files have been deleted and recreated:

```go
// Get generation number for a path
func (fhm *FileHandleMap) getGeneration(path string) uint32 {
    gen, exists := fhm.generations[path]
    if !exists {
        // Default to 1 for new paths
        fhm.generations[path] = 1
        return 1
    }
    return gen
}
```

### Expiration

Handles that haven't been used for a while are automatically expired:

```go
// Clean up expired handles
func (fhm *FileHandleMap) cleanupExpiredHandles() {
    fhm.mu.Lock()
    defer fhm.mu.Unlock()
    
    now := time.Now()
    for id, node := range fhm.handleToNode {
        if node.RefCount <= 0 && now.Sub(node.LastAccess) > fhm.handleTimeout {
            delete(fhm.handleToNode, id)
            delete(fhm.pathToHandle, node.Path)
        }
    }
}
```

## Generation Number Management

Generation numbers are crucial for detecting stale handles. When a file is deleted and a new file is created with the same name, the generation number ensures that old handles for the deleted file cannot be used to access the new file.

```go
// Increment generation number when a file is deleted
func (fhm *FileHandleMap) OnFileDelete(path string) {
    fhm.mu.Lock()
    defer fhm.mu.Unlock()
    
    // Invalidate any existing handle
    if id, exists := fhm.pathToHandle[path]; exists {
        delete(fhm.handleToNode, id)
        delete(fhm.pathToHandle, path)
    }
    
    // Increment generation number
    if gen, exists := fhm.generations[path]; exists {
        fhm.generations[path] = gen + 1
    } else {
        fhm.generations[path] = 1
    }
}
```

## File Handle Persistence

For some applications, it's desirable for file handles to persist across server restarts. ABSNFS supports optional handle persistence:

```go
// Save file handle state to disk
func (fhm *FileHandleMap) SaveState(filename string) error {
    fhm.mu.RLock()
    defer fhm.mu.RUnlock()
    
    // Prepare state for serialization
    state := FileHandleMapState{
        NextID:          fhm.nextID,
        VerificationKey: fhm.verificationKey,
        Generations:     fhm.generations,
        HandleToNode:    make(map[uint64]SerializedNodeInfo),
        PathToHandle:    fhm.pathToHandle,
    }
    
    // Convert NodeInfo to SerializedNodeInfo
    for id, node := range fhm.handleToNode {
        state.HandleToNode[id] = SerializedNodeInfo{
            Path:       node.Path,
            Type:       node.Type,
            Generation: node.Generation,
        }
    }
    
    // Serialize to JSON
    data, err := json.Marshal(state)
    if err != nil {
        return err
    }
    
    // Write to file
    return ioutil.WriteFile(filename, data, 0600)
}

// Load file handle state from disk
func (fhm *FileHandleMap) LoadState(filename string) error {
    data, err := ioutil.ReadFile(filename)
    if err != nil {
        return err
    }
    
    // Deserialize from JSON
    var state FileHandleMapState
    if err := json.Unmarshal(data, &state); err != nil {
        return err
    }
    
    fhm.mu.Lock()
    defer fhm.mu.Unlock()
    
    // Restore state
    fhm.nextID = state.NextID
    fhm.verificationKey = state.VerificationKey
    fhm.generations = state.Generations
    fhm.pathToHandle = state.PathToHandle
    
    // Convert SerializedNodeInfo to NodeInfo
    fhm.handleToNode = make(map[uint64]*NodeInfo)
    for id, serNode := range state.HandleToNode {
        fhm.handleToNode[id] = &NodeInfo{
            Path:       serNode.Path,
            Type:       serNode.Type,
            Generation: serNode.Generation,
            RefCount:   0,
            LastAccess: time.Now(),
        }
    }
    
    return nil
}
```

## Performance Considerations

File handle operations are on the critical path for NFS performance. ABSNFS implements several optimizations:

### Concurrent Access

The `FileHandleMap` uses a read-write mutex to allow concurrent handle lookups:

```go
// Lookup multiple handles concurrently
func (fhm *FileHandleMap) LookupHandles(handlesList [][]byte) ([]string, []error) {
    results := make([]string, len(handlesList))
    errors := make([]error, len(handlesList))
    
    var wg sync.WaitGroup
    wg.Add(len(handlesList))
    
    for i, handles := range handlesList {
        go func(idx int, h []byte) {
            defer wg.Done()
            path, err := fhm.LookupHandle(h)
            results[idx] = path
            errors[idx] = err
        }(i, handles)
    }
    
    wg.Wait()
    return results, errors
}
```

### Fast Path for Common Operations

For frequently performed operations, ABSNFS provides optimized "fast paths":

```go
// Fast path for root handle
func (fhm *FileHandleMap) GetRootHandle() []byte {
    fhm.mu.RLock()
    defer fhm.mu.RUnlock()
    
    // Check if root handle exists
    if id, exists := fhm.pathToHandle["/"]; exists {
        node := fhm.handleToNode[id]
        return fhm.generateHandle(id, node.Generation, node.Type)
    }
    
    // Create root handle if it doesn't exist
    fhm.mu.RUnlock()
    handle, _ := fhm.CreateHandle("/", TYPE_DIRECTORY)
    return handle
}
```

### Handle Caching

To avoid repeated handle lookups, ABSNFS caches frequently used handles:

```go
// Handle cache
type HandleCache struct {
    cache map[string][]byte // Path -> Handle
    mu    sync.RWMutex
}

// Get cached handle
func (hc *HandleCache) Get(path string) ([]byte, bool) {
    hc.mu.RLock()
    defer hc.mu.RUnlock()
    
    handle, exists := hc.cache[path]
    return handle, exists
}

// Set cached handle
func (hc *HandleCache) Set(path string, handle []byte) {
    hc.mu.Lock()
    defer hc.mu.Unlock()
    
    hc.cache[path] = handle
}
```

## Resource Management

ABSNFS implements several strategies to manage file handle resources:

### Reference Counting

Reference counting ensures that handles are cleaned up when no longer needed:

```go
// Acquire a handle (increment reference count)
func (fhm *FileHandleMap) AcquireHandle(handleBytes []byte) error {
    // Deserialize handle
    fh, err := DeserializeHandle(handleBytes)
    if err != nil {
        return ErrInvalidHandle
    }
    
    fhm.mu.Lock()
    defer fhm.mu.Unlock()
    
    // Look up node info
    node, exists := fhm.handleToNode[fh.ID]
    if !exists {
        return ErrStaleHandle
    }
    
    // Increment reference count
    node.RefCount++
    node.LastAccess = time.Now()
    
    return nil
}
```

### Periodic Cleanup

A background goroutine periodically cleans up unused handles:

```go
// Start cleanup goroutine
func (fhm *FileHandleMap) StartCleanup(interval time.Duration) {
    go func() {
        ticker := time.NewTicker(interval)
        defer ticker.Stop()
        
        for range ticker.C {
            fhm.cleanupExpiredHandles()
        }
    }()
}
```

### Handle Limits

To prevent resource exhaustion, ABSNFS can limit the number of active handles:

```go
// Create a handle with limit enforcement
func (fhm *FileHandleMap) CreateHandleWithLimit(path string, fileType uint8) ([]byte, error) {
    fhm.mu.Lock()
    defer fhm.mu.Unlock()
    
    // Check handle limit
    if len(fhm.handleToNode) >= fhm.maxHandles {
        // Try to clean up some handles
        fhm.cleanupLeastRecentlyUsed(100) // Free up 100 handles
        
        // Check again
        if len(fhm.handleToNode) >= fhm.maxHandles {
            return nil, ErrTooManyHandles
        }
    }
    
    // Proceed with normal handle creation
    // ...
}

// Clean up least recently used handles
func (fhm *FileHandleMap) cleanupLeastRecentlyUsed(count int) {
    // Create a list of handles sorted by last access time
    type handleAge struct {
        id      uint64
        lastUse time.Time
    }
    
    handles := make([]handleAge, 0, len(fhm.handleToNode))
    for id, node := range fhm.handleToNode {
        if node.RefCount <= 0 {
            handles = append(handles, handleAge{id, node.LastAccess})
        }
    }
    
    // Sort by last access time (oldest first)
    sort.Slice(handles, func(i, j int) bool {
        return handles[i].lastUse.Before(handles[j].lastUse)
    })
    
    // Remove the oldest handles
    for i := 0; i < count && i < len(handles); i++ {
        id := handles[i].id
        node := fhm.handleToNode[id]
        
        delete(fhm.handleToNode, id)
        delete(fhm.pathToHandle, node.Path)
    }
}
```

## Handling Edge Cases

ABSNFS handles several edge cases related to file handles:

### Stale Handles

Stale handles can occur when files are deleted and recreated:

```go
// Handle file recreation
func (fhm *FileHandleMap) HandleFileRecreation(path string) {
    fhm.mu.Lock()
    defer fhm.mu.Unlock()
    
    // Increment generation number for the path
    if gen, exists := fhm.generations[path]; exists {
        fhm.generations[path] = gen + 1
    } else {
        fhm.generations[path] = 1
    }
}
```

### Renamed Files

When files are renamed, their handles need to be updated:

```go
// Handle file rename
func (fhm *FileHandleMap) HandleFileRename(oldPath, newPath string) {
    fhm.mu.Lock()
    defer fhm.mu.Unlock()
    
    // Look up handle ID for old path
    id, exists := fhm.pathToHandle[oldPath]
    if !exists {
        return
    }
    
    // Update node path
    node := fhm.handleToNode[id]
    node.Path = newPath
    
    // Update path to handle mapping
    delete(fhm.pathToHandle, oldPath)
    fhm.pathToHandle[newPath] = id
}
```

### Handle Verification Failures

If handle verification fails, ABSNFS treats it as a security issue:

```go
// Handle verification failure
func (fhm *FileHandleMap) handleVerificationFailure(handleBytes []byte, clientInfo string) {
    // Log the incident
    log.Printf("WARNING: Possible handle forgery attempt from %s", clientInfo)
    
    // If there are too many failures from the same client, consider blocking
    // ...
}
```

## Summary

File handle management is a crucial aspect of an NFS server implementation. ABSNFS provides a robust, efficient, and secure file handle management system that:

1. Creates unique, secure file handles
2. Efficiently maps between handles and filesystem objects
3. Detects and rejects forged or stale handles
4. Manages handle lifecycle and resources
5. Supports persistence across server restarts
6. Handles edge cases like file deletion and renaming

Understanding file handle management is essential for developers who want to extend ABSNFS or integrate it with custom filesystem implementations.

By carefully managing file handles, ABSNFS ensures that NFS clients can reliably access files and directories even when they are moved or renamed, while maintaining security and performance.