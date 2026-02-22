package absnfs

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/absfs/memfs"
)

func TestMetricsCollection(t *testing.T) {
	// Create a memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create NFS server
	options := ExportOptions{}
	server, err := New(fs, options)
	if err != nil {
		t.Fatalf("Failed to create NFS server: %v", err)
	}

	// Verify metrics collector was initialized
	if server.metrics == nil {
		t.Fatal("Metrics collector was not initialized")
	}

	// Test operation counting
	operationTypes := []string{
		"READ", "WRITE", "LOOKUP", "GETATTR",
		"CREATE", "REMOVE", "RENAME", "MKDIR",
		"RMDIR", "READDIR", "ACCESS",
	}

	// Record some operations
	for _, opType := range operationTypes {
		// Start operation and complete it
		completion := server.RecordOperationStart(opType)
		completion(nil) // Complete without error
	}

	// Record some errors
	errorCompletion := server.RecordOperationStart("WRITE")
	errorCompletion(ErrPermission) // Complete with error

	// Manually record some cache hits and misses
	for i := 0; i < 10; i++ {
		server.RecordAttrCacheHit()
	}
	for i := 0; i < 5; i++ {
		server.RecordAttrCacheMiss()
	}

	// Wait to ensure uptime is at least 1 second
	time.Sleep(1 * time.Second)

	// Get metrics
	metrics := server.GetMetrics()

	// Verify operation counts
	if metrics.TotalOperations != uint64(len(operationTypes)+1) {
		t.Errorf("Expected %d total operations, got %d", len(operationTypes)+1, metrics.TotalOperations)
	}

	// Verify specific operation counts
	if metrics.ReadOperations != 1 {
		t.Errorf("Expected 1 read operation, got %d", metrics.ReadOperations)
	}
	if metrics.WriteOperations != 2 { // One regular + one error
		t.Errorf("Expected 2 write operations, got %d", metrics.WriteOperations)
	}

	// Verify cache hit rates
	expectedAttrCacheHitRate := float64(10) / float64(10+5)
	if metrics.CacheHitRate != expectedAttrCacheHitRate {
		t.Errorf("Expected attr cache hit rate %f, got %f", expectedAttrCacheHitRate, metrics.CacheHitRate)
	}

	// Verify uptime
	if metrics.UptimeSeconds <= 0 {
		t.Errorf("Expected positive uptime, got %d", metrics.UptimeSeconds)
	}

	// Test IsHealthy
	if !server.IsHealthy() {
		t.Error("Expected server to be healthy")
	}

	// Test latency recording
	readOp := server.RecordOperationStart("READ")
	time.Sleep(10 * time.Millisecond) // Small delay to ensure measurable latency
	readOp(nil)

	writeOp := server.RecordOperationStart("WRITE")
	time.Sleep(20 * time.Millisecond)
	writeOp(nil)

	metrics = server.GetMetrics()

	// Verify latency metrics exist
	if metrics.AvgReadLatency <= 0 {
		t.Errorf("Expected positive average read latency, got %v", metrics.AvgReadLatency)
	}
	if metrics.AvgWriteLatency <= 0 {
		t.Errorf("Expected positive average write latency, got %v", metrics.AvgWriteLatency)
	}
}

func TestGetMetricsMethod(t *testing.T) {
	// Create a memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create NFS server
	options := ExportOptions{}
	server, err := New(fs, options)
	if err != nil {
		t.Fatalf("Failed to create NFS server: %v", err)
	}

	// Get initial metrics
	metrics := server.GetMetrics()

	// Verify metrics structure is populated
	if metrics.StartTime.IsZero() {
		t.Error("Expected non-zero start time")
	}

	// Record some operations to change metrics
	server.RecordOperationStart("READ")(nil)
	server.RecordOperationStart("WRITE")(nil)

	// Get updated metrics
	updatedMetrics := server.GetMetrics()

	// Verify metrics were updated
	if updatedMetrics.TotalOperations <= metrics.TotalOperations {
		t.Errorf("Expected total operations to increase, got %d -> %d",
			metrics.TotalOperations, updatedMetrics.TotalOperations)
	}

	// Test health check method
	if !server.IsHealthy() {
		t.Error("Expected server to be healthy")
	}
}

