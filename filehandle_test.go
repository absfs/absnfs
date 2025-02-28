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
		handles:    make(map[uint64]absfs.File),
		lastHandle: 0,
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
