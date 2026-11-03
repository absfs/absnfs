//go:build unix

package absnfs

import (
	"bytes"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
)

// ownerFS records owners itself, so a test can chown to any id without
// root: Chown and Lchown record the owner, and Lstat reports it the way
// osfs does, in a *syscall.Stat_t.
type ownerFS struct {
	*countingFS
	mu     sync.Mutex
	owners map[string]FileOwner
}

type ownedInfo struct {
	os.FileInfo
	st *syscall.Stat_t
}

func (i ownedInfo) Sys() any { return i.st }

func (f *ownerFS) setOwner(name string, uid, gid int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.owners[name] = FileOwner{UID: uint32(uid), GID: uint32(gid)}
}

func (f *ownerFS) Chown(name string, uid, gid int) error {
	f.setOwner(name, uid, gid)
	return nil
}

func (f *ownerFS) Lchown(name string, uid, gid int) error {
	f.setOwner(name, uid, gid)
	return nil
}

func (f *ownerFS) Lstat(name string) (os.FileInfo, error) {
	info, err := f.countingFS.Lstat(name)
	if err != nil {
		return info, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if o, ok := f.owners[name]; ok {
		return ownedInfo{info, &syscall.Stat_t{Uid: o.UID, Gid: o.GID}}, nil
	}
	return info, nil
}

// newOwnerEnv is newCacheEnv with an ownerFS between the server and the
// counting memfs.
func newOwnerEnv(t *testing.T, opts ExportOptions) (*cacheEnv, *ownerFS) {
	t.Helper()
	e := newCacheEnv(t, opts)
	ofs := &ownerFS{countingFS: e.fs, owners: map[string]FileOwner{}}
	e.nfs.fs = ofs
	return e, ofs
}

// TestSymlinkReportsOwnerAfterLchown: SYMLINK's reply and a later GETATTR
// report the owner the link was given after Symlink cached its attributes.
func TestSymlinkReportsOwnerAfterLchown(t *testing.T) {
	e, _ := newOwnerEnv(t, ExportOptions{AttrCacheTimeout: time.Hour})
	e.auth.EffectiveUID, e.auth.EffectiveGID = 4242, 4343
	e.writeFile("/f", "x")

	args := faDirOp(e.handle("/"), "l", faNoSattr, func(b *bytes.Buffer) { xdrEncodeString(b, "f") })
	reply, err := e.h.handleSymlink(bytes.NewReader(args), &RPCReply{}, e.auth)
	if err != nil {
		t.Fatal(err)
	}
	r := newFaReader(t, reply)
	if st := r.status(); st != NFS_OK {
		t.Fatalf("SYMLINK status %d", st)
	}
	if r.u32() != 1 {
		t.Fatal("SYMLINK reply without a handle")
	}
	r.fh()
	if a := r.postOpAttr(); a.Uid != 4242 || a.Gid != 4343 {
		t.Errorf("SYMLINK reply reports owner %d:%d, want 4242:4343", a.Uid, a.Gid)
	}
	attrs, err := e.nfs.GetAttr(e.lookup("/l"))
	if err != nil {
		t.Fatal(err)
	}
	if attrs.Uid != 4242 || attrs.Gid != 4343 {
		t.Errorf("GETATTR after SYMLINK reports owner %d:%d, want 4242:4343", attrs.Uid, attrs.Gid)
	}
}

// TestOwnerOverrideChecksCurrentOwner: the owner override for writing a
// read-only file goes to whoever owns the file now, not to the owner the
// attribute cache remembers.
func TestOwnerOverrideChecksCurrentOwner(t *testing.T) {
	e, ofs := newOwnerEnv(t, ExportOptions{AttrCacheTimeout: time.Hour})
	e.writeFile("/f", "")
	if err := e.fs.SymlinkFileSystem.Chmod("/f", 0o444); err != nil {
		t.Fatal(err)
	}
	ofs.setOwner("/f", 1000, 1000)
	e.auth.EffectiveUID, e.auth.EffectiveGID = 1000, 1000
	fh := e.handle("/f")
	if st, a := e.getattr(fh); st != NFS_OK || a.Uid != 1000 {
		t.Fatalf("GETATTR: status %d uid %d", st, a.Uid)
	}

	ofs.setOwner("/f", 2000, 2000) // chowned behind the server's back
	reply, err := e.h.handleWrite(bytes.NewReader(faWriteRequest(fh, 0, []byte("x"))), &RPCReply{}, e.auth)
	if err != nil {
		t.Fatal(err)
	}
	if st := readStatusFromReply(reply); st == NFS_OK {
		t.Errorf("WRITE by the former owner to a 0444 file succeeded")
	}

	ofs.setOwner("/f", 1000, 1000)
	reply, err = e.h.handleWrite(bytes.NewReader(faWriteRequest(fh, 0, []byte("x"))), &RPCReply{}, e.auth)
	if err != nil {
		t.Fatal(err)
	}
	if st := readStatusFromReply(reply); st != NFS_OK {
		t.Errorf("WRITE by the owner to a 0444 file: status %d, want NFS_OK", st)
	}
}
