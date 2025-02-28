package absnfs

import (
	"fmt"
	"io"
	"os"
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

// Lookup implements the LOOKUP operation
func (s *AbsfsNFS) Lookup(path string) (*NFSNode, error) {
	if path == "" {
		return nil, fmt.Errorf("empty path")
	}

	// Check cache first
	if attrs := s.attrCache.Get(path); attrs != nil {
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

	attrs := &NFSAttrs{
		Mode:  info.Mode(),
		Size:  info.Size(),
		Mtime: info.ModTime(),
		Atime: info.ModTime(),
		Uid:   node.attrs.Uid,
		Gid:   node.attrs.Gid,
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

	if attrs.Mode != node.attrs.Mode {
		if err := s.fs.Chmod(node.path, attrs.Mode); err != nil {
			return err
		}
	}

	if attrs.Uid != node.attrs.Uid || attrs.Gid != node.attrs.Gid {
		if err := s.fs.Chown(node.path, int(attrs.Uid), int(attrs.Gid)); err != nil {
			return err
		}
	}

	if attrs.Mtime != node.attrs.Mtime || attrs.Atime != node.attrs.Atime {
		if err := s.fs.Chtimes(node.path, attrs.Atime, attrs.Mtime); err != nil {
			return err
		}
	}

	node.attrs = attrs
	node.attrs.Refresh() // Initialize cache validity
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

	// Try read-ahead buffer first
	if data, ok := s.readBuf.Read(node.path, offset, int(count)); ok {
		return data, nil
	}

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

	// Only attempt read-ahead if we got all requested data and there's more to read
	if err != io.EOF && n == int(count) && offset+count < info.Size() {
		readAheadSize := int64(1024 * 1024) // 1MB read-ahead
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

	f, err := s.fs.OpenFile(node.path, os.O_WRONLY, 0)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	n, err := f.WriteAt(data, offset)
	if err == nil {
		// Invalidate cache after successful write
		s.attrCache.Invalidate(node.path)
		s.readBuf.Clear()

		// Update modification time explicitly
		now := time.Now()
		if err := s.fs.Chtimes(node.path, now, now); err != nil {
			return int64(n), err
		}

		// Update node attributes to reflect new size and time
		info, statErr := s.fs.Stat(node.path)
		if statErr == nil {
			node.attrs.Size = info.Size()
			node.attrs.Mtime = info.ModTime()
			node.attrs.Refresh() // Initialize cache validity
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

	path := dir.path + "/" + name
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

	path := dir.path + "/" + name
	err := s.fs.Remove(path)
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

	oldPath := oldDir.path + "/" + oldName
	newPath := newDir.path + "/" + newName

	err := s.fs.Rename(oldPath, newPath)
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
		entryPath := dir.path + "/" + name
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
