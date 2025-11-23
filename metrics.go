package absnfs

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// NFSMetrics holds all metrics for the NFS server
type NFSMetrics struct {
	// Operation counts
	TotalOperations   uint64
	ReadOperations    uint64
	WriteOperations   uint64
	LookupOperations  uint64
	GetAttrOperations uint64
	CreateOperations  uint64
	RemoveOperations  uint64
	RenameOperations  uint64
	MkdirOperations   uint64
	RmdirOperations   uint64
	ReaddirOperations uint64
	AccessOperations  uint64

	// Latency metrics
	AvgReadLatency  time.Duration
	AvgWriteLatency time.Duration
	MaxReadLatency  time.Duration
	MaxWriteLatency time.Duration
	P95ReadLatency  time.Duration
	P95WriteLatency time.Duration

	// Cache metrics
	CacheHitRate        float64
	ReadAheadHitRate    float64
	AttrCacheSize       int
	AttrCacheCapacity   int
	ReadAheadBufferSize int64
	DirCacheHitRate     float64

	// Connection metrics
	ActiveConnections    int
	TotalConnections     uint64
	RejectedConnections  uint64

	// TLS metrics
	TLSHandshakes           uint64 // Successful TLS handshakes
	TLSHandshakeFailures    uint64 // Failed TLS handshakes
	TLSClientCertProvided   uint64 // Connections with client certificates
	TLSClientCertValidated  uint64 // Successfully validated client certificates
	TLSClientCertRejected   uint64 // Rejected client certificates
	TLSSessionReused        uint64 // TLS session resumptions
	TLSVersion12            uint64 // Connections using TLS 1.2
	TLSVersion13            uint64 // Connections using TLS 1.3

	// Error metrics
	ErrorCount       uint64
	AuthFailures     uint64
	AccessViolations uint64
	StaleHandles     uint64
	ResourceErrors   uint64
	RateLimitExceeded uint64

	// Internal metrics for calculating averages and percentiles
	readLatencies  []time.Duration
	writeLatencies []time.Duration
	
	// Time-based metrics
	StartTime time.Time
	UptimeSeconds int64
}

