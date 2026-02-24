package absnfs

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/absfs/absfs"
	"github.com/absfs/memfs"
)

// sink prevents dead-code elimination in hot benchmark loops.
var sinkFHFile absfs.File
var sinkFHBool bool
var sinkFHUint64 uint64

// BenchmarkFileHandlePathDedup measures the cost of allocating a handle for a
// path that already has a handle (hits the pathHandles dedup fast path).
func BenchmarkFileHandlePathDedup(b *testing.B) {
	b.ReportAllocs()

	fm := benchFileHandleMap(b)
	node := &NFSNode{path: "/dedup/target"}
	fm.Allocate(node)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkFHUint64 = fm.Allocate(&NFSNode{path: "/dedup/target"})
	}
}

// BenchmarkFileHandleGet measures Get latency at various map sizes.
func BenchmarkFileHandleGet(b *testing.B) {
	for _, size := range []int{100, 1000, 10000, 50000} {
		b.Run(fmt.Sprintf("handles/%d", size), func(b *testing.B) {
			b.ReportAllocs()

			fm := &FileHandleMap{
				handles:     make(map[uint64]absfs.File, size),
				pathHandles: make(map[string]uint64, size),
				nextHandle:  1,
				freeHandles: NewUint64MinHeap(),
				maxHandles:  size + 1,
			}

			for i := 0; i < size; i++ {
				fm.handles[uint64(i+1)] = &NFSNode{path: fmt.Sprintf("/f%d", i)}
			}
			fm.nextHandle = uint64(size + 1)

			target := uint64(size / 2) // look up a handle in the middle
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sinkFHFile, sinkFHBool = fm.Get(target)
			}
		})
	}
}

// BenchmarkFileHandleGetParallel measures concurrent Get throughput.
func BenchmarkFileHandleGetParallel(b *testing.B) {
	b.ReportAllocs()

	const size = 1000
	fm := &FileHandleMap{
		handles:     make(map[uint64]absfs.File, size),
		pathHandles: make(map[string]uint64, size),
		nextHandle:  uint64(size + 1),
		freeHandles: NewUint64MinHeap(),
		maxHandles:  size + 1,
	}
	for i := 0; i < size; i++ {
		fm.handles[uint64(i+1)] = &NFSNode{path: fmt.Sprintf("/f%d", i)}
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var f absfs.File
		var ok bool
		h := uint64(1)
		for pb.Next() {
			f, ok = fm.Get(h)
			h++
			if h > size {
				h = 1
			}
		}
		_, _ = f, ok
	})
}

// BenchmarkFileHandleAllocateRelease measures the allocate-then-release cycle,
// exercising the free-list reuse path on subsequent iterations.
func BenchmarkFileHandleAllocateRelease(b *testing.B) {
	b.ReportAllocs()

	fm := benchFileHandleMap(b)
	node := &NFSNode{path: ""}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h := fm.Allocate(node)
		fm.Release(h)
	}
}

// BenchmarkFileHandleMixedOps simulates a realistic workload:
// 70% Get, 20% Allocate, 10% Release.
func BenchmarkFileHandleMixedOps(b *testing.B) {
	b.ReportAllocs()

	fm := benchFileHandleMap(b)
	// Pre-populate with some handles
	handles := make([]uint64, 100)
	for i := range handles {
		handles[i] = fm.Allocate(&NFSNode{path: fmt.Sprintf("/mix%d", i)})
	}
	idx := 0

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		switch i % 10 {
		case 0: // 10% Release
			if idx > 0 {
				idx--
				fm.Release(handles[idx])
			}
		case 1, 2: // 20% Allocate
			if idx < len(handles) {
				handles[idx] = fm.Allocate(&NFSNode{path: fmt.Sprintf("/mix%d", idx)})
				idx++
			}
		default: // 70% Get
			if idx > 0 {
				sinkFHFile, sinkFHBool = fm.Get(handles[idx-1])
			}
		}
	}
}

// BenchmarkFileHandleAllocateParallel measures concurrent Allocate throughput
// where each goroutine allocates handles for unique paths.
func BenchmarkFileHandleAllocateParallel(b *testing.B) {
	b.ReportAllocs()

	fs, err := memfs.NewFS()
	if err != nil {
		b.Fatal(err)
	}

	fm := &FileHandleMap{
		handles:     make(map[uint64]absfs.File),
		pathHandles: make(map[string]uint64),
		nextHandle:  1,
		freeHandles: NewUint64MinHeap(),
		maxHandles:  b.N + 10000,
	}

	var counter atomic.Uint64
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var h uint64
		for pb.Next() {
			n := counter.Add(1)
			name := fmt.Sprintf("/par_%d", n)
			f, err := fs.Create(name)
			if err != nil {
				b.Fatal(err)
			}
			f.Close()
			f2, _ := fs.OpenFile(name, 0, 0)
			h = fm.Allocate(f2)
		}
		_ = h
	})
}
