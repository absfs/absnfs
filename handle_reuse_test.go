package absnfs

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/absfs/absfs"
	"github.com/absfs/memfs"
)

// TestEvictedHandleIsStale: a client still holding a handle the server
// evicted gets NFS3ERR_STALE, also after more handles are given out. When
// eviction put numbers back for reuse, the next file looked up got the
// evicted number, and GETATTR with the old handle described that file.
func TestEvictedHandleIsStale(t *testing.T) {
	e := newCacheEnv(t, ExportOptions{})
	e.nfs.fileMap.maxHandles = 10
	for i := 0; i < 12; i++ {
		e.writeFile(fmt.Sprintf("/f%d", i), "x")
	}
	old := e.handle("/f0")
	for i := 1; i <= 10; i++ {
		e.handle(fmt.Sprintf("/f%d", i))
	}
	if _, ok := e.nfs.fileMap.Get(old); ok {
		t.Fatal("/f0's handle was not evicted")
	}

	if h := e.handle("/f11"); h == old {
		t.Fatalf("/f11 was given the evicted handle %d", h)
	}
	if st, a := e.getattr(old); st != NFSERR_STALE {
		t.Errorf("GETATTR of an evicted handle: status %d fileid %d, want NFSERR_STALE", st, a.FileId)
	}

	// /f0 looked up again gets a new handle; the old one stays stale.
	if h := e.handle("/f0"); h == old {
		t.Errorf("/f0 looked up again got its evicted handle %d", h)
	}
	if st, _ := e.getattr(old); st != NFSERR_STALE {
		t.Errorf("GETATTR of an evicted handle after /f0 was looked up again: status %d, want NFSERR_STALE", st)
	}
}

// TestReleasedHandleNumberNotReused: Release and ReleaseAll give no number
// back; the next allocation gets a number above every earlier one.
func TestReleasedHandleNumberNotReused(t *testing.T) {
	fm := newFileHandleMap(0)
	a := fm.Allocate(&NFSNode{path: "/a"})
	b := fm.Allocate(&NFSNode{path: "/b"})
	fm.Release(a)
	c := fm.Allocate(&NFSNode{path: "/c"})
	if c == a || c <= b {
		t.Errorf("allocation after Release(%d) got %d, want a number above %d", a, c, b)
	}
	if _, ok := fm.Get(a); ok {
		t.Errorf("released handle %d still resolves", a)
	}

	fm.ReleaseAll()
	d := fm.Allocate(&NFSNode{path: "/a"})
	if d <= c {
		t.Errorf("allocation after ReleaseAll got %d, want a number above %d", d, c)
	}
	for _, h := range []uint64{a, b, c} {
		if _, ok := fm.Get(h); ok {
			t.Errorf("handle %d still resolves after ReleaseAll", h)
		}
	}
}

// exportedServer serves fs with Export on a free port and returns the
// server and a connection to it.
func exportedServer(t *testing.T, fs absfs.SymlinkFileSystem) (*AbsfsNFS, net.Conn) {
	t.Helper()
	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nfs.Close() })
	if err := nfs.Export("/", 0); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", nfs.ExportPort()), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return nfs, conn
}

// mountHandle sends MNT for p and returns the file handle as it goes on
// the wire (length and data) and as a number.
func mountHandle(t *testing.T, conn net.Conn, xid uint32, p string) ([]byte, uint64) {
	t.Helper()
	var args bytes.Buffer
	xdrEncodeString(&args, p)
	res := exportRPC(t, conn, xid, MOUNT_PROGRAM, MOUNT_V3, 1, args.Bytes())
	if len(res) < 16 || binary.BigEndian.Uint32(res) != 0 || binary.BigEndian.Uint32(res[4:]) != 8 {
		t.Fatalf("MNT %s: reply %x, want status 0 and an 8-byte handle", p, res)
	}
	return res[4:16], binary.BigEndian.Uint64(res[8:16])
}

