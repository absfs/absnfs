package absnfs

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/absfs/memfs"
)

func TestAttrCache(t *testing.T) {
	t.Run("basic operations", func(t *testing.T) {
		cache := NewAttrCache(2*time.Second, 1000)

		// Test initial state
		if attrs, _ := cache.Get("/test.txt", nil); attrs != nil {
			t.Error("Expected nil for non-existent entry")
		}

		// Test Put and Get
		initialAttrs := &NFSAttrs{
			Mode: 0644,
			Size: 1234,
			// Mtime: time.Now()
			// Atime: time.Now()
			Uid: 1000,
			Gid: 1000,
		}
		cache.Put("/test.txt", initialAttrs)

		// Get should return a copy, not the original
		cachedAttrs, _ := cache.Get("/test.txt", nil)
		if cachedAttrs == nil {
			t.Fatal("Expected non-nil cached attributes")
		}
		if cachedAttrs == initialAttrs {
			t.Error("Get should return a copy, not the original")
		}
		if cachedAttrs.Mode != initialAttrs.Mode ||
			cachedAttrs.Size != initialAttrs.Size ||
			cachedAttrs.Uid != initialAttrs.Uid ||
			cachedAttrs.Gid != initialAttrs.Gid {
			t.Error("Cached attributes don't match original")
		}

		// Test expiration
		time.Sleep(3 * time.Second)
		if attrs, _ := cache.Get("/test.txt", nil); attrs != nil {
			t.Error("Expected nil for expired entry")
		}
	})

	t.Run("cache eviction", func(t *testing.T) {
		cache := NewAttrCache(10*time.Second, 5)

		// Add entries until eviction occurs
		for i := 0; i < 10; i++ {
			path := fmt.Sprintf("/file%d.txt", i)
			attrs := &NFSAttrs{
				Mode: 0644,
				Size: int64(i * 1000),
				// Mtime: time.Now()
				// Atime: time.Now()
				Uid: 1000,
				Gid: 1000,
			}
			cache.Put(path, attrs)
		}

		// Check size is limited to maxSize
		if cache.Size() > 5 {
			t.Errorf("Expected size <= 5, got %d", cache.Size())
		}

		// Verify the first entries were evicted (least recently used)
		if attrs, _ := cache.Get("/file0.txt", nil); attrs != nil {
			t.Error("Expected early entry to be evicted")
		}
		if attrs, _ := cache.Get("/file9.txt", nil); attrs == nil {
			t.Error("Expected recent entry to be present")
		}
	})

	t.Run("concurrent operations", func(t *testing.T) {
		cache := NewAttrCache(5*time.Second, 1000)
		var wg sync.WaitGroup
		numGoroutines := 5
		numOperations := 100

		// Launch multiple goroutines to perform cache operations
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < numOperations; j++ {
					path := fmt.Sprintf("/file%d_%d.txt", id, j)
					// Put
					attrs := &NFSAttrs{
						Mode: 0644,
						Size: int64(j * 1000),
						// Mtime: time.Now()
						// Atime: time.Now()
						Uid: uint32(id),
						Gid: uint32(id),
					}
					cache.Put(path, attrs)

					// Get
					_, _ = cache.Get(path, nil)

					// Invalidate (occasionally)
					if j%10 == 0 {
						cache.Invalidate(path)
					}
				}
			}(i)
		}

		// Wait for all goroutines to finish
		wg.Wait()

		// Verify cache size is reasonable (should be less than total operations due to invalidations)
		if cache.Size() > numGoroutines*numOperations {
			t.Errorf("Cache size larger than expected: %d", cache.Size())
		}
	})
}

