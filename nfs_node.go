package absnfs

import (
	"os"
	"path/filepath"
	"time"

	"github.com/absfs/absfs"
)

// Ensure NFSNode implements absfs.File
var _ absfs.File = (*NFSNode)(nil)

// Close implements absfs.File
func (n *NFSNode) Close() error {
	return nil
}

// Read implements absfs.File
func (n *NFSNode) Read(p []byte) (int, error) {
	f, err := n.FileSystem.OpenFile(n.path, os.O_RDONLY, 0)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.Read(p)
}

// ReadAt implements absfs.File
func (n *NFSNode) ReadAt(p []byte, off int64) (int, error) {
	f, err := n.FileSystem.OpenFile(n.path, os.O_RDONLY, 0)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.ReadAt(p, off)
}

// Write implements absfs.File
func (n *NFSNode) Write(p []byte) (int, error) {
	f, err := n.FileSystem.OpenFile(n.path, os.O_WRONLY, 0)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n.mu.Lock()
	n.attrs.Invalidate() // Invalidate cache on write
	n.mu.Unlock()
	return f.Write(p)
}

// WriteAt implements absfs.File
func (n *NFSNode) WriteAt(p []byte, off int64) (int, error) {
	f, err := n.FileSystem.OpenFile(n.path, os.O_WRONLY, 0)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n.mu.Lock()
	n.attrs.Invalidate() // Invalidate cache on write
	n.mu.Unlock()
	return f.WriteAt(p, off)
}

// Seek implements absfs.File
func (n *NFSNode) Seek(offset int64, whence int) (int64, error) {
	f, err := n.FileSystem.OpenFile(n.path, os.O_RDONLY, 0)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.Seek(offset, whence)
}

// Name implements absfs.File
func (n *NFSNode) Name() string {
	if n.path == "/" {
		return "/"
	}
	return filepath.Base(n.path)
}

// Readdir implements absfs.File
func (n *NFSNode) Readdir(count int) ([]os.FileInfo, error) {
	f, err := n.FileSystem.OpenFile(n.path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.Readdir(count)
	if err != nil {
		return nil, err
	}
	// Filter out "." and ".." entries
	filtered := make([]os.FileInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() != "." && entry.Name() != ".." {
			filtered = append(filtered, entry)
		}
	}
	return filtered, nil
}

// Readdirnames implements absfs.File
func (n *NFSNode) Readdirnames(count int) ([]string, error) {
	entries, err := n.Readdir(count)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names, nil
}

// Stat implements absfs.File
func (n *NFSNode) Stat() (os.FileInfo, error) {
	return n.FileSystem.Stat(n.path)
}

// Sync implements absfs.File
func (n *NFSNode) Sync() error {
	// Check if file exists before attempting sync
	_, err := n.FileSystem.Stat(n.path)
	return err
}

// Truncate implements absfs.File
func (n *NFSNode) Truncate(size int64) error {
	n.mu.Lock()
	n.attrs.Invalidate() // Invalidate cache on truncate
	n.mu.Unlock()
	return n.FileSystem.Truncate(n.path, size)
}

// WriteString implements absfs.File
func (n *NFSNode) WriteString(s string) (int, error) {
	return n.Write([]byte(s))
}

// Chdir implements absfs.File
func (n *NFSNode) Chdir() error {
	return n.FileSystem.Chdir(n.path)
}

// Chmod implements absfs.File
func (n *NFSNode) Chmod(mode os.FileMode) error {
	n.mu.Lock()
	n.attrs.Invalidate() // Invalidate cache on chmod
	n.mu.Unlock()
	return n.FileSystem.Chmod(n.path, mode)
}

// Chown implements absfs.File
func (n *NFSNode) Chown(uid, gid int) error {
	n.mu.Lock()
	n.attrs.Invalidate() // Invalidate cache on chown
	n.mu.Unlock()
	return n.FileSystem.Chown(n.path, uid, gid)
}

// Chtimes implements absfs.File
func (n *NFSNode) Chtimes(atime time.Time, mtime time.Time) error {
	n.mu.Lock()
	n.attrs.Invalidate() // Invalidate cache on chtimes
	n.mu.Unlock()
	return n.FileSystem.Chtimes(n.path, atime, mtime)
}
