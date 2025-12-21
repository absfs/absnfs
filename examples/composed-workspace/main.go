// Composed Workspace NFS Server
//
// This example demonstrates how to create an NFS server that exports
// a virtual filesystem composed of multiple absfs filesystems:
//
//   /project   - Real filesystem directory (read-write)
//   /scratch   - In-memory temp space (fast, volatile)
//   /libs      - Read-only view of a library/vendor directory
//   /shared    - Another real directory for shared files
//
// This creates a unified "workspace" view that clients can mount,
// with different storage characteristics for different paths.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/absfs/absfs"
	"github.com/absfs/absnfs"
	"github.com/absfs/memfs"
	"github.com/absfs/osfs"
)

// ComposedFS implements absfs.SymlinkFileSystem by routing operations
// to different underlying filesystems based on path prefixes.
type ComposedFS struct {
	mounts map[string]*MountPoint
	root   *memfs.FileSystem // Root directory structure
	cwd    string
}

// MountPoint represents a filesystem mounted at a specific path
type MountPoint struct {
	Path string
	FS   absfs.SymlinkFileSystem
	Info string // Description for logging
}

// NewComposedFS creates a new composed filesystem
func NewComposedFS() (*ComposedFS, error) {
	root, err := memfs.NewFS()
	if err != nil {
		return nil, fmt.Errorf("failed to create root fs: %w", err)
	}

	return &ComposedFS{
		mounts: make(map[string]*MountPoint),
		root:   root,
		cwd:    "/",
	}, nil
}

// Mount adds a filesystem at the specified path
func (c *ComposedFS) Mount(path string, fs absfs.SymlinkFileSystem, info string) error {
	// Create the mount point directory in the root
	if err := c.root.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("failed to create mount point %s: %w", path, err)
	}

	c.mounts[path] = &MountPoint{
		Path: path,
		FS:   fs,
		Info: info,
	}

	log.Printf("Mounted %s: %s", path, info)
	return nil
}

// resolveMount finds the appropriate mount point and relative path
func (c *ComposedFS) resolveMount(path string) (*MountPoint, string) {
	// Normalize path
	if path == "" {
		path = "/"
	}

	// Find the longest matching mount point
	var bestMount *MountPoint
	var bestPrefix string

	for prefix, mount := range c.mounts {
		if path == prefix || (len(path) > len(prefix) && path[:len(prefix)] == prefix && path[len(prefix)] == '/') {
			if len(prefix) > len(bestPrefix) {
				bestMount = mount
				bestPrefix = prefix
			}
		}
		// Exact match
		if path == prefix {
			return mount, "/"
		}
	}

	if bestMount != nil {
		// Return the relative path within the mount
		relPath := path[len(bestPrefix):]
		if relPath == "" {
			relPath = "/"
		}
		return bestMount, relPath
	}

	return nil, path
}

// Implement absfs.SymlinkFileSystem interface

func (c *ComposedFS) Open(name string) (absfs.File, error) {
	mount, relPath := c.resolveMount(name)
	if mount != nil {
		return mount.FS.Open(relPath)
	}
	return c.root.Open(name)
}

func (c *ComposedFS) OpenFile(name string, flag int, perm os.FileMode) (absfs.File, error) {
	mount, relPath := c.resolveMount(name)
	if mount != nil {
		return mount.FS.OpenFile(relPath, flag, perm)
	}
	return c.root.OpenFile(name, flag, perm)
}

func (c *ComposedFS) Create(name string) (absfs.File, error) {
	mount, relPath := c.resolveMount(name)
	if mount != nil {
		return mount.FS.Create(relPath)
	}
	return c.root.Create(name)
}

func (c *ComposedFS) Mkdir(name string, perm os.FileMode) error {
	mount, relPath := c.resolveMount(name)
	if mount != nil {
		return mount.FS.Mkdir(relPath, perm)
	}
	return c.root.Mkdir(name, perm)
}

func (c *ComposedFS) MkdirAll(path string, perm os.FileMode) error {
	mount, relPath := c.resolveMount(path)
	if mount != nil {
		return mount.FS.MkdirAll(relPath, perm)
	}
	return c.root.MkdirAll(path, perm)
}

