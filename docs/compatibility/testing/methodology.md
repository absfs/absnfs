---
layout: default
title: Testing Methodology
---

# NFS Client Compatibility Testing Methodology

This document outlines the standardized methodology we use to test compatibility between ABSNFS and various NFS clients.

## Test Environment Setup

### Server Configuration

For all compatibility tests, we use the following standard ABSNFS server configuration:

```go
package main

import (
    "log"
    "os"
    "os/signal"
    "syscall"

    "github.com/absfs/absnfs"
    "github.com/absfs/memfs"
)

func main() {
    // Create an in-memory filesystem
    fs, err := memfs.NewFS()
    if err != nil {
        log.Fatalf("Failed to create filesystem: %v", err)
    }

    // Create test content
    createTestContent(fs)

    // Configure standard export options for testing
    options := absnfs.ExportOptions{
        ReadOnly:         false,
        Secure:           true,
        AllowedIPs:       []string{}, // Allow all IPs for testing
        EnableReadAhead:  true,
        ReadAheadSize:    262144,                // 256KB
        AttrCacheTimeout: 10 * time.Second,
        TransferSize:     65536,                 // 64KB
        MaxConnections:   100,
        IdleTimeout:      5 * time.Minute,
    }

    // Create NFS server
    server, err := absnfs.New(fs, options)
    if err != nil {
        log.Fatalf("Failed to create NFS server: %v", err)
    }

    // Export the filesystem
    if err := server.Export("/export/test", 2049); err != nil {
        log.Fatalf("Failed to export filesystem: %v", err)
    }

    log.Println("Test NFS server running on port 2049")

    // Wait for interrupt
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    <-sigChan

    // Shut down server
    if err := server.Unexport(); err != nil {
        log.Printf("Error during shutdown: %v", err)
    }
}

// Create standard test content for all client tests
func createTestContent(fs absfs.FileSystem) {
    // Standard directory structure
    directories := []string{
        "/simple",
        "/complex",
        "/large",
        "/attrs",
        "/locks",
        "/unicode",
    }
    
    for _, dir := range directories {
        fs.Mkdir(dir, 0755)
    }
    
    // Create small test files
    for i := 1; i <= 10; i++ {
        f, _ := fs.Create(fmt.Sprintf("/simple/file%d.txt", i))
        f.Write([]byte(fmt.Sprintf("This is test file %d", i)))
        f.Close()
    }
    
    // Create a large file for testing
    largeFile, _ := fs.Create("/large/bigfile.bin")
    data := make([]byte, 1024) // 1KB block
    for i := 0; i < 1024*10; i++ { // 10MB file
        largeFile.Write(data)
    }
    largeFile.Close()
    
    // Create files with Unicode names
    unicodeNames := []string{
        "unicode/普通话.txt",         // Chinese
        "unicode/日本語.txt",         // Japanese
        "unicode/한국어.txt",         // Korean
        "unicode/Русский.txt",      // Russian
        "unicode/العربية.txt",        // Arabic
        "unicode/emoji_😀🚀🌍.txt",  // Emoji
    }
    
    for _, name := range unicodeNames {
        f, _ := fs.Create(name)
        f.Write([]byte("Unicode filename test"))
        f.Close()
    }
}
```

### Test Environment Requirements

Each client test environment should include:

1. A clean installation of the target operating system/environment
2. Network connectivity to the ABSNFS test server
3. Default NFS client software (no custom configurations)
4. Monitoring tools to capture performance and errors
5. Ability to capture detailed logs

## Test Categories

We test the following categories for each client:

### 1. Basic Mount Operations

- Default mount with no options
- Read-only mount
- Mount with various timeout settings
- Mount with different block sizes (rsize/wsize)
- Mount persistence across network interruptions

### 2. File Operations

- Create, read, write, and delete files
- Append to existing files
- Truncate files
- Random access (seek and read/write)
- File permission operations

### 3. Directory Operations

- Create and delete directories
- List directory contents (with varying sizes)
- Rename files and directories
- Move files between directories

### 4. Attribute Handling

- Get and set file attributes
- Permission handling
- Timestamp preservation
- Extended attributes (if supported)

### 5. Special Cases

- Large files (>2GB, >4GB)
- Unicode filenames
- Special characters in filenames
- Very long paths
- Very large directories

### 6. Concurrency

- Multiple read clients
- Multiple write clients
- Mixed read/write workloads
- File locking behavior

### 7. Error Handling

- Server disconnection behavior
- Timeout behavior
- Permission denied scenarios
- Disk full scenarios

### 8. Performance

- Sequential read/write throughput
- Random read/write performance
- Directory listing performance
- File creation/deletion rate

## Test Procedure

For each client, follow this procedure:

1. **Setup Phase**
   - Install client OS/environment
   - Install any monitoring tools
   - Verify network connectivity to server

2. **Basic Connectivity**
   - Mount the NFS share with default settings
   - Verify basic read/write functionality
   - Unmount and remount to verify persistence

3. **Systematic Testing**
   - Execute tests for each category listed above
   - Record detailed results for each test
   - Capture client and server logs during tests

4. **Edge Case Testing**
   - Test identified edge cases specific to this client
   - Test with different mount options
   - Test error recovery scenarios

5. **Performance Benchmarking**
   - Run standardized performance tests
   - Compare with baseline (local filesystem performance)
   - Test with different mount options and configurations

6. **Documentation**
   - Complete the client report template
   - Document any workarounds for identified issues
   - Update compatibility matrix

## Compatibility Ratings

We use the following ratings to classify client compatibility:

### Fully Compatible (✅)
- All core functionality works without issues
- Performance is within expected ranges
- No workarounds needed for standard operations

### Mostly Compatible (⚠️)
- Core functionality works with minor issues
- May require specific mount options
- Has documented workarounds for all issues
- Performance may be suboptimal in some scenarios

### Partially Compatible (⛔)
- Basic functionality works but with significant limitations
- Major features may be unreliable or require workarounds
- Performance may be significantly degraded
- Suitable only for specific use cases

### Not Compatible (❌)
- Basic functionality does not work reliably
- Critical operations fail
- Not recommended for production use

## Test Documentation

For each client test, complete the [Client Report Template](./templates.md) with detailed findings.

## Contributing Test Results

If you'd like to contribute client compatibility information:

1. Set up the test environment as described above
2. Run the standard test suite
3. Complete the client report template
4. Submit a pull request with your findings

For more detailed instructions, see our [contribution guidelines](../contributing.md).