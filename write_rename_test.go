package absnfs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/absfs/absfs"
	"github.com/absfs/osfs"
)

// TestWriteOwnerOverride: NFS is stateless, so a file created read-only and
// then written (git's 0444 object files) arrives as a WRITE to a 0444 file.
func TestWriteOwnerOverride(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	if err := e.fs.Chmod("/dir/file.txt", 0o444); err != nil {
		t.Fatal(err)
	}
	file := e.handle(t, "/dir/file.txt")
	node, _ := e.handler.lookupNode(file)

	// The public Write API applies no override.
	if _, err := e.nfs.Write(node, 0, []byte("x")); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("Write to a 0444 file = %v, want a permission error", err)
	}

	// Another user is refused.
	other := *e.auth
	other.EffectiveUID = e.owner.UID + 1
	r, _ := e.handler.handleWrite(bytes.NewReader(faWriteRequest(file, 0, []byte("HELLO"))), &RPCReply{}, &other)
	if st := readStatusFromReply(r); st == NFS_OK {
		t.Fatal("WRITE by a non-owner to a 0444 file succeeded")
	}

	// The owner gets through, and the mode is left as it was.
	r, _ = e.handler.handleWrite(bytes.NewReader(faWriteRequest(file, 0, []byte("HELLO"))), &RPCReply{}, e.auth)
	if st := readStatusFromReply(r); st != NFS_OK {
		t.Fatalf("WRITE by the owner to a 0444 file = %d, want NFS_OK", st)
	}
	info, err := e.fs.Lstat("/dir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o444 {
		t.Errorf("mode after owner write = %o, want 444", info.Mode().Perm())
	}
	f, _ := e.fs.Open("/dir/file.txt")
	data, _ := io.ReadAll(f)
	f.Close()
	if string(data) != "HELLO" {
		t.Errorf("content = %q, want HELLO", data)
	}
}

func TestOpenForOwnerWrite(t *testing.T) {
	denied := errors.New("denied")

	t.Run("not applicable", func(t *testing.T) {
		e := newFaEnv(t, ExportOptions{})
		for _, p := range []string{"/dir/missing", "/dir/sub", "/dir/link"} {
			if _, err := e.nfs.openForOwnerWrite(p, e.owner.UID, denied); err != denied {
				t.Errorf("%s: err = %v, want the original denial", p, err)
			}
		}
	})

	t.Run("already writable opens directly", func(t *testing.T) {
		e := newFaEnv(t, ExportOptions{})
		f, err := e.nfs.openForOwnerWrite("/dir/file.txt", e.owner.UID, denied)
		if err != nil {
			t.Fatalf("err = %v, want an open file", err)
		}
		f.Close()
	})

	t.Run("chmod refused", func(t *testing.T) {
		e := newFaEnv(t, ExportOptions{})
		e.fs.Chmod("/dir/file.txt", 0o444)
		e.nfs.fs = &faHookFS{SymlinkFileSystem: e.fs, chmodErr: []error{syscall.EPERM}}
		if _, err := e.nfs.openForOwnerWrite("/dir/file.txt", e.owner.UID, denied); err != denied {
			t.Errorf("err = %v, want the original denial", err)
		}
	})

	t.Run("mode cannot be restored", func(t *testing.T) {
		e := newFaEnv(t, ExportOptions{})
		e.fs.Chmod("/dir/file.txt", 0o444)
		e.nfs.fs = &faHookFS{SymlinkFileSystem: e.fs, chmodErr: []error{nil, syscall.EIO}}
		if _, err := e.nfs.openForOwnerWrite("/dir/file.txt", e.owner.UID, denied); !errors.Is(err, syscall.EIO) {
			t.Errorf("err = %v, want EIO", err)
		}
	})
}

func TestRenameReplacesExistingTarget(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	dir, err := e.nfs.Lookup("/dir")
	if err != nil {
		t.Fatal(err)
	}
	write := func(p, s string) {
		f, err := e.fs.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(s))
		f.Close()
	}

	// file over file
	write("/dir/new.txt", "new")
	if err := e.nfs.Rename(dir, "new.txt", dir, "file.txt"); err != nil {
		t.Fatalf("rename over a file: %v", err)
	}
	f, _ := e.fs.Open("/dir/file.txt")
	data, _ := io.ReadAll(f)
	f.Close()
	if string(data) != "new" {
		t.Errorf("content = %q, want new", data)
	}
	if _, err := e.fs.Lstat("/dir/new.txt"); !os.IsNotExist(err) {
		t.Errorf("source still present: %v", err)
	}

	// directory over an empty directory
	e.fs.Mkdir("/dir/empty", 0o755)
	e.fs.Mkdir("/dir/full", 0o755)
	write("/dir/full/x", "x")
	if err := e.nfs.Rename(dir, "full", dir, "empty"); err != nil {
		t.Fatalf("rename a directory over an empty one: %v", err)
	}
	if _, err := e.fs.Lstat("/dir/empty/x"); err != nil {
		t.Errorf("moved directory lost its contents: %v", err)
	}

	cases := []struct {
		from, to string
		want     uint32
	}{
		{"file.txt", "sub", NFSERR_ISDIR},  // file over directory
		{"sub", "file.txt", NFSERR_NOTDIR}, // directory over file
		{"sub", "empty", NFSERR_NOTEMPTY},  // directory over non-empty directory
	}
	for _, c := range cases {
		err := e.nfs.Rename(dir, c.from, dir, c.to)
		if got := mapError(err); got != c.want {
			t.Errorf("rename %s over %s: %v (status %d), want status %d", c.from, c.to, err, got, c.want)
		}
	}

	// Renaming onto itself is a no-op.
	if err := e.nfs.Rename(dir, "file.txt", dir, "file.txt"); err != nil {
		t.Errorf("rename onto itself: %v", err)
	}
}

