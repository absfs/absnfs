---
layout: default
title: Test Types
---

# Test Types

ABSNFS employs a comprehensive testing approach that includes various types of tests to ensure reliability, correctness, and performance. This page describes the different types of tests used in the project.

## Unit Tests

Unit tests verify the behavior of individual functions and methods in isolation. These tests form the foundation of the testing strategy.

### Characteristics

- Test a single function or small component
- Mock or stub dependencies
- Quick to run
- Highly focused and deterministic
- Cover both success and error paths

### Example

```go
func TestFileHandleMap_GetNode(t *testing.T) {
    // Create a test map
    fm := NewFileHandleMap()
    
    // Create a test node
    node := &NFSNode{path: "/test.txt"}
    
    // Create a handle for the node
    handle, err := fm.CreateHandle(node)
    if err != nil {
        t.Fatalf("Failed to create handle: %v", err)
    }
    
    // Test getting the node
    retrievedNode, err := fm.GetNode(handle)
    if err != nil {
        t.Fatalf("Failed to get node: %v", err)
    }
    
    // Verify the node is correct
    if retrievedNode.path != node.path {
        t.Errorf("Expected path %s, got %s", node.path, retrievedNode.path)
    }
    
    // Test with invalid handle
    invalidHandle := []byte("invalid")
    _, err = fm.GetNode(invalidHandle)
    if err == nil {
        t.Error("Expected error for invalid handle, got nil")
    }
}
```

## Integration Tests

Integration tests verify that multiple components work together correctly. These tests ensure that interfaces between components are correctly implemented.

### Characteristics

- Test interactions between multiple components
- Use real implementations rather than mocks
- May involve filesystem operations
- Verify end-to-end behavior
- Cover common usage scenarios

### Example

```go
func TestLookupReadWrite(t *testing.T) {
    // Create a test filesystem
    fs, err := memfs.NewFS()
    if err != nil {
        t.Fatalf("Failed to create filesystem: %v", err)
    }
    
    // Create test file
    content := []byte("Hello, World!")
    f, err := fs.Create("/test.txt")
    if err != nil {
        t.Fatalf("Failed to create file: %v", err)
    }
    if _, err := f.Write(content); err != nil {
        t.Fatalf("Failed to write to file: %v", err)
    }
    f.Close()
    
    // Create NFS server
    nfs, err := absnfs.New(fs, absnfs.ExportOptions{})
    if err != nil {
        t.Fatalf("Failed to create NFS server: %v", err)
    }
    
    // Get root file handle (simplified for example)
    rootHandle := nfs.GetRootHandle()
    
    // Lookup the file
    fileHandle, attrs, err := nfs.Lookup(rootHandle, "test.txt")
    if err != nil {
        t.Fatalf("Lookup failed: %v", err)
    }
    
    // Verify file attributes
    if attrs.Size != uint64(len(content)) {
        t.Errorf("Expected size %d, got %d", len(content), attrs.Size)
    }
    
    // Read the file
    data, err := nfs.Read(fileHandle, 0, uint32(len(content)))
    if err != nil {
        t.Fatalf("Read failed: %v", err)
    }
    
    // Verify content
    if string(data) != string(content) {
        t.Errorf("Expected content %q, got %q", content, data)
    }
    
    // Write to the file
    newContent := []byte("Updated content")
    count, err := nfs.Write(fileHandle, 0, newContent)
    if err != nil {
        t.Fatalf("Write failed: %v", err)
    }
    if count != uint32(len(newContent)) {
        t.Errorf("Expected write count %d, got %d", len(newContent), count)
    }
    
    // Read again to verify the update
    data, err = nfs.Read(fileHandle, 0, uint32(len(newContent)))
    if err != nil {
        t.Fatalf("Read after write failed: %v", err)
    }
    if string(data) != string(newContent) {
        t.Errorf("Expected updated content %q, got %q", newContent, data)
    }
}
```

## Table-Driven Tests

