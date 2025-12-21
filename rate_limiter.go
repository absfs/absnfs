package absnfs

import (
	"sync"
	"time"
)

// RateLimiterConfig defines rate limiting parameters
type RateLimiterConfig struct {
	// Global limits
	GlobalRequestsPerSecond int // Maximum requests per second across all clients

	// Per-IP limits
	PerIPRequestsPerSecond int // Maximum requests per second per IP
	PerIPBurstSize         int // Burst allowance per IP

	// Per-connection limits
	PerConnectionRequestsPerSecond int // Maximum requests per second per connection
	PerConnectionBurstSize         int // Burst allowance per connection

	// Per-operation type limits
	ReadLargeOpsPerSecond     int // Large reads (>64KB) per second per IP
	WriteLargeOpsPerSecond    int // Large writes (>64KB) per second per IP
	ReaddirOpsPerSecond       int // READDIR operations per second per IP

	// Mount operation limits
	MountOpsPerMinute int // MOUNT operations per minute per IP

	// File handle limits
	FileHandlesPerIP  int // Maximum file handles per IP
	FileHandlesGlobal int // Maximum file handles globally

	// Cleanup
	CleanupInterval time.Duration // How often to cleanup old entries
}

// DefaultRateLimiterConfig returns sensible defaults
func DefaultRateLimiterConfig() RateLimiterConfig {
	return RateLimiterConfig{
		GlobalRequestsPerSecond:        10000,
		PerIPRequestsPerSecond:         1000,
		PerIPBurstSize:                 500, // Increased for NFS client bursts during mount
		PerConnectionRequestsPerSecond: 500,
		PerConnectionBurstSize:         100, // Increased for NFS client bursts
		ReadLargeOpsPerSecond:          100,
		WriteLargeOpsPerSecond:         50,
		ReaddirOpsPerSecond:            50, // Increased for directory listings
		MountOpsPerMinute:              10,
		FileHandlesPerIP:               10000,
		FileHandlesGlobal:              1000000,
		CleanupInterval:                5 * time.Minute,
	}
}

// TokenBucket implements a token bucket rate limiter
type TokenBucket struct {
	mu           sync.Mutex
	tokens       float64
	maxTokens    float64
	refillRate   float64 // tokens per second
	lastRefill   time.Time
}

// NewTokenBucket creates a new token bucket
func NewTokenBucket(rate float64, burst int) *TokenBucket {
	return &TokenBucket{
		tokens:     float64(burst),
		maxTokens:  float64(burst),
		refillRate: rate,
		lastRefill: time.Now(),
	}
}

// Allow checks if a request can proceed and consumes a token if so
func (tb *TokenBucket) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	// Refill tokens based on elapsed time
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.tokens += elapsed * tb.refillRate

	// Cap at max tokens
	if tb.tokens > tb.maxTokens {
		tb.tokens = tb.maxTokens
	}

	tb.lastRefill = now

	// Check if we have tokens available
	if tb.tokens >= 1.0 {
		tb.tokens -= 1.0
		return true
	}

	return false
}

// AllowN checks if N requests can proceed and consumes N tokens if so
func (tb *TokenBucket) AllowN(n int) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	// Refill tokens based on elapsed time
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.tokens += elapsed * tb.refillRate

	// Cap at max tokens
	if tb.tokens > tb.maxTokens {
		tb.tokens = tb.maxTokens
	}

	tb.lastRefill = now

	// Check if we have enough tokens
	if tb.tokens >= float64(n) {
		tb.tokens -= float64(n)
		return true
	}

	return false
}

// Tokens returns the current token count (for testing/metrics)
func (tb *TokenBucket) Tokens() float64 {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	// Refill tokens based on elapsed time
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tokens := tb.tokens + elapsed*tb.refillRate

	if tokens > tb.maxTokens {
		tokens = tb.maxTokens
	}

	return tokens
}

// SlidingWindow implements a sliding window rate limiter
type SlidingWindow struct {
	mu        sync.Mutex
	window    time.Duration
	maxCount  int
	requests  []time.Time
}

// NewSlidingWindow creates a new sliding window rate limiter
func NewSlidingWindow(window time.Duration, maxCount int) *SlidingWindow {
	return &SlidingWindow{
		window:   window,
		maxCount: maxCount,
		requests: make([]time.Time, 0, maxCount+1),
	}
}

// Allow checks if a request can proceed
func (sw *SlidingWindow) Allow() bool {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-sw.window)

	// Remove old requests outside the window
	validRequests := sw.requests[:0]
	for _, t := range sw.requests {
		if t.After(cutoff) {
			validRequests = append(validRequests, t)
		}
	}
	sw.requests = validRequests

	// Check if we're under the limit
	if len(sw.requests) < sw.maxCount {
		sw.requests = append(sw.requests, now)
		return true
	}

	return false
}

