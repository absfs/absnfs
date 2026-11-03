//go:build darwin

package absnfs

// macOS end-to-end test: serve a tree and mount it with the kernel NFS client
// as a normal user, the way a development workspace is mounted.
//
// It is skipped unless ABSNFS_MACOS_MOUNT=1 and the test runs on darwin, so CI
// and ordinary `go test` runs never mount anything. Run it with:
//
//	ABSNFS_MACOS_MOUNT=1 go test -run TestMacOSUserMount -v -timeout 10m
//
// Safety: it refuses to run as root, mounts only into a directory it creates
// under a fresh temporary directory, always uses a soft mount with short
// timeouts, and unmounts (umount, umount -f, diskutil unmount force) before
// stopping the server on every exit path.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/absfs/absfs"
	"github.com/absfs/basefs"
	"github.com/absfs/memfs"
	"github.com/absfs/osfs"
)

const macOSMountEnv = "ABSNFS_MACOS_MOUNT"

// macOSSeedEntries is the tree every backend starts with, at the export root.
var macOSSeedEntries = []string{"hello.txt", "link", "sub"}

const macOSSeedContent = "hello from absnfs\n"

func TestMacOSUserMount(t *testing.T) {
	if os.Getenv(macOSMountEnv) != "1" {
		t.Skipf("set %s=1 on macOS to run the mount test", macOSMountEnv)
	}
	if os.Geteuid() == 0 {
		t.Skip("the mount test must run as a normal user, not root")
	}

	cases := []struct {
		name    string
		backend string
		opts    string // extra mount options
		export  bool   // serve with AbsfsNFS.Export instead of NewServer
	}{
		{name: "osfs", backend: "osfs"},
		{name: "osfs-rdirplus", backend: "osfs", opts: "rdirplus"},
		{name: "memfs", backend: "memfs"},
		{name: "memfs-rdirplus", backend: "memfs", opts: "rdirplus"},
		{name: "memfs-export", backend: "memfs", export: true},
		{name: "osfs-locallocks", backend: "osfs", opts: "locallocks"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runMacOSMount(t, tc.backend, tc.opts, tc.export)
		})
	}
}

// newMacOSBackend builds the seeded filesystem to export.
func newMacOSBackend(t *testing.T, backend, scratch string) absfs.SymlinkFileSystem {
	t.Helper()
	var fs absfs.SymlinkFileSystem
	switch backend {
	case "osfs":
		root := filepath.Join(scratch, "export")
		if err := os.Mkdir(root, 0o755); err != nil {
			t.Fatal(err)
		}
		ofs, err := osfs.NewFS()
		if err != nil {
			t.Fatal(err)
		}
		bfs, err := basefs.NewFS(ofs, root)
		if err != nil {
			t.Fatal(err)
		}
		fs = bfs
	case "memfs":
		mfs, err := memfs.NewFS()
		if err != nil {
			t.Fatal(err)
		}
		fs = mfs
	default:
		t.Fatalf("unknown backend %q", backend)
	}

	f, err := fs.Create("/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte(macOSSeedContent)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fs.Mkdir("/sub", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fs.Symlink("hello.txt", "/link"); err != nil {
		t.Fatal(err)
	}
	return fs
}