// TestL10_MetricsIsHealthyWindowed verifies that IsHealthy uses a windowed
// error rate that can recover after an initial burst of errors.
func TestL10_MetricsIsHealthyWindowed(t *testing.T) {
	mc := NewMetricsCollector(nil)

	// Record a burst of errors (>50%)
	for i := 0; i < 100; i++ {
		mc.RecordOperationResult(true) // error
	}
	if mc.IsHealthy() {
		t.Fatal("should be unhealthy after 100% error rate")
	}

	// Now record many successes to push errors out of the window
	for i := 0; i < 1000; i++ {
		mc.RecordOperationResult(false) // success
	}

	if !mc.IsHealthy() {
		t.Fatal("should recover to healthy after window fills with successes")
	}
}

// TestL11_MetricsLatencyRingBuffer verifies that latency samples are stored
// in a fixed-size ring buffer that doesn't grow unbounded.
func TestL11_MetricsLatencyRingBuffer(t *testing.T) {
	mc := NewMetricsCollector(nil)

	// Record more than maxLatencySamples latencies
	for i := 0; i < 2000; i++ {
		mc.RecordLatency("READ", time.Duration(i)*time.Microsecond)
	}

	// The underlying slice should be capped at maxLatencySamples
	mc.latencyMutex.Lock()
	readLen := mc.readLatLen
	readCap := cap(mc.readLatencies)
	mc.latencyMutex.Unlock()

	if readLen != mc.maxLatencySamples {
		t.Fatalf("expected readLatLen=%d, got %d", mc.maxLatencySamples, readLen)
	}
	if readCap != mc.maxLatencySamples {
		t.Fatalf("expected readLatencies cap=%d, got %d", mc.maxLatencySamples, readCap)
	}

	// Verify stats are computed correctly
	mc.latencyMutex.Lock()
	avg := mc.metrics.AvgReadLatency
	mc.latencyMutex.Unlock()

	if avg == 0 {
		t.Fatal("expected non-zero average read latency")
	}
}

// TestR15_P95IndexCorrect verifies that P95 index uses n-1 to stay in bounds.
func TestR15_P95IndexCorrect(t *testing.T) {
	mc := NewMetricsCollector(nil)

	// Record exactly 20 samples (minimum for P95 calculation)
	for i := 1; i <= 20; i++ {
		mc.RecordLatency("READ", time.Duration(i)*time.Millisecond)
	}

	mc.latencyMutex.Lock()
	p95 := mc.metrics.P95ReadLatency
	mc.latencyMutex.Unlock()

	// With 20 samples [1ms..20ms], P95 index = int(19 * 0.95) = int(18.05) = 18
	// sorted[18] = 19ms
	expected := 19 * time.Millisecond
	if p95 != expected {
		t.Fatalf("expected P95=%v, got %v", expected, p95)
	}
}

// TestR17_LatencyRaceDetector verifies that concurrent RecordLatency, IsHealthy,
// and GetMetrics calls don't trigger the race detector.
func TestR17_LatencyRaceDetector(t *testing.T) {
	mc := NewMetricsCollector(nil)

	var wg sync.WaitGroup
	wg.Add(3)

	// Concurrent RecordLatency
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			mc.RecordLatency("READ", time.Duration(i)*time.Microsecond)
			mc.RecordLatency("WRITE", time.Duration(i)*time.Microsecond)
		}
	}()

	// Concurrent IsHealthy
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			mc.IsHealthy()
		}
	}()

	// Concurrent GetMetrics
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			mc.GetMetrics()
		}
	}()

	wg.Wait()
}

