# Composing Filesystems: A Virtual Workspace Server

This tutorial demonstrates how to create an NFS server that exports a virtual filesystem composed of multiple underlying filesystems. This powerful pattern allows you to:

- Combine different storage backends into a unified view
- Apply different policies (read-only, in-memory, persistent) to different paths
- Create isolated workspaces with mixed storage characteristics

## What We'll Build

A "workspace" NFS server with the following structure:

```
/                     <- Virtual root
├── scratch/          <- In-memory temp space (fast, volatile)
├── project/          <- Real directory (persistent, read-write)
├── libs/             <- Real directory (read-only)
└── shared/           <- Real directory (read-write)
```

Each mount point can have different characteristics:
- **`/scratch`**: Lightning-fast in-memory storage, perfect for temp files and build artifacts
- **`/project`**: Your actual project directory on disk
- **`/libs`**: Read-only access to libraries or vendor dependencies
- **`/shared`**: Shared files accessible to all clients

## Prerequisites

```bash
go get github.com/absfs/absnfs
go get github.com/absfs/memfs
go get github.com/absfs/osfs
```

## The ComposedFS Pattern

The key abstraction is a `ComposedFS` that implements `absfs.SymlinkFileSystem` by routing operations to different underlying filesystems based on path prefixes:

```go
type ComposedFS struct {
    mounts map[string]*MountPoint
    root   *memfs.FileSystem
}

type MountPoint struct {
    Path string
    FS   absnfs.SymlinkFileSystem
    Info string
}
```

### Path Resolution

When a file operation comes in, we find the longest matching mount point:

```go
func (c *ComposedFS) resolveMount(path string) (*MountPoint, string) {
    var bestMount *MountPoint
    var bestPrefix string

    for prefix, mount := range c.mounts {
        if strings.HasPrefix(path, prefix) {
            if len(prefix) > len(bestPrefix) {
                bestMount = mount
                bestPrefix = prefix
            }
        }
    }

    if bestMount != nil {
        relPath := path[len(bestPrefix):]
        if relPath == "" {
            relPath = "/"
        }
        return bestMount, relPath
    }
    return nil, path
}
```

### Delegating Operations

Each filesystem operation delegates to the appropriate mount:

```go
func (c *ComposedFS) Open(name string) (absnfs.File, error) {
    mount, relPath := c.resolveMount(name)
    if mount != nil {
        return mount.FS.Open(relPath)
    }
    return c.root.Open(name)
}
```

## Read-Only Wrapper

To create a read-only view, wrap any filesystem:

```go
type ReadOnlyFS struct {
    fs absnfs.SymlinkFileSystem
}

func (r *ReadOnlyFS) Create(name string) (absnfs.File, error) {
    return nil, os.ErrPermission
}

func (r *ReadOnlyFS) OpenFile(name string, flag int, perm os.FileMode) (absnfs.File, error) {
    if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE) != 0 {
        return nil, os.ErrPermission
    }
    return r.fs.OpenFile(name, flag, perm)
}

// ... other write operations return os.ErrPermission
```

## Base Path Restriction

Restrict a filesystem to a specific directory:

```go
type BasePathFS struct {
    fs   absnfs.SymlinkFileSystem
    base string
}

func (b *BasePathFS) resolvePath(name string) string {
    clean := filepath.Clean(name)
    if clean == "." || clean == "/" {
        return b.base
    }
    return filepath.Join(b.base, strings.TrimPrefix(clean, "/"))
}

func (b *BasePathFS) Open(name string) (absnfs.File, error) {
    return b.fs.Open(b.resolvePath(name))
}
```

## Complete Example

See the full implementation in [`examples/composed-workspace/main.go`](../../examples/composed-workspace/main.go).

### Running the Server

```bash
# Build the example
cd examples/composed-workspace
go build -o workspace-server

# Run with custom directories
./workspace-server \
    -port 2049 \
    -project ~/myproject \
    -libs ~/go/pkg/mod \
    -shared /tmp/shared \
    -debug
```

### Command Line Options

| Flag | Description | Default |
|------|-------------|---------|
| `-port` | NFS server port | 2049 |
| `-project` | Project directory path | (none) |
| `-libs` | Libraries directory (read-only) | (none) |
| `-shared` | Shared directory path | (none) |
| `-debug` | Enable debug logging | false |

## Mounting the Export

### macOS

```bash
# Create mount point
sudo mkdir -p /Volumes/workspace

# Mount the NFS share
sudo mount_nfs -o resvport,nolocks,vers=3,tcp,port=2049,mountport=2049 \
    localhost:/ /Volumes/workspace

# Verify mount
ls /Volumes/workspace
# Should show: scratch/ project/ libs/ shared/

# Unmount when done
sudo umount /Volumes/workspace
```

