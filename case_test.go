package absnfs

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/absfs/absfs"
	"github.com/absfs/basefs"
	"github.com/absfs/osfs"
	"github.com/absfs/unionfs"
)

// foldFS looks names up as a case-insensitive host does: a name that is
// not in its directory finds an entry spelled with letters of another
// case. It counts the directories it opens.
type foldFS struct {
	absfs.SymlinkFileSystem
	opens atomic.Int64
}

func newFoldFS(t *testing.T) *foldFS {
	return &foldFS{SymlinkFileSystem: newMemFS(t)}
}

// fold returns the stored spelling of p, or p when nothing matches.
func (f *foldFS) fold(p string) string {
	out := "/"
	for _, name := range strings.Split(strings.Trim(p, "/"), "/") {
		if name == "" {
			continue
		}
		next := path.Join(out, name)
		if _, err := f.SymlinkFileSystem.Lstat(next); err != nil {
			if d, err := f.SymlinkFileSystem.Open(out); err == nil {
				names, _ := d.Readdirnames(-1)
				d.Close()
				for _, n := range names {
					if strings.EqualFold(n, name) {
						next = path.Join(out, n)
						break
					}
				}
			}
		}
		out = next
	}
	return out
}

func (f *foldFS) Lstat(p string) (os.FileInfo, error) { return f.SymlinkFileSystem.Lstat(f.fold(p)) }
func (f *foldFS) Stat(p string) (os.FileInfo, error)  { return f.SymlinkFileSystem.Stat(f.fold(p)) }
func (f *foldFS) Open(p string) (absfs.File, error) {
	f.opens.Add(1)
	return f.SymlinkFileSystem.Open(f.fold(p))
}

// pathconfCase asks PATHCONF about p and returns case_insensitive and
// case_preserving.
func pathconfCase(t *testing.T, nfs *AbsfsNFS, p string) (insensitive, preserving uint32) {
	t.Helper()
	node, err := nfs.Lookup(p)
	if err != nil {
		t.Fatalf("lookup %s: %v", p, err)
	}
	var body bytes.Buffer
	xdrEncodeFileHandle(&body, nfs.fileMap.issue(node))
	h := &NFSProcedureHandler{server: &Server{handler: nfs}}
	reply, err := h.handlePathconf(&body, &RPCReply{}, &AuthContext{})
	if err != nil {
		t.Fatal(err)
	}
	data := reply.Data.([]byte)
	if st := binary.BigEndian.Uint32(data); st != NFS_OK {
		t.Fatalf("PATHCONF %s: status %d", p, st)
	}
	tail := data[len(data)-8:]
	return binary.BigEndian.Uint32(tail), binary.BigEndian.Uint32(tail[4:])
}

// fsinfoProperties asks FSINFO about the root and returns its properties.
func fsinfoProperties(t *testing.T, nfs *AbsfsNFS) uint32 {
	t.Helper()
	node, err := nfs.Lookup("/")
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	xdrEncodeFileHandle(&body, nfs.fileMap.issue(node))
	h := &NFSProcedureHandler{server: &Server{handler: nfs}}
	reply, err := h.handleFsinfo(&body, &RPCReply{}, &AuthContext{})
	if err != nil {
		t.Fatal(err)
	}
	data := reply.Data.([]byte)
	return binary.BigEndian.Uint32(data[len(data)-4:])
}

func newCaseNFS(t *testing.T, fs absfs.SymlinkFileSystem, opts ExportOptions) *AbsfsNFS {
	t.Helper()
	nfs, err := New(fs, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nfs.Close() })
	return nfs
}

