package absnfs

import (
	"github.com/absfs/absfs"
)

// Allocate creates a new file handle for the given absfs.File
// Optimized to O(log n) or O(1) using a free list instead of O(n) linear search
func (fm *FileHandleMap) Allocate(f absfs.File) uint64 {
	fm.Lock()
	defer fm.Unlock()

	var handle uint64

	// First, try to reuse a freed handle (prefer smallest available)
	if !fm.freeHandles.IsEmpty() {
		handle = fm.freeHandles.PopMin()
	} else {
		// No freed handles available, use the next sequential handle
		handle = fm.nextHandle
		fm.nextHandle++
	}

	fm.handles[handle] = f
	return handle
}

// Get retrieves the absfs.File associated with the given handle
func (fm *FileHandleMap) Get(handle uint64) (absfs.File, bool) {
	fm.RLock()
	defer fm.RUnlock()

	f, exists := fm.handles[handle]
	return f, exists
}

// Release removes the file handle mapping and closes the associated file
func (fm *FileHandleMap) Release(handle uint64) {
	fm.Lock()
	defer fm.Unlock()

	if f, exists := fm.handles[handle]; exists {
		f.Close()
		delete(fm.handles, handle)
		// Add the freed handle to the free list for reuse
		fm.freeHandles.PushValue(handle)
	}
}

// ReleaseAll closes and removes all file handles
func (fm *FileHandleMap) ReleaseAll() {
	fm.Lock()
	defer fm.Unlock()

	for handle, f := range fm.handles {
		f.Close()
		delete(fm.handles, handle)
	}

	// Clear the free list since all handles are now released
	fm.freeHandles = NewUint64MinHeap()
}

// Count returns the number of active file handles
func (fm *FileHandleMap) Count() int {
	fm.RLock()
	defer fm.RUnlock()
	return len(fm.handles)
}
