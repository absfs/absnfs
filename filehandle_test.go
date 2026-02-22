package absnfs

import (
	"fmt"
	"sync"
	"testing"

	"github.com/absfs/absfs"
	"github.com/absfs/memfs"
)

func TestFileHandleMap(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test files
	files := make([]string, 5)
	for i := range files {
		name := fmt.Sprintf("/test%d.txt", i)
		f, err := fs.Create(name)
		if err != nil {
			t.Fatalf("Failed to create test file %s: %v", name, err)
		}
		f.Close()
		files[i] = name
	}

	// Create file handle map
	fm := &FileHandleMap{
		handles:     make(map[uint64]absfs.File),
		nextHandle:  1,
		freeHandles: NewUint64MinHeap(),
	}

	// Test Allocate
	t.Run("allocate", func(t *testing.T) {
		var handles []uint64
		for _, name := range files {
			f, err := fs.OpenFile(name, 0, 0)
			if err != nil {
				t.Fatalf("Failed to open file %s: %v", name, err)
			}
			handle := fm.Allocate(f)
			if handle == 0 {
				t.Error("Expected non-zero handle")
			}
			handles = append(handles, handle)
		}

		// Verify handle count
		if count := fm.Count(); count != len(files) {
			t.Errorf("Expected %d handles, got %d", len(files), count)
		}

		// Verify handles are sequential starting from 1
		for i, handle := range handles {
			if handle != uint64(i+1) {
				t.Errorf("Expected handle %d, got %d", i+1, handle)
			}
		}
	})

	// Test Get
	t.Run("get", func(t *testing.T) {
		f, err := fs.OpenFile(files[0], 0, 0)
		if err != nil {
			t.Fatalf("Failed to open file: %v", err)
		}
		handle := fm.Allocate(f)

		// Test valid handle
		if got, exists := fm.Get(handle); !exists {
			t.Error("Expected handle to exist")
		} else if got != f {
			t.Error("Got wrong file for handle")
		}

		// Test invalid handle
		if _, exists := fm.Get(99999); exists {
			t.Error("Expected handle to not exist")
		}
	})

	// Test Release
	t.Run("release", func(t *testing.T) {
		f, err := fs.OpenFile(files[0], 0, 0)
		if err != nil {
			t.Fatalf("Failed to open file: %v", err)
		}
		handle := fm.Allocate(f)

		// Release the handle
		fm.Release(handle)

		// Verify handle is gone
		if _, exists := fm.Get(handle); exists {
			t.Error("Expected handle to be released")
		}

		// Test releasing non-existent handle (should not panic)
		fm.Release(99999)
	})

	// Test ReleaseAll
	t.Run("release all", func(t *testing.T) {
		// Allocate several handles
		var handles []uint64
		for _, name := range files {
			f, err := fs.OpenFile(name, 0, 0)
			if err != nil {
				t.Fatalf("Failed to open file %s: %v", name, err)
			}
			handle := fm.Allocate(f)
			handles = append(handles, handle)
		}

		// Release all handles
		fm.ReleaseAll()

		// Verify all handles are gone
		if count := fm.Count(); count != 0 {
			t.Errorf("Expected 0 handles after ReleaseAll, got %d", count)
		}

		// Verify each handle is gone
		for _, handle := range handles {
			if _, exists := fm.Get(handle); exists {
				t.Errorf("Handle %d still exists after ReleaseAll", handle)
			}
		}
	})

	// Test concurrent operations
	t.Run("concurrent operations", func(t *testing.T) {
		// Create test files first
		testFiles := make([]string, 5)
		for i := range testFiles {
			name := fmt.Sprintf("/concurrent%d.txt", i)
			f, err := fs.Create(name)
			if err != nil {
				t.Fatalf("Failed to create test file %s: %v", name, err)
			}
			f.Close()
			testFiles[i] = name
		}

		var wg sync.WaitGroup
		errChan := make(chan error, 10)

		// Run concurrent operations on existing files
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				// Use modulo to cycle through available files
				name := testFiles[i%len(testFiles)]

				f, err := fs.OpenFile(name, 0, 0)
				if err != nil {
					errChan <- fmt.Errorf("Failed to open file %s: %v", name, err)
					return
				}

				// Allocate handle
				handle := fm.Allocate(f)

				// Get file
				if _, exists := fm.Get(handle); !exists {
					errChan <- fmt.Errorf("Failed to get handle %d", handle)
					return
				}

				// Release handle
				fm.Release(handle)
			}(i)
		}

		// Wait for all operations to complete
		wg.Wait()
		close(errChan)

		// Check for any errors
		for err := range errChan {
			t.Error(err)
		}

		// Verify all handles are cleaned up
		if count := fm.Count(); count != 0 {
			t.Errorf("Expected 0 handles after concurrent operations, got %d", count)
		}
	})
}

