// operations.go: Filesystem operation layer bridging NFS to absfs.
//
// Contains ReadFile, WriteFile, CreateFile, RemoveFile, MakeDirectory,
// RemoveDirectory, Rename, Symlink, and related helpers. Translates NFS
// request semantics into absfs.SymlinkFileSystem calls, handling path resolution,
// striped locking, and error mapping.
package absnfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/absfs/absfs"
)

// ErrTimeout is returned when an operation times out
var ErrTimeout = errors.New("operation timed out")

// statusError is a condition the server detected itself and whose NFS status
// must not depend on how the host spells the errno: on Windows syscall.ENOTDIR
// is ERROR_PATH_NOT_FOUND, which also matches os.ErrNotExist.
type statusError struct {
	status uint32
	msg    string
	errno  error
}

func (e *statusError) Error() string { return e.msg }
func (e *statusError) Unwrap() error { return e.errno }

var (
	errNotDir   = &statusError{NFSERR_NOTDIR, "not a directory", syscall.ENOTDIR}
	errIsDir    = &statusError{NFSERR_ISDIR, "is a directory", syscall.EISDIR}
	errNotEmpty = &statusError{NFSERR_NOTEMPTY, "directory not empty", syscall.ENOTEMPTY}
)

// mapError converts absfs errors to NFS status codes
func mapError(err error) uint32 {
	// Check custom errors first
	var invalidHandle *InvalidFileHandleError
	var notSupported *NotSupportedError
	var status *statusError

	switch {
	case err == nil:
		return NFS_OK
	case errors.As(err, &status):
		return status.status
	case errors.As(err, &invalidHandle):
		return NFSERR_BADHANDLE
	case errors.As(err, &notSupported):
		return NFSERR_NOTSUPP
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrTimeout):
		return NFSERR_DELAY
	case errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOENT):
		return NFSERR_NOENT
	case errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM):
		return NFSERR_PERM
	// Before ErrExist: on unix, syscall.ENOTEMPTY also matches os.ErrExist.
	case errors.Is(err, syscall.ENOTEMPTY):
		return NFSERR_NOTEMPTY
	case errors.Is(err, os.ErrExist) || errors.Is(err, syscall.EEXIST):
		return NFSERR_EXIST
	case errors.Is(err, os.ErrInvalid) || errors.Is(err, syscall.EINVAL):
		return NFSERR_INVAL
	case errors.Is(err, syscall.ENOTDIR):
		return NFSERR_NOTDIR
	case errors.Is(err, syscall.EISDIR):
		return NFSERR_ISDIR
	case errors.Is(err, syscall.ENOSPC):
		return NFSERR_NOSPC
	case errors.Is(err, syscall.EFBIG):
		return NFSERR_FBIG
	case errors.Is(err, syscall.ENAMETOOLONG):
		return NFSERR_NAMETOOLONG
	default:
		return NFSERR_IO
	}
}

// sanitizePath validates and sanitizes a path to prevent directory traversal attacks.
// It ensures the resulting path is within the base directory and rejects paths containing ".." components.
func sanitizePath(basePath, name string) (string, error) {
	// Reject empty names
	if name == "" {
		return "", fmt.Errorf("empty name")
	}

	// Reject names containing path separators or parent directory references
	if strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return "", fmt.Errorf("invalid name: contains path separator")
	}

	if name == ".." || name == "." {
		return "", fmt.Errorf("invalid name: parent or current directory reference")
	}

	// Construct the path using filepath.Join (uses OS-specific separators)
	path := filepath.Join(basePath, name)

	// Clean the path to resolve any ".." or "." components
	cleanPath := filepath.Clean(path)

	// Ensure the cleaned path is still within the base directory
	// by checking if it starts with the base path
	cleanBase := filepath.Clean(basePath)

	// Convert both paths to forward slashes for consistent comparison
	// This ensures the prefix check works correctly on Windows
	cleanPathSlash := filepath.ToSlash(cleanPath)
	cleanBaseSlash := filepath.ToSlash(cleanBase)

	if !strings.HasPrefix(cleanPathSlash, cleanBaseSlash) {
		return "", fmt.Errorf("invalid path: traversal attempt detected")
	}

	// Additional check: ensure no ".." components remain after cleaning
	if strings.Contains(cleanPathSlash, "..") {
		return "", fmt.Errorf("invalid path: contains parent directory reference")
	}

	// Return the path with forward slashes for consistent NFS path representation
	return cleanPathSlash, nil
}

// Lookup implements the LOOKUP operation
func (s *AbsfsNFS) Lookup(path string) (*NFSNode, error) {
	return s.LookupWithContext(context.Background(), path)
}

