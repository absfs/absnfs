package absnfs

import (
	"bytes"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/absfs/absfs"
	"github.com/absfs/basefs"
	"github.com/absfs/inode"
	"github.com/absfs/memfs"
	"github.com/absfs/osfs"
	"github.com/absfs/unionfs"
)

// foldName gives names APFS takes for one name one key, and names it keeps
// apart different keys. Each pair was checked against a case-insensitive
// APFS volume.
func TestFoldName(t *testing.T) {
	same := [][2]string{
		{"README.md", "readme.md"},
		{"Docs", "docs"},
		{"café", "CAFÉ"},     // composed and decomposed, upper and lower
		{"café", "café"},     // composed and decomposed
		{"straße", "STRASSE"}, // ß folds to ss
		{"straße", "STRAẞE"},  // capital sharp s
		{"k", "K"},            // Kelvin sign
		{"s", "ſ"},            // long s
		{"σ", "ς"},            // final sigma
		{"σ", "Σ"},
		{"ǆ", "ǅ"},  // dž, Dž
		{"ﬀ", "ff"}, // ligature
		{"ǰ", "J̌"},
		{"µ", "μ"}, // micro sign, mu
		{"ａ", "Ａ"}, // fullwidth a, A
	}
	for _, p := range same {
		if a, b := foldName(p[0]), foldName(p[1]); a != b {
			t.Errorf("foldName(%q) = %q, foldName(%q) = %q; want equal", p[0], a, p[1], b)
		}
	}
	differ := [][2]string{
		{"i", "İ"}, // dotted capital I
		{"ı", "I"}, // dotless i
		{"a", "b"},
		{"README.md", "README.txt"},
	}
	for _, p := range differ {
		if a, b := foldName(p[0]), foldName(p[1]); a == b {
			t.Errorf("foldName(%q) = foldName(%q) = %q; want different", p[0], p[1], a)
		}
	}
	for _, s := range []string{"readme.md", "", "a/b", "src"} {
		if got := foldName(s); got != s {
			t.Errorf("foldName(%q) = %q, want it unchanged", s, got)
		}
	}
	for _, s := range []string{"Readme.md", "café", "K"} {
		if k := foldName(s); foldName(k) != k {
			t.Errorf("foldName(%q) = %q is not its own key", s, k)
		}
	}
}

// fileIDs of exports whose names do not fold are the hash of the path as
// spelled, byte for byte as before names were folded (the values are
// FNV-1a 64 of the path, as absnfs has always reported them): on an export answering
// case-sensitive whatever its backend does, and on one probing a backend
// that keeps /Docs and /docs apart.
func TestFileIDCaseSensitiveUnchanged(t *testing.T) {
	golden := map[string]uint64{
		"/":               0xaf63a24c860189fe,
		"/Docs":           0xde30ea289e58aa39,
		"/docs":           0xb3a2e91c9ce83659,
		"/Docs/README.md": 0xf53f03d18a2977f3,
		"/docs/café":      0x3dc99b7c7b558b40,
		"/docs/café":     0xa02317876fe3bb68,
	}
	for p, want := range golden {
		if got := fileID(p); got != want {
			t.Errorf("fileID(%q) = %#x, want %#x", p, got, want)
		}
	}
	check := func(t *testing.T, nfs *AbsfsNFS) {
		t.Helper()
		for p := range golden {
			node, err := nfs.Lookup(p)
			if err != nil {
				t.Fatalf("lookup %s: %v", p, err)
			}
			if got := node.attrs.FileId; got != golden[p] {
				t.Errorf("fileid of %s = %#x, want %#x", p, got, golden[p])
			}
		}
	}
	t.Run("export answers case-sensitive over a folding backend", func(t *testing.T) {
		fs := newFoldFS(t)
		mustMkdir(t, fs, "/docs")
		writeFile(t, fs, "/docs/README.md", 3)
		writeFile(t, fs, "/docs/café", 3)
		check(t, newCaseNFS(t, fs, ExportOptions{CaseInsensitive: CaseInsensitiveFalse}))
	})
	t.Run("auto over a case-sensitive backend", func(t *testing.T) {
		fs := newMemFS(t)
		mustMkdir(t, fs, "/Docs")
		mustMkdir(t, fs, "/docs")
		writeFile(t, fs, "/Docs/README.md", 3)
		writeFile(t, fs, "/docs/café", 3)
		writeFile(t, fs, "/docs/café", 4)
		check(t, newCaseNFS(t, fs, ExportOptions{}))
	})
}

