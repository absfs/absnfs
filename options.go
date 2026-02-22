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
	EnableReadAhead       bool
	ReadAheadSize         int
	ReadAheadMaxFiles     int
	ReadAheadMaxMemory    int64
	AttrCacheTimeout      time.Duration
	AttrCacheSize         int
	CacheNegativeLookups  bool
	NegativeCacheTimeout  time.Duration
	EnableDirCache        bool
	DirCacheTimeout       time.Duration
	DirCacheMaxEntries    int
	DirCacheMaxDirSize    int
	AdaptToMemoryPressure bool
	MemoryHighWatermark   float64
	MemoryLowWatermark    float64
	MemoryCheckInterval   time.Duration
	MaxWorkers            int
	BatchOperations       bool
	MaxBatchSize          int
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
		EnableReadAhead:       opts.EnableReadAhead,
		ReadAheadSize:         opts.ReadAheadSize,
		ReadAheadMaxFiles:     opts.ReadAheadMaxFiles,
		ReadAheadMaxMemory:    opts.ReadAheadMaxMemory,
		AttrCacheTimeout:      opts.AttrCacheTimeout,
		AttrCacheSize:         opts.AttrCacheSize,
		CacheNegativeLookups:  opts.CacheNegativeLookups,
		NegativeCacheTimeout:  opts.NegativeCacheTimeout,
		EnableDirCache:        opts.EnableDirCache,
		DirCacheTimeout:       opts.DirCacheTimeout,
		DirCacheMaxEntries:    opts.DirCacheMaxEntries,
		DirCacheMaxDirSize:    opts.DirCacheMaxDirSize,
		AdaptToMemoryPressure: opts.AdaptToMemoryPressure,
		MemoryHighWatermark:   opts.MemoryHighWatermark,
		MemoryLowWatermark:    opts.MemoryLowWatermark,
		MemoryCheckInterval:   opts.MemoryCheckInterval,
		MaxWorkers:            opts.MaxWorkers,
		BatchOperations:       opts.BatchOperations,
		MaxBatchSize:          opts.MaxBatchSize,
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
		EnableReadAhead:       t.EnableReadAhead,
		ReadAheadSize:         t.ReadAheadSize,
		ReadAheadMaxFiles:     t.ReadAheadMaxFiles,
		ReadAheadMaxMemory:    t.ReadAheadMaxMemory,
		AttrCacheTimeout:      t.AttrCacheTimeout,
		AttrCacheSize:         t.AttrCacheSize,
		CacheNegativeLookups:  t.CacheNegativeLookups,
		NegativeCacheTimeout:  t.NegativeCacheTimeout,
		EnableDirCache:        t.EnableDirCache,
		DirCacheTimeout:       t.DirCacheTimeout,
		DirCacheMaxEntries:    t.DirCacheMaxEntries,
		DirCacheMaxDirSize:    t.DirCacheMaxDirSize,
		AdaptToMemoryPressure: t.AdaptToMemoryPressure,
		MemoryHighWatermark:   t.MemoryHighWatermark,
		MemoryLowWatermark:    t.MemoryLowWatermark,
		MemoryCheckInterval:   t.MemoryCheckInterval,
		MaxWorkers:            t.MaxWorkers,
		BatchOperations:       t.BatchOperations,
		MaxBatchSize:          t.MaxBatchSize,
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

	// Update read-ahead buffer
	if updated.ReadAheadMaxMemory > 0 && updated.ReadAheadMaxMemory != old.ReadAheadMaxMemory {
		if n.readBuf != nil {
			n.readBuf.Resize(updated.ReadAheadMaxFiles, updated.ReadAheadMaxMemory)
		}
	}
	if updated.ReadAheadMaxFiles > 0 && updated.ReadAheadMaxFiles != old.ReadAheadMaxFiles {
		if n.readBuf != nil {
			n.readBuf.Resize(updated.ReadAheadMaxFiles, updated.ReadAheadMaxMemory)
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
