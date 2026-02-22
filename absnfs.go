// absnfs.go: Constructor and lifecycle methods for AbsfsNFS.
//
// Contains New() for creating server instances, Close() for graceful
// shutdown, and runtime configuration methods like SetLogger() and
// UpdateExportOptions(). This is the main API entry point.
package absnfs

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/absfs/absfs"
)

// New creates a new AbsfsNFS server instance
func New(fs absfs.SymlinkFileSystem, options ExportOptions) (*AbsfsNFS, error) {
	if fs == nil {
		return nil, os.ErrInvalid
	}

	// Validate squash mode
	squash := strings.ToLower(options.Squash)
	if squash != "" && squash != "root" && squash != "all" && squash != "none" {
		return nil, fmt.Errorf("invalid squash mode %q: must be root, all, or none", options.Squash)
	}

	// Set default values if not specified
	if options.TransferSize <= 0 {
		options.TransferSize = 65536 // Default: 64KB
	}

	// Set attribute cache defaults
	if options.AttrCacheTimeout <= 0 {
		options.AttrCacheTimeout = 5 * time.Second
	}

	if options.AttrCacheSize <= 0 {
		options.AttrCacheSize = 10000
	}

	// Set negative cache defaults
	if options.NegativeCacheTimeout <= 0 {
		options.NegativeCacheTimeout = 5 * time.Second
	}

	// Set directory cache defaults
	if options.DirCacheTimeout <= 0 {
		options.DirCacheTimeout = 10 * time.Second
	}

	if options.DirCacheMaxEntries <= 0 {
		options.DirCacheMaxEntries = 1000
	}

	if options.DirCacheMaxDirSize <= 0 {
		options.DirCacheMaxDirSize = 10000
	}

	// Set worker pool defaults
	if options.MaxWorkers <= 0 {
		options.MaxWorkers = runtime.NumCPU() * 4 // Default: number of logical CPUs * 4
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

	// Set timeout defaults if not specified
	if options.Timeouts == nil {
		options.Timeouts = &TimeoutConfig{
			ReadTimeout:    30 * time.Second,
			WriteTimeout:   60 * time.Second,
			LookupTimeout:  10 * time.Second,
			ReaddirTimeout: 30 * time.Second,
			CreateTimeout:  15 * time.Second,
			RemoveTimeout:  15 * time.Second,
			RenameTimeout:  20 * time.Second,
			HandleTimeout:  5 * time.Second,
			DefaultTimeout: 30 * time.Second,
		}
	} else {
		// Fill in any zero values with defaults
		if options.Timeouts.ReadTimeout <= 0 {
			options.Timeouts.ReadTimeout = 30 * time.Second
		}
		if options.Timeouts.WriteTimeout <= 0 {
			options.Timeouts.WriteTimeout = 60 * time.Second
		}
		if options.Timeouts.LookupTimeout <= 0 {
			options.Timeouts.LookupTimeout = 10 * time.Second
		}
		if options.Timeouts.ReaddirTimeout <= 0 {
			options.Timeouts.ReaddirTimeout = 30 * time.Second
		}
		if options.Timeouts.CreateTimeout <= 0 {
			options.Timeouts.CreateTimeout = 15 * time.Second
		}
		if options.Timeouts.RemoveTimeout <= 0 {
			options.Timeouts.RemoveTimeout = 15 * time.Second
		}
		if options.Timeouts.RenameTimeout <= 0 {
			options.Timeouts.RenameTimeout = 20 * time.Second
		}
		if options.Timeouts.HandleTimeout <= 0 {
			options.Timeouts.HandleTimeout = 5 * time.Second
		}
		if options.Timeouts.DefaultTimeout <= 0 {
			options.Timeouts.DefaultTimeout = 30 * time.Second
		}
	}

	// Create server object with configured caches
	// Initialize structured logger
	var structuredLogger Logger
	if options.Log != nil {
		slogger, err := NewSlogLogger(options.Log)
		if err != nil {
			return nil, fmt.Errorf("failed to create logger: %w", err)
		}
		structuredLogger = slogger
	} else {
		// Use no-op logger when logging is disabled
		structuredLogger = NewNoopLogger()
	}

	server := &AbsfsNFS{
		fs: fs,
		fileMap: &FileHandleMap{
			handles:     make(map[uint64]absfs.File),
			pathHandles: make(map[string]uint64),
			nextHandle:  1, // Start from 1, as 0 is typically reserved
			freeHandles: NewUint64MinHeap(),
		},
		logger:           log.New(os.Stderr, "[absnfs] ", log.LstdFlags),
		structuredLogger: structuredLogger,
		attrCache:        NewAttrCache(options.AttrCacheTimeout, options.AttrCacheSize),
	}

	// Populate atomic option pointers from the fully-defaulted ExportOptions
	server.initAtomicOptions(&options)

	// Initialize directory cache if enabled
	if options.EnableDirCache {
		server.dirCache = NewDirCache(options.DirCacheTimeout, options.DirCacheMaxEntries, options.DirCacheMaxDirSize)
	}

	// Configure negative caching
	server.attrCache.ConfigureNegativeCaching(options.CacheNegativeLookups, options.NegativeCacheTimeout)

	// Initialize and start worker pool
	server.workerPool = NewWorkerPool(options.MaxWorkers, server)
	server.workerPool.Start()

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
		SymlinkFileSystem: fs,
		path:              "/",
		children:          make(map[string]*NFSNode),
	}

	info, err := fs.Stat("/")
	if err != nil {
		return nil, err
	}

	modTime := info.ModTime()
	root.attrs = &NFSAttrs{
		Mode: info.Mode(),
		Size: info.Size(),
		Uid:  0, // Root ownership by default
		Gid:  0,
	}
	root.attrs.SetMtime(modTime)
	root.attrs.SetAtime(modTime) // Use ModTime as Atime since absfs doesn't expose Atime
	root.mu.Lock()
	root.attrs.Refresh() // Initialize cache validity
	root.mu.Unlock()

	server.root = root
	return server, nil
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

// GetAttrCacheSize returns the current attribute cache size in a thread-safe manner
func (n *AbsfsNFS) GetAttrCacheSize() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.attrCache == nil {
		return 0
	}
	return n.attrCache.MaxSize()
}