// getattrStatus sends GETATTR for the wire handle fh and returns the status.
func getattrStatus(t *testing.T, conn net.Conn, xid uint32, fh []byte) uint32 {
	t.Helper()
	res := exportRPC(t, conn, xid, NFS_PROGRAM, NFS_V3, NFSPROC3_GETATTR, fh)
	if len(res) < 4 {
		t.Fatalf("GETATTR: short reply %x", res)
	}
	return binary.BigEndian.Uint32(res)
}

// TestHandlesFromBeforeRestartAreStale: a server started after another
// over the same filesystem gives out none of its handle numbers, so a
// client holding a handle from before the restart gets NFS3ERR_STALE
// instead of reaching whatever the new server gave that number to.
func TestHandlesFromBeforeRestartAreStale(t *testing.T) {
	mfs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	if err := mfs.Mkdir("/d", 0o755); err != nil {
		t.Fatal(err)
	}

	first, conn := exportedServer(t, mfs)
	rootFH, _ := mountHandle(t, conn, 1, "/")
	dirFH, last := mountHandle(t, conn, 2, "/d")
	if st := getattrStatus(t, conn, 3, dirFH); st != NFS_OK {
		t.Fatalf("GETATTR before the restart: status %d", st)
	}
	conn.Close()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if port := first.ExportPort(); port != 0 {
		t.Errorf("ExportPort after Close = %d, want 0 (export stopped)", port)
	}

	// Restart at once, with no wait for the clock: within a process the
	// numbering continues, whatever the clock's resolution.
	_, conn = exportedServer(t, mfs)
	// The restarted server hands out handles in the opposite order, so with
	// numbers restarting from the same point /d would get the old root's.
	newDirFH, newDir := mountHandle(t, conn, 1, "/d")
	newRootFH, _ := mountHandle(t, conn, 2, "/")
	if newDir <= last {
		t.Errorf("restarted server's first handle %d is not above the first server's last %d", newDir, last)
	}
	for _, fh := range [][]byte{newDirFH, newRootFH} {
		if bytes.Equal(fh, rootFH) || bytes.Equal(fh, dirFH) {
			t.Errorf("restarted server gave out handle %x, which the first server had given out", fh)
		}
	}
	for i, fh := range [][]byte{rootFH, dirFH} {
		if st := getattrStatus(t, conn, uint32(10+i), fh); st != NFSERR_STALE {
			t.Errorf("GETATTR of handle %x from before the restart: status %d, want NFSERR_STALE", fh, st)
		}
	}
	if st := getattrStatus(t, conn, 20, newRootFH); st != NFS_OK {
		t.Errorf("GETATTR of the restarted server's root handle: status %d, want NFS_OK", st)
	}
}