// LookupWithContext implements the LOOKUP operation with timeout support
func (s *AbsfsNFS) LookupWithContext(ctx context.Context, path string) (*NFSNode, error) {
	if path == "" {
		return nil, fmt.Errorf("empty path")
	}

	tuning := s.tuning.Load()

	// Create context with timeout
	timeout := tuning.Timeouts.LookupTimeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Log operation if enabled
	if slog := s.getStructuredLogger(); slog != nil && tuning.Log != nil && tuning.Log.LogOperations {
		startTime := time.Now()
		defer func() {
			duration := time.Since(startTime)
			slog.Debug("LOOKUP operation",
				LogField{Key: "path", Value: path},
				LogField{Key: "duration_ms", Value: duration.Milliseconds()})
		}()
	}

	// Check for timeout before proceeding
	select {
	case <-ctx.Done():
		if s.metrics != nil {
			s.metrics.RecordTimeout("LOOKUP")
		}
		return nil, ErrTimeout
	default:
	}

	// Check cache first (including negative cache). The generation is taken
	// first so a result read before a concurrent change is not cached.
	gen := s.attrCache.generation()
	if attrs, found := s.attrCache.Get(path, s); found {
		if attrs == nil {
			// Negative cache hit: path confirmed non-existent
			return nil, fmt.Errorf("lookup: failed to stat %s: %w", path, os.ErrNotExist)
		}
		node := &NFSNode{
			SymlinkFileSystem: s.fs,
			path:              path,
			server:            s,
			attrs:             attrs,
		}
		if attrs.Mode&os.ModeDir != 0 {
			node.children = make(map[string]*NFSNode)
		}
		return node, nil
	}

	attrs, err := s.statAttrs(path)
	if err != nil {
		// Store negative cache entry if enabled and error is "not found"
		if errors.Is(err, os.ErrNotExist) {
			s.attrCache.putNegativeAt(path, gen)
			s.RecordNegativeCacheMiss()
		}
		return nil, fmt.Errorf("lookup: %w", err)
	}

	node := &NFSNode{
		SymlinkFileSystem: s.fs,
		path:              path,
		server:            s,
		attrs:             attrs,
	}

	if attrs.Mode.IsDir() {
		node.children = make(map[string]*NFSNode)
	}

	s.attrCache.putAt(path, attrs, gen)
	return node, nil
}

// GetAttr implements the GETATTR operation
func (s *AbsfsNFS) GetAttr(node *NFSNode) (*NFSAttrs, error) {
	if node == nil {
		return nil, fmt.Errorf("nil node")
	}

	// The cache serves only entries within their TTL
	gen := s.attrCache.generation()
	if attrs, _ := s.attrCache.Get(node.path, s); attrs != nil {
		return attrs, nil
	}

	// Get fresh attributes (Lstat, so a symlink is described as itself)
	attrs, err := s.statAttrs(node.path)
	if err != nil {
		return nil, fmt.Errorf("getattr: %w", err)
	}

	s.attrCache.putAt(node.path, attrs, gen)
	return attrs, nil
}

// SetAttr implements the SETATTR operation
func (s *AbsfsNFS) SetAttr(node *NFSNode, attrs *NFSAttrs) error {
	if node == nil {
		return fmt.Errorf("nil node")
	}
	if attrs == nil {
		return fmt.Errorf("nil attrs")
	}

	// The request is compared with what the filesystem holds now, never
	// with cached attributes: a field changed behind the server's back
	// since then must still be set.
	info, err := s.fs.Lstat(node.path)
	if err != nil {
		return fmt.Errorf("setattr: %w", err)
	}
	current := s.attrsFromInfo(node.path, info)
	// Forget the cached attributes once done, also when a later change
	// fails after an earlier one was applied.
	defer s.attrCache.Invalidate(node.path)

	currentMode := current.Mode
	currentUid := current.Uid
	currentGid := current.Gid
	currentMtime := current.Mtime()
	currentAtime := current.Atime()

	if attrs.Mode&os.ModePerm != currentMode&os.ModePerm {
		if err := s.fs.Chmod(node.path, attrs.Mode&os.ModePerm); err != nil {
			return fmt.Errorf("setattr: chmod failed: %w", err)
		}
	}

	if attrs.Uid != currentUid || attrs.Gid != currentGid {
		if err := s.fs.Chown(node.path, int(attrs.Uid), int(attrs.Gid)); err != nil {
			return fmt.Errorf("setattr: chown failed: %w", err)
		}
	}

	if (!attrs.Atime().IsZero() || !attrs.Mtime().IsZero()) &&
		(attrs.Mtime() != currentMtime || attrs.Atime() != currentAtime) {
		if err := s.fs.Chtimes(node.path, attrs.Atime(), attrs.Mtime()); err != nil {
			return fmt.Errorf("setattr: chtimes failed: %w", err)
		}
	}

	// Re-read what the filesystem now records rather than trusting the
	// request, so the node keeps its file id and real owner.
	fresh, err := s.statAttrs(node.path)
	if err != nil {
		return fmt.Errorf("setattr: %w", err)
	}
	node.mu.Lock()
	node.attrs = fresh
	node.mu.Unlock()
	return nil
}

