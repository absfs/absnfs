// Package absnfs implements an NFS server adapter for the absfs filesystem interface.
//
// This package allows any filesystem that implements the absfs.SymlinkFileSystem interface
// to be exported as an NFSv3 share over a network. It provides a complete NFS server
// implementation with support for standard file operations, security features, and
// performance optimizations.
//
// Key Features:
//   - NFSv3 protocol implementation
//   - TLS/SSL encryption support for secure connections
//   - Symlink support (SYMLINK and READLINK operations)
//   - Rate limiting and DoS protection
//   - Attribute caching for improved performance
//   - Worker pool for concurrent request handling
//   - Comprehensive metrics and monitoring
//
// Basic Usage:
//
//	fs, _ := memfs.NewFS()
//	server, _ := absnfs.New(fs, absnfs.ExportOptions{})
//	server.Export("/export/test", 2049)
//
// Security Features:
//   - IP-based access control
//   - Read-only export mode
//   - User ID mapping (squash options)
//   - Rate limiting to prevent DoS attacks
//   - TLS/SSL encryption
//
// For detailed documentation, see the docs/ directory in the repository.
package absnfs

import (
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/absfs/absfs"
)

// Version represents the current version of the absnfs package
const Version = "2.0.0"

// AbsfsNFS represents an NFS server that exports an absfs filesystem
type AbsfsNFS struct {
	mu               sync.RWMutex            // Protects shared mutable state
	fs               absfs.SymlinkFileSystem // The wrapped absfs filesystem (supports symlinks)
	root             *NFSNode                // Root directory node
	logger           *log.Logger             // Deprecated: use structuredLogger instead
	structuredLogger Logger                  // Structured logger for production use
	fileMap          *FileHandleMap          // Maps file handles to absfs files
	mountPath        string                  // Export path
	attrCache        *AttrCache              // Cache for file attributes
	dirCache         *DirCache               // Cache for directory entries
	workerPool       *WorkerPool             // Worker pool for concurrent operations
	metrics          *MetricsCollector       // Metrics collection and reporting
	rateLimiter      *RateLimiter            // Rate limiter for DoS protection
	exportServer     *Server                 // Server created by Export(), nil if not exported

	// Options are stored as immutable snapshots behind atomic pointers.
	// Readers load the pointer -- no lock needed.
	tuning atomic.Pointer[TuningOptions]
	policy atomic.Pointer[PolicyOptions]

	// tuningMu serializes tuning updates to prevent lost-update races.
	tuningMu sync.Mutex

	// policyMu serializes policy changes (drain-and-swap).
	policyMu sync.Mutex

	// policyRWMu protects policy reads during NFS requests.
	// HandleCall acquires RLock (via TryRLock) for the duration of request processing.
	// UpdatePolicyOptions acquires Lock to drain in-flight requests and swap policy.
	policyRWMu sync.RWMutex

	// loggerMu protects structuredLogger writes from concurrent access.
	loggerMu sync.RWMutex
}

// FileHandleMap manages the mapping between NFS file handles and absfs files
type FileHandleMap struct {
	sync.RWMutex
	handles     map[uint64]absfs.File
	pathHandles map[string]uint64  // Reverse map: path -> handle for deduplication
	nextHandle  uint64             // Counter for allocating new handles
	freeHandles *uint64MinHeap     // Min-heap of freed handles for reuse
	maxHandles  int                // Maximum handles before eviction (0 = DefaultMaxHandles)
}

// NFSNode represents a file or directory in the NFS tree
type NFSNode struct {
	absfs.SymlinkFileSystem
	path     string
	fileId   uint64
	mu       sync.RWMutex // Protects attrs access
	attrs    *NFSAttrs
	children map[string]*NFSNode
}

// NFSAttrs holds the NFS attributes for a file or directory with caching
type NFSAttrs struct {
	Mode       os.FileMode
	Size       int64
	FileId     uint64 // Unique file identifier (inode number)
	mtime      time.Time
	atime      time.Time
	Uid        uint32
	Gid        uint32
	validUntil time.Time
}

// NewNFSAttrs creates a new NFSAttrs with the specified values
func NewNFSAttrs(mode os.FileMode, size int64, mtime, atime time.Time, uid, gid uint32) *NFSAttrs {
	attrs := &NFSAttrs{
		Mode: mode,
		Size: size,
		Uid:  uid,
		Gid:  gid,
	}
	attrs.SetMtime(mtime)
	attrs.SetAtime(atime)
	return attrs
}

// Mtime returns the modification time
func (a *NFSAttrs) Mtime() time.Time {
	return a.mtime
}

// SetMtime sets the modification time
func (a *NFSAttrs) SetMtime(t time.Time) {
	a.mtime = t
}

// Atime returns the access time
func (a *NFSAttrs) Atime() time.Time {
	return a.atime
}

// SetAtime sets the access time
func (a *NFSAttrs) SetAtime(t time.Time) {
	a.atime = t
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
