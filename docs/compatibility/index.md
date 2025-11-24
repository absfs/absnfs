---
layout: default
title: Client Compatibility
---

# ABSNFS Client Compatibility

This section contains information about ABSNFS compatibility with various NFS clients across different operating systems and environments.

## Compatibility Matrix

The following matrix provides a quick overview of compatibility status with different NFS clients:

| Client | Version | Basic Mount | Read Ops | Write Ops | Attrs | Locking | Large Files | Symlinks | Overall |
|--------|---------|:-----------:|:--------:|:---------:|:-----:|:-------:|:-----------:|:--------:|:-------:|
| macOS | 15.4 (Sequoia) | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | [✅](./clients/macos-15.4.md) |
| Linux Kernel | 5.15+ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | [✅](./clients/linux-5.15.md) |
| Linux Kernel | 4.x | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ |
| macOS | 13.x | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ |
| macOS | 12.x | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ |
| Windows | 11 | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ |
| Windows | 10 | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ |
| FreeBSD | 13.x | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ |
| VMware ESXi | 7.x | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ |
| Kubernetes NFS-Client | - | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ |

**Legend:**
- ✅ Fully Compatible
- ⚠️ Mostly Compatible (minor issues)
- ⛔ Partially Compatible (major issues)
- ❌ Not Compatible
- 🔄 Testing in Progress
- ⏳ Not Yet Tested

## Project Status

ABSNFS has completed compatibility testing with core NFS clients and implemented major features. Current status as of November 2025:

- [Detailed Progress Tracking](./progress.md)
- [Latest Weekly Report](./progress-reports/2023-07-25.md)
- [Testing Methodology](./testing/methodology.md)
- [Setup Instructions](./setup-instructions.md)

### Overall Progress
- Phase 1 (Research): ✅ Complete (100%)
- Phase 2 (Core Testing): ✅ Complete (100%)
- Phase 3 (Expanded Testing): 🔄 In Progress (30%)
- Phase 4 (Documentation): ✅ Complete (100%)
- Phase 5 (Maintenance): 🔄 Ongoing

### Recent Achievements (November 2025)
- ✅ Symlink support implemented (commit c8c8c92, Nov 23, 2025)
- ✅ TLS/SSL encryption system added (commit a4e9573, Nov 23, 2025)
- ✅ Performance issues #10, #11, #12 resolved (Nov 22, 2025)
- ✅ Security vulnerabilities fixed (rate limiting, authentication, path traversal)
- ✅ Resource leak and race condition fixes completed

## Client-Specific Documentation

For detailed compatibility information, please see the client-specific pages:

### macOS Clients
- [macOS 15.4 (Sequoia)](./clients/macos-15.4.md) ✅
- macOS 13.x ⏳
- macOS 12.x ⏳

### Linux Clients
- [Linux Kernel 5.15+](./clients/linux-5.15.md) ✅
- Linux Kernel 4.x ⏳

### Windows Clients
- Windows 11 ⏳
- Windows 10 ⏳

### Other Clients
- FreeBSD 13.x ⏳
- VMware ESXi 7.x ⏳
- Kubernetes NFS-Client ⏳

## Contributing

If you're using ABSNFS with a client not listed above or have additional information about listed clients, please consider contributing your findings.

See our [Contributing Guide](./contributing.md) for information on how to share your compatibility experiences.

## Historical Progress Reports

Archive of progress reports from initial compatibility testing phase:

- [Week of July 25, 2023](./progress-reports/2023-07-25.md)
- [Week of July 15, 2023](./progress-reports/2023-07-15.md)

*Note: These reports are from the initial testing phase. See [Progress](./progress.md) for current status.*

## Testing Resources

- [Testing Methodology](./testing/methodology.md): How we test client compatibility
- [Test Templates](./testing/templates.md): Templates for consistent documentation
- [Progress Report Template](./testing/progress-report-template.md): Template for weekly reports