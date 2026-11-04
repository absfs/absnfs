package absnfs

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestAncestorSwappedForLink: a directory above a handle's object, replaced
// by a symbolic link after the handle was issued, is not walked through.
// Outside the export lies a subtree of the same shape, so every path the
// handles name exists there too; each procedure answers NFS3ERR_STALE (the
// object is no longer where its handle says), counts it in StaleHandles,
// and nothing outside is read or changed.
func TestAncestorSwappedForLink(t *testing.T) {
	s := newSwapEnv(t)
	for _, name := range []string{"sub/victim", "sub/victimdir"} {
		p := filepath.Join(s.outside, name)
		var err error
		if filepath.Base(name) == "victimdir" {
			err = os.Mkdir(p, 0o755)
		} else {
			err = os.WriteFile(p, []byte("victim"), 0o644)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("f", filepath.Join(s.outside, "ln")); err != nil {
		t.Fatal(err)
	}
	s.before = s.snapshot()

	for _, name := range []string{"d", "d/sub", "d/sub/victimdir"} {
		s.mkdir(s.export + "/" + name)
	}
	s.writeFile(s.export+"/d/f", "inside")
	s.writeFile(s.export+"/d/sub/victim", "inside")
	if err := os.Symlink("f", filepath.Join(s.native, "export", "d", "ln")); err != nil {
		t.Fatal(err)
	}
	root := s.handle(s.export)
	sub := s.handle(s.export + "/d/sub")
	f := s.handle(s.export + "/d/f")
	ln := s.handle(s.export + "/d/ln")
	s.swapForLink("d", s.outside)

	data := []byte("pwned")
	write := append(xdrArgs(f, uint64(0), uint32(len(data)), uint32(2), uint32(len(data))), data...)
	write = append(write, 0, 0, 0)
	sattr := func(mode bool, size bool) []byte {
		return encodeSattr3(mode, 0o600, false, 0, false, 0, size, 0, 0, 0, 0, 0, 0, 0)
	}
	owner := encodeSattr3(false, 0, true, uint32(os.Getuid()), false, 0, false, 0, 0, 0, 0, 0, 0, 0)
	cases := []struct {
		name string
		proc uint32
		args []byte
	}{
		{"LOOKUP", NFSPROC3_LOOKUP, xdrArgs(sub, "victim")},
		{"READDIR", NFSPROC3_READDIR, xdrArgs(sub, uint64(0), uint64(0), uint32(4096))},
		{"READDIRPLUS", NFSPROC3_READDIRPLUS, xdrArgs(sub, uint64(0), uint64(0), uint32(4096), uint32(4096))},
		{"CREATE", NFSPROC3_CREATE, append(xdrArgs(sub, "planted", uint32(0)), noSattr...)},
		{"CREATE over a file", NFSPROC3_CREATE, append(xdrArgs(sub, "victim", uint32(0)), sattr(false, true)...)},
		{"MKDIR", NFSPROC3_MKDIR, append(xdrArgs(sub, "newdir"), noSattr...)},
		{"SYMLINK", NFSPROC3_SYMLINK, append(append(xdrArgs(sub, "newln"), noSattr...), xdrArgs("t")...)},
		{"REMOVE", NFSPROC3_REMOVE, xdrArgs(sub, "victim")},
		{"RMDIR", NFSPROC3_RMDIR, xdrArgs(sub, "victimdir")},
		{"RENAME from it", NFSPROC3_RENAME, xdrArgs(sub, "victim", root, "stolen")},
		{"RENAME into it", NFSPROC3_RENAME, xdrArgs(root, "d", sub, "victim")},
		{"READ", NFSPROC3_READ, xdrArgs(f, uint64(0), uint32(100))},
		{"WRITE", NFSPROC3_WRITE, write},
		{"SETATTR size", NFSPROC3_SETATTR, setattrRequest(f, sattr(false, true))},
		{"SETATTR mode", NFSPROC3_SETATTR, setattrRequest(f, sattr(true, false))},
		{"SETATTR owner", NFSPROC3_SETATTR, setattrRequest(f, owner)},
		{"READLINK", NFSPROC3_READLINK, xdrArgs(ln)},
	}
	for _, c := range cases {
		before := s.nfs.GetMetrics().StaleHandles
		reply, err := s.h.handleNFSCall(&RPCCall{Header: RPCMsgHeader{Program: NFS_PROGRAM, Version: NFS_V3, Procedure: c.proc}}, bytes.NewReader(c.args), &RPCReply{}, s.auth)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := readStatusFromReply(reply); got != NFSERR_STALE {
			t.Errorf("%s below a directory swapped for a link: status %d, want NFS3ERR_STALE", c.name, got)
		} else if s.nfs.GetMetrics().StaleHandles != before+1 {
			t.Errorf("%s: the STALE reply was not counted", c.name)
		}
		s.checkOutside(c.name)
	}
	if _, err := os.Lstat(filepath.Join(s.native, "export", "stolen")); err == nil {
		t.Error("RENAME moved a file from outside into the export")
	}

	// Through the Go API too.
	for name, err := range map[string]error{
		"Read":  func() error { n := s.nodeAt("/d/f"); _, err := s.nfs.Read(n, 0, 10); return err }(),
		"Write": func() error { n := s.nodeAt("/d/f"); _, err := s.nfs.Write(n, 0, data); return err }(),
		"SetAttr": func() error {
			n := s.nodeAt("/d/f")
			a := *n.attrs
			a.Mode = 0o600
			return s.nfs.SetAttr(n, &a)
		}(),
		"Readlink": func() error { _, err := s.nfs.Readlink(s.nodeAt("/d/ln")); return err }(),
		"Create": func() error {
			_, err := s.nfs.Create(s.nodeAt("/d/sub"), "planted", &NFSAttrs{Mode: 0o644})
			return err
		}(),
	} {
		if mapError(err) != NFSERR_STALE {
			t.Errorf("%s below a directory swapped for a link: %v, want NFS3ERR_STALE", name, err)
		}
	}
	s.checkOutside("the Go API")
}

// nodeAt returns a node for the export path p as it was issued, without
// looking it up now (which would follow a swapped directory on the host).
func (s *swapEnv) nodeAt(p string) *NFSNode {
	path := s.export + p
	return &NFSNode{SymlinkFileSystem: s.nfs.fs, path: path, server: s.nfs, attrs: &NFSAttrs{Mode: 0o644}}
}

// TestCreateUncheckedOverLinkOrDirectory: UNCHECKED CREATE of a name that
// is a symbolic link (to a file outside the export) or a directory answers
// NFS3ERR_EXIST, as Linux knfsd does, and never opens or truncates what the
// link points to.
func TestCreateUncheckedOverLinkOrDirectory(t *testing.T) {
	s := newSwapEnv(t)
	canary := filepath.Join(s.outside, "canary")
	if err := os.Symlink(canary, filepath.Join(s.native, "export", "ln")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(s.outside, "nothing"), filepath.Join(s.native, "export", "dangling")); err != nil {
		t.Fatal(err)
	}
	s.mkdir(s.export + "/dir")
	root := s.handle(s.export)
	sizeZero := encodeSattr3(false, 0, false, 0, false, 0, true, 0, 0, 0, 0, 0, 0, 0)
	for _, c := range []struct {
		name  string
		sattr []byte
	}{
		{"ln", noSattr},
		{"ln", sizeZero},
		{"dangling", sizeZero},
		{"dir", sizeZero},
	} {
		if got := s.call(NFSPROC3_CREATE, append(xdrArgs(root, c.name, uint32(0)), c.sattr...)); got != NFSERR_EXIST {
			t.Errorf("UNCHECKED CREATE over %s: status %d, want NFS3ERR_EXIST", c.name, got)
		}
		s.checkOutside("UNCHECKED CREATE over " + c.name)
	}
	if _, err := os.Lstat(filepath.Join(s.outside, "nothing")); err == nil {
		t.Error("CREATE over a dangling link made its target outside the export")
	}

	dir, err := s.nfs.Lookup(s.export)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.nfs.Create(dir, "ln", &NFSAttrs{Mode: 0o644}); !errors.Is(err, os.ErrExist) {
		t.Errorf("Create over a link: %v, want an os.ErrExist error", err)
	}
	s.checkOutside("Create over a link")

	// A regular file is truncated only to the size the request sets.
	s.writeFile(s.export+"/file", "content")
	if got := s.call(NFSPROC3_CREATE, append(xdrArgs(root, "file", uint32(0)), noSattr...)); got != NFS_OK {
		t.Fatalf("UNCHECKED CREATE over a file: status %d", got)
	}
	if b, _ := os.ReadFile(filepath.Join(s.native, "export", "file")); string(b) != "content" {
		t.Errorf("UNCHECKED CREATE without a size changed the file to %q", b)
	}
	three := encodeSattr3(false, 0, false, 0, false, 0, true, 3, 0, 0, 0, 0, 0, 0)
	if got := s.call(NFSPROC3_CREATE, append(xdrArgs(root, "file", uint32(0)), three...)); got != NFS_OK {
		t.Fatalf("UNCHECKED CREATE over a file with size 3: status %d", got)
	}
	if b, _ := os.ReadFile(filepath.Join(s.native, "export", "file")); string(b) != "con" {
		t.Errorf("UNCHECKED CREATE with size 3 left %q", b)
	}
	huge := encodeSattr3(false, 0, false, 0, false, 0, true, 1<<63, 0, 0, 0, 0, 0, 0)
	if got := s.call(NFSPROC3_CREATE, append(xdrArgs(root, "file", uint32(0)), huge...)); got != NFSERR_INVAL {
		t.Errorf("UNCHECKED CREATE with a size beyond int64: status %d, want NFS3ERR_INVAL", got)
	}
}

// TestCreateOverDirectoryWithoutEEXIST: a backend that refuses O_EXCL on a
// directory with another error than EEXIST (Windows answers "is a
// directory" or "access denied") still gets NFS3ERR_EXIST for every create
// mode: the name exists.
func TestCreateOverDirectoryWithoutEEXIST(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	root := e.handle(t, "/")
	e.nfs.fs = &faHookFS{SymlinkFileSystem: e.fs, openErr: map[string]error{"/dir": syscall.EISDIR}}
	for _, how := range []uint32{0, 1} {
		body := faDirOp(root, "dir", faU32(how), faNoSattr)
		if st := faCall(t, e, wrap(e.handler.handleCreate), body); st != NFSERR_EXIST {
			t.Errorf("CREATE mode %d over a directory: status %d, want NFS3ERR_EXIST", how, st)
		}
	}
}
