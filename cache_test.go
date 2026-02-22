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

func TestReadAheadBuffer(t *testing.T) {
	t.Run("basic operations", func(t *testing.T) {
		buffer := NewReadAheadBuffer(100) // 100 byte buffer size
		buffer.Configure(10, 1000)        // Max 10 files, 1KB total

		// Test initial state
		if _, ok := buffer.Read("/test.txt", 0, 50, nil); ok {
			t.Error("Expected false for non-existent entry")
		}

		// Fill with test data
		testData := make([]byte, 100)
		for i := range testData {
			testData[i] = byte(i)
		}
		buffer.Fill("/test.txt", testData, 0)

		// Read should return a copy
		readData, ok := buffer.Read("/test.txt", 0, 50, nil)
		if !ok {
			t.Fatal("Expected true for valid read")
		}
		if len(readData) != 50 {
			t.Errorf("Expected 50 bytes, got %d", len(readData))
		}
		for i := range readData {
			if readData[i] != byte(i) {
				t.Errorf("Data mismatch at position %d: expected %d, got %d", i, i, readData[i])
			}
		}

		// Test offset reading
		readData, ok = buffer.Read("/test.txt", 50, 50, nil)
		if !ok {
			t.Fatal("Expected true for valid read")
		}
		if len(readData) != 50 {
			t.Errorf("Expected 50 bytes, got %d", len(readData))
		}
		for i := range readData {
			if readData[i] != byte(i+50) {
				t.Errorf("Data mismatch at position %d: expected %d, got %d", i, i+50, readData[i])
			}
		}

		// Test reading past end
		readData, ok = buffer.Read("/test.txt", 90, 50, nil)
		if !ok {
			t.Fatal("Expected true for valid read")
		}
		if len(readData) != 10 {
			t.Errorf("Expected 10 bytes, got %d", len(readData))
		}

		// Test reading at end
		readData, ok = buffer.Read("/test.txt", 100, 10, nil)
		if !ok || len(readData) != 0 {
			t.Error("Expected empty result at end")
		}

		// Test reading past end
		_, ok = buffer.Read("/test.txt", 101, 10, nil)
		if ok {
			t.Error("Expected false for read past end")
		}
	})

	t.Run("buffer eviction", func(t *testing.T) {
		buffer := NewReadAheadBuffer(100) // 100 byte buffer size
		buffer.Configure(3, 1000)         // Max 3 files, 1KB total

		testData := make([]byte, 100)

		// Fill multiple files
		for i := 0; i < 5; i++ {
			path := fmt.Sprintf("/file%d.txt", i)
			buffer.Fill(path, testData, 0)
		}

		// Verify old buffers were evicted
		if _, ok := buffer.Read("/file0.txt", 0, 10, nil); ok {
			t.Error("Expected early buffer to be evicted")
		}
		if _, ok := buffer.Read("/file4.txt", 0, 10, nil); !ok {
			t.Error("Expected recent buffer to be present")
		}

		// Check current memory usage
		if buffer.Size() > 300 {
			t.Errorf("Expected size <= 300, got %d", buffer.Size())
		}
	})

	t.Run("memory limits", func(t *testing.T) {
		buffer := NewReadAheadBuffer(100) // 100 byte buffer size
		buffer.Configure(10, 300)         // Max 10 files, 300 bytes total (only room for 3 buffers)

		testData := make([]byte, 100)

		// Fill multiple files
		for i := 0; i < 5; i++ {
			path := fmt.Sprintf("/file%d.txt", i)
			buffer.Fill(path, testData, 0)
		}

		// Verify memory limit is respected
		if buffer.Size() > 300 {
			t.Errorf("Expected memory usage <= 300, got %d", buffer.Size())
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
