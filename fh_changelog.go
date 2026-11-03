// fh_changelog.go: what the server's own REMOVE, RMDIR and RENAME did to
// paths, so path handles never reach a different object than the one they
// were issued for.
//
// Every such change gets an epoch from a clock that only moves forward. A
// handle carries g, the newest epoch of a change to its path or one of its
// ancestors when it was issued (eff, below). A handle is valid while no
// change on its path's chain is newer than g; when one is, a rename record
// moves the handle to the new path, and anything else makes it stale.
//
// Memory is bounded: tombstones (path hash -> epoch) live in an
// open-addressing table of at most 2^20 entries; when it is full the oldest
// tenth folds into a coarse array indexed by the hash's low bits, which only
// ever raises what lookup returns. Rename records live in an LRU of
// redirectLimit paths; dropping one leaves the tombstone, so the handle goes
// stale instead of following the rename. Every approximation errs towards a
// spurious NFS3ERR_STALE, never a wrong object.
package absnfs

import (
	"container/list"
	"context"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	redirectDepth  = 4 // records kept per path
	maxRedirectHop = 8 // renames validate follows for one handle
)

// Bounds of the change log; variables so tests can make them small.
var (
	tombLimit     = 1 << 20 // exact tombstones before the oldest fold
	coarseSlots   = 1 << 20 // the coarse array (8 MiB, made on first fold); a power of 2
	redirectLimit = 16384   // paths with rename records
)

// fhEpochBase is epoch 0 of the change-log clock. Epochs count microseconds
// since then, so a server restarted with a journal keeps counting upward.
var fhEpochBase = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

// redirect records that src was renamed to dst at epoch e, when src's
// effective epoch was from.
type redirect struct {
	e, from uint64
	dst     string
}

type redirectList struct {
	src  string
	recs []redirect // oldest first, at most redirectDepth
}

// inflightOp is a REMOVE, RMDIR or RENAME between begin and commit.
type inflightOp struct {
	paths []string
	done  chan struct{}
}

// changeLog is the record of the server's own namespace changes.
type changeLog struct {
	hash func(string) uint64 // keyed path hash (handleCodec.pathHash)

	mu     sync.RWMutex
	last   uint64 // newest epoch given out
	tombs  tombTable
	coarse []uint64 // nil until the first fold

	redirects map[string]*list.Element // src -> element holding *redirectList
	lru       *list.List               // front is most recently recorded
	children  map[string]map[string]int

	inflight  map[*inflightOp]struct{}
	nInflight atomic.Int64
	seq       atomic.Uint64 // bumped at every begin and commit

	journal *handleJournal // nil unless HandleStateDir is set

	// active is set once anything is recorded; until then validate and
	// effective need no lock.
	active atomic.Bool

	folds       atomic.Uint64
	redirDrops  atomic.Uint64
	staleWaited atomic.Uint64
}

func newChangeLog(hash func(string) uint64) *changeLog {
	return &changeLog{
		hash:      hash,
		redirects: make(map[string]*list.Element),
		lru:       list.New(),
		children:  make(map[string]map[string]int),
		inflight:  make(map[*inflightOp]struct{}),
	}
}

// nextEpoch returns a new epoch, above every one given out before and, as
// far as the clock allows, the time in microseconds since fhEpochBase. The
// caller holds l.mu.
func (l *changeLog) nextEpoch() uint64 {
	now := uint64(max(time.Since(fhEpochBase).Microseconds(), 0))
	l.last = max(l.last+1, now)
	return l.last
}

// lookup returns the newest epoch at which p was removed, replaced or
// renamed away (0 if never, as far as the log knows). Collisions and folds
// only ever make it larger. The caller holds l.mu.
func (l *changeLog) lookup(p string) uint64 {
	h := l.hash(p)
	if e, ok := l.tombs.get(h); ok {
		return e
	}
	if l.coarse != nil {
		return l.coarse[h&uint64(len(l.coarse)-1)]
	}
	return 0
}

// setTomb records that p changed at epoch e. The caller holds l.mu.
func (l *changeLog) setTomb(p string, e uint64) {
	l.setTombHash(l.hash(p), e)
}

func (l *changeLog) setTombHash(h, e uint64) {
	if old, ok := l.tombs.get(h); ok && old >= e {
		return
	}
	if l.tombs.n >= tombLimit {
		l.fold()
	}
	l.tombs.put(h, e)
}

// fold moves the oldest tenth of the exact tombstones into the coarse
// array. The caller holds l.mu.
func (l *changeLog) fold() {
	if l.coarse == nil {
		l.coarse = make([]uint64, coarseSlots)
	}
	entries := l.tombs.entries()
	slices.SortFunc(entries, func(a, b tombEntry) int {
		switch {
		case a.e < b.e:
			return -1
		case a.e > b.e:
			return 1
		}
		return 0
	})
	n := max(len(entries)/10, 1)
	for _, t := range entries[:n] {
		slot := t.h & uint64(len(l.coarse)-1)
		l.coarse[slot] = max(l.coarse[slot], t.e)
	}
	l.tombs = tombTable{}
	for _, t := range entries[n:] {
		l.tombs.put(t.h, t.e)
	}
	l.folds.Add(1)
}