func (c *ComposedFS) Remove(name string) error {
	mount, relPath := c.resolveMount(name)
	if mount != nil {
		return mount.FS.Remove(relPath)
	}
	return c.root.Remove(name)
}

func (c *ComposedFS) RemoveAll(path string) error {
	mount, relPath := c.resolveMount(path)
	if mount != nil {
		return mount.FS.RemoveAll(relPath)
	}
	return c.root.RemoveAll(path)
}

func (c *ComposedFS) Rename(oldpath, newpath string) error {
	oldMount, oldRel := c.resolveMount(oldpath)
	newMount, newRel := c.resolveMount(newpath)

	// Cross-mount renames are not supported
	if oldMount != newMount {
		return fmt.Errorf("cross-mount rename not supported")
	}

	if oldMount != nil {
		return oldMount.FS.Rename(oldRel, newRel)
	}
	return c.root.Rename(oldpath, newpath)
}

func (c *ComposedFS) Stat(name string) (os.FileInfo, error) {
	mount, relPath := c.resolveMount(name)
	if mount != nil {
		return mount.FS.Stat(relPath)
	}
	return c.root.Stat(name)
}

func (c *ComposedFS) Lstat(name string) (os.FileInfo, error) {
	mount, relPath := c.resolveMount(name)
	if mount != nil {
		return mount.FS.Lstat(relPath)
	}
	return c.root.Lstat(name)
}

func (c *ComposedFS) Chmod(name string, mode os.FileMode) error {
	mount, relPath := c.resolveMount(name)
	if mount != nil {
		return mount.FS.Chmod(relPath, mode)
	}
	return c.root.Chmod(name, mode)
}

func (c *ComposedFS) Chown(name string, uid, gid int) error {
	mount, relPath := c.resolveMount(name)
	if mount != nil {
		return mount.FS.Chown(relPath, uid, gid)
	}
	return c.root.Chown(name, uid, gid)
}

func (c *ComposedFS) Chtimes(name string, atime, mtime time.Time) error {
	mount, relPath := c.resolveMount(name)
	if mount != nil {
		return mount.FS.Chtimes(relPath, atime, mtime)
	}
	return c.root.Chtimes(name, atime, mtime)
}

func (c *ComposedFS) Readlink(name string) (string, error) {
	mount, relPath := c.resolveMount(name)
	if mount != nil {
		return mount.FS.Readlink(relPath)
	}
	return c.root.Readlink(name)
}

func (c *ComposedFS) Symlink(oldname, newname string) error {
	mount, relPath := c.resolveMount(newname)
	if mount != nil {
		return mount.FS.Symlink(oldname, relPath)
	}
	return c.root.Symlink(oldname, newname)
}

func (c *ComposedFS) Lchown(name string, uid, gid int) error {
	mount, relPath := c.resolveMount(name)
	if mount != nil {
		return mount.FS.Lchown(relPath, uid, gid)
	}
	return c.root.Lchown(name, uid, gid)
}

func (c *ComposedFS) ReadDir(name string) ([]fs.DirEntry, error) {
	// Handle root directory specially - show mount points
	if name == "/" || name == "" {
		entries, err := c.root.ReadDir("/")
		if err != nil {
			return nil, err
		}
		return entries, nil
	}

	mount, relPath := c.resolveMount(name)
	if mount != nil {
		return mount.FS.ReadDir(relPath)
	}
	return c.root.ReadDir(name)
}

func (c *ComposedFS) Chdir(dir string) error {
	// Verify directory exists
	info, err := c.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return &os.PathError{Op: "chdir", Path: dir, Err: syscall.ENOTDIR}
	}
	c.cwd = dir
	return nil
}

func (c *ComposedFS) Getwd() (string, error) {
	return c.cwd, nil
}

func (c *ComposedFS) TempDir() string {
	return "/scratch"
}

func (c *ComposedFS) Truncate(name string, size int64) error {
	mount, relPath := c.resolveMount(name)
	if mount != nil {
		return mount.FS.Truncate(relPath, size)
	}
	return c.root.Truncate(name, size)
}

func (c *ComposedFS) ReadFile(name string) ([]byte, error) {
	mount, relPath := c.resolveMount(name)
	if mount != nil {
		return mount.FS.ReadFile(relPath)
	}
	return c.root.ReadFile(name)
}

