# Integration Testing Safety Procedures

## Overview

This document defines the safety procedures for NFS integration testing with elevated privileges. These procedures are designed to prevent any accidental damage to existing filesystems, files, or directories.

**CRITICAL**: All privileged operations must follow these procedures exactly. No exceptions.

## Safety Principles

1. **Isolation First** - All testing occurs in dedicated, clearly-marked directories
2. **Verify Before Acting** - Every destructive operation requires pre-verification
3. **Absolute Paths Only** - Never use relative paths for privileged operations
4. **Marker Files** - Use marker files to confirm we're in the correct location
5. **Unique Naming** - Use unique identifiers to prevent path collisions
6. **Explicit Cleanup** - Clean up is explicit, verified, and logged
7. **Fail Safe** - When in doubt, abort rather than proceed

## Protected Paths (NEVER OPERATE ON)

The following paths must NEVER be targets of mount, unmount, rm, or rmdir operations:

```
/
/bin
/boot
/dev
/etc
/home
/lib
/lib64
/opt
/private
/proc
/root
/sbin
/sys
/tmp
/usr
/var
/Applications
/Library
/System
/Users (except designated test paths)
/Volumes (except our test mounts)
```

## Designated Test Locations

All integration tests MUST use these specific paths:

```
Primary:    /mnt/absnfs-test-{uuid}
Fallback:   /tmp/absnfs-test-{uuid}
```

Where `{uuid}` is a unique identifier generated per test session.

## Test Session Lifecycle

### Phase 1: Environment Preparation

```bash
# 1. Generate unique session ID
TEST_SESSION_ID="absnfs-$(date +%Y%m%d-%H%M%S)-$$"

# 2. Define test mount point (MUST be under /mnt or /tmp)
TEST_MOUNT_BASE="/mnt"
TEST_MOUNT_POINT="${TEST_MOUNT_BASE}/${TEST_SESSION_ID}"

# 3. Verify base directory exists and is safe
if [[ ! -d "${TEST_MOUNT_BASE}" ]]; then
    echo "SAFETY: Base mount directory does not exist, creating..."
    sudo mkdir -p "${TEST_MOUNT_BASE}"
fi

# 4. Verify mount point does NOT already exist (prevents mounting over existing data)
if [[ -e "${TEST_MOUNT_POINT}" ]]; then
    echo "FATAL: Test mount point already exists: ${TEST_MOUNT_POINT}"
    echo "SAFETY: Refusing to proceed - manual cleanup required"
    exit 1
fi

# 5. Create mount point
sudo mkdir -p "${TEST_MOUNT_POINT}"

# 6. Create marker file in parent to verify location
MARKER_FILE="${TEST_MOUNT_BASE}/.absnfs-test-marker-${TEST_SESSION_ID}"
echo "ABSNFS_TEST_SESSION=${TEST_SESSION_ID}" | sudo tee "${MARKER_FILE}" > /dev/null

# 7. Verify marker file was created correctly
if [[ ! -f "${MARKER_FILE}" ]]; then
    echo "FATAL: Could not create marker file"
    exit 1
fi
```

### Phase 2: Pre-Mount Verification

Before mounting, verify:

```bash
# 1. Mount point exists and is empty
if [[ ! -d "${TEST_MOUNT_POINT}" ]]; then
    echo "FATAL: Mount point does not exist"
    exit 1
fi

if [[ -n "$(ls -A "${TEST_MOUNT_POINT}" 2>/dev/null)" ]]; then
    echo "FATAL: Mount point is not empty"
    exit 1
fi

# 2. Mount point is NOT already a mount
if mount | grep -q " ${TEST_MOUNT_POINT} "; then
    echo "FATAL: Mount point already has something mounted"
    exit 1
fi

# 3. Path is under allowed base
case "${TEST_MOUNT_POINT}" in
    /mnt/absnfs-*|/tmp/absnfs-*)
        echo "SAFETY: Mount point path verified"
        ;;
    *)
        echo "FATAL: Mount point not under allowed base: ${TEST_MOUNT_POINT}"
        exit 1
        ;;
esac
```

### Phase 3: Mount Operation