// spellingCase is a set of spellings of one object, the first as stored.
type spellingCase []string

// checkSpellings asserts that every spelling of each object in objs reports
// one fileid through LOOKUP, the same on a second lookup, that different
// objects report different ones, and that READDIRPLUS of each object's
// directory (looked up by another spelling) reports that fileid for its
// entry. It returns the fileid of each object, by its stored spelling.
func checkSpellings(t *testing.T, nfs *AbsfsNFS, objs []spellingCase) map[string]uint64 {
	t.Helper()
	ids := make(map[string]uint64)
	owner := make(map[uint64]string)
	for _, spellings := range objs {
		var want uint64
		for pass := 0; pass < 2; pass++ {
			for _, p := range spellings {
				node, err := nfs.Lookup(p)
				if err != nil {
					t.Fatalf("lookup %s: %v", p, err)
				}
				got := node.attrs.FileId
				if want == 0 {
					want = got
				} else if got != want {
					t.Errorf("fileid of %s = %#x, of %s = %#x; want one fileid", p, got, spellings[0], want)
				}
			}
		}
		if o, dup := owner[want]; dup {
			t.Errorf("%s and %s report one fileid %#x", o, spellings[0], want)
		}
		owner[want] = spellings[0]
		ids[spellings[0]] = want
	}
	h := &NFSProcedureHandler{server: &Server{handler: nfs}}
	for _, spellings := range objs {
		stored := spellings[0]
		if stored == "/" {
			continue
		}
		// List the directory by its last spelling, which is not the stored
		// one where there are several.
		dirSpelling := path.Dir(path.Clean(spellings[len(spellings)-1]))
		dir, err := nfs.Lookup(dirSpelling)
		if err != nil {
			t.Fatalf("lookup %s: %v", dirSpelling, err)
		}
		r, _ := h.handleReaddirplus(bytes.NewReader(buildReaddirplusRequest(nfs.fileMap.issue(dir), 0, 8192, 65536)), &RPCReply{}, &AuthContext{})
		f := newFaReader(t, r)
		if st := f.status(); st != NFS_OK {
			t.Fatalf("READDIRPLUS %s: status %d", dirSpelling, st)
		}
		f.postOpAttr()
		f.skip(8)
		found := false
		for _, en := range f.entries(true) {
			if en.Name != path.Base(stored) {
				continue
			}
			found = true
			if en.FileId != ids[stored] || en.Attr.FileId != ids[stored] {
				t.Errorf("READDIRPLUS %s: %s has fileid %#x (attrs %#x), LOOKUP %v reports %#x",
					dirSpelling, en.Name, en.FileId, en.Attr.FileId, spellings, ids[stored])
			}
		}
		if !found {
			t.Errorf("READDIRPLUS %s does not list %s", dirSpelling, path.Base(stored))
		}
	}
	return ids
}

