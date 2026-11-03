// filehandle.go: the file handle cache.
//
// Handles name their objects by path (fh_codec.go) and are checked against
// the change log (fh_changelog.go), so the server can resolve any handle it
// issued without remembering it. FileHandleMap is only a cache in front of
// that: it maps handle bytes to the NFSNode they resolved to, so a request
// with a handle used recently costs no decoding and no stat, and path to
// the bytes issued for it, so the same object keeps getting the same bytes.
// When the cache is full the entries least recently used are evicted; a
// client that uses an evicted handle gets it resolved again, not
// NFS3ERR_STALE for an object that still exists (but for the residual
// cases in docs/internals/file-handles.md: opaque handles, tail walks past
// their caps).
package absnfs

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"sync/atomic"
	"time"

	"github.com/absfs/absfs"
)

// DefaultMaxHandles is the number of handles the cache holds when
// ExportOptions.MaxHandles is not set.
const DefaultMaxHandles = 100000

// inflightWait bounds how long resolving or issuing a handle waits for a
// REMOVE, RMDIR or RENAME in flight on its path to finish. A resolution
// that waits longer is answered NFS3ERR_JUKEBOX, so the client retries.
// A variable for tests.
var inflightWait = 5 * time.Second

// handleEntry is a cached handle: the node it resolved to, and the path and
// epoch it stands for after any RENAME it followed. node, path and g change
// only under the map's write lock.
type handleEntry struct {
	node *NFSNode
	path string // canonical
	g    uint64
	typ  uint8
	used atomic.Bool // set by lookups, cleared as the eviction clock passes
}

// newFileHandleMap returns an empty cache of at most maxHandles handles
// (<= 0 means DefaultMaxHandles), with a new random key: handles from any
// other map are stale in it.
func newFileHandleMap(maxHandles int) *FileHandleMap {
	key := make([]byte, handleKeySize)
	rand.Read(key)
	return newFileHandleMapWithKey(maxHandles, key)
}

func newFileHandleMapWithKey(maxHandles int, key []byte) *FileHandleMap {
	codec := newHandleCodec(key)
	return &FileHandleMap{
		handles:    make(map[string]*handleEntry),
		paths:      make(map[string]string),
		maxHandles: maxHandles,
		codec:      codec,
		log:        newChangeLog(codec.pathHash),
		dirs:       newDirHashIndex(dirIndexLimit),
	}
}

// limit is the number of handles the cache holds before it evicts.
func (fm *FileHandleMap) limit() int {
	if fm.maxHandles <= 0 {
		return DefaultMaxHandles
	}
	return fm.maxHandles
}

// Resize sets the cache's capacity (<= 0 means DefaultMaxHandles) and
// evicts down to it at once. Only memory and the miss rate depend on it:
// every handle still resolves.
func (fm *FileHandleMap) Resize(maxHandles int) {
	fm.Lock()
	defer fm.Unlock()
	fm.maxHandles = maxHandles
	for len(fm.handles) > fm.limit() {
		before := len(fm.handles)
		fm.evict()
		if len(fm.handles) == before {
			break
		}
	}
}

// issue returns the handle for node, the object at node.path: the bytes
// issued for that path before while they still name it, so a client keeps
// one vnode per object, else new ones.
func (fm *FileHandleMap) issue(node *NFSNode) []byte {
	ctx, cancel := context.WithTimeout(context.Background(), inflightWait)
	defer cancel()

	node.mu.RLock()
	typ := uint8(fhTypeOther)
	if node.attrs != nil {
		typ = fhType(node.attrs.Mode)
	}
	node.mu.RUnlock()
	p, spellable := canonicalPath(node.path)

	fm.RLock()
	key, ok := fm.paths[p]
	e := fm.handles[key]
	var ePath string
	var eG uint64
	if ok && e != nil {
		ePath, eG = e.path, e.g
	}
	fm.RUnlock()
	if ok && e != nil && e.typ == typ && ePath == p {
		if p2, _, valid := fm.log.validate(ctx, ePath, eG); valid && p2 == p {
			fm.Lock()
			if fm.handles[key] == e {
				e.node = node // may have newer attributes
				e.used.Store(true)
				fm.Unlock()
				return []byte(key)
			}
			fm.Unlock()
		}
	}

	// The wait only spares a handle that a commit would stale at once:
	// one issued with an older epoch is stale or follows the change, as
	// the change requires.
	g := fm.log.effective(ctx, p)
	var fh []byte
	if spellable {
		fh = fm.codec.encode(typ, g, p)
	} else {
		fh = fm.codec.encodeOpaque(typ, g, p)
	}
	fm.insert(fh, node, p, g, typ, true)
	return fh
}

