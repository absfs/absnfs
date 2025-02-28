# Client Compatibility: macOS 15.4 (Sequoia)

## Client Information
- **Client OS/Name:** macOS 15.4 (Sequoia)
- **Version:** 15.4 (24E11)
- **Environment:** Apple Silicon (M2 Pro)

## Testing Status
- [x] Research client behavior and requirements
- [x] Set up test environment
- [x] Execute basic mount tests
- [x] Test file operations
- [x] Test directory operations
- [x] Test attribute handling
- [ ] Test special cases
- [ ] Test error handling
- [x] Benchmark performance
- [ ] Document findings
- [x] Create compatibility report
- [x] Update compatibility matrix

## Notes
macOS 15.4 includes the native NFS client which appears to have good compatibility with ABSNFS. A few issues have been identified:

1. The Finder sometimes shows "Operation not permitted" when trying to modify files created on the server with specific permissions. We're investigating the exact conditions that trigger this.

2. There's an occasional disconnect when the system goes to sleep. Adding `-o soft` mount option helps with recovery.

Performance is generally good, with throughput close to local file access for sequential operations.

## Resources
- [Apple NFS Documentation](https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man8/mount_nfs.8.html)
- [macOS 15.4 Release Notes](https://developer.apple.com/documentation/macos-release-notes/macos-15_4-release-notes)