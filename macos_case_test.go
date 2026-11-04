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
// holding README.md and src/main.go. It returns the writable directory.
func hostUnion(t *testing.T, scratch, name string) (absfs.SymlinkFileSystem, string) {
	t.Helper()
	lower := hostDirFS(t, filepath.Join(scratch, name+"-lower"))
	writeFile(t, lower, "/README.md", 7)
	mustMkdir(t, lower, "/src")
	writeFile(t, lower, "/src/main.go", 3)
	upperDir := filepath.Join(scratch, name+"-upper")
	upper := hostDirFS(t, upperDir)
	return unionfs.New(unionfs.WithWritableLayer(upper), unionfs.WithReadOnlyLayer(lower)).SymlinkFileSystem(), upperDir
}

// upperChangeset lists what a union's writable directory records: "W p"
// for an entry written, "D p" for a whiteout.
func upperChangeset(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == dir {
			return err
		}
		rel := "/" + filepath.ToSlash(p[len(dir)+1:])
		if target, ok := unionfs.WhiteoutTarget(rel); ok {
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

	t.Run("memfs, auto", func(t *testing.T) {
		fs := newMemFS(t)
		writeFile(t, fs, "/README.md", 7)
		mustMkdir(t, fs, "/src")
		_, mnt := mountMacOS(t, fs, ExportOptions{}, "")
		wantCase(t, mnt, map[string]int{".": 1, "src": 1, "README.md": 1})
		mvCase(t, mnt)
	})
}