func TestReplaceByRenameEdgeCases(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	exists := errors.New("exists")
	if err := e.nfs.replaceByRename("/dir/missing", "/dir/file.txt", exists); !os.IsNotExist(err) && !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing source: %v, want not-exist", err)
	}
	if err := e.nfs.replaceByRename("/dir/file.txt", "/dir/missing", exists); err != exists {
		t.Errorf("missing target: %v, want the original error", err)
	}
	// Conflicts that appear after RENAME's own check (a racing client).
	e.fs.Mkdir("/dir/full", 0o755)
	if f, err := e.fs.Create("/dir/full/x"); err == nil {
		f.Close()
	}
	cases := []struct {
		from, to string
		want     uint32
	}{
		{"/dir/sub", "/dir/file.txt", NFSERR_NOTDIR},
		{"/dir/file.txt", "/dir/sub", NFSERR_ISDIR},
		{"/dir/sub", "/dir/full", NFSERR_NOTEMPTY},
	}
	for _, c := range cases {
		if got := mapError(e.nfs.replaceByRename(c.from, c.to, exists)); got != c.want {
			t.Errorf("replace %s with %s: status %d, want %d", c.to, c.from, got, c.want)
		}
	}
}

func TestMapErrorNotEmpty(t *testing.T) {
	if got := mapError(&os.PathError{Op: "remove", Path: "/d", Err: syscall.ENOTEMPTY}); got != NFSERR_NOTEMPTY {
		t.Errorf("mapError(ENOTEMPTY) = %d, want NFSERR_NOTEMPTY", got)
	}
}

// renameHookFS makes Rename fail the way a host does, to show that RENAME's
// conflict rules do not depend on the backend's error.
type renameHookFS struct {
	absfs.SymlinkFileSystem
	renameErr  []error // returned by successive Rename calls; nil passes through
	readDirErr error
}

func (f *renameHookFS) Rename(oldpath, newpath string) error {
	if len(f.renameErr) > 0 {
		err := f.renameErr[0]
		f.renameErr = f.renameErr[1:]
		if err != nil {
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: err}
		}
	}
	return f.SymlinkFileSystem.Rename(oldpath, newpath)
}

func (f *renameHookFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if f.readDirErr != nil {
		return nil, f.readDirErr
	}
	return f.SymlinkFileSystem.ReadDir(name)
}

// TestRenameConflictsIgnoreBackendError: a backend that refuses every
// existing target with a permission error (Windows' ERROR_ACCESS_DENIED)
// still yields the RFC 1813 statuses, and a directory still replaces an
// empty directory.
func TestRenameConflictsIgnoreBackendError(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	e.fs.Mkdir("/dir/empty", 0o755)
	e.fs.Mkdir("/dir/full", 0o755)
	if f, err := e.fs.Create("/dir/full/x"); err == nil {
		f.Close()
	}
	hook := &renameHookFS{SymlinkFileSystem: e.fs}
	e.nfs.fs = hook
	dir, err := e.nfs.Lookup("/dir")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		from, to string
		want     uint32
	}{
		{"file.txt", "sub", NFSERR_ISDIR},
		{"sub", "file.txt", NFSERR_NOTDIR},
		{"sub", "full", NFSERR_NOTEMPTY},
		{"link", "sub", NFSERR_ISDIR},     // a symlink is not a directory
		{"sublink", "sub", NFSERR_ISDIR},  // nor is a symlink to one
		{"sub", "sublink", NFSERR_NOTDIR}, // so a directory cannot replace it
	}
	for _, c := range cases {
		hook.renameErr = []error{syscall.EACCES}
		err := e.nfs.Rename(dir, c.from, dir, c.to)
		if got := mapError(err); got != c.want {
			t.Errorf("rename %s over %s: %v (status %d), want status %d", c.from, c.to, err, got, c.want)
		}
		if len(hook.renameErr) == 0 {
			t.Errorf("rename %s over %s reached the backend", c.from, c.to)
		}
	}

	hook.renameErr = []error{syscall.EACCES}
	if err := e.nfs.Rename(dir, "sub", dir, "empty"); err != nil {
		t.Fatalf("directory over an empty directory: %v", err)
	}
	if info, err := e.fs.Lstat("/dir/empty"); err != nil || !info.IsDir() {
		t.Errorf("target after replace: %v, %v", info, err)
	}
	if _, err := e.fs.Lstat("/dir/sub"); !os.IsNotExist(err) {
		t.Errorf("source still present: %v", err)
	}
}