// TestHandleNumbersUniqueUnderConcurrency allocates, looks up, releases
// and evicts handles from many goroutines (run with -race). Every
// allocation must get a number never given out before, and a handle must
// only ever resolve to the object it was allocated for.
func TestHandleNumbersUniqueUnderConcurrency(t *testing.T) {
	fm := newFileHandleMap(64)
	const workers, perWorker = 8, 500

	var mu sync.Mutex
	seen := make(map[uint64]string)
	var wg sync.WaitGroup
	errs := make(chan string, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				node := &NFSNode{path: fmt.Sprintf("/w%d/f%d", w, i)}
				h := fm.Allocate(node)
				if i%50 == 0 {
					h = fm.allocateMount(node) // the same handle, now maybe pinned
				}

				mu.Lock()
				prev, dup := seen[h]
				seen[h] = node.path
				mu.Unlock()
				if dup {
					errs <- fmt.Sprintf("handle %d given to %s and again to %s", h, prev, node.path)
					return
				}

				if f, ok := fm.Get(h); ok && f != absfs.File(node) {
					errs <- fmt.Sprintf("handle %d of %s resolved to %s", h, node.path, f.Name())
					return
				}
				if i%2 == 0 {
					fm.Release(h)
					if _, ok := fm.Get(h); ok {
						errs <- fmt.Sprintf("released handle %d of %s resolves again", h, node.path)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	if len(seen) != workers*perWorker {
		t.Errorf("%d distinct handles for %d allocations", len(seen), workers*perWorker)
	}
	if n := fm.Count(); n > 64 {
		t.Errorf("%d live handles, want at most the limit of 64", n)
	}
}

// lookupHandle sends LOOKUP of name in the directory with wire handle dir
// and returns the status and, when NFS_OK, the object's wire handle.
func lookupHandle(t *testing.T, conn net.Conn, xid uint32, dir []byte, name string) (uint32, []byte) {
	t.Helper()
	var args bytes.Buffer
	args.Write(dir)
	xdrEncodeString(&args, name)
	res := exportRPC(t, conn, xid, NFS_PROGRAM, NFS_V3, NFSPROC3_LOOKUP, args.Bytes())
	if len(res) < 4 {
		t.Fatalf("LOOKUP %s: short reply %x", name, res)
	}
	st := binary.BigEndian.Uint32(res)
	if st != NFS_OK {
		return st, nil
	}
	if len(res) < 16 || binary.BigEndian.Uint32(res[4:]) != 8 {
		t.Fatalf("LOOKUP %s: reply %x, want an 8-byte handle", name, res)
	}
	return st, res[4:16]
}

// TestEvictionKeepsMountAndBusyHandles: eviction drops handles not used
// for a while, never the handle MNT gave out (a client that lost its
// mount's root could look nothing up again without remounting), and not a
// directory handle the client keeps using. When eviction dropped the
// lowest numbers first, the root went first, and with numbers no longer
// reused it would have stayed NFS3ERR_STALE for good.
func TestEvictionKeepsMountAndBusyHandles(t *testing.T) {
	mfs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	if err := mfs.Mkdir("/d", 0o755); err != nil {
		t.Fatal(err)
	}
	const files = 40
	for i := 0; i < files; i++ {
		f, err := mfs.Create(fmt.Sprintf("/d/f%d", i))
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	nfs, conn := exportedServer(t, mfs)
	nfs.fileMap.Lock()
	nfs.fileMap.maxHandles = 10
	nfs.fileMap.Unlock()

	xid := uint32(1)
	rootFH, _ := mountHandle(t, conn, xid, "/")
	xid++
	st, dirFH := lookupHandle(t, conn, xid, rootFH, "d")
	if st != NFS_OK {
		t.Fatalf("LOOKUP d: status %d", st)
	}
	var first []byte
	for i := 0; i < files; i++ {
		xid++
		st, fh := lookupHandle(t, conn, xid, dirFH, fmt.Sprintf("f%d", i))
		if st != NFS_OK {
			t.Fatalf("LOOKUP f%d in a directory looked up %d times: status %d", i, i, st)
		}
		if i == 0 {
			first = fh
		}
	}
	if n := nfs.fileMap.Count(); n > 10 {
		t.Errorf("%d live handles, want at most the limit of 10", n)
	}

	xid++
	if st := getattrStatus(t, conn, xid, rootFH); st != NFS_OK {
		t.Errorf("GETATTR of the mount root after %d lookups: status %d, want NFS_OK", files, st)
	}
	xid++
	if st := getattrStatus(t, conn, xid, dirFH); st != NFS_OK {
		t.Errorf("GETATTR of the directory used for every lookup: status %d, want NFS_OK", st)
	}
	xid++
	if st := getattrStatus(t, conn, xid, first); st != NFSERR_STALE {
		t.Errorf("GETATTR of the first file's evicted handle: status %d, want NFSERR_STALE", st)
	}
	// The client recovers the evicted file by looking it up again.
	xid++
	if st, fh := lookupHandle(t, conn, xid, dirFH, "f0"); st != NFS_OK || bytes.Equal(fh, first) {
		t.Errorf("LOOKUP f0 again: status %d handle %x, want NFS_OK and a new handle (old %x)", st, fh, first)
	}
}

// TestEvictionSecondChance: a handle looked up since the eviction clock
// last passed it is kept, and the handles not used are evicted, oldest
// first.
func TestEvictionSecondChance(t *testing.T) {
	fm := newFileHandleMap(10)
	hot := fm.Allocate(&NFSNode{path: "/hot"})
	cold := fm.Allocate(&NFSNode{path: "/cold"})
	for i := 0; i < 50; i++ {
		fm.Allocate(&NFSNode{path: fmt.Sprintf("/f%d", i)})
		if _, ok := fm.Get(hot); !ok {
			t.Fatalf("handle used before every allocation was evicted after %d allocations", i+1)
		}
		if fm.Count() > 10 {
			t.Fatalf("%d live handles, want at most 10", fm.Count())
		}
	}
	if _, ok := fm.Get(cold); ok {
		t.Error("handle never used again survived 50 allocations past the limit")
	}
	// Re-allocating a path also counts as use.
	again := fm.Allocate(&NFSNode{path: "/again"})
	for i := 0; i < 50; i++ {
		fm.Allocate(&NFSNode{path: fmt.Sprintf("/g%d", i)})
		if h := fm.Allocate(&NFSNode{path: "/again"}); h != again {
			t.Fatalf("/again got handle %d, want %d (it was evicted while in use)", h, again)
		}
	}
}

// TestPinnedHandles: MNT's handles are never evicted, at most a tenth of
// the limit (at least one) is pinned, and releasing or retiring a pinned
// handle frees its pin.
func TestPinnedHandles(t *testing.T) {
	fm := newFileHandleMap(10)
	root := fm.allocateMount(&NFSNode{path: "/"})
	extra := fm.allocateMount(&NFSNode{path: "/extra"}) // past the cap of 1: ordinary
	if h := fm.allocateMount(&NFSNode{path: "/"}); h != root {
		t.Fatalf("mounting / again got handle %d, want %d", h, root)
	}
	for i := 0; i < 50; i++ {
		fm.Allocate(&NFSNode{path: fmt.Sprintf("/f%d", i)})
	}
	if _, ok := fm.Get(root); !ok {
		t.Error("pinned mount handle was evicted")
	}
	if _, ok := fm.Get(extra); ok {
		t.Error("mount handle past the pin cap was never evicted")
	}
	if fm.pinned != 1 {
		t.Errorf("%d pinned handles, want 1", fm.pinned)
	}

	// Pinning an existing handle: /f49 is live and gets the freed pin.
	fm.Release(root)
	if fm.pinned != 0 {
		t.Errorf("%d pinned handles after Release, want 0", fm.pinned)
	}
	h := fm.Allocate(&NFSNode{path: "/f49"})
	if p := fm.allocateMount(&NFSNode{path: "/f49"}); p != h {
		t.Fatalf("mounting the live /f49 got handle %d, want its handle %d", p, h)
	}
	for i := 0; i < 50; i++ {
		fm.Allocate(&NFSNode{path: fmt.Sprintf("/g%d", i)})
	}
	if _, ok := fm.Get(h); !ok {
		t.Error("handle pinned after it was allocated was evicted")
	}
	fm.removed("/f49")
	if fm.pinned != 0 {
		t.Errorf("%d pinned handles after the pinned object was removed, want 0", fm.pinned)
	}
	fm.allocateMount(&NFSNode{path: "/m"})
	fm.ReleaseAll()
	if fm.pinned != 0 || len(fm.clock) != 0 {
		t.Errorf("after ReleaseAll: %d pinned, clock of %d, want none", fm.pinned, len(fm.clock))
	}
}

// TestExportRootPinnedPastCap: subdirectory mounts that use up the pin cap
// do not leave the export root evictable. A client mounts /a (taking the
// one pin a limit of 10 allows), another mounts /, and lookups fill the
// table well past the limit: the root handle still works, the table stays
// bounded, and handles nobody uses are still evicted. Before the root was
// exempt from the cap, its handle was evicted and the client got
// NFS3ERR_STALE until it remounted.
func TestExportRootPinnedPastCap(t *testing.T) {
	mfs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	if err := mfs.Mkdir("/a", 0o755); err != nil {
		t.Fatal(err)
	}
	const files = 40
	for i := 0; i < files; i++ {
		f, err := mfs.Create(fmt.Sprintf("/a/f%d", i))
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	nfs, conn := exportedServer(t, mfs)
	nfs.fileMap.Lock()
	nfs.fileMap.maxHandles = 10
	nfs.fileMap.Unlock()

	xid := uint32(1)
	subFH, _ := mountHandle(t, conn, xid, "/a")
	xid++
	rootFH, _ := mountHandle(t, conn, xid, "/")
	var first []byte
	for i := 0; i < files; i++ {
		xid++
		st, fh := lookupHandle(t, conn, xid, subFH, fmt.Sprintf("f%d", i))
		if st != NFS_OK {
			t.Fatalf("LOOKUP f%d: status %d", i, st)
		}
		if i == 0 {
			first = fh
		}
	}
	if n := nfs.fileMap.Count(); n > 10 {
		t.Errorf("%d live handles, want at most the limit of 10", n)
	}
	nfs.fileMap.RLock()
	pinned := nfs.fileMap.pinned
	nfs.fileMap.RUnlock()
	if pinned != 2 {
		t.Errorf("%d pinned handles, want 2 (the cap of 1 plus the export root)", pinned)
	}

	xid++
	if st := getattrStatus(t, conn, xid, rootFH); st != NFS_OK {
		t.Errorf("GETATTR of the export root after %d lookups: status %d, want NFS_OK", files, st)
	}
	xid++
	if st, _ := lookupHandle(t, conn, xid, rootFH, "a"); st != NFS_OK {
		t.Errorf("LOOKUP a from the export root: status %d, want NFS_OK", st)
	}
	xid++
	if st := getattrStatus(t, conn, xid, subFH); st != NFS_OK {
		t.Errorf("GETATTR of the /a mount handle: status %d, want NFS_OK", st)
	}
	xid++
	if st := getattrStatus(t, conn, xid, first); st != NFSERR_STALE {
		t.Errorf("GETATTR of the first file's handle: status %d, want NFSERR_STALE (evicted)", st)
	}
}

// TestRootPinnedHowever: the export root's handle is pinned however it is
// allocated, past the cap, and only once; other paths still respect the
// cap, and a released root frees its pin.
func TestRootPinnedHowever(t *testing.T) {
	fm := newFileHandleMap(20) // cap of 2
	fm.allocateMount(&NFSNode{path: "/m1"})
	fm.allocateMount(&NFSNode{path: "/m2"})
	over := fm.allocateMount(&NFSNode{path: "/m3"}) // past the cap: ordinary
	root := fm.Allocate(&NFSNode{path: "/"})        // not via MNT, still pinned
	if h := fm.allocateMount(&NFSNode{path: "/"}); h != root {
		t.Fatalf("mounting / got handle %d, want the existing %d", h, root)
	}
	if fm.pinned != 3 {
		t.Errorf("%d pinned handles, want 3 (the cap of 2 plus the root)", fm.pinned)
	}
	for i := 0; i < 200; i++ {
		fm.Allocate(&NFSNode{path: fmt.Sprintf("/f%d", i)})
		if n := fm.Count(); n > 20 {
			t.Fatalf("%d live handles after %d allocations, want at most 20", n, i+1)
		}
	}
	if _, ok := fm.Get(root); !ok {
		t.Error("export root's handle was evicted")
	}
	if _, ok := fm.Get(over); ok {
		t.Error("mount handle past the cap was never evicted")
	}
	fm.Release(root)
	if fm.pinned != 2 {
		t.Errorf("%d pinned handles after releasing the root, want 2", fm.pinned)
	}
}

// TestRootPinUnderConcurrency mounts the export root after subdirectory
// mounts used up the pin cap, then mounts more subdirectories and
// allocates, looks up and releases other handles from many goroutines (run
// with -race). The root's handle never changes and always resolves, pins
// stay within the cap plus the root, and the table stays bounded.
func TestRootPinUnderConcurrency(t *testing.T) {
	const limit = 50 // cap of 5
	fm := newFileHandleMap(limit)
	for i := 0; i < limit/10; i++ { // use up the cap before the root mounts
		fm.allocateMount(&NFSNode{path: fmt.Sprintf("/early%d", i)})
	}
	root := fm.allocateMount(&NFSNode{path: "/"})
	const workers, perWorker = 8, 500
	var wg sync.WaitGroup
	errs := make(chan string, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				h := fm.Allocate(&NFSNode{path: fmt.Sprintf("/w%d/f%d", w, i)})
				switch {
				case i%10 == 0:
					fm.allocateMount(&NFSNode{path: fmt.Sprintf("/w%d/m%d", w, i)})
				case i%10 == 1:
					fm.Release(h)
				case i%100 == 99:
					// Use the root rarely, so a root that is not pinned
					// would be evicted in between, and look it up both
					// ways a client does: with its handle and by MNT.
					if f, ok := fm.Get(root); !ok || f.Name() != "/" {
						errs <- fmt.Sprintf("export root's handle %d stopped resolving to / after %d allocations", root, i+1)
						return
					}
					if r := fm.allocateMount(&NFSNode{path: "/"}); r != root {
						errs <- fmt.Sprintf("mounting / got handle %d, want %d", r, root)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	if _, ok := fm.Get(root); !ok {
		t.Error("export root's handle was evicted")
	}
	if n := fm.Count(); n > limit {
		t.Errorf("%d live handles, want at most the limit of %d", n, limit)
	}
	fm.RLock()
	defer fm.RUnlock()
	if fm.pinned > limit/10+1 {
		t.Errorf("%d pinned handles, want at most %d (the cap plus the root)", fm.pinned, limit/10+1)
	}
}

// TestEvictionClockStaysSmall: handles released or retired before the
// clock reaches them do not pile up in it, with or without eviction.
func TestEvictionClockStaysSmall(t *testing.T) {
	fm := newFileHandleMap(100)
	for i := 0; i < 10000; i++ {
		fm.Release(fm.Allocate(&NFSNode{path: fmt.Sprintf("/r%d", i)}))
		fm.Allocate(&NFSNode{path: fmt.Sprintf("/k%d", i)})
		if i%3 == 0 {
			fm.removed(fmt.Sprintf("/k%d", i))
		}
		if n, live := len(fm.clock), fm.Count(); n > 4*live+130 {
			t.Fatalf("after %d rounds the clock holds %d numbers for %d live handles", i+1, n, live)
		}
	}
}

// TestQuickRestartGivesNewNumbers: a FileHandleMap created right after
// another, as when an embedding program closes a server and creates a new
// one, starts above every number the earlier one gave out, even within
// one tick of a coarse clock.
func TestQuickRestartGivesNewNumbers(t *testing.T) {
	for i := 0; i < 1000; i++ {
		before := newFileHandleMap(0)
		last := before.Allocate(&NFSNode{path: "/a"})
		before.ReleaseAll()
		after := newFileHandleMap(0)
		if first := after.Allocate(&NFSNode{path: "/b"}); first <= last {
			t.Fatalf("round %d: a map created after another gave out %d, not above its last %d", i, first, last)
		}
	}
}

// TestNewHandleNotEvictedByItsOwnAllocation: when every older handle was
// used since the eviction hand last passed, the hand clears and requeues
// them all behind the new handle. A new handle that did not count as used
// was then reached while eviction still wanted more, and Allocate returned
// a handle that was already NFS3ERR_STALE.
func TestNewHandleNotEvictedByItsOwnAllocation(t *testing.T) {
	const limit = 10
	fm := newFileHandleMap(limit)
	var hs []uint64
	for i := 0; i < limit; i++ {
		hs = append(hs, fm.Allocate(&NFSNode{path: fmt.Sprintf("/f%d", i)}))
	}
	for round := 0; round < 30; round++ {
		for _, h := range hs {
			fm.Get(h) // every handle used since the hand last passed
		}
		h := fm.Allocate(&NFSNode{path: fmt.Sprintf("/new%d", round)})
		if _, ok := fm.Get(h); !ok {
			t.Fatalf("round %d: handle %d just returned by Allocate is already stale", round, h)
		}
		if n := fm.Count(); n > limit {
			t.Fatalf("round %d: %d live handles, want at most %d", round, n, limit)
		}
		hs = append(hs, h)
	}
}

// TestPinsLeaveRoomAtSmallLimits: pins never take the last place in the
// table, so at the smallest limits a new handle still survives its own
// allocation. A root refused a pin for that reason is pinned once a place
// is free again, when it is allocated or mounted again.
func TestPinsLeaveRoomAtSmallLimits(t *testing.T) {
	fm := newFileHandleMap(2) // cap of 1, and one place must stay unpinned
	a := fm.allocateMount(&NFSNode{path: "/a"})
	root := fm.allocateMount(&NFSNode{path: "/"})
	if fm.pinned != 1 {
		t.Fatalf("%d pinned handles at a limit of 2, want 1", fm.pinned)
	}
	for i := 0; i < 5; i++ {
		h := fm.Allocate(&NFSNode{path: fmt.Sprintf("/f%d", i)})
		if _, ok := fm.Get(h); !ok {
			t.Fatalf("handle %d just returned by Allocate is already stale", h)
		}
		if n := fm.Count(); n > 2 {
			t.Fatalf("%d live handles, want at most 2", n)
		}
	}
	if _, ok := fm.Get(a); !ok {
		t.Error("pinned mount handle was evicted")
	}

	if _, ok := fm.Get(root); ok {
		t.Error("root refused a pin survived allocations past a limit of 2")
	}

	// A live root refused a pin gets one when it is looked up again after
	// a place was freed.
	fm = newFileHandleMap(2)
	a = fm.allocateMount(&NFSNode{path: "/a"})
	root = fm.Allocate(&NFSNode{path: "/"})
	if fm.handles[root].pinned {
		t.Fatal("root pinned into the last place of a table with a limit of 2")
	}
	fm.Release(a)
	if h := fm.Allocate(&NFSNode{path: "/"}); h != root {
		t.Fatalf("allocating / again got handle %d, want %d", h, root)
	}
	if fm.pinned != 1 || !fm.handles[root].pinned {
		t.Fatalf("live root not pinned once a place was free (%d pinned)", fm.pinned)
	}
	for i := 0; i < 5; i++ {
		fm.Allocate(&NFSNode{path: fmt.Sprintf("/g%d", i)})
	}
	if _, ok := fm.Get(root); !ok {
		t.Error("pinned export root was evicted")
	}

	one := newFileHandleMap(1) // nothing can be pinned
	one.allocateMount(&NFSNode{path: "/"})
	h := one.Allocate(&NFSNode{path: "/f"})
	if _, ok := one.Get(h); !ok || one.pinned != 0 || one.Count() != 1 {
		t.Errorf("limit of 1: new handle live %v, %d pinned, %d handles; want true, 0, 1", ok, one.pinned, one.Count())
	}
}
