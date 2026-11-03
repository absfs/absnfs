package absnfs

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"
)

func testLog() *changeLog {
	return newChangeLog(testCodec().pathHash)
}

func remove(l *changeLog, p string) {
	l.commitRemove(l.begin(p), true, p)
}

func rename(l *changeLog, src, dst string) uint64 {
	return l.commitRename(l.begin(src, dst), true, false, src, dst)
}

// follows asserts where a handle issued for p at epoch g leads.
func follows(t *testing.T, l *changeLog, p string, g uint64, want string) {
	t.Helper()
	got, _, ok := l.validate(context.Background(), p, g)
	switch {
	case want == "" && ok:
		t.Errorf("handle of %s (g %d) leads to %s, want stale", p, g, got)
	case want != "" && !ok:
		t.Errorf("handle of %s (g %d) is stale, want %s", p, g, want)
	case want != "" && got != want:
		t.Errorf("handle of %s (g %d) leads to %s, want %s", p, g, got, want)
	}
}

func TestChangeLogRemoveAndRecreate(t *testing.T) {
	l := testLog()
	ctx := context.Background()
	g := l.effective(ctx, "d/f")
	if g != 0 {
		t.Fatalf("untouched path has epoch %d", g)
	}
	remove(l, "d/f")
	follows(t, l, "d/f", g, "")
	follows(t, l, "d/other", 0, "d/other")
	// The object made again at d/f gets a newer epoch, so a new handle.
	g2 := l.effective(ctx, "d/f")
	if g2 <= g {
		t.Fatalf("recreated path has epoch %d, want above %d", g2, g)
	}
	follows(t, l, "d/f", g2, "d/f")
	// Removing a directory stales everything issued below it before.
	remove(l, "d")
	follows(t, l, "d/x/y", 0, "")
}

func TestChangeLogRenames(t *testing.T) {
	l := testLog()
	ctx := context.Background()

	// A renamed file's handle follows it; the target's goes stale.
	rename(l, "a", "b")
	follows(t, l, "a", 0, "b")
	follows(t, l, "b", 0, "")
	// What is made at the old name later is another object.
	ga := l.effective(ctx, "a")
	follows(t, l, "a", ga, "a")

	// A directory rename carries every descendant along.
	rename(l, "dir", "moved/dir")
	follows(t, l, "dir/x/y/z", 0, "moved/dir/x/y/z")

	// Chains of renames are followed, in order.
	rename(l, "p", "q")
	rename(l, "q", "r")
	rename(l, "r", "p")
	follows(t, l, "p", 0, "p")
	follows(t, l, "q", 0, "")

	// A child removed and made again before its parent moved is stale:
	// its handle is for the old child.
	remove(l, "s/c")
	rename(l, "s", "t")
	follows(t, l, "s/c", 0, "")
	follows(t, l, "s/d", 0, "t/d")

	// Rename of a parent after a rename of its child, and the other way.
	rename(l, "u/v", "u/w")
	rename(l, "u", "U")
	follows(t, l, "u/v/f", 0, "U/w/f")
	rename(l, "m", "M")
	rename(l, "M/n", "M/N")
	follows(t, l, "m/n/f", 0, "M/N/f")
}

// git's silly rename: the client renames an open, deleted index to
// .nfsXXXX, then index.lock over index. The open file's handle follows it,
// and the new index has other bytes.
func TestChangeLogSillyRename(t *testing.T) {
	l := testLog()
	ctx := context.Background()
	gIndex := l.effective(ctx, "repo/.git/index")
	gLock := l.effective(ctx, "repo/.git/index.lock")
	rename(l, "repo/.git/index", "repo/.git/.nfs0001")
	e2 := rename(l, "repo/.git/index.lock", "repo/.git/index")
	follows(t, l, "repo/.git/index", gIndex, "repo/.git/.nfs0001")
	follows(t, l, "repo/.git/index.lock", gLock, "repo/.git/index")
	if g := l.effective(ctx, "repo/.git/index"); g != e2 {
		t.Errorf("the new index has epoch %d, want %d", g, e2)
	}
	// The next round reuses index.lock as a name and index as a target:
	// both old handles still lead where they did.
	rename(l, "repo/.git/index", "repo/.git/.nfs0002")
	rename(l, "repo/.git/index.lock", "repo/.git/index")
	follows(t, l, "repo/.git/index", gIndex, "repo/.git/.nfs0001")
	follows(t, l, "repo/.git/index.lock", gLock, "repo/.git/.nfs0002")
}

