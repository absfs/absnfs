package absnfs

import (
	"time"
)

// initMetrics initializes the metrics collector for the server
func (n *AbsfsNFS) initMetrics() {
	n.metrics = NewMetricsCollector(n)
}

// GetMetrics returns a snapshot of the current NFS server metrics
func (n *AbsfsNFS) GetMetrics() NFSMetrics {
	if n.metrics == nil {
		// If metrics collection is not initialized, return an empty metrics object
		return NFSMetrics{
			StartTime: time.Now(),
		}
	}
	
	return n.metrics.GetMetrics()
}

// IsHealthy returns whether the server is in a healthy state
func (n *AbsfsNFS) IsHealthy() bool {
	if n.metrics == nil {
		// If metrics collection is not initialized, assume server is healthy
		return true
	}
	
	return n.metrics.IsHealthy()
}

// RecordOperationStart records the start of an NFS operation for metrics tracking
// Returns a function that should be called when the operation completes
func (n *AbsfsNFS) RecordOperationStart(opType string) func(err error) {
	if n.metrics == nil {
		// If metrics collection is not initialized, return a no-op function
		return func(err error) {}
	}
	
	// Record operation count
	n.metrics.IncrementOperationCount(opType)
	
	// Record start time for latency tracking
	startTime := time.Now()
	
	// Return a function that will be called when the operation completes
	return func(err error) {
		// Record latency
		if opType == "READ" || opType == "WRITE" {
			n.metrics.RecordLatency(opType, time.Since(startTime))
		}
		
		// Record error if any
		if err != nil {
			// Determine error type
			errorType := "UNKNOWN"
			
			// This is a simplified example - in a real implementation, you would
			// examine the error to determine its type more precisely
			if n.options.ReadOnly && opType == "WRITE" {
				errorType = "ACCESS"
			} else if isStaleFileHandle(err) {
				errorType = "STALE"
			} else if isAuthError(err) {
				errorType = "AUTH"
			} else if isResourceError(err) {
				errorType = "RESOURCE"
			}
			
			n.metrics.RecordError(errorType)
		}
	}
}

// isStaleFileHandle checks if an error is related to a stale file handle
func isStaleFileHandle(err error) bool {
	// In a real implementation, you would check the error type or message
	// This is a placeholder
	return false
}

// isAuthError checks if an error is related to authentication
func isAuthError(err error) bool {
	// In a real implementation, you would check the error type or message
	// This is a placeholder
	return false
}

// isResourceError checks if an error is related to resource limits
func isResourceError(err error) bool {
	// In a real implementation, you would check the error type or message
	// This is a placeholder
	return false
}

// RecordAttrCacheHit records a hit in the attribute cache
func (n *AbsfsNFS) RecordAttrCacheHit() {
	if n.metrics == nil {
		return
	}
	n.metrics.RecordAttrCacheHit()
}

// RecordAttrCacheMiss records a miss in the attribute cache
func (n *AbsfsNFS) RecordAttrCacheMiss() {
	if n.metrics == nil {
		return
	}
	n.metrics.RecordAttrCacheMiss()
}

// RecordReadAheadHit records a hit in the read-ahead buffer
func (n *AbsfsNFS) RecordReadAheadHit() {
	if n.metrics == nil {
		return
	}
	n.metrics.RecordReadAheadHit()
}

// RecordReadAheadMiss records a miss in the read-ahead buffer
func (n *AbsfsNFS) RecordReadAheadMiss() {
	if n.metrics == nil {
		return
	}
	n.metrics.RecordReadAheadMiss()
}

// RecordDirCacheHit records a hit in the directory cache
func (n *AbsfsNFS) RecordDirCacheHit() {
	if n.metrics == nil {
		return
	}
	n.metrics.RecordDirCacheHit()
}

// RecordDirCacheMiss records a miss in the directory cache
func (n *AbsfsNFS) RecordDirCacheMiss() {
	if n.metrics == nil {
		return
	}
	n.metrics.RecordDirCacheMiss()
}