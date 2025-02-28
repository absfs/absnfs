---
layout: default
title: Client Compatibility
---

# ABSNFS Client Compatibility

This section contains information about ABSNFS compatibility with various NFS clients across different operating systems and environments.

## Compatibility Matrix

The following matrix provides a quick overview of compatibility status with different NFS clients:

| Client | Version | Basic Mount | Read Ops | Write Ops | Attrs | Locking | Large Files | Unicode | Overall |
|--------|---------|:-----------:|:--------:|:---------:|:-----:|:-------:|:-----------:|:-------:|:-------:|
| macOS | 15.4 (Sequoia) | 🔄 | 🔄 | 🔄 | 🔄 | 🔄 | 🔄 | 🔄 | [🔄](./clients/macos-15.4.md) |
| Linux Kernel | 5.15+ | 🔄 | 🔄 | 🔄 | 🔄 | 🔄 | 🔄 | 🔄 | [🔄](./clients/linux-5.15.md) |
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

We're actively testing ABSNFS compatibility with various NFS clients. The current status of the project:

- [Detailed Progress Tracking](./progress.md)
- [Latest Weekly Report](./progress-reports/2023-07-25.md)
- [Testing Methodology](./testing/methodology.md)
- [Setup Instructions](./setup-instructions.md)

### Overall Progress
- Phase 1 (Research): 🔄 In Progress (80%)
- Phase 2 (Core Testing): 🔄 In Progress (20%)
- Phase 3 (Expanded Testing): ⏳ Not Started
- Phase 4 (Documentation): ⏳ Not Started
- Phase 5 (Maintenance): ⏳ Not Started

## Client-Specific Documentation

For detailed compatibility information, please see the client-specific pages:

### macOS Clients
- [macOS 15.4 (Sequoia)](./clients/macos-15.4.md) 🔄
- macOS 13.x ⏳
- macOS 12.x ⏳

### Linux Clients
- [Linux Kernel 5.15+](./clients/linux-5.15.md) 🔄
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

## Weekly Progress Reports

We publish weekly progress reports on our compatibility testing efforts:

- [Week of July 25, 2023](./progress-reports/2023-07-25.md) (Latest)
- [Week of July 15, 2023](./progress-reports/2023-07-15.md)

## Testing Resources

- [Testing Methodology](./testing/methodology.md): How we test client compatibility
- [Test Templates](./testing/templates.md): Templates for consistent documentation
- [Progress Report Template](./testing/progress-report-template.md): Template for weekly reports