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

	// Record read-ahead hits and misses
	for i := 0; i < 20; i++ {
		server.RecordReadAheadHit()
	}
	for i := 0; i < 10; i++ {
		server.RecordReadAheadMiss()
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

	expectedReadAheadHitRate := float64(20) / float64(20+10)
	if metrics.ReadAheadHitRate != expectedReadAheadHitRate {
		t.Errorf("Expected read-ahead hit rate %f, got %f", expectedReadAheadHitRate, metrics.ReadAheadHitRate)
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

// Add mock error for testing
var ErrPermission = &os.PathError{Op: "write", Path: "/test", Err: os.ErrPermission}
