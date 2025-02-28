package absnfs

import (
	"log"
	"os"
	"sync"
	"time"

	"github.com/absfs/absfs"
)

// Version represents the current version of the absnfs package
const Version = "0.1.0"

// AbsfsNFS represents an NFS server that exports an absfs filesystem
type AbsfsNFS struct {
	fs        absfs.FileSystem // The wrapped absfs filesystem
	root      *NFSNode         // Root directory node
	logger    *log.Logger      // Optional logging
	fileMap   *FileHandleMap   // Maps file handles to absfs files
	mountPath string           // Export path
	options   ExportOptions    // NFS export options
	attrCache *AttrCache       // Cache for file attributes
	readBuf   *ReadAheadBuffer // Read-ahead buffer
}

// ExportOptions defines the configuration for an NFS export
type ExportOptions struct {
	ReadOnly    bool     // Export as read-only
	Secure      bool     // Require secure ports (<1024)
	AllowedIPs  []string // List of allowed client IPs/subnets
	Squash      string   // User mapping (root/all/none)
	Async       bool     // Allow async writes
	MaxFileSize int64    // Maximum file size
}

// FileHandleMap manages the mapping between NFS file handles and absfs files
type FileHandleMap struct {
	sync.RWMutex
	handles    map[uint64]absfs.File
	lastHandle uint64
}

// NFSNode represents a file or directory in the NFS tree
type NFSNode struct {
	absfs.FileSystem
	path     string
	fileId   uint64
	attrs    *NFSAttrs
	children map[string]*NFSNode
}

// NFSAttrs holds the NFS attributes for a file or directory with caching
type NFSAttrs struct {
	Mode       os.FileMode
	Size       int64
	Mtime      time.Time
	Atime      time.Time
	Uid        uint32
	Gid        uint32
	validUntil time.Time
}

// IsValid returns true if the attributes are still valid
func (a *NFSAttrs) IsValid() bool {
	return time.Now().Before(a.validUntil)
}

// Refresh updates the validity time of the attributes
func (a *NFSAttrs) Refresh() {
	a.validUntil = time.Now().Add(2 * time.Second)
}

// Invalidate marks the attributes as invalid
func (a *NFSAttrs) Invalidate() {
	a.validUntil = time.Time{}
}

// New creates a new AbsfsNFS server instance
func New(fs absfs.FileSystem, options ExportOptions) (*AbsfsNFS, error) {
	if fs == nil {
		return nil, os.ErrInvalid
	}

	server := &AbsfsNFS{
		fs:      fs,
		options: options,
		fileMap: &FileHandleMap{
			handles: make(map[uint64]absfs.File),
		},
		logger:    log.New(os.Stderr, "[absnfs] ", log.LstdFlags),
		attrCache: NewAttrCache(30 * time.Second),  // 30 second TTL for attributes
		readBuf:   NewReadAheadBuffer(1024 * 1024), // 1MB read-ahead buffer
	}

	// Initialize root node
	root := &NFSNode{
		FileSystem: fs,
		path:       "/",
		children:   make(map[string]*NFSNode),
	}

	info, err := fs.Stat("/")
	if err != nil {
		return nil, err
	}

	root.attrs = &NFSAttrs{
		Mode:  info.Mode(),
		Size:  info.Size(),
		Mtime: info.ModTime(),
		Atime: info.ModTime(), // Use ModTime as Atime since absfs doesn't expose Atime
		Uid:   0,              // Root ownership by default
		Gid:   0,
	}
	root.attrs.Refresh() // Initialize cache validity

	server.root = root
	return server, nil
}
