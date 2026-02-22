// filehandle.go: NFS file handle allocation and lifecycle management.
//
// Contains FileHandleMap methods for allocating, looking up, releasing,
// and evicting file handles. Uses a min-heap for O(log n) handle ID
// reuse and supports LRU eviction when the handle limit is reached.
package absnfs

import (
	"math"

	"github.com/absfs/absfs"
)

// DefaultMaxHandles is the maximum number of file handles before eviction occurs.
const DefaultMaxHandles = 100000

// Allocate creates a new file handle for the given absfs.File
// Optimized to O(log n) or O(1) using a free list instead of O(n) linear search.
// For NFSNode files, deduplicates by path: if a handle already exists for
// the same path, updates the file reference and returns the existing handle.
// This prevents unbounded handle growth from repeated LOOKUP/READDIRPLUS calls.
func (fm *FileHandleMap) Allocate(f absfs.File) uint64 {
	fm.Lock()
	defer fm.Unlock()

	// Deduplicate by path for NFSNode files
	if node, ok := f.(*NFSNode); ok && node.path != "" {
		if existing, found := fm.pathHandles[node.path]; found {
			// Update the file reference (may have newer attrs) and return existing handle
			fm.handles[existing] = f
			return existing
		}
	}

	var handle uint64

	// First, try to reuse a freed handle (prefer smallest available)
	if val, ok := fm.freeHandles.PopMin(); ok {
		handle = val
	} else {
		// No freed handles available, use the next sequential handle
		handle = fm.nextHandle
		fm.nextHandle++
	}

	fm.handles[handle] = f

	// Record path mapping for NFSNode files
	if node, ok := f.(*NFSNode); ok && node.path != "" {
		fm.pathHandles[node.path] = handle
	}

	// Evict oldest handles if map exceeds maxHandles
	maxH := fm.maxHandles
	if maxH <= 0 {
		maxH = DefaultMaxHandles
	}
	if len(fm.handles) > maxH {
		evictCount := maxH / 10
		if evictCount < 1 {
			evictCount = 1
		}
		// Find the lowest handle numbers (oldest) to evict
		minHandle := uint64(math.MaxUint64)
		for h := range fm.handles {
			if h < minHandle {
				minHandle = h
			}
		}
		// Evict starting from the lowest handles
		for h := minHandle; evictCount > 0; h++ {
			if file, exists := fm.handles[h]; exists {
				// Clean up path mapping for evicted entries
				if node, ok := file.(*NFSNode); ok {
					delete(fm.pathHandles, node.path)
				}
				file.Close()
				delete(fm.handles, h)
				fm.freeHandles.PushValue(h)
				evictCount--
			}
		}
	}

	return handle
}

// Get retrieves the absfs.File associated with the given handle
func (fm *FileHandleMap) Get(handle uint64) (absfs.File, bool) {
	fm.RLock()
	defer fm.RUnlock()

	f, exists := fm.handles[handle]
	return f, exists
}

// GetOrError retrieves the absfs.File associated with the given handle
// Returns an InvalidFileHandleError if the handle is not found
func (fm *FileHandleMap) GetOrError(handle uint64) (absfs.File, error) {
	fm.RLock()
	defer fm.RUnlock()

	f, exists := fm.handles[handle]
	if !exists {
		return nil, &InvalidFileHandleError{
			Handle: handle,
			Reason: "handle not found in file handle map",
		}
	}
	return f, nil
}

// Release removes the file handle mapping and closes the associated file
func (fm *FileHandleMap) Release(handle uint64) {
	fm.Lock()
	defer fm.Unlock()

	if f, exists := fm.handles[handle]; exists {
		// Clean up path mapping
		if node, ok := f.(*NFSNode); ok {
			delete(fm.pathHandles, node.path)
		}
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

	// Clear the path mapping and free list since all handles are now released
	fm.pathHandles = make(map[string]uint64)
	fm.freeHandles = NewUint64MinHeap()
}

// Count returns the number of active file handles
func (fm *FileHandleMap) Count() int {
	fm.RLock()
	defer fm.RUnlock()
	return len(fm.handles)
}