// On an export whose names fold, every spelling of an object reports one
// fileid, and READDIRPLUS entries (stored spellings) report it too.
func TestFileIDFoldsSpellings(t *testing.T) {
	objs := []spellingCase{
		{"/", "/."},
		{"/docs", "/Docs", "/DOCS", "/docs/"},
		{"/docs/README.md", "/Docs/readme.md", "/DOCS/Readme.MD", "/docs/README.md"},
		{"/docs/café", "/docs/café", "/Docs/CAFÉ", "/DOCS/CAFÉ"},
		{"/docs/straße", "/Docs/STRASSE"},
		{"/docs/Sub", "/docs/sub", "/DOCS/SUB"},
		{"/docs/Sub/x.go", "/Docs/sub/X.GO"},
	}
	for _, mode := range []CaseInsensitivity{CaseInsensitiveAuto, CaseInsensitiveTrue} {
		t.Run(mode.String(), func(t *testing.T) {
			fs := newFoldFS(t)
			mustMkdir(t, fs, "/docs")
			mustMkdir(t, fs, "/docs/Sub")
			writeFile(t, fs, "/docs/README.md", 1)
			writeFile(t, fs, "/docs/café", 2)
			writeFile(t, fs, "/docs/straße", 3)
			writeFile(t, fs, "/docs/Sub/x.go", 4)
			nfs := newCaseNFS(t, fs, ExportOptions{CaseInsensitive: mode})
			ids := checkSpellings(t, nfs, objs)
			// A path that is its own key reports the hash of its spelling,
			// as on a case-sensitive export.
			if ids["/docs"] != fileID("/docs") {
				t.Errorf("fileid of /docs = %#x, want %#x", ids["/docs"], fileID("/docs"))
			}
		})
	}
}

// With auto, only names in a directory that folds are folded: a
// case-sensitive directory keeps /A and /a apart, also when the
// directories below it fold.
func TestFileIDFoldsPerDirectory(t *testing.T) {
	folding := newFoldFS(t)
	writeFile(t, folding, "/Notes.txt", 3)
	sw := newSwitchFS(t, map[string]absfs.FileSystem{"/w": folding})
	mustMkdir(t, sw.fs, "/A")
	mustMkdir(t, sw.fs, "/a")
	writeFile(t, sw.fs, "/A/x", 1)
	writeFile(t, sw.fs, "/a/x", 2)
	nfs := newCaseNFS(t, sw.fs, ExportOptions{})
	ids := checkSpellings(t, nfs, []spellingCase{
		{"/w/Notes.txt", "/w/notes.txt", "/w/NOTES.TXT"},
		{"/A"},
		{"/a"},
		{"/A/x"},
		{"/a/x"},
		{"/w"},
	})
	if ids["/A"] != fileID("/A") {
		t.Errorf("fileid of /A in the case-sensitive root = %#x, want %#x", ids["/A"], fileID("/A"))
	}
	if _, err := nfs.Lookup("/W"); err == nil {
		t.Error("lookup /W in the case-sensitive root found it")
	}
}

// foldingTempDir returns a new directory on a volume that folds case, or
// skips the test.
func foldingTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	probe := filepath.Join(dir, "Probe")
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(probe)
	if _, err := os.Lstat(filepath.Join(dir, "pROBE")); err != nil {
		t.Skipf("%s does not fold case", dir)
	}
	return dir
}

// hostLayer is basefs over osfs rooted at a new directory dir.
func hostLayer(t *testing.T, dir string) absfs.SymlinkFileSystem {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ofs, err := osfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	fs, err := basefs.NewFS(ofs, filepath.ToSlash(dir))
	if err != nil {
		t.Skipf("basefs over %s: %v", dir, err)
	}
	t.Cleanup(func() { fs.Close() })
	return fs
}