func runMacOSMount(t *testing.T, backend, extraOpts string, viaExport bool) {
	scratch, err := os.MkdirTemp("", "absnfs-macos-")
	if err != nil {
		t.Fatal(err)
	}
	// mount(8) reports the resolved path (/private/var/...), so use it everywhere.
	if scratch, err = filepath.EvalSymlinks(scratch); err != nil {
		t.Fatal(err)
	}
	mnt := filepath.Join(scratch, "mnt")
	if err := os.Mkdir(mnt, 0o700); err != nil {
		t.Fatal(err)
	}

	fs := newMacOSBackend(t, backend, scratch)
	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var srv *Server
	var port int
	if viaExport {
		if err := nfs.Export("/", 0); err != nil {
			t.Fatal(err)
		}
		srv, port = nfs.exportServer, nfs.ExportPort()
	} else {
		if srv, err = NewServer(ServerOptions{Hostname: "127.0.0.1", UseRecordMarking: true}); err != nil {
			t.Fatal(err)
		}
		srv.SetHandler(nfs)
		if err := srv.Listen(); err != nil {
			t.Fatal(err)
		}
		port = srv.GetPort()
	}

	// Deferred in this order so the unmount below always runs before the
	// server stops: a server gone under a live mount hangs its clients.
	mounted := false
	defer func() {
		srv.Stop()
		nfs.Close()
		if mounted {
			t.Errorf("left %s mounted; not removing %s", mnt, scratch)
			return
		}
		os.RemoveAll(scratch)
	}()

	locking := "nolocks"
	if strings.Contains(extraOpts, "locallocks") {
		locking = ""
	}
	opts := fmt.Sprintf("vers=3,tcp,port=%d,mountport=%d,noresvport,soft,intr,timeo=10,retrans=2", port, port)
	for _, o := range []string{locking, extraOpts} {
		if o != "" {
			opts += "," + o
		}
	}
	// Export binds "localhost"; mount it by that name, as the README does.
	source := "127.0.0.1:/"
	if viaExport {
		source = "localhost:/"
	}
	out, err := runWithTimeout(30*time.Second, "", nil, "/sbin/mount_nfs", "-o", opts, source, mnt)
	if err != nil {
		if isMountedAt(mnt) {
			mounted = true
			if uerr := unmountMacOS(mnt); uerr == nil {
				mounted = false
			}
		}
		t.Fatalf("mount_nfs -o %s: %v\n%s", opts, err, out)
	}
	mounted = true
	defer func() {
		if err := unmountMacOS(mnt); err != nil {
			t.Errorf("unmount %s: %v", mnt, err)
			return
		}
		mounted = false
	}()
	if !isMountedAt(mnt) {
		t.Fatalf("mount_nfs succeeded but %s is not in mount(8) output", mnt)
	}
	t.Logf("mounted %s on %s with -o %s", source, mnt, opts)

	uid, gid := uint32(os.Getuid()), uint32(os.Getgid())

	t.Run("ls lists every entry", func(t *testing.T) {
		got := readDirNames(t, mnt)
		if !equalStrings(got, macOSSeedEntries) {
			t.Fatalf("os.ReadDir = %v, want %v", got, macOSSeedEntries)
		}
		out, err := runWithTimeout(20*time.Second, "", nil, "/bin/ls", "-1A", mnt)
		if err != nil {
			t.Fatalf("ls: %v\n%s", err, out)
		}
		lines := strings.Fields(string(out))
		sort.Strings(lines)
		if !equalStrings(lines, macOSSeedEntries) {
			t.Fatalf("ls -1A = %v, want %v", lines, macOSSeedEntries)
		}
	})

	t.Run("owned by the current user", func(t *testing.T) {
		for _, name := range append([]string{"."}, macOSSeedEntries...) {
			info, err := os.Lstat(filepath.Join(mnt, name))
			if err != nil {
				t.Fatal(err)
			}
			st := info.Sys().(*syscall.Stat_t)
			if st.Uid != uid || st.Gid != gid {
				t.Errorf("%s: owner %d:%d, want %d:%d", name, st.Uid, st.Gid, uid, gid)
			}
		}
	})

	t.Run("symlinks are symlinks", func(t *testing.T) {
		link := filepath.Join(mnt, "link")
		info, err := os.Lstat(link)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("link mode = %v, want a symlink", info.Mode())
		}
		target, err := os.Readlink(link)
		if err != nil || target != "hello.txt" {
			t.Fatalf("readlink = %q, %v; want hello.txt", target, err)
		}
		data, err := os.ReadFile(link)
		if err != nil || string(data) != macOSSeedContent {
			t.Fatalf("read through link = %q, %v", data, err)
		}

		// A symlink made through the mount (NFS SYMLINK) is one too.
		made := filepath.Join(mnt, "made-link")
		if err := os.Symlink("sub", made); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Lstat(made); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("made-link: %v, %v", info, err)
		}
		if target, err := os.Readlink(made); err != nil || target != "sub" {
			t.Fatalf("readlink made-link = %q, %v", target, err)
		}
		if err := os.Remove(made); err != nil {
			t.Fatal(err)
		}

		// A link up a level reads back and resolves (READLINK used to
		// refuse "..", which read as EIO); making one is refused by
		// default (ExportOptions.AllowAnySymlinkTarget).
		if err := fs.Symlink("../hello.txt", "/sub/up"); err != nil {
			t.Fatal(err)
		}
		defer fs.Remove("/sub/up")
		up := filepath.Join(mnt, "sub", "up")
		if target, err := os.Readlink(up); err != nil || target != "../hello.txt" {
			t.Fatalf("readlink sub/up = %q, %v; want ../hello.txt", target, err)
		}
		if data, err := os.ReadFile(up); err != nil || string(data) != macOSSeedContent {
			t.Fatalf("read through sub/up = %q, %v", data, err)
		}
		if err := os.Symlink("../hello.txt", filepath.Join(mnt, "sub", "up2")); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("ln -s ../hello.txt sub/up2: %v, want permission denied", err)
		}
	})

	t.Run("write read rename rm mkdir rmdir", func(t *testing.T) {
		f1 := filepath.Join(mnt, "new.txt")
		if err := os.WriteFile(f1, []byte("first version, longer\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f1, []byte("second\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if data, err := os.ReadFile(f1); err != nil || string(data) != "second\n" {
			t.Fatalf("read new.txt = %q, %v", data, err)
		}
		if info, err := os.Stat(f1); err != nil {
			t.Fatal(err)
		} else if st := info.Sys().(*syscall.Stat_t); st.Uid != uid {
			t.Errorf("new.txt uid = %d, want %d", st.Uid, uid)
		}

		f2 := filepath.Join(mnt, "sub", "moved.txt")
		if err := os.Rename(f1, f2); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(f1); !os.IsNotExist(err) {
			t.Fatalf("new.txt still present after rename: %v", err)
		}
		if data, err := os.ReadFile(f2); err != nil || string(data) != "second\n" {
			t.Fatalf("read moved.txt = %q, %v", data, err)
		}
		if got := readDirNames(t, filepath.Join(mnt, "sub")); !equalStrings(got, []string{"moved.txt"}) {
			t.Fatalf("sub lists %v, want [moved.txt]", got)
		}

		// Rename over an existing file replaces it (git's lock-file commit).
		f3 := filepath.Join(mnt, "sub", "other.txt")
		if err := os.WriteFile(f3, []byte("replaced\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(f3, f2); err != nil {
			t.Fatalf("rename over an existing file: %v", err)
		}
		if data, err := os.ReadFile(f2); err != nil || string(data) != "replaced\n" {
			t.Fatalf("read moved.txt after replace = %q, %v", data, err)
		}
		if err := os.Remove(f2); err != nil {
			t.Fatal(err)
		}

		// A file created read-only can still be written through the open
		// descriptor (git writes its 0444 object files this way).
		ro := filepath.Join(mnt, "readonly.txt")
		f, err := os.OpenFile(ro, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o444)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte("sealed\n")); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("close read-only file after writing: %v", err)
		}
		if info, err := os.Stat(ro); err != nil || info.Mode().Perm() != 0o444 {
			t.Fatalf("readonly.txt: %v, %v; want mode 0444", info, err)
		}
		if data, err := os.ReadFile(ro); err != nil || string(data) != "sealed\n" {
			t.Fatalf("read readonly.txt = %q, %v", data, err)
		}
		// O_EXCL on an existing name fails and leaves the file alone
		// (lock files depend on it).
		if _, err := os.OpenFile(ro, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); !os.IsExist(err) {
			t.Fatalf("O_EXCL create of an existing file: %v, want EEXIST", err)
		}
		if data, err := os.ReadFile(ro); err != nil || string(data) != "sealed\n" {
			t.Fatalf("readonly.txt after O_EXCL attempt = %q, %v", data, err)
		}
		if err := os.Remove(ro); err != nil {
			t.Fatal(err)
		}

		d := filepath.Join(mnt, "dir")
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(d); err != nil || !info.IsDir() {
			t.Fatalf("stat dir: %v, %v", info, err)
		} else if st := info.Sys().(*syscall.Stat_t); st.Uid != uid {
			t.Errorf("dir uid = %d, want %d", st.Uid, uid)
		}
		if err := os.Remove(d); err != nil {
			t.Fatal(err)
		}
		if got := readDirNames(t, mnt); !equalStrings(got, macOSSeedEntries) {
			t.Fatalf("after cleanup the root lists %v, want %v", got, macOSSeedEntries)
		}
	})

	t.Run("removing a large directory while listing it", func(t *testing.T) {
		// Hundreds of entries span many READDIR pages; removing each batch
		// before reading the next must not make the client skip entries.
		for _, how := range []string{"batches", "rm -rf"} {
			big := filepath.Join(mnt, "big")
			if err := os.Mkdir(big, 0o755); err != nil {
				t.Fatal(err)
			}
			const n = 400
			for i := 0; i < n; i++ {
				name := fmt.Sprintf("entry-with-a-reasonably-long-name-%04d", i)
				if err := os.WriteFile(filepath.Join(big, name), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := len(readDirNames(t, big)); got != n {
				t.Fatalf("%s: listing shows %d entries, want %d", how, got, n)
			}
			if how == "rm -rf" {
				if out, err := runWithTimeout(60*time.Second, mnt, os.Environ(), "rm", "-rf", "big"); err != nil {
					t.Fatalf("rm -rf: %v\n%s", err, out)
				}
			} else {
				removeInBatches(t, big)
				if err := os.Remove(big); err != nil {
					t.Fatalf("%s: rmdir after removing every listed entry: %v (%d entries were skipped)", how, err, len(readDirNames(t, big)))
				}
			}
			if _, err := os.Lstat(big); !os.IsNotExist(err) {
				t.Fatalf("%s: big still exists: %v", how, err)
			}
		}
	})

	if locking == "" {
		t.Run("locallocks lets the client lock files", func(t *testing.T) {
			f, err := os.Create(filepath.Join(mnt, "lockme"))
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(f.Name())
			defer f.Close()
			if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
				t.Fatalf("flock: %v", err)
			}
			if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
				t.Fatalf("unlock: %v", err)
			}
		})
	}

	t.Run("git accepts the workspace", func(t *testing.T) {
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("git not installed")
		}
		repo := filepath.Join(mnt, "repo")
		if err := os.Mkdir(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, "README"), []byte("workspace\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Isolate git from the user's config so no safe.directory entry can
		// hide an ownership problem.
		env := append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=absnfs", "GIT_AUTHOR_EMAIL=absnfs@example.invalid",
			"GIT_COMMITTER_NAME=absnfs", "GIT_COMMITTER_EMAIL=absnfs@example.invalid",
		)
		git := func(args ...string) string {
			t.Helper()
			out, err := runWithTimeout(60*time.Second, repo, env, "git", args...)
			if bytes.Contains(out, []byte("dubious ownership")) {
				t.Fatalf("git %v: dubious ownership\n%s", args, out)
			}
			if err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
			return string(out)
		}
		git("init", "-q")
		git("add", "README")
		if st := git("status", "--porcelain"); !strings.Contains(st, "A  README") {
			t.Fatalf("git status = %q, want README staged", st)
		}
		git("commit", "-q", "-m", "first")
		if st := git("status", "--porcelain"); st != "" {
			t.Fatalf("git status after commit = %q, want clean", st)
		}
		if log := git("log", "--oneline"); !strings.Contains(log, "first") {
			t.Fatalf("git log = %q", log)
		}
		if err := os.RemoveAll(repo); err != nil {
			t.Fatal(err)
		}
	})
}