```bash
# 1. Log the operation
echo "[$(date)] MOUNT: ${TEST_MOUNT_POINT}" >> /tmp/absnfs-test-operations.log

# 2. Perform mount with explicit options
sudo mount_nfs -o resvport,nolocks,vers=3,tcp,port=${NFS_PORT},mountport=${MOUNT_PORT} \
    localhost:/ "${TEST_MOUNT_POINT}"

# 3. Verify mount succeeded
if ! mount | grep -q " ${TEST_MOUNT_POINT} "; then
    echo "ERROR: Mount verification failed"
    exit 1
fi

# 4. Create test marker inside mount to verify it's our filesystem
TEST_MOUNT_MARKER="${TEST_MOUNT_POINT}/.absnfs-mount-marker"
echo "MOUNTED_AT=$(date)" > "${TEST_MOUNT_MARKER}" 2>/dev/null || true
```

### Phase 4: Test Execution

During tests:

```bash
# Always verify we're still mounted before file operations
verify_mounted() {
    if ! mount | grep -q " ${TEST_MOUNT_POINT} "; then
        echo "FATAL: Mount disappeared during test"
        exit 1
    fi
}

# Always use absolute paths
create_test_file() {
    local filename="$1"
    local fullpath="${TEST_MOUNT_POINT}/${filename}"

    # Verify path is under mount point
    case "${fullpath}" in
        ${TEST_MOUNT_POINT}/*)
            echo "test content" > "${fullpath}"
            ;;
        *)
            echo "FATAL: Path escape attempt: ${fullpath}"
            exit 1
            ;;
    esac
}
```

### Phase 5: Pre-Unmount Verification

```bash
# 1. Verify this is our test mount
if ! mount | grep -q " ${TEST_MOUNT_POINT} "; then
    echo "WARNING: Nothing mounted at ${TEST_MOUNT_POINT}"
    # Safe to skip unmount
else
    # 2. Verify path pattern before unmount
    case "${TEST_MOUNT_POINT}" in
        /mnt/absnfs-*|/tmp/absnfs-*)
            echo "SAFETY: Unmount path verified"
            ;;
        *)
            echo "FATAL: Refusing to unmount non-test path: ${TEST_MOUNT_POINT}"
            exit 1
            ;;
    esac
fi
```

### Phase 6: Unmount Operation

```bash
# 1. Log the operation
echo "[$(date)] UNMOUNT: ${TEST_MOUNT_POINT}" >> /tmp/absnfs-test-operations.log

# 2. Perform unmount
sudo umount "${TEST_MOUNT_POINT}" 2>/dev/null || \
    sudo umount -f "${TEST_MOUNT_POINT}" 2>/dev/null || \
    sudo diskutil unmount force "${TEST_MOUNT_POINT}" 2>/dev/null || true

# 3. Verify unmount succeeded
sleep 1
if mount | grep -q " ${TEST_MOUNT_POINT} "; then
    echo "WARNING: Unmount may have failed, attempting force unmount"
    sudo umount -f "${TEST_MOUNT_POINT}" 2>/dev/null || true
fi
```

### Phase 7: Cleanup

```bash
cleanup_test_session() {
    local mount_point="$1"
    local marker_file="$2"

    # 1. Verify path pattern before any deletion
    case "${mount_point}" in
        /mnt/absnfs-*|/tmp/absnfs-*)
            ;;
        *)
            echo "FATAL: Refusing to clean non-test path: ${mount_point}"
            return 1
            ;;
    esac

    # 2. Ensure not mounted
    if mount | grep -q " ${mount_point} "; then
        echo "FATAL: Cannot cleanup - still mounted: ${mount_point}"
        return 1
    fi

    # 3. Verify directory is empty or contains only our test files
    if [[ -d "${mount_point}" ]]; then
        local file_count=$(find "${mount_point}" -mindepth 1 -maxdepth 1 | wc -l)
        if [[ ${file_count} -gt 0 ]]; then
            echo "WARNING: Mount point not empty, listing contents:"
            ls -la "${mount_point}"
            # Only remove if files match our patterns
            if find "${mount_point}" -mindepth 1 -name ".absnfs-*" -o -name "test-*" | head -1 | grep -q .; then
                echo "Contents appear to be test files, proceeding with cleanup"
            else
                echo "FATAL: Unknown files in mount point, refusing to delete"
                return 1
            fi
        fi
    fi

    # 4. Log the operation
    echo "[$(date)] CLEANUP: ${mount_point}" >> /tmp/absnfs-test-operations.log

    # 5. Remove mount point directory
    if [[ -d "${mount_point}" ]]; then
        sudo rmdir "${mount_point}" 2>/dev/null || \
            sudo rm -rf "${mount_point}"
    fi

    # 6. Remove marker file
    if [[ -f "${marker_file}" ]]; then
        sudo rm -f "${marker_file}"
    fi

    # 7. Verify cleanup
    if [[ -e "${mount_point}" ]]; then
        echo "WARNING: Mount point still exists after cleanup"
        return 1
    fi

    echo "SAFETY: Cleanup completed successfully"
    return 0
}
```