Table-driven tests run the same test logic over multiple sets of inputs and expected outputs. This approach enables systematic testing of various scenarios.

### Characteristics

- Define tables of test cases
- Each case has inputs and expected outputs
- Run the same test logic for each case
- Easy to add new test cases
- Clear error messages identifying which case failed

### Example

```go
func TestErrorMapping(t *testing.T) {
    cases := []struct {
        name     string
        err      error
        expected nfsv3.NFSStatus
    }{
        {"nil", nil, nfsv3.NFS3_OK},
        {"not exist", os.ErrNotExist, nfsv3.NFS3ERR_NOENT},
        {"permission", os.ErrPermission, nfsv3.NFS3ERR_ACCES},
        {"exists", os.ErrExist, nfsv3.NFS3ERR_EXIST},
        {"invalid", os.ErrInvalid, nfsv3.NFS3ERR_INVAL},
        {"not empty", syscall.ENOTEMPTY, nfsv3.NFS3ERR_NOTEMPTY},
        {"is dir", syscall.EISDIR, nfsv3.NFS3ERR_ISDIR},
        {"not dir", syscall.ENOTDIR, nfsv3.NFS3ERR_NOTDIR},
        {"unknown", errors.New("unknown error"), nfsv3.NFS3ERR_IO},
    }
    
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            result := mapErrorToNFS(tc.err)
            if result != tc.expected {
                t.Errorf("Expected %v, got %v", tc.expected, result)
            }
        })
    }
}
```

## Property-Based Tests

Property-based tests verify that certain properties or invariants hold for a wide range of inputs, including edge cases.

### Characteristics

- Generate random or structured inputs
- Focus on properties rather than specific examples
- Help discover unexpected edge cases
- Provide good coverage with fewer test cases
- Can identify subtle bugs

### Example

```go
func TestFileHandlePropertiesRoundTrip(t *testing.T) {
    // Note: This is a simplified example.
    // Real property-based testing would typically use a framework like rapid or gopter
    
    fm := NewFileHandleMap()
    
    // Test with a variety of paths
    paths := []string{
        "/",
        "/simple.txt",
        "/dir/file.txt",
        "/deep/nested/path/file.dat",
        "/file with spaces.txt",
        "/very/long/path/" + strings.Repeat("a", 1000) + ".txt",
    }
    
    for _, path := range paths {
        t.Run(path, func(t *testing.T) {
            // Create node
            node := &NFSNode{path: path}
            
            // Create handle
            handle, err := fm.CreateHandle(node)
            if err != nil {
                t.Fatalf("Failed to create handle: %v", err)
            }
            
            // Get node back from handle
            retrievedNode, err := fm.GetNode(handle)
            if err != nil {
                t.Fatalf("Failed to get node: %v", err)
            }
            
            // Verify the round trip worked
            if retrievedNode.path != path {
                t.Errorf("Path changed in round trip: got %s, expected %s", 
                          retrievedNode.path, path)
            }
        })
    }
}
```

## Benchmark Tests

Benchmark tests measure the performance characteristics of critical operations, helping to identify performance regressions and optimization opportunities.

### Characteristics

- Measure execution time of operations
- Run the operation multiple times for statistical significance
- Compare performance between implementations
- Identify performance bottlenecks
- Detect performance regressions

### Example

```go
func BenchmarkRead(b *testing.B) {
    // Create a test filesystem with a large file
    fs, _ := memfs.NewFS()
    f, _ := fs.Create("/largefile.bin")
    data := make([]byte, 1024*1024) // 1MB of data
    for i := range data {
        data[i] = byte(i % 256)
    }
    f.Write(data)
    f.Close()
    
    // Create NFS server
    nfs, _ := absnfs.New(fs, absnfs.ExportOptions{})
    
    // Get file handle
    rootHandle := nfs.GetRootHandle()
    fileHandle, _, _ := nfs.Lookup(rootHandle, "largefile.bin")
    
    // Run the benchmark
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        offset := uint64((i * 1024) % (len(data) - 1024))
        _, err := nfs.Read(fileHandle, offset, 1024)
        if err != nil {
            b.Fatalf("Read failed: %v", err)
        }
    }
}
```

