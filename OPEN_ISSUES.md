# Open GitHub Issues

This document lists all currently open issues in the absnfs project.

**Total Open Issues:** 24
**Last Updated:** 2025-11-22

---

## Status Summary

All **CRITICAL** priority issues have been resolved! 🎉

- ✅ Critical issues: 0 (all resolved)
- ⚠️ High priority: 11
- 📋 Medium priority: 10
- 📝 Low priority: 3

---

## High Priority Issues

#10: Inefficient File Handle Allocation - O(n) Performance
Linear search from handle=1 every time causing O(n) allocation time. The code in filehandle.go:8-26 iterates through all handles sequentially to find an available one, causing slow handle allocation with many open files and creating a scalability bottleneck.

#11: Inefficient LRU Implementation - O(n) Cache Access
LRU access log uses slice requiring O(n) search and removal on every access. The removeFromAccessLog function in cache.go:88-104 performs a linear search through the access log slice, causing poor performance with large caches and scalability issues.

#12: Bubble Sort in Metrics - O(n²) Performance
Uses O(n²) bubble sort for latency percentile calculation on 1000 samples. The sort function in metrics.go:372-382 implements bubble sort, resulting in high CPU overhead for metrics with 1000 samples requiring 1,000,000 comparisons.

#13: Race Condition in ReadAheadBuffer
Similar race condition pattern as AttrCache - releases RLock then acquires Lock. Found in cache.go:334-414, this can cause buffer corruption, incorrect read-ahead behavior, and potential panics.

#14: Connection Tracking Race Condition
Multiple goroutines could unregister the same connection simultaneously in server.go:100-113. This leads to incorrect connection counts, potential negative counter, and connection limit bypass.

#15: Unsafe Type Assertions Without Checking
Type assertion without checking success will panic on type mismatch. In batch.go:380-384, the code performs type assertion without the ok check, which will cause the program to panic if the type doesn't match.

#16: Memory Leak in Access Logs
updateAccessLog prepends to slice without bound checking, causing unbounded growth. The code in cache.go:93 continuously prepends paths to the access log without any size limit, leading to a memory leak with many unique paths.

#17: No Input Validation on CREATE/MKDIR
Filenames not validated for null bytes, special characters, maximum length, or reserved names in nfs_operations.go:437,524. This creates potential for filesystem corruption, security issues, and cross-platform compatibility problems.

#18: Race in fileMap Iteration
Iterates over fileMap with RLock, but map could be modified after lock release in operations.go:192-199,294-301. This can lead to potential use of invalid handles, race conditions, and undefined behavior.

#19: Mode Validation Insufficient
Only validates one bit (0x8000) instead of full mode validation in nfs_operations.go:462-468,541-547. Invalid modes could be set, leading to permission bypass potential and filesystem inconsistency.

#20: No Symlink Support
NFSv3 supports symlinks (SYMLINK, READLINK operations) but absnfs doesn't implement them, despite absfs providing SymLinker interface. This results in incomplete NFSv3 implementation and compatibility issues with clients expecting symlinks.

#33: [HIGH] No TLS/Encryption - All Data Sent in Plaintext
All NFS traffic is transmitted over unencrypted TCP in server.go:200. File data, metadata, and credentials are visible to network attackers. This enables attackers to perform network sniffing, capture all file contents in plaintext, intercept credentials, and perform man-in-the-middle modifications.

#34: [HIGH] Unsynchronized Access to NFSNode.attrs
Multiple methods in NFSNode call n.attrs.Invalidate() without synchronization in nfs_node.go:46,57,127,143,149,155. Concurrent writes from different goroutines create data races on the validUntil field, causing corrupted attribute cache and undefined behavior that can be detected by `go test -race`.

#35: [HIGH] FileHandle Race in Batch Operations
File handles are looked up in the map, lock is released, then the handle is used for batch operations in operations.go:192-213,293-328. The handle could be released by another goroutine between lookup and use, causing use of invalid file handles and reading wrong files.

#36: [HIGH] ResultChan Never Closed in Batch Processing
All batch processing functions send results to req.ResultChan but never close it in batch.go:205-245,264-322,326-403,406-445,449-500. This leaks channels for every batched operation, causing significant memory leak under high load.

#37: [HIGH] Batch Replacement Race Condition
When a batch becomes full, the code replaces it with a new batch while still holding the old batch's mutex lock in batch.go:109-130. The old batch is then passed to a goroutine for processing while locked, creating potential deadlocks and incorrect batch processing.

---

## Medium Priority Issues

#38: [MEDIUM] Race on acceptErrs Counter
The acceptErrs field is read and written without synchronization in server.go:273,278,283, violating Go's memory model. While currently benign, this is a fragile race condition.

#39: [MEDIUM] Old taskQueue Not Closed in WorkerPool Resize
When resizing the worker pool in worker_pool.go:210, a new taskQueue is created without properly draining and closing the old one, causing channel and task leaks on resize.

