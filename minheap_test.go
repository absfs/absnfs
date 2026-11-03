package absnfs

import "testing"

// TestUint64MinHeapPopsInOrder covers the deprecated heap kept for API
// compatibility: values come out smallest first, then the heap is empty.
func TestUint64MinHeapPopsInOrder(t *testing.T) {
	h := NewUint64MinHeap()
	if !h.IsEmpty() {
		t.Fatal("new heap is not empty")
	}
	for _, v := range []uint64{5, 1, 4, 2, 3} {
		h.PushValue(v)
	}
	for want := uint64(1); want <= 5; want++ {
		got, ok := h.PopMin()
		if !ok || got != want {
			t.Fatalf("PopMin = %d, %v; want %d, true", got, ok, want)
		}
	}
	if v, ok := h.PopMin(); ok {
		t.Errorf("PopMin on an empty heap = %d, true; want false", v)
	}
}