// MetricsCollector handles collecting and aggregating metrics
type MetricsCollector struct {
	mutex          sync.RWMutex
	metrics        NFSMetrics
	attrCacheHits  uint64
	attrCacheMisses uint64
	readAheadHits  uint64
	readAheadMisses uint64
	dirCacheHits   uint64
	dirCacheMisses uint64
	
	// For latency tracking
	latencyMutex   sync.Mutex
	readLatencies  []time.Duration
	writeLatencies []time.Duration
	
	// Maximum number of latency samples to keep
	maxLatencySamples int
	
	// Reference to server components for gathering metrics
	server         *AbsfsNFS
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector(server *AbsfsNFS) *MetricsCollector {
	return &MetricsCollector{
		server:           server,
		maxLatencySamples: 1000, // Keep the last 1000 latency samples
		readLatencies:    make([]time.Duration, 0, 1000),
		writeLatencies:   make([]time.Duration, 0, 1000),
		metrics: NFSMetrics{
			StartTime: time.Now(),
		},
	}
}

// IncrementOperationCount increments the count for the specified operation type
func (m *MetricsCollector) IncrementOperationCount(opType string) {
	atomic.AddUint64(&m.metrics.TotalOperations, 1)
	
	switch opType {
	case "READ":
		atomic.AddUint64(&m.metrics.ReadOperations, 1)
	case "WRITE":
		atomic.AddUint64(&m.metrics.WriteOperations, 1)
	case "LOOKUP":
		atomic.AddUint64(&m.metrics.LookupOperations, 1)
	case "GETATTR":
		atomic.AddUint64(&m.metrics.GetAttrOperations, 1)
	case "CREATE":
		atomic.AddUint64(&m.metrics.CreateOperations, 1)
	case "REMOVE":
		atomic.AddUint64(&m.metrics.RemoveOperations, 1)
	case "RENAME":
		atomic.AddUint64(&m.metrics.RenameOperations, 1)
	case "MKDIR":
		atomic.AddUint64(&m.metrics.MkdirOperations, 1)
	case "RMDIR":
		atomic.AddUint64(&m.metrics.RmdirOperations, 1)
	case "READDIR":
		atomic.AddUint64(&m.metrics.ReaddirOperations, 1)
	case "ACCESS":
		atomic.AddUint64(&m.metrics.AccessOperations, 1)
	}
}

// RecordLatency records the latency for an operation
func (m *MetricsCollector) RecordLatency(opType string, duration time.Duration) {
	m.latencyMutex.Lock()
	defer m.latencyMutex.Unlock()
	
	switch opType {
	case "READ":
		// Update max latency atomically
		for {
			current := atomic.LoadInt64((*int64)(&m.metrics.MaxReadLatency))
			if duration.Nanoseconds() <= current {
				break // No need to update
			}
			if atomic.CompareAndSwapInt64((*int64)(&m.metrics.MaxReadLatency), current, duration.Nanoseconds()) {
				break // Successfully updated
			}
		}
		
		// Add to latency samples for percentile calculation
		m.readLatencies = append(m.readLatencies, duration)
		if len(m.readLatencies) > m.maxLatencySamples {
			// Remove oldest entry
			m.readLatencies = m.readLatencies[1:]
		}
		
		// Update average latency
		if len(m.readLatencies) > 0 {
			var sum time.Duration
			for _, d := range m.readLatencies {
				sum += d
			}
			m.metrics.AvgReadLatency = sum / time.Duration(len(m.readLatencies))
			
			// Calculate 95th percentile
			if len(m.readLatencies) >= 20 { // Need enough samples for meaningful percentile
				sorted := make([]time.Duration, len(m.readLatencies))
				copy(sorted, m.readLatencies)
				sortDurations(sorted)
				idx := int(float64(len(sorted)) * 0.95)
				m.metrics.P95ReadLatency = sorted[idx]
			}
		}
		
	case "WRITE":
		// Update max latency atomically
		for {
			current := atomic.LoadInt64((*int64)(&m.metrics.MaxWriteLatency))
			if duration.Nanoseconds() <= current {
				break // No need to update
			}
			if atomic.CompareAndSwapInt64((*int64)(&m.metrics.MaxWriteLatency), current, duration.Nanoseconds()) {
				break // Successfully updated
			}
		}
		
		// Add to latency samples for percentile calculation
		m.writeLatencies = append(m.writeLatencies, duration)
		if len(m.writeLatencies) > m.maxLatencySamples {
			// Remove oldest entry
			m.writeLatencies = m.writeLatencies[1:]
		}
		
		// Update average latency
		if len(m.writeLatencies) > 0 {
			var sum time.Duration
			for _, d := range m.writeLatencies {
				sum += d
			}
			m.metrics.AvgWriteLatency = sum / time.Duration(len(m.writeLatencies))
			
			// Calculate 95th percentile
			if len(m.writeLatencies) >= 20 { // Need enough samples for meaningful percentile
				sorted := make([]time.Duration, len(m.writeLatencies))
				copy(sorted, m.writeLatencies)
				sortDurations(sorted)
				idx := int(float64(len(sorted)) * 0.95)
				m.metrics.P95WriteLatency = sorted[idx]
			}
		}
	}
}

// RecordAttrCacheHit records a hit in the attribute cache
func (m *MetricsCollector) RecordAttrCacheHit() {
	atomic.AddUint64(&m.attrCacheHits, 1)
	m.updateCacheHitRate()
}

// RecordAttrCacheMiss records a miss in the attribute cache
func (m *MetricsCollector) RecordAttrCacheMiss() {
	atomic.AddUint64(&m.attrCacheMisses, 1)
	m.updateCacheHitRate()
}

// RecordReadAheadHit records a hit in the read-ahead buffer
func (m *MetricsCollector) RecordReadAheadHit() {
	atomic.AddUint64(&m.readAheadHits, 1)
	m.updateReadAheadHitRate()
}

// RecordReadAheadMiss records a miss in the read-ahead buffer
func (m *MetricsCollector) RecordReadAheadMiss() {
	atomic.AddUint64(&m.readAheadMisses, 1)
	m.updateReadAheadHitRate()
}

// RecordDirCacheHit records a hit in the directory cache
func (m *MetricsCollector) RecordDirCacheHit() {
	atomic.AddUint64(&m.dirCacheHits, 1)
	m.updateDirCacheHitRate()
}

// RecordDirCacheMiss records a miss in the directory cache
func (m *MetricsCollector) RecordDirCacheMiss() {
	atomic.AddUint64(&m.dirCacheMisses, 1)
	m.updateDirCacheHitRate()
}

// RecordError records an error
func (m *MetricsCollector) RecordError(errorType string) {
	atomic.AddUint64(&m.metrics.ErrorCount, 1)

	switch errorType {
	case "AUTH":
		atomic.AddUint64(&m.metrics.AuthFailures, 1)
	case "ACCESS":
		atomic.AddUint64(&m.metrics.AccessViolations, 1)
	case "STALE":
		atomic.AddUint64(&m.metrics.StaleHandles, 1)
	case "RESOURCE":
		atomic.AddUint64(&m.metrics.ResourceErrors, 1)
	case "RATELIMIT":
		atomic.AddUint64(&m.metrics.RateLimitExceeded, 1)
	}
}

// RecordRateLimitExceeded records a rate limit rejection
func (m *MetricsCollector) RecordRateLimitExceeded() {
	atomic.AddUint64(&m.metrics.RateLimitExceeded, 1)
}

// RecordConnection records a new connection
func (m *MetricsCollector) RecordConnection() {
	atomic.AddUint64(&m.metrics.TotalConnections, 1)
	
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.metrics.ActiveConnections++
}

// RecordConnectionClosed records a closed connection
func (m *MetricsCollector) RecordConnectionClosed() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if m.metrics.ActiveConnections > 0 {
		m.metrics.ActiveConnections--
	}
}