func (c *ComposedFS) Sub(dir string) (fs.FS, error) {
	mount, relPath := c.resolveMount(dir)
	if mount != nil {
		return mount.FS.Sub(relPath)
	}
	return c.root.Sub(dir)
}

// ReadOnlyFS wraps a filesystem to make it read-only
type ReadOnlyFS struct {
	fs  absfs.SymlinkFileSystem
	cwd string
}

func NewReadOnlyFS(fs absfs.SymlinkFileSystem) *ReadOnlyFS {
	return &ReadOnlyFS{fs: fs, cwd: "/"}
}

func (r *ReadOnlyFS) Open(name string) (absfs.File, error) {
	return r.fs.Open(name)
}

func (r *ReadOnlyFS) OpenFile(name string, flag int, perm os.FileMode) (absfs.File, error) {
	// Only allow read-only opens
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_APPEND|os.O_CREATE|os.O_TRUNC) != 0 {
		return nil, os.ErrPermission
	}
	return r.fs.OpenFile(name, flag, perm)
}

func (r *ReadOnlyFS) Create(name string) (absfs.File, error) {
	return nil, os.ErrPermission
}

func (r *ReadOnlyFS) Mkdir(name string, perm os.FileMode) error {
	return os.ErrPermission
}

func (r *ReadOnlyFS) MkdirAll(path string, perm os.FileMode) error {
	return os.ErrPermission
}

func (r *ReadOnlyFS) Remove(name string) error {
	return os.ErrPermission
}

func (r *ReadOnlyFS) RemoveAll(path string) error {
	return os.ErrPermission
}

func (r *ReadOnlyFS) Rename(oldpath, newpath string) error {
	return os.ErrPermission
}

func (r *ReadOnlyFS) Stat(name string) (os.FileInfo, error) {
	return r.fs.Stat(name)
}

func (r *ReadOnlyFS) Lstat(name string) (os.FileInfo, error) {
	return r.fs.Lstat(name)
}

func (r *ReadOnlyFS) Chmod(name string, mode os.FileMode) error {
	return os.ErrPermission
}

func (r *ReadOnlyFS) Chown(name string, uid, gid int) error {
	return os.ErrPermission
}

func (r *ReadOnlyFS) Chtimes(name string, atime, mtime time.Time) error {
	return os.ErrPermission
}

func (r *ReadOnlyFS) Readlink(name string) (string, error) {
	return r.fs.Readlink(name)
}

func (r *ReadOnlyFS) Symlink(oldname, newname string) error {
	return os.ErrPermission
}

func (r *ReadOnlyFS) Lchown(name string, uid, gid int) error {
	return os.ErrPermission
}

func (r *ReadOnlyFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return r.fs.ReadDir(name)
}

func (r *ReadOnlyFS) Chdir(dir string) error {
	info, err := r.fs.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return &os.PathError{Op: "chdir", Path: dir, Err: syscall.ENOTDIR}
	}
	r.cwd = dir
	return nil
}

func (r *ReadOnlyFS) Getwd() (string, error) {
	return r.cwd, nil
}

func (r *ReadOnlyFS) TempDir() string {
	return r.fs.TempDir()
}

func (r *ReadOnlyFS) Truncate(name string, size int64) error {
	return os.ErrPermission
}

func (r *ReadOnlyFS) ReadFile(name string) ([]byte, error) {
	return r.fs.ReadFile(name)
}

func (r *ReadOnlyFS) Sub(dir string) (fs.FS, error) {
	return r.fs.Sub(dir)
}