// chain returns p and its ancestors, from the root ("") down to p. The
// root itself is never removed or renamed, so lookups skip it: with a
// folded coarse array it could otherwise share a slot with a removed path
// and stale every handle at once.
func chain(p string) []string {
	out := []string{""}
	for i := 0; i < len(p); i++ {
		if p[i] == '/' {
			out = append(out, p[:i])
		}
	}
	if p != "" {
		out = append(out, p)
	}
	return out
}

// eff is the newest change to p or one of its ancestors. The caller holds
// l.mu.
func (l *changeLog) eff(p string) uint64 {
	var g uint64
	for _, c := range chain(p)[1:] {
		g = max(g, l.lookup(c))
	}
	return g
}

// effective is eff for a handle about to be issued, after any change in
// flight on p's chain has committed (or ctx ended: a handle issued before a
// commit is stale or follows the change after it, which is safe).
func (l *changeLog) effective(ctx context.Context, p string) uint64 {
	l.await(ctx, p)
	if !l.active.Load() {
		return 0
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.eff(p)
}

// validate reports where a handle issued for p at epoch g leads now:
// p itself, the path a chain of RENAMEs moved it to (with the epoch of the
// last), or nowhere (ok false), when the object was removed or replaced, or
// a record needed to follow it was dropped. It waits for changes in flight
// on the chain to commit first.
func (l *changeLog) validate(ctx context.Context, p string, g uint64) (string, uint64, bool) {
	if !l.await(ctx, p) {
		return "", 0, false
	}
	if !l.active.Load() {
		return p, g, true // nothing ever changed: every handle is valid
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	for hop := 0; ; hop++ {
		cs := chain(p)
		changed := false
		for _, c := range cs[1:] {
			if l.lookup(c) > g {
				changed = true
				break
			}
		}
		if !changed {
			return p, g, true
		}
		if hop == maxRedirectHop {
			return "", 0, false
		}
		// The oldest rename since g of a component on the chain that
		// moved the object as the handle knew it.
		best, bestAt := redirect{}, -1
		for i, c := range cs[1:] {
			i++
			el, ok := l.redirects[c]
			if !ok {
				continue
			}
			for _, r := range el.Value.(*redirectList).recs {
				if r.e > g && r.from <= g && (bestAt < 0 || r.e < best.e) {
					best, bestAt = r, i
				}
			}
		}
		if bestAt < 0 {
			return "", 0, false
		}
		// Nothing below the renamed component changed since g: a name
		// removed and made again before its parent moved is another object.
		for _, d := range cs[bestAt+1:] {
			if l.lookup(d) > g {
				return "", 0, false
			}
		}
		p = joinPath(best.dst, p[len(cs[bestAt]):])
		g = best.e
	}
}

// joinPath appends rest ("" or "/name...") to dir, both canonical.
func joinPath(dir, rest string) string {
	if dir == "" {
		return strings.TrimPrefix(rest, "/")
	}
	return dir + rest
}

// redirectedChildren returns the names below dir that were renamed away
// and still have rename records: a tail walk looks for them too, since a
// listing no longer shows them.
func (l *changeLog) redirectedChildren(dir string) []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	names := make([]string, 0, len(l.children[dir]))
	for name := range l.children[dir] {
		names = append(names, name)
	}
	return names
}

// await waits until no change is in flight on p's chain (p, an ancestor,
// or a descendant being renamed or removed is not on it). It reports false
// if ctx ends first.
func (l *changeLog) await(ctx context.Context, p string) bool {
	for l.nInflight.Load() > 0 {
		done := l.blocking(p)
		if done == nil {
			return true
		}
		l.staleWaited.Add(1)
		select {
		case <-done:
		case <-ctx.Done():
			return false
		}
	}
	return true
}

// blocking returns the done channel of a change in flight on p's chain.
func (l *changeLog) blocking(p string) chan struct{} {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for op := range l.inflight {
		for _, q := range op.paths {
			if q == p || isAncestor(q, p) {
				return op.done
			}
		}
	}
	return nil
}

// isAncestor reports whether a is a proper ancestor of p (canonical paths).
func isAncestor(a, p string) bool {
	if a == "" {
		return p != ""
	}
	return len(p) > len(a) && p[len(a)] == '/' && p[:len(a)] == a
}

// begin registers a REMOVE, RMDIR or RENAME of the given canonical paths
// before the filesystem is changed. Handles on their chains wait in
// validate and effective until commit.
func (l *changeLog) begin(paths ...string) *inflightOp {
	op := &inflightOp{paths: paths, done: make(chan struct{})}
	l.mu.Lock()
	l.inflight[op] = struct{}{}
	l.mu.Unlock()
	l.nInflight.Add(1)
	l.seq.Add(1)
	return op
}

// commitRemove ends op; if the filesystem change succeeded, p was removed.
func (l *changeLog) commitRemove(op *inflightOp, ok bool, p string) {
	l.mu.Lock()
	if ok {
		l.active.Store(true)
		e := l.nextEpoch()
		l.setTomb(p, e)
		l.journal.recordTomb(l.hash(p), e)
	}
	l.end(op)
}

// commitRename ends op and returns the epoch it recorded. If the
// filesystem change succeeded, src was renamed to dst, replacing whatever
// dst named; if it failed but dst may have been removed on the way
// (dstChanged), dst's handles become stale.
func (l *changeLog) commitRename(op *inflightOp, ok, dstChanged bool, src, dst string) uint64 {
	var e uint64
	l.mu.Lock()
	switch {
	case ok && src != dst:
		l.active.Store(true)
		from := l.eff(src)
		e = l.nextEpoch()
		l.setTomb(src, e)
		l.setTomb(dst, e)
		l.addRedirect(src, redirect{e: e, from: from, dst: dst})
		l.journal.recordRename(e, from, src, dst)
	case !ok && dstChanged:
		l.active.Store(true)
		e = l.nextEpoch()
		l.setTomb(dst, e)
		l.journal.recordTomb(l.hash(dst), e)
	}
	l.end(op)
	return e
}

// end unregisters op and releases l.mu. The caller holds l.mu.
func (l *changeLog) end(op *inflightOp) {
	delete(l.inflight, op)
	l.mu.Unlock()
	l.seq.Add(1)
	l.nInflight.Add(-1)
	close(op.done)
}

// addRedirect records a rename of src. The caller holds l.mu.
func (l *changeLog) addRedirect(src string, r redirect) {
	if el, ok := l.redirects[src]; ok {
		rl := el.Value.(*redirectList)
		rl.recs = append(rl.recs, r)
		if len(rl.recs) > redirectDepth {
			rl.recs = rl.recs[len(rl.recs)-redirectDepth:]
		}
		l.lru.MoveToFront(el)
		return
	}
	l.redirects[src] = l.lru.PushFront(&redirectList{src: src, recs: []redirect{r}})
	dir, name := splitPath(src)
	if l.children[dir] == nil {
		l.children[dir] = make(map[string]int)
	}
	l.children[dir][name]++
	for len(l.redirects) > redirectLimit {
		oldest := l.lru.Back()
		rl := oldest.Value.(*redirectList)
		l.lru.Remove(oldest)
		delete(l.redirects, rl.src)
		dir, name := splitPath(rl.src)
		if l.children[dir][name]--; l.children[dir][name] <= 0 {
			delete(l.children[dir], name)
			if len(l.children[dir]) == 0 {
				delete(l.children, dir)
			}
		}
		l.redirDrops.Add(1)
	}
}

// splitPath splits a canonical path into its parent and last component.
func splitPath(p string) (string, string) {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i], p[i+1:]
	}
	return "", p
}