// Count returns the current request count in the window
func (sw *SlidingWindow) Count() int {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-sw.window)

	count := 0
	for _, t := range sw.requests {
		if t.After(cutoff) {
			count++
		}
	}

	return count
}

// PerIPLimiter manages rate limiters per IP address
type PerIPLimiter struct {
	mu       sync.RWMutex
	limiters map[string]*TokenBucket
	rate     float64
	burst    int
	lastCleanup time.Time
	cleanupInterval time.Duration
}

// NewPerIPLimiter creates a new per-IP rate limiter
func NewPerIPLimiter(rate float64, burst int, cleanupInterval time.Duration) *PerIPLimiter {
	return &PerIPLimiter{
		limiters: make(map[string]*TokenBucket),
		rate:     rate,
		burst:    burst,
		lastCleanup: time.Now(),
		cleanupInterval: cleanupInterval,
	}
}

// Allow checks if a request from the given IP can proceed
func (pl *PerIPLimiter) Allow(ip string) bool {
	pl.mu.Lock()

	// Periodic cleanup of old entries
	if time.Since(pl.lastCleanup) > pl.cleanupInterval {
		pl.cleanup()
		pl.lastCleanup = time.Now()
	}

	limiter, exists := pl.limiters[ip]
	if !exists {
		limiter = NewTokenBucket(pl.rate, pl.burst)
		pl.limiters[ip] = limiter
	}
	pl.mu.Unlock()

	return limiter.Allow()
}

// cleanup removes limiters that are at max capacity (inactive)
func (pl *PerIPLimiter) cleanup() {
	for ip, limiter := range pl.limiters {
		// Remove limiters that are full (haven't been used recently)
		if limiter.Tokens() >= float64(pl.burst) {
			delete(pl.limiters, ip)
		}
	}
}

// GetStats returns statistics about the limiter
func (pl *PerIPLimiter) GetStats() map[string]float64 {
	pl.mu.RLock()
	defer pl.mu.RUnlock()

	stats := make(map[string]float64)
	for ip, limiter := range pl.limiters {
		stats[ip] = limiter.Tokens()
	}
	return stats
}

// OperationType represents different NFS operation types for rate limiting
type OperationType string

const (
	OpTypeReadLarge  OperationType = "read_large"  // READ >64KB
	OpTypeWriteLarge OperationType = "write_large" // WRITE >64KB
	OpTypeReaddir    OperationType = "readdir"     // READDIR
	OpTypeMount      OperationType = "mount"       // MOUNT operations
)

// PerOperationLimiter manages rate limiters per operation type per IP
type PerOperationLimiter struct {
	mu       sync.RWMutex
	limiters map[string]map[OperationType]*TokenBucket
	rates    map[OperationType]float64
	bursts   map[OperationType]int
	lastCleanup time.Time
	cleanupInterval time.Duration
}

// NewPerOperationLimiter creates a new per-operation rate limiter
func NewPerOperationLimiter(config RateLimiterConfig) *PerOperationLimiter {
	rates := map[OperationType]float64{
		OpTypeReadLarge:  float64(config.ReadLargeOpsPerSecond),
		OpTypeWriteLarge: float64(config.WriteLargeOpsPerSecond),
		OpTypeReaddir:    float64(config.ReaddirOpsPerSecond),
		OpTypeMount:      float64(config.MountOpsPerMinute) / 60.0, // Convert to per-second
	}

	bursts := map[OperationType]int{
		OpTypeReadLarge:  10,
		OpTypeWriteLarge: 5,
		OpTypeReaddir:    5,
		OpTypeMount:      2,
	}

	return &PerOperationLimiter{
		limiters: make(map[string]map[OperationType]*TokenBucket),
		rates:    rates,
		bursts:   bursts,
		lastCleanup: time.Now(),
		cleanupInterval: config.CleanupInterval,
	}
}

// Allow checks if an operation from the given IP can proceed
func (pol *PerOperationLimiter) Allow(ip string, opType OperationType) bool {
	pol.mu.Lock()

	// Periodic cleanup
	if time.Since(pol.lastCleanup) > pol.cleanupInterval {
		pol.cleanup()
		pol.lastCleanup = time.Now()
	}

	ipLimiters, exists := pol.limiters[ip]
	if !exists {
		ipLimiters = make(map[OperationType]*TokenBucket)
		pol.limiters[ip] = ipLimiters
	}

	limiter, exists := ipLimiters[opType]
	if !exists {
		rate := pol.rates[opType]
		burst := pol.bursts[opType]
		limiter = NewTokenBucket(rate, burst)
		ipLimiters[opType] = limiter
	}
	pol.mu.Unlock()

	return limiter.Allow()
}

