// options.go: Configuration types and live-update logic.
//
// Defines TuningOptions (cache sizes, worker pool, transfer size),
// PolicyOptions (allowed hosts, squash mode, read-only), ExportOptions
// (user-facing union of both), TimeoutConfig, and LogConfig. Policy
// changes are applied at runtime via drain-and-swap through atomic pointers.
package absnfs

import (
	"fmt"
	"sync/atomic"
	"time"
)

// TuningOptions contains performance-related settings safe for runtime change.
// Stale reads are harmless -- they only affect performance characteristics.
type TuningOptions struct {
	TransferSize          int
	AttrCacheTimeout      time.Duration
	AttrCacheSize         int
	CacheNegativeLookups  bool
	NegativeCacheTimeout  time.Duration
	EnableDirCache        bool
	DirCacheTimeout       time.Duration
	DirCacheMaxEntries    int
	DirCacheMaxDirSize    int
	MaxWorkers            int
	MaxConnections        int
	IdleTimeout           time.Duration
	TCPKeepAlive          bool
	TCPNoDelay            bool
	SendBufferSize        int
	ReceiveBufferSize     int
	Async                 bool
	Log                   *LogConfig
	Timeouts              *TimeoutConfig
}

// PolicyOptions contains security/access settings that require drain-and-swap.
// Stale reads are dangerous -- they can violate security invariants.
type PolicyOptions struct {
	ReadOnly           bool
	Secure             bool
	AllowedIPs         []string
	Squash             string
	MaxFileSize        int64
	EnableRateLimiting bool
	RateLimitConfig    *RateLimiterConfig
	TLS                *TLSConfig
}

// RequestOptions is a per-request snapshot of both tuning and policy options.
// Created once at HandleCall entry and threaded through the entire request.
type RequestOptions struct {
	Tuning *TuningOptions
	Policy *PolicyOptions
}

// snapshotOptions creates a RequestOptions from the current atomic state.
func (n *AbsfsNFS) snapshotOptions() *RequestOptions {
	return &RequestOptions{
		Tuning: n.tuning.Load(),
		Policy: n.policy.Load(),
	}
}

// tuningFromExportOptions extracts TuningOptions from ExportOptions.
func tuningFromExportOptions(opts *ExportOptions) *TuningOptions {
	t := &TuningOptions{
		TransferSize:          opts.TransferSize,
		AttrCacheTimeout:      opts.AttrCacheTimeout,
		AttrCacheSize:         opts.AttrCacheSize,
		CacheNegativeLookups:  opts.CacheNegativeLookups,
		NegativeCacheTimeout:  opts.NegativeCacheTimeout,
		EnableDirCache:        opts.EnableDirCache,
		DirCacheTimeout:       opts.DirCacheTimeout,
		DirCacheMaxEntries:    opts.DirCacheMaxEntries,
		DirCacheMaxDirSize:    opts.DirCacheMaxDirSize,
		MaxWorkers:            opts.MaxWorkers,
		MaxConnections:        opts.MaxConnections,
		IdleTimeout:           opts.IdleTimeout,
		TCPKeepAlive:          opts.TCPKeepAlive,
		TCPNoDelay:            opts.TCPNoDelay,
		SendBufferSize:        opts.SendBufferSize,
		ReceiveBufferSize:     opts.ReceiveBufferSize,
		Async:                 opts.Async,
	}
	if opts.Log != nil {
		logCopy := *opts.Log
		t.Log = &logCopy
	}
	if opts.Timeouts != nil {
		tCopy := *opts.Timeouts
		t.Timeouts = &tCopy
	}
	return t
}

// policyFromExportOptions extracts PolicyOptions from ExportOptions.
func policyFromExportOptions(opts *ExportOptions) *PolicyOptions {
	p := &PolicyOptions{
		ReadOnly:           opts.ReadOnly,
		Secure:             opts.Secure,
		Squash:             opts.Squash,
		MaxFileSize:        opts.MaxFileSize,
		EnableRateLimiting: opts.EnableRateLimiting,
	}
	if len(opts.AllowedIPs) > 0 {
		p.AllowedIPs = make([]string, len(opts.AllowedIPs))
		copy(p.AllowedIPs, opts.AllowedIPs)
	}
	if opts.RateLimitConfig != nil {
		rc := *opts.RateLimitConfig
		p.RateLimitConfig = &rc
	}
	if opts.TLS != nil {
		p.TLS = opts.TLS.Clone()
	}
	return p
}

