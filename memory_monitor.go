package absnfs

import (
	"runtime"
	"sync/atomic"
	"time"
)

// memoryStats holds the current memory usage statistics
type memoryStats struct {
	// Total system memory (bytes)
	totalMemory uint64
	// Current system memory usage (bytes)
	usedMemory uint64
	// Memory usage as a fraction of total memory (0.0-1.0)
	usageFraction float64
	// Is the system under memory pressure?
	underPressure bool
	// Time when stats were last updated
	lastUpdated time.Time
}

// MemoryMonitor tracks system memory usage and manages memory pressure responses
type MemoryMonitor struct {
	// AbsfsNFS instance to adjust when memory pressure is detected
	nfs *AbsfsNFS
	// Memory usage stats
	stats memoryStats
	// Is monitoring active?
	active int32
	// Channel to signal monitor to stop
	stopCh chan struct{}
}

// NewMemoryMonitor creates a new memory monitor for the given AbsfsNFS instance
func NewMemoryMonitor(nfs *AbsfsNFS) *MemoryMonitor {
	return &MemoryMonitor{
		nfs:    nfs,
		stopCh: make(chan struct{}),
	}
}

// Start begins monitoring system memory usage at the specified interval
func (m *MemoryMonitor) Start(interval time.Duration) {
	// Use atomic CAS to ensure we only start once
	if !atomic.CompareAndSwapInt32(&m.active, 0, 1) {
		return // Monitor is already running
	}

	// Update stats immediately
	m.updateStats()

	// Start background monitoring
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				m.checkMemoryPressure()
			case <-m.stopCh:
				return
			}
		}
	}()
}

// Stop ends the memory monitoring
func (m *MemoryMonitor) Stop() {
	if atomic.CompareAndSwapInt32(&m.active, 1, 0) {
		close(m.stopCh)
	}
}

// IsActive returns true if monitoring is active
func (m *MemoryMonitor) IsActive() bool {
	return atomic.LoadInt32(&m.active) == 1
}

// updateStats refreshes memory usage statistics
func (m *MemoryMonitor) updateStats() {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// Update stats
	m.stats.totalMemory = memStats.Sys
	m.stats.usedMemory = memStats.HeapAlloc
	if m.stats.totalMemory > 0 {
		m.stats.usageFraction = float64(m.stats.usedMemory) / float64(m.stats.totalMemory)
	} else {
		m.stats.usageFraction = 0
	}
	m.stats.lastUpdated = time.Now()
}

// checkMemoryPressure checks if the system is under memory pressure
// and takes appropriate action if necessary
func (m *MemoryMonitor) checkMemoryPressure() {
	// Update memory stats
	m.updateStats()

	// Get threshold values from export options
	highWatermark := m.nfs.options.MemoryHighWatermark
	lowWatermark := m.nfs.options.MemoryLowWatermark

	// Check if we're under pressure (usage exceeds high watermark)
	if m.stats.usageFraction >= highWatermark && !m.stats.underPressure {
		// Transition to pressure state
		m.stats.underPressure = true
		m.handleMemoryPressure()
	} else if m.stats.usageFraction <= lowWatermark && m.stats.underPressure {
		// Transition out of pressure state
		m.stats.underPressure = false
	}
}

// handleMemoryPressure takes action to reduce memory usage
func (m *MemoryMonitor) handleMemoryPressure() {
	// Log the memory pressure event
	m.nfs.logger.Printf("Memory pressure detected: usage at %.2f%% (threshold: %.2f%%)",
		m.stats.usageFraction*100, m.nfs.options.MemoryHighWatermark*100)

	// Calculate reduction factor based on current usage and low watermark target
	// We want to reduce to reach the low watermark
	target := m.nfs.options.MemoryLowWatermark
	current := m.stats.usageFraction
	reductionFactor := 1.0 - (target / current)

	// Reduce cache sizes to alleviate memory pressure
	m.reduceCacheSizes(reductionFactor)
}

// reduceCacheSizes reduces the size of various caches to free memory
func (m *MemoryMonitor) reduceCacheSizes(reductionFactor float64) {
	// Ensure the reduction factor is reasonable
	if reductionFactor < 0.1 {
		reductionFactor = 0.1 // Minimum 10% reduction
	}
	if reductionFactor > 0.9 {
		reductionFactor = 0.9 // Maximum 90% reduction
	}

	// Get current cache settings
	attrCacheSize := m.nfs.attrCache.MaxSize()
	fileCount, memoryUsage := m.nfs.readBuf.Stats()

	// Calculate new reduced sizes
	newAttrCacheSize := int(float64(attrCacheSize) * (1.0 - reductionFactor))
	newReadAheadMaxFiles := int(float64(m.nfs.options.ReadAheadMaxFiles) * (1.0 - reductionFactor))
	newReadAheadMaxMemory := int64(float64(m.nfs.options.ReadAheadMaxMemory) * (1.0 - reductionFactor))

	// Ensure minimum values
	if newAttrCacheSize < 100 {
		newAttrCacheSize = 100 // Minimum 100 entries
	}
	if newReadAheadMaxFiles < 10 {
		newReadAheadMaxFiles = 10 // Minimum 10 files
	}
	if newReadAheadMaxMemory < 1048576 {
		newReadAheadMaxMemory = 1048576 // Minimum 1MB
	}

	// Log the changes being made
	m.nfs.logger.Printf("Reducing cache sizes due to memory pressure:")
	m.nfs.logger.Printf(" - Attribute cache: %d → %d entries", attrCacheSize, newAttrCacheSize)
	m.nfs.logger.Printf(" - Read-ahead files: %d → %d (currently using %d)", 
		m.nfs.options.ReadAheadMaxFiles, newReadAheadMaxFiles, fileCount)
	m.nfs.logger.Printf(" - Read-ahead memory: %d → %d bytes (currently using %d)", 
		m.nfs.options.ReadAheadMaxMemory, newReadAheadMaxMemory, memoryUsage)

	// Create new attribute cache with reduced size
	newAttrCache := NewAttrCache(m.nfs.options.AttrCacheTimeout, newAttrCacheSize)
	
	// Replace the old cache with the new one
	// This effectively clears the cache but maintains the same timeout setting
	m.nfs.attrCache = newAttrCache

	// Update read-ahead buffer configuration
	m.nfs.readBuf.Configure(newReadAheadMaxFiles, newReadAheadMaxMemory)

	// Run garbage collection to reclaim memory
	runtime.GC()
}

// GetMemoryStats returns a copy of the current memory statistics
func (m *MemoryMonitor) GetMemoryStats() memoryStats {
	// Update stats before returning
	m.updateStats()
	return m.stats
}