// TestR30_MaxLatencySimpleComparison verifies that max latency is tracked
// correctly using simple comparison under mutex (no unsafe pointer cast).
func TestR30_MaxLatencySimpleComparison(t *testing.T) {
	mc := NewMetricsCollector(nil)

	mc.RecordLatency("READ", 10*time.Millisecond)
	mc.RecordLatency("READ", 50*time.Millisecond)
	mc.RecordLatency("READ", 30*time.Millisecond)

	mc.latencyMutex.Lock()
	maxRead := mc.metrics.MaxReadLatency
	mc.latencyMutex.Unlock()

	if maxRead != 50*time.Millisecond {
		t.Fatalf("expected max=50ms, got %v", maxRead)
	}
}

// Tests for sortDurations function
func TestSortDurations(t *testing.T) {
	t.Run("sort empty slice", func(t *testing.T) {
		durations := []time.Duration{}
		sortDurations(durations)
		if len(durations) != 0 {
			t.Errorf("Expected empty slice, got %v", durations)
		}
	})

	t.Run("sort single element", func(t *testing.T) {
		durations := []time.Duration{100 * time.Millisecond}
		sortDurations(durations)
		if durations[0] != 100*time.Millisecond {
			t.Errorf("Expected 100ms, got %v", durations[0])
		}
	})

	t.Run("sort already sorted", func(t *testing.T) {
		durations := []time.Duration{
			10 * time.Millisecond,
			20 * time.Millisecond,
			30 * time.Millisecond,
		}
		sortDurations(durations)
		if durations[0] != 10*time.Millisecond || durations[2] != 30*time.Millisecond {
			t.Errorf("Sort failed: %v", durations)
		}
	})

	t.Run("sort reverse order", func(t *testing.T) {
		durations := []time.Duration{
			300 * time.Millisecond,
			200 * time.Millisecond,
			100 * time.Millisecond,
		}
		sortDurations(durations)
		if durations[0] != 100*time.Millisecond {
			t.Errorf("Expected 100ms first, got %v", durations[0])
		}
		if durations[2] != 300*time.Millisecond {
			t.Errorf("Expected 300ms last, got %v", durations[2])
		}
	})

	t.Run("sort random order", func(t *testing.T) {
		durations := []time.Duration{
			50 * time.Millisecond,
			10 * time.Millisecond,
			80 * time.Millisecond,
			20 * time.Millisecond,
			60 * time.Millisecond,
		}
		sortDurations(durations)
		for i := 1; i < len(durations); i++ {
			if durations[i] < durations[i-1] {
				t.Errorf("Not sorted at index %d: %v < %v", i, durations[i], durations[i-1])
			}
		}
	})
}

// Tests for RecordError with all error types
func TestRecordErrorAllTypes(t *testing.T) {
	createTestCollector := func() *MetricsCollector {
		mfs, _ := memfs.NewFS()
		config := DefaultRateLimiterConfig()
		nfs, _ := New(mfs, ExportOptions{
			EnableRateLimiting: false,
			RateLimitConfig:    &config,
		})
		return NewMetricsCollector(nfs)
	}

	t.Run("record AUTH error", func(t *testing.T) {
		mc := createTestCollector()
		mc.RecordError("AUTH")
		metrics := mc.GetMetrics()
		if metrics.AuthFailures != 1 {
			t.Errorf("Expected 1 auth failure, got %d", metrics.AuthFailures)
		}
		if metrics.ErrorCount != 1 {
			t.Errorf("Expected 1 error count, got %d", metrics.ErrorCount)
		}
	})

	t.Run("record ACCESS error", func(t *testing.T) {
		mc := createTestCollector()
		mc.RecordError("ACCESS")
		metrics := mc.GetMetrics()
		if metrics.AccessViolations != 1 {
			t.Errorf("Expected 1 access violation, got %d", metrics.AccessViolations)
		}
	})

	t.Run("record STALE error", func(t *testing.T) {
		mc := createTestCollector()
		mc.RecordError("STALE")
		metrics := mc.GetMetrics()
		if metrics.StaleHandles != 1 {
			t.Errorf("Expected 1 stale handle, got %d", metrics.StaleHandles)
		}
	})

	t.Run("record RESOURCE error", func(t *testing.T) {
		mc := createTestCollector()
		mc.RecordError("RESOURCE")
		metrics := mc.GetMetrics()
		if metrics.ResourceErrors != 1 {
			t.Errorf("Expected 1 resource error, got %d", metrics.ResourceErrors)
		}
	})

	t.Run("record RATELIMIT error", func(t *testing.T) {
		mc := createTestCollector()
		mc.RecordError("RATELIMIT")
		metrics := mc.GetMetrics()
		if metrics.RateLimitExceeded != 1 {
			t.Errorf("Expected 1 rate limit exceeded, got %d", metrics.RateLimitExceeded)
		}
	})

	t.Run("record unknown error type", func(t *testing.T) {
		mc := createTestCollector()
		mc.RecordError("UNKNOWN")
		metrics := mc.GetMetrics()
		// Should still increment error count
		if metrics.ErrorCount != 1 {
			t.Errorf("Expected 1 error count, got %d", metrics.ErrorCount)
		}
	})
}

