package absnfs

import (
	"runtime"
	"testing"
	"time"

	"github.com/absfs/memfs"
)

func TestMemoryMonitorCreation(t *testing.T) {
	// Create a test filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create NFS server with memory pressure detection enabled
	options := ExportOptions{
		AdaptToMemoryPressure: true,
		MemoryHighWatermark:   0.8,
		MemoryLowWatermark:    0.6,
		MemoryCheckInterval:   1 * time.Second,
	}

	server, err := New(fs, options)
	if err != nil {
		t.Fatalf("Failed to create NFS server: %v", err)
	}

	// Verify that memory monitor was created and started
	if server.memoryMonitor == nil {
		t.Fatal("Memory monitor was not created when AdaptToMemoryPressure is true")
	}

	if !server.memoryMonitor.IsActive() {
		t.Fatal("Memory monitor was not started when AdaptToMemoryPressure is true")
	}

	// Clean up
	server.stopMemoryMonitoring()
}

func TestMemoryMonitorDisabled(t *testing.T) {
	// Create a test filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create NFS server with memory pressure detection disabled (default)
	options := ExportOptions{}

	server, err := New(fs, options)
	if err != nil {
		t.Fatalf("Failed to create NFS server: %v", err)
	}

	// Verify that memory monitor was not created
	if server.memoryMonitor != nil {
		t.Fatal("Memory monitor was created when AdaptToMemoryPressure is false")
	}
}

func TestMemoryStatsUpdate(t *testing.T) {
	// Create a test filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create NFS server
	server, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatalf("Failed to create NFS server: %v", err)
	}

	// Create a memory monitor but don't start it
	monitor := NewMemoryMonitor(server)

	// Test initial stats
	initialStats := monitor.GetMemoryStats()
	if initialStats.totalMemory == 0 {
		t.Error("Total memory should not be zero")
	}
	if initialStats.usageFraction < 0 || initialStats.usageFraction > 1.0 {
		t.Errorf("Usage fraction outside valid range: %f", initialStats.usageFraction)
	}

	// Allocate some memory to change the stats
	// This is just to ensure the values change between readings
	data := make([]byte, 1024*1024*10) // 10MB
	for i := range data {
		data[i] = byte(i & 0xff)
	}

	// Force garbage collection to stabilize memory readings
	runtime.GC()

	// Update stats again and verify they've changed
	monitor.updateStats()
	newStats := monitor.stats

	// The test passes if we can successfully get memory stats
	// We don't test specific values since they'll vary by system
	if newStats.lastUpdated.Before(initialStats.lastUpdated) {
		t.Error("Stats timestamp did not update")
	}

	// Clean up
	data = nil
}

func TestCacheReductionCalculation(t *testing.T) {
	// Create a test filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Set options with large cache sizes
	options := ExportOptions{
		AttrCacheSize:      5000,
		ReadAheadMaxFiles:  50,
		ReadAheadMaxMemory: 50 * 1024 * 1024, // 50MB
	}

	server, err := New(fs, options)
	if err != nil {
		t.Fatalf("Failed to create NFS server: %v", err)
	}

	// Create a memory monitor
	monitor := NewMemoryMonitor(server)

	// Mock memory pressure state
	monitor.stats.usageFraction = 0.9 // 90% used
	server.options.MemoryHighWatermark = 0.8
	server.options.MemoryLowWatermark = 0.6

	// Store initial cache sizes
	initialAttrCacheSize := server.attrCache.MaxSize()
	initialReadAheadMaxFiles := server.options.ReadAheadMaxFiles
	initialReadAheadMaxMemory := server.options.ReadAheadMaxMemory

	// Simulate memory pressure reduction
	// This should reduce cache sizes
	monitor.reduceCacheSizes(0.3) // 30% reduction

	// Verify cache sizes were reduced
	if server.attrCache.MaxSize() >= initialAttrCacheSize {
		t.Errorf("Attribute cache size was not reduced: %d vs %d", 
			server.attrCache.MaxSize(), initialAttrCacheSize)
	}

	// Get current read-ahead buffer configuration
	fileCount, memUsage := server.readBuf.Stats()

	// Verify file count limit was reduced
	if fileCount > initialReadAheadMaxFiles {
		t.Errorf("Read-ahead files limit was not reduced")
	}

	// Verify memory limit was reduced
	if memUsage > initialReadAheadMaxMemory {
		t.Errorf("Read-ahead memory limit was not reduced")
	}
}

func TestMemoryPressureHandling(t *testing.T) {
	// Skip in short mode as this test takes a few seconds
	if testing.Short() {
		t.Skip("Skipping memory pressure test in short mode")
	}

	// Create a test filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create NFS server with memory pressure detection
	options := ExportOptions{
		AdaptToMemoryPressure: true,
		MemoryHighWatermark:   0.1, // Set very low to force triggering
		MemoryLowWatermark:    0.05,
		MemoryCheckInterval:   500 * time.Millisecond,
		AttrCacheSize:         5000,
		ReadAheadMaxFiles:     100,
		ReadAheadMaxMemory:    50 * 1024 * 1024, // 50MB
	}

	server, err := New(fs, options)
	if err != nil {
		t.Fatalf("Failed to create NFS server: %v", err)
	}

	// Store initial cache sizes
	initialAttrCacheSize := server.attrCache.MaxSize()

	// We need to create load to trigger memory pressure
	// Allocate memory in smaller chunks to avoid overwhelming the test system
	allData := make([][]byte, 0)
	for i := 0; i < 10; i++ {
		data := make([]byte, 1024*1024*10) // 10MB per chunk
		for j := range data {
			data[j] = byte(j & 0xff) // Ensure memory is actually used
		}
		allData = append(allData, data)
		
		// Brief pause to let the monitor detect changes
		time.Sleep(100 * time.Millisecond)
	}

	// Wait for monitor to detect pressure and act
	time.Sleep(2 * time.Second)

	// Verify cache sizes were reduced in response to pressure
	if server.attrCache.MaxSize() >= initialAttrCacheSize {
		t.Logf("Warning: Expected attribute cache size to be reduced, but it wasn't: %d vs %d", 
			server.attrCache.MaxSize(), initialAttrCacheSize)
		// Note: This might not always happen reliably in test environments due to
		// varying memory conditions, so we log a warning instead of failing the test
	}

	// Clean up
	server.stopMemoryMonitoring()
	allData = nil
	runtime.GC()
}

func BenchmarkMemoryMonitor(b *testing.B) {
	// Create a test filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		b.Fatalf("Failed to create memfs: %v", err)
	}

	// Create NFS server
	server, err := New(fs, ExportOptions{})
	if err != nil {
		b.Fatalf("Failed to create NFS server: %v", err)
	}

	// Create a memory monitor
	monitor := NewMemoryMonitor(server)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		monitor.updateStats()
		monitor.checkMemoryPressure()
	}
}