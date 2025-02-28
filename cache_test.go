package absnfs

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/absfs/memfs"
)

func TestAttrCache(t *testing.T) {
	t.Run("basic operations", func(t *testing.T) {
		cache := NewAttrCache(2 * time.Second, 1000)

		// Test initial state
		if attrs := cache.Get("/test.txt"); attrs != nil {
			t.Error("Expected nil for non-existent entry")
		}

		// Test Put and Get
		initialAttrs := &NFSAttrs{
			Mode:  0644,
			Size:  1234,
			Mtime: time.Now(),
			Atime: time.Now(),
			Uid:   1000,
			Gid:   1000,
		}
		cache.Put("/test.txt", initialAttrs)

		// Get should return a copy, not the original
		cachedAttrs := cache.Get("/test.txt")
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
		if attrs := cache.Get("/test.txt"); attrs != nil {
			t.Error("Expected nil for expired entry")
		}

		// Test Invalidate
		cache.Put("/test.txt", initialAttrs)
		cache.Invalidate("/test.txt")
		if attrs := cache.Get("/test.txt"); attrs != nil {
			t.Error("Expected nil after invalidation")
		}

		// Test Clear
		cache.Put("/test1.txt", initialAttrs)
		cache.Put("/test2.txt", initialAttrs)
		cache.Clear()
		if attrs := cache.Get("/test1.txt"); attrs != nil {
			t.Error("Expected nil after clear")
		}
		if attrs := cache.Get("/test2.txt"); attrs != nil {
			t.Error("Expected nil after clear")
		}
	})

	t.Run("concurrent access", func(t *testing.T) {
		cache := NewAttrCache(2 * time.Second, 1000)
		var wg sync.WaitGroup
		const goroutines = 10
		errChan := make(chan error, goroutines*2) // For both readers and writers

		// Start concurrent writers
		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				path := fmt.Sprintf("/test%d.txt", i)
				attrs := &NFSAttrs{
					Mode:  0644,
					Size:  int64(i),
					Mtime: time.Now(),
					Atime: time.Now(),
					Uid:   uint32(1000 + i),
					Gid:   uint32(1000 + i),
				}
				cache.Put(path, attrs)

				// Verify attributes were cached correctly
				cached := cache.Get(path)
				if cached == nil {
					errChan <- fmt.Errorf("failed to get cached attributes for path %s", path)
					return
				}
				if cached.Size != int64(i) || cached.Uid != uint32(1000+i) {
					errChan <- fmt.Errorf("attribute mismatch for path %s", path)
				}
			}(i)
		}

		// Start concurrent readers/invalidators
		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				path := fmt.Sprintf("/test%d.txt", i)
				// Mix of reads and invalidations
				if i%2 == 0 {
					cache.Get(path)
				} else {
					cache.Invalidate(path)
				}
			}(i)
		}

		wg.Wait()
		close(errChan)

		for err := range errChan {
			t.Error(err)
		}
	})
}

func TestCacheInvalidation(t *testing.T) {
	memfs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	fs, err := New(memfs, ExportOptions{})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}

	// Create test file
	f, err := memfs.Create("/test.txt")
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	f.Close()

	// Get initial attributes
	node, err := fs.Lookup("/test.txt")
	if err != nil {
		t.Fatalf("Failed to lookup test file: %v", err)
	}

	// Cache should be valid
	if !node.attrs.IsValid() {
		t.Error("Expected attributes to be valid after lookup")
	}

	// Wait for cache to expire
	time.Sleep(2 * time.Second)

	// Cache should be invalid
	if node.attrs.IsValid() {
		t.Error("Expected attributes to be invalid after timeout")
	}

	// Getting attributes should refresh cache
	attrs, err := fs.GetAttr(node)
	if err != nil {
		t.Fatalf("Failed to get attributes: %v", err)
	}

	if !attrs.IsValid() {
		t.Error("Expected attributes to be valid after refresh")
	}

	// Test cache invalidation on write
	f, err = memfs.OpenFile("/test.txt", 0x02, 0644) // O_RDWR
	if err != nil {
		t.Fatalf("Failed to open test file: %v", err)
	}
	defer f.Close()

	if _, err := f.Write([]byte("test")); err != nil {
		t.Fatalf("Failed to write to test file: %v", err)
	}

	// Cache should be invalid after write
	if node.attrs.IsValid() {
		t.Error("Expected attributes to be invalid after write")
	}
}

