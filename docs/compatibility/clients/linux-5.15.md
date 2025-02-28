---
layout: default
title: Linux Kernel 5.15+ Compatibility
---

# Linux Kernel 5.15+ Compatibility (In Progress)

**Test Date:** 2023-07-15 (Testing in progress)  
**Tester:** ABSNFS Team  
**ABSNFS Version:** 0.1.0  
**Client OS/Environment:** Ubuntu 22.04 LTS with Linux Kernel 5.15.0-78-generic  

## Compatibility Summary

- **Overall Rating:** 🔄 Testing in Progress
- **Recommended For:** General-purpose NFS usage
- **Major Limitations:** Being evaluated

## Mount Operations

| Mount Option | Supported | Notes |
|--------------|:---------:|-------|
| Default (no options) | ✅ | Works as expected |
| `-o ro` (read-only) | ✅ | Read-only enforcement works properly |
| `-o rw` (read-write) | ✅ | Read-write operations function correctly |
| `-o soft` | 🔄 | Currently testing timeout scenarios |
| `-o hard` | 🔄 | Currently testing recovery scenarios |
| `-o timeo=X` | 🔄 | Testing with various timeout values |
| `-o retrans=X` | 🔄 | Testing with different retry counts |
| `-o rsize=X` | ✅ | Tested with 4K, 32K, 64K, 1M blocks |
| `-o wsize=X` | ✅ | Tested with 4K, 32K, 64K, 1M blocks |
| `-o nolock` | 🔄 | Testing in progress |
| `-o actimeo=X` | 🔄 | Testing with various cache timeout values |
| `-o bg` | 🔄 | Testing background mounting |

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
| **File Attributes** | | |
| Permission Reading | ✅ | Correctly displays file permissions |
| Permission Setting | ✅ | Changes permissions successfully |
| Timestamp Preservation | ✅ | Preserves access and modification times |
| Extended Attributes | 🔄 | Testing in progress |
| **Special Cases** | | |
| File Locking | 🔄 | Basic locking tests passed, advanced tests in progress |
| Large Files (>2GB) | ✅ | Successfully handles files up to 10GB in testing |
| Large Files (>4GB) | ✅ | Successfully handles files up to 10GB in testing |
| Unicode Filenames | ✅ | Correctly handles UTF-8 filenames |
| Long Paths | 🔄 | Testing paths approaching 4096 character limit |
| Special Characters | ✅ | Handles special characters in filenames correctly |
| **Reliability** | | |
| Reconnection Behavior | 🔄 | Testing in progress |
| Server Restart Handling | 🔄 | Testing in progress |
| Network Interruption | 🔄 | Testing in progress |
| Concurrent Access | 🔄 | Initial tests promising, detailed testing in progress |

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

1. **Issue:** Occasional stale file handle errors after heavy file deletion operations  
   **Investigation:** Currently investigating the exact conditions that trigger this issue

2. **Issue:** Suboptimal default read-ahead behavior with certain workloads  
   **Workaround:** Using `-o rsize=65536` improves performance for sequential reads

## Recommended Configuration (Preliminary)

```bash
# Current recommended mount command for Linux 5.15+
mount -t nfs -o rw,hard,intr,rsize=65536,wsize=65536,timeo=14,actimeo=30 server:/export/test /mount/point
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
- [ ] TC006: Special cases (in progress)
- [ ] TC007: Concurrency testing (in progress)
- [ ] TC008: Error handling (in progress)
- [x] TC009: Performance benchmarking (preliminary)

## Next Steps

1. Complete testing of reliability scenarios
2. Finalize concurrency testing
3. Test with additional mount options
4. Verify behavior with different kernel configurations