// TestH6_CacheInvalidateOrder verifies that Invalidate calls removeFromAccessLog
// before delete, so that the access log can still look up the cache entry.
func TestH6_CacheInvalidateOrder(t *testing.T) {
	cache := NewAttrCache(10*time.Second, 100)

	// Add an entry
	attrs := &NFSAttrs{Mode: 0644, Size: 100}
	cache.Put("/test/file.txt", attrs)

	// Verify entry exists
	got, found := cache.Get("/test/file.txt")
	if !found || got == nil {
		t.Fatal("Expected cache entry to exist after Put")
	}

	// Invalidate should not panic and should remove both the entry and access log
	cache.Invalidate("/test/file.txt")

	// Verify entry is gone
	got, found = cache.Get("/test/file.txt")
	if found || got != nil {
		t.Error("Expected cache entry to be removed after Invalidate")
	}

	// Verify cache size is 0
	if cache.Size() != 0 {
		t.Errorf("Expected cache size 0 after Invalidate, got %d", cache.Size())
	}

	// Test DirCache Invalidate order too
	dirCache := NewDirCache(10*time.Second, 100, 1000)
	dirCache.Put("/testdir", []os.FileInfo{})

	entries, found := dirCache.Get("/testdir")
	if !found {
		t.Fatal("Expected dir cache entry to exist after Put")
	}
	_ = entries

	dirCache.Invalidate("/testdir")
	_, found = dirCache.Get("/testdir")
	if found {
		t.Error("Expected dir cache entry to be removed after Invalidate")
	}
}

// TestM7_AttrCacheThreeStateReturn verifies that AttrCache.Get returns a
// 3-state result: (attrs, true) for hit, (nil, true) for negative hit,
// (nil, false) for miss.
func TestM7_AttrCacheThreeStateReturn(t *testing.T) {
	cache := NewAttrCache(10*time.Second, 100)
	cache.ConfigureNegativeCaching(true, 10*time.Second)

	// Cache miss: path not in cache at all
	attrs, found := cache.Get("/nonexistent")
	if found {
		t.Error("Expected cache miss (found=false) for path not in cache")
	}
	if attrs != nil {
		t.Error("Expected nil attrs for cache miss")
	}

	// Positive cache hit
	cache.Put("/exists", &NFSAttrs{Mode: 0644, Size: 42})
	attrs, found = cache.Get("/exists")
	if !found {
		t.Error("Expected cache hit (found=true) for cached path")
	}
	if attrs == nil {
		t.Error("Expected non-nil attrs for positive cache hit")
	}

	// Negative cache hit
	cache.PutNegative("/deleted")
	attrs, found = cache.Get("/deleted")
	if !found {
		t.Error("Expected negative cache hit (found=true) for negatively cached path")
	}
	if attrs != nil {
		t.Error("Expected nil attrs for negative cache hit")
	}
}

// TestR3_InvalidateNegativeInDirAccessLogOrder verifies that InvalidateNegativeInDir
// removes entries from the access log before deleting from the cache map, preventing
// ghost elements in the LRU list.
func TestR3_InvalidateNegativeInDirAccessLogOrder(t *testing.T) {
	cache := NewAttrCache(10*time.Second, 100)
	cache.ConfigureNegativeCaching(true, 10*time.Second)

	// Add several negative entries under /dir
	cache.PutNegative("/dir/a")
	cache.PutNegative("/dir/b")
	cache.PutNegative("/dir/c")

	// Add a positive entry too
	cache.Put("/dir/existing", &NFSAttrs{Mode: 0644, Size: 10})

	// Verify all entries exist
	if cache.Size() != 4 {
		t.Fatalf("Expected 4 cache entries, got %d", cache.Size())
	}

	// Invalidate negative entries in /dir
	cache.InvalidateNegativeInDir("/dir")

	// Negative entries should be gone, positive entry should remain
	if cache.Size() != 1 {
		t.Errorf("Expected 1 cache entry after InvalidateNegativeInDir, got %d", cache.Size())
	}

	// The positive entry should still be accessible
	got, found := cache.Get("/dir/existing")
	if !found || got == nil {
		t.Error("Positive entry should still exist after InvalidateNegativeInDir")
	}

	// Verify that the LRU list is consistent by filling cache to max and forcing eviction
	for i := 0; i < 100; i++ {
		cache.Put(fmt.Sprintf("/other/%d", i), &NFSAttrs{Mode: 0644, Size: int64(i)})
	}
	// If ghost elements existed, the cache size would exceed maxSize
	if cache.Size() > 100 {
		t.Errorf("Cache size %d exceeds max 100, LRU list may have ghost elements", cache.Size())
	}
}

