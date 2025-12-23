package absnfs

import (
	"os"
	"testing"
	"time"

	"github.com/absfs/memfs"
)

func TestDirCacheConfiguration(t *testing.T) {
	// Create a memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Test cases for different dir cache configurations
	testCases := []struct {
		name               string
		enableDirCache     bool
		cacheTimeout       time.Duration
		cacheMaxEntries    int
		cacheMaxDirSize    int
		expectedTimeout    time.Duration
		expectedMaxEntries int
		expectedMaxDirSize int
	}{
		{
			name:               "Disabled by default",
			enableDirCache:     false,
			cacheTimeout:       0,
			cacheMaxEntries:    0,
			cacheMaxDirSize:    0,
			expectedTimeout:    10 * time.Second,
			expectedMaxEntries: 1000,
			expectedMaxDirSize: 10000,
		},
		{
			name:               "Enabled with default values",
			enableDirCache:     true,
			cacheTimeout:       0,
			cacheMaxEntries:    0,
			cacheMaxDirSize:    0,
			expectedTimeout:    10 * time.Second,
			expectedMaxEntries: 1000,
			expectedMaxDirSize: 10000,
		},
		{
			name:               "Custom timeout",
			enableDirCache:     true,
			cacheTimeout:       30 * time.Second,
			cacheMaxEntries:    0,
			cacheMaxDirSize:    0,
			expectedTimeout:    30 * time.Second,
			expectedMaxEntries: 1000,
			expectedMaxDirSize: 10000,
		},
		{
			name:               "Custom max entries",
			enableDirCache:     true,
			cacheTimeout:       0,
			cacheMaxEntries:    500,
			cacheMaxDirSize:    0,
			expectedTimeout:    10 * time.Second,
			expectedMaxEntries: 500,
			expectedMaxDirSize: 10000,
		},
		{
			name:               "Custom max dir size",
			enableDirCache:     true,
			cacheTimeout:       0,
			cacheMaxEntries:    0,
			cacheMaxDirSize:    5000,
			expectedTimeout:    10 * time.Second,
			expectedMaxEntries: 1000,
			expectedMaxDirSize: 5000,
		},
		{
			name:               "All custom values",
			enableDirCache:     true,
			cacheTimeout:       15 * time.Second,
			cacheMaxEntries:    200,
			cacheMaxDirSize:    2000,
			expectedTimeout:    15 * time.Second,
			expectedMaxEntries: 200,
			expectedMaxDirSize: 2000,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create server with the specified dir cache configuration
			options := ExportOptions{
				EnableDirCache:     tc.enableDirCache,
				DirCacheTimeout:    tc.cacheTimeout,
				DirCacheMaxEntries: tc.cacheMaxEntries,
				DirCacheMaxDirSize: tc.cacheMaxDirSize,
			}
			server, err := New(fs, options)
			if err != nil {
				t.Fatalf("Failed to create server: %v", err)
			}
			defer server.Close()

			// Verify options were set correctly in the server options
			if server.options.DirCacheTimeout != tc.expectedTimeout {
				t.Errorf("DirCacheTimeout not set correctly: got %v, want %v",
					server.options.DirCacheTimeout, tc.expectedTimeout)
			}
			if server.options.DirCacheMaxEntries != tc.expectedMaxEntries {
				t.Errorf("DirCacheMaxEntries not set correctly: got %d, want %d",
					server.options.DirCacheMaxEntries, tc.expectedMaxEntries)
			}
			if server.options.DirCacheMaxDirSize != tc.expectedMaxDirSize {
				t.Errorf("DirCacheMaxDirSize not set correctly: got %d, want %d",
					server.options.DirCacheMaxDirSize, tc.expectedMaxDirSize)
			}

			// Verify the directory cache was created correctly
			if tc.enableDirCache {
				if server.dirCache == nil {
					t.Error("DirCache should be initialized when enabled")
				}
			} else {
				if server.dirCache != nil {
					t.Error("DirCache should not be initialized when disabled")
				}
			}
		})
	}
}

