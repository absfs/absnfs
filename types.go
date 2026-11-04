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
//	server.Export("/", 12049)
//
// On macOS a normal user can then mount it without root:
//
//	mount_nfs -o vers=3,tcp,port=12049,mountport=12049,noresvport,locallocks,noatime localhost:/ ~/nfs
//
// Attributes: every procedure reports the same non-zero file id for an
// object (derived from its path, like the file handles), describes symlinks
// as symlinks, and reports the real owner when the filesystem's FileInfo
// carries a *syscall.Stat_t, otherwise ExportOptions.DefaultOwner, which
// defaults to the user running the server.
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
const Version = "1.2.0"

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

	// owner is reported for files whose FileInfo carries no owner, and for
	// every file when forceOwner is set. Fixed at New.
	owner      FileOwner
	forceOwner bool

	// handleStateDir is ExportOptions.HandleStateDir. Fixed at New.
	handleStateDir string

	// handleGeneration is ExportOptions.HandleGeneration. Fixed at New.
	handleGeneration string

	// caseMode is ExportOptions.CaseInsensitive. Fixed at New. caseCache
	// holds the answers CaseInsensitiveAuto probed (see case.go).
	caseMode  CaseInsensitivity
	caseCache caseCache

	// ownerWriteMu serializes owner-override opens (see openForOwnerWrite).
	ownerWriteMu sync.Mutex

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

// FileHandleMap caches file handles: the NFSNode each recently used handle
// resolved to, and the handle issued for each path. Handles do not depend
// on it to resolve (see filehandle.go).
type FileHandleMap struct {
	sync.RWMutex
	handles    map[string]*handleEntry // handle bytes -> entry
	paths      map[string]string       // canonical path -> handle bytes issued for it
	clock      []string                // eviction candidates in clock order; clock[hand] comes next
	hand       int                     // index in clock of the next eviction candidate
	maxHandles int                     // capacity (<= 0 means DefaultMaxHandles)

	codec *handleCodec  // encodes and verifies handles with the server's key
	log   *changeLog    // the server's REMOVEs, RMDIRs and RENAMEs
	dirs  *dirHashIndex // name hashes of directories, for tail handles

	misses       atomic.Uint64 // handles resolved without the cache
	tailFailures atomic.Uint64 // tail handles no walk resolved
	busy         atomic.Uint64 // resolutions that outwaited a change in flight
}

// NFSNode represents a file or directory in the NFS tree. Its absfs.File
// methods that change the object (Write, WriteAt, WriteString, Truncate,
// Chmod, Chown, Chtimes) invalidate the server's cached attributes for it,
// so the next GetAttr reports the change.
type NFSNode struct {
	absfs.SymlinkFileSystem
	path     string
	fileId   uint64
	server   *AbsfsNFS    // the server whose caches describe this node
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