func TestFileHandleManagement(t *testing.T) {
	memfs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	fs, err := New(memfs, ExportOptions{})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}

	// Create test files
	files := []string{"/test1.txt", "/test2.txt", "/test3.txt"}
	for _, path := range files {
		f, err := memfs.Create(path)
		if err != nil {
			t.Fatalf("Failed to create test file %s: %v", path, err)
		}
		f.Close()
	}

	// Get handles for all files
	var handles []uint64
	var nodes []*NFSNode
	for _, path := range files {
		node, err := fs.Lookup(path)
		if err != nil {
			t.Fatalf("Failed to lookup %s: %v", path, err)
		}
		handle := fs.fileMap.Allocate(node)
		handles = append(handles, handle)
		nodes = append(nodes, node)
	}

	// Verify all handles are unique
	seen := make(map[uint64]bool)
	for _, handle := range handles {
		if seen[handle] {
			t.Error("Duplicate handle allocated")
		}
		seen[handle] = true
	}

	// Verify handle lookup works
	for i, handle := range handles {
		file, ok := fs.fileMap.Get(handle)
		if !ok {
			t.Errorf("Failed to get node for handle %d", handle)
			continue
		}
		node, ok := file.(*NFSNode)
		if !ok {
			t.Errorf("Expected *NFSNode, got %T", file)
			continue
		}
		if node.path != files[i] {
			t.Errorf("Wrong node returned for handle %d: got %s, want %s", handle, node.path, files[i])
		}
	}

	// Test handle reuse after release
	fs.fileMap.Release(handles[0])
	// Create test4.txt first
	f, err := memfs.Create("/test4.txt")
	if err != nil {
		t.Fatalf("Failed to create test4.txt: %v", err)
	}
	f.Close()

	newNode, err := fs.Lookup("/test4.txt")
	if err != nil {
		t.Fatalf("Failed to lookup test4.txt: %v", err)
	}
	newHandle := fs.fileMap.Allocate(newNode)

	// The new handle should reuse the released handle
	if newHandle != handles[0] {
		t.Error("Handle not reused after release")
	}

	// Test concurrent handle allocation
	var wg sync.WaitGroup
	var mu sync.Mutex
	errors := make(chan error, 10)

	// Create files first to avoid race conditions
	for i := 0; i < 10; i++ {
		path := fmt.Sprintf("/concurrent%d.txt", i)
		f, err := memfs.Create(path)
		if err != nil {
			t.Fatalf("Failed to create test file %s: %v", path, err)
		}
		f.Close()
	}

	// Now test concurrent handle allocation
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := fmt.Sprintf("/concurrent%d.txt", i)

			mu.Lock()
			node, err := fs.Lookup(path)
			if err != nil {
				errors <- fmt.Errorf("Failed to lookup %s: %v", path, err)
				mu.Unlock()
				return
			}
			handle := fs.fileMap.Allocate(node)
			mu.Unlock()

			if handle == 0 {
				errors <- fmt.Errorf("Invalid handle allocated for %s", path)
			}
		}(i)
	}

	// Wait for all goroutines to finish
	wg.Wait()
	close(errors)

	// Check for any errors
	for err := range errors {
		t.Error(err)
	}
}

func TestCacheConsistency(t *testing.T) {
	memfs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	fs, err := New(memfs, ExportOptions{})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}

	// Create test file
	f, err := memfs.Create("/test.txt")
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	if _, err := f.Write([]byte("initial")); err != nil {
		t.Fatalf("Failed to write initial content: %v", err)
	}
	f.Close()

	// Get initial node and attributes
	node, err := fs.Lookup("/test.txt")
	if err != nil {
		t.Fatalf("Failed to lookup test file: %v", err)
	}

	initialAttrs, err := fs.GetAttr(node)
	if err != nil {
		t.Fatalf("Failed to get initial attributes: %v", err)
	}

	// Add a small delay to ensure modification time will be different
	time.Sleep(10 * time.Millisecond)

	// Modify file through NFS interface
	_, err = fs.Write(node, 0, []byte("modified"))
	if err != nil {
		t.Fatalf("Failed to write modified content: %v", err)
	}

	// Add another small delay to ensure modification time is updated
	time.Sleep(10 * time.Millisecond)

	// Cache should be invalid after external modification
	// Force cache invalidation since we modified the file externally
	node.attrs.Invalidate()

	// Get new attributes
	newAttrs, err := fs.GetAttr(node)
	if err != nil {
		t.Fatalf("Failed to get new attributes: %v", err)
	}

	// Size should be different
	if newAttrs.Size == initialAttrs.Size {
		t.Error("Expected file size to change after modification")
	}

	// Modification time should be different
	if newAttrs.Mtime.UnixNano() <= initialAttrs.Mtime.UnixNano() {
		t.Error("Expected modification time to increase")
	}
}