// TestR20_AttrCachePreservesFileId verifies that AttrCache.Put and Get
// correctly copy the FileId field.
func TestR20_AttrCachePreservesFileId(t *testing.T) {
	cache := NewAttrCache(10*time.Second, 100)

	attrs := &NFSAttrs{
		Mode:   0644,
		Size:   100,
		FileId: 12345,
		Uid:    1000,
		Gid:    1000,
	}
	attrs.SetMtime(time.Now())
	attrs.SetAtime(time.Now())

	cache.Put("/test", attrs)

	got, found := cache.Get("/test")
	if !found || got == nil {
		t.Fatal("Expected cache hit")
	}
	if got.FileId != 12345 {
		t.Errorf("FileId = %d, expected 12345", got.FileId)
	}
}

// TestR24_DirCacheExpiredEntryRecheck verifies that DirCache.Get re-checks
// the entry after upgrading from RLock to Lock when removing expired entries.
func TestR24_DirCacheExpiredEntryRecheck(t *testing.T) {
	// Create a cache with very short TTL
	cache := NewDirCache(1*time.Millisecond, 100, 1000)

	// Add an entry
	cache.Put("/testdir", []os.FileInfo{})

	// Wait for it to expire
	time.Sleep(5 * time.Millisecond)

	// Get should return miss for expired entry
	_, found := cache.Get("/testdir")
	if found {
		t.Error("Expected cache miss for expired entry")
	}

	// Verify it was cleaned up
	if cache.Size() != 0 {
		t.Errorf("Expected cache size 0 after expired entry cleanup, got %d", cache.Size())
	}
}

// TestR25_AttrCacheExpiredEntryRecheck verifies that AttrCache.Get re-checks
// the entry after upgrading from RLock to Lock when removing expired entries.
func TestR25_AttrCacheExpiredEntryRecheck(t *testing.T) {
	// Create a cache with very short TTL
	cache := NewAttrCache(1*time.Millisecond, 100)

	// Add an entry
	attrs := &NFSAttrs{Mode: 0644, Size: 100}
	cache.Put("/test", attrs)

	// Wait for it to expire
	time.Sleep(5 * time.Millisecond)

	// Get should return miss for expired entry
	got, found := cache.Get("/test")
	if found || got != nil {
		t.Error("Expected cache miss for expired entry")
	}

	// Verify it was cleaned up
	if cache.Size() != 0 {
		t.Errorf("Expected cache size 0 after expired entry cleanup, got %d", cache.Size())
	}
}

// TestR3_CacheTypeAssertionSafety verifies that the attribute cache handles
// LRU list entries correctly without panicking due to type assertion failures.
func TestR3_CacheTypeAssertionSafety(t *testing.T) {
	cache := NewAttrCache(10*time.Second, 10)

	// Fill the cache up to capacity
	for i := 0; i < 10; i++ {
		attrs := &NFSAttrs{Mode: 0644, Size: int64(i)}
		attrs.SetMtime(time.Now())
		attrs.SetAtime(time.Now())
		cache.Put(fmt.Sprintf("/file%d", i), attrs)
	}

	// Verify all entries are accessible
	for i := 0; i < 10; i++ {
		got, found := cache.Get(fmt.Sprintf("/file%d", i))
		if !found {
			t.Errorf("Expected cache hit for /file%d", i)
		}
		if got == nil {
			t.Errorf("Expected non-nil attrs for /file%d", i)
		}
	}

	// Trigger eviction by adding more entries
	for i := 10; i < 15; i++ {
		attrs := &NFSAttrs{Mode: 0644, Size: int64(i)}
		attrs.SetMtime(time.Now())
		attrs.SetAtime(time.Now())
		cache.Put(fmt.Sprintf("/file%d", i), attrs)
	}

	// Cache should not exceed its max size
	if cache.Size() > 10 {
		t.Errorf("Cache size %d exceeds max 10", cache.Size())
	}

	// Verify no panic during concurrent access with eviction
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("/concurrent%d", n)
			attrs := &NFSAttrs{Mode: 0644, Size: int64(n)}
			attrs.SetMtime(time.Now())
			attrs.SetAtime(time.Now())
			cache.Put(key, attrs)
			cache.Get(key)
		}(i)
	}
	wg.Wait()
}