// Read implements the READ operation
func (s *AbsfsNFS) Read(node *NFSNode, offset int64, count int64) ([]byte, error) {
	return s.ReadWithContext(context.Background(), node, offset, count)
}

// ReadWithContext implements the READ operation with timeout support
func (s *AbsfsNFS) ReadWithContext(ctx context.Context, node *NFSNode, offset int64, count int64) ([]byte, error) {
	if node == nil {
		return nil, fmt.Errorf("nil node")
	}
	if offset < 0 {
		return nil, fmt.Errorf("negative offset")
	}
	if count < 0 {
		return nil, fmt.Errorf("negative count")
	}

	tuning := s.tuning.Load()

	// Create context with timeout
	timeout := tuning.Timeouts.ReadTimeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Log operation if enabled
	if slog := s.getStructuredLogger(); slog != nil && tuning.Log != nil && tuning.Log.LogOperations {
		startTime := time.Now()
		defer func() {
			duration := time.Since(startTime)
			slog.Debug("READ operation",
				LogField{Key: "path", Value: node.path},
				LogField{Key: "offset", Value: offset},
				LogField{Key: "count", Value: count},
				LogField{Key: "duration_ms", Value: duration.Milliseconds()})
		}()
	}

	// Check for timeout before proceeding
	select {
	case <-ctx.Done():
		if s.metrics != nil {
			s.metrics.RecordTimeout("READ")
		}
		return nil, ErrTimeout
	default:
	}

	// Limit the read size to TransferSize if it exceeds the configured limit
	if count > int64(tuning.TransferSize) {
		count = int64(tuning.TransferSize)
	}

	// Standard read path
	f, err := s.fs.OpenFile(node.path, os.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("read: failed to open %s: %w", node.path, err)
	}
	defer f.Close()

	// Get file size
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("read: failed to stat %s: %w", node.path, err)
	}

	// Adjust count if it would read beyond EOF
	remaining := info.Size() - offset
	if remaining <= 0 {
		return []byte{}, nil
	}
	if count > remaining {
		count = remaining
	}

	// Read the adjusted amount
	buf := make([]byte, count)
	n, err := f.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("read: failed to read from %s at offset %d: %w", node.path, offset, err)
	}

	return buf[:n], nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Write implements the WRITE operation
func (s *AbsfsNFS) Write(node *NFSNode, offset int64, data []byte) (int64, error) {
	return s.WriteWithContext(context.Background(), node, offset, data)
}

// WriteWithContext implements the WRITE operation with timeout support
func (s *AbsfsNFS) WriteWithContext(ctx context.Context, node *NFSNode, offset int64, data []byte) (int64, error) {
	return s.writeWithContext(ctx, node, offset, data, nil)
}

