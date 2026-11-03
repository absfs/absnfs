//go:build unix

package absnfs

import (
	"os"
	"path/filepath"
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
// the way C4 roots each workspace layer at a host folder.
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
