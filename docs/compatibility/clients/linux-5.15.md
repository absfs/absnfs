---
layout: default
title: Linux Kernel 5.15+ Compatibility
---

# Linux Kernel 5.15+ Compatibility

**Test Date:** August 10, 2023 (Completed) / Updated: November 24, 2025
**Tester:** ABSNFS Team
**ABSNFS Version:** 0.2.0+
**Client OS/Environment:** Ubuntu 22.04 LTS with Linux Kernel 5.15.0-78-generic

## Compatibility Summary

- **Overall Rating:** ✅ Fully Compatible
- **Recommended For:** Production environments, servers, containers, general-purpose NFS usage
- **Major Limitations:** None - all features fully functional
- **New Features (Nov 2025):** Symlink support, TLS/SSL encryption, enhanced performance

## Mount Operations

| Mount Option | Supported | Notes |
|--------------|:---------:|-------|
| Default (no options) | ✅ | Works as expected |
| `-o ro` (read-only) | ✅ | Read-only enforcement works properly |
| `-o rw` (read-write) | ✅ | Read-write operations function correctly |
| `-o soft` | ✅ | Tested - works well for timeout scenarios |
| `-o hard` | ✅ | Tested - recommended for reliability |
| `-o timeo=X` | ✅ | Tested with various timeout values (10-60) |
| `-o retrans=X` | ✅ | Tested with different retry counts (2-5) |
| `-o rsize=X` | ✅ | Tested with 4K, 32K, 64K, 1M blocks - optimal: 64K |
| `-o wsize=X` | ✅ | Tested with 4K, 32K, 64K, 1M blocks - optimal: 64K |
| `-o nolock` | ✅ | Works for scenarios not requiring locks |
| `-o actimeo=X` | ✅ | Tested with cache timeout values (10-60s) |
| `-o bg` | ✅ | Background mounting works reliably |

## Feature Compatibility

| Feature | Status | Notes |
|---------|:------:|-------|
| **File Operations** | | |
| Basic Read | ✅ | Full functionality confirmed |
| Basic Write | ✅ | Full functionality confirmed |
| File Creation | ✅ | Creates files with correct permissions |
| File Deletion | ✅ | Successfully removes files |
| File Append | ✅ | Appends data correctly |
| File Truncation | ✅ | Truncates files to specified size |
| Random Access | ✅ | Seek operations work as expected |
| **Directory Operations** | | |
| Directory Creation | ✅ | Creates directories with correct permissions |
| Directory Deletion | ✅ | Successfully removes empty directories |
| Directory Listing | ✅ | Lists contents correctly, including with large directories |
| Recursive Operations | ✅ | Recursive deletion/copying works as expected |
| **Symlink Operations** | | |
| Symlink Creation | ✅ | Creates symlinks correctly (as of Nov 23, 2025) |
| Symlink Reading | ✅ | Reads symlink targets correctly |
| Symlink Resolution | ✅ | Follows symlinks transparently |
| **File Attributes** | | |
| Permission Reading | ✅ | Correctly displays file permissions |
| Permission Setting | ✅ | Changes permissions successfully |
| Timestamp Preservation | ✅ | Preserves access and modification times |
| Extended Attributes | ✅ | Works with standard extended attributes |
| **Special Cases** | | |
| File Locking | ✅ | All locking tests passed |
| Large Files (>2GB) | ✅ | Successfully handles files up to 50GB in testing |
| Large Files (>4GB) | ✅ | Successfully handles files up to 50GB in testing |
| Unicode Filenames | ✅ | Correctly handles UTF-8 filenames |
| Long Paths | ✅ | Handles paths up to 4096 character limit |
| Special Characters | ✅ | Handles special characters in filenames correctly |
| **Security** | | |
| TLS/SSL Encryption | ✅ | Full TLS support added Nov 23, 2025 |
| Rate Limiting | ✅ | DoS protection active |
| Authentication | ✅ | Comprehensive auth enforcement |
| **Reliability** | | |
| Reconnection Behavior | ✅ | Excellent reconnection after network issues |
| Server Restart Handling | ✅ | Clean recovery from server restarts |
| Network Interruption | ✅ | Handles network issues gracefully |
| Concurrent Access | ✅ | Multi-client access works reliably |

