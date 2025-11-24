package absnfs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/absfs/memfs"
)

// TestNegativeCacheBasic tests basic negative caching functionality
func TestNegativeCacheBasic(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create server with negative caching enabled
	server, err := New(fs, ExportOptions{
		CacheNegativeLookups: true,
		NegativeCacheTimeout: 5 * time.Second,
		AttrCacheTimeout:     5 * time.Second,
		AttrCacheSize:        100,
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	// Try to lookup a non-existent file
	nonExistentPath := "/nonexistent.txt"
	node, err := server.Lookup(nonExistentPath)
	if err == nil {
		t.Fatal("Expected error for non-existent file")
	}
	if node != nil {
		t.Fatal("Expected nil node for non-existent file")
	}

	// Check that negative cache entry was created
	negativeSize := server.attrCache.NegativeStats()
	if negativeSize != 1 {
		t.Errorf("Expected 1 negative cache entry, got %d", negativeSize)
	}

	// Try to lookup the same file again (should hit negative cache)
	node, err = server.Lookup(nonExistentPath)
	if err == nil {
		t.Fatal("Expected error for cached non-existent file")
	}
	if node != nil {
		t.Fatal("Expected nil node for cached non-existent file")
	}

	// Verify metrics
	metrics := server.GetMetrics()
	if metrics.NegativeCacheSize != 1 {
		t.Errorf("Expected NegativeCacheSize=1, got %d", metrics.NegativeCacheSize)
	}
}

// TestNegativeCacheDisabled tests that negative caching can be disabled
func TestNegativeCacheDisabled(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create server with negative caching disabled
	server, err := New(fs, ExportOptions{
		CacheNegativeLookups: false,
		AttrCacheTimeout:     5 * time.Second,
		AttrCacheSize:        100,
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	// Try to lookup a non-existent file
	nonExistentPath := "/nonexistent.txt"
	node, err := server.Lookup(nonExistentPath)
	if err == nil {
		t.Fatal("Expected error for non-existent file")
	}
	if node != nil {
		t.Fatal("Expected nil node for non-existent file")
	}

	// Check that no negative cache entry was created
	negativeSize := server.attrCache.NegativeStats()
	if negativeSize != 0 {
		t.Errorf("Expected 0 negative cache entries when disabled, got %d", negativeSize)
	}
}

// TestNegativeCacheExpiration tests that negative cache entries expire
func TestNegativeCacheExpiration(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create server with short negative cache timeout
	server, err := New(fs, ExportOptions{
		CacheNegativeLookups: true,
		NegativeCacheTimeout: 100 * time.Millisecond,
		AttrCacheTimeout:     5 * time.Second,
		AttrCacheSize:        100,
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	// Try to lookup a non-existent file
	nonExistentPath := "/nonexistent.txt"
	_, err = server.Lookup(nonExistentPath)
	if err == nil {
		t.Fatal("Expected error for non-existent file")
	}

	// Check that negative cache entry was created
	negativeSize := server.attrCache.NegativeStats()
	if negativeSize != 1 {
		t.Errorf("Expected 1 negative cache entry, got %d", negativeSize)
	}

	// Wait for negative cache to expire
	time.Sleep(150 * time.Millisecond)

	// Now create the file
	f, err := fs.Create(nonExistentPath)
	if err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}
	f.Close()

	// Try to lookup the file (should succeed since cache expired)
	node, err := server.Lookup(nonExistentPath)
	if err != nil {
		t.Fatalf("Expected no error after creating file, got: %v", err)
	}
	if node == nil {
		t.Fatal("Expected non-nil node after creating file")
	}
}

// TestNegativeCacheInvalidationOnCreate tests that negative cache is invalidated on CREATE
func TestNegativeCacheInvalidationOnCreate(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create a root directory
	if err := fs.Mkdir("/testdir", 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	// Create server with negative caching enabled
	server, err := New(fs, ExportOptions{
		CacheNegativeLookups: true,
		NegativeCacheTimeout: 5 * time.Second,
		AttrCacheTimeout:     5 * time.Second,
		AttrCacheSize:        100,
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	// Try to lookup a non-existent file
	testPath := "/testdir/test.txt"
	_, err = server.Lookup(testPath)
	if err == nil {
		t.Fatal("Expected error for non-existent file")
	}

	// Check that negative cache entry was created
	negativeSize := server.attrCache.NegativeStats()
	if negativeSize != 1 {
		t.Errorf("Expected 1 negative cache entry, got %d", negativeSize)
	}

	// Create the file using NFS Create operation
	dirNode, err := server.Lookup("/testdir")
	if err != nil {
		t.Fatalf("Failed to lookup directory: %v", err)
	}

	attrs := &NFSAttrs{
		Mode: 0644,
		Uid:  0,
		Gid:  0,
	}

	node, err := server.Create(dirNode, "test.txt", attrs)
	if err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}
	if node == nil {
		t.Fatal("Expected non-nil node after creating file")
	}

	// Check that negative cache entry was invalidated
	negativeSize = server.attrCache.NegativeStats()
	if negativeSize != 0 {
		t.Errorf("Expected 0 negative cache entries after CREATE, got %d", negativeSize)
	}

	// Verify we can lookup the file now
	node, err = server.Lookup(testPath)
	if err != nil {
		t.Fatalf("Expected no error after creating file, got: %v", err)
	}
	if node == nil {
		t.Fatal("Expected non-nil node after creating file")
	}
}

// TestNegativeCacheInvalidationOnRename tests that negative cache is invalidated on RENAME
func TestNegativeCacheInvalidationOnRename(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create directories
	if err := fs.Mkdir("/dir1", 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}
	if err := fs.Mkdir("/dir2", 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	// Create a file in dir1
	f, err := fs.Create("/dir1/test.txt")
	if err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}
	f.Close()

	// Create server with negative caching enabled
	server, err := New(fs, ExportOptions{
		CacheNegativeLookups: true,
		NegativeCacheTimeout: 5 * time.Second,
		AttrCacheTimeout:     5 * time.Second,
		AttrCacheSize:        100,
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	// Try to lookup a non-existent file in dir2
	targetPath := "/dir2/renamed.txt"
	_, err = server.Lookup(targetPath)
	if err == nil {
		t.Fatal("Expected error for non-existent file")
	}

	// Check that negative cache entry was created
	negativeSize := server.attrCache.NegativeStats()
	if negativeSize != 1 {
		t.Errorf("Expected 1 negative cache entry, got %d", negativeSize)
	}

	// Rename the file from dir1 to dir2
	dir1Node, err := server.Lookup("/dir1")
	if err != nil {
		t.Fatalf("Failed to lookup dir1: %v", err)
	}

	dir2Node, err := server.Lookup("/dir2")
	if err != nil {
		t.Fatalf("Failed to lookup dir2: %v", err)
	}

	err = server.Rename(dir1Node, "test.txt", dir2Node, "renamed.txt")
	if err != nil {
		t.Fatalf("Failed to rename file: %v", err)
	}

	// Check that negative cache entry was invalidated
	negativeSize = server.attrCache.NegativeStats()
	if negativeSize != 0 {
		t.Errorf("Expected 0 negative cache entries after RENAME, got %d", negativeSize)
	}

	// Verify we can lookup the file now
	node, err := server.Lookup(targetPath)
	if err != nil {
		t.Fatalf("Expected no error after renaming file, got: %v", err)
	}
	if node == nil {
		t.Fatal("Expected non-nil node after renaming file")
	}
}

// TestNegativeCacheLRUEviction tests that negative cache entries are evicted using LRU
func TestNegativeCacheLRUEviction(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create server with small cache size
	server, err := New(fs, ExportOptions{
		CacheNegativeLookups: true,
		NegativeCacheTimeout: 5 * time.Second,
		AttrCacheTimeout:     5 * time.Second,
		AttrCacheSize:        5, // Small cache size
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	// Try to lookup multiple non-existent files
	for i := 0; i < 10; i++ {
		path := filepath.Join("/", "nonexistent", string(rune('a'+i))+".txt")
		_, err := server.Lookup(path)
		if err == nil {
			t.Fatalf("Expected error for non-existent file %s", path)
		}
	}

	// Check that cache size is limited
	cacheSize, cacheCapacity := server.attrCache.Stats()
	if cacheSize > cacheCapacity {
		t.Errorf("Cache size (%d) exceeds capacity (%d)", cacheSize, cacheCapacity)
	}
}

// TestNegativeCacheMetrics tests that metrics are properly tracked
func TestNegativeCacheMetrics(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create server with negative caching enabled
	server, err := New(fs, ExportOptions{
		CacheNegativeLookups: true,
		NegativeCacheTimeout: 5 * time.Second,
		AttrCacheTimeout:     5 * time.Second,
		AttrCacheSize:        100,
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	// Try to lookup a non-existent file (should miss)
	nonExistentPath := "/nonexistent.txt"
	_, err = server.Lookup(nonExistentPath)
	if err == nil {
		t.Fatal("Expected error for non-existent file")
	}

	// Try to lookup the same file again (should hit)
	_, err = server.Lookup(nonExistentPath)
	if err == nil {
		t.Fatal("Expected error for cached non-existent file")
	}

	// Get metrics
	metrics := server.GetMetrics()

	// Verify negative cache size
	if metrics.NegativeCacheSize != 1 {
		t.Errorf("Expected NegativeCacheSize=1, got %d", metrics.NegativeCacheSize)
	}

	// Note: NegativeCacheHitRate requires proper tracking of hits/misses
	// which is done through RecordNegativeCacheHit/RecordNegativeCacheMiss
}

// TestNegativeCacheWithSymlinks tests negative caching with symlink operations
func TestNegativeCacheWithSymlinks(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Check if filesystem supports symlinks
	// memfs doesn't support symlinks, so skip this test
	t.Skip("memfs does not support symlinks")

	// Create a directory
	if err := fs.Mkdir("/testdir", 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	// Create server with negative caching enabled
	server, err := New(fs, ExportOptions{
		CacheNegativeLookups: true,
		NegativeCacheTimeout: 5 * time.Second,
		AttrCacheTimeout:     5 * time.Second,
		AttrCacheSize:        100,
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	// Try to lookup a non-existent symlink
	symlinkPath := "/testdir/link.txt"
	_, err = server.Lookup(symlinkPath)
	if err == nil {
		t.Fatal("Expected error for non-existent symlink")
	}

	// Check that negative cache entry was created
	negativeSize := server.attrCache.NegativeStats()
	if negativeSize != 1 {
		t.Errorf("Expected 1 negative cache entry, got %d", negativeSize)
	}

	// Create a target file first
	targetFile, err := fs.Create("/testdir/target.txt")
	if err != nil {
		t.Fatalf("Failed to create target file: %v", err)
	}
	targetFile.Close()

	// Create the symlink using NFS Symlink operation
	dirNode, err := server.Lookup("/testdir")
	if err != nil {
		t.Fatalf("Failed to lookup directory: %v", err)
	}

	attrs := &NFSAttrs{
		Mode: os.ModeSymlink | 0777,
		Uid:  0,
		Gid:  0,
	}

	node, err := server.Symlink(dirNode, "link.txt", "target.txt", attrs)
	if err != nil {
		t.Fatalf("Failed to create symlink: %v", err)
	}
	if node == nil {
		t.Fatal("Expected non-nil node after creating symlink")
	}

	// Check that negative cache entry was invalidated
	negativeSize = server.attrCache.NegativeStats()
	if negativeSize != 0 {
		t.Errorf("Expected 0 negative cache entries after SYMLINK, got %d", negativeSize)
	}

	// Verify we can lookup the symlink now
	node, err = server.Lookup(symlinkPath)
	if err != nil {
		t.Fatalf("Expected no error after creating symlink, got: %v", err)
	}
	if node == nil {
		t.Fatal("Expected non-nil node after creating symlink")
	}
}

// TestNegativeCacheIsChildOf tests the isChildOf helper function
func TestNegativeCacheIsChildOf(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		dirPath  string
		expected bool
	}{
		{"root child", "/test.txt", "/", true},
		{"direct child", "/dir/test.txt", "/dir", true},
		{"nested child", "/dir/subdir/test.txt", "/dir", true},
		{"not child", "/other/test.txt", "/dir", false},
		{"same path", "/dir", "/dir", false},
		{"parent path", "/", "/dir", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isChildOf(tt.path, tt.dirPath)
			if result != tt.expected {
				t.Errorf("isChildOf(%q, %q) = %v, want %v",
					tt.path, tt.dirPath, result, tt.expected)
			}
		})
	}
}

// TestNegativeCacheConfigureTimeout tests configuring negative cache timeout
func TestNegativeCacheConfigureTimeout(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create server with custom negative cache timeout
	customTimeout := 10 * time.Second
	server, err := New(fs, ExportOptions{
		CacheNegativeLookups: true,
		NegativeCacheTimeout: customTimeout,
		AttrCacheTimeout:     5 * time.Second,
		AttrCacheSize:        100,
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close()

	// Verify that the timeout was set
	if server.attrCache.negativeTTL != customTimeout {
		t.Errorf("Expected negativeTTL=%v, got %v", customTimeout, server.attrCache.negativeTTL)
	}
}