// insert caches fh as naming node, at canonical path p with epoch g. With
// reverse set, issue returns fh for p from now on.
func (fm *FileHandleMap) insert(fh []byte, node *NFSNode, p string, g uint64, typ uint8, reverse bool) {
	fm.Lock()
	defer fm.Unlock()
	key := string(fh)
	if e, ok := fm.handles[key]; ok {
		e.node, e.path, e.g = node, p, g
		e.used.Store(true)
	} else {
		e := &handleEntry{node: node, path: p, g: g, typ: typ}
		fm.handles[key] = e
		// The new entry joins the clock only after the eviction it caused,
		// so that eviction cannot pick it.
		fm.evict()
		fm.clock = append(fm.clock, key)
	}
	if _, taken := fm.paths[p]; reverse || !taken {
		fm.paths[p] = key
	}
}

// cached returns the entry of fh, marked used, and its fields.
func (fm *FileHandleMap) cached(fh []byte) (e *handleEntry, node *NFSNode, p string, g uint64) {
	fm.RLock()
	defer fm.RUnlock()
	e = fm.handles[string(fh)]
	if e == nil {
		return nil, nil, "", 0
	}
	if !e.used.Load() {
		e.used.Store(true)
	}
	return e, e.node, e.path, e.g
}

// moved records that the cached handle fh, entry e, now leads to path p at
// epoch g, after RENAMEs, and returns the node to use for it.
func (fm *FileHandleMap) moved(fh []byte, e *handleEntry, p string, g uint64) *NFSNode {
	fm.Lock()
	defer fm.Unlock()
	if fm.handles[string(fh)] != e {
		return e.node.movedTo(nodePath(p))
	}
	if e.path != p {
		if fm.paths[e.path] == string(fh) {
			delete(fm.paths, e.path)
		}
		e.node = e.node.movedTo(nodePath(p))
		e.path = p
	}
	e.g = g
	return e.node
}

// forget drops fh's entry, if it is still e.
func (fm *FileHandleMap) forget(fh []byte, e *handleEntry) {
	fm.Lock()
	defer fm.Unlock()
	if fm.handles[string(fh)] == e {
		fm.drop(string(fh), e)
	}
}

// evict drops a tenth of the limit (at least one) of handles when the cache
// holds more than its limit, choosing them with the clock algorithm: the
// hand walks the entries from the oldest; one used since the hand last
// passed it gets a second chance (its mark is cleared and it goes to the
// back), one not used is evicted. Each step is O(1): keys dropped since
// they were queued are skipped, and the queue is compacted once it is
// mostly such dead keys. The caller holds fm's lock.
func (fm *FileHandleMap) evict() {
	maxH := fm.limit()
	if len(fm.handles) > maxH {
		for want := max(maxH/10, 1); want > 0 && fm.hand < len(fm.clock); {
			key := fm.clock[fm.hand]
			fm.hand++
			e, ok := fm.handles[key]
			switch {
			case !ok:
				// Dropped since it was queued.
			case e.used.Swap(false):
				fm.clock = append(fm.clock, key)
			default:
				fm.drop(key, e)
				want--
			}
		}
	}
	fm.compactClock()
}

// compactClock drops the part of the clock queue the hand has passed, and
// the keys in it that are no longer cached, once either outgrows the live
// part. The caller holds fm's lock.
func (fm *FileHandleMap) compactClock() {
	rest := fm.clock[fm.hand:]
	if fm.hand <= len(rest) && len(rest) <= 2*len(fm.handles)+64 {
		return
	}
	clock := make([]string, 0, len(fm.handles)+64)
	for _, key := range rest {
		if _, ok := fm.handles[key]; ok {
			clock = append(clock, key)
		}
	}
	fm.clock, fm.hand = clock, 0
}

