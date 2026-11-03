// filehandle.go: NFS file handle allocation and lifecycle management.
//
// Contains FileHandleMap methods for allocating, looking up, releasing,
// and evicting file handles. Handle numbers are never reused, so a handle
// a client still holds after it was released or evicted reports
// NFS3ERR_STALE instead of reaching another object. The oldest handles are
// evicted when the handle limit is reached.
package absnfs

import (
	"slices"
	"time"

	"github.com/absfs/absfs"
)

// DefaultMaxHandles is the maximum number of file handles before eviction occurs.
const DefaultMaxHandles = 100000

// newFileHandleMap returns an empty FileHandleMap whose first handle is
// the current time in nanoseconds. Handle numbers only go up, by one per
// allocation, and an allocation takes longer than a nanosecond, so every
// handle a previous run of the server gave out is below the first handle
// of a later run (unless the clock is set back between them): a client
// holding a handle from before a restart gets NFS3ERR_STALE, not whatever
// object a restarted server gave that number to. maxHandles 0 means
// DefaultMaxHandles.
func newFileHandleMap(maxHandles int) *FileHandleMap {
	return &FileHandleMap{
		handles:     make(map[uint64]absfs.File),
		pathHandles: make(map[string]uint64),
		nextHandle:  uint64(time.Now().UnixNano()),
		maxHandles:  maxHandles,
	}
}

// Allocate creates a new file handle for the given absfs.File.
// For NFSNode files, deduplicates by path: if a handle already exists for
// the same path, updates the file reference and returns the existing handle.
// This prevents unbounded handle growth from repeated LOOKUP/READDIRPLUS calls.
// Otherwise the handle is a number this map has never given out before:
// numbers are not reused after Release or eviction, so a stale handle can
// never name a different object.
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

	handle := fm.nextHandle
	fm.nextHandle++
	fm.handles[handle] = f

	// Record path mapping for NFSNode files
	if node, ok := f.(*NFSNode); ok && node.path != "" {
		fm.pathHandles[node.path] = handle
	}

	fm.evictOldest()
	return handle
}

// evictOldest drops the oldest tenth of the handles (at least one) when the
// map holds more than its limit. Handle numbers grow with each allocation,
// so the oldest are the lowest. They are not contiguous (released and
// retired numbers leave gaps), so the live numbers are sorted rather than
// counted up from the lowest. The caller holds fm's lock.
func (fm *FileHandleMap) evictOldest() {
	maxH := fm.maxHandles
	if maxH <= 0 {
		maxH = DefaultMaxHandles
	}
	if len(fm.handles) <= maxH {
		return
	}
	evictCount := max(maxH/10, 1)
	live := make([]uint64, 0, len(fm.handles))
	for h := range fm.handles {
		live = append(live, h)
	}
	slices.Sort(live)
	for _, h := range live[:evictCount] {
		file := fm.handles[h]
		if node, ok := file.(*NFSNode); ok {
			delete(fm.pathHandles, node.path)
		}
		file.Close()
		delete(fm.handles, h)
	}
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

// Release removes the file handle mapping and closes the associated file.
// The number is not given out again: requests with it get NFS3ERR_STALE.
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
	}
}

// renamed moves the handles of oldPath and of everything below it to the
// same names below newPath, after a RENAME, so a client's handles keep
// reaching the objects they named and a name made again under oldPath gets
// a new handle. The handles of what newPath replaced are retired.
func (fm *FileHandleMap) renamed(oldPath, newPath string) {
	if oldPath == newPath {
		return
	}
	fm.Lock()
	defer fm.Unlock()

	oldRoots, newRoots := []string{oldPath}, []string{newPath}
	oldPrefixes, newPrefixes := treePrefixes(oldRoots), treePrefixes(newRoots)
	moved := make(map[uint64]string)
	for p, h := range fm.pathHandles {
		switch {
		case inAnyTree(p, oldRoots, oldPrefixes):
			moved[h] = newPath + p[len(oldPath):]
		case inAnyTree(p, newRoots, newPrefixes):
			fm.retire(p, h)
		}
	}
	for h, p := range moved {
		node := fm.handles[h].(*NFSNode) // pathHandles only records nodes
		delete(fm.pathHandles, node.path)
		fm.handles[h] = node.movedTo(p)
		fm.pathHandles[p] = h
	}
}

// removed retires the handle of the object at p, after REMOVE or RMDIR.
func (fm *FileHandleMap) removed(p string) {
	fm.Lock()
	defer fm.Unlock()
	if h, ok := fm.pathHandles[p]; ok {
		fm.retire(p, h)
	}
}

// retire drops handle h of the object at p for good: requests with it get
// NFS3ERR_STALE, and, like every handle number, it is never handed out
// again, so it cannot come to name another object. The caller holds fm's
// lock.
func (fm *FileHandleMap) retire(p string, h uint64) {
	if f, ok := fm.handles[h]; ok {
		f.Close()
		delete(fm.handles, h)
	}
	delete(fm.pathHandles, p)
}

// ReleaseAll closes and removes all file handles
func (fm *FileHandleMap) ReleaseAll() {
	fm.Lock()
	defer fm.Unlock()

	for handle, f := range fm.handles {
		f.Close()
		delete(fm.handles, handle)
	}

	// Clear the path mapping since all handles are now released. The
	// counter is kept, so released numbers are not given out again.
	fm.pathHandles = make(map[string]uint64)
}

// Count returns the number of active file handles
func (fm *FileHandleMap) Count() int {
	fm.RLock()
	defer fm.RUnlock()
	return len(fm.handles)
}
