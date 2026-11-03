//go:build unix

package absnfs

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/absfs/basefs"
	"github.com/absfs/osfs"
)

type faInfo struct{ sys any }

func (i faInfo) Name() string       { return "x" }
func (i faInfo) Size() int64        { return 0 }
func (i faInfo) Mode() os.FileMode  { return 0o644 }
func (i faInfo) ModTime() time.Time { return time.Time{} }
func (i faInfo) IsDir() bool        { return false }
func (i faInfo) Sys() any           { return i.sys }

func TestSysOwner(t *testing.T) {
	if _, _, ok := sysOwner(faInfo{}); ok {
		t.Error("nil Sys reported an owner")
	}
	if _, _, ok := sysOwner(faInfo{sys: (*syscall.Stat_t)(nil)}); ok {
		t.Error("nil *Stat_t reported an owner")
	}
	if _, _, ok := sysOwner(faInfo{sys: "something else"}); ok {
		t.Error("non-Stat_t Sys reported an owner")
	}
	uid, gid, ok := sysOwner(faInfo{sys: &syscall.Stat_t{Uid: 12, Gid: 34}})
	if !ok || uid != 12 || gid != 34 {
		t.Errorf("sysOwner = %d, %d, %v; want 12, 34, true", uid, gid, ok)
	}
}

// newOSExport serves a fresh temporary directory through basefs over osfs,
// the way a development workspace roots each layer at a host folder.
func newOSExport(t *testing.T, opts ExportOptions) (*AbsfsNFS, string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ofs, err := osfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	bfs, err := basefs.NewFS(ofs, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	nfs, err := New(bfs, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nfs.Close() })
	return nfs, dir
}

func TestRealOwnerFromOS(t *testing.T) {
	nfs, dir := newOSExport(t, ExportOptions{DefaultOwner: &FileOwner{UID: 4242, GID: 4343}})
	for _, p := range []string{"/", "/file.txt", "/link"} {
		var st syscall.Stat_t
		if err := syscall.Lstat(filepath.Join(dir, p), &st); err != nil {
			t.Fatal(err)
		}
		node, err := nfs.Lookup(p)
		if err != nil {
			t.Fatal(err)
		}
		if node.attrs.Uid != st.Uid || node.attrs.Gid != st.Gid {
			t.Errorf("%s: owner %d:%d, want the OS owner %d:%d", p, node.attrs.Uid, node.attrs.Gid, st.Uid, st.Gid)
		}
	}
	link, err := nfs.Lookup("/link")
	if err != nil {
		t.Fatal(err)
	}
	if link.attrs.Mode&os.ModeSymlink == 0 {
		t.Errorf("/link mode = %v, want a symlink", link.attrs.Mode)
	}
}

func TestForceOwnerOverridesOS(t *testing.T) {
	nfs, _ := newOSExport(t, ExportOptions{DefaultOwner: &FileOwner{UID: 4242, GID: 4343}, ForceOwner: true})
	node, err := nfs.Lookup("/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if node.attrs.Uid != 4242 || node.attrs.Gid != 4343 {
		t.Errorf("owner = %d:%d, want the forced 4242:4343", node.attrs.Uid, node.attrs.Gid)
	}
}

// TestParallelOwnerWrites: clients send WRITEs for one file in parallel; every
// one must get through, and the file keeps its read-only mode.
func TestParallelOwnerWrites(t *testing.T) {
	// osfs: memfs itself is not safe for concurrent chmod and stat.
	nfs, dir := newOSExport(t, ExportOptions{})
	if err := os.Truncate(filepath.Join(dir, "file.txt"), 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "file.txt"), 0o444); err != nil {
		t.Fatal(err)
	}
	node, err := nfs.Lookup("/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	file := nfs.fileMap.issue(node)
	owner := processOwner()
	handler := &NFSProcedureHandler{server: &Server{handler: nfs}}
	const writers = 16
	statuses := make(chan uint32, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			auth := AuthContext{ClientIP: "127.0.0.1", EffectiveUID: owner.UID, EffectiveGID: owner.GID, Credential: &RPCCredential{Flavor: AUTH_SYS}}
			r, _ := handler.handleWrite(bytes.NewReader(faWriteRequest(file, uint64(4*i), []byte("abcd"))), &RPCReply{}, &auth)
			statuses <- readStatusFromReply(r)
		}(i)
	}
	wg.Wait()
	close(statuses)
	for st := range statuses {
		if st != NFS_OK {
			t.Errorf("parallel owner WRITE: status %d", st)
		}
	}
	info, err := os.Lstat(filepath.Join(dir, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o444 || info.Size() != 4*writers {
		t.Errorf("after parallel writes: mode %o size %d, want 444 and %d", info.Mode().Perm(), info.Size(), 4*writers)
	}
}
