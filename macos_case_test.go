//go:build darwin

package absnfs

// macOS end-to-end test of case-insensitive names: a case-only rename
// (mv README.md Readme.md) over the mount, and what pathconf reports as
// _PC_CASE_SENSITIVE, for exports of host directories on a volume that
// folds case, of unions over them, and of a memfs root listing switchfs
// routes to such unions (as c4d serves workspaces).
//
// Like TestMacOSUserMount it is skipped unless ABSNFS_MACOS_MOUNT=1:
//
//	ABSNFS_MACOS_MOUNT=1 go test -run TestMacOSCaseOnlyRename -v

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/absfs/absfs"
	"github.com/absfs/basefs"
	"github.com/absfs/osfs"
	"github.com/absfs/unionfs"
)

// pcCaseSensitive is _PC_CASE_SENSITIVE in <sys/unistd.h>.
const pcCaseSensitive = 11

// macOSCaseSensitive returns pathconf(p, _PC_CASE_SENSITIVE): 1 when names
// in p's filesystem are case-sensitive, 0 when they fold.
func macOSCaseSensitive(t *testing.T, p string) int {
	t.Helper()
	b, err := syscall.BytePtrFromString(p)
	if err != nil {
		t.Fatal(err)
	}
	r, _, errno := syscall.Syscall(syscall.SYS_PATHCONF, uintptr(unsafe.Pointer(b)), pcCaseSensitive, 0)
	if errno != 0 {
		t.Fatalf("pathconf(%s, _PC_CASE_SENSITIVE): %v", p, errno)
	}
	return int(r)
}

// hostDirFS is basefs over osfs rooted at a new directory dir, confined to
// it by os.Root where the build supports that (see checkConfined).
func hostDirFS(t *testing.T, dir string) absfs.SymlinkFileSystem {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ofs, err := osfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	fs, err := basefs.NewFS(ofs, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close() })
	checkConfined(t, fs)
	return fs
}