**macOS Mount Options Explained:**
- `resvport`: Use a reserved port (required by many NFS servers)
- `nolocks`: Disable NFS locking (our server doesn't support it)
- `vers=3`: Use NFSv3 protocol
- `tcp`: Use TCP transport
- `port=2049`: NFS server port
- `mountport=2049`: Mount protocol port

### Linux

```bash
# Create mount point
sudo mkdir -p /mnt/workspace

# Mount the NFS share
sudo mount -t nfs -o vers=3,tcp,port=2049,mountport=2049,nolock \
    localhost:/ /mnt/workspace

# Verify mount
ls /mnt/workspace

# Unmount when done
sudo umount /mnt/workspace
```

**Linux Mount Options Explained:**
- `vers=3`: Use NFSv3 protocol
- `tcp`: Use TCP transport
- `port=2049`: NFS server port
- `mountport=2049`: Mount protocol port
- `nolock`: Disable NFS locking

**For persistent mounts, add to `/etc/fstab`:**
```
localhost:/  /mnt/workspace  nfs  vers=3,tcp,port=2049,mountport=2049,nolock  0  0
```

### Windows

Windows requires the NFS Client feature to be installed.

**1. Enable NFS Client (Run as Administrator):**

```powershell
# Windows 10/11 Pro or Enterprise
Enable-WindowsOptionalFeature -FeatureName ServicesForNFS-ClientOnly -Online

# Or via GUI: Settings > Apps > Optional Features > Add > "Services for NFS"
```

**2. Mount the share (Command Prompt as Administrator):**

```cmd
mount -o anon,nolock,vers=3,port=2049,mountport=2049 \\localhost\ W:
```

**3. Or use PowerShell:**

```powershell
New-PSDrive -Name W -PSProvider FileSystem -Root "\\localhost\" -Persist
```

**4. Verify the mount:**

```cmd
dir W:\
```

**5. Unmount when done:**

```cmd
umount W:
```

**Windows Troubleshooting:**
- If mount fails, ensure the NFS Client service is running: `sc query nfsclnt`
- Use `-o mtype=soft,retry=1` for faster timeout on connection issues
- Windows NFS client may require registry tweaks for anonymous access

## Use Cases

### Development Workspace

```bash
./workspace-server \
    -project ~/code/myapp \
    -libs ~/code/myapp/vendor \
    -shared /tmp/build-cache
```

Access your project with vendor libs as read-only, with a shared build cache.

### Multi-User Build Server

Multiple developers can mount the same workspace:
- Everyone sees the same `/libs` (read-only vendor dependencies)
- Each has their own `/scratch` (actually shared, but volatile)
- `/shared` for exchanging files

### Containerized Development

Export a workspace that containers can mount:

```bash
# Host runs the NFS server
./workspace-server -port 12049 -project ~/code -libs ~/vendor

# Containers mount it
docker run -v /mnt/workspace:/workspace myimage
```

## Advanced Patterns

### Layered Filesystems

Create an overlay-style filesystem:

```go
// Base layer (read-only)
baseFS := NewReadOnlyFS(osfsBase)
composed.Mount("/base", baseFS, "Read-only base layer")

// Overlay layer (read-write, in-memory)
overlayFS, _ := memfs.NewFS()
composed.Mount("/overlay", overlayFS, "Writable overlay")
```

### Per-User Scratch Space

```go
// Create unique scratch per connection
func (c *ComposedFS) createUserScratch(userID string) error {
    scratchFS, _ := memfs.NewFS()
    return c.Mount("/scratch/"+userID, scratchFS, "User scratch: "+userID)
}
```

### Quota-Limited Filesystems

Wrap filesystems with size limits:

```go
type QuotaFS struct {
    fs        absnfs.SymlinkFileSystem
    maxBytes  int64
    usedBytes int64
}

func (q *QuotaFS) Create(name string) (absnfs.File, error) {
    if q.usedBytes >= q.maxBytes {
        return nil, syscall.ENOSPC
    }
    return q.fs.Create(name)
}
```

## Performance Considerations

1. **In-Memory (`/scratch`)**: Fastest, but limited by available RAM
2. **Local Disk**: Good performance, persistent
3. **Read-Only**: Slightly faster (no write tracking)
4. **Network Backends**: Consider caching for remote filesystems

## Security Notes

- The example server has no authentication (AUTH_NONE)
- For production, implement proper access controls
- Consider using TLS (see absnfs TLS options)
- Restrict which directories can be exported

## Next Steps

- Add authentication and access control
- Implement quota enforcement
- Add filesystem event logging
- Create a web dashboard for monitoring

## Complete Code

The full working example is available at:

```
examples/composed-workspace/main.go
```

Build and run:

```bash
cd examples/composed-workspace
go build
./composed-workspace -project /path/to/project -debug
```