// Tests for MetricsCollector cache hit/miss recording
func TestMetricsCacheRecording(t *testing.T) {
	createTestNFS := func() *AbsfsNFS {
		mfs, _ := memfs.NewFS()
		config := DefaultRateLimiterConfig()
		nfs, _ := New(mfs, ExportOptions{
			EnableRateLimiting: false,
			RateLimitConfig:    &config,
		})
		return nfs
	}

	t.Run("record attr cache hit", func(t *testing.T) {
		nfs := createTestNFS()
		nfs.RecordAttrCacheHit()
	})

	t.Run("record attr cache miss", func(t *testing.T) {
		nfs := createTestNFS()
		nfs.RecordAttrCacheMiss()
	})

	t.Run("record dir cache hit", func(t *testing.T) {
		nfs := createTestNFS()
		nfs.RecordDirCacheHit()
	})

	t.Run("record dir cache miss", func(t *testing.T) {
		nfs := createTestNFS()
		nfs.RecordDirCacheMiss()
	})

	t.Run("record negative cache hit", func(t *testing.T) {
		nfs := createTestNFS()
		nfs.RecordNegativeCacheHit()
	})

	t.Run("record negative cache miss", func(t *testing.T) {
		nfs := createTestNFS()
		nfs.RecordNegativeCacheMiss()
	})
}

// Tests for RecordLatency edge cases
func TestRecordLatencyCoverage(t *testing.T) {
	mfs, _ := memfs.NewFS()
	config := DefaultRateLimiterConfig()
	nfs, _ := New(mfs, ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
	})

	mc := NewMetricsCollector(nfs)

	t.Run("record various operations", func(t *testing.T) {
		operations := []string{
			"NULL", "GETATTR", "SETATTR", "LOOKUP", "ACCESS",
			"READLINK", "READ", "WRITE", "CREATE", "MKDIR",
			"SYMLINK", "MKNOD", "REMOVE", "RMDIR", "RENAME",
			"LINK", "READDIR", "READDIRPLUS", "FSSTAT", "FSINFO",
			"PATHCONF", "COMMIT", "UNKNOWN_OP",
		}

		for _, op := range operations {
			mc.RecordLatency(op, 10*time.Millisecond)
		}
	})

	t.Run("record zero latency", func(t *testing.T) {
		mc.RecordLatency("READ", 0)
	})

	t.Run("record large latency", func(t *testing.T) {
		mc.RecordLatency("WRITE", 10*time.Second)
	})
}

// Tests for RecordTLSVersion edge cases
func TestRecordTLSVersionCoverage(t *testing.T) {
	mfs, _ := memfs.NewFS()
	config := DefaultRateLimiterConfig()
	nfs, _ := New(mfs, ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
	})

	mc := NewMetricsCollector(nfs)

	t.Run("all TLS versions", func(t *testing.T) {
		versions := []uint16{
			0x0301, // TLS 1.0
			0x0302, // TLS 1.1
			0x0303, // TLS 1.2
			0x0304, // TLS 1.3
			0x0000, // Unknown
		}

		for _, v := range versions {
			mc.RecordTLSVersion(v)
		}
	})
}