func TestSwapASCIICase(t *testing.T) {
	for in, want := range map[string]string{
		"README.md": "readme.MD",
		"go.mod":    "GO.MOD",
		"123_-.":    "123_-.",
		"Ünïcode":   "ÜNïCODE",
	} {
		if got := swapASCIICase(in); got != want {
			t.Errorf("swapASCIICase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCaseInsensitivityOption(t *testing.T) {
	for c, want := range map[CaseInsensitivity]string{
		CaseInsensitiveAuto:  "auto",
		CaseInsensitiveTrue:  "true",
		CaseInsensitiveFalse: "false",
		7:                    "CaseInsensitivity(7)",
	} {
		if got := c.String(); got != want {
			t.Errorf("String() = %q, want %q", got, want)
		}
	}
	if _, err := New(newMemFS(t), ExportOptions{CaseInsensitive: 3}); err == nil {
		t.Error("New accepted CaseInsensitive 3")
	}

	nfs := newCaseNFS(t, newMemFS(t), ExportOptions{CaseInsensitive: CaseInsensitiveTrue})
	opts := nfs.GetExportOptions()
	if opts.CaseInsensitive != CaseInsensitiveTrue {
		t.Fatalf("GetExportOptions().CaseInsensitive = %v, want true", opts.CaseInsensitive)
	}
	if err := nfs.UpdateExportOptions(opts); err != nil {
		t.Fatalf("passing GetExportOptions back: %v", err)
	}
	opts.CaseInsensitive = CaseInsensitiveAuto
	if err := nfs.UpdateExportOptions(opts); err == nil {
		t.Fatal("UpdateExportOptions changed CaseInsensitive")
	}
	if got, _ := pathconfCase(t, nfs, "/"); got != 1 {
		t.Fatalf("after a refused update case_insensitive = %d, want 1", got)
	}
}

// Explicit answers apply to every handle whatever the filesystem does;
// auto reports what it does. case_preserving is always true.
func TestPathconfCaseInsensitive(t *testing.T) {
	cases := []struct {
		name string
		fold bool
		mode CaseInsensitivity
		want uint32
	}{
		{"memfs auto", false, CaseInsensitiveAuto, 0},
		{"memfs true", false, CaseInsensitiveTrue, 1},
		{"memfs false", false, CaseInsensitiveFalse, 0},
		{"folding auto", true, CaseInsensitiveAuto, 1},
		{"folding true", true, CaseInsensitiveTrue, 1},
		{"folding false", true, CaseInsensitiveFalse, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var fs absfs.SymlinkFileSystem = newMemFS(t)
			if tc.fold {
				fs = newFoldFS(t)
			}
			writeFile(t, fs, "/README.md", 3)
			mustMkdir(t, fs, "/sub")
			writeFile(t, fs, "/sub/Main.go", 3)
			nfs := newCaseNFS(t, fs, ExportOptions{CaseInsensitive: tc.mode})
			for _, p := range []string{"/", "/README.md", "/sub", "/sub/Main.go"} {
				ci, cp := pathconfCase(t, nfs, p)
				if ci != tc.want || cp != 1 {
					t.Errorf("%s: case_insensitive %d, case_preserving %d; want %d, 1", p, ci, cp, tc.want)
				}
			}
		})
	}
}

// FSINFO reports FSF3_HOMOGENEOUS only when PATHCONF's answer is the same
// for every object.
func TestFsinfoHomogeneous(t *testing.T) {
	const homogeneous = 0x0008
	for mode, want := range map[CaseInsensitivity]bool{
		CaseInsensitiveAuto:  false,
		CaseInsensitiveTrue:  true,
		CaseInsensitiveFalse: true,
	} {
		nfs := newCaseNFS(t, newMemFS(t), ExportOptions{CaseInsensitive: mode})
		props := fsinfoProperties(t, nfs)
		if got := props&homogeneous != 0; got != want {
			t.Errorf("%v: FSF3_HOMOGENEOUS %v, want %v", mode, got, want)
		}
		if props&^homogeneous != 0x0002|0x0010 {
			t.Errorf("%v: properties %#x, want symlink and cansettime", mode, props)
		}
	}
}

// Auto answers per handle: a case-sensitive memfs root listing routes, one
// to a folding filesystem and one to a case-sensitive one, as c4d serves
// workspaces. A directory with no name to probe takes its nearest
// ancestor's answer.
func TestCaseAutoPerHandle(t *testing.T) {
	fold, plain := newFoldFS(t), newMemFS(t)
	for _, fs := range []absfs.SymlinkFileSystem{fold, plain} {
		writeFile(t, fs, "/README.md", 3)
		mustMkdir(t, fs, "/src/pkg")
		writeFile(t, fs, "/src/pkg/a.go", 3)
		mustMkdir(t, fs, "/empty")
		mustMkdir(t, fs, "/digits/123")
		writeFile(t, fs, "/digits/123/456", 3)
	}
	sw := newSwitchFS(t, map[string]absfs.FileSystem{"/w1": fold, "/w2": plain})
	nfs := newCaseNFS(t, sw.fs, ExportOptions{})
	want := map[string]uint32{
		"/":                  0,
		"/w1":                1,
		"/w1/README.md":      1,
		"/w1/src/pkg":        1,
		"/w1/src/pkg/a.go":   1,
		"/w1/empty":          1,
		"/w1/digits/123":     1,
		"/w1/digits/123/456": 1,
		"/w2":                0,
		"/w2/README.md":      0,
		"/w2/empty":          0,
		"/w2/digits/123":     0,
	}
	for p, w := range want {
		if got, _ := pathconfCase(t, nfs, p); got != w {
			t.Errorf("%s: case_insensitive %d, want %d", p, got, w)
		}
	}
}

// A directory with no name to probe anywhere up to the root is reported
// case-sensitive.
func TestCaseAutoNothingToProbe(t *testing.T) {
	fs := newFoldFS(t)
	mustMkdir(t, fs, "/1/2")
	nfs := newCaseNFS(t, fs, ExportOptions{})
	for _, p := range []string{"/", "/1", "/1/2"} {
		if got, _ := pathconfCase(t, nfs, p); got != 0 {
			t.Errorf("%s: case_insensitive %d, want 0", p, got)
		}
	}
}

// Two entries whose names differ only in case show that names do not fold,
// without looking either up.
func TestCaseAutoBothSpellingsListed(t *testing.T) {
	fs := newMemFS(t)
	writeFile(t, fs, "/a", 1)
	writeFile(t, fs, "/A", 2)
	nfs := newCaseNFS(t, fs, ExportOptions{})
	if folds, ok := nfs.probeCase("/"); folds || !ok {
		t.Fatalf("probeCase = %v, %v; want false, true", folds, ok)
	}
}

// A probed answer is cached for AttrCacheTimeout and dropped when the
// server moves or removes the directory.
func TestCaseAutoCache(t *testing.T) {
	fold := newFoldFS(t)
	writeFile(t, fold, "/README.md", 3)
	mustMkdir(t, fold, "/d/e")
	writeFile(t, fold, "/d/e/File", 3)
	sw := newSwitchFS(t, map[string]absfs.FileSystem{"/w1": fold})
	nfs := newCaseNFS(t, sw.fs, ExportOptions{AttrCacheTimeout: time.Hour})

	if got, _ := pathconfCase(t, nfs, "/w1"); got != 1 {
		t.Fatalf("/w1: case_insensitive %d, want 1", got)
	}
	opens := fold.opens.Load()
	if got, _ := pathconfCase(t, nfs, "/w1/README.md"); got != 1 || fold.opens.Load() != opens {
		t.Fatalf("second PATHCONF in /w1: %d, %d more opens; want 1 from the cache", got, fold.opens.Load()-opens)
	}

	// The route now reaches a case-sensitive filesystem: the cached answer
	// stands until it expires (an hour) or the server changes /w1.
	plain := newMemFS(t)
	writeFile(t, plain, "/README.md", 3)
	sw.swap(t, "/w1", plain)
	if got, _ := pathconfCase(t, nfs, "/w1"); got != 1 {
		t.Fatalf("/w1 after the swap, cached: %d, want 1", got)
	}
	nfs.invalidate("/w1")
	if got, _ := pathconfCase(t, nfs, "/w1"); got != 0 {
		t.Fatalf("/w1 after the server changed it: %d, want 0", got)
	}

	// Renaming a directory drops the answers below it.
	sw.swap(t, "/w1", fold)
	for _, p := range []string{"/w1/d", "/w1/d/e"} {
		pathconfCase(t, nfs, p)
	}
	if _, ok := nfs.caseCache.get("/w1/d/e", time.Now()); !ok {
		t.Fatal("/w1/d/e not cached")
	}
	root, _ := nfs.Lookup("/w1")
	if err := nfs.Rename(root, "d", root, "x"); err != nil {
		t.Fatal(err)
	}
	if _, ok := nfs.caseCache.get("/w1/d/e", time.Now()); ok {
		t.Error("/w1/d/e still cached after /w1/d was renamed")
	}
	if got, _ := pathconfCase(t, nfs, "/w1/x/e"); got != 1 {
		t.Errorf("/w1/x/e: %d, want 1", got)
	}
}

func TestCaseAutoCacheExpires(t *testing.T) {
	fold := newFoldFS(t)
	writeFile(t, fold, "/README.md", 3)
	sw := newSwitchFS(t, map[string]absfs.FileSystem{"/w1": fold})
	nfs := newCaseNFS(t, sw.fs, ExportOptions{AttrCacheTimeout: 20 * time.Millisecond})
	if got, _ := pathconfCase(t, nfs, "/w1"); got != 1 {
		t.Fatalf("/w1: %d, want 1", got)
	}
	plain := newMemFS(t)
	writeFile(t, plain, "/README.md", 3)
	sw.swap(t, "/w1", plain)
	time.Sleep(40 * time.Millisecond)
	if got, _ := pathconfCase(t, nfs, "/w1"); got != 0 {
		t.Fatalf("/w1 after the answer expired: %d, want 0", got)
	}
}

func TestCaseCacheBounded(t *testing.T) {
	var c caseCache
	exp := time.Now().Add(time.Hour)
	for i := 0; i < caseCacheMax+10; i++ {
		c.put(fmt.Sprintf("/d%d", i), true, exp)
	}
	if n := len(c.m); n > caseCacheMax {
		t.Fatalf("cache holds %d answers, cap %d", n, caseCacheMax)
	}
}

// sameFile sees through FileInfo wrappers to the file's identity.
func TestSameFile(t *testing.T) {
	mem := newMemFS(t)
	writeFile(t, mem, "/a", 1)
	writeFile(t, mem, "/b", 1)
	a1, _ := mem.Lstat("/a")
	a2, _ := mem.Lstat("/a")
	b, _ := mem.Lstat("/b")
	if same, known := sameFile(a1, a2); !same || !known {
		t.Errorf("memfs a, a: %v, %v", same, known)
	}
	if same, known := sameFile(a1, b); same || !known {
		t.Errorf("memfs a, b: %v, %v", same, known)
	}
	if same, known := sameFile(nameInfo{a1}, nameInfo{a2}); known || same {
		t.Errorf("infos with no identity: %v, %v; want unknown", same, known)
	}
	if !sameAttrs(a1, a2) {
		t.Error("sameAttrs(a, a) = false")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "h"), []byte("h"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "i"), []byte("i"), 0o644); err != nil {
		t.Fatal(err)
	}
	ofs, err := osfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	host, err := basefs.NewFS(ofs, filepath.ToSlash(dir))
	if err != nil {
		t.Skipf("basefs over %s: %v", dir, err)
	}
	h1, _ := host.Lstat("/h")
	h2, _ := host.Lstat("/h")
	i, _ := host.Lstat("/i")
	same, known := sameFile(h1, h2)
	if !same {
		t.Errorf("host h, h through basefs: same %v (known %v)", same, known)
	}
	if same, _ := sameFile(h1, i); same {
		t.Error("host h, i through basefs: same")
	}
	if runtime.GOOS != "windows" && !known {
		t.Error("host identity unknown through basefs")
	}
}

// nameInfo hides the FileInfo's identity.
type nameInfo struct{ os.FileInfo }

func (nameInfo) Sys() any { return nil }

// A case-only rename of a non-empty directory on a host that folds names
// renames it: the target is the source itself, not a non-empty directory.
func TestRenameCaseOnlyDirectory(t *testing.T) {
	fs := newFoldFS(t)
	mustMkdir(t, fs, "/Dir")
	writeFile(t, fs, "/Dir/x", 3)
	nfs := newCaseNFS(t, fs, ExportOptions{})
	root, _ := nfs.Lookup("/")
	if err := nfs.Rename(root, "Dir", root, "dir"); err != nil {
		t.Fatalf("rename Dir dir: %v", err)
	}
	f, err := fs.SymlinkFileSystem.Open("/")
	if err != nil {
		t.Fatal(err)
	}
	names, _ := f.Readdirnames(-1)
	f.Close()
	if len(names) != 1 || names[0] != "dir" {
		t.Fatalf("root lists %v, want [dir]", names)
	}
}

// A case-only rename in a union of host directories on a volume that folds
// names (macOS, Windows) renames the file whether it is still in the lower
// layer or was copied up first: the writable layer then holds Readme.md
// and a whiteout of README.md.
func TestRenameCaseOnlyUnion(t *testing.T) {
	scratch := t.TempDir()
	if scratch, _ = filepath.EvalSymlinks(scratch); scratch == "" {
		t.Skip("cannot resolve the temporary directory")
	}
	probe := filepath.Join(scratch, "Probe")
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(scratch, "pROBE")); err != nil {
		t.Skipf("%s does not fold case", scratch)
	}
	os.Remove(probe)
	ofs, err := osfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	layer := func(dir string) absfs.SymlinkFileSystem {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		fs, err := basefs.NewFS(ofs, filepath.ToSlash(dir))
		if err != nil {
			t.Skipf("basefs over %s: %v", dir, err)
		}
		return fs
	}
	for _, copyUp := range []bool{false, true} {
		t.Run(fmt.Sprintf("copied up %v", copyUp), func(t *testing.T) {
			base := filepath.Join(scratch, fmt.Sprint(copyUp))
			lower := layer(filepath.Join(base, "lower"))
			writeFile(t, lower, "/README.md", 7)
			upperDir := filepath.Join(base, "upper")
			upper := layer(upperDir)
			u := unionfs.New(unionfs.WithWritableLayer(upper), unionfs.WithReadOnlyLayer(lower)).SymlinkFileSystem()
			size := int64(7)
			if copyUp {
				f, err := u.OpenFile("/README.md", os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.Write([]byte("x\n")); err != nil {
					t.Fatal(err)
				}
				f.Close()
				size += 2
			}
			nfs := newCaseNFS(t, u, ExportOptions{AllowAnySymlinkTarget: true})
			root, _ := nfs.Lookup("/")
			if err := nfs.Rename(root, "README.md", root, "Readme.md"); err != nil {
				t.Fatalf("rename README.md Readme.md: %v", err)
			}
			ents, err := os.ReadDir(upperDir)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, e := range ents {
				name := e.Name()
				if target, ok := unionfs.WhiteoutTarget("/" + name); ok {
					name = "D " + target
				} else {
					name = "W /" + name
				}
				got = append(got, name)
			}
			slices.Sort(got)
			if !slices.Equal(got, []string{"D /README.md", "W /Readme.md"}) {
				t.Fatalf("upper records %v, want [D /README.md W /Readme.md]", got)
			}
			info, err := u.Stat("/Readme.md")
			if err != nil || info.Size() != size {
				t.Fatalf("stat /Readme.md: %v, %v; want %d bytes", info, err, size)
			}
		})
	}
}