// tombEntry is one exact tombstone.
type tombEntry struct{ h, e uint64 }

// tombTable maps path hashes to epochs with open addressing and linear
// probing: 16 bytes a slot, grown by doubling at 3/4 load. Hash 0 marks an
// empty slot, so a path hashing to 0 is stored as 1 (a collision, which can
// only make a handle stale).
type tombTable struct {
	slots []tombEntry
	n     int
}

func (t *tombTable) get(h uint64) (uint64, bool) {
	if h == 0 {
		h = 1
	}
	if len(t.slots) == 0 {
		return 0, false
	}
	mask := uint64(len(t.slots) - 1)
	for i := h & mask; ; i = (i + 1) & mask {
		switch t.slots[i].h {
		case h:
			return t.slots[i].e, true
		case 0:
			return 0, false
		}
	}
}

func (t *tombTable) put(h, e uint64) {
	if h == 0 {
		h = 1
	}
	if 4*(t.n+1) > 3*len(t.slots) {
		t.grow()
	}
	mask := uint64(len(t.slots) - 1)
	for i := h & mask; ; i = (i + 1) & mask {
		switch t.slots[i].h {
		case h:
			t.slots[i].e = e
			return
		case 0:
			t.slots[i] = tombEntry{h, e}
			t.n++
			return
		}
	}
}

func (t *tombTable) grow() {
	old := t.slots
	t.slots = make([]tombEntry, max(2*len(old), 64))
	t.n = 0
	for _, s := range old {
		if s.h != 0 {
			t.put(s.h, s.e)
		}
	}
}

func (t *tombTable) entries() []tombEntry {
	out := make([]tombEntry, 0, t.n)
	for _, s := range t.slots {
		if s.h != 0 {
			out = append(out, s)
		}
	}
	return out
}
