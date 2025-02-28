package absnfs

import (
	"github.com/absfs/absfs"
)

// Allocate creates a new file handle for the given absfs.File
func (fm *FileHandleMap) Allocate(f absfs.File) uint64 {
	fm.Lock()
	defer fm.Unlock()

	// Try to find the smallest available handle
	var handle uint64 = 1
	for {
		if _, exists := fm.handles[handle]; !exists {
			break
		}
		handle++
	}

	fm.handles[handle] = f
	if handle > fm.lastHandle {
		fm.lastHandle = handle
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

// Release removes the file handle mapping and closes the associated file
func (fm *FileHandleMap) Release(handle uint64) {
	fm.Lock()
	defer fm.Unlock()

	if f, exists := fm.handles[handle]; exists {
		f.Close()
		delete(fm.handles, handle)
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
}

// Count returns the number of active file handles
func (fm *FileHandleMap) Count() int {
	fm.RLock()
	defer fm.RUnlock()
	return len(fm.handles)
}