// drop removes the entry of key. The caller holds fm's lock.
func (fm *FileHandleMap) drop(key string, e *handleEntry) {
	if fm.paths[e.path] == key {
		delete(fm.paths, e.path)
	}
	delete(fm.handles, key)
}

// renamed moves the cached handle of src, if any, to dst after a RENAME
// committed at epoch e, so a LOOKUP of the new name returns the bytes the
// client already holds for the object. Handles below a renamed directory
// keep their entries and follow the change log's record when next used.
// The handle cached for what dst replaced is dropped.
func (fm *FileHandleMap) renamed(src, dst string, e uint64) {
	if src == dst {
		return
	}
	fm.Lock()
	defer fm.Unlock()
	if key, ok := fm.paths[dst]; ok {
		if old := fm.handles[key]; old != nil && old.path == dst {
			fm.drop(key, old)
		}
		delete(fm.paths, dst)
	}
	key, ok := fm.paths[src]
	if !ok {
		return
	}
	delete(fm.paths, src)
	ent := fm.handles[key]
	if ent == nil || ent.path != src {
		return
	}
	ent.node = ent.node.movedTo(nodePath(dst))
	ent.path, ent.g = dst, e
	fm.paths[dst] = key
}

// removed drops the cached handle of p after REMOVE or RMDIR.
func (fm *FileHandleMap) removed(p string) {
	fm.Lock()
	defer fm.Unlock()
	if key, ok := fm.paths[p]; ok {
		if e := fm.handles[key]; e != nil {
			fm.drop(key, e)
		}
		delete(fm.paths, p)
	}
}

// ReleaseAll empties the cache. Every handle still resolves.
func (fm *FileHandleMap) ReleaseAll() {
	fm.Lock()
	defer fm.Unlock()
	fm.handles = make(map[string]*handleEntry)
	fm.paths = make(map[string]string)
	fm.clock, fm.hand = nil, 0
}

// Count returns the number of cached handles.
func (fm *FileHandleMap) Count() int {
	fm.RLock()
	defer fm.RUnlock()
	return len(fm.handles)
}

// Allocate returns an 8-byte identifier of the handle issued for f.
//
// Deprecated: handles are byte strings of up to 64 bytes, issued and
// resolved by the server; the identifier is their tag, for logging only.
// No FileHandleMap can be made outside this package.
func (fm *FileHandleMap) Allocate(f absfs.File) uint64 {
	node, ok := f.(*NFSNode)
	if !ok || node.path == "" {
		return 0
	}
	return handleID(fm.issue(node))
}

// Get returns the file of the cached handle whose identifier (see Allocate)
// is handle. It scans the cache.
//
// Deprecated: see Allocate.
func (fm *FileHandleMap) Get(handle uint64) (absfs.File, bool) {
	fm.RLock()
	defer fm.RUnlock()
	for key, e := range fm.handles {
		if handleID([]byte(key)) == handle {
			return e.node, true
		}
	}
	return nil, false
}

// GetOrError is Get with an InvalidFileHandleError for a handle not cached.
//
// Deprecated: see Allocate.
func (fm *FileHandleMap) GetOrError(handle uint64) (absfs.File, error) {
	if f, ok := fm.Get(handle); ok {
		return f, nil
	}
	return nil, &InvalidFileHandleError{Handle: handle, Reason: "handle not found in file handle map"}
}

// Release drops the cached handle whose identifier is handle. The handle
// still resolves.
//
// Deprecated: see Allocate.
func (fm *FileHandleMap) Release(handle uint64) {
	fm.Lock()
	defer fm.Unlock()
	for key, e := range fm.handles {
		if handleID([]byte(key)) == handle {
			fm.drop(key, e)
		}
	}
}

// handleID is the identifier of fh that logs and the deprecated uint64
// methods use: its first 8 bytes.
func handleID(fh []byte) uint64 {
	var b [8]byte
	copy(b[:], fh)
	return binary.BigEndian.Uint64(b[:])
}