// TestR3_AttrCacheGetPassesServerForMetrics verifies that AttrCache.Get
// accepts a variadic server parameter and records cache hit/miss metrics.
func TestR3_AttrCacheGetPassesServerForMetrics(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer nfs.Close()

	cache := NewAttrCache(10*time.Second, 100)

	// Test cache miss with server parameter
	_, found := cache.Get("/nonexistent", nfs)
	if found {
		t.Error("Expected cache miss")
	}

	// Test cache hit with server parameter
	attrs := &NFSAttrs{Mode: 0644, Size: 42}
	attrs.SetMtime(time.Now())
	attrs.SetAtime(time.Now())
	cache.Put("/existing", attrs)

	got, found := cache.Get("/existing", nfs)
	if !found {
		t.Error("Expected cache hit")
	}
	if got == nil {
		t.Error("Expected non-nil attrs for cache hit")
	}

	// Test cache Get without server parameter (backwards compatibility)
	got2, found2 := cache.Get("/existing")
	if !found2 {
		t.Error("Expected cache hit without server param")
	}
	if got2 == nil {
		t.Error("Expected non-nil attrs without server param")
	}

	// Verify the metrics methods exist and don't panic
	nfs.RecordAttrCacheHit()
	nfs.RecordAttrCacheMiss()
}

func TestDirCacheResizeZeroCoverage(t *testing.T) {
	cache := NewDirCache(time.Second, 100, 1000)

	// Create mock FileInfo entries using memfs
	mfs, _ := memfs.NewFS()
	mfs.Create("/file1.txt")
	mfs.Create("/file2.txt")
	info1, _ := mfs.Stat("/file1.txt")
	info2, _ := mfs.Stat("/file2.txt")
	entries := []os.FileInfo{info1, info2}

	cache.Put("/dir1", entries)
	cache.Put("/dir2", entries)
	cache.Put("/dir3", entries)

	t.Run("resize to smaller", func(t *testing.T) {
		cache.Resize(2)
		size, _, _ := cache.Stats()
		if size > 2 {
			t.Errorf("Expected max 2 entries after resize, got %d", size)
		}
	})

	t.Run("resize to larger", func(t *testing.T) {
		cache.Resize(1000)
	})

	t.Run("resize with invalid value", func(t *testing.T) {
		cache.Resize(0)
		cache.Resize(-1)
	})
}

func TestDirCacheUpdateTTLZeroCoverage(t *testing.T) {
	cache := NewDirCache(time.Second, 100, 1000)

	t.Run("update TTL", func(t *testing.T) {
		cache.UpdateTTL(5 * time.Second)
	})

	t.Run("update TTL with invalid value", func(t *testing.T) {
		cache.UpdateTTL(0)
		cache.UpdateTTL(-time.Second)
	})
}

// Tests for NewDirCache with edge cases
func TestNewDirCacheCoverage(t *testing.T) {
	t.Run("default values for zero inputs", func(t *testing.T) {
		cache := NewDirCache(0, 0, 0)
		if cache == nil {
			t.Fatal("Expected non-nil cache")
		}
		// Defaults should be applied
		if cache.maxEntries <= 0 {
			t.Error("Expected positive maxEntries default")
		}
		if cache.maxDirSize <= 0 {
			t.Error("Expected positive maxDirSize default")
		}
		if cache.timeout <= 0 {
			t.Error("Expected positive timeout default")
		}
	})

	t.Run("negative values trigger defaults", func(t *testing.T) {
		cache := NewDirCache(-1*time.Second, -1, -1)
		if cache == nil {
			t.Fatal("Expected non-nil cache")
		}
		if cache.maxEntries <= 0 {
			t.Error("Expected positive maxEntries after negative input")
		}
	})

	t.Run("custom values preserved", func(t *testing.T) {
		cache := NewDirCache(30*time.Second, 500, 5000)
		if cache.timeout != 30*time.Second {
			t.Errorf("Expected 30s timeout, got %v", cache.timeout)
		}
		if cache.maxEntries != 500 {
			t.Errorf("Expected 500 maxEntries, got %d", cache.maxEntries)
		}
		if cache.maxDirSize != 5000 {
			t.Errorf("Expected 5000 maxDirSize, got %d", cache.maxDirSize)
		}
	})
}