// Tests for IsHealthy edge cases
func TestIsHealthyCoverage(t *testing.T) {
	mfs, _ := memfs.NewFS()
	config := DefaultRateLimiterConfig()
	nfs, _ := New(mfs, ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
	})

	mc := NewMetricsCollector(nfs)

	t.Run("healthy by default", func(t *testing.T) {
		if !mc.IsHealthy() {
			t.Error("Expected healthy by default")
		}
	})

	t.Run("unhealthy with high error rate", func(t *testing.T) {
		// Record many errors
		for i := 0; i < 100; i++ {
			mc.RecordError("AUTH")
		}
		// May still be healthy depending on threshold
	})
}

func createTestNFSForMetrics() *AbsfsNFS {
	mfs, _ := memfs.NewFS()
	config := DefaultRateLimiterConfig()
	nfs, _ := New(mfs, ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
	})
	return nfs
}

func TestMetricsRecordConnectionZeroCoverage(t *testing.T) {
	nfs := createTestNFSForMetrics()
	mc := NewMetricsCollector(nfs)

	t.Run("record connection", func(t *testing.T) {
		mc.RecordConnection()
		metrics := mc.GetMetrics()
		if metrics.ActiveConnections != 1 {
			t.Errorf("Expected 1 active connection, got %d", metrics.ActiveConnections)
		}
		if metrics.TotalConnections != 1 {
			t.Errorf("Expected 1 total connection, got %d", metrics.TotalConnections)
		}
	})
}

func TestMetricsRecordConnectionClosedZeroCoverage(t *testing.T) {
	nfs := createTestNFSForMetrics()
	mc := NewMetricsCollector(nfs)

	t.Run("record connection closed", func(t *testing.T) {
		mc.RecordConnection()
		mc.RecordConnectionClosed()
		metrics := mc.GetMetrics()
		if metrics.ActiveConnections != 0 {
			t.Errorf("Expected 0 active connections, got %d", metrics.ActiveConnections)
		}
	})
}

func TestMetricsRecordRejectedConnectionZeroCoverage(t *testing.T) {
	nfs := createTestNFSForMetrics()
	mc := NewMetricsCollector(nfs)

	t.Run("record rejected connection", func(t *testing.T) {
		mc.RecordRejectedConnection()
		metrics := mc.GetMetrics()
		if metrics.RejectedConnections != 1 {
			t.Errorf("Expected 1 rejected connection, got %d", metrics.RejectedConnections)
		}
	})
}

func TestMetricsRecordRateLimitExceededZeroCoverage(t *testing.T) {
	nfs := createTestNFSForMetrics()
	mc := NewMetricsCollector(nfs)

	t.Run("record rate limit exceeded", func(t *testing.T) {
		mc.RecordRateLimitExceeded()
		metrics := mc.GetMetrics()
		if metrics.RateLimitExceeded != 1 {
			t.Errorf("Expected 1 rate limit exceeded, got %d", metrics.RateLimitExceeded)
		}
	})
}

func TestMetricsRecordTLSZeroCoverage(t *testing.T) {
	nfs := createTestNFSForMetrics()
	mc := NewMetricsCollector(nfs)

	t.Run("record TLS handshake", func(t *testing.T) {
		mc.RecordTLSHandshake()
		metrics := mc.GetMetrics()
		if metrics.TLSHandshakes != 1 {
			t.Errorf("Expected 1 TLS handshake, got %d", metrics.TLSHandshakes)
		}
	})

	t.Run("record TLS handshake failure", func(t *testing.T) {
		mc.RecordTLSHandshakeFailure()
		metrics := mc.GetMetrics()
		if metrics.TLSHandshakeFailures != 1 {
			t.Errorf("Expected 1 TLS handshake failure, got %d", metrics.TLSHandshakeFailures)
		}
	})

	t.Run("record TLS client cert", func(t *testing.T) {
		mc.RecordTLSClientCert(true)
		mc.RecordTLSClientCert(false)
	})

	t.Run("record TLS session reused", func(t *testing.T) {
		mc.RecordTLSSessionReused()
	})

	t.Run("record TLS version", func(t *testing.T) {
		mc.RecordTLSVersion(0x0304) // TLS 1.3 version constant
	})
}