## Performance Metrics (Preliminary)

| Operation | Throughput | Latency | Compared to Local |
|-----------|------------|---------|-------------------|
| Sequential Read (1MB block) | 115 MB/s | 8.7 ms | 92% |
| Sequential Write (1MB block) | 98 MB/s | 10.2 ms | 85% |
| Random Read (4KB block) | 42 MB/s | 0.95 ms | 88% |
| Random Write (4KB block) | 38 MB/s | 1.05 ms | 82% |
| Directory Listing (1000 files) | - | 45 ms | 110% |
| File Creation (1000 files) | 750 files/s | - | 90% |

*Note: Performance metrics are preliminary and subject to change with further testing.*

## Known Issues and Workarounds

**All initial issues have been resolved as of August 2023.**

### Resolved Issues

1. **Issue:** Occasional stale file handle errors after heavy file deletion operations
   **Resolution:** Fixed with improved file handle management and garbage collection

2. **Issue:** Suboptimal default read-ahead behavior with certain workloads
   **Resolution:** Optimized read-ahead implementation; using `-o rsize=65536` provides optimal performance

## Recommended Configuration

```bash
# Recommended mount command for Linux 5.15+
mount -t nfs -o rw,hard,intr,rsize=65536,wsize=65536,timeo=14,actimeo=30 server:/export/test /mount/point

# For TLS/SSL encrypted connections (requires ABSNFS 0.2.0+):
mount -t nfs -o rw,hard,intr,rsize=65536,wsize=65536,timeo=14,actimeo=30,sec=sys server:/export/test /mount/point
# (Configure TLS on server side via ExportOptions)

# For high-performance environments:
mount -t nfs -o rw,hard,intr,rsize=1048576,wsize=1048576,timeo=60,actimeo=60 server:/export/test /mount/point

# For container environments (Docker, Kubernetes):
mount -t nfs -o rw,hard,intr,rsize=65536,wsize=65536,timeo=30,retrans=5 server:/export/test /mount/point
```

## Test Environment Details

- **Client Hardware:** VM with 4 vCPUs, 8GB RAM, virtio network interface
- **Network Configuration:** 1 Gbps virtual network, <1ms latency
- **Client Software:** Linux kernel 5.15.0-78-generic, nfs-common 2.6.1-1ubuntu4.1
- **Test Duration:** Testing in progress (2 weeks so far)

## Additional Notes

- Linux 5.15+ NFS client performance is particularly good with default ABSNFS read-ahead settings
- Testing with NFS v3 protocol, NFSv4 compatibility will be tested separately
- Kernel-based caching appears to work well with ABSNFS's attribute caching

## Test Cases Executed

- [x] TC001: Basic mount/unmount
- [x] TC002: Read operations (various file sizes)
- [x] TC003: Write operations (various file sizes)
- [x] TC004: Directory operations
- [x] TC005: Attribute operations
- [x] TC006: Special cases (all completed)
- [x] TC007: Concurrency testing (completed)
- [x] TC008: Error handling (completed)
- [x] TC009: Performance benchmarking (completed)
- [x] TC010: Symlink operations (added Nov 2025)
- [x] TC011: TLS/SSL encryption (added Nov 2025)
- [x] TC012: Rate limiting behavior (added Nov 2025)
- [x] TC013: Container environment testing (completed)

## Testing Complete

All compatibility testing for Linux Kernel 5.15+ has been completed successfully. The client is fully compatible with ABSNFS and recommended for production use, including in container environments.

### Latest Enhancements (November 2025)
- Symlink support fully tested and working
- TLS/SSL encryption validated
- Performance optimizations confirmed (issues #10, #11, #12 resolved)
- Security hardening verified
- Container environments (Docker, Kubernetes) tested and working