## Concurrency Tests

Concurrency tests verify that the system behaves correctly under concurrent access, helping to identify race conditions, deadlocks, and other concurrency issues.

### Characteristics

- Run operations concurrently
- Focus on thread safety
- May use stress testing with high concurrency
- Run with race detection enabled
- Verify correctness under concurrent access

### Example

```go
func TestConcurrentReads(t *testing.T) {
    // Create a test filesystem with a file
    fs, _ := memfs.NewFS()
    f, _ := fs.Create("/test.txt")
    f.Write([]byte("Hello, World!"))
    f.Close()
    
    // Create NFS server
    nfs, _ := absnfs.New(fs, absnfs.ExportOptions{})
    
    // Get file handle
    rootHandle := nfs.GetRootHandle()
    fileHandle, _, _ := nfs.Lookup(rootHandle, "test.txt")
    
    // Number of concurrent readers
    const numReaders = 100
    
    // Wait group to synchronize goroutines
    var wg sync.WaitGroup
    wg.Add(numReaders)
    
    // Channel to collect errors
    errCh := make(chan error, numReaders)
    
    // Start concurrent readers
    for i := 0; i < numReaders; i++ {
        go func(id int) {
            defer wg.Done()
            
            // Each reader reads with a different offset
            offset := uint64(id % 5)
            length := uint32(5)
            
            data, err := nfs.Read(fileHandle, offset, length)
            if err != nil {
                errCh <- fmt.Errorf("reader %d: %v", id, err)
                return
            }
            
            // Verify data
            expected := "Hello, World!"[offset:offset+uint64(length)]
            if string(data) != expected {
                errCh <- fmt.Errorf("reader %d: expected %q, got %q", 
                                    id, expected, data)
            }
        }(i)
    }
    
    // Wait for all readers to finish
    wg.Wait()
    close(errCh)
    
    // Check for errors
    for err := range errCh {
        t.Error(err)
    }
}
```

## Error Tests

Error tests specifically target error conditions to ensure that the system handles errors gracefully and returns appropriate error information.

### Characteristics

- Force error conditions
- Verify error handling logic
- Test recovery from errors
- Ensure meaningful error messages
- Verify proper cleanup after errors

### Example

```go
func TestReadErrors(t *testing.T) {
    // Create a filesystem that returns errors
    fs := &mockErrorFS{
        readErr: errors.New("simulated read error"),
    }
    
    // Create NFS server
    nfs, _ := absnfs.New(fs, absnfs.ExportOptions{})
    
    // Get file handle (simplified)
    handle := []byte("testhandle")
    
    // Attempt to read
    _, err := nfs.Read(handle, 0, 100)
    
    // Verify error is properly mapped
    if err == nil {
        t.Fatal("Expected error, got nil")
    }
    
    // Verify error is mapped to appropriate NFS error
    nfsErr, ok := err.(*NFSError)
    if !ok {
        t.Fatalf("Expected NFSError, got %T: %v", err, err)
    }
    
    if nfsErr.Status != nfsv3.NFS3ERR_IO {
        t.Errorf("Expected status NFS3ERR_IO, got %v", nfsErr.Status)
    }
}

// Mock filesystem that returns errors
type mockErrorFS struct {
    readErr error
}

// Implement necessary methods of absfs.FileSystem interface
func (m *mockErrorFS) Open(name string) (absfs.File, error) {
    return &mockErrorFile{readErr: m.readErr}, nil
}

// Other required methods...

// Mock file that returns errors
type mockErrorFile struct {
    readErr error
}

func (m *mockErrorFile) Read(p []byte) (n int, err error) {
    return 0, m.readErr
}

// Other required methods...
```

## Each test type plays a crucial role in the overall testing strategy, helping to ensure that ABSNFS is reliable, correct, and high-performing across a wide range of scenarios and conditions.