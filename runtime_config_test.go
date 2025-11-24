package absnfs

import (
	"sync"
	"testing"
	"time"

	"github.com/absfs/memfs"
)

func TestGetExportOptions(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	originalOptions := ExportOptions{
		ReadOnly:             true,
		AllowedIPs:           []string{"192.168.1.0/24", "10.0.0.1"},
		Squash:               "root",
		AttrCacheSize:        5000,
		AttrCacheTimeout:     10 * time.Second,
		ReadAheadMaxMemory:   50 * 1024 * 1024,
		ReadAheadMaxFiles:    50,
		MemoryHighWatermark:  0.75,
		MemoryLowWatermark:   0.5,
		MaxWorkers:           8,
		BatchOperations:      true,
		MaxBatchSize:         15,
	}

	server, err := New(fs, originalOptions)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	// Get the options
	opts := server.GetExportOptions()

	// Verify all fields match
	if opts.ReadOnly != originalOptions.ReadOnly {
		t.Errorf("ReadOnly mismatch: got %v, want %v", opts.ReadOnly, originalOptions.ReadOnly)
	}

	if len(opts.AllowedIPs) != len(originalOptions.AllowedIPs) {
		t.Errorf("AllowedIPs length mismatch: got %d, want %d", len(opts.AllowedIPs), len(originalOptions.AllowedIPs))
	} else {
		for i := range opts.AllowedIPs {
			if opts.AllowedIPs[i] != originalOptions.AllowedIPs[i] {
				t.Errorf("AllowedIPs[%d] mismatch: got %v, want %v", i, opts.AllowedIPs[i], originalOptions.AllowedIPs[i])
			}
		}
	}

	// Test that modifying the returned copy doesn't affect the server's options
	opts.ReadOnly = false
	opts.AllowedIPs[0] = "127.0.0.1"

	serverOpts := server.GetExportOptions()
	if serverOpts.ReadOnly != originalOptions.ReadOnly {
		t.Error("Server options were modified when returned copy was changed")
	}
	if serverOpts.AllowedIPs[0] != originalOptions.AllowedIPs[0] {
		t.Error("Server AllowedIPs were modified when returned copy was changed")
	}
}

func TestUpdateExportOptions_SafeFields(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	initialOptions := ExportOptions{
		ReadOnly:             false,
		AttrCacheSize:        1000,
		AttrCacheTimeout:     5 * time.Second,
		ReadAheadMaxMemory:   100 * 1024 * 1024,
		ReadAheadMaxFiles:    100,
		MemoryHighWatermark:  0.8,
		MemoryLowWatermark:   0.6,
		MaxWorkers:           4,
		BatchOperations:      false,
		MaxBatchSize:         10,
	}

	server, err := New(fs, initialOptions)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	// Update safe fields
	updatedOptions := ExportOptions{
		ReadOnly:             true,
		AllowedIPs:           []string{"10.0.0.0/8"},
		AttrCacheSize:        2000,
		AttrCacheTimeout:     10 * time.Second,
		ReadAheadMaxMemory:   200 * 1024 * 1024,
		ReadAheadMaxFiles:    200,
		MemoryHighWatermark:  0.9,
		MemoryLowWatermark:   0.7,
		MaxWorkers:           8,
		BatchOperations:      true,
		MaxBatchSize:         20,
	}

	err = server.UpdateExportOptions(updatedOptions)
	if err != nil {
		t.Fatalf("Failed to update export options: %v", err)
	}

	// Verify the updates were applied
	opts := server.GetExportOptions()

	if opts.ReadOnly != true {
		t.Errorf("ReadOnly not updated: got %v, want true", opts.ReadOnly)
	}

	if len(opts.AllowedIPs) != 1 || opts.AllowedIPs[0] != "10.0.0.0/8" {
		t.Errorf("AllowedIPs not updated: got %v, want [10.0.0.0/8]", opts.AllowedIPs)
	}

	if opts.AttrCacheSize != 2000 {
		t.Errorf("AttrCacheSize not updated: got %d, want 2000", opts.AttrCacheSize)
	}

	if opts.AttrCacheTimeout != 10*time.Second {
		t.Errorf("AttrCacheTimeout not updated: got %v, want 10s", opts.AttrCacheTimeout)
	}

	if opts.ReadAheadMaxMemory != 200*1024*1024 {
		t.Errorf("ReadAheadMaxMemory not updated: got %d, want %d", opts.ReadAheadMaxMemory, 200*1024*1024)
	}

	if opts.ReadAheadMaxFiles != 200 {
		t.Errorf("ReadAheadMaxFiles not updated: got %d, want 200", opts.ReadAheadMaxFiles)
	}

	if opts.MemoryHighWatermark != 0.9 {
		t.Errorf("MemoryHighWatermark not updated: got %f, want 0.9", opts.MemoryHighWatermark)
	}

	if opts.MemoryLowWatermark != 0.7 {
		t.Errorf("MemoryLowWatermark not updated: got %f, want 0.7", opts.MemoryLowWatermark)
	}

	if opts.MaxWorkers != 8 {
		t.Errorf("MaxWorkers not updated: got %d, want 8", opts.MaxWorkers)
	}

	if !opts.BatchOperations {
		t.Error("BatchOperations not updated: got false, want true")
	}

	if opts.MaxBatchSize != 20 {
		t.Errorf("MaxBatchSize not updated: got %d, want 20", opts.MaxBatchSize)
	}

	// Verify underlying components were resized
	attrCacheSize, attrCacheMax := server.attrCache.Stats()
	if attrCacheMax != 2000 {
		t.Errorf("AttrCache max size not updated: got %d, want 2000", attrCacheMax)
	}

	workerMax, _, _ := server.workerPool.Stats()
	if workerMax != 8 {
		t.Errorf("WorkerPool size not updated: got %d, want 8", workerMax)
	}

	t.Logf("AttrCache stats: size=%d, max=%d", attrCacheSize, attrCacheMax)
}