// TestRenameRestoresEmptyTarget: when the directory replacing an empty one
// cannot be moved, the empty target is put back.
func TestRenameRestoresEmptyTarget(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	e.fs.Mkdir("/dir/empty", 0o700)
	e.nfs.fs = &renameHookFS{SymlinkFileSystem: e.fs, renameErr: []error{syscall.EACCES, syscall.EIO}}
	dir, _ := e.nfs.Lookup("/dir")
	if err := e.nfs.Rename(dir, "sub", dir, "empty"); !errors.Is(err, syscall.EIO) {
		t.Fatalf("err = %v, want EIO", err)
	}
	info, err := e.fs.Lstat("/dir/empty")
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Errorf("empty target after failed rename: %v, %v", info, err)
	}
	if _, err := e.fs.Lstat("/dir/sub"); err != nil {
		t.Errorf("source lost: %v", err)
	}
}

func TestRenameTargetUnreadable(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	e.fs.Mkdir("/dir/empty", 0o755)
	e.nfs.fs = &renameHookFS{SymlinkFileSystem: e.fs, readDirErr: syscall.EIO}
	dir, _ := e.nfs.Lookup("/dir")
	if err := e.nfs.Rename(dir, "sub", dir, "empty"); !errors.Is(err, syscall.EIO) {
		t.Errorf("err = %v, want EIO", err)
	}
}

// TestMapErrorStatusPrecedence: a server-detected status wins over whatever
// else its errno matches. On Windows syscall.ENOTDIR is ERROR_PATH_NOT_FOUND,
// which is also os.ErrNotExist; this reproduces that on every host.
func TestMapErrorStatusPrecedence(t *testing.T) {
	notDir := &statusError{NFSERR_NOTDIR, "not a directory", os.ErrNotExist}
	if got := mapError(fmt.Errorf("wrapped: %w", notDir)); got != NFSERR_NOTDIR {
		t.Errorf("mapError = %d, want NFSERR_NOTDIR", got)
	}
	// A host errno is mapped as the host means it. Windows has no ENOTDIR of
	// its own: syscall.ENOTDIR there is ERROR_PATH_NOT_FOUND, a missing path.
	want := uint32(NFSERR_NOTDIR)
	if runtime.GOOS == "windows" {
		want = NFSERR_NOENT
	}
	if got := mapError(&os.PathError{Op: "open", Path: "/a/b", Err: syscall.ENOTDIR}); got != want {
		t.Errorf("mapError(syscall.ENOTDIR) = %d, want %d", got, want)
	}
	for _, e := range []*statusError{errNotDir, errIsDir, errNotEmpty} {
		if e.Error() == "" || !errors.Is(e, e.errno) {
			t.Errorf("%v: empty message or does not unwrap to its errno", e.errno)
		}
	}
}

// TestRenameConflictsOnHost runs RENAME's conflict cases against the host
// filesystem (osfs), whose rename rules differ between unix and Windows.
func TestRenameConflictsOnHost(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"sub", "empty", "full", "full2"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"a.txt", "b.txt", "full/x", "full2/y"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ofs, err := osfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	// osfs directly, not under basefs: basefs wants a native absolute prefix
	// and osfs an absfs path (/c/...), which on Windows cannot both hold.
	nfs, err := New(ofs, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nfs.Close() })
	dir, err := nfs.Lookup(osfs.FromNative(root))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		from, to string
		want     uint32
	}{
		{"a.txt", "sub", NFSERR_ISDIR},
		{"sub", "a.txt", NFSERR_NOTDIR},
		{"sub", "full", NFSERR_NOTEMPTY},
		{"a.txt", "b.txt", NFS_OK},
		{"full2", "empty", NFS_OK},
	}
	for _, c := range cases {
		err := nfs.Rename(dir, c.from, dir, c.to)
		if got := mapError(err); got != c.want {
			t.Errorf("rename %s over %s: %v (status %d), want status %d", c.from, c.to, err, got, c.want)
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "b.txt")); err != nil || string(data) != "a.txt" {
		t.Errorf("b.txt = %q, %v; want the renamed a.txt", data, err)
	}
	if _, err := os.Stat(filepath.Join(root, "empty", "y")); err != nil {
		t.Errorf("replaced directory lost its contents: %v", err)
	}
}