// runWithTimeout runs a command with a deadline so a misbehaving mount can
// never hang the test.
func runWithTimeout(d time.Duration, dir string, env []string, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	return cmd.CombinedOutput()
}

// isMountedAt reports whether mount(8) lists something mounted on dir.
func isMountedAt(dir string) bool {
	out, err := runWithTimeout(10*time.Second, "", nil, "/sbin/mount")
	if err != nil {
		return false
	}
	return strings.Contains(string(out), " on "+dir+" (")
}

// unmountMacOS unmounts dir as the current user, escalating from umount to
// umount -f to diskutil unmount force.
func unmountMacOS(dir string) error {
	attempts := [][]string{
		{"/sbin/umount", dir},
		{"/sbin/umount", "-f", dir},
		{"/usr/sbin/diskutil", "unmount", "force", dir},
	}
	var last []byte
	for _, a := range attempts {
		if !isMountedAt(dir) {
			return nil
		}
		last, _ = runWithTimeout(30*time.Second, "", nil, a[0], a[1:]...)
	}
	if isMountedAt(dir) {
		return fmt.Errorf("still mounted: %s", last)
	}
	return nil
}

func readDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// removeInBatches lists dir a few entries at a time through one open handle,
// removing each batch before reading the next, the way an incremental
// cleanup walks a directory.
func removeInBatches(t *testing.T, dir string) {
	t.Helper()
	f, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for {
		batch, err := f.ReadDir(25)
		for _, e := range batch {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				t.Fatal(err)
			}
		}
		if err != nil {
			return // io.EOF, or an error the rmdir check reports
		}
	}
}
