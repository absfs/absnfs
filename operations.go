package absnfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/absfs/absfs"
)

// ErrTimeout is returned when an operation times out
var ErrTimeout = errors.New("operation timed out")

// mapError converts absfs errors to NFS status codes
func mapError(err error) uint32 {
	// Check custom errors first
	var invalidHandle *InvalidFileHandleError
	var notSupported *NotSupportedError

	switch {
	case err == nil:
		return NFS_OK
	case errors.As(err, &invalidHandle):
		return NFSERR_BADHANDLE
	case errors.As(err, &notSupported):
		return NFSERR_NOTSUPP
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrTimeout):
		return NFSERR_DELAY
	case os.IsNotExist(err):
		return NFSERR_NOENT
	case os.IsPermission(err):
		return NFSERR_PERM
	case os.IsExist(err):
		return NFSERR_EXIST
	case err == os.ErrInvalid:
		return NFSERR_INVAL
	default:
		return NFSERR_IO
	}
}

// toFileAttribute converts absfs FileInfo to NFS FileAttribute
func toFileAttribute(info os.FileInfo) FileAttribute {
	mode := info.Mode()
	mtime := info.ModTime()
	return FileAttribute{
		Type:   uint32(mode >> 16),
		Mode:   uint32(mode & 0xFFFF),
		Nlink:  1,
		Size:   uint64(info.Size()),
		Mtime:  uint32(mtime.Unix()),
		Atime:  uint32(mtime.Unix()),
		Ctime:  uint32(mtime.Unix()),
		Fileid: uint64(time.Now().UnixNano()), // Generate a unique file ID
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

	// Create context with timeout
	timeout := s.options.Timeouts.LookupTimeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Log operation if enabled
	if s.structuredLogger != nil && s.options.Log != nil && s.options.Log.LogOperations {
		startTime := time.Now()
		defer func() {
			duration := time.Since(startTime)
			s.structuredLogger.Debug("LOOKUP operation",
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

	// Check cache first (including negative cache)
	if attrs := s.attrCache.Get(path, s); attrs != nil {
		node := &NFSNode{
			SymlinkFileSystem: s.fs,
			path:              path,
			attrs:             attrs,
		}
		if attrs.Mode&os.ModeDir != 0 {
			node.children = make(map[string]*NFSNode)
		}
		return node, nil
	}

	// Use Lstat to get symlink info without following
	// The filesystem now implements absfs.SymlinkFileSystem which has Lstat
	info, err := s.fs.Lstat(path)

	if err != nil {
		// Store negative cache entry if enabled and error is "not found"
		if os.IsNotExist(err) {
			s.attrCache.PutNegative(path)
			s.RecordNegativeCacheMiss()
		}
		return nil, fmt.Errorf("lookup: failed to stat %s: %w", path, err)
	}

	modTime := info.ModTime()
	attrs := &NFSAttrs{
		Mode: info.Mode(),
		Size: info.Size(),
		Uid:  0,
		Gid:  0,
	}
	attrs.SetMtime(modTime)
	attrs.SetAtime(modTime)
	attrs.Refresh() // Initialize cache validity

	node := &NFSNode{
		SymlinkFileSystem: s.fs,
		path:              path,
		attrs:             attrs,
	}

	if info.IsDir() {
		node.children = make(map[string]*NFSNode)
	}

	// Cache the attributes
	s.attrCache.Put(path, attrs)
	return node, nil
}

// GetAttr implements the GETATTR operation
func (s *AbsfsNFS) GetAttr(node *NFSNode) (*NFSAttrs, error) {
	if node == nil {
		return nil, fmt.Errorf("nil node")
	}

	// Check cache first
	if attrs := s.attrCache.Get(node.path); attrs != nil && attrs.IsValid() {
		return attrs, nil
	}

	// Get fresh attributes using Lstat (to handle symlinks properly)
	// The filesystem implements absfs.SymlinkFileSystem which has Lstat
	info, err := s.fs.Lstat(node.path)

	if err != nil {
		return nil, fmt.Errorf("getattr: failed to stat %s: %w", node.path, err)
	}

	// Read Uid/Gid from node.attrs with lock protection
	node.mu.RLock()
	uid := node.attrs.Uid
	gid := node.attrs.Gid
	node.mu.RUnlock()

	modTime := info.ModTime()
	attrs := &NFSAttrs{
		Mode: info.Mode(),
		Size: info.Size(),
		Uid:  uid,
		Gid:  gid,
	}
	attrs.SetMtime(modTime)
	attrs.SetAtime(modTime)
	attrs.Refresh() // Initialize cache validity

	// Cache the attributes
	s.attrCache.Put(node.path, attrs)
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

	// Check if file exists first
	_, err := s.fs.Stat(node.path)
	if err != nil {
		return fmt.Errorf("setattr: %w", err)
	}

	// Read current attrs with lock protection to compare
	node.mu.RLock()
	currentMode := node.attrs.Mode
	currentUid := node.attrs.Uid
	currentGid := node.attrs.Gid
	currentMtime := node.attrs.Mtime()
	currentAtime := node.attrs.Atime()
	node.mu.RUnlock()

	if attrs.Mode != currentMode {
		if err := s.fs.Chmod(node.path, attrs.Mode); err != nil {
			return fmt.Errorf("setattr: chmod failed: %w", err)
		}
	}

	if attrs.Uid != currentUid || attrs.Gid != currentGid {
		if err := s.fs.Chown(node.path, int(attrs.Uid), int(attrs.Gid)); err != nil {
			return fmt.Errorf("setattr: chown failed: %w", err)
		}
	}

	if attrs.Mtime() != currentMtime || attrs.Atime() != currentAtime {
		if err := s.fs.Chtimes(node.path, attrs.Atime(), attrs.Mtime()); err != nil {
			return fmt.Errorf("setattr: chtimes failed: %w", err)
		}
	}

	// Update attrs with lock protection
	node.mu.Lock()
	node.attrs = attrs
	node.attrs.Refresh() // Initialize cache validity
	node.mu.Unlock()

	// Invalidate cache after attribute changes
	s.attrCache.Invalidate(node.path)
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

	// Create context with timeout
	timeout := s.options.Timeouts.ReadTimeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Log operation if enabled
	if s.structuredLogger != nil && s.options.Log != nil && s.options.Log.LogOperations {
		startTime := time.Now()
		defer func() {
			duration := time.Since(startTime)
			s.structuredLogger.Debug("READ operation",
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
	if count > int64(s.options.TransferSize) {
		count = int64(s.options.TransferSize)
	}

	// Try read-ahead buffer first if enabled
	if data, ok := s.readBuf.Read(node.path, offset, int(count), s); ok {
		return data, nil
	}

	// Get the file handle for this node and use batch processing if enabled
	var fileHandle uint64
	var useBatch bool
	s.fileMap.RLock()
	for handle, file := range s.fileMap.handles {
		if nodeFile, ok := file.(*NFSNode); ok && nodeFile.path == node.path {
			fileHandle = handle
			break
		}
	}
	// Check if we should use batch processing while still holding the lock
	// This prevents a race where the handle could be removed after we release the lock
	if fileHandle != 0 && s.options.BatchOperations && s.batchProc != nil {
		// Verify handle still exists in map before using it
		if _, exists := s.fileMap.handles[fileHandle]; exists {
			useBatch = true
		}
	}
	s.fileMap.RUnlock()

	// Use batch processing if we determined it's safe to do so
	if useBatch {
		data, status, err := s.batchProc.BatchRead(context.Background(), fileHandle, offset, int(count))
		if err == nil && status == NFS_OK {
			return data, nil
		}
		// If batch processing failed but not because of a file error, fall back to normal read
		if status != NFSERR_NOENT && status != NFSERR_IO {
			// Fall through to standard read
		} else {
			return nil, err
		}
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

	// Only attempt read-ahead if enabled and we got all requested data and there's more to read
	if s.options.EnableReadAhead && err != io.EOF && n == int(count) && offset+count < info.Size() {
		readAheadSize := int64(s.options.ReadAheadSize)
		readAheadRemaining := info.Size() - (offset + count)
		if readAheadSize > readAheadRemaining {
			readAheadSize = readAheadRemaining
		}
		if readAheadSize > 0 {
			readAheadBuf := make([]byte, readAheadSize)
			rn, rerr := f.ReadAt(readAheadBuf, offset+count)
			if rerr == nil || rerr == io.EOF {
				s.readBuf.Fill(node.path, readAheadBuf[:rn], offset+count)
			}
		}
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
	if node == nil {
		return 0, fmt.Errorf("nil node")
	}
	if offset < 0 {
		return 0, fmt.Errorf("negative offset")
	}
	if data == nil {
		return 0, fmt.Errorf("nil data")
	}

	if s.options.ReadOnly {
		if s.structuredLogger != nil && s.options.Log != nil && s.options.Log.LogOperations {
			s.structuredLogger.Warn("WRITE operation denied: read-only mode",
				LogField{Key: "path", Value: node.path})
		}
		return 0, os.ErrPermission
	}

	// Create context with timeout
	timeout := s.options.Timeouts.WriteTimeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Log operation if enabled
	if s.structuredLogger != nil && s.options.Log != nil && s.options.Log.LogOperations {
		startTime := time.Now()
		defer func() {
			duration := time.Since(startTime)
			s.structuredLogger.Debug("WRITE operation",
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
	if dataLength > s.options.TransferSize {
		data = data[:s.options.TransferSize]
	}

	// Get the file handle for this node and use batch processing if enabled
	var fileHandle uint64
	var useBatch bool
	s.fileMap.RLock()
	for handle, file := range s.fileMap.handles {
		if nodeFile, ok := file.(*NFSNode); ok && nodeFile.path == node.path {
			fileHandle = handle
			break
		}
	}
	// Check if we should use batch processing while still holding the lock
	// This prevents a race where the handle could be removed after we release the lock
	if fileHandle != 0 && s.options.BatchOperations && s.batchProc != nil {
		// Verify handle still exists in map before using it
		if _, exists := s.fileMap.handles[fileHandle]; exists {
			useBatch = true
		}
	}
	s.fileMap.RUnlock()

	// Use batch processing if we determined it's safe to do so
	if useBatch {
		status, err := s.batchProc.BatchWrite(context.Background(), fileHandle, offset, data)
		if err == nil && status == NFS_OK {
			// Invalidate cache after successful write
			s.attrCache.Invalidate(node.path)
			// Clear only the specific file's buffer, not all buffers
			s.readBuf.ClearPath(node.path)

			// Update node attributes to reflect changes
			info, statErr := s.fs.Stat(node.path)
			if statErr == nil {
				node.mu.Lock()
				node.attrs.Size = info.Size()
				node.attrs.SetMtime(info.ModTime())
				node.attrs.Refresh() // Initialize cache validity
				node.mu.Unlock()
			}

			return int64(len(data)), nil
		}
		// If batch processing failed but not because of a file error, fall back to normal write
		if status != NFSERR_NOENT && status != NFSERR_IO && status != NFSERR_ROFS {
			// Fall through to standard write
		} else {
			return 0, err
		}
	}

	// Standard write path
	f, err := s.fs.OpenFile(node.path, os.O_WRONLY, 0)
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
		// Invalidate cache after successful write
		s.attrCache.Invalidate(node.path)
		// Clear only the specific file's buffer, not all buffers
		s.readBuf.ClearPath(node.path)

		// Update modification time explicitly
		now := time.Now()
		if err := s.fs.Chtimes(node.path, now, now); err != nil {
			return int64(n), err
		}

		// Update node attributes to reflect new size and time
		info, statErr := s.fs.Stat(node.path)
		if statErr == nil {
			node.mu.Lock()
			node.attrs.Size = info.Size()
			node.attrs.SetMtime(info.ModTime())
			node.attrs.Refresh() // Initialize cache validity
			node.mu.Unlock()
		}
	}
	return int64(n), err
}

// Create implements the CREATE operation
func (s *AbsfsNFS) Create(dir *NFSNode, name string, attrs *NFSAttrs) (*NFSNode, error) {
	return s.CreateWithContext(context.Background(), dir, name, attrs)
}

// CreateWithContext implements the CREATE operation with timeout support
func (s *AbsfsNFS) CreateWithContext(ctx context.Context, dir *NFSNode, name string, attrs *NFSAttrs) (*NFSNode, error) {
	if dir == nil {
		return nil, fmt.Errorf("nil directory node")
	}
	if name == "" {
		return nil, fmt.Errorf("empty name")
	}
	if attrs == nil {
		return nil, fmt.Errorf("nil attrs")
	}

	if s.options.ReadOnly {
		if s.structuredLogger != nil && s.options.Log != nil && s.options.Log.LogFileAccess {
			s.structuredLogger.Warn("CREATE operation denied: read-only mode",
				LogField{Key: "dir", Value: dir.path},
				LogField{Key: "name", Value: name})
		}
		return nil, os.ErrPermission
	}

	// Create context with timeout
	timeout := s.options.Timeouts.CreateTimeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Log operation if enabled
	if s.structuredLogger != nil && s.options.Log != nil && s.options.Log.LogFileAccess {
		startTime := time.Now()
		defer func() {
			duration := time.Since(startTime)
			s.structuredLogger.Info("CREATE operation",
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

	f, err := s.fs.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create: failed to create %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		s.fs.Remove(path)
		return nil, fmt.Errorf("create: failed to close %s: %w", path, err)
	}

	if err := s.fs.Chmod(path, attrs.Mode); err != nil {
		s.fs.Remove(path)
		return nil, fmt.Errorf("create: failed to chmod %s: %w", path, err)
	}

	// Invalidate parent directory caches and negative cache entries in the directory
	s.attrCache.Invalidate(dir.path)
	s.attrCache.InvalidateNegativeInDir(dir.path)
	s.attrCache.Invalidate(path) // Also invalidate the specific path in case it was negatively cached
	if s.dirCache != nil {
		s.dirCache.Invalidate(dir.path)
	}
	return s.Lookup(path)
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

	if s.options.ReadOnly {
		if s.structuredLogger != nil && s.options.Log != nil && s.options.Log.LogFileAccess {
			s.structuredLogger.Warn("REMOVE operation denied: read-only mode",
				LogField{Key: "dir", Value: dir.path},
				LogField{Key: "name", Value: name})
		}
		return os.ErrPermission
	}

	// Create context with timeout
	timeout := s.options.Timeouts.RemoveTimeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Log operation if enabled
	if s.structuredLogger != nil && s.options.Log != nil && s.options.Log.LogFileAccess {
		startTime := time.Now()
		defer func() {
			duration := time.Since(startTime)
			s.structuredLogger.Info("REMOVE operation",
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

	err = s.fs.Remove(path)
	if err != nil {
		return fmt.Errorf("remove: failed to remove %s: %w", path, err)
	}
	// Invalidate caches
	s.attrCache.Invalidate(path)
	s.attrCache.Invalidate(dir.path)
	if s.dirCache != nil {
		s.dirCache.Invalidate(dir.path)
	}
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

	if s.options.ReadOnly {
		return os.ErrPermission
	}

	// Create context with timeout
	timeout := s.options.Timeouts.RenameTimeout
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

	err = s.fs.Rename(oldPath, newPath)
	if err != nil {
		return fmt.Errorf("rename: failed to rename %s to %s: %w", oldPath, newPath, err)
	}
	// Invalidate caches and negative cache entries
	s.attrCache.Invalidate(oldPath)
	s.attrCache.Invalidate(newPath)
	s.attrCache.Invalidate(oldDir.path)
	s.attrCache.Invalidate(newDir.path)
	// Invalidate negative cache entries in both directories
	s.attrCache.InvalidateNegativeInDir(oldDir.path)
	s.attrCache.InvalidateNegativeInDir(newDir.path)
	if s.dirCache != nil {
		s.dirCache.Invalidate(oldDir.path)
		s.dirCache.Invalidate(newDir.path)
	}
	return nil
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

	// Create context with timeout
	timeout := s.options.Timeouts.ReaddirTimeout
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
	if s.dirCache != nil {
		entries, cacheHit = s.dirCache.Get(dir.path)
		if cacheHit {
			// Record cache hit in metrics
			if s.metrics != nil {
				s.RecordDirCacheHit()
			}

			// Log cache hit if debug logging is enabled
			if s.structuredLogger != nil && s.options.Log != nil && s.options.Log.Level == "debug" {
				s.structuredLogger.Debug("directory cache hit",
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
		if s.structuredLogger != nil && s.options.Log != nil && s.options.Log.Level == "debug" {
			s.structuredLogger.Debug("directory cache miss",
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

	// Store entries in cache if enabled
	if s.dirCache != nil {
		s.dirCache.Put(dir.path, entries)
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

	// Pre-cache attributes for all entries
	for _, node := range nodes {
		if attrs := s.attrCache.Get(node.path); attrs == nil || !attrs.IsValid() {
			info, err := s.fs.Stat(node.path)
			if err != nil {
				continue
			}
			modTime := info.ModTime()
			attrs := &NFSAttrs{
				Mode: info.Mode(),
				Size: info.Size(),
				Uid:  node.attrs.Uid,
				Gid:  node.attrs.Gid,
			}
			attrs.SetMtime(modTime)
			attrs.SetAtime(modTime)
			attrs.Refresh() // Initialize cache validity
			s.attrCache.Put(node.path, attrs)
			node.attrs = attrs
		}
	}

	return nodes, nil
}

// Export starts serving the NFS export
func (s *AbsfsNFS) Export(mountPath string, port int) error {
	if mountPath == "" {
		return fmt.Errorf("empty mount path")
	}
	if port < 0 {
		return fmt.Errorf("invalid port")
	}

	s.mountPath = mountPath

	server, err := NewServer(ServerOptions{
		Name:     "absfs",
		UID:      0,
		GID:      0,
		ReadOnly: s.options.ReadOnly,
		Port:     port,
		Hostname: "localhost",
	})
	if err != nil {
		return err
	}

	server.SetHandler(s)
	return server.Listen()
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

	if s.options.ReadOnly {
		return nil, os.ErrPermission
	}

	// Sanitize the path to prevent directory traversal attacks
	path, err := sanitizePath(dir.path, name)
	if err != nil {
		return nil, fmt.Errorf("symlink: failed to sanitize path: %w", err)
	}

	// Create the symlink (s.fs is absfs.SymlinkFileSystem)
	err = s.fs.Symlink(target, path)
	if err != nil {
		return nil, fmt.Errorf("symlink: failed to create symlink at %s pointing to %s: %w", path, target, err)
	}

	// Invalidate parent directory caches and negative cache entries in the directory
	s.attrCache.Invalidate(dir.path)
	s.attrCache.InvalidateNegativeInDir(dir.path)
	s.attrCache.Invalidate(path) // Also invalidate the specific path in case it was negatively cached
	if s.dirCache != nil {
		s.dirCache.Invalidate(dir.path)
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

	return target, nil
}

// Unexport stops serving the NFS export
func (s *AbsfsNFS) Unexport() error {
	// Cleanup all open file handles
	s.fileMap.ReleaseAll()
	// Clear caches
	s.attrCache.Clear()
	s.readBuf.Clear()
	return nil
}