func TestChangeLogFailedChangeRecordsNothing(t *testing.T) {
	l := testLog()
	l.commitRemove(l.begin("d"), false, "d") // RMDIR of a non-empty directory
	l.commitRename(l.begin("a", "b"), false, false, "a", "b")
	follows(t, l, "d/f", 0, "d/f")
	follows(t, l, "a", 0, "a")
	follows(t, l, "b", 0, "b")
	if l.active.Load() {
		t.Error("failed changes made the log active")
	}
	// A failed rename that may have removed its target stales the target.
	l.commitRename(l.begin("a", "b"), false, true, "a", "b")
	follows(t, l, "b", 0, "")
	follows(t, l, "a", 0, "a")
}

func TestChangeLogHopLimitAndDroppedRecords(t *testing.T) {
	l := testLog()
	names := []string{"n0"}
	for i := 1; i <= maxRedirectHop+1; i++ {
		names = append(names, fmt.Sprintf("n%d", i))
		rename(l, names[i-1], names[i])
	}
	follows(t, l, "n0", 0, "") // more renames than validate follows

	defer func(n int) { redirectLimit = n }(redirectLimit)
	redirectLimit = 4
	l = testLog()
	rename(l, "keep", "kept")
	for i := 0; i < 10; i++ {
		rename(l, fmt.Sprintf("x%d", i), fmt.Sprintf("y%d", i))
	}
	follows(t, l, "keep", 0, "") // its record was dropped: stale, never wrong
	follows(t, l, "x9", 0, "y9")
	if l.redirDrops.Load() == 0 {
		t.Error("no dropped records counted")
	}
	if len(l.redirects) > redirectLimit || len(l.children) > redirectLimit {
		t.Errorf("%d records and %d parent entries kept, limit %d", len(l.redirects), len(l.children), redirectLimit)
	}
}

// A change in flight holds handles on its chain until it commits.
func TestChangeLogInflightWait(t *testing.T) {
	l := testLog()
	op := l.begin("d/f")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, ok := l.validate(ctx, "d/f", 0); ok {
		t.Fatal("validate did not wait for the remove in flight")
	}
	follows(t, l, "d/g", 0, "d/g") // not on the chain: no wait

	done := make(chan bool)
	go func() {
		_, _, ok := l.validate(context.Background(), "d/f", 0)
		done <- ok
	}()
	time.Sleep(10 * time.Millisecond)
	l.commitRemove(op, true, "d/f")
	if <-done {
		t.Error("handle of a file removed while it waited is valid")
	}
	if l.staleWaited.Load() == 0 {
		t.Error("waits not counted")
	}
	if l.nInflight.Load() != 0 {
		t.Error("op still in flight")
	}
}

// The tombstone table folds its oldest tenth into the coarse array when
// full; that may stale handles spuriously, never lead them wrong.
func TestChangeLogFold(t *testing.T) {
	defer func(a, b int) { tombLimit, coarseSlots = a, b }(tombLimit, coarseSlots)
	tombLimit, coarseSlots = 64, 16
	l := testLog()
	gKeep := l.effective(context.Background(), "keep/f")
	for i := 0; i < 1000; i++ {
		remove(l, fmt.Sprintf("gone/%d", i))
	}
	if l.folds.Load() == 0 || l.coarse == nil {
		t.Fatal("no fold")
	}
	if l.tombs.n > tombLimit {
		t.Errorf("%d exact tombstones, limit %d", l.tombs.n, tombLimit)
	}
	for i := 0; i < 1000; i++ {
		follows(t, l, fmt.Sprintf("gone/%d", i), 0, "")
	}
	// keep/f was never touched; with 16 coarse slots it shares one with
	// removed paths, so it may be stale, but if valid it is itself.
	if p, _, ok := l.validate(context.Background(), "keep/f", gKeep); ok && p != "keep/f" {
		t.Errorf("an untouched handle leads to %s", p)
	}
}

func TestTombTable(t *testing.T) {
	var tt tombTable
	for i := uint64(1); i <= 1000; i++ {
		tt.put(i, i*10)
	}
	tt.put(5, 7)
	for i := uint64(1); i <= 1000; i++ {
		want := i * 10
		if i == 5 {
			want = 7
		}
		if e, ok := tt.get(i); !ok || e != want {
			t.Fatalf("get(%d) = %d, %v; want %d", i, e, ok, want)
		}
	}
	if _, ok := tt.get(5000); ok {
		t.Error("found a key never put")
	}
	if tt.n != 1000 || len(tt.entries()) != 1000 {
		t.Errorf("n %d, entries %d", tt.n, len(tt.entries()))
	}
	// Hash 0 marks empty slots, so it shares hash 1's tombstone: a
	// collision, which can only make a handle stale.
	if e, ok := tt.get(0); !ok || e != 10 {
		t.Errorf("get(0) = %d, %v; want hash 1's 10", e, ok)
	}
}

