package absnfs

import (
	"os"
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

// Add mock error for testing
var ErrPermission = &os.PathError{Op: "write", Path: "/test", Err: os.ErrPermission}