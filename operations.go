package absnfs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/absfs/absfs"
)

// mapError converts absfs errors to NFS status codes
func mapError(err error) uint32 {
	switch {
	case err == nil:
		return NFS_OK
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

	// Construct the path
	path := filepath.Join(basePath, name)

	// Clean the path to resolve any ".." or "." components
	cleanPath := filepath.Clean(path)

	// Ensure the cleaned path is still within the base directory
	// by checking if it starts with the base path
	cleanBase := filepath.Clean(basePath)
	if !strings.HasPrefix(cleanPath, cleanBase) {
		return "", fmt.Errorf("invalid path: traversal attempt detected")
	}

	// Additional check: ensure no ".." components remain after cleaning
	if strings.Contains(cleanPath, "..") {
		return "", fmt.Errorf("invalid path: contains parent directory reference")
	}

	return cleanPath, nil
}

// Lookup implements the LOOKUP operation
func (s *AbsfsNFS) Lookup(path string) (*NFSNode, error) {
	if path == "" {
		return nil, fmt.Errorf("empty path")
	}

	// Check cache first
	if attrs := s.attrCache.Get(path, s); attrs != nil {
		node := &NFSNode{
			FileSystem: s.fs,
			path:       path,
			attrs:      attrs,
		}
		if attrs.Mode&os.ModeDir != 0 {
			node.children = make(map[string]*NFSNode)
		}
		return node, nil
	}

	info, err := s.fs.Stat(path)
	if err != nil {
		return nil, err
	}

	attrs := &NFSAttrs{
		Mode:  info.Mode(),
		Size:  info.Size(),
		Mtime: info.ModTime(),
		Atime: info.ModTime(),
		Uid:   0,
		Gid:   0,
	}
	attrs.Refresh() // Initialize cache validity

	node := &NFSNode{
		FileSystem: s.fs,
		path:       path,
		attrs:      attrs,
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

	// Get fresh attributes
	info, err := s.fs.Stat(node.path)
	if err != nil {
		return nil, err
	}

	// Read Uid/Gid from node.attrs with lock protection
	node.mu.RLock()
	uid := node.attrs.Uid
	gid := node.attrs.Gid
	node.mu.RUnlock()

	attrs := &NFSAttrs{
		Mode:  info.Mode(),
		Size:  info.Size(),
		Mtime: info.ModTime(),
		Atime: info.ModTime(),
		Uid:   uid,
		Gid:   gid,
	}
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
		return err
	}

	// Read current attrs with lock protection to compare
	node.mu.RLock()
	currentMode := node.attrs.Mode
	currentUid := node.attrs.Uid
	currentGid := node.attrs.Gid
	currentMtime := node.attrs.Mtime
	currentAtime := node.attrs.Atime
	node.mu.RUnlock()

	if attrs.Mode != currentMode {
		if err := s.fs.Chmod(node.path, attrs.Mode); err != nil {
			return err
		}
	}

	if attrs.Uid != currentUid || attrs.Gid != currentGid {
		if err := s.fs.Chown(node.path, int(attrs.Uid), int(attrs.Gid)); err != nil {
			return err
		}
	}

	if attrs.Mtime != currentMtime || attrs.Atime != currentAtime {
		if err := s.fs.Chtimes(node.path, attrs.Atime, attrs.Mtime); err != nil {
			return err
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
	if node == nil {
		return nil, fmt.Errorf("nil node")
	}
	if offset < 0 {
		return nil, fmt.Errorf("negative offset")
	}
	if count < 0 {
		return nil, fmt.Errorf("negative count")
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
		data, err, status := s.batchProc.BatchRead(context.Background(), fileHandle, offset, int(count))
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
		return nil, err
	}
	defer f.Close()

	// Get file size
	info, err := f.Stat()
	if err != nil {
		return nil, err
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
		return nil, err
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
		return 0, os.ErrPermission
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
		err, status := s.batchProc.BatchWrite(context.Background(), fileHandle, offset, data)
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
				node.attrs.Mtime = info.ModTime()
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
		return 0, err
	}
	defer f.Close()

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
			node.attrs.Mtime = info.ModTime()
			node.attrs.Refresh() // Initialize cache validity
			node.mu.Unlock()
		}
	}
	return int64(n), err
}

// Create implements the CREATE operation
func (s *AbsfsNFS) Create(dir *NFSNode, name string, attrs *NFSAttrs) (*NFSNode, error) {
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
		return nil, os.ErrPermission
	}

	// Sanitize the path to prevent directory traversal attacks
	path, err := sanitizePath(dir.path, name)
	if err != nil {
		return nil, err
	}

	f, err := s.fs.Create(path)
	if err != nil {
		return nil, err
	}
	f.Close()

	if err := s.fs.Chmod(path, attrs.Mode); err != nil {
		s.fs.Remove(path)
		return nil, err
	}

	// Invalidate parent directory cache
	s.attrCache.Invalidate(dir.path)
	return s.Lookup(path)
}