// modelFS is the namespace as the server changes it: path -> object id.
type modelFS map[string]int

func (m modelFS) children(dir string) []string {
	var out []string
	for p := range m {
		if p != "" && parentOf(p) == dir {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func parentOf(p string) string {
	d, _ := splitPath(p)
	return d
}

// TestChangeLogNeverLeadsToAnotherObject drives random REMOVEs, RMDIRs
// and RENAMEs (over targets too) and checks every handle issued along the
// way: where validate leads must be the object the handle was issued for.
// With bounds small enough to fold and drop records, stale is allowed.
func TestChangeLogNeverLeadsToAnotherObject(t *testing.T) {
	for _, small := range []bool{false, true} {
		t.Run(fmt.Sprintf("small=%v", small), func(t *testing.T) {
			if small {
				defer func(a, b, c int) { tombLimit, coarseSlots, redirectLimit = a, b, c }(tombLimit, coarseSlots, redirectLimit)
				tombLimit, coarseSlots, redirectLimit = 32, 8, 8
			}
			for seed := int64(1); seed <= 4; seed++ {
				modelCheck(t, rand.New(rand.NewSource(seed)), 4000, small)
			}
		})
	}
}

func modelCheck(t *testing.T, r *rand.Rand, steps int, small bool) {
	l := testLog()
	ctx := context.Background()
	m := modelFS{"": 0}
	dirs := map[int]bool{0: true}
	next := 1
	type handle struct {
		id int
		p  string
		g  uint64
	}
	var handles []handle
	where := func(id int) string {
		for p, o := range m {
			if o == id {
				return p
			}
		}
		return ""
	}
	names := []string{"a", "b", "c", "index", "index.lock", ".nfs1"}
	randomDir := func() string {
		var ds []string
		for p, id := range m {
			if dirs[id] {
				ds = append(ds, p)
			}
		}
		sort.Strings(ds)
		return ds[r.Intn(len(ds))]
	}
	stale, valid, spurious := 0, 0, 0
	for step := 0; step < steps; step++ {
		dir := randomDir()
		p := joinPath(dir, "/"+names[r.Intn(len(names))])
		switch op := r.Intn(10); {
		case op < 3: // create
			if _, ok := m[p]; !ok && strings.Count(p, "/") < 4 {
				m[p] = next
				dirs[next] = r.Intn(2) == 0
				next++
			}
		case op < 5: // remove, rmdir
			if id, ok := m[p]; ok && len(m.children(p)) == 0 {
				delete(m, p)
				remove(l, p)
				_ = id
			}
		case op < 8: // rename
			src := p
			dst := joinPath(randomDir(), "/"+names[r.Intn(len(names))])
			id, ok := m[src]
			if !ok || src == dst || isAncestor(src, dst) || strings.Count(dst, "/")+subtreeDepth(m, src) > 6 {
				continue
			}
			if tid, exists := m[dst]; exists {
				if dirs[tid] != dirs[id] || len(m.children(dst)) > 0 {
					continue
				}
				delete(m, dst)
			}
			moved := map[string]int{}
			for q, o := range m {
				if q == src || isAncestor(src, q) {
					moved[dst+q[len(src):]] = o
					delete(m, q)
				}
			}
			for q, o := range moved {
				m[q] = o
			}
			rename(l, src, dst)
		default: // issue a handle for an existing object
			if id, ok := m[p]; ok {
				handles = append(handles, handle{id: id, p: p, g: l.effective(ctx, p)})
			}
		}
		if step%50 != 0 {
			continue
		}
		for _, h := range handles {
			got, _, ok := l.validate(ctx, h.p, h.g)
			now := where(h.id)
			switch {
			case ok && got != now:
				t.Fatalf("step %d: handle of object %d issued at %s (g %d) leads to %s (object %d), but the object is at %q",
					step, h.id, h.p, h.g, got, m[got], now)
			case ok:
				valid++
			case now != "":
				spurious++
			default:
				stale++
			}
		}
	}
	t.Logf("%d handles: %d checks valid, %d stale, %d stale while the object exists", len(handles), valid, stale, spurious)
	// Spurious stales are allowed (dropped records, folds, and old paths
	// reused after their directory moved) but must stay rare.
	if valid == 0 || (!small && spurious > valid/10) {
		t.Errorf("%d valid, %d spurious stale", valid, spurious)
	}
}

func subtreeDepth(m modelFS, p string) int {
	d := 0
	for q := range m {
		if isAncestor(p, q) {
			d = max(d, strings.Count(q[len(p):], "/"))
		}
	}
	return d
}