// writeWithContext writes data at offset. When clientUID is not nil and
// that user owns the file, it may write even when the file's mode denies
// writing (see openForOwnerWrite).
func (s *AbsfsNFS) writeWithContext(ctx context.Context, node *NFSNode, offset int64, data []byte, clientUID *uint32) (int64, error) {
	if node == nil {
		return 0, fmt.Errorf("nil node")
	}
	if offset < 0 {
		return 0, fmt.Errorf("negative offset")
	}
	if data == nil {
		return 0, fmt.Errorf("nil data")
	}

	tuning := s.tuning.Load()
	policy := s.policy.Load()

	if policy.ReadOnly {
		if slog := s.getStructuredLogger(); slog != nil && tuning.Log != nil && tuning.Log.LogOperations {
			slog.Warn("WRITE operation denied: read-only mode",
				LogField{Key: "path", Value: node.path})
		}
		return 0, os.ErrPermission
	}

	// Create context with timeout
	timeout := tuning.Timeouts.WriteTimeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Log operation if enabled
	if slog := s.getStructuredLogger(); slog != nil && tuning.Log != nil && tuning.Log.LogOperations {
		startTime := time.Now()
		defer func() {
			duration := time.Since(startTime)
			slog.Debug("WRITE operation",
				LogField{Key: "path", Value: node.path},
				LogField{Key: "offset", Value: offset},
				LogField{Key: "size", Value: len(data)},
				LogField{Key: "duration_ms", Value: duration.Milliseconds()})
		}()
	}

	// Check for timeout before proceeding
	select {
	case <-ctx.Done():
		if s.metrics != nil {
			s.metrics.RecordTimeout("WRITE")
		}
		return 0, ErrTimeout
	default:
	}

	// Limit the write size to TransferSize if it exceeds the configured limit
	dataLength := len(data)
	if dataLength > tuning.TransferSize {
		data = data[:tuning.TransferSize]
	}

	// Forget the cached attributes once the file is written and closed (or
	// the attempt failed, perhaps after an owner override changed the mode).
	defer s.attrCache.Invalidate(node.path)

	// Standard write path
	f, err := s.fs.OpenFile(node.path, os.O_WRONLY, 0)
	if err != nil && clientUID != nil && errors.Is(err, os.ErrPermission) {
		f, err = s.openForOwnerWrite(node.path, *clientUID, err)
	}
	if err != nil {
		return 0, fmt.Errorf("write: failed to open %s: %w", node.path, err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	n, err := f.WriteAt(data, offset)
	if err == nil {
		// Update modification time explicitly
		now := time.Now()
		if chtimesErr := s.fs.Chtimes(node.path, now, now); chtimesErr != nil {
			// Log but don't fail the write for timestamp update failure
			if slog := s.getStructuredLogger(); slog != nil {
				slog.Warn("WRITE: Failed to update times",
					LogField{Key: "path", Value: node.path},
					LogField{Key: "error", Value: chtimesErr})
			}
		}

		// Update node attributes to reflect new size and time
		if fresh, statErr := s.statAttrs(node.path); statErr == nil {
			node.mu.Lock()
			node.attrs = fresh
			node.mu.Unlock()
		}
	}
	return int64(n), err
}

// openForOwnerWrite opens a regular file for writing on behalf of its owner
// when its mode denies the owner write permission, by adding owner write for
// the duration of the open. NFS is stateless, so a client that creates a file
// read-only and then writes to it (as git does with 0444 object files) sends
// WRITEs the filesystem would refuse; NFS servers conventionally let the owner
// through ("owner override"). The owner is checked here, from the same Lstat
// that reads the mode, not from cached attributes: the file may have changed
// owner since. denied is returned when the override does not apply.
//
// Overrides are serialized: clients send parallel WRITEs for one file, and
// each must see the file's real mode, not another override's temporary one.
func (s *AbsfsNFS) openForOwnerWrite(p string, uid uint32, denied error) (absfs.File, error) {
	s.ownerWriteMu.Lock()
	defer s.ownerWriteMu.Unlock()

	info, err := s.fs.Lstat(p)
	if err != nil || !info.Mode().IsRegular() {
		return nil, denied
	}
	if owner, _ := s.ownerOf(info); owner != uid {
		return nil, denied
	}
	if info.Mode().Perm()&0o200 != 0 {
		// Writable now: another override finished, or the mode changed.
		return s.fs.OpenFile(p, os.O_WRONLY, 0)
	}
	mode := info.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
	if err := s.fs.Chmod(p, mode|0o200); err != nil {
		return nil, denied
	}
	f, openErr := s.fs.OpenFile(p, os.O_WRONLY, 0)
	if err := s.fs.Chmod(p, mode); err != nil && openErr == nil {
		f.Close()
		return nil, fmt.Errorf("restore mode %v: %w", mode, err)
	}
	return f, openErr
}

// Create implements the CREATE operation
func (s *AbsfsNFS) Create(dir *NFSNode, name string, attrs *NFSAttrs) (*NFSNode, error) {
	return s.CreateWithContext(context.Background(), dir, name, attrs)
}

// CreateWithContext implements the CREATE operation with timeout support.
// An existing file is truncated (NFS UNCHECKED semantics).
func (s *AbsfsNFS) CreateWithContext(ctx context.Context, dir *NFSNode, name string, attrs *NFSAttrs) (*NFSNode, error) {
	return s.createWithContext(ctx, dir, name, attrs, false)
}

// createWithContext creates a file. With exclusive set it fails with an
// os.ErrExist error when the name exists (NFS GUARDED and EXCLUSIVE).
func (s *AbsfsNFS) createWithContext(ctx context.Context, dir *NFSNode, name string, attrs *NFSAttrs, exclusive bool) (*NFSNode, error) {
	if dir == nil {
		return nil, fmt.Errorf("nil directory node")
	}
	if name == "" {
		return nil, fmt.Errorf("empty name")
	}
	if attrs == nil {
		return nil, fmt.Errorf("nil attrs")
	}

	tuning := s.tuning.Load()
	policy := s.policy.Load()

	if policy.ReadOnly {
		if slog := s.getStructuredLogger(); slog != nil && tuning.Log != nil && tuning.Log.LogFileAccess {
			slog.Warn("CREATE operation denied: read-only mode",
				LogField{Key: "dir", Value: dir.path},
				LogField{Key: "name", Value: name})
		}
		return nil, os.ErrPermission
	}

	// Create context with timeout
	timeout := tuning.Timeouts.CreateTimeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Log operation if enabled
	if slog := s.getStructuredLogger(); slog != nil && tuning.Log != nil && tuning.Log.LogFileAccess {
		startTime := time.Now()
		defer func() {
			duration := time.Since(startTime)
			slog.Info("CREATE operation",
				LogField{Key: "dir", Value: dir.path},
				LogField{Key: "name", Value: name},
				LogField{Key: "duration_ms", Value: duration.Milliseconds()})
		}()
	}

	// Check for timeout before proceeding
	select {
	case <-ctx.Done():
		if s.metrics != nil {
			s.metrics.RecordTimeout("CREATE")
		}
		return nil, ErrTimeout
	default:
	}

	// Sanitize the path to prevent directory traversal attacks
	path, err := sanitizePath(dir.path, name)
	if err != nil {
		return nil, fmt.Errorf("create: failed to sanitize path: %w", err)
	}

	// A failed attempt may still have changed the directory (a file made
	// and removed again), so the caches forget both either way.
	err = s.createFile(path, attrs, exclusive)
	s.invalidate(dir.path, path)
	if err != nil {
		return nil, err
	}
	return s.Lookup(path)
}

// createFile makes the file at path, or truncates it unless exclusive.
//
// It tries to make a new file first. Only a file this call created gets the
// requested mode and owner, and only such a file is removed on failure:
// UNCHECKED on an existing file truncates it but keeps its mode and owner,
// as open(O_CREAT|O_TRUNC) does.
func (s *AbsfsNFS) createFile(path string, attrs *NFSAttrs, exclusive bool) error {
	created := true
	f, err := s.fs.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, attrs.Mode&os.ModePerm)
	if err != nil && !exclusive && errors.Is(err, os.ErrExist) {
		created = false
		f, err = s.fs.OpenFile(path, os.O_RDWR|os.O_TRUNC, 0)
	}
	if err != nil {
		return fmt.Errorf("create: failed to create %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		if created {
			s.fs.Remove(path)
		}
		return fmt.Errorf("create: failed to close %s: %w", path, err)
	}
	if created {
		return s.applyNewFileAttrs(path, attrs)
	}
	return nil
}

// applyNewFileAttrs gives a file CREATE just made its requested mode and
// owner. If the mode cannot be set the file is removed again.
func (s *AbsfsNFS) applyNewFileAttrs(path string, attrs *NFSAttrs) error {
	if err := s.fs.Chmod(path, attrs.Mode&os.ModePerm); err != nil {
		s.fs.Remove(path)
		return fmt.Errorf("create: failed to chmod %s: %w", path, err)
	}

	// Give the file the requested owner, as MKDIR and SYMLINK do. This is
	// best effort: a server not running as root can only chown to itself.
	if err := s.fs.Chown(path, int(attrs.Uid), int(attrs.Gid)); err != nil {
		if slog := s.getStructuredLogger(); slog != nil {
			slog.Debug("CREATE: chown failed",
				LogField{Key: "path", Value: path},
				LogField{Key: "error", Value: err})
		}
	}
	return nil
}

// Remove implements the REMOVE operation
func (s *AbsfsNFS) Remove(dir *NFSNode, name string) error {
	return s.RemoveWithContext(context.Background(), dir, name)
}

// RemoveWithContext implements the REMOVE operation with timeout support
func (s *AbsfsNFS) RemoveWithContext(ctx context.Context, dir *NFSNode, name string) error {
	if dir == nil {
		return fmt.Errorf("nil directory node")
	}
	if name == "" {
		return fmt.Errorf("empty name")
	}

	tuning := s.tuning.Load()
	policy := s.policy.Load()

	if policy.ReadOnly {
		if slog := s.getStructuredLogger(); slog != nil && tuning.Log != nil && tuning.Log.LogFileAccess {
			slog.Warn("REMOVE operation denied: read-only mode",
				LogField{Key: "dir", Value: dir.path},
				LogField{Key: "name", Value: name})
		}
		return os.ErrPermission
	}

	// Create context with timeout
	timeout := tuning.Timeouts.RemoveTimeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Log operation if enabled
	if slog := s.getStructuredLogger(); slog != nil && tuning.Log != nil && tuning.Log.LogFileAccess {
		startTime := time.Now()
		defer func() {
			duration := time.Since(startTime)
			slog.Info("REMOVE operation",
				LogField{Key: "dir", Value: dir.path},
				LogField{Key: "name", Value: name},
				LogField{Key: "duration_ms", Value: duration.Milliseconds()})
		}()
	}

	// Check for timeout before proceeding
	select {
	case <-ctx.Done():
		if s.metrics != nil {
			s.metrics.RecordTimeout("REMOVE")
		}
		return ErrTimeout
	default:
	}

	// Sanitize the path to prevent directory traversal attacks
	path, err := sanitizePath(dir.path, name)
	if err != nil {
		return fmt.Errorf("remove: failed to sanitize path: %w", err)
	}

	// Handles of path wait while it is removed, then are stale.
	canon, _ := canonicalPath(path)
	op := s.fileMap.log.begin(canon)
	err = s.fs.Remove(path)
	s.fileMap.log.commitRemove(op, err == nil, canon)
	if err != nil {
		return fmt.Errorf("remove: failed to remove %s: %w", path, err)
	}
	// Only an empty directory can be removed, so nothing below path needs
	// forgetting: a cached miss there stays true.
	s.invalidate(dir.path, path)
	s.fileMap.removed(canon)
	return nil
}

// Rename implements the RENAME operation
func (s *AbsfsNFS) Rename(oldDir *NFSNode, oldName string, newDir *NFSNode, newName string) error {
	return s.RenameWithContext(context.Background(), oldDir, oldName, newDir, newName)
}

// RenameWithContext implements the RENAME operation with timeout support
func (s *AbsfsNFS) RenameWithContext(ctx context.Context, oldDir *NFSNode, oldName string, newDir *NFSNode, newName string) error {
	if oldDir == nil || newDir == nil {
		return fmt.Errorf("nil directory node")
	}
	if oldName == "" || newName == "" {
		return fmt.Errorf("empty name")
	}

	tuning := s.tuning.Load()
	policy := s.policy.Load()

	if policy.ReadOnly {
		return os.ErrPermission
	}

	// Create context with timeout
	timeout := tuning.Timeouts.RenameTimeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Check for timeout before proceeding
	select {
	case <-ctx.Done():
		if s.metrics != nil {
			s.metrics.RecordTimeout("RENAME")
		}
		return ErrTimeout
	default:
	}

	// Sanitize both paths to prevent directory traversal attacks
	oldPath, err := sanitizePath(oldDir.path, oldName)
	if err != nil {
		return fmt.Errorf("rename: failed to sanitize old path: %w", err)
	}

	newPath, err := sanitizePath(newDir.path, newName)
	if err != nil {
		return fmt.Errorf("rename: failed to sanitize new path: %w", err)
	}

	// A directory cannot be moved inside itself (POSIX and RFC 1813 require
	// EINVAL). Some filesystems (memfs) would otherwise detach the subtree.
	if isWithin(newPath, oldPath) {
		return fmt.Errorf("rename: cannot move %s inside itself to %s: %w", oldPath, newPath, syscall.EINVAL)
	}

	bothDirs, err := s.renameConflict(oldPath, newPath)
	if err != nil {
		return fmt.Errorf("rename: cannot rename %s over %s: %w", oldPath, newPath, err)
	}

	// Handles of both names, and of everything below them, wait while
	// they change; then the source's follow it and the target's are stale.
	src, _ := canonicalPath(oldPath)
	dst, _ := canonicalPath(newPath)
	op := s.fileMap.log.begin(src, dst)
	err = s.fs.Rename(oldPath, newPath)
	// Go's os.Rename refuses an existing directory target on every host
	// (EEXIST on unix, ERROR_ACCESS_DENIED on Windows), so a directory over
	// an empty directory is replaced whatever the backend said.
	replaced := false
	if err != nil && (bothDirs || errors.Is(err, os.ErrExist)) {
		replaced = true
		err = s.replaceByRename(oldPath, newPath, err)
	}
	// replaceByRename may have removed the target even when it failed.
	epoch := s.fileMap.log.commitRename(op, err == nil, replaced, src, dst)
	// Both names and everything below them (when a directory moved or was
	// replaced) are forgotten, also after a failure: replaceByRename may
	// have removed the target first.
	s.invalidate(oldDir.path, newDir.path)
	s.invalidateTree(oldPath, newPath)
	if err != nil {
		return fmt.Errorf("rename: failed to rename %s to %s: %w", oldPath, newPath, err)
	}
	s.fileMap.renamed(src, dst, epoch)
	return nil
}

// isWithin reports whether p lies strictly below dir. Both are cleaned paths
// as sanitizePath returns them.
func isWithin(p, dir string) bool {
	for _, sep := range []string{"/", string(filepath.Separator)} {
		prefix := strings.TrimSuffix(dir, sep) + sep
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// replaceByRename gives RENAME its NFS (and POSIX) meaning on filesystems whose
// Rename refuses an existing target (memfs does): RFC 1813 says a compatible
// target, both non-directories or an empty directory replaced by a directory,
// is removed and the rename proceeds. Unlike a native rename this is not
// atomic. exists is the error Rename returned.
func (s *AbsfsNFS) replaceByRename(oldPath, newPath string, exists error) error {
	if oldPath == newPath {
		return nil
	}
	src, err := s.fs.Lstat(oldPath)
	if err != nil {
		return err
	}
	dst, err := s.fs.Lstat(newPath)
	if err != nil {
		return exists
	}
	switch {
	case src.IsDir() && !dst.IsDir():
		return errNotDir
	case !src.IsDir() && dst.IsDir():
		return errIsDir
	}
	// Remove fails on a non-empty directory, which is the required error.
	if err := s.fs.Remove(newPath); err != nil {
		return err
	}
	err = s.fs.Rename(oldPath, newPath)
	if err != nil && dst.IsDir() {
		// Put the empty directory back so a failed RENAME changes nothing.
		s.fs.Mkdir(newPath, dst.Mode().Perm())
	}
	return err
}

// renameConflict applies RFC 1813's rules for an existing RENAME target
// before the backend sees the call, because hosts disagree: Windows refuses
// all of these with ERROR_ACCESS_DENIED, and its ENOTDIR is the errno for a
// missing path. A directory may only replace an empty directory and a
// non-directory only a non-directory. bothDirs reports an empty directory
// target being replaced by a directory.
func (s *AbsfsNFS) renameConflict(oldPath, newPath string) (bothDirs bool, err error) {
	if oldPath == newPath {
		return false, nil
	}
	src, err := s.fs.Lstat(oldPath)
	if err != nil {
		return false, nil // the backend reports a missing source
	}
	dst, err := s.fs.Lstat(newPath)
	// SameFile: a case-only rename on a case-insensitive host.
	if err != nil || os.SameFile(src, dst) {
		return false, nil
	}
	switch {
	case src.IsDir() && !dst.IsDir():
		return false, errNotDir
	case !src.IsDir() && dst.IsDir():
		return false, errIsDir
	case !src.IsDir():
		return false, nil
	}
	entries, err := s.fs.ReadDir(newPath)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.Name() != "." && e.Name() != ".." {
			return false, errNotEmpty
		}
	}
	return true, nil
}

// ReadDir implements the READDIR operation
func (s *AbsfsNFS) ReadDir(dir *NFSNode) ([]*NFSNode, error) {
	return s.ReadDirWithContext(context.Background(), dir)
}

// ReadDirWithContext implements the READDIR operation with timeout support
func (s *AbsfsNFS) ReadDirWithContext(ctx context.Context, dir *NFSNode) ([]*NFSNode, error) {
	if dir == nil {
		return nil, fmt.Errorf("nil directory node")
	}

	tuning := s.tuning.Load()

	// Create context with timeout
	timeout := tuning.Timeouts.ReaddirTimeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Check for timeout before proceeding
	select {
	case <-ctx.Done():
		if s.metrics != nil {
			s.metrics.RecordTimeout("READDIR")
		}
		return nil, ErrTimeout
	default:
	}

	// Check directory cache first if enabled
	var entries []os.FileInfo
	var cacheHit bool
	var gen uint64
	if s.dirCache != nil {
		gen = s.dirCache.generation()
		entries, cacheHit = s.dirCache.Get(dir.path)
		if cacheHit {
			// Record cache hit in metrics
			if s.metrics != nil {
				s.RecordDirCacheHit()
			}

			// Log cache hit if debug logging is enabled
			if slog := s.getStructuredLogger(); slog != nil && tuning.Log != nil && tuning.Log.Level == "debug" {
				slog.Debug("directory cache hit",
					LogField{Key: "path", Value: dir.path})
			}

			// Convert cached entries to nodes
			var nodes []*NFSNode
			for _, entry := range entries {
				name := entry.Name()
				// Skip "." and ".." entries
				if name == "." || name == ".." {
					continue
				}
				// Sanitize the path to prevent directory traversal attacks
				entryPath, err := sanitizePath(dir.path, name)
				if err != nil {
					// Skip entries with invalid names
					continue
				}
				node, err := s.Lookup(entryPath)
				if err != nil {
					continue
				}
				nodes = append(nodes, node)
			}
			return nodes, nil
		}

		// Record cache miss in metrics
		if s.metrics != nil {
			s.RecordDirCacheMiss()
		}

		// Log cache miss if debug logging is enabled
		if slog := s.getStructuredLogger(); slog != nil && tuning.Log != nil && tuning.Log.Level == "debug" {
			slog.Debug("directory cache miss",
				LogField{Key: "path", Value: dir.path})
		}
	}

	f, err := s.fs.OpenFile(dir.path, os.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("readdir: failed to open directory %s: %w", dir.path, err)
	}
	defer f.Close()

	// Type assert to get directory interface
	dirFile, ok := f.(absfs.File)
	if !ok {
		return nil, os.ErrInvalid
	}

	// Read directory entries
	entries, err = dirFile.Readdir(-1)
	if err != nil {
		return nil, fmt.Errorf("readdir: failed to read entries from %s: %w", dir.path, err)
	}

	// Store entries in cache if enabled and nothing changed meanwhile
	if s.dirCache != nil {
		s.dirCache.putAt(dir.path, entries, gen)
	}

	var nodes []*NFSNode
	for _, entry := range entries {
		name := entry.Name()
		// Skip "." and ".." entries
		if name == "." || name == ".." {
			continue
		}
		// Sanitize the path to prevent directory traversal attacks
		entryPath, err := sanitizePath(dir.path, name)
		if err != nil {
			// Skip entries with invalid names
			continue
		}
		node, err := s.Lookup(entryPath)
		if err != nil {
			continue
		}
		nodes = append(nodes, node)
	}

	return nodes, nil
}

// ReadDirPlus implements the READDIRPLUS operation
func (s *AbsfsNFS) ReadDirPlus(dir *NFSNode) ([]*NFSNode, error) {
	if dir == nil {
		return nil, fmt.Errorf("nil directory node")
	}

	nodes, err := s.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	// Re-read every entry's attributes: ReadDir's lookups may have been
	// answered from the attribute cache, and READDIRPLUS replaces the
	// client's GETATTRs, so it reports what the filesystem holds now. An
	// entry that vanished in between keeps the attributes LOOKUP gave it.
	for _, node := range nodes {
		gen := s.attrCache.generation()
		attrs, err := s.statAttrs(node.path)
		if err != nil {
			continue
		}
		s.attrCache.putAt(node.path, attrs, gen)

		// Assign attrs with write lock protection
		node.mu.Lock()
		node.attrs = attrs
		node.mu.Unlock()
	}

	return nodes, nil
}

// Export starts serving the NFS export on localhost at port, with the MOUNT
// protocol on the same port and RPC record marking, as kernel NFS clients
// expect. Port 0 picks a free port; ExportPort reports it. No portmapper is
// started, so clients pass the port explicitly (mount_nfs -o
// port=P,mountport=P). Stop it with Unexport or Close. For another bind
// address, a portmapper or more listener options, build a Server with
// NewServer instead. ExportOptions.TLS applies here too.
//
// mountPath is informational: clients mount filesystem paths (localhost:/
// for the whole tree, localhost:/sub for a subdirectory), whatever it is.
func (s *AbsfsNFS) Export(mountPath string, port int) error {
	if mountPath == "" {
		return fmt.Errorf("empty mount path")
	}
	if port < 0 {
		return fmt.Errorf("invalid port")
	}

	s.mountPath = mountPath

	server, err := NewServer(ServerOptions{
		Name:             "absfs",
		UID:              0,
		GID:              0,
		ReadOnly:         s.policy.Load().ReadOnly,
		Port:             port, // MOUNT is served on the same listener
		Hostname:         "localhost",
		UseRecordMarking: true,
	})
	if err != nil {
		return err
	}

	server.SetHandler(s)
	if err := server.Listen(); err != nil {
		return err
	}
	s.exportServer = server
	return nil
}

// ExportPort returns the TCP port the export started by Export listens on,
// which is how a caller that passed port 0 learns which port to mount. It
// returns 0 when no export is running.
func (s *AbsfsNFS) ExportPort() int {
	if s.exportServer == nil {
		return 0
	}
	return s.exportServer.GetPort()
}

// Symlink implements the SYMLINK operation
func (s *AbsfsNFS) Symlink(dir *NFSNode, name string, target string, attrs *NFSAttrs) (*NFSNode, error) {
	if dir == nil {
		return nil, fmt.Errorf("nil directory node")
	}
	if name == "" {
		return nil, fmt.Errorf("empty name")
	}
	if target == "" {
		return nil, fmt.Errorf("empty target")
	}
	if attrs == nil {
		return nil, fmt.Errorf("nil attrs")
	}

	if s.policy.Load().ReadOnly {
		return nil, os.ErrPermission
	}

	// Sanitize the path to prevent directory traversal attacks
	path, err := sanitizePath(dir.path, name)
	if err != nil {
		return nil, fmt.Errorf("symlink: failed to sanitize path: %w", err)
	}

	// Create the symlink (s.fs is absfs.SymlinkFileSystem). The caches
	// forget the name also on failure: it may exist after all (EEXIST).
	err = s.fs.Symlink(target, path)
	s.invalidate(dir.path, path)
	if err != nil {
		return nil, fmt.Errorf("symlink: failed to create symlink at %s pointing to %s: %w", path, target, err)
	}
	return s.Lookup(path)
}

// Readlink implements the READLINK operation
func (s *AbsfsNFS) Readlink(node *NFSNode) (string, error) {
	if node == nil {
		return "", fmt.Errorf("nil node")
	}

	// s.fs is absfs.SymlinkFileSystem, so Readlink is always available
	target, err := s.fs.Readlink(node.path)
	if err != nil {
		return "", fmt.Errorf("readlink: failed to read symlink %s: %w", node.path, err)
	}

	// The target is returned as stored, ".." components and all: the
	// client resolves it in its own namespace, so its text reveals nothing
	// the client could not reach, and refusing it broke ordinary links
	// such as docs/up -> ../README.md (git, rg and ls -l failed with EIO).
	return target, nil
}

// Unexport stops serving the NFS export
func (s *AbsfsNFS) Unexport() error {
	// Stop the server if Export() created one
	if s.exportServer != nil {
		s.exportServer.Stop()
		s.exportServer = nil
	}
	// Cleanup all open file handles
	s.fileMap.ReleaseAll()
	// Clear caches
	s.attrCache.Clear()
	if s.dirCache != nil {
		s.dirCache.Clear()
	}
	return nil
}