// Close releases resources and stops any background processes
func (n *AbsfsNFS) Close() error {
	// Stop the server if Export() created one
	if n.exportServer != nil {
		n.exportServer.Stop()
		n.exportServer = nil
	}

	// Stop worker pool
	if n.workerPool != nil {
		n.workerPool.Stop()
	}

	// Release all file handles to prevent file descriptor leaks
	if n.fileMap != nil {
		n.fileMap.ReleaseAll()
	}

	// Clear caches to free memory
	if n.attrCache != nil {
		n.attrCache.Clear()
	}

	if n.dirCache != nil {
		n.dirCache.Clear()
	}

	// Close structured logger if it's a SlogLogger
	n.loggerMu.Lock()
	slogger, isSlog := n.structuredLogger.(*SlogLogger)
	n.loggerMu.Unlock()
	if isSlog {
		if err := slogger.Close(); err != nil {
			return fmt.Errorf("failed to close logger: %w", err)
		}
	}

	return nil
}

// SetLogger sets or updates the structured logger for the NFS server
// This allows changing the logger after the server has been created
// Pass nil to disable logging (uses no-op logger)
func (n *AbsfsNFS) SetLogger(logger Logger) error {
	if n == nil {
		return fmt.Errorf("nil server")
	}

	n.loggerMu.Lock()
	defer n.loggerMu.Unlock()

	// Close existing logger if it's a SlogLogger before replacing it
	if slogger, ok := n.structuredLogger.(*SlogLogger); ok {
		if err := slogger.Close(); err != nil {
			// Log error but continue with setting new logger
			n.logger.Printf("failed to close previous logger: %v", err)
		}
	}

	if logger == nil {
		// Use no-op logger when nil is passed
		n.structuredLogger = NewNoopLogger()
		return nil
	}

	n.structuredLogger = logger
	return nil
}

// GetExportOptions returns a copy of the current export options
// This is thread-safe and returns a snapshot of the current configuration
func (n *AbsfsNFS) GetExportOptions() ExportOptions {
	return exportOptionsFromSnapshots(n.tuning.Load(), n.policy.Load())
}

// UpdateExportOptions updates the server's export options at runtime.
// Internally splits the incoming ExportOptions into tuning and policy changes.
// Tuning changes apply immediately via atomic swap.
// Policy changes use drain-and-swap to ensure in-flight requests complete first.
func (n *AbsfsNFS) UpdateExportOptions(newOptions ExportOptions) error {
	if n == nil {
		return fmt.Errorf("nil server")
	}

	// Apply tuning changes (lock-free, immediate).
	// Use tuningFromExportOptions for complete field coverage.
	// Preserve Timeouts and Log from the current snapshot when not provided,
	// since nil pointer fields would cause panics on NFS operations.
	n.UpdateTuningOptions(func(t *TuningOptions) {
		newTuning := tuningFromExportOptions(&newOptions)
		if newTuning.Timeouts == nil {
			newTuning.Timeouts = t.Timeouts
		}
		if newTuning.Log == nil {
			newTuning.Log = t.Log
		}
		*t = *newTuning
	})

	// Validate immutable fields before attempting policy update.
	// Squash cannot be changed at runtime.
	currentPolicy := n.policy.Load()
	if newOptions.Squash != "" && newOptions.Squash != currentPolicy.Squash {
		return fmt.Errorf("cannot change Squash mode at runtime (requires restart)")
	}

	// Apply policy changes (drain-and-swap)
	newPolicy := PolicyOptions{
		ReadOnly:           newOptions.ReadOnly,
		Secure:             newOptions.Secure,
		Squash:             currentPolicy.Squash, // immutable
		MaxFileSize:        newOptions.MaxFileSize,
		EnableRateLimiting: newOptions.EnableRateLimiting,
	}
	if len(newOptions.AllowedIPs) > 0 {
		newPolicy.AllowedIPs = make([]string, len(newOptions.AllowedIPs))
		copy(newPolicy.AllowedIPs, newOptions.AllowedIPs)
	}
	if newOptions.RateLimitConfig != nil {
		rc := *newOptions.RateLimitConfig
		newPolicy.RateLimitConfig = &rc
	}
	if newOptions.TLS != nil {
		newPolicy.TLS = newOptions.TLS.Clone()
	}

	return n.UpdatePolicyOptions(newPolicy)
}