// TestR3_FileHandleMapEviction verifies that the FileHandleMap evicts
// the oldest handles when the maximum is exceeded.
func TestR3_FileHandleMapEviction(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer nfs.Close()

	// Create a FileHandleMap with a very small maximum
	fhMap := &FileHandleMap{
		handles:     make(map[uint64]absfs.File),
		freeHandles: NewUint64MinHeap(),
		maxHandles:  10,
	}

	// Create test files and allocate handles for them
	for i := 0; i < 10; i++ {
		fname := fmt.Sprintf("/evictfile%d", i)
		f, err := fs.Create(fname)
		if err != nil {
			t.Fatalf("Failed to create file %s: %v", fname, err)
		}
		fhMap.Allocate(f)
	}

	if fhMap.Count() != 10 {
		t.Fatalf("Expected 10 handles, got %d", fhMap.Count())
	}

	// Allocate one more to trigger eviction
	extraFile, err := fs.Create("/extra")
	if err != nil {
		t.Fatal(err)
	}
	fhMap.Allocate(extraFile)

	// After eviction, count should be <= maxHandles
	count := fhMap.Count()
	if count > 10 {
		t.Errorf("Expected count <= 10 after eviction, got %d", count)
	}

	// Should have evicted at least 1 (maxH/10 = 1)
	if count > 10 {
		t.Errorf("Eviction did not reduce handle count below max")
	}
}

// ================================================================
// Coverage boost: filehandle.go Allocate – eviction path
// ================================================================

func TestCovBoost_AllocateEviction(t *testing.T) {
	fm := &FileHandleMap{
		handles:     make(map[uint64]absfs.File),
		nextHandle:  1,
		freeHandles: NewUint64MinHeap(),
		maxHandles:  5, // tiny limit to trigger eviction
	}

	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		name := "/" + string(rune('a'+i)) + ".txt"
		f, _ := fs.Create(name)
		f.Close()
	}

	// Fill up to max
	for i := 0; i < 5; i++ {
		name := "/" + string(rune('a'+i)) + ".txt"
		f, _ := fs.OpenFile(name, 0, 0)
		fm.Allocate(f)
	}
	if fm.Count() != 5 {
		t.Fatalf("expected 5, got %d", fm.Count())
	}

	// Allocate one more – should trigger eviction of oldest
	f, _ := fs.Create("/extra.txt")
	f.Close()
	f2, _ := fs.OpenFile("/extra.txt", 0, 0)
	fm.Allocate(f2)

	// After eviction of maxHandles/10 = 0 (rounds up to 1), count should be 5
	if fm.Count() > 5 {
		t.Errorf("expected count <= 5 after eviction, got %d", fm.Count())
	}
}

func TestCovBoost_AllocateEvictionSmallMax(t *testing.T) {
	// maxHandles=1: evictCount = 1/10 = 0 -> clamped to 1
	fm := &FileHandleMap{
		handles:     make(map[uint64]absfs.File),
		nextHandle:  1,
		freeHandles: NewUint64MinHeap(),
		maxHandles:  1,
	}

	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	fs.Create("/a.txt")
	fs.Create("/b.txt")

	f1, _ := fs.OpenFile("/a.txt", 0, 0)
	fm.Allocate(f1)
	f2, _ := fs.OpenFile("/b.txt", 0, 0)
	fm.Allocate(f2)

	if fm.Count() != 1 {
		t.Errorf("expected 1 after eviction with maxHandles=1, got %d", fm.Count())
	}
}

func TestFileHandleMapGetOrErrorZeroCoverage(t *testing.T) {
	fm := &FileHandleMap{
		handles:     make(map[uint64]absfs.File),
		nextHandle:  1,
		freeHandles: NewUint64MinHeap(),
	}

	mfs, _ := memfs.NewFS()
	f, _ := mfs.Create("/test.txt")

	handle := fm.Allocate(f)

	t.Run("get existing handle", func(t *testing.T) {
		file, err := fm.GetOrError(handle)
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if file == nil {
			t.Errorf("Expected file, got nil")
		}
	})

	t.Run("get non-existent handle", func(t *testing.T) {
		_, err := fm.GetOrError(999999)
		if err == nil {
			t.Errorf("Expected error for non-existent handle")
		}
		if _, ok := err.(*InvalidFileHandleError); !ok {
			t.Errorf("Expected InvalidFileHandleError, got %T", err)
		}
	})
}