// appendMount appends a line to the file p on the mount, as an edit does,
// which copies a file still in a union's lower layer up to the writable one.
func appendMount(t *testing.T, p string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("x\n")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// hostUnion is a union of a writable host directory over a read-only one
// holding README.md, src/main.go and docs/guide.md. It returns the
// writable directory.
func hostUnion(t *testing.T, scratch, name string) (absfs.SymlinkFileSystem, string) {
	t.Helper()
	lower := hostDirFS(t, filepath.Join(scratch, name+"-lower"))
	writeFile(t, lower, "/README.md", 7)
	mustMkdir(t, lower, "/src")
	writeFile(t, lower, "/src/main.go", 3)
	mustMkdir(t, lower, "/docs")
	writeFile(t, lower, "/docs/guide.md", 5)
	upperDir := filepath.Join(scratch, name+"-upper")
	upper := hostDirFS(t, upperDir)
	return unionfs.New(unionfs.WithWritableLayer(upper), unionfs.WithReadOnlyLayer(lower)).SymlinkFileSystem(), upperDir
}

// upperChangeset lists what a union's writable directory records: "W p"
// for an entry written, "D p" for a whiteout, "O p" for a directory made
// opaque (its lower contents hidden).
func upperChangeset(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == dir {
			return err
		}
		rel := "/" + filepath.ToSlash(p[len(dir)+1:])
		if path.Base(rel) == unionfs.OpaqueWhiteout {
			out = append(out, "O "+path.Dir(rel))
		} else if target, ok := unionfs.WhiteoutTarget(rel); ok {
			out = append(out, "D "+target)
		} else {
			out = append(out, "W "+rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func TestMacOSCaseOnlyRename(t *testing.T) {
	if os.Getenv(macOSMountEnv) != "1" {
		t.Skipf("set %s=1 on macOS to run the mount test", macOSMountEnv)
	}
	if os.Geteuid() == 0 {
		t.Skip("the mount test must run as a normal user, not root")
	}
	scratch, err := os.MkdirTemp("", "absnfs-case-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(scratch) })
	if scratch, err = filepath.EvalSymlinks(scratch); err != nil {
		t.Fatal(err)
	}
	if macOSCaseSensitive(t, scratch) != 0 {
		t.Skipf("%s is on a case-sensitive volume; the test needs one that folds case", scratch)
	}

	// mvCase renames README.md to Readme.md in dir on the mount, as a user
	// does, and checks the mount lists the new spelling.
	mvCase := func(t *testing.T, dir string) {
		t.Helper()
		if out, err := runWithTimeout(20*time.Second, dir, nil, "/bin/mv", "README.md", "Readme.md"); err != nil {
			t.Fatalf("mv README.md Readme.md: %v\n%s", err, out)
		}
		if got := readDirNames(t, dir); !slices.Contains(got, "Readme.md") || slices.Contains(got, "README.md") {
			t.Fatalf("%s lists %v after mv, want Readme.md and no README.md", dir, got)
		}
	}
	wantCase := func(t *testing.T, mnt string, want map[string]int) {
		t.Helper()
		for rel, w := range want {
			if got := macOSCaseSensitive(t, filepath.Join(mnt, rel)); got != w {
				t.Errorf("pathconf(%s, _PC_CASE_SENSITIVE) = %d, want %d", rel, got, w)
			}
		}
	}

	t.Run("host directory, auto", func(t *testing.T) {
		dir := filepath.Join(scratch, "host")
		fs := hostDirFS(t, dir)
		writeFile(t, fs, "/README.md", 7)
		mustMkdir(t, fs, "/src")
		_, mnt := mountMacOS(t, fs, ExportOptions{}, "")
		wantCase(t, mnt, map[string]int{".": 0, "src": 0, "README.md": 0})
		mvCase(t, mnt)
		if got := readDirNames(t, dir); !equalStrings(got, []string{"Readme.md", "src"}) {
			t.Fatalf("host directory lists %v", got)
		}
	})

	t.Run("union, auto", func(t *testing.T) {
		fs, upper := hostUnion(t, scratch, "union")
		_, mnt := mountMacOS(t, fs, ExportOptions{AllowAnySymlinkTarget: true}, "")
		wantCase(t, mnt, map[string]int{".": 0, "src": 0, "src/main.go": 0})
		mvCase(t, mnt)
		if got := upperChangeset(t, upper); !equalStrings(got, []string{"D /README.md", "W /Readme.md"}) {
			t.Fatalf("upper records %v, want [D /README.md W /Readme.md]", got)
		}
	})

	// README.md copied up to the writable layer first, by an append over
	// the mount, as an edit before the rename does.
	t.Run("union, copied up, auto", func(t *testing.T) {
		fs, upper := hostUnion(t, scratch, "copyup")
		_, mnt := mountMacOS(t, fs, ExportOptions{AllowAnySymlinkTarget: true}, "")
		appendMount(t, filepath.Join(mnt, "README.md"))
		if got := upperChangeset(t, upper); !equalStrings(got, []string{"W /README.md"}) {
			t.Fatalf("upper records %v after the append, want [W /README.md]", got)
		}
		mvCase(t, mnt)
		if got := upperChangeset(t, upper); !equalStrings(got, []string{"D /README.md", "W /Readme.md"}) {
			t.Fatalf("upper records %v, want [D /README.md W /Readme.md]", got)
		}
		if data, err := os.ReadFile(filepath.Join(mnt, "Readme.md")); err != nil || len(data) != 9 {
			t.Fatalf("read Readme.md: %d bytes, %v; want 9", len(data), err)
		}
	})

	// c4d's shape: a memfs root, case-sensitive, lists the workspaces,
	// each a switchfs route to a union over host directories. README.md is
	// renamed in w1 while still in the lower layer, then in w2 after an
	// edit copies it up.
	for _, mode := range []CaseInsensitivity{CaseInsensitiveTrue, CaseInsensitiveAuto} {
		t.Run("memfs root, union routes, "+mode.String(), func(t *testing.T) {
			w1, upper1 := hostUnion(t, scratch, "w1-"+mode.String())
			w2, upper2 := hostUnion(t, scratch, "w2-"+mode.String())
			sw := newSwitchFS(t, map[string]absfs.FileSystem{"/w1": w1, "/w2": w2})
			_, mnt := mountMacOS(t, sw.fs, ExportOptions{CaseInsensitive: mode, AllowAnySymlinkTarget: true}, "")
			// With auto the root answers for itself, and the client takes
			// that as the volume's answer; each workspace answers for
			// itself.
			root := 0
			if mode == CaseInsensitiveAuto {
				root = 1
			}
			wantCase(t, mnt, map[string]int{".": root, "w1": 0, "w1/src": 0, "w1/README.md": 0, "w2": 0})
			if got := readDirNames(t, mnt); !equalStrings(got, []string{"w1", "w2"}) {
				t.Fatalf("root lists %v, want [w1 w2]", got)
			}
			// The root still answers names exactly as spelled.
			if _, err := os.Lstat(filepath.Join(mnt, "W1")); !os.IsNotExist(err) {
				t.Fatalf("lstat W1 in the case-sensitive root: %v, want not found", err)
			}
			mvCase(t, filepath.Join(mnt, "w1"))
			if got := upperChangeset(t, upper1); !equalStrings(got, []string{"D /README.md", "W /Readme.md"}) {
				t.Fatalf("w1 upper records %v, want [D /README.md W /Readme.md]", got)
			}
			if got := upperChangeset(t, upper2); len(got) != 0 {
				t.Fatalf("w2 upper records %v, want nothing", got)
			}
			if data, err := os.ReadFile(filepath.Join(mnt, "w1", "Readme.md")); err != nil || len(data) != 7 {
				t.Fatalf("read w1/Readme.md: %d bytes, %v", len(data), err)
			}
			appendMount(t, filepath.Join(mnt, "w2", "README.md"))
			if got := upperChangeset(t, upper2); !equalStrings(got, []string{"W /README.md"}) {
				t.Fatalf("w2 upper records %v after the append, want [W /README.md]", got)
			}
			mvCase(t, filepath.Join(mnt, "w2"))
			if got := upperChangeset(t, upper2); !equalStrings(got, []string{"D /README.md", "W /Readme.md"}) {
				t.Fatalf("w2 upper records %v, want [D /README.md W /Readme.md]", got)
			}
			if data, err := os.ReadFile(filepath.Join(mnt, "w2", "Readme.md")); err != nil || len(data) != 9 {
				t.Fatalf("read w2/Readme.md: %d bytes, %v; want 9", len(data), err)
			}
		})
	}

	// mvDirCase renames the directory docs to Docs in dir on the mount, as
	// a user does. /bin/mv stats the target first: had Docs reported
	// another inode number than docs, mv would take it for an existing
	// directory and move docs into it ("rename docs to Docs/docs: Invalid
	// argument"). Both spellings report one inode, as on APFS itself.
	mvDirCase := func(t *testing.T, dir string) {
		t.Helper()
		lower, upper := macOSIno(t, filepath.Join(dir, "docs")), macOSIno(t, filepath.Join(dir, "Docs"))
		if lower != upper {
			t.Fatalf("inode of docs %d, of Docs %d; want one", lower, upper)
		}
		if listed := readDirInos(t, dir)["docs"]; listed != lower {
			t.Fatalf("readdir lists docs with inode %d, lstat reports %d", listed, lower)
		}
		if out, err := runWithTimeout(20*time.Second, dir, nil, "/bin/mv", "docs", "Docs"); err != nil {
			t.Fatalf("mv docs Docs: %v\n%s", err, out)
		}
		if got := readDirNames(t, dir); !slices.Contains(got, "Docs") || slices.Contains(got, "docs") {
			t.Fatalf("%s lists %v after mv, want Docs and no docs", dir, got)
		}
		if data, err := os.ReadFile(filepath.Join(dir, "Docs", "guide.md")); err != nil || len(data) != 5 {
			t.Fatalf("read Docs/guide.md: %d bytes, %v; want 5", len(data), err)
		}
		if a, b := macOSIno(t, filepath.Join(dir, "docs")), macOSIno(t, filepath.Join(dir, "Docs")); a != b {
			t.Fatalf("after mv: inode of docs %d, of Docs %d; want one", a, b)
		}
	}

	t.Run("directory, host directory, auto", func(t *testing.T) {
		dir := filepath.Join(scratch, "host-dir")
		fs := hostDirFS(t, dir)
		mustMkdir(t, fs, "/docs")
		writeFile(t, fs, "/docs/guide.md", 5)
		_, mnt := mountMacOS(t, fs, ExportOptions{}, "")
		mvDirCase(t, mnt)
		if got := readDirNames(t, dir); !equalStrings(got, []string{"Docs"}) {
			t.Fatalf("host directory lists %v, want [Docs]", got)
		}
	})

	t.Run("directory, union, auto", func(t *testing.T) {
		fs, upper := hostUnion(t, scratch, "union-dir")
		_, mnt := mountMacOS(t, fs, ExportOptions{AllowAnySymlinkTarget: true}, "")
		mvDirCase(t, mnt)
		if got := upperChangeset(t, upper); !equalStrings(got, wantDirRenameChangeset) {
			t.Fatalf("upper records %v, want %v", got, wantDirRenameChangeset)
		}
	})

	t.Run("directory, memfs root, union route, true", func(t *testing.T) {
		w, upper := hostUnion(t, scratch, "w-dir")
		sw := newSwitchFS(t, map[string]absfs.FileSystem{"/w": w})
		_, mnt := mountMacOS(t, sw.fs, ExportOptions{CaseInsensitive: CaseInsensitiveTrue, AllowAnySymlinkTarget: true}, "")
		mvDirCase(t, filepath.Join(mnt, "w"))
		if got := upperChangeset(t, upper); !equalStrings(got, wantDirRenameChangeset) {
			t.Fatalf("upper records %v, want %v", got, wantDirRenameChangeset)
		}
	})

	t.Run("memfs, auto", func(t *testing.T) {
		fs := newMemFS(t)
		writeFile(t, fs, "/README.md", 7)
		mustMkdir(t, fs, "/src")
		_, mnt := mountMacOS(t, fs, ExportOptions{}, "")
		wantCase(t, mnt, map[string]int{".": 1, "src": 1, "README.md": 1})
		mvCase(t, mnt)
	})
}

// wantDirRenameChangeset is what a union's writable directory records after
// docs, a directory in the lower layer, is renamed to Docs: a whiteout of
// docs and an opaque Docs holding a copy of what docs held.
var wantDirRenameChangeset = []string{"D /docs", "O /Docs", "W /Docs", "W /Docs/guide.md"}

// macOSIno returns the inode number lstat reports for p.
func macOSIno(t *testing.T, p string) uint64 {
	t.Helper()
	info, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return info.Sys().(*syscall.Stat_t).Ino
}

// readDirInos returns the inode number each entry of dir has in its
// directory entry (d_ino, which the client takes from READDIR), not what
// lstat of it reports.
func readDirInos(t *testing.T, dir string) map[string]uint64 {
	t.Helper()
	f, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := make(map[string]uint64)
	buf := make([]byte, 64<<10)
	for {
		n, err := syscall.ReadDirent(int(f.Fd()), buf)
		if err != nil {
			t.Fatal(err)
		}
		if n <= 0 {
			return out
		}
		for off := 0; off < n; {
			d := (*syscall.Dirent)(unsafe.Pointer(&buf[off]))
			if d.Reclen == 0 {
				break
			}
			name := make([]byte, d.Namlen)
			for i := range name {
				name[i] = byte(d.Name[i])
			}
			out[string(name)] = d.Ino
			off += int(d.Reclen)
		}
	}
}
