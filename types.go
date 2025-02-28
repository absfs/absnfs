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
	fs            absfs.FileSystem // The wrapped absfs filesystem
	root          *NFSNode         // Root directory node
	logger        *log.Logger      // Optional logging
	fileMap       *FileHandleMap   // Maps file handles to absfs files
	mountPath     string           // Export path
	options       ExportOptions    // NFS export options
	attrCache     *AttrCache       // Cache for file attributes
	readBuf       *ReadAheadBuffer // Read-ahead buffer
	memoryMonitor *MemoryMonitor   // Monitors system memory usage (optional)
}

// ExportOptions defines the configuration for an NFS export
type ExportOptions struct {
	ReadOnly    bool     // Export as read-only
	Secure      bool     // Require secure ports (<1024)
	AllowedIPs  []string // List of allowed client IPs/subnets
	Squash      string   // User mapping (root/all/none)
	Async       bool     // Allow async writes
	MaxFileSize int64    // Maximum file size
	
	// TransferSize controls the maximum size in bytes of read/write transfers
	// Larger values may improve performance but require more memory
	// Default: 65536 (64KB)
	TransferSize int
	
	// EnableReadAhead enables read-ahead buffering for improved sequential read performance
	// When a client reads a file sequentially, the server prefetches additional data
	// Default: true
	EnableReadAhead bool
	
	// ReadAheadSize controls the size in bytes of the read-ahead buffer
	// Only applicable when EnableReadAhead is true
	// Default: 262144 (256KB)
	ReadAheadSize int
	
	// ReadAheadMaxFiles controls the maximum number of files that can have active read-ahead buffers
	// Helps limit memory usage by read-ahead buffering
	// Default: 100 files
	ReadAheadMaxFiles int
	
	// ReadAheadMaxMemory controls the maximum amount of memory in bytes that can be used for read-ahead buffers
	// Once this limit is reached, least recently used buffers will be evicted
	// Default: 104857600 (100MB)
	ReadAheadMaxMemory int64
	
	// AttrCacheTimeout controls how long file attributes are cached
	// Longer timeouts improve performance but may cause clients to see stale data
	// Default: 5 * time.Second
	AttrCacheTimeout time.Duration
	
	// AttrCacheSize controls the maximum number of entries in the attribute cache
	// Larger values improve performance but consume more memory
	// Default: 10000 entries
	AttrCacheSize int
	
	// AdaptToMemoryPressure enables automatic cache reduction when system memory is under pressure
	// When enabled, the server will periodically check system memory usage and reduce cache sizes
	// when memory usage exceeds MemoryHighWatermark, until usage falls below MemoryLowWatermark
	// Default: false (disabled)
	AdaptToMemoryPressure bool
	
	// MemoryHighWatermark defines the threshold (as a fraction of total memory) at which 
	// memory pressure reduction actions will be triggered
	// Only applicable when AdaptToMemoryPressure is true
	// Valid range: 0.0 to 1.0 (0% to 100% of total memory)
	// Default: 0.8 (80% of total memory)
	MemoryHighWatermark float64
	
	// MemoryLowWatermark defines the target memory usage (as a fraction of total memory)
	// that the server will try to achieve when reducing cache sizes in response to memory pressure
	// Only applicable when AdaptToMemoryPressure is true
	// Valid range: 0.0 to MemoryHighWatermark
	// Default: 0.6 (60% of total memory)
	MemoryLowWatermark float64
	
	// MemoryCheckInterval defines how frequently memory usage is checked for pressure detection
	// Only applicable when AdaptToMemoryPressure is true
	// Default: 30 * time.Second
	MemoryCheckInterval time.Duration
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

	// Set default values if not specified
	if options.TransferSize <= 0 {
		options.TransferSize = 65536 // Default: 64KB
	}
	
	// Set read-ahead defaults
	if options.ReadAheadSize <= 0 {
		options.ReadAheadSize = 262144 // Default: 256KB
	}
	
	if options.ReadAheadMaxFiles <= 0 {
		options.ReadAheadMaxFiles = 100 // Default: 100 files
	}
	
	if options.ReadAheadMaxMemory <= 0 {
		options.ReadAheadMaxMemory = 104857600 // Default: 100MB
	}
	
	// Set attribute cache defaults
	if options.AttrCacheTimeout <= 0 {
		options.AttrCacheTimeout = 5 * time.Second
	}
	
	if options.AttrCacheSize <= 0 {
		options.AttrCacheSize = 10000
	}
	
	// Set memory pressure detection defaults
	if options.MemoryHighWatermark <= 0 || options.MemoryHighWatermark > 1.0 {
		options.MemoryHighWatermark = 0.8 // Default: 80% of total memory
	}
	
	if options.MemoryLowWatermark <= 0 || options.MemoryLowWatermark >= options.MemoryHighWatermark {
		options.MemoryLowWatermark = 0.6 // Default: 60% of total memory
	}
	
	if options.MemoryCheckInterval <= 0 {
		options.MemoryCheckInterval = 30 * time.Second // Check every 30 seconds by default
	}

	// Create server object with configured caches
	server := &AbsfsNFS{
		fs:      fs,
		options: options,
		fileMap: &FileHandleMap{
			handles: make(map[uint64]absfs.File),
		},
		logger:    log.New(os.Stderr, "[absnfs] ", log.LstdFlags),
		attrCache: NewAttrCache(options.AttrCacheTimeout, options.AttrCacheSize),
		readBuf:   NewReadAheadBuffer(options.ReadAheadSize),
	}
	
	// Configure read-ahead buffer with size limits
	server.readBuf.Configure(options.ReadAheadMaxFiles, options.ReadAheadMaxMemory)
	
	// Start memory pressure monitoring if enabled
	if options.AdaptToMemoryPressure {
		server.startMemoryMonitoring()
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

// startMemoryMonitoring initializes and starts the memory monitor
func (n *AbsfsNFS) startMemoryMonitoring() {
	n.memoryMonitor = NewMemoryMonitor(n)
	n.memoryMonitor.Start(n.options.MemoryCheckInterval)
	n.logger.Printf("Memory pressure monitoring enabled (check interval: %v, high watermark: %.1f%%, low watermark: %.1f%%)",
		n.options.MemoryCheckInterval, 
		n.options.MemoryHighWatermark*100,
		n.options.MemoryLowWatermark*100)
}

// stopMemoryMonitoring stops the memory monitor if it's running
func (n *AbsfsNFS) stopMemoryMonitoring() {
	if n.memoryMonitor != nil && n.memoryMonitor.IsActive() {
		n.memoryMonitor.Stop()
		n.logger.Printf("Memory pressure monitoring stopped")
	}
}

// Close releases resources and stops any background processes
func (n *AbsfsNFS) Close() error {
	// Stop memory monitoring if active
	n.stopMemoryMonitoring()
	
	// Clear caches to free memory
	if n.attrCache != nil {
		n.attrCache.Clear()
	}
	
	if n.readBuf != nil {
		n.readBuf.Clear()
	}
	
	return nil
}