// exportOptionsFromSnapshots reconstructs an ExportOptions from tuning + policy snapshots.
func exportOptionsFromSnapshots(t *TuningOptions, p *PolicyOptions) ExportOptions {
	opts := ExportOptions{
		ReadOnly:              p.ReadOnly,
		Secure:                p.Secure,
		Squash:                p.Squash,
		MaxFileSize:           p.MaxFileSize,
		EnableRateLimiting:    p.EnableRateLimiting,
		Async:                 t.Async,
		TransferSize:          t.TransferSize,
		AttrCacheTimeout:      t.AttrCacheTimeout,
		AttrCacheSize:         t.AttrCacheSize,
		CacheNegativeLookups:  t.CacheNegativeLookups,
		NegativeCacheTimeout:  t.NegativeCacheTimeout,
		EnableDirCache:        t.EnableDirCache,
		DirCacheTimeout:       t.DirCacheTimeout,
		DirCacheMaxEntries:    t.DirCacheMaxEntries,
		DirCacheMaxDirSize:    t.DirCacheMaxDirSize,
		MaxWorkers:            t.MaxWorkers,
		MaxConnections:        t.MaxConnections,
		IdleTimeout:           t.IdleTimeout,
		TCPKeepAlive:          t.TCPKeepAlive,
		TCPNoDelay:            t.TCPNoDelay,
		SendBufferSize:        t.SendBufferSize,
		ReceiveBufferSize:     t.ReceiveBufferSize,
	}
	if len(p.AllowedIPs) > 0 {
		opts.AllowedIPs = make([]string, len(p.AllowedIPs))
		copy(opts.AllowedIPs, p.AllowedIPs)
	}
	if p.RateLimitConfig != nil {
		rc := *p.RateLimitConfig
		opts.RateLimitConfig = &rc
	}
	if p.TLS != nil {
		opts.TLS = p.TLS.Clone()
	}
	if t.Log != nil {
		logCopy := *t.Log
		opts.Log = &logCopy
	}
	if t.Timeouts != nil {
		tCopy := *t.Timeouts
		opts.Timeouts = &tCopy
	}
	return opts
}

// UpdateTuningOptions applies a mutation function to the current tuning options.
// The mutation is applied to a copy; the result is stored atomically.
// No drain is needed -- stale tuning reads are harmless.
func (n *AbsfsNFS) UpdateTuningOptions(fn func(*TuningOptions)) {
	n.tuningMu.Lock()
	defer n.tuningMu.Unlock()

	old := n.tuning.Load()
	updated := *old // shallow copy
	// Deep copy pointer fields
	if old.Log != nil {
		logCopy := *old.Log
		updated.Log = &logCopy
	}
	if old.Timeouts != nil {
		tCopy := *old.Timeouts
		updated.Timeouts = &tCopy
	}
	fn(&updated)
	n.tuning.Store(&updated)
	n.applyTuningSideEffects(old, &updated)
}

// UpdatePolicyOptions swaps policy using drain-and-swap.
// Stops accepting new requests, waits for in-flight requests to finish,
// then atomically swaps to the new policy.
func (n *AbsfsNFS) UpdatePolicyOptions(newPolicy PolicyOptions) error {
	n.policyMu.Lock()
	defer n.policyMu.Unlock()

	old := n.policy.Load()
	if old.Squash != newPolicy.Squash {
		return fmt.Errorf("cannot change Squash mode at runtime")
	}

	// Drain in-flight requests: Lock() blocks until all RLock holders
	// (in-flight requests) release. New requests using TryRLock will fail
	// and return NFSERR_JUKEBOX so clients retry.
	n.policyRWMu.Lock()

	// Swap to new policy (deep copy slices/pointers)
	snapshot := newPolicy
	if len(newPolicy.AllowedIPs) > 0 {
		snapshot.AllowedIPs = make([]string, len(newPolicy.AllowedIPs))
		copy(snapshot.AllowedIPs, newPolicy.AllowedIPs)
	}
	if newPolicy.RateLimitConfig != nil {
		rc := *newPolicy.RateLimitConfig
		snapshot.RateLimitConfig = &rc
	}
	if newPolicy.TLS != nil {
		snapshot.TLS = newPolicy.TLS.Clone()
	}
	n.policy.Store(&snapshot)

	// Update rate limiter while still holding the write lock (H2 fix)
	if newPolicy.EnableRateLimiting && newPolicy.RateLimitConfig != nil {
		n.rateLimiter = NewRateLimiter(*newPolicy.RateLimitConfig)
	} else if !newPolicy.EnableRateLimiting {
		n.rateLimiter = nil
	}

	// Resume accepting requests
	n.policyRWMu.Unlock()

	return nil
}

