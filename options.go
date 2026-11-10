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
	TransferSize         int
	AttrCacheTimeout     time.Duration
	AttrCacheSize        int
	CacheNegativeLookups bool
	NegativeCacheTimeout time.Duration
	EnableDirCache       bool
	DirCacheTimeout      time.Duration
	DirCacheMaxEntries   int
	DirCacheMaxDirSize   int
	MaxWorkers           int
	MaxConnections       int
	IdleTimeout          time.Duration
	TCPKeepAlive         bool
	TCPNoDelay           bool
	SendBufferSize       int
	ReceiveBufferSize    int
	Async                bool
	MaxHandles           int // file handle cache capacity (see ExportOptions.MaxHandles)
	Log                  *LogConfig
	Timeouts             *TimeoutConfig
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

	// AllowAnySymlinkTarget is ExportOptions.AllowAnySymlinkTarget.
	AllowAnySymlinkTarget bool

	// AllowedUIDs is ExportOptions.AllowedUIDs.
	AllowedUIDs []uint32
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
		TransferSize:         opts.TransferSize,
		AttrCacheTimeout:     opts.AttrCacheTimeout,
		AttrCacheSize:        opts.AttrCacheSize,
		CacheNegativeLookups: opts.CacheNegativeLookups,
		NegativeCacheTimeout: opts.NegativeCacheTimeout,
		EnableDirCache:       opts.EnableDirCache,
		DirCacheTimeout:      opts.DirCacheTimeout,
		DirCacheMaxEntries:   opts.DirCacheMaxEntries,
		DirCacheMaxDirSize:   opts.DirCacheMaxDirSize,
		MaxWorkers:           opts.MaxWorkers,
		MaxConnections:       opts.MaxConnections,
		IdleTimeout:          opts.IdleTimeout,
		TCPKeepAlive:         opts.TCPKeepAlive,
		TCPNoDelay:           opts.TCPNoDelay,
		SendBufferSize:       opts.SendBufferSize,
		ReceiveBufferSize:    opts.ReceiveBufferSize,
		Async:                opts.Async,
		MaxHandles:           opts.MaxHandles,
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

		AllowAnySymlinkTarget: opts.AllowAnySymlinkTarget,
	}
	if len(opts.AllowedIPs) > 0 {
		p.AllowedIPs = make([]string, len(opts.AllowedIPs))
		copy(p.AllowedIPs, opts.AllowedIPs)
	}
	p.AllowedUIDs = cloneUIDs(opts.AllowedUIDs)
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
		AllowAnySymlinkTarget: p.AllowAnySymlinkTarget,
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
		MaxHandles:            t.MaxHandles,
	}
	if len(p.AllowedIPs) > 0 {
		opts.AllowedIPs = make([]string, len(p.AllowedIPs))
		copy(opts.AllowedIPs, p.AllowedIPs)
	}
	opts.AllowedUIDs = cloneUIDs(p.AllowedUIDs)
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
	if updated.MaxHandles <= 0 {
		updated.MaxHandles = DefaultMaxHandles
	}
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
	snapshot.AllowedUIDs = cloneUIDs(newPolicy.AllowedUIDs)
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

	// Resize the file handle cache
	if updated.MaxHandles != old.MaxHandles && n.fileMap != nil {
		n.fileMap.Resize(updated.MaxHandles)
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

	// AttrCacheTimeout controls how long file attributes are cached.
	// Changes made through the server are seen at once (for the spelling
	// of the name they used, on a case-insensitive filesystem); changes
	// made to the filesystem by anything else are seen once it has passed.
	// A changed value applies to entries already cached.
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

	// NegativeCacheTimeout controls how long negative cache entries are kept:
	// how long a file created by anything other than the server can stay
	// invisible. Only applicable when CacheNegativeLookups is true
	// Default: 5 * time.Second
	NegativeCacheTimeout time.Duration

	// EnableDirCache enables caching of directory entries for improved performance
	// When enabled, directory listings are cached to reduce filesystem calls
	// Default: false (disabled)
	EnableDirCache bool

	// DirCacheTimeout controls how long directory listings are cached: how
	// long a change made to the filesystem by anything other than the
	// server can stay out of a listing (the server's own changes are seen at
	// once). Only applicable when EnableDirCache is true
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

	// DefaultOwner is the uid/gid reported for files whose os.FileInfo does
	// not record an owner (memfs and most virtual filesystems, and every
	// filesystem on Windows). Files from the operating system's own
	// filesystem (osfs on unix) report their real owner instead.
	//
	// When nil, the uid and gid of the serving process are used (0/0 on
	// Windows, which has no numeric ids). Reporting the serving user rather
	// than root matters to clients such as git, which refuses to work in a
	// directory owned by someone else ("dubious ownership").
	//
	// Fixed when the export is created: UpdateExportOptions rejects a
	// different non-nil value, and nil there means "unchanged".
	DefaultOwner *FileOwner

	// ForceOwner reports DefaultOwner (or the serving user, when
	// DefaultOwner is nil) for every file, even those whose real owner is
	// known. Use it to present a tree owned by another account as the
	// mounting user's own. Default: false.
	//
	// Fixed when the export is created: UpdateExportOptions rejects a
	// different value.
	ForceOwner bool

	// MaxHandles caps the file handle cache: how many handles the server
	// keeps resolved, each with its NFSNode (about 300 bytes). It bounds
	// memory and the cost of misses, not correctness: a handle no longer
	// cached is decoded, checked and resolved again (one stat), so a
	// client does not see NFS3ERR_STALE for an object that still exists,
	// except in the residual cases docs/internals/file-handles.md lists.
	// Size it above the number of files and directories clients keep
	// using, so their requests stay cache hits. Changeable at runtime; a
	// smaller value evicts at once.
	// Default (<= 0): DefaultMaxHandles (100000)
	MaxHandles int

	// HandleStateDir keeps file handles valid across a server restart. The
	// server keeps its handle key and a journal of its REMOVEs, RMDIRs and
	// RENAMEs there (the journal records only those, before replying), and
	// locks it: a second server cannot use the same directory. After a
	// restart with the same directory, handles mean exactly what they did:
	// clients carry on without remounting.
	//
	// When empty (the default), handles are valid for the life of the
	// server: every handle from before a restart is stale, and clients
	// remount. Deleting or changing the directory has the same effect. The
	// files hold paths of the exported filesystem and are made mode 0600.
	//
	// Handles name objects by path, so HandleStateDir assumes the exported
	// namespace is stable across restarts: a path names the same object
	// before and after, but for the changes the journal records. Those are
	// the changes made through the server; a namespace rebuilt at start
	// (switchfs route names reassigned, a backend swapped for one with
	// other content) is not recorded, and a handle to the old object at a
	// reused path silently reaches the new one. Route names and backends
	// must not be reused for different content. A caller that cannot
	// guarantee a stable namespace sets HandleGeneration, and changes it
	// whenever the namespace changes.
	//
	// Fixed when the export is created: UpdateExportOptions rejects a
	// different non-empty value, and empty there means "unchanged".
	HandleStateDir string

	// HandleGeneration names the version of the exported namespace that
	// handles are issued for. It is an opaque byte string, compared
	// exactly, and folded into the keys handles are tagged with: a handle
	// issued under one generation is NFS3ERR_STALE under any other, decided
	// by its tag alone (with no filesystem I/O, but for a tail handle,
	// which costs at most its capped walk first). Use a value that changes
	// whenever paths may come to name other objects, such as a per-run ID
	// or a hash of the route set, when a restart with HandleStateDir cannot
	// promise that the same path names the same object.
	//
	// It matters only with HandleStateDir: without it every restart
	// already makes every handle stale. With it, the generation is kept in
	// the directory (handle.gen); a start with a different generation makes
	// the key and the journal afresh, so every older handle is stale even
	// if that generation comes back later. A start with the same generation
	// keeps handles valid as HandleStateDir describes. The empty generation
	// (the default) tags handles exactly as servers before it existed did,
	// so a directory made by one keeps its handles.
	//
	// Fixed when the export is created: UpdateExportOptions rejects a
	// different non-empty value, and empty there means "unchanged". To
	// change it, Close the server and make a new one, as a restart does.
	HandleGeneration string

	// AllowAnySymlinkTarget lets clients create symbolic links with any
	// target, including absolute ones and ones with ".." components, such
	// as the "../README.md" links common in source trees. Default: false,
	// in which case SYMLINK refuses such targets with NFSERR_ACCES.
	//
	// The default protects exports of a host directory (osfs, or basefs
	// over osfs): there the host follows links when the server opens a
	// path, so a client could otherwise create a link that leads out of
	// the export and read through it. Set this only when the exported
	// filesystem resolves links within its own namespace, as memfs,
	// c4fs, and unionfs do (a union resolves links itself, even over host
	// layers). READLINK returns every target as stored, whatever this
	// says: reading a link's text gives the client nothing it could not
	// already reach, since the client resolves links itself.
	AllowAnySymlinkTarget bool

	// AllowedUIDs, when not empty, limits the export to AUTH_SYS requests
	// whose credential carries one of these uids (as sent, before any
	// squashing). Other requests, and every AUTH_NONE request, are
	// refused with AUTH_ERROR (AUTH_BADCRED). Default: empty, any uid.
	// NULL and the MOUNT protocol are answered for any uid (macOS
	// mount_nfs sends MNT as uid 0), but an unlisted uid may MNT only the
	// export's root: any other path answers MNT3ERR_ACCES.
	//
	// AUTH_SYS uids are asserted by the client, not proven: a program
	// that can reach the server can put any uid in its credential. So this
	// stops accidents and casual misuse (another local user mounting the
	// export with their own uid, a client mounted as the wrong user), not
	// a hostile user who crafts RPC credentials. Combine it with binding
	// to 127.0.0.1 or an AllowedIPs list, and TLS client certificates
	// where clients must be proven. A typical value is the uid of the
	// user the server serves: []uint32{uint32(os.Getuid())}.
	AllowedUIDs []uint32

	// CaseInsensitive is what PATHCONF reports in case_insensitive:
	// whether a name finds a file spelled with letters of another case
	// (case_preserving is always reported true).
	//
	// CaseInsensitiveAuto (the zero value, the default) answers for each
	// handle what the filesystem it is in does. It probes, reading only:
	// it lists a few names of the handle's directory (a file's parent),
	// looks a few of them up with the case of their letters swapped (ASCII
	// letters where a name has one, else letters with exactly one other
	// case) and compares what it finds with the entry (device and inode, a
	// memfs inode, else type, size and modification time). A name that
	// finds the same file means names fold there; one that finds nothing or
	// another file means they do not. A directory with no name to try
	// (empty, or only digits or kana) takes the answer of its nearest
	// ancestor that has one, and when none does the export's: the answer
	// of the first probe that decided one, else a guess from a name
	// looked up composed and decomposed (a filesystem that matches those,
	// as APFS does, is guessed to fold), kept for the server's lifetime
	// once there is one, else false. Answers are cached per directory for
	// AttrCacheTimeout, and dropped when the server removes, renames or
	// replaces the directory. Since a union may fold names in one layer
	// and not another, a directory where any probed name folds is
	// reported case-insensitive.
	//
	// Where the answer is true, every spelling of an object reports one
	// fileid (names folded as APFS matches them), except two names one
	// directory lists that fold alike, which keep one each. See
	// docs/api/export-options.md.
	//
	// The answers can differ between handles, so in auto mode FSINFO does
	// not report FSF3_HOMOGENEOUS, and clients ask PATHCONF for each
	// object (macOS asks on every pathconf call). The macOS client still
	// takes the volume's capabilities (VOL_CAP_FMT_CASE_SENSITIVE, as
	// Finder and getattrlist see them) from the root's answer at mount.
	//
	// CaseInsensitiveTrue and CaseInsensitiveFalse give that answer for
	// every handle, and FSINFO reports FSF3_HOMOGENEOUS, so clients ask
	// once per mount. CaseInsensitiveFalse is what absnfs reported before
	// this option existed. Set CaseInsensitiveTrue for an export whose
	// root is case-sensitive but whose content folds, such as a memfs root
	// listing switchfs routes to host directories on a case-insensitive
	// volume: auto then answers false for the root (and for a route's
	// directories with no names yet), which the client uses as the
	// volume's answer. Auto also answers false for a union whose probed
	// names all come from a case-sensitive lower layer while its writable
	// layer folds. True is harmless over a case-sensitive root: names
	// still reach the filesystem as spelled.
	//
	// Fixed when the export is created: UpdateExportOptions rejects a
	// different value.
	CaseInsensitive CaseInsensitivity
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

// cloneUIDs returns a copy of uids, nil when it is empty.
func cloneUIDs(uids []uint32) []uint32 {
	if len(uids) == 0 {
		return nil
	}
	return append([]uint32(nil), uids...)
}
