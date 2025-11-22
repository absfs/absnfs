package absnfs

import "container/heap"

// uint64MinHeap implements a min-heap for uint64 values
type uint64MinHeap []uint64

func (h uint64MinHeap) Len() int           { return len(h) }
func (h uint64MinHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h uint64MinHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *uint64MinHeap) Push(x interface{}) {
	*h = append(*h, x.(uint64))
}

func (h *uint64MinHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// NewUint64MinHeap creates and initializes a new min-heap
func NewUint64MinHeap() *uint64MinHeap {
	h := &uint64MinHeap{}
	heap.Init(h)
	return h
}

// PopMin removes and returns the minimum element from the heap
func (h *uint64MinHeap) PopMin() uint64 {
	return heap.Pop(h).(uint64)
}

// PushValue adds a value to the heap
func (h *uint64MinHeap) PushValue(val uint64) {
	heap.Push(h, val)
}

// IsEmpty returns true if the heap is empty
func (h *uint64MinHeap) IsEmpty() bool {
	return h.Len() == 0
}
