package absnfs

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestAttrCache(t *testing.T) {
	t.Run("basic operations", func(t *testing.T) {
		cache := NewAttrCache(2*time.Second, 1000)

		// Test initial state
		if attrs := cache.Get("/test.txt", nil); attrs != nil {
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
		cachedAttrs := cache.Get("/test.txt", nil)
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
		if attrs := cache.Get("/test.txt", nil); attrs != nil {
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
		if attrs := cache.Get("/file0.txt", nil); attrs != nil {
			t.Error("Expected early entry to be evicted")
		}
		if attrs := cache.Get("/file9.txt", nil); attrs == nil {
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
					_ = cache.Get(path, nil)

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