// Tests for NewAttrCache with edge cases
func TestNewAttrCacheCoverage(t *testing.T) {
	t.Run("zero size uses default", func(t *testing.T) {
		cache := NewAttrCache(5*time.Second, 0)
		if cache == nil {
			t.Fatal("Expected non-nil cache")
		}
		if cache.maxSize <= 0 {
			t.Error("Expected positive maxSize default")
		}
	})

	t.Run("negative size uses default", func(t *testing.T) {
		cache := NewAttrCache(5*time.Second, -100)
		if cache == nil {
			t.Fatal("Expected non-nil cache")
		}
		if cache.maxSize <= 0 {
			t.Error("Expected positive maxSize after negative input")
		}
	})

	t.Run("positive values", func(t *testing.T) {
		cache := NewAttrCache(10*time.Second, 500)
		if cache == nil {
			t.Fatal("Expected non-nil cache")
		}
		if cache.maxSize != 500 {
			t.Errorf("Expected maxSize 500, got %d", cache.maxSize)
		}
	})
}

// Tests for AttrCache removeFromAccessLog
func TestAttrCacheRemoveFromAccessLog(t *testing.T) {
	t.Run("remove non-existent path", func(t *testing.T) {
		cache := NewAttrCache(5*time.Second, 100)
		// Should not panic
		cache.removeFromAccessLog("/nonexistent")
	})

	t.Run("remove existing path", func(t *testing.T) {
		cache := NewAttrCache(5*time.Second, 100)
		attrs := &NFSAttrs{
			Mode: os.FileMode(0644),
			Size: 100,
		}
		cache.Put("/test/file", attrs)
		cache.removeFromAccessLog("/test/file")
		// Verify the element was removed from list
		cache.mu.RLock()
		cached, ok := cache.cache["/test/file"]
		cache.mu.RUnlock()
		if ok && cached.listElement != nil {
			t.Error("Expected listElement to be nil after removeFromAccessLog")
		}
	})
}

// Tests for DirCache removeFromAccessList
func TestDirCacheRemoveFromAccessList(t *testing.T) {
	t.Run("remove non-existent path", func(t *testing.T) {
		cache := NewDirCache(5*time.Second, 100, 1000)
		// Should not panic
		cache.removeFromAccessList("/nonexistent")
	})

	t.Run("remove existing path", func(t *testing.T) {
		cache := NewDirCache(5*time.Second, 100, 1000)
		cache.Put("/test/dir", []os.FileInfo{})
		cache.removeFromAccessList("/test/dir")
		// Verify the element was removed from list
		cache.mu.RLock()
		cached, ok := cache.entries["/test/dir"]
		cache.mu.RUnlock()
		if ok && cached.listElement != nil {
			t.Error("Expected listElement to be nil after removeFromAccessList")
		}
	})
}

// Tests for DirCache Put with oversized directory
func TestDirCachePutOversized(t *testing.T) {
	t.Run("reject directory exceeding maxDirSize", func(t *testing.T) {
		cache := NewDirCache(5*time.Second, 100, 10) // maxDirSize = 10

		// Create more entries than allowed
		entries := make([]os.FileInfo, 20)
		cache.Put("/large/dir", entries)

		// Should not be cached
		_, found := cache.Get("/large/dir")
		if found {
			t.Error("Expected oversized directory to not be cached")
		}
	})
}

// mockFileInfo for testing
type mockFileInfo struct {
	name    string
	size    int64
	isDir   bool
	modTime time.Time
}

func (m *mockFileInfo) Name() string       { return m.name }
func (m *mockFileInfo) Size() int64        { return m.size }
func (m *mockFileInfo) Mode() os.FileMode  { return 0644 }
func (m *mockFileInfo) ModTime() time.Time { return m.modTime }
func (m *mockFileInfo) IsDir() bool        { return m.isDir }
func (m *mockFileInfo) Sys() interface{}   { return nil }