func TestDirCacheBasicFunctionality(t *testing.T) {
	// Create a memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test directory and files
	if err := fs.Mkdir("/testdir", 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	for i := 0; i < 5; i++ {
		f, err := fs.Create("/testdir/file" + string(rune('0'+i)) + ".txt")
		if err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
		f.Close()
	}

	// Create server with dir cache enabled
	options := ExportOptions{
		EnableDirCache: true,
	}
	server, err := New(fs, options)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	// First read should miss cache
	node := &NFSNode{
		SymlinkFileSystem: fs,
		path:              "/testdir",
		attrs: &NFSAttrs{
			Mode: os.ModeDir | 0755,
		},
	}

	nodes1, err := server.ReadDir(node)
	if err != nil {
		t.Fatalf("First ReadDir failed: %v", err)
	}

	if len(nodes1) != 5 {
		t.Errorf("Expected 5 entries, got %d", len(nodes1))
	}

	// Check cache stats
	size, hits, misses := server.dirCache.Stats()
	if size != 1 {
		t.Errorf("Expected cache size 1, got %d", size)
	}
	if hits != 0 {
		t.Errorf("Expected 0 hits, got %d", hits)
	}
	if misses != 1 {
		t.Errorf("Expected 1 miss, got %d", misses)
	}

	// Second read should hit cache
	nodes2, err := server.ReadDir(node)
	if err != nil {
		t.Fatalf("Second ReadDir failed: %v", err)
	}

	if len(nodes2) != 5 {
		t.Errorf("Expected 5 entries, got %d", len(nodes2))
	}

	// Check cache stats again
	size, hits, misses = server.dirCache.Stats()
	if size != 1 {
		t.Errorf("Expected cache size 1, got %d", size)
	}
	if hits != 1 {
		t.Errorf("Expected 1 hit, got %d", hits)
	}
	if misses != 1 {
		t.Errorf("Expected 1 miss, got %d", misses)
	}
}

func TestDirCacheExpiration(t *testing.T) {
	// Create a memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test directory
	if err := fs.Mkdir("/testdir", 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	// Create server with short cache timeout
	options := ExportOptions{
		EnableDirCache:  true,
		DirCacheTimeout: 100 * time.Millisecond,
	}
	server, err := New(fs, options)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	node := &NFSNode{
		SymlinkFileSystem: fs,
		path:              "/testdir",
		attrs: &NFSAttrs{
			Mode: os.ModeDir | 0755,
		},
	}

	// First read should cache
	_, err = server.ReadDir(node)
	if err != nil {
		t.Fatalf("First ReadDir failed: %v", err)
	}

	// Check cache has entry
	size := server.dirCache.Size()
	if size != 1 {
		t.Errorf("Expected cache size 1, got %d", size)
	}

	// Wait for cache to expire
	time.Sleep(150 * time.Millisecond)

	// Read again should miss cache (expired)
	_, err = server.ReadDir(node)
	if err != nil {
		t.Fatalf("Second ReadDir failed: %v", err)
	}

	// Check cache stats
	_, hits, misses := server.dirCache.Stats()
	if hits != 0 {
		t.Errorf("Expected 0 hits (cache expired), got %d", hits)
	}
	if misses != 2 {
		t.Errorf("Expected 2 misses, got %d", misses)
	}
}

func TestDirCacheLRUEviction(t *testing.T) {
	// Create a memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create server with small cache size
	options := ExportOptions{
		EnableDirCache:     true,
		DirCacheMaxEntries: 3,
	}
	server, err := New(fs, options)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	// Create 4 test directories
	for i := 0; i < 4; i++ {
		dirPath := "/testdir" + string(rune('0'+i))
		if err := fs.Mkdir(dirPath, 0755); err != nil {
			t.Fatalf("Failed to create directory %s: %v", dirPath, err)
		}
	}

	// Read 3 directories to fill cache
	for i := 0; i < 3; i++ {
		dirPath := "/testdir" + string(rune('0'+i))
		node := &NFSNode{
			SymlinkFileSystem: fs,
			path:              dirPath,
			attrs: &NFSAttrs{
				Mode: os.ModeDir | 0755,
			},
		}
		_, err := server.ReadDir(node)
		if err != nil {
			t.Fatalf("ReadDir failed for %s: %v", dirPath, err)
		}
	}

	// Cache should be full
	size := server.dirCache.Size()
	if size != 3 {
		t.Errorf("Expected cache size 3, got %d", size)
	}

	// Read 4th directory - should evict LRU (first one)
	node4 := &NFSNode{
		SymlinkFileSystem: fs,
		path:              "/testdir3",
		attrs: &NFSAttrs{
			Mode: os.ModeDir | 0755,
		},
	}
	_, err = server.ReadDir(node4)
	if err != nil {
		t.Fatalf("ReadDir failed for /testdir3: %v", err)
	}

	// Cache should still be size 3
	size = server.dirCache.Size()
	if size != 3 {
		t.Errorf("Expected cache size 3, got %d", size)
	}

	// Verify first directory was evicted by checking if it's still in cache
	entries, found := server.dirCache.Get("/testdir0")
	if found {
		t.Errorf("Expected /testdir0 to be evicted, but it was found in cache with %d entries", len(entries))
	}

	// Verify last 3 directories are still in cache
	for i := 1; i < 4; i++ {
		dirPath := "/testdir" + string(rune('0'+i))
		entries, found := server.dirCache.Get(dirPath)
		if !found {
			t.Errorf("Expected %s to be in cache, but it was not found", dirPath)
		} else if entries == nil {
			t.Errorf("Expected %s to have entries in cache, but got nil", dirPath)
		}
	}
}

func TestDirCacheInvalidation(t *testing.T) {
	// Create a memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test directory
	if err := fs.Mkdir("/testdir", 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	// Create server with dir cache enabled
	options := ExportOptions{
		EnableDirCache: true,
	}
	server, err := New(fs, options)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	node := &NFSNode{
		SymlinkFileSystem: fs,
		path:              "/testdir",
		attrs: &NFSAttrs{
			Mode: os.ModeDir | 0755,
		},
	}

	// First read should cache
	nodes1, err := server.ReadDir(node)
	if err != nil {
		t.Fatalf("First ReadDir failed: %v", err)
	}
	if len(nodes1) != 0 {
		t.Errorf("Expected 0 entries initially, got %d", len(nodes1))
	}

	// Add a file
	f, err := fs.Create("/testdir/newfile.txt")
	if err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}
	f.Close()

	// Invalidate cache (simulating what happens after CREATE)
	server.dirCache.Invalidate("/testdir")

	// Read again should miss cache and see new file
	nodes2, err := server.ReadDir(node)
	if err != nil {
		t.Fatalf("Second ReadDir failed: %v", err)
	}
	if len(nodes2) != 1 {
		t.Errorf("Expected 1 entry after adding file, got %d", len(nodes2))
	}

	// Check cache stats
	_, hits, misses := server.dirCache.Stats()
	if hits != 0 {
		t.Errorf("Expected 0 hits (cache was invalidated), got %d", hits)
	}
	if misses != 2 {
		t.Errorf("Expected 2 misses, got %d", misses)
	}
}

func TestDirCacheLargeDirSize(t *testing.T) {
	// Create a memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test directory with many files
	if err := fs.Mkdir("/largedir", 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	// Create 15 files (exceeds default max dir size of 10 for this test)
	for i := 0; i < 15; i++ {
		f, err := fs.Create("/largedir/file" + string(rune('0'+i/10)) + string(rune('0'+i%10)) + ".txt")
		if err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
		f.Close()
	}

	// Create server with small max dir size
	options := ExportOptions{
		EnableDirCache:     true,
		DirCacheMaxDirSize: 10,
	}
	server, err := New(fs, options)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	node := &NFSNode{
		SymlinkFileSystem: fs,
		path:              "/largedir",
		attrs: &NFSAttrs{
			Mode: os.ModeDir | 0755,
		},
	}

	// First read should not cache (too many entries)
	nodes1, err := server.ReadDir(node)
	if err != nil {
		t.Fatalf("First ReadDir failed: %v", err)
	}
	if len(nodes1) != 15 {
		t.Errorf("Expected 15 entries, got %d", len(nodes1))
	}

	// Cache should be empty
	size := server.dirCache.Size()
	if size != 0 {
		t.Errorf("Expected cache size 0 (dir too large), got %d", size)
	}

	// Second read should also not use cache
	nodes2, err := server.ReadDir(node)
	if err != nil {
		t.Fatalf("Second ReadDir failed: %v", err)
	}
	if len(nodes2) != 15 {
		t.Errorf("Expected 15 entries, got %d", len(nodes2))
	}

	// Check cache stats - should have 2 misses, 0 hits
	_, hits, misses := server.dirCache.Stats()
	if hits != 0 {
		t.Errorf("Expected 0 hits (dir too large), got %d", hits)
	}
	if misses != 2 {
		t.Errorf("Expected 2 misses, got %d", misses)
	}
}

func TestDirCacheClear(t *testing.T) {
	// Create a memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test directories
	for i := 0; i < 3; i++ {
		dirPath := "/testdir" + string(rune('0'+i))
		if err := fs.Mkdir(dirPath, 0755); err != nil {
			t.Fatalf("Failed to create directory %s: %v", dirPath, err)
		}
	}

	// Create server with dir cache enabled
	options := ExportOptions{
		EnableDirCache: true,
	}
	server, err := New(fs, options)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	// Read all directories to fill cache
	for i := 0; i < 3; i++ {
		dirPath := "/testdir" + string(rune('0'+i))
		node := &NFSNode{
			SymlinkFileSystem: fs,
			path:              dirPath,
			attrs: &NFSAttrs{
				Mode: os.ModeDir | 0755,
			},
		}
		_, err := server.ReadDir(node)
		if err != nil {
			t.Fatalf("ReadDir failed for %s: %v", dirPath, err)
		}
	}

	// Cache should have 3 entries
	size := server.dirCache.Size()
	if size != 3 {
		t.Errorf("Expected cache size 3, got %d", size)
	}

	// Clear cache
	server.dirCache.Clear()

	// Cache should be empty
	size = server.dirCache.Size()
	if size != 0 {
		t.Errorf("Expected cache size 0 after clear, got %d", size)
	}

	// Stats should be reset
	_, hits, misses := server.dirCache.Stats()
	if hits != 0 {
		t.Errorf("Expected 0 hits after clear, got %d", hits)
	}
	if misses != 3 {
		t.Errorf("Expected 3 misses (from initial reads), got %d", misses)
	}
}

func TestDirCacheMetricsTracking(t *testing.T) {
	// Create a memory filesystem
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test directory
	if err := fs.Mkdir("/testdir", 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	// Create server with dir cache enabled
	options := ExportOptions{
		EnableDirCache: true,
	}
	server, err := New(fs, options)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	node := &NFSNode{
		SymlinkFileSystem: fs,
		path:              "/testdir",
		attrs: &NFSAttrs{
			Mode: os.ModeDir | 0755,
		},
	}

	// First read (miss)
	_, err = server.ReadDir(node)
	if err != nil {
		t.Fatalf("First ReadDir failed: %v", err)
	}

	// Check metrics were recorded
	metrics := server.GetMetrics()
	if metrics.DirCacheHitRate != 0.0 {
		t.Errorf("Expected hit rate 0.0 after first read, got %f", metrics.DirCacheHitRate)
	}

	// Second read (hit)
	_, err = server.ReadDir(node)
	if err != nil {
		t.Fatalf("Second ReadDir failed: %v", err)
	}

	// Check metrics were updated
	metrics = server.GetMetrics()
	expectedHitRate := 0.5 // 1 hit out of 2 total
	if metrics.DirCacheHitRate != expectedHitRate {
		t.Errorf("Expected hit rate %f after second read, got %f", expectedHitRate, metrics.DirCacheHitRate)
	}

	// Third read (hit)
	_, err = server.ReadDir(node)
	if err != nil {
		t.Fatalf("Third ReadDir failed: %v", err)
	}

	// Check metrics were updated
	metrics = server.GetMetrics()
	expectedHitRate = 2.0 / 3.0 // 2 hits out of 3 total
	if metrics.DirCacheHitRate < expectedHitRate-0.01 || metrics.DirCacheHitRate > expectedHitRate+0.01 {
		t.Errorf("Expected hit rate ~%f after third read, got %f", expectedHitRate, metrics.DirCacheHitRate)
	}
}