// Over a host directory on a volume that folds case (APFS, NTFS), directly
// and through a union, every spelling reports one fileid with auto. The
// host's name for an object, as Lstat reports it, is the caller's spelling
// (os.Lstat names the info after its argument, and basefs and unionfs
// pass that on), so the fileid cannot come from it.
func TestFileIDFoldsOnHost(t *testing.T) {
	scratch := foldingTempDir(t)
	objs := []spellingCase{
		{"/docs", "/Docs", "/DOCS"},
		{"/docs/README.md", "/Docs/readme.md", "/DOCS/Readme.MD"},
	}
	// Volumes that also match names composed and decomposed (APFS).
	nfd := filepath.Join(scratch, "café")
	if err := os.WriteFile(nfd, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(scratch, "CAFÉ")); err == nil {
		objs = append(objs, spellingCase{"/docs/café", "/Docs/café", "/DOCS/CAFÉ"})
	}
	os.Remove(nfd)
	seed := func(t *testing.T, fs absfs.SymlinkFileSystem) {
		mustMkdir(t, fs, "/docs")
		writeFile(t, fs, "/docs/README.md", 7)
		writeFile(t, fs, "/docs/café", 3)
	}
	t.Run("basefs", func(t *testing.T) {
		fs := hostLayer(t, filepath.Join(scratch, "host"))
		seed(t, fs)
		info, err := fs.Lstat("/Docs")
		if err != nil {
			t.Fatal(err)
		}
		if info.Name() != "Docs" {
			t.Logf("Lstat(/Docs).Name() = %q", info.Name())
		}
		checkSpellings(t, newCaseNFS(t, fs, ExportOptions{}), objs)
	})
	t.Run("unionfs", func(t *testing.T) {
		lower := hostLayer(t, filepath.Join(scratch, "lower"))
		seed(t, lower)
		upper := hostLayer(t, filepath.Join(scratch, "upper"))
		u := unionfs.New(unionfs.WithWritableLayer(upper), unionfs.WithReadOnlyLayer(lower)).SymlinkFileSystem()
		checkSpellings(t, newCaseNFS(t, u, ExportOptions{AllowAnySymlinkTarget: true}), objs)
	})
}

// A union reports a file a metadata marker changes with a fresh merged
// *inode.Inode on every Lstat; sameFile still takes two Lstats of it for
// one file (absnfs#88).
func TestSameFileMetaMarked(t *testing.T) {
	lower, upper := newMemFS(t), newMemFS(t)
	writeFile(t, lower, "/f", 3)
	writeFile(t, lower, "/g", 3)
	u := unionfs.New(unionfs.WithWritableLayer(upper), unionfs.WithReadOnlyLayer(lower)).SymlinkFileSystem()
	if err := u.Chmod("/f", 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := u.Lstat("/f")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := u.Lstat("/f")
	g, _ := u.Lstat("/g")
	x, _ := a.Sys().(*inode.Inode)
	y, _ := b.Sys().(*inode.Inode)
	if x == nil || x == y {
		t.Fatalf("Sys of two Lstats of a marked file: %p, %p; the test needs two fresh inodes", x, y)
	}
	if a.Mode().Perm() != 0o600 {
		t.Fatalf("marked file mode %v, want 0600", a.Mode())
	}
	if same, known := sameFile(a, b); !same || !known {
		t.Errorf("sameFile(f, f) = %v, %v; want true, true", same, known)
	}
	if same, known := sameFile(a, g); same || !known {
		t.Errorf("sameFile(f, g) = %v, %v; want false, true", same, known)
	}
}

// BenchmarkObjectID measures what folding adds to a fileid on an export
// probing a folding backend: nothing for a lowercase ASCII path, a cached
// lookup per name otherwise.
func BenchmarkObjectID(b *testing.B) {
	fs := &foldFS{SymlinkFileSystem: newMemFSB(b)}
	for _, d := range []string{"/src", "/src/Pkg", "/src/Pkg/Internal"} {
		if err := fs.Mkdir(d, 0o755); err != nil {
			b.Fatal(err)
		}
	}
	for _, f := range []string{"/src/Pkg/Internal/Main.go", "/src/Pkg/Internal/caf\u00e9.go"} {
		file, err := fs.Create(f)
		if err != nil {
			b.Fatal(err)
		}
		file.Close()
	}
	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { nfs.Close() })
	for name, p := range map[string]string{
		"lowercase": "/src/pkg/internal/main.go",
		"mixed":     "/src/Pkg/Internal/Main.go",
		"unicode":   "/src/Pkg/Internal/café.go",
	} {
		b.Run(name, func(b *testing.B) {
			nfs.objectID(p)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				nfs.objectID(p)
			}
		})
	}
	b.Run("unfolded", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			fileID("/src/Pkg/Internal/Main.go")
		}
	})
}

func newMemFSB(b *testing.B) absfs.SymlinkFileSystem {
	fs, err := memfs.NewFS()
	if err != nil {
		b.Fatal(err)
	}
	return fs
}
