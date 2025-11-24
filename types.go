// Package absnfs implements an NFS server adapter for the absfs filesystem interface.
//
// This package allows any filesystem that implements the absfs.FileSystem interface
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
//   - Batch operation processing
//   - Worker pool for concurrent request handling
//   - Comprehensive metrics and monitoring
//
// Basic Usage:
//
//	fs, _ := memfs.NewFS()
//	server, _ := absnfs.New(fs, absnfs.ExportOptions{})
//	server.Export("/export/test")
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
	"runtime"
	"sync"
	"time"

	"github.com/absfs/absfs"
)

// Version represents the current version of the absnfs package
const Version = "0.1.0"

// SymlinkFileSystem represents a filesystem that supports symbolic links
type SymlinkFileSystem interface {
	Symlink(oldname, newname string) error
	Readlink(name string) (string, error)
	Lstat(name string) (os.FileInfo, error)
}

// AbsfsNFS represents an NFS server that exports an absfs filesystem
type AbsfsNFS struct {
	fs            absfs.FileSystem  // The wrapped absfs filesystem
	root          *NFSNode          // Root directory node
	logger        *log.Logger       // Optional logging
	fileMap       *FileHandleMap    // Maps file handles to absfs files
	mountPath     string            // Export path
	options       ExportOptions     // NFS export options
	attrCache     *AttrCache        // Cache for file attributes
	readBuf       *ReadAheadBuffer  // Read-ahead buffer
	memoryMonitor *MemoryMonitor    // Monitors system memory usage (optional)
	workerPool    *WorkerPool       // Worker pool for concurrent operations
	batchProc     *BatchProcessor   // Processor for batched operations
	metrics       *MetricsCollector // Metrics collection and reporting
	rateLimiter   *RateLimiter      // Rate limiter for DoS protection
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
	
	// MaxWorkers controls the maximum number of goroutines used for handling concurrent operations
	// More workers can improve performance for concurrent workloads but consume more CPU resources
	// Default: runtime.NumCPU() * 4 (number of logical CPUs multiplied by 4)
	MaxWorkers int
	
	// BatchOperations enables grouping of similar operations for improved performance
	// When enabled, the server will attempt to process multiple read/write operations
	// together to reduce context switching and improve throughput
	// Default: true
	BatchOperations bool
	
	// MaxBatchSize controls the maximum number of operations that can be included in a single batch
	// Larger batches can improve performance but may increase latency for individual operations
	// Only applicable when BatchOperations is true
	// Default: 10 operations
	MaxBatchSize int
	
	// MaxConnections limits the number of simultaneous client connections
	// Setting to 0 means unlimited connections (limited only by system resources)
	// Default: 100
	MaxConnections int
	
	// IdleTimeout defines how long to keep inactive connections before closing them
	// This helps reclaim resources from abandoned connections
	// Default: 5 * time.Minute
	IdleTimeout time.Duration
	
	// TCPKeepAlive enables TCP keep-alive probes on NFS connections
	// Keep-alive helps detect dead connections when clients disconnect improperly
	// Default: true
	TCPKeepAlive bool
	
	// TCPNoDelay disables Nagle's algorithm on TCP connections to reduce latency
	// This may improve performance for small requests at the cost of increased bandwidth usage
	// Default: true
	TCPNoDelay bool
	
	// internal field to track if TCP settings have been explicitly set
	hasExplicitTCPSettings bool
	
	// SendBufferSize controls the size of the TCP send buffer in bytes
	// Larger buffers can improve throughput but consume more memory
	// Default: 262144 (256KB)
	SendBufferSize int
	
	// ReceiveBufferSize controls the size of the TCP receive buffer in bytes
	// Larger buffers can improve throughput but consume more memory
	// Default: 262144 (256KB)
	ReceiveBufferSize int

	// EnableRateLimiting enables rate limiting and DoS protection
	// When enabled, the server will limit requests per IP, per connection, and per operation type
	// Default: true
	EnableRateLimiting bool

	// RateLimitConfig provides detailed rate limiting configuration
	// Only applicable when EnableRateLimiting is true
	// If nil, default configuration will be used
	RateLimitConfig *RateLimiterConfig

	// TLS holds the TLS/SSL configuration for encrypted connections
	// When TLS.Enabled is true, all NFS connections will be encrypted using TLS
	// Provides confidentiality, integrity, and optional mutual authentication
	// If nil, TLS is disabled and connections are unencrypted (default NFSv3 behavior)
	TLS *TLSConfig
}

// FileHandleMap manages the mapping between NFS file handles and absfs files
type FileHandleMap struct {
	sync.RWMutex
	handles     map[uint64]absfs.File
	nextHandle  uint64        // Counter for allocating new handles
	freeHandles *uint64MinHeap // Min-heap of freed handles for reuse
}