// RecordRejectedConnection records a rejected connection
func (m *MetricsCollector) RecordRejectedConnection() {
	atomic.AddUint64(&m.metrics.RejectedConnections, 1)
}

// RecordTLSHandshake records a successful TLS handshake
func (m *MetricsCollector) RecordTLSHandshake() {
	atomic.AddUint64(&m.metrics.TLSHandshakes, 1)
}

// RecordTLSHandshakeFailure records a failed TLS handshake
func (m *MetricsCollector) RecordTLSHandshakeFailure() {
	atomic.AddUint64(&m.metrics.TLSHandshakeFailures, 1)
}

// RecordTLSClientCert records a connection with a client certificate
func (m *MetricsCollector) RecordTLSClientCert(validated bool) {
	atomic.AddUint64(&m.metrics.TLSClientCertProvided, 1)
	if validated {
		atomic.AddUint64(&m.metrics.TLSClientCertValidated, 1)
	} else {
		atomic.AddUint64(&m.metrics.TLSClientCertRejected, 1)
	}
}

// RecordTLSSessionReused records a TLS session resumption
func (m *MetricsCollector) RecordTLSSessionReused() {
	atomic.AddUint64(&m.metrics.TLSSessionReused, 1)
}

// RecordTLSVersion records the TLS version used for a connection
func (m *MetricsCollector) RecordTLSVersion(version uint16) {
	switch version {
	case 0x0303: // TLS 1.2
		atomic.AddUint64(&m.metrics.TLSVersion12, 1)
	case 0x0304: // TLS 1.3
		atomic.AddUint64(&m.metrics.TLSVersion13, 1)
	}
}