func TestUpdateExportOptions_ImmutableFields(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	initialOptions := ExportOptions{
		Squash: "root",
	}

	server, err := New(fs, initialOptions)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	// Try to change Squash mode (immutable field)
	updatedOptions := ExportOptions{
		Squash: "all",
	}

	err = server.UpdateExportOptions(updatedOptions)
	if err == nil {
		t.Error("Expected error when changing Squash mode, got nil")
	}

	// Verify the original value is still set
	opts := server.GetExportOptions()
	if opts.Squash != "root" {
		t.Errorf("Squash mode was changed: got %v, want root", opts.Squash)
	}
}

func TestUpdateExportOptions_ConcurrentUpdates(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	server, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	var wg sync.WaitGroup
	numGoroutines := 10
	numIterations := 100

	// Launch multiple goroutines that update and read options concurrently
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < numIterations; j++ {
				// Update options
				opts := ExportOptions{
					ReadOnly:        id%2 == 0,
					MaxWorkers:      id + 1,
					AttrCacheSize:   1000 + id*100,
					MaxBatchSize:    5 + id,
					BatchOperations: true,
				}

				err := server.UpdateExportOptions(opts)
				if err != nil {
					t.Errorf("Goroutine %d iteration %d: UpdateExportOptions failed: %v", id, j, err)
				}

				// Read options
				_ = server.GetExportOptions()

				// Small delay to increase chance of race conditions
				time.Sleep(time.Microsecond)
			}
		}(i)
	}

	wg.Wait()

	// Verify the server is still functional
	opts := server.GetExportOptions()
	if opts.MaxWorkers <= 0 {
		t.Error("Server state corrupted after concurrent updates")
	}
}

func TestUpdateExportOptions_LogConfiguration(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	initialOptions := ExportOptions{
		Log: &LogConfig{
			Level:  "info",
			Format: "text",
			Output: "stderr",
		},
	}

	server, err := New(fs, initialOptions)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	// Update log configuration
	updatedOptions := ExportOptions{
		Log: &LogConfig{
			Level:  "debug",
			Format: "json",
			Output: "stdout",
		},
	}

	err = server.UpdateExportOptions(updatedOptions)
	if err != nil {
		t.Fatalf("Failed to update log configuration: %v", err)
	}

	// Verify the log configuration was updated
	opts := server.GetExportOptions()
	if opts.Log == nil {
		t.Fatal("Log configuration is nil after update")
	}

	if opts.Log.Level != "debug" {
		t.Errorf("Log level not updated: got %v, want debug", opts.Log.Level)
	}

	if opts.Log.Format != "json" {
		t.Errorf("Log format not updated: got %v, want json", opts.Log.Format)
	}

	if opts.Log.Output != "stdout" {
		t.Errorf("Log output not updated: got %v, want stdout", opts.Log.Output)
	}
}

