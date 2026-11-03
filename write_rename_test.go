package absnfs

import (
	"bytes"
	"errors"
	"io"
	"os"
	"syscall"
	"testing"
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
			if _, err := e.nfs.openForOwnerWrite(p, denied); err != denied {
				t.Errorf("%s: err = %v, want the original denial", p, err)
			}
		}
	})

	t.Run("already writable opens directly", func(t *testing.T) {
		e := newFaEnv(t, ExportOptions{})
		f, err := e.nfs.openForOwnerWrite("/dir/file.txt", denied)
		if err != nil {
			t.Fatalf("err = %v, want an open file", err)
		}
		f.Close()
	})

	t.Run("chmod refused", func(t *testing.T) {
		e := newFaEnv(t, ExportOptions{})
		e.fs.Chmod("/dir/file.txt", 0o444)
		e.nfs.fs = &faHookFS{SymlinkFileSystem: e.fs, chmodErr: []error{syscall.EPERM}}
		if _, err := e.nfs.openForOwnerWrite("/dir/file.txt", denied); err != denied {
			t.Errorf("err = %v, want the original denial", err)
		}
	})

	t.Run("mode cannot be restored", func(t *testing.T) {
		e := newFaEnv(t, ExportOptions{})
		e.fs.Chmod("/dir/file.txt", 0o444)
		e.nfs.fs = &faHookFS{SymlinkFileSystem: e.fs, chmodErr: []error{nil, syscall.EIO}}
		if _, err := e.nfs.openForOwnerWrite("/dir/file.txt", denied); !errors.Is(err, syscall.EIO) {
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
}

func TestMapErrorNotEmpty(t *testing.T) {
	if got := mapError(&os.PathError{Op: "remove", Path: "/d", Err: syscall.ENOTEMPTY}); got != NFSERR_NOTEMPTY {
		t.Errorf("mapError(ENOTEMPTY) = %d, want NFSERR_NOTEMPTY", got)
	}
}