// updateCacheHitRate updates the attribute cache hit rate
func (m *MetricsCollector) updateCacheHitRate() {
	hits := atomic.LoadUint64(&m.attrCacheHits)
	misses := atomic.LoadUint64(&m.attrCacheMisses)
	total := hits + misses
	
	if total > 0 {
		hitRate := float64(hits) / float64(total)
		
		m.mutex.Lock()
		m.metrics.CacheHitRate = hitRate
		m.mutex.Unlock()
	}
}

// updateReadAheadHitRate updates the read-ahead buffer hit rate
func (m *MetricsCollector) updateReadAheadHitRate() {
	hits := atomic.LoadUint64(&m.readAheadHits)
	misses := atomic.LoadUint64(&m.readAheadMisses)
	total := hits + misses
	
	if total > 0 {
		hitRate := float64(hits) / float64(total)
		
		m.mutex.Lock()
		m.metrics.ReadAheadHitRate = hitRate
		m.mutex.Unlock()
	}
}

// updateDirCacheHitRate updates the directory cache hit rate
func (m *MetricsCollector) updateDirCacheHitRate() {
	hits := atomic.LoadUint64(&m.dirCacheHits)
	misses := atomic.LoadUint64(&m.dirCacheMisses)
	total := hits + misses
	
	if total > 0 {
		hitRate := float64(hits) / float64(total)
		
		m.mutex.Lock()
		m.metrics.DirCacheHitRate = hitRate
		m.mutex.Unlock()
	}
}

// updateCacheMetrics updates various cache-related metrics
func (m *MetricsCollector) updateCacheMetrics() {
	if m.server == nil || m.server.attrCache == nil || m.server.readBuf == nil {
		return
	}
	
	// Get attribute cache metrics
	attrSize, attrCapacity := m.server.attrCache.Stats()
	
	// Get read-ahead buffer size
	readAheadSize := m.server.readBuf.Size()
	
	m.mutex.Lock()
	defer m.mutex.Unlock()
	
	m.metrics.AttrCacheSize = attrSize
	m.metrics.AttrCacheCapacity = attrCapacity
	m.metrics.ReadAheadBufferSize = readAheadSize
}

// GetMetrics returns a snapshot of the current metrics
func (m *MetricsCollector) GetMetrics() NFSMetrics {
	// Update dynamic metrics before returning
	m.updateCacheMetrics()
	
	// Update uptime
	m.mutex.Lock()
	m.metrics.UptimeSeconds = int64(time.Since(m.metrics.StartTime).Seconds())
	m.mutex.Unlock()
	
	// Return a copy of the metrics to ensure thread safety
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	
	// Create a copy of the metrics
	metricsCopy := m.metrics
	
	return metricsCopy
}

// sortDurations is a helper function to sort duration slices using efficient O(n log n) algorithm
func sortDurations(durations []time.Duration) {
	// Use Go's built-in sort.Slice which uses an optimized O(n log n) algorithm
	// This is significantly faster than bubble sort, especially for large datasets
	// For 1000 samples: O(n log n) ≈ 10,000 operations vs O(n²) ≈ 1,000,000 operations
	sort.Slice(durations, func(i, j int) bool {
		return durations[i] < durations[j]
	})
}

// IsHealthy checks if the server is in a healthy state
func (m *MetricsCollector) IsHealthy() bool {
	// Basic health check logic
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	
	// Consider the server unhealthy if:
	
	// 1. Error rate is too high (more than 50% of operations in the last period)
	errorRate := float64(m.metrics.ErrorCount) / float64(m.metrics.TotalOperations + 1) // Add 1 to avoid division by zero
	if errorRate > 0.5 {
		return false
	}
	
	// 2. Read/write latency is too high (more than 5 seconds for 95th percentile)
	maxAllowedLatency := 5 * time.Second
	if m.metrics.P95ReadLatency > maxAllowedLatency || m.metrics.P95WriteLatency > maxAllowedLatency {
		return false
	}
	
	// Otherwise consider the server healthy
	return true
}