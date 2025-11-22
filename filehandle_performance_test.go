package absnfs

import (
	"fmt"
	"testing"

	"github.com/absfs/absfs"
	"github.com/absfs/memfs"
)

// TestFileHandleReuse verifies that released handles are reused efficiently
func TestFileHandleReuse(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	// Create a test file
	f, err := fs.Create("/test.txt")
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	f.Close()

	// Create file handle map
	fm := &FileHandleMap{
		handles:     make(map[uint64]absfs.File),
		nextHandle:  1,
		freeHandles: NewUint64MinHeap(),
	}

	// Allocate handles 1-10
	handles := make([]uint64, 10)
	for i := 0; i < 10; i++ {
		f, err := fs.OpenFile("/test.txt", 0, 0)
		if err != nil {
			t.Fatalf("Failed to open file: %v", err)
		}
		handles[i] = fm.Allocate(f)
	}

	// Verify handles are 1-10
	for i, h := range handles {
		if h != uint64(i+1) {
			t.Errorf("Expected handle %d, got %d", i+1, h)
		}
	}

	// Release handles 3, 5, and 7
	fm.Release(3)
	fm.Release(5)
	fm.Release(7)

	// Allocate 3 new handles - should reuse 3, 5, 7 (in ascending order)
	newHandles := make([]uint64, 3)
	for i := 0; i < 3; i++ {
		f, err := fs.OpenFile("/test.txt", 0, 0)
		if err != nil {
			t.Fatalf("Failed to open file: %v", err)
		}
		newHandles[i] = fm.Allocate(f)
	}

	// Verify handles are reused in order: 3, 5, 7
	expected := []uint64{3, 5, 7}
	for i, h := range newHandles {
		if h != expected[i] {
			t.Errorf("Expected reused handle %d, got %d", expected[i], h)
		}
	}

	// Next allocation should get a new handle (11)
	f, err = fs.OpenFile("/test.txt", 0, 0)
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	nextHandle := fm.Allocate(f)
	if nextHandle != 11 {
		t.Errorf("Expected next handle to be 11, got %d", nextHandle)
	}
}

// BenchmarkFileHandleAllocation benchmarks the new O(log n) allocation
func BenchmarkFileHandleAllocation(b *testing.B) {
	fs, err := memfs.NewFS()
	if err != nil {
		b.Fatalf("Failed to create memfs: %v", err)
	}

	// Create test file
	f, err := fs.Create("/test.txt")
	if err != nil {
		b.Fatalf("Failed to create test file: %v", err)
	}
	f.Close()

	fm := &FileHandleMap{
		handles:     make(map[uint64]absfs.File),
		nextHandle:  1,
		freeHandles: NewUint64MinHeap(),
	}

	// Pre-allocate 1000 handles to simulate a busy server
	preAllocated := make([]uint64, 1000)
	for i := 0; i < 1000; i++ {
		f, err := fs.OpenFile("/test.txt", 0, 0)
		if err != nil {
			b.Fatalf("Failed to open file: %v", err)
		}
		preAllocated[i] = fm.Allocate(f)
	}

	// Release every other handle to create a realistic scenario
	for i := 0; i < 1000; i += 2 {
		fm.Release(preAllocated[i])
	}

	b.ResetTimer()

	// Benchmark allocation with many existing handles
	for i := 0; i < b.N; i++ {
		f, err := fs.OpenFile("/test.txt", 0, 0)
		if err != nil {
			b.Fatalf("Failed to open file: %v", err)
		}
		handle := fm.Allocate(f)
		fm.Release(handle)
	}
}

// BenchmarkFileHandleAllocationSequential benchmarks best-case sequential allocation
func BenchmarkFileHandleAllocationSequential(b *testing.B) {
	fs, err := memfs.NewFS()
	if err != nil {
		b.Fatalf("Failed to create memfs: %v", err)
	}

	f, err := fs.Create("/test.txt")
	if err != nil {
		b.Fatalf("Failed to create test file: %v", err)
	}
	f.Close()

	fm := &FileHandleMap{
		handles:     make(map[uint64]absfs.File),
		nextHandle:  1,
		freeHandles: NewUint64MinHeap(),
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		f, err := fs.OpenFile("/test.txt", 0, 0)
		if err != nil {
			b.Fatalf("Failed to open file: %v", err)
		}
		handle := fm.Allocate(f)
		// Don't release - measure pure allocation performance
		_ = handle
	}
}

// TestFileHandleAllocationStress is a stress test to verify correctness under load
func TestFileHandleAllocationStress(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	f, err := fs.Create("/test.txt")
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	f.Close()

	fm := &FileHandleMap{
		handles:     make(map[uint64]absfs.File),
		nextHandle:  1,
		freeHandles: NewUint64MinHeap(),
	}

	// Allocate and release handles in a pattern to test free list
	allocated := make(map[uint64]bool)

	// Allocate 100 handles
	for i := 0; i < 100; i++ {
		f, err := fs.OpenFile("/test.txt", 0, 0)
		if err != nil {
			t.Fatalf("Failed to open file: %v", err)
		}
		handle := fm.Allocate(f)

		if allocated[handle] {
			t.Fatalf("Handle %d was allocated twice!", handle)
		}
		allocated[handle] = true
	}

	// Release odd handles
	for h := range allocated {
		if h%2 == 1 {
			fm.Release(h)
			delete(allocated, h)
		}
	}

	// Allocate more handles - should reuse the freed ones
	for i := 0; i < 50; i++ {
		f, err := fs.OpenFile("/test.txt", 0, 0)
		if err != nil {
			t.Fatalf("Failed to open file: %v", err)
		}
		handle := fm.Allocate(f)

		if allocated[handle] {
			t.Fatalf("Handle %d was allocated twice!", handle)
		}
		allocated[handle] = true
	}

	// Verify all allocated handles are unique
	if len(allocated) != 100 {
		t.Errorf("Expected 100 unique handles, got %d", len(allocated))
	}

	// Verify count matches
	if fm.Count() != 100 {
		t.Errorf("Expected count of 100, got %d", fm.Count())
	}
}

// Example demonstrates the performance characteristics of the optimized allocation
func ExampleFileHandleMap_Allocate_performance() {
	fs, _ := memfs.NewFS()
	f, _ := fs.Create("/test.txt")
	f.Close()

	fm := &FileHandleMap{
		handles:     make(map[uint64]absfs.File),
		nextHandle:  1,
		freeHandles: NewUint64MinHeap(),
	}

	// Allocate handles - O(1) time
	handles := make([]uint64, 5)
	for i := 0; i < 5; i++ {
		f, _ := fs.OpenFile("/test.txt", 0, 0)
		handles[i] = fm.Allocate(f)
		fmt.Printf("Allocated handle: %d\n", handles[i])
	}

	// Release some handles - O(log n) time
	fm.Release(handles[2]) // Release handle 3
	fmt.Println("Released handle 3")

	// Allocate again - reuses handle 3 in O(log n) time
	f, _ = fs.OpenFile("/test.txt", 0, 0)
	reused := fm.Allocate(f)
	fmt.Printf("Reused handle: %d\n", reused)

	// Output:
	// Allocated handle: 1
	// Allocated handle: 2
	// Allocated handle: 3
	// Allocated handle: 4
	// Allocated handle: 5
	// Released handle 3
	// Reused handle: 3
}