func main() {
	// Command line flags
	port := flag.Int("port", 2049, "NFS server port")
	projectDir := flag.String("project", "", "Project directory to mount at /project")
	libsDir := flag.String("libs", "", "Libraries directory to mount read-only at /libs")
	sharedDir := flag.String("shared", "", "Shared directory to mount at /shared")
	debug := flag.Bool("debug", false, "Enable debug logging")
	flag.Parse()

	// Create the composed filesystem
	composed, err := NewComposedFS()
	if err != nil {
		log.Fatalf("Failed to create composed filesystem: %v", err)
	}

	// Mount /scratch - in-memory temp space
	scratchFS, err := memfs.NewFS()
	if err != nil {
		log.Fatalf("Failed to create scratch filesystem: %v", err)
	}
	if err := composed.Mount("/scratch", scratchFS, "In-memory scratch space"); err != nil {
		log.Fatalf("Failed to mount /scratch: %v", err)
	}

	// Mount /project - real filesystem (if provided)
	if *projectDir != "" {
		absPath, err := filepath.Abs(*projectDir)
		if err != nil {
			log.Fatalf("Invalid project path: %v", err)
		}
		projectFS, err := osfs.NewFS()
		if err != nil {
			log.Fatalf("Failed to create project filesystem: %v", err)
		}
		// Create a base-path restricted view
		basedFS := NewBasePathFS(projectFS, absPath)
		if err := composed.Mount("/project", basedFS, fmt.Sprintf("Project directory: %s", absPath)); err != nil {
			log.Fatalf("Failed to mount /project: %v", err)
		}
	}

	// Mount /libs - read-only view (if provided)
	if *libsDir != "" {
		absPath, err := filepath.Abs(*libsDir)
		if err != nil {
			log.Fatalf("Invalid libs path: %v", err)
		}
		libsFS, err := osfs.NewFS()
		if err != nil {
			log.Fatalf("Failed to create libs filesystem: %v", err)
		}
		basedFS := NewBasePathFS(libsFS, absPath)
		readOnlyFS := NewReadOnlyFS(basedFS)
		if err := composed.Mount("/libs", readOnlyFS, fmt.Sprintf("Read-only libs: %s", absPath)); err != nil {
			log.Fatalf("Failed to mount /libs: %v", err)
		}
	}

	// Mount /shared - shared directory (if provided)
	if *sharedDir != "" {
		absPath, err := filepath.Abs(*sharedDir)
		if err != nil {
			log.Fatalf("Invalid shared path: %v", err)
		}
		sharedFS, err := osfs.NewFS()
		if err != nil {
			log.Fatalf("Failed to create shared filesystem: %v", err)
		}
		basedFS := NewBasePathFS(sharedFS, absPath)
		if err := composed.Mount("/shared", basedFS, fmt.Sprintf("Shared directory: %s", absPath)); err != nil {
			log.Fatalf("Failed to mount /shared: %v", err)
		}
	}

	// Create NFS handler
	nfs, err := absnfs.New(composed, absnfs.ExportOptions{})
	if err != nil {
		log.Fatalf("Failed to create NFS handler: %v", err)
	}

	// Create and start NFS server
	server, err := absnfs.NewServer(absnfs.ServerOptions{
		Port:             *port,
		MountPort:        *port,
		Debug:            *debug,
		UseRecordMarking: true,
	})
	if err != nil {
		log.Fatalf("Failed to create NFS server: %v", err)
	}
	server.SetHandler(nfs)

	if err := server.Listen(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	fmt.Println()
	fmt.Println("=======================================================")
	fmt.Println("  Composed Workspace NFS Server")
	fmt.Println("=======================================================")
	fmt.Println()
	fmt.Printf("  Server running on port %d\n", *port)
	fmt.Println()
	fmt.Println("  Mounted filesystems:")
	fmt.Println("    /scratch  - In-memory temp space (volatile)")
	if *projectDir != "" {
		fmt.Printf("    /project  - %s\n", *projectDir)
	}
	if *libsDir != "" {
		fmt.Printf("    /libs     - %s (read-only)\n", *libsDir)
	}
	if *sharedDir != "" {
		fmt.Printf("    /shared   - %s\n", *sharedDir)
	}
	fmt.Println()
	fmt.Println("  Mount commands:")
	fmt.Println()
	fmt.Println("  macOS:")
	fmt.Printf("    sudo mkdir -p /Volumes/workspace\n")
	fmt.Printf("    sudo mount_nfs -o resvport,nolocks,vers=3,tcp,port=%d,mountport=%d localhost:/ /Volumes/workspace\n", *port, *port)
	fmt.Println()
	fmt.Println("  Linux:")
	fmt.Printf("    sudo mkdir -p /mnt/workspace\n")
	fmt.Printf("    sudo mount -t nfs -o vers=3,tcp,port=%d,mountport=%d,nolock localhost:/ /mnt/workspace\n", *port, *port)
	fmt.Println()
	fmt.Println("  Windows (PowerShell as Administrator):")
	fmt.Printf("    mount -o anon,nolock,vers=3,port=%d,mountport=%d \\\\localhost\\ W:\n", *port, *port)
	fmt.Println()
	fmt.Println("  Press Ctrl+C to stop")
	fmt.Println("=======================================================")

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\nShutting down...")
	server.Stop()
}

// BasePathFS restricts a filesystem to a base path
type BasePathFS struct {
	fs   absfs.SymlinkFileSystem
	base string
	cwd  string
}

func NewBasePathFS(fs absfs.SymlinkFileSystem, base string) *BasePathFS {
	return &BasePathFS{fs: fs, base: base, cwd: "/"}
}

func (b *BasePathFS) resolvePath(name string) string {
	// Clean and join with base
	clean := filepath.Clean(name)
	if clean == "." || clean == "/" {
		return b.base
	}
	// Remove leading slash
	if len(clean) > 0 && clean[0] == '/' {
		clean = clean[1:]
	}
	return filepath.Join(b.base, clean)
}

func (b *BasePathFS) Open(name string) (absfs.File, error) {
	return b.fs.Open(b.resolvePath(name))
}

func (b *BasePathFS) OpenFile(name string, flag int, perm os.FileMode) (absfs.File, error) {
	return b.fs.OpenFile(b.resolvePath(name), flag, perm)
}

func (b *BasePathFS) Create(name string) (absfs.File, error) {
	return b.fs.Create(b.resolvePath(name))
}

func (b *BasePathFS) Mkdir(name string, perm os.FileMode) error {
	return b.fs.Mkdir(b.resolvePath(name), perm)
}

func (b *BasePathFS) MkdirAll(path string, perm os.FileMode) error {
	return b.fs.MkdirAll(b.resolvePath(path), perm)
}

func (b *BasePathFS) Remove(name string) error {
	return b.fs.Remove(b.resolvePath(name))
}

func (b *BasePathFS) RemoveAll(path string) error {
	return b.fs.RemoveAll(b.resolvePath(path))
}

func (b *BasePathFS) Rename(oldpath, newpath string) error {
	return b.fs.Rename(b.resolvePath(oldpath), b.resolvePath(newpath))
}

func (b *BasePathFS) Stat(name string) (os.FileInfo, error) {
	return b.fs.Stat(b.resolvePath(name))
}

func (b *BasePathFS) Lstat(name string) (os.FileInfo, error) {
	return b.fs.Lstat(b.resolvePath(name))
}

func (b *BasePathFS) Chmod(name string, mode os.FileMode) error {
	return b.fs.Chmod(b.resolvePath(name), mode)
}

func (b *BasePathFS) Chown(name string, uid, gid int) error {
	return b.fs.Chown(b.resolvePath(name), uid, gid)
}

func (b *BasePathFS) Chtimes(name string, atime, mtime time.Time) error {
	return b.fs.Chtimes(b.resolvePath(name), atime, mtime)
}

func (b *BasePathFS) Readlink(name string) (string, error) {
	return b.fs.Readlink(b.resolvePath(name))
}

func (b *BasePathFS) Symlink(oldname, newname string) error {
	return b.fs.Symlink(oldname, b.resolvePath(newname))
}

func (b *BasePathFS) Lchown(name string, uid, gid int) error {
	return b.fs.Lchown(b.resolvePath(name), uid, gid)
}

func (b *BasePathFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return b.fs.ReadDir(b.resolvePath(name))
}

func (b *BasePathFS) Chdir(dir string) error {
	info, err := b.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return &os.PathError{Op: "chdir", Path: dir, Err: syscall.ENOTDIR}
	}
	b.cwd = dir
	return nil
}

func (b *BasePathFS) Getwd() (string, error) {
	return b.cwd, nil
}

func (b *BasePathFS) TempDir() string {
	return "/tmp"
}

func (b *BasePathFS) Truncate(name string, size int64) error {
	return b.fs.Truncate(b.resolvePath(name), size)
}

func (b *BasePathFS) ReadFile(name string) ([]byte, error) {
	return b.fs.ReadFile(b.resolvePath(name))
}

func (b *BasePathFS) Sub(dir string) (fs.FS, error) {
	return b.fs.Sub(b.resolvePath(dir))
}
