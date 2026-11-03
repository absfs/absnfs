package absnfs

import (
	"fmt"
	"slices"
	"testing"

	"github.com/absfs/memfs"
)

// TestFileHandleNumbersNotReused verifies that released handle numbers are
// not given out again: allocations after a release continue the sequence.
func TestFileHandleNumbersNotReused(t *testing.T) {
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
	fm := newFileHandleMap(0)

	// Allocate ten handles; each number is above the one before
	handles := make([]uint64, 10)
	for i := 0; i < 10; i++ {
		f, err := fs.OpenFile("/test.txt", 0, 0)
		if err != nil {
			t.Fatalf("Failed to open file: %v", err)
		}
		handles[i] = fm.Allocate(f)
		if i > 0 && handles[i] <= handles[i-1] {
			t.Errorf("handle %d after %d, want increasing numbers", handles[i], handles[i-1])
		}
	}

	// Release the third, fifth and seventh
	released := []uint64{handles[2], handles[4], handles[6]}
	for _, h := range released {
		fm.Release(h)
	}

	// Allocate 3 new handles: they continue above the last, not reusing
	// the released numbers
	last := handles[9]
	for i := 0; i < 3; i++ {
		f, err := fs.OpenFile("/test.txt", 0, 0)
		if err != nil {
			t.Fatalf("Failed to open file: %v", err)
		}
		h := fm.Allocate(f)
		if h <= last {
			t.Errorf("allocation %d after releases: got handle %d, want one above %d", i, h, last)
		}
		last = h
	}
	for _, h := range released {
		if _, ok := fm.Get(h); ok {
			t.Errorf("released handle %d resolves again", h)
		}
	}
}

// BenchmarkFileHandleAllocation benchmarks allocation on a map with many live handles
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

	fm := newFileHandleMap(0)

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

	fm := newFileHandleMap(0)

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

	fm := newFileHandleMap(0)

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

	// Allocate more handles - new numbers, never the released ones
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

// ExampleFileHandleMap_Allocate_performance shows that handle numbers
// are never reused: after a release, allocation continues the sequence.
func ExampleFileHandleMap_Allocate_performance() {
	fs, _ := memfs.NewFS()
	f, _ := fs.Create("/test.txt")
	f.Close()

	fm := newFileHandleMap(0)

	// Allocate handles - O(1) time. Numbers start from the clock, so they
	// are large; each is above the one before.
	handles := make([]uint64, 5)
	for i := range handles {
		f, _ := fs.OpenFile("/test.txt", 0, 0)
		handles[i] = fm.Allocate(f)
	}
	fmt.Println("increasing:", slices.IsSorted(handles))

	// Release a handle; a client still holding it gets NFS3ERR_STALE
	fm.Release(handles[2])
	_, ok := fm.Get(handles[2])
	fmt.Println("released handle resolves:", ok)

	// Allocate again - a new number, above every earlier one
	f, _ = fs.OpenFile("/test.txt", 0, 0)
	next := fm.Allocate(f)
	fmt.Println("new number:", next > handles[4])

	// Output:
	// increasing: true
	// released handle resolves: false
	// new number: true
}

// BenchmarkAllocatePastLimit allocates new paths in a map held at
// DefaultMaxHandles, so allocations keep triggering eviction.
func BenchmarkAllocatePastLimit(b *testing.B) {
	fm := newFileHandleMap(0)
	for i := 0; i < DefaultMaxHandles; i++ {
		fm.Allocate(&NFSNode{path: fmt.Sprintf("/pre%d", i)})
	}
	nodes := make([]*NFSNode, b.N)
	for i := range nodes {
		nodes[i] = &NFSNode{path: fmt.Sprintf("/n%d", i)}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fm.Allocate(nodes[i])
	}
}