func TestReadAheadBuffer(t *testing.T) {
	// Test basic read-ahead functionality
	t.Run("basic operations", func(t *testing.T) {
		buf := NewReadAheadBuffer(1024)

		// Test initial state
		data, ok := buf.Read("/test.txt", 0, 10)
		if ok {
			t.Error("Expected read from empty buffer to fail")
		}
		if len(data) != 0 {
			t.Error("Expected empty data from failed read")
		}

		// Fill buffer
		testData := []byte("Hello, World!")
		buf.Fill("/test.txt", testData, 0)

		// Test successful read
		data, ok = buf.Read("/test.txt", 0, len(testData))
		if !ok {
			t.Error("Expected read to succeed")
		}
		if string(data) != string(testData) {
			t.Errorf("Wrong data returned: got %s, want %s", string(data), string(testData))
		}

		// Test partial read
		data, ok = buf.Read("/test.txt", 7, 5)
		if !ok {
			t.Error("Expected partial read to succeed")
		}
		if string(data) != "World" {
			t.Errorf("Wrong partial data: got %s, want World", string(data))
		}

		// Test read beyond EOF
		data, ok = buf.Read("/test.txt", int64(len(testData)), 5)
		if !ok {
			t.Error("Expected EOF read to succeed")
		}
		if len(data) != 0 {
			t.Error("Expected empty data for EOF read")
		}

		// Test read before buffer start
		data, ok = buf.Read("/test.txt", -1, 5)
		if ok {
			t.Error("Expected read before buffer to fail")
		}

		// Test wrong path
		data, ok = buf.Read("/other.txt", 0, 5)
		if ok {
			t.Error("Expected read from wrong path to fail")
		}

		// Test clear
		buf.Clear()
		data, ok = buf.Read("/test.txt", 0, 5)
		if ok {
			t.Error("Expected read after clear to fail")
		}
	})

	// Test concurrent access
	t.Run("concurrent access", func(t *testing.T) {
		// Test concurrent reads
		t.Run("concurrent reads", func(t *testing.T) {
			readBuf := NewReadAheadBuffer(1024)
			var wg sync.WaitGroup
			const goroutines = 10
			errChan := make(chan error, goroutines)

			// Fill buffer
			testData := []byte("Hello, World!")
			readBuf.Fill("/test.txt", testData, 0)

			// Start concurrent readers
			for i := 0; i < goroutines; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					data, ok := readBuf.Read("/test.txt", 0, len(testData))
					if !ok {
						errChan <- fmt.Errorf("concurrent read failed")
						return
					}
					if string(data) != string(testData) {
						errChan <- fmt.Errorf("wrong data in concurrent read: got %s, want %s", string(data), string(testData))
					}
				}()
			}

			wg.Wait()
			close(errChan)

			for err := range errChan {
				t.Error(err)
			}
		})

		// Test concurrent writes
		t.Run("concurrent writes", func(t *testing.T) {
			var wg sync.WaitGroup
			const goroutines = 10
			errChan := make(chan error, goroutines)

			// Create a buffer for each writer
			buffers := make([]*ReadAheadBuffer, goroutines)
			for i := range buffers {
				buffers[i] = NewReadAheadBuffer(1024)
			}

			// Start concurrent writers
			for i := 0; i < goroutines; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					path := fmt.Sprintf("/test%d.txt", i)
					newData := []byte(fmt.Sprintf("Data %d", i))
					buffers[i].Fill(path, newData, 0)

					// Verify written data can be read back
					data, ok := buffers[i].Read(path, 0, len(newData))
					if !ok {
						errChan <- fmt.Errorf("failed to read back written data for path %s", path)
						return
					}
					if string(data) != string(newData) {
						errChan <- fmt.Errorf("data mismatch after write for path %s: got %s, want %s", path, string(data), string(newData))
					}
				}(i)
			}

			wg.Wait()
			close(errChan)

			for err := range errChan {
				t.Error(err)
			}
		})
	})

	// Test buffer size limits
	t.Run("buffer size limits", func(t *testing.T) {
		// Test with small buffer
		smallBuf := NewReadAheadBuffer(10)
		testData := []byte("This is a test that exceeds buffer size")
		smallBuf.Fill("/test.txt", testData, 0)

		// Read should still work but truncate data
		data, ok := smallBuf.Read("/test.txt", 0, len(testData))
		if !ok {
			t.Error("Expected read to succeed with truncation")
		}
		if len(data) > 10 {
			t.Error("Data should be truncated to buffer size")
		}

		// Test with exact buffer size
		exactBuf := NewReadAheadBuffer(len(testData))
		exactBuf.Fill("/test.txt", testData, 0)
		data, ok = exactBuf.Read("/test.txt", 0, len(testData))
		if !ok {
			t.Error("Expected read to succeed")
		}
		if string(data) != string(testData) {
			t.Error("Data should match exactly")
		}
	})
}