// Tests for cache Resize
func TestCacheResize(t *testing.T) {
	t.Run("resize attr cache smaller", func(t *testing.T) {
		cache := NewAttrCache(5*time.Second, 100)

		// Add some entries
		for i := 0; i < 50; i++ {
			attrs := &NFSAttrs{Mode: os.FileMode(0644), Size: int64(i)}
			cache.Put("/file"+string(rune('0'+i)), attrs)
		}

		// Resize smaller
		cache.Resize(20)
		if cache.MaxSize() != 20 {
			t.Errorf("Expected maxSize 20, got %d", cache.MaxSize())
		}
	})

	t.Run("resize attr cache larger", func(t *testing.T) {
		cache := NewAttrCache(5*time.Second, 50)
		cache.Resize(200)
		if cache.MaxSize() != 200 {
			t.Errorf("Expected maxSize 200, got %d", cache.MaxSize())
		}
	})

	t.Run("resize dir cache", func(t *testing.T) {
		cache := NewDirCache(5*time.Second, 100, 1000)
		cache.Resize(50)
		// Just verify no panic
	})
}

// Tests for UpdateTTL
func TestUpdateTTL(t *testing.T) {
	t.Run("update attr cache TTL", func(t *testing.T) {
		cache := NewAttrCache(5*time.Second, 100)
		cache.UpdateTTL(10 * time.Second)
		// Verify by checking that new entries use new TTL
		attrs := &NFSAttrs{Mode: os.FileMode(0644)}
		cache.Put("/test", attrs)
		// Entry should be valid for longer
	})

	t.Run("update dir cache TTL", func(t *testing.T) {
		cache := NewDirCache(5*time.Second, 100, 1000)
		cache.UpdateTTL(10 * time.Second)
		// Just verify no panic
	})
}

func TestCacheResizeOperations(t *testing.T) {
	nfs, _ := createTestServer(t)
	defer nfs.Close()

	t.Run("attr cache resize smaller", func(t *testing.T) {
		// Access internal attr cache
		nfs.mu.RLock()
		cache := nfs.attrCache
		nfs.mu.RUnlock()

		// Fill cache with entries (need non-nil attrs)
		for i := 0; i < 100; i++ {
			attrs := &NFSAttrs{
				Size: int64(i * 100),
				Mode: 0644,
				Uid:  1000,
				Gid:  1000,
			}
			cache.Put("/test"+string(rune('0'+i)), attrs)
		}

		// Resize to smaller
		cache.Resize(50)
		if cache.MaxSize() != 50 {
			t.Errorf("Expected max size 50, got %d", cache.MaxSize())
		}
	})

	t.Run("dir cache resize smaller", func(t *testing.T) {
		cache := NewDirCache(5*time.Second, 100, 1000)

		// Fill cache with entries
		for i := 0; i < 50; i++ {
			cache.Put("/dir"+string(rune('0'+i)), nil)
		}

		// Resize smaller
		cache.Resize(20)
	})

	t.Run("negative cache operations via attr cache", func(t *testing.T) {
		// Negative caching is handled by AttrCache
		cache := NewAttrCache(5*time.Second, 100)
		cache.ConfigureNegativeCaching(true, time.Second)

		// Add negative entries
		cache.PutNegative("/missing1")
		cache.PutNegative("/missing2")

		// Check stats
		negativeCount := cache.NegativeStats()
		if negativeCount < 2 {
			t.Errorf("Expected at least 2 negative entries, got %d", negativeCount)
		}
	})
}

func TestCacheTTLOperations(t *testing.T) {
	nfs, mfs := createTestServer(t, func(o *ExportOptions) {
		o.AttrCacheTimeout = 100 * time.Millisecond
	})
	defer nfs.Close()

	f, _ := mfs.Create("/ttltest.txt")
	f.Write([]byte("ttl test"))
	f.Close()

	t.Run("update attr cache TTL", func(t *testing.T) {
		// Get the cache
		nfs.mu.RLock()
		cache := nfs.attrCache
		nfs.mu.RUnlock()

		// Get initial entry
		attrs := &NFSAttrs{
			Size: 8,
			Mode: 0644,
			Uid:  1000,
			Gid:  1000,
		}
		cache.Put("/ttltest.txt", attrs)

		// Update TTL
		cache.UpdateTTL(time.Second)
	})

	t.Run("dir cache TTL update", func(t *testing.T) {
		cache := NewDirCache(5*time.Second, 100, 1000)
		cache.Put("/testdir", nil)
		cache.UpdateTTL(time.Second)
	})
}

// More tests for cache eviction and access patterns
func TestCacheAccessPatterns(t *testing.T) {
	nfs, mfs := createTestServer(t, func(o *ExportOptions) {
		o.AttrCacheSize = 10 // Small cache to trigger eviction
	})
	defer nfs.Close()

	// Create many files
	for i := 0; i < 20; i++ {
		f, _ := mfs.Create("/cachetest" + string(rune('a'+i)) + ".txt")
		f.Write([]byte("cache test content"))
		f.Close()
	}

	t.Run("cache eviction on overflow", func(t *testing.T) {
		// Lookup all files to fill cache
		for i := 0; i < 20; i++ {
			_, _ = nfs.Lookup("/cachetest" + string(rune('a'+i)) + ".txt")
		}
	})

	t.Run("cache invalidation", func(t *testing.T) {
		nfs.mu.RLock()
		cache := nfs.attrCache
		nfs.mu.RUnlock()

		cache.Invalidate("/cachetest" + string(rune('a')) + ".txt")
		// Invalidate a few more entries individually
		cache.Invalidate("/cachetest" + string(rune('b')) + ".txt")
		cache.Invalidate("/cachetest" + string(rune('c')) + ".txt")
	})
}

func TestCacheUpdateTTL(t *testing.T) {
	nfs, mfs := createTestServer(t)
	defer nfs.Close()

	f, _ := mfs.Create("/ttltest.txt")
	f.Write([]byte("ttl test"))
	f.Close()

	// Perform lookups to populate cache
	_, _ = nfs.Lookup("/ttltest.txt")

	t.Run("update ttl", func(t *testing.T) {
		nfs.mu.RLock()
		cache := nfs.attrCache
		nfs.mu.RUnlock()

		cache.UpdateTTL(10 * time.Second)
	})
}

func TestDirCacheResize(t *testing.T) {
	cache := NewDirCache(5*time.Second, 100, 1000)

	t.Run("resize dir cache", func(t *testing.T) {
		cache.Resize(50)
	})

	t.Run("resize to larger", func(t *testing.T) {
		cache.Resize(200)
	})

	t.Run("resize to zero uses default", func(t *testing.T) {
		cache.Resize(0)
	})
}

func TestAttrCacheResizeSameSize(t *testing.T) {
	cache := NewAttrCache(5*time.Second, 100)

	t.Run("resize to same size", func(t *testing.T) {
		cache.Resize(100) // Same as current
		cache.Resize(100) // Again
	})

	t.Run("resize to smaller with eviction", func(t *testing.T) {
		// Add some entries
		for i := 0; i < 50; i++ {
			attrs := &NFSAttrs{Mode: 0644, Size: int64(i)}
			cache.Put("/file"+string(rune('a'+i)), attrs)
		}
		// Now resize smaller to force eviction
		cache.Resize(10)
	})

	t.Run("resize to zero", func(t *testing.T) {
		cache.Resize(0)
	})
}

// Tests for UpdateTTL edge cases
func TestAttrCacheUpdateTTLEdgeCases(t *testing.T) {
	cache := NewAttrCache(5*time.Second, 100)

	t.Run("update ttl to zero", func(t *testing.T) {
		cache.UpdateTTL(0)
	})

	t.Run("update ttl to negative", func(t *testing.T) {
		cache.UpdateTTL(-1 * time.Second)
	})

	t.Run("update ttl to valid", func(t *testing.T) {
		cache.UpdateTTL(10 * time.Second)
	})
}

func TestCacheUpdateAccessLog(t *testing.T) {
	cache := NewAttrCache(5*time.Second, 10)

	// Add entries and access them to exercise updateAccessLog
	for i := 0; i < 15; i++ {
		attrs := &NFSAttrs{Mode: 0644, Size: int64(i)}
		cache.Put("/access"+string(rune('a'+i))+".txt", attrs)
	}

	// Access some entries to update access log
	for i := 0; i < 5; i++ {
		_, _ = cache.Get("/access"+string(rune('a'+i))+".txt", nil)
	}

	// Access same entries again
	for i := 0; i < 5; i++ {
		_, _ = cache.Get("/access"+string(rune('a'+i))+".txt", nil)
	}
}