## Emergency Procedures

### If a mount is stuck:

```bash
# List all NFS mounts
mount | grep nfs

# Force unmount (macOS)
sudo diskutil unmount force /mnt/absnfs-*

# Kill any stuck mount processes
sudo killall -9 mount_nfs 2>/dev/null || true
```

### If cleanup fails:

```bash
# Manual verification
ls -la /mnt/

# Manual cleanup (VERIFY PATH FIRST)
# ONLY if path matches /mnt/absnfs-* pattern
sudo umount -f /mnt/absnfs-SPECIFIC-SESSION-ID
sudo rm -rf /mnt/absnfs-SPECIFIC-SESSION-ID
```

### Recovery checklist:

1. [ ] List all mounts: `mount | grep absnfs`
2. [ ] List test directories: `ls -la /mnt/absnfs-* /tmp/absnfs-* 2>/dev/null`
3. [ ] Unmount any stuck mounts
4. [ ] Remove test directories
5. [ ] Verify no test artifacts remain

## Automated Safety Wrapper

All integration tests MUST use the safety wrapper functions:

```go
// SafeTestMount encapsulates all safety checks for mounting
type SafeTestMount struct {
    SessionID    string
    MountPoint   string
    MarkerFile   string
    NFSPort      int
    MountPort    int
    IsMounted    bool
}

// NewSafeTestMount creates a new safe test mount with unique identifiers
func NewSafeTestMount() (*SafeTestMount, error)

// Mount performs a safe mount with all precondition checks
func (s *SafeTestMount) Mount() error

// Unmount performs a safe unmount with verification
func (s *SafeTestMount) Unmount() error

// Cleanup removes all test artifacts after verification
func (s *SafeTestMount) Cleanup() error

// MustCleanup is a defer-safe cleanup that logs but doesn't panic
func (s *SafeTestMount) MustCleanup()
```

## Logging Requirements

All privileged operations MUST be logged to `/tmp/absnfs-test-operations.log`:

```
[2024-01-15 10:30:45] SESSION_START: absnfs-20240115-103045-12345
[2024-01-15 10:30:45] CREATE_MOUNTPOINT: /mnt/absnfs-20240115-103045-12345
[2024-01-15 10:30:46] MOUNT: /mnt/absnfs-20240115-103045-12345
[2024-01-15 10:30:50] TEST: basic_file_operations
[2024-01-15 10:30:55] UNMOUNT: /mnt/absnfs-20240115-103045-12345
[2024-01-15 10:30:56] CLEANUP: /mnt/absnfs-20240115-103045-12345
[2024-01-15 10:30:56] SESSION_END: absnfs-20240115-103045-12345
```

## Pre-Flight Checklist

Before running ANY integration test:

- [ ] Verify no existing absnfs test mounts: `mount | grep absnfs`
- [ ] Verify no leftover test directories: `ls /mnt/absnfs-* 2>/dev/null`
- [ ] Verify NFS server is not already running on test ports
- [ ] Verify adequate disk space
- [ ] Verify sudoers configuration is correct

## Post-Test Checklist

After EVERY integration test:

- [ ] All mounts unmounted: `mount | grep absnfs` returns nothing
- [ ] All test directories removed: `ls /mnt/absnfs-* 2>/dev/null` returns nothing
- [ ] NFS server stopped
- [ ] Review operation log for anomalies