// Remove implements the REMOVE operation
func (s *AbsfsNFS) Remove(dir *NFSNode, name string) error {
	if dir == nil {
		return fmt.Errorf("nil directory node")
	}
	if name == "" {
		return fmt.Errorf("empty name")
	}

	if s.options.ReadOnly {
		return os.ErrPermission
	}

	// Sanitize the path to prevent directory traversal attacks
	path, err := sanitizePath(dir.path, name)
	if err != nil {
		return err
	}

	err = s.fs.Remove(path)
	if err == nil {
		// Invalidate caches
		s.attrCache.Invalidate(path)
		s.attrCache.Invalidate(dir.path)
	}
	return err
}

// Rename implements the RENAME operation
func (s *AbsfsNFS) Rename(oldDir *NFSNode, oldName string, newDir *NFSNode, newName string) error {
	if oldDir == nil || newDir == nil {
		return fmt.Errorf("nil directory node")
	}
	if oldName == "" || newName == "" {
		return fmt.Errorf("empty name")
	}

	if s.options.ReadOnly {
		return os.ErrPermission
	}

	// Sanitize both paths to prevent directory traversal attacks
	oldPath, err := sanitizePath(oldDir.path, oldName)
	if err != nil {
		return err
	}

	newPath, err := sanitizePath(newDir.path, newName)
	if err != nil {
		return err
	}

	err = s.fs.Rename(oldPath, newPath)
	if err == nil {
		// Invalidate caches
		s.attrCache.Invalidate(oldPath)
		s.attrCache.Invalidate(newPath)
		s.attrCache.Invalidate(oldDir.path)
		s.attrCache.Invalidate(newDir.path)
	}
	return err
}

// ReadDir implements the READDIR operation
func (s *AbsfsNFS) ReadDir(dir *NFSNode) ([]*NFSNode, error) {
	if dir == nil {
		return nil, fmt.Errorf("nil directory node")
	}

	f, err := s.fs.OpenFile(dir.path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Type assert to get directory interface
	dirFile, ok := f.(absfs.File)
	if !ok {
		return nil, os.ErrInvalid
	}

	// Read directory entries
	entries, err := dirFile.Readdir(-1)
	if err != nil {
		return nil, err
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
			attrs := &NFSAttrs{
				Mode:  info.Mode(),
				Size:  info.Size(),
				Mtime: info.ModTime(),
				Atime: info.ModTime(),
				Uid:   node.attrs.Uid,
				Gid:   node.attrs.Gid,
			}
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

// Unexport stops serving the NFS export
func (s *AbsfsNFS) Unexport() error {
	// Cleanup all open file handles
	s.fileMap.ReleaseAll()
	// Clear caches
	s.attrCache.Clear()
	s.readBuf.Clear()
	return nil
}
