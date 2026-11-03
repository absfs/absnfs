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

	// A restart starts after the last handle was given out. Wait until the
	// clock says so, which on a coarse clock (Windows) can take a tick.
	for deadline := time.Now().Add(2 * time.Second); uint64(time.Now().UnixNano()) <= last; {
		if time.Now().After(deadline) {
			t.Fatal("clock did not pass the last handle")
		}
		time.Sleep(time.Millisecond)
	}

	_, conn = exportedServer(t, mfs)
	// The restarted server hands out handles in the opposite order, so with
	// numbers restarting from the same point /d would get the old root's.
	newDirFH, _ := mountHandle(t, conn, 1, "/d")
	newRootFH, _ := mountHandle(t, conn, 2, "/")
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