#40: [MEDIUM] Documentation Claims Non-Existent Methods
The API documentation in docs/api/absfsnfs.md:46-67 describes three methods on AbsfsNFS that don't exist in the code: GetFileSystem(), GetExportOptions(), and UpdateExportOptions(options ExportOptions) error. This causes user confusion and broken example code.

#41: [MEDIUM] Documentation References Non-Existent Configuration Fields
Multiple guides and examples use configuration fields that don't exist in ExportOptions in docs/guides/configuration.md:77,136-139,154-161,488-494 and docs/examples/custom-export-options.md:62-63,283-285. Fields include LogLevel, LogClientIPs, LogOperations, LogFile, LogFormat, LogMaxSize, LogMaxBackups, LogMaxAge, LogCompress, CacheNegativeLookups, OperationTimeout, and HandleTimeout. This causes examples to not compile and creates user confusion.

#42: [MEDIUM] API Reference Missing 17 Implemented Configuration Fields
The API reference for ExportOptions in docs/api/export-options.md only documents 9 of 26 fields. Missing documentation for ReadAheadMaxFiles, ReadAheadMaxMemory, AdaptToMemoryPressure, MemoryHighWatermark, MemoryLowWatermark, MemoryCheckInterval, MaxWorkers, BatchOperations, MaxBatchSize, MaxConnections, IdleTimeout, TCPKeepAlive, TCPNoDelay, SendBufferSize, ReceiveBufferSize, Async, and MaxFileSize. Users are unaware of available features.

#43: [MEDIUM] Missing API Documentation for Public Types
Several public types lack dedicated API documentation: WorkerPool, BatchProcessor, NFSMetrics, MetricsCollector, and ServerOptions. Users can't effectively use advanced features without this documentation.

#47: [MEDIUM] Ignored Close Errors Throughout Codebase
File close errors are ignored in multiple locations including filehandle.go:43,54, server.go:141,288,435,472, nfs_node.go:25,35,44,55,67,85, and operations.go:220,335,382,454,86. This can lead to data loss, especially for write operations where buffered data may not be flushed, and resource leaks.

#48: [MEDIUM] Generic Error Messages Without Context
Error messages like "nil node", "empty path", "negative offset" appear in multiple operations without indicating which operation failed in operations.go:50,99,131,134,171,174,177,274,276,279,364,367,370,397,400,420,423,447,489. This makes debugging extremely difficult.

#49: [MEDIUM] Errors Not Properly Wrapped - Breaking Error Chain
Many errors use %v instead of %w for wrapping in rpc_types.go:142,148,157-192,202-222, operations.go (numerous locations), and server.go:207. This breaks the error chain and prevents use of errors.Is() and errors.As().

#50: [MEDIUM] Ignored TCP Socket Configuration Errors
All TCP socket configuration calls (SetKeepAlive, SetNoDelay, SetWriteBuffer, etc.) ignore errors in server.go:297,298,302,307,311. Calls like tcpConn.SetKeepAlive(true) and tcpConn.SetWriteBuffer() don't check for errors, causing silent configuration failures and performance degradation.

---

## Low Priority Issues

#44: [LOW] Test Compilation Errors in read_ahead_test.go
Multiple test files have incorrect expectations for buffer.Stats() return values (expects 3, returns 2) in read_ahead_test.go:217. Tests need to be updated to match the actual function signature.

#45: [LOW] Placeholder Error Classification Functions
Error classification functions in metrics_api.go (isStaleFileHandle(), isAuthError(), isResourceError()) return hardcoded false and have no tests. Error metrics are not categorized correctly.

#46: [LOW] Missing Tests for Critical Close() Method
The Close() method in types.go:371 performs critical cleanup but has no tests. Cleanup bugs may go undetected without comprehensive tests for Close() including error cases.

---

## Recently Resolved (Closed Issues)

The following critical issues were recently addressed and closed:
- #1: Update Outdated Dependencies with Known Bugs
- #2: Path Traversal Vulnerability in Lookup/Remove/Rename
- #3: XDR String Length Validation Missing - DoS Vulnerability
- #4: Integer Overflow in Read/Write Operations
- #5: Race Condition in AttrCache
- #6: Goroutine Leak in HandleCall
- #7: File Descriptor Leak
- #8: No Authentication Enforcement
- #32: AllowedIPs Security Control Not Enforced

Also resolved:
- #9: No Rate Limiting / DoS Protection (closed)
- #10: Inefficient File Handle Allocation - O(n) Performance (closed)
- #11: Inefficient LRU Implementation - O(n) Cache Access (closed)
- #14: Connection Tracking Race Condition (closed)
- #15: Unsafe Type Assertions Without Checking (closed)

---

**Last Updated:** 2025-11-22