func TestAttrCache_Resize(t *testing.T) {
	cache := NewAttrCache(5*time.Second, 100)

	// Fill the cache with entries
	for i := 0; i < 100; i++ {
		path := "/test/file" + string(rune(i))
		attrs := &NFSAttrs{
			Mode:  0644,
			Size:  1024,
			Mtime: time.Now(),
		}
		cache.Put(path, attrs)
	}

	size, max := cache.Stats()
	if size != 100 || max != 100 {
		t.Errorf("Initial cache stats: got size=%d max=%d, want size=100 max=100", size, max)
	}

	// Resize to a smaller size
	cache.Resize(50)

	size, max = cache.Stats()
	if max != 50 {
		t.Errorf("Cache max size not updated: got %d, want 50", max)
	}

	if size > 50 {
		t.Errorf("Cache not evicted: got size=%d, want <=50", size)
	}

	// Resize to a larger size
	cache.Resize(200)

	size, max = cache.Stats()
	if max != 200 {
		t.Errorf("Cache max size not updated: got %d, want 200", max)
	}
}

func TestAttrCache_UpdateTTL(t *testing.T) {
	cache := NewAttrCache(100*time.Millisecond, 100)

	// Add an entry
	attrs := &NFSAttrs{
		Mode:  0644,
		Size:  1024,
		Mtime: time.Now(),
	}
	cache.Put("/test/file", attrs)

	// Wait for it to expire
	time.Sleep(150 * time.Millisecond)

	// Should be expired
	result := cache.Get("/test/file")
	if result != nil {
		t.Error("Entry should have expired but was still cached")
	}

	// Update TTL to a longer duration
	cache.UpdateTTL(1 * time.Second)

	// Add another entry
	cache.Put("/test/file2", attrs)

	// Wait for old TTL duration
	time.Sleep(150 * time.Millisecond)

	// Should still be cached with new TTL
	result = cache.Get("/test/file2")
	if result == nil {
		t.Error("Entry expired too early with new TTL")
	}
}

func TestReadAheadBuffer_Resize(t *testing.T) {
	buffer := NewReadAheadBuffer(4096)
	buffer.Configure(10, 100*1024) // 10 files, 100KB total

	// Fill the buffer with some files
	for i := 0; i < 10; i++ {
		path := "/test/file" + string(rune(i))
		data := make([]byte, 10*1024) // 10KB each
		buffer.Fill(path, data, 0)
	}

	files, memory := buffer.Stats()
	if files != 10 {
		t.Errorf("Initial buffer files: got %d, want 10", files)
	}

	// Resize to smaller limits
	buffer.Resize(5, 50*1024) // 5 files, 50KB total

	files, memory = buffer.Stats()
	if files > 5 {
		t.Errorf("Buffer not evicted: got %d files, want <=5", files)
	}

	if memory > 50*1024 {
		t.Errorf("Buffer memory not reduced: got %d, want <=51200", memory)
	}

	// Resize to larger limits
	buffer.Resize(20, 200*1024)

	// Should be able to add more files now
	for i := 10; i < 20; i++ {
		path := "/test/file" + string(rune(i))
		data := make([]byte, 10*1024)
		buffer.Fill(path, data, 0)
	}

	files, _ = buffer.Stats()
	if files > 20 {
		t.Errorf("Buffer exceeded new max files: got %d, want <=20", files)
	}
}

func TestUpdateExportOptions_NilServer(t *testing.T) {
	var server *AbsfsNFS

	err := server.UpdateExportOptions(ExportOptions{})
	if err == nil {
		t.Error("Expected error when updating nil server, got nil")
	}
}

func TestGetExportOptions_EmptyAllowedIPs(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	server, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	opts := server.GetExportOptions()
	if opts.AllowedIPs != nil && len(opts.AllowedIPs) > 0 {
		t.Errorf("Expected empty AllowedIPs, got %v", opts.AllowedIPs)
	}
}

func TestUpdateExportOptions_ClearAllowedIPs(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	initialOptions := ExportOptions{
		AllowedIPs: []string{"192.168.1.0/24"},
	}

	server, err := New(fs, initialOptions)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	// Clear AllowedIPs by passing empty slice
	updatedOptions := ExportOptions{
		AllowedIPs: []string{},
	}

	err = server.UpdateExportOptions(updatedOptions)
	if err != nil {
		t.Fatalf("Failed to update export options: %v", err)
	}

	opts := server.GetExportOptions()
	if opts.AllowedIPs != nil && len(opts.AllowedIPs) > 0 {
		t.Errorf("AllowedIPs should be cleared, got %v", opts.AllowedIPs)
	}
}