// cleanup removes old entries
func (pol *PerOperationLimiter) cleanup() {
	for ip, ipLimiters := range pol.limiters {
		allFull := true
		for opType, limiter := range ipLimiters {
			if limiter.Tokens() < float64(pol.bursts[opType]) {
				allFull = false
				break
			}
		}
		if allFull {
			delete(pol.limiters, ip)
		}
	}
}

// RateLimiter manages all rate limiting for the NFS server
type RateLimiter struct {
	config              RateLimiterConfig
	globalLimiter       *TokenBucket
	perIPLimiter        *PerIPLimiter
	perConnectionLimiter sync.Map // map[connID]*TokenBucket
	perOperationLimiter *PerOperationLimiter
	fileHandlesPerIP    sync.Map // map[IP]int
	fileHandlesGlobal   int
	fileHandlesMu       sync.Mutex
}

// NewRateLimiter creates a new rate limiter with the given configuration
func NewRateLimiter(config RateLimiterConfig) *RateLimiter {
	return &RateLimiter{
		config:        config,
		globalLimiter: NewTokenBucket(float64(config.GlobalRequestsPerSecond), config.GlobalRequestsPerSecond),
		perIPLimiter:  NewPerIPLimiter(float64(config.PerIPRequestsPerSecond), config.PerIPBurstSize, config.CleanupInterval),
		perOperationLimiter: NewPerOperationLimiter(config),
	}
}

// AllowRequest checks if a request should be allowed
func (rl *RateLimiter) AllowRequest(ip string, connID string) bool {
	// Check global limit first
	if !rl.globalLimiter.Allow() {
		return false
	}

	// Check per-IP limit
	if !rl.perIPLimiter.Allow(ip) {
		return false
	}

	// Check per-connection limit if enabled
	if rl.config.PerConnectionRequestsPerSecond > 0 {
		limiterInterface, exists := rl.perConnectionLimiter.Load(connID)
		var limiter *TokenBucket
		if !exists {
			limiter = NewTokenBucket(
				float64(rl.config.PerConnectionRequestsPerSecond),
				rl.config.PerConnectionBurstSize,
			)
			rl.perConnectionLimiter.Store(connID, limiter)
		} else {
			var ok bool
			limiter, ok = limiterInterface.(*TokenBucket)
			if !ok {
				// Type assertion failed, create a new limiter
				limiter = NewTokenBucket(
					float64(rl.config.PerConnectionRequestsPerSecond),
					rl.config.PerConnectionBurstSize,
				)
				rl.perConnectionLimiter.Store(connID, limiter)
			}
		}

		if !limiter.Allow() {
			return false
		}
	}

	return true
}

// AllowOperation checks if a specific operation type should be allowed
func (rl *RateLimiter) AllowOperation(ip string, opType OperationType) bool {
	return rl.perOperationLimiter.Allow(ip, opType)
}

// AllocateFileHandle attempts to allocate a file handle for an IP
func (rl *RateLimiter) AllocateFileHandle(ip string) bool {
	rl.fileHandlesMu.Lock()
	defer rl.fileHandlesMu.Unlock()

	// Check global limit
	if rl.config.FileHandlesGlobal > 0 && rl.fileHandlesGlobal >= rl.config.FileHandlesGlobal {
		return false
	}

	// Check per-IP limit
	if rl.config.FileHandlesPerIP > 0 {
		countInterface, _ := rl.fileHandlesPerIP.LoadOrStore(ip, 0)
		count := countInterface.(int)
		if count >= rl.config.FileHandlesPerIP {
			return false
		}
		rl.fileHandlesPerIP.Store(ip, count+1)
	}

	rl.fileHandlesGlobal++
	return true
}

// ReleaseFileHandle releases a file handle for an IP
func (rl *RateLimiter) ReleaseFileHandle(ip string) {
	rl.fileHandlesMu.Lock()
	defer rl.fileHandlesMu.Unlock()

	if rl.fileHandlesGlobal > 0 {
		rl.fileHandlesGlobal--
	}

	countInterface, exists := rl.fileHandlesPerIP.Load(ip)
	if exists {
		count := countInterface.(int)
		if count > 0 {
			rl.fileHandlesPerIP.Store(ip, count-1)
		}
	}
}

// CleanupConnection removes rate limiter for a connection
func (rl *RateLimiter) CleanupConnection(connID string) {
	rl.perConnectionLimiter.Delete(connID)
}

// GetStats returns rate limiter statistics
func (rl *RateLimiter) GetStats() map[string]interface{} {
	stats := make(map[string]interface{})

	stats["global_tokens"] = rl.globalLimiter.Tokens()
	stats["per_ip_stats"] = rl.perIPLimiter.GetStats()

	rl.fileHandlesMu.Lock()
	stats["file_handles_global"] = rl.fileHandlesGlobal
	rl.fileHandlesMu.Unlock()

	return stats
}