// NFSNode represents a file or directory in the NFS tree
type NFSNode struct {
	absfs.FileSystem
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
	
	// Set worker pool defaults
	if options.MaxWorkers <= 0 {
		options.MaxWorkers = runtime.NumCPU() * 4 // Default: number of logical CPUs * 4
	}
	
	// For BatchOperations, we can't easily check if it was explicitly set to false
	// or just has the default false value. We'll set the default to true for
	// most other cases in the test.
	// This field needs special handling in testing.
	
	if options.MaxBatchSize <= 0 {
		options.MaxBatchSize = 10 // Default: 10 operations per batch
	}
	
	// Connection management defaults
	if options.MaxConnections <= 0 {
		options.MaxConnections = 100 // Default: 100 concurrent connections
	}
	
	if options.IdleTimeout <= 0 {
		options.IdleTimeout = 5 * time.Minute // Default: 5 minutes
	}
	
	// Set TCP socket options defaults if they haven't been explicitly configured
	// We're checking if the options struct was created with fields vs. default values
	if !options.hasExplicitTCPSettings {
		options.TCPKeepAlive = true // Default: enabled
		options.TCPNoDelay = true   // Default: enabled
	}
	
	if options.SendBufferSize <= 0 {
		options.SendBufferSize = 262144 // Default: 256KB
	}
	
	if options.ReceiveBufferSize <= 0 {
		options.ReceiveBufferSize = 262144 // Default: 256KB
	}

	// Rate limiting is enabled by default for security
	// This can be explicitly disabled by setting EnableRateLimiting to false
	// Note: In Go, bool fields default to false, so we can't distinguish between
	// "explicitly set to false" and "not set". We treat not setting EnableRateLimiting
	// as opting in to rate limiting (secure by default).
	if options.RateLimitConfig == nil {
		config := DefaultRateLimiterConfig()
		options.RateLimitConfig = &config
		// Enable rate limiting by default (secure by default)
		options.EnableRateLimiting = true
	}

	// Create server object with configured caches
	server := &AbsfsNFS{
		fs:      fs,
		options: options,
		fileMap: &FileHandleMap{
			handles:     make(map[uint64]absfs.File),
			nextHandle:  1, // Start from 1, as 0 is typically reserved
			freeHandles: NewUint64MinHeap(),
		},
		logger:    log.New(os.Stderr, "[absnfs] ", log.LstdFlags),
		attrCache: NewAttrCache(options.AttrCacheTimeout, options.AttrCacheSize),
		readBuf:   NewReadAheadBuffer(options.ReadAheadSize),
	}
	
	// Configure read-ahead buffer with size limits
	server.readBuf.Configure(options.ReadAheadMaxFiles, options.ReadAheadMaxMemory)
	
	// Initialize and start worker pool
	server.workerPool = NewWorkerPool(options.MaxWorkers, server)
	server.workerPool.Start()
	
	// Initialize batch processor
	server.batchProc = NewBatchProcessor(server, options.MaxBatchSize)
	
	// Start memory pressure monitoring if enabled
	if options.AdaptToMemoryPressure {
		server.startMemoryMonitoring()
	}
	
	// Initialize metrics collection
	server.initMetrics()

	// Initialize rate limiter if enabled
	if options.EnableRateLimiting {
		server.rateLimiter = NewRateLimiter(*options.RateLimitConfig)
		server.logger.Printf("Rate limiting enabled (per-IP: %d req/s, global: %d req/s)",
			options.RateLimitConfig.PerIPRequestsPerSecond,
			options.RateLimitConfig.GlobalRequestsPerSecond)
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
	root.mu.Lock()
	root.attrs.Refresh() // Initialize cache validity
	root.mu.Unlock()

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

// ExecuteWithWorker runs a task in the worker pool
// If the worker pool is not available (disabled or full), it executes the task directly
func (n *AbsfsNFS) ExecuteWithWorker(task func() interface{}) interface{} {
	// If worker pool is not initialized, execute directly
	if n.workerPool == nil {
		return task()
	}
	
	// Try to submit to worker pool with immediate result wait
	result, ok := n.workerPool.SubmitWait(task)
	if ok {
		return result
	}
	
	// If submission failed (pool full or stopped), execute directly
	return task()
}

// Close releases resources and stops any background processes
func (n *AbsfsNFS) Close() error {
	// Stop memory monitoring if active
	n.stopMemoryMonitoring()

	// Stop worker pool
	if n.workerPool != nil {
		n.workerPool.Stop()
	}

	// Stop batch processor
	if n.batchProc != nil {
		n.batchProc.Stop()
	}

	// Release all file handles to prevent file descriptor leaks
	if n.fileMap != nil {
		n.fileMap.ReleaseAll()
	}

	// Clear caches to free memory
	if n.attrCache != nil {
		n.attrCache.Clear()
	}

	if n.readBuf != nil {
		n.readBuf.Clear()
	}

	return nil
}
