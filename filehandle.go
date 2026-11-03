// filehandle.go: NFS file handle allocation and lifecycle management.
//
// Contains FileHandleMap methods for allocating, looking up, releasing,
// and evicting file handles. Handle numbers are never reused, so a handle
// a client still holds after it was released or evicted reports
// NFS3ERR_STALE instead of reaching another object. When the handle limit
// is reached, the handles least recently used are evicted; the export
// root's handle and the handles MNT gave out are pinned and never evicted.
package absnfs

import (
	"sync/atomic"
	"time"

	"github.com/absfs/absfs"
)

// DefaultMaxHandles is the maximum number of file handles before eviction occurs.
const DefaultMaxHandles = 100000

// lastHandle is the last handle number given out by any FileHandleMap in
// this process. Every map draws its numbers from it, so a server that is
// closed and created again in the same process, however quickly, never
// gives out a number an earlier one did.
//
// It starts at the time the process started, in nanoseconds. A later
// process therefore starts above every number an earlier one gave out,
// unless the clock was set back between them (a machine without a
// real-time clock that boots at 1970 until NTP syncs counts as set back),
// or the earlier process gave out more handles than nanoseconds passed
// between the two starts, which a real restart cannot do. Wrapping 2^64
// would take more allocations than there are nanoseconds until the year
// 2554.
var lastHandle = startHandleCounter()

func startHandleCounter() *atomic.Uint64 {
	var c atomic.Uint64
	c.Store(uint64(time.Now().UnixNano()))
	return &c
}

// handleEntry is what a handle names.
type handleEntry struct {
	file   absfs.File
	used   atomic.Bool // set by lookups, cleared as the eviction clock passes
	pinned bool        // never evicted (the export root, handles given out by MNT)
}

// newFileHandleMap returns an empty FileHandleMap that holds at most
// maxHandles handles (0 means DefaultMaxHandles).
func newFileHandleMap(maxHandles int) *FileHandleMap {
	return &FileHandleMap{
		handles:     make(map[uint64]*handleEntry),
		pathHandles: make(map[string]uint64),
		maxHandles:  maxHandles,
	}
}

// limit is the number of handles the map holds before it evicts.
func (fm *FileHandleMap) limit() int {
	if fm.maxHandles <= 0 {
		return DefaultMaxHandles
	}
	return fm.maxHandles
}

// Allocate creates a new file handle for the given absfs.File.
// For NFSNode files, deduplicates by path: if a handle already exists for
// the same path, updates the file reference and returns the existing handle.
// This prevents unbounded handle growth from repeated LOOKUP/READDIRPLUS calls.
// Otherwise the handle is a number never given out before in this process:
// numbers are not reused after Release or eviction, so a stale handle can
// never name a different object.
func (fm *FileHandleMap) Allocate(f absfs.File) uint64 {
	return fm.allocate(f, false)
}

// allocateMount is Allocate for the handle MNT returns, which is pinned:
// eviction never drops it, since a client that loses its mount's root
// handle cannot look anything up again without remounting. At most a tenth
// of the limit (at least one) is pinned; past that, MNT handles are
// ordinary ones. The export root's handle is the exception: it is always
// pinned, however it was allocated, even past the cap, so a client that
// mounted the export can always reach it. That adds at most one pin.
func (fm *FileHandleMap) allocateMount(f absfs.File) uint64 {
	return fm.allocate(f, true)
}

func (fm *FileHandleMap) allocate(f absfs.File, pin bool) uint64 {
	fm.Lock()
	defer fm.Unlock()

	node, isNode := f.(*NFSNode)
	isNode = isNode && node.path != ""
	root := isNode && node.path == "/"
	if isNode {
		if h, found := fm.pathHandles[node.path]; found {
			// Update the file reference (may have newer attrs) and return
			// the existing handle.
			e := fm.handles[h]
			e.file = f
			e.used.Store(true)
			if pin || root {
				fm.pin(e, root)
			}
			return h
		}
	}

	h := lastHandle.Add(1)
	e := &handleEntry{file: f}
	fm.handles[h] = e
	if isNode {
		fm.pathHandles[node.path] = h
	}
	if pin || root {
		fm.pin(e, root)
	}
	if !e.pinned {
		fm.clock = append(fm.clock, h)
	}
	fm.evict()
	return h
}

// pin marks e never to be evicted, if fewer than a tenth of the limit (at
// least one) is pinned or e is the export root's entry, which is pinned
// regardless. Only the root passes the cap, so at most one more than the
// cap is pinned. A pinned entry still in the clock is skipped and dropped
// from it when the hand reaches it. The caller holds fm's lock.
func (fm *FileHandleMap) pin(e *handleEntry, root bool) {
	if e.pinned || (!root && fm.pinned >= max(fm.limit()/10, 1)) {
		return
	}
	e.pinned = true
	fm.pinned++
}