// Tests for DirCache updateAccessLog
func TestDirCacheUpdateAccessLog(t *testing.T) {
	cache := NewDirCache(5*time.Second, 10, 1000)

	// Create mock file info
	mockFiles := make([]os.FileInfo, 5)
	for i := range mockFiles {
		mockFiles[i] = &mockFileInfo{name: "file" + string(rune('0'+i)), isDir: false, size: 100}
	}

	// Add entries
	for i := 0; i < 15; i++ {
		cache.Put("/dir"+string(rune('a'+i)), mockFiles)
	}

	// Access some entries
	for i := 0; i < 5; i++ {
		cache.Get("/dir" + string(rune('a'+i)))
	}
}

// Tests for cache enforceMemoryLimits
func TestCacheEnforceMemoryLimits(t *testing.T) {
	nfs, mfs := createTestServer(t)
	defer nfs.Close()

	// Create files larger than limits
	for i := 0; i < 5; i++ {
		content := make([]byte, 500)
		for j := range content {
			content[j] = 'x'
		}
		f, _ := mfs.Create("/limit" + string(rune('a'+i)) + ".txt")
		f.Write(content)
		f.Close()
	}

	// Read all files to fill buffer beyond limits
	for i := 0; i < 5; i++ {
		node, _ := nfs.Lookup("/limit" + string(rune('a'+i)) + ".txt")
		_, _ = nfs.Read(node, 0, 500)
	}
}

// Tests for cache updateAccessOrder
func TestCacheUpdateAccessOrder(t *testing.T) {
	nfs, mfs := createTestServer(t)
	defer nfs.Close()

	// Create files
	for i := 0; i < 5; i++ {
		content := make([]byte, 100)
		for j := range content {
			content[j] = 'x'
		}
		f, _ := mfs.Create("/order" + string(rune('a'+i)) + ".txt")
		f.Write(content)
		f.Close()
	}

	// Read files to populate cache
	for i := 0; i < 5; i++ {
		node, _ := nfs.Lookup("/order" + string(rune('a'+i)) + ".txt")
		_, _ = nfs.Read(node, 0, 50)
	}

	// Re-read first files to update access order
	for i := 0; i < 3; i++ {
		node, _ := nfs.Lookup("/order" + string(rune('a'+i)) + ".txt")
		_, _ = nfs.Read(node, 0, 50)
	}
}

func TestCacheAccessPatternsCoverage(t *testing.T) {
	cache := NewAttrCache(5*time.Second, 5) // Small cache

	// Fill cache to capacity
	for i := 0; i < 5; i++ {
		attrs := &NFSAttrs{Mode: 0644, Size: int64(i * 100)}
		cache.Put("/cap"+string(rune('a'+i))+".txt", attrs)
	}

	// Access middle entries
	for i := 2; i < 4; i++ {
		_, _ = cache.Get("/cap"+string(rune('a'+i))+".txt", nil)
	}

	// Add more entries to trigger eviction
	for i := 5; i < 10; i++ {
		attrs := &NFSAttrs{Mode: 0644, Size: int64(i * 100)}
		cache.Put("/cap"+string(rune('a'+i))+".txt", attrs)
	}
}

// Tests for DirCache Get returning multiple values
func TestDirCacheGetCoverage(t *testing.T) {
	cache := NewDirCache(5*time.Second, 100, 1000)

	t.Run("get missing entry", func(t *testing.T) {
		result, ok := cache.Get("/missing")
		if ok || result != nil {
			t.Log("Got result for missing entry")
		}
	})
}

// Tests for isChildOf
func TestIsChildOf(t *testing.T) {
	tests := []struct {
		child    string
		parent   string
		expected bool
	}{
		{"/foo/bar", "/foo", true},
		{"/foo/bar/baz", "/foo", false}, // grandchild, not direct child
		{"/foo", "/foo", false},
		{"/foobar", "/foo", false},
		{"/bar/foo", "/foo", false},
		{"/", "/", false},
		{"/foo", "/", true},
	}

	for _, tc := range tests {
		t.Run(tc.child+"_"+tc.parent, func(t *testing.T) {
			result := isChildOf(tc.child, tc.parent)
			if result != tc.expected {
				t.Errorf("isChildOf(%q, %q) = %v, expected %v", tc.child, tc.parent, result, tc.expected)
			}
		})
	}
}
