// fh_resolve.go: from handle bytes to the NFSNode a request works on.
package absnfs

import (
	"container/list"
	"context"
	"sync"
	"time"
)

const (
	dirIndexLimit   = 1024 // directories whose listings are kept
	resolveAttempts = 3    // stats retried when changes race a resolution
)

// Caps on what one tail handle's walk may cost; variables for tests.
var (
	tailMaxListings = 32  // directory listings
	tailMaxChecks   = 256 // candidate paths tag-checked
)

// resolveHandle returns the node handle fh names, or false when fh is stale:
// not a handle this server issued, issued for an object removed or replaced
// since, or for one that no longer exists. Every failure is reported to the
// client as NFS3ERR_STALE, from which clients recover by looking the name
// up again.
func (s *AbsfsNFS) resolveHandle(fh []byte) (*NFSNode, bool) {
	fm := s.fileMap
	ctx, cancel := context.WithTimeout(context.Background(), inflightWait)
	defer cancel()

	if e, node, p, g := fm.cached(fh); e != nil {
		p2, g2, ok := fm.log.validate(ctx, p, g)
		switch {
		case !ok:
			fm.forget(fh, e)
			return nil, false
		case p2 != p || g2 != g:
			return fm.moved(fh, e, p2, g2), true
		}
		return node, true
	}
	s.recordHandleMiss()

	ph, ok := parseHandle(fh)
	if !ok {
		return nil, false
	}
	var p string
	switch ph.kind {
	case fhKindInline:
		if !fm.codec.verify(fh, ph, ph.path) {
			return nil, false
		}
		p = ph.path
	case fhKindTail:
		if p, ok = s.walkTail(ctx, fh, ph); !ok {
			return nil, false
		}
	default:
		// Opaque handles resolve only from the cache.
		return nil, false
	}

	for attempt := 0; attempt < resolveAttempts; attempt++ {
		seq := fm.log.seq.Load()
		p2, g2, ok := fm.log.validate(ctx, p, ph.g)
		if !ok {
			return nil, false
		}
		node, err := s.Lookup(nodePath(p2))
		if err != nil || fhType(node.attrs.Mode) != ph.typ {
			return nil, false
		}
		// A REMOVE or RENAME that began after validate may have changed
		// what the stat saw: validate again, after it commits.
		if fm.log.seq.Load() != seq {
			continue
		}
		// Not the path's issued bytes unless none are cached: the client
		// may hold those for the object too.
		fm.insert(fh, node, p2, g2, ph.typ, false)
		return node, true
	}
	return nil, false
}

// walkTail recovers the full path of a tail handle: from the directory its
// prefix names, it follows the hashed components through directory
// listings, backtracking over hash collisions, until a candidate's tag
// matches. A listing reflects the directory now, so the walk also looks at
// names renamed away since (they have rename records) and lists a renamed
// directory at its new path. One attempt with cached listings and one with
// fresh ones; each is capped.
func (s *AbsfsNFS) walkTail(ctx context.Context, fh []byte, ph parsedHandle) (string, bool) {
	for _, fresh := range []bool{false, true} {
		w := tailWalker{s: s, ctx: ctx, fh: fh, ph: ph, fresh: fresh}
		if p, ok := w.walk(ph.path, 0); ok {
			return p, true
		}
	}
	s.recordTailWalkFailure()
	return "", false
}

type tailWalker struct {
	s        *AbsfsNFS
	ctx      context.Context
	fh       []byte
	ph       parsedHandle
	fresh    bool
	listings int
	checks   int
}

// walk tries the names below dir (a path in the namespace the handle was
// issued in) whose hashes match from the i-th hashed component on.
func (w *tailWalker) walk(dir string, i int) (string, bool) {
	if i == len(w.ph.hashes) {
		w.checks++
		if w.checks > tailMaxChecks {
			return "", false
		}
		return dir, w.s.fileMap.codec.verify(w.fh, w.ph, dir)
	}
	if w.listings >= tailMaxListings || w.checks >= tailMaxChecks {
		return "", false
	}
	for _, name := range w.candidates(dir, w.ph.hashes[i]) {
		if p, ok := w.walk(joinPath(dir, "/"+name), i+1); ok {
			return p, true
		}
	}
	return "", false
}

// candidates returns the names below dir whose hash is h.
func (w *tailWalker) candidates(dir string, h uint16) []string {
	fm := w.s.fileMap
	now, _, ok := fm.log.validate(w.ctx, dir, w.ph.g)
	seen := make(map[string]bool)
	var out []string
	add := func(names []string) {
		for _, n := range names {
			if !seen[n] && validCanonical(n) && fm.codec.tailHash(n, w.ph.wide) == h {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	if ok {
		w.listings++
		add(fm.dirs.names(w.s, now, w.fresh))
		add(fm.log.redirectedChildren(now))
	}
	add(fm.log.redirectedChildren(dir))
	return out
}

// dirHashIndex caches directory listings for tail walks: an LRU of
// directories, each listing dropped after the attribute cache's timeout or
// when the server changes the directory.
type dirHashIndex struct {
	mu      sync.Mutex
	limit   int
	entries map[string]*list.Element // canonical dir -> *dirListing
	lru     *list.List
}

type dirListing struct {
	dir     string
	names   []string
	expires time.Time
}

func newDirHashIndex(limit int) *dirHashIndex {
	return &dirHashIndex{limit: limit, entries: make(map[string]*list.Element), lru: list.New()}
}

// names lists canonical dir, from the cache unless fresh.
func (d *dirHashIndex) names(s *AbsfsNFS, dir string, fresh bool) []string {
	now := time.Now()
	if !fresh {
		d.mu.Lock()
		if el, ok := d.entries[dir]; ok {
			l := el.Value.(*dirListing)
			if now.Before(l.expires) {
				d.lru.MoveToFront(el)
				d.mu.Unlock()
				return l.names
			}
		}
		d.mu.Unlock()
	}
	f, err := s.fs.Open(nodePath(dir))
	if err != nil {
		return nil
	}
	names, err := f.Readdirnames(-1)
	f.Close()
	if err != nil {
		return nil
	}
	ttl := s.tuning.Load().AttrCacheTimeout
	d.mu.Lock()
	defer d.mu.Unlock()
	if el, ok := d.entries[dir]; ok {
		d.lru.Remove(el)
	}
	d.entries[dir] = d.lru.PushFront(&dirListing{dir: dir, names: names, expires: now.Add(ttl)})
	for d.lru.Len() > d.limit {
		old := d.lru.Back()
		d.lru.Remove(old)
		delete(d.entries, old.Value.(*dirListing).dir)
	}
	return names
}

// forget drops the listing of the directory at NFSNode path p.
func (d *dirHashIndex) forget(p string) {
	dir, _ := canonicalPath(p)
	d.mu.Lock()
	defer d.mu.Unlock()
	if el, ok := d.entries[dir]; ok {
		d.lru.Remove(el)
		delete(d.entries, dir)
	}
}

func (s *AbsfsNFS) recordHandleMiss()      { s.fileMap.misses.Add(1) }
func (s *AbsfsNFS) recordTailWalkFailure() { s.fileMap.tailFailures.Add(1) }