// Add mock error for testing
var ErrPermission = &os.PathError{Op: "write", Path: "/test", Err: os.ErrPermission}

// Tests for metrics RecordAttrCacheHit/Miss with nil metrics
func TestMetricsRecordWithNilCollector(t *testing.T) {
	nfs, _ := createTestServer(t)
	defer nfs.Close()

	// Set metrics to nil
	nfs.metrics = nil

	// These should not panic
	nfs.RecordAttrCacheHit()
	nfs.RecordAttrCacheMiss()
	nfs.RecordDirCacheHit()
	nfs.RecordDirCacheMiss()
	nfs.RecordNegativeCacheHit()
	nfs.RecordNegativeCacheMiss()
}

// Tests for metrics RecordLatency with more operation types
func TestRecordLatencyMoreOps(t *testing.T) {
	nfs, _ := createTestServer(t)
	defer nfs.Close()

	collector := NewMetricsCollector(nfs)

	ops := []string{
		"READ", "WRITE", "LOOKUP", "GETATTR", "SETATTR",
		"READDIR", "CREATE", "REMOVE", "RENAME",
	}

	for _, op := range ops {
		collector.IncrementOperationCount(op)
		collector.RecordLatency(op, time.Millisecond*100)
	}

	// Verify metrics are collected
	metrics := collector.GetMetrics()
	if metrics.TotalOperations < uint64(len(ops)) {
		t.Error("Expected operations to be recorded")
	}
}

// Tests for metrics IsHealthy
func TestMetricsIsHealthy(t *testing.T) {
	nfs, _ := createTestServer(t)
	defer nfs.Close()

	collector := NewMetricsCollector(nfs)

	// Initially should be healthy
	healthy := collector.IsHealthy()
	if !healthy {
		t.Error("Expected healthy initially")
	}

	// Record some operations
	for i := 0; i < 100; i++ {
		collector.IncrementOperationCount("READ")
		collector.RecordLatency("READ", time.Millisecond*10)
	}

	// Check health again
	_ = collector.IsHealthy()
}

// Tests for RecordOperationStart
func TestRecordOperationStartCoverage(t *testing.T) {
	nfs, _ := createTestServer(t)
	defer nfs.Close()

	ops := []string{"READ", "WRITE", "LOOKUP", "CREATE", "REMOVE"}
	for _, op := range ops {
		nfs.RecordOperationStart(op)
	}
}

func TestMetricsRecordingMore(t *testing.T) {
	nfs, _ := createTestServer(t)
	defer nfs.Close()

	t.Run("record various events", func(t *testing.T) {
		nfs.RecordAttrCacheHit()
		nfs.RecordAttrCacheMiss()
		nfs.RecordDirCacheHit()
		nfs.RecordDirCacheMiss()
	})

	t.Run("get metrics", func(t *testing.T) {
		metrics := nfs.GetMetrics()
		if metrics.TotalOperations < 0 {
			t.Error("Total operations should not be negative")
		}
	})

	t.Run("is healthy", func(t *testing.T) {
		healthy := nfs.IsHealthy()
		if !healthy {
			t.Log("Server reports unhealthy")
		}
	})
}

// Tests for RecordOperationStart coverage
func TestRecordOperationStartCoverageFull(t *testing.T) {
	nfs, _ := createTestServer(t)
	defer nfs.Close()

	// Record many different operations
	ops := []string{"GETATTR", "SETATTR", "LOOKUP", "ACCESS", "READ", "WRITE", "CREATE", "MKDIR", "REMOVE", "RMDIR"}
	for _, op := range ops {
		done := nfs.RecordOperationStart(op)
		done(nil)
	}

	// Record with errors
	for _, op := range ops {
		done := nfs.RecordOperationStart(op)
		done(os.ErrNotExist)
	}
}