// getStructuredLogger returns the current structured logger safely.
// The returned Logger is safe to use after the call returns.
func (n *AbsfsNFS) getStructuredLogger() Logger {
	n.loggerMu.RLock()
	l := n.structuredLogger
	n.loggerMu.RUnlock()
	return l
}

// applyTuningSideEffects resizes caches, worker pools, etc.
// when tuning options change.
func (n *AbsfsNFS) applyTuningSideEffects(old, updated *TuningOptions) {
	// Resize attribute cache
	if updated.AttrCacheSize > 0 && updated.AttrCacheSize != old.AttrCacheSize {
		if n.attrCache != nil {
			n.attrCache.Resize(updated.AttrCacheSize)
		}
	}
	if updated.AttrCacheTimeout > 0 && updated.AttrCacheTimeout != old.AttrCacheTimeout {
		if n.attrCache != nil {
			n.attrCache.UpdateTTL(updated.AttrCacheTimeout)
		}
	}

	// Update negative caching
	if updated.CacheNegativeLookups != old.CacheNegativeLookups ||
		updated.NegativeCacheTimeout != old.NegativeCacheTimeout {
		if n.attrCache != nil {
			n.attrCache.ConfigureNegativeCaching(updated.CacheNegativeLookups, updated.NegativeCacheTimeout)
		}
	}

	// Update directory cache
	if updated.DirCacheMaxEntries > 0 && updated.DirCacheMaxEntries != old.DirCacheMaxEntries {
		if n.dirCache != nil {
			n.dirCache.Resize(updated.DirCacheMaxEntries)
		}
	}
	if updated.DirCacheTimeout > 0 && updated.DirCacheTimeout != old.DirCacheTimeout {
		if n.dirCache != nil {
			n.dirCache.UpdateTTL(updated.DirCacheTimeout)
		}
	}

	// Update worker pool
	if updated.MaxWorkers > 0 && updated.MaxWorkers != old.MaxWorkers {
		if n.workerPool != nil {
			n.workerPool.Resize(updated.MaxWorkers)
		}
	}

	// Update logging configuration
	if updated.Log != nil && (old.Log == nil || *updated.Log != *old.Log) {
		slogger, err := NewSlogLogger(updated.Log)
		if err != nil {
			n.logger.Printf("failed to create structured logger: %v", err)
		} else {
			n.loggerMu.Lock()
			if oldLogger, ok := n.structuredLogger.(*SlogLogger); ok {
				oldLogger.Close()
			}
			n.structuredLogger = slogger
			n.loggerMu.Unlock()
		}
	}
}

// initAtomicOptions populates the atomic pointers from an ExportOptions.
// Called once during New().
func (n *AbsfsNFS) initAtomicOptions(opts *ExportOptions) {
	n.tuning = atomic.Pointer[TuningOptions]{}
	n.policy = atomic.Pointer[PolicyOptions]{}
	n.tuning.Store(tuningFromExportOptions(opts))
	n.policy.Store(policyFromExportOptions(opts))
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

	// AttrCacheTimeout controls how long file attributes are cached
	// Longer timeouts improve performance but may cause clients to see stale data
	// Default: 5 * time.Second
	AttrCacheTimeout time.Duration

	// AttrCacheSize controls the maximum number of entries in the attribute cache
	// Larger values improve performance but consume more memory
	// Default: 10000 entries
	AttrCacheSize int

	// CacheNegativeLookups enables caching of failed lookups (file not found)
	// This can significantly reduce filesystem load for repeated lookups of non-existent files
	// Negative cache entries use a shorter TTL than positive entries
	// Default: false (disabled)
	CacheNegativeLookups bool

	// NegativeCacheTimeout controls how long negative cache entries are kept
	// Shorter timeouts reduce the chance of stale negative cache entries
	// Only applicable when CacheNegativeLookups is true
	// Default: 5 * time.Second
	NegativeCacheTimeout time.Duration

	// EnableDirCache enables caching of directory entries for improved performance
	// When enabled, directory listings are cached to reduce filesystem calls
	// Default: false (disabled)
	EnableDirCache bool

	// DirCacheTimeout controls how long directory entries are cached
	// Longer timeouts improve performance but may cause clients to see stale directory listings
	// Only applicable when EnableDirCache is true
	// Default: 10 * time.Second
	DirCacheTimeout time.Duration

	// DirCacheMaxEntries controls the maximum number of directories that can be cached
	// Helps limit memory usage by directory entry caching
	// Only applicable when EnableDirCache is true
	// Default: 1000 directories
	DirCacheMaxEntries int

	// DirCacheMaxDirSize controls the maximum number of entries in a single directory that will be cached
	// Directories with more entries than this will not be cached to prevent memory issues
	// Only applicable when EnableDirCache is true
	// Default: 10000 entries per directory
	DirCacheMaxDirSize int

	// MaxWorkers controls the maximum number of goroutines used for handling concurrent operations
	// More workers can improve performance for concurrent workloads but consume more CPU resources
	// Default: runtime.NumCPU() * 4 (number of logical CPUs multiplied by 4)
	MaxWorkers int

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
	// Default: false
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

	// Log holds the logging configuration for the NFS server
	// When nil, logging is disabled (no-op logger is used)
	// When provided, enables structured logging with configurable level, format, and output
	Log *LogConfig

	// Timeouts controls operation-specific timeout durations
	// When nil, default timeouts are used for all operations
	// Allows fine-grained control over how long each operation type can take
	Timeouts *TimeoutConfig
}