// evict drops a tenth of the limit (at least one) of handles when the map
// holds more than its limit, choosing them with the clock algorithm: the
// hand walks the handles from the oldest; one used since the hand last
// passed it gets a second chance (its mark is cleared and it goes to the
// back), one not used is evicted. So the handles evicted are ones not used
// for a while, never ones a client keeps using, and never pinned ones.
// Each step is O(1): numbers released or retired since they were queued
// are skipped, and the queue is compacted once it is mostly such dead
// entries. The caller holds fm's lock.
func (fm *FileHandleMap) evict() {
	maxH := fm.limit()
	if len(fm.handles) > maxH {
		// Every live entry is passed over at most once, as its mark is
		// cleared, so this ends once the queue is empty at the latest.
		for want := max(maxH/10, 1); want > 0 && fm.hand < len(fm.clock); {
			h := fm.clock[fm.hand]
			fm.hand++
			e, ok := fm.handles[h]
			switch {
			case !ok || e.pinned:
				// Released, retired or pinned since it was queued.
			case e.used.Swap(false):
				fm.clock = append(fm.clock, h)
			default:
				fm.drop(h, e)
				want--
			}
		}
	}
	fm.compactClock()
}

// compactClock drops the part of the clock queue the hand has passed, and
// the numbers in it that no longer need a place, once either outgrows the
// live part. That bounds the queue by a small multiple of the live handles
// and keeps the cost amortized O(1) per allocation. The caller holds fm's
// lock.
func (fm *FileHandleMap) compactClock() {
	rest := fm.clock[fm.hand:]
	if fm.hand <= len(rest) && len(rest) <= 2*len(fm.handles)+64 {
		return
	}
	clock := make([]uint64, 0, len(fm.handles)+64)
	for _, h := range rest {
		if e, ok := fm.handles[h]; ok && !e.pinned {
			clock = append(clock, h)
		}
	}
	fm.clock, fm.hand = clock, 0
}

// drop removes handle h, which names e, and closes its file. The caller
// holds fm's lock.
func (fm *FileHandleMap) drop(h uint64, e *handleEntry) {
	if node, ok := e.file.(*NFSNode); ok && fm.pathHandles[node.path] == h {
		delete(fm.pathHandles, node.path)
	}
	if e.pinned {
		fm.pinned--
	}
	e.file.Close()
	delete(fm.handles, h)
}

// lookup returns the entry of handle h and marks it used, so eviction
// passes it over. The caller holds fm's lock, read or write.
func (fm *FileHandleMap) lookup(h uint64) (*handleEntry, bool) {
	e, ok := fm.handles[h]
	if ok && !e.used.Load() {
		e.used.Store(true)
	}
	return e, ok
}

// Get retrieves the absfs.File associated with the given handle
func (fm *FileHandleMap) Get(handle uint64) (absfs.File, bool) {
	fm.RLock()
	defer fm.RUnlock()

	if e, ok := fm.lookup(handle); ok {
		return e.file, true
	}
	return nil, false
}

// GetOrError retrieves the absfs.File associated with the given handle
// Returns an InvalidFileHandleError if the handle is not found
func (fm *FileHandleMap) GetOrError(handle uint64) (absfs.File, error) {
	fm.RLock()
	defer fm.RUnlock()

	e, ok := fm.lookup(handle)
	if !ok {
		return nil, &InvalidFileHandleError{
			Handle: handle,
			Reason: "handle not found in file handle map",
		}
	}
	return e.file, nil
}

// Release removes the file handle mapping and closes the associated file.
// The number is not given out again: requests with it get NFS3ERR_STALE.
func (fm *FileHandleMap) Release(handle uint64) {
	fm.Lock()
	defer fm.Unlock()

	if e, ok := fm.handles[handle]; ok {
		fm.drop(handle, e)
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
		e := fm.handles[h]
		node := e.file.(*NFSNode) // pathHandles only records nodes
		delete(fm.pathHandles, node.path)
		e.file = node.movedTo(p)
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
	if e, ok := fm.handles[h]; ok {
		fm.drop(h, e)
	}
	delete(fm.pathHandles, p)
}

// ReleaseAll closes and removes all file handles. Their numbers are not
// given out again.
func (fm *FileHandleMap) ReleaseAll() {
	fm.Lock()
	defer fm.Unlock()

	for _, e := range fm.handles {
		e.file.Close()
	}
	fm.handles = make(map[uint64]*handleEntry)
	fm.pathHandles = make(map[string]uint64)
	fm.clock, fm.hand, fm.pinned = nil, 0, 0
}

// Count returns the number of active file handles
func (fm *FileHandleMap) Count() int {
	fm.RLock()
	defer fm.RUnlock()
	return len(fm.handles)
}