// TimeoutConfig defines timeout durations for various NFS operations
type TimeoutConfig struct {
	// ReadTimeout is the maximum time allowed for read operations
	// Default: 30 seconds
	ReadTimeout time.Duration

	// WriteTimeout is the maximum time allowed for write operations
	// Default: 60 seconds
	WriteTimeout time.Duration

	// LookupTimeout is the maximum time allowed for lookup operations
	// Default: 10 seconds
	LookupTimeout time.Duration

	// ReaddirTimeout is the maximum time allowed for readdir operations
	// Default: 30 seconds
	ReaddirTimeout time.Duration

	// CreateTimeout is the maximum time allowed for create operations
	// Default: 15 seconds
	CreateTimeout time.Duration

	// RemoveTimeout is the maximum time allowed for remove operations
	// Default: 15 seconds
	RemoveTimeout time.Duration

	// RenameTimeout is the maximum time allowed for rename operations
	// Default: 20 seconds
	RenameTimeout time.Duration

	// HandleTimeout is the maximum time allowed for file handle operations
	// Default: 5 seconds
	HandleTimeout time.Duration

	// DefaultTimeout is the fallback timeout for operations without a specific timeout
	// Default: 30 seconds
	DefaultTimeout time.Duration
}

// LogConfig defines the logging configuration for the NFS server
type LogConfig struct {
	// Level sets the minimum log level to output
	// Valid values: "debug", "info", "warn", "error"
	// Default: "info"
	Level string

	// Format sets the log output format
	// Valid values: "json", "text"
	// Default: "text"
	Format string

	// Output sets the log destination
	// Valid values: "stdout", "stderr", or a file path
	// Default: "stderr"
	Output string

	// LogClientIPs enables logging of client IP addresses
	// When true, client IPs are included in connection and authentication logs
	// Default: false (for privacy)
	LogClientIPs bool

	// LogOperations enables detailed logging of NFS operations
	// When true, logs each NFS operation (LOOKUP, READ, WRITE, etc.) with timing
	// Default: false (reduces log volume)
	LogOperations bool

	// LogFileAccess enables logging of file access patterns
	// When true, logs file opens, closes, and access patterns
	// Default: false (reduces log volume)
	LogFileAccess bool

	// MaxSize defines the maximum size of log file in megabytes before rotation
	// NOTE: File rotation is not yet implemented. This field is reserved for future enhancement.
	// Only applicable when Output is a file path
	// Default: 100 MB
	MaxSize int

	// MaxBackups defines the maximum number of old log files to retain
	// NOTE: File rotation is not yet implemented. This field is reserved for future enhancement.
	// Only applicable when Output is a file path
	// Default: 3
	MaxBackups int

	// MaxAge defines the maximum number of days to retain old log files
	// NOTE: File rotation is not yet implemented. This field is reserved for future enhancement.
	// Only applicable when Output is a file path
	// Default: 28 days
	MaxAge int

	// Compress enables gzip compression of rotated log files
	// NOTE: File rotation is not yet implemented. This field is reserved for future enhancement.
	// Only applicable when Output is a file path
	// Default: false
	Compress bool
}
