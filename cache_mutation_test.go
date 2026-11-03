package absnfs

import (
	"bytes"
	"os"
	"path"
	"slices"
	"testing"
	"time"
)

// getattr sends GETATTR for fh and returns the status and, when NFS_OK, the
// attributes.
func (e *cacheEnv) getattr(fh []byte) (uint32, faFattr) {
	e.t.Helper()
	var req bytes.Buffer
	xdrEncodeFileHandle(&req, fh)
	reply, err := e.h.handleGetattr(&req, &RPCReply{}, e.auth)
	if err != nil {
		e.t.Fatal(err)
	}
	t, ok := e.t.(*testing.T)
	if !ok {
		e.t.Fatal("getattr needs a *testing.T")
	}
	r := newFaReader(t, reply)
	status := r.status()
	if status != NFS_OK {
		return status, faFattr{}
	}
	return status, r.fattr()
}

// setattrArgs encodes SETATTR3args setting mode, with a ctime guard when
// guard is not nil.
func setattrArgs(fh []byte, mode uint32, guard *time.Time) *bytes.Buffer {
	var b bytes.Buffer
	xdrEncodeFileHandle(&b, fh)
	faSattr(&b, &mode)
	if guard == nil {
		xdrEncodeUint32(&b, 0)
	} else {
		xdrEncodeUint32(&b, 1)
		xdrEncodeUint32(&b, uint32(guard.Unix()))
		xdrEncodeUint32(&b, uint32(guard.Nanosecond()))
	}
	return &b
}

func (e *cacheEnv) mode(p string) os.FileMode {
	e.t.Helper()
	info, err := e.fs.SymlinkFileSystem.Lstat(p)
	if err != nil {
		e.t.Fatal(err)
	}
	return info.Mode().Perm()
}

// TestSetattrAppliesToCurrentAttrs: SETATTR compares the request with what
// the filesystem holds, not with cached attributes. A mode changed behind
// the server's back since GETATTR cached the file must still be set.
func TestSetattrAppliesToCurrentAttrs(t *testing.T) {
	e := newCacheEnv(t, ExportOptions{AttrCacheTimeout: time.Hour})
	e.writeFile("/f", "x")
	fh := e.handle("/f")
	if st, a := e.getattr(fh); st != NFS_OK || a.Mode&0o777 != 0o644 {
		t.Fatalf("GETATTR: status %d mode %o", st, a.Mode)
	}
	if err := e.fs.SymlinkFileSystem.Chmod("/f", 0o600); err != nil {
		t.Fatal(err)
	}

	reply, err := e.h.handleSetattr(setattrArgs(fh, 0o644, nil), &RPCReply{}, e.auth)
	if err != nil {
		t.Fatal(err)
	}
	if st := readStatusFromReply(reply); st != NFS_OK {
		t.Fatalf("SETATTR status %d", st)
	}
	if got := e.mode("/f"); got != 0o644 {
		t.Errorf("SETATTR mode=0644 replied NFS_OK but the file is %o", got)
	}
}

// TestSetattrGuardChecksCurrentCtime: a guarded SETATTR is refused when the
// file changed since the client's ctime, even if the cache still holds the
// old one (RFC 1813 3.3.2).
func TestSetattrGuardChecksCurrentCtime(t *testing.T) {
	e := newCacheEnv(t, ExportOptions{AttrCacheTimeout: time.Hour})
	e.writeFile("/f", "x")
	t0 := time.Unix(1_700_000_000, 0)
	t1 := t0.Add(time.Hour)
	if err := e.fs.SymlinkFileSystem.Chtimes("/f", t0, t0); err != nil {
		t.Fatal(err)
	}
	fh := e.handle("/f")
	if st, _ := e.getattr(fh); st != NFS_OK {
		t.Fatalf("GETATTR status %d", st)
	}
	if err := e.fs.SymlinkFileSystem.Chtimes("/f", t1, t1); err != nil {
		t.Fatal(err)
	}

	reply, err := e.h.handleSetattr(setattrArgs(fh, 0o600, &t0), &RPCReply{}, e.auth)
	if err != nil {
		t.Fatal(err)
	}
	if st := readStatusFromReply(reply); st != NFSERR_NOT_SYNC {
		t.Errorf("guarded SETATTR with an outdated ctime: status %d, want NFSERR_NOT_SYNC", st)
	}
	if got := e.mode("/f"); got != 0o644 {
		t.Errorf("refused SETATTR changed the mode to %o", got)
	}
}

// TestNodeMutatorsInvalidateCache: changes made through the NFSNode a
// Lookup returned are seen by the next GetAttr.
func TestNodeMutatorsInvalidateCache(t *testing.T) {
	e := newCacheEnv(t, ExportOptions{AttrCacheTimeout: time.Hour})
	e.writeFile("/f", "")
	node := e.lookup("/f")
	getattr := func() *NFSAttrs {
		t.Helper()
		attrs, err := e.nfs.GetAttr(node)
		if err != nil {
			t.Fatal(err)
		}
		return attrs
	}
	getattr()

	if _, err := node.WriteAt([]byte("hello world"), 0); err != nil {
		t.Fatal(err)
	}
	if got := getattr().Size; got != 11 {
		t.Errorf("after WriteAt: size %d, want 11", got)
	}
	if err := node.Chmod(0o600); err != nil {
		t.Fatal(err)
	}
	if got := getattr().Mode.Perm(); got != 0o600 {
		t.Errorf("after Chmod: mode %o, want 600", got)
	}
	if err := node.Truncate(2); err != nil {
		t.Fatal(err)
	}
	if got := getattr().Size; got != 2 {
		t.Errorf("after Truncate: size %d, want 2", got)
	}
	if _, err := node.WriteString("abc"); err != nil {
		t.Fatal(err)
	}
	if got := getattr().Size; got != 3 {
		t.Errorf("after WriteString: size %d, want 3", got)
	}
	when := time.Unix(1_700_000_000, 0)
	if err := node.Chtimes(when, when); err != nil {
		t.Fatal(err)
	}
	if got := getattr().Mtime(); !got.Equal(when) {
		t.Errorf("after Chtimes: mtime %v, want %v", got, when)
	}
	if err := node.Chown(os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.nfs.attrCache.Get("/f"); ok {
		t.Errorf("after Chown the attributes are still cached")
	}
}

// TestHandlesFollowRename: a client's handles keep reaching the objects they
// named after a RENAME of the object or of a directory above it, and a name
// made again later gets a new handle. The handle of an object a RENAME
// replaced reports NFS3ERR_STALE.
func TestHandlesFollowRename(t *testing.T) {
	e := newCacheEnv(t, ExportOptions{AttrCacheTimeout: time.Hour})
	e.mkdir("/a")
	e.writeFile("/a/f", "hello")
	fh, ah := e.handle("/a/f"), e.handle("/a")
	root := e.lookup("/")

	if err := e.nfs.Rename(root, "a", root, "b"); err != nil {
		t.Fatal(err)
	}
	if st, a := e.getattr(fh); st != NFS_OK || a.Size != 5 {
		t.Errorf("GETATTR of /a/f's handle after renaming /a to /b: status %d size %d, want NFS_OK and 5", st, a.Size)
	}
	if n, _ := e.h.lookupNode(ah); n == nil || n.path != "/b" {
		t.Errorf("/a's handle names %v after the rename, want /b", n)
	}
	e.mkdir("/a")
	e.writeFile("/a/f", "other")
	if got := e.handle("/a/f"); bytes.Equal(got, fh) {
		t.Errorf("a new /a/f got handle %x, which still belongs to /b/f", got)
	}

	e.writeFile("/b/t", "target")
	th := e.handle("/b/t")
	b := e.lookup("/b")
	if err := e.nfs.Rename(b, "f", b, "t"); err != nil {
		t.Fatal(err)
	}
	if st, a := e.getattr(fh); st != NFS_OK || a.Size != 5 || a.FileId != fileID("/b/t") {
		t.Errorf("GETATTR of the moved file's handle: status %d size %d fileid %d, want NFS_OK, 5, %d", st, a.Size, a.FileId, fileID("/b/t"))
	}
	if st, _ := e.getattr(th); st != NFSERR_STALE {
		t.Errorf("GETATTR of the replaced /b/t's handle: status %d, want NFSERR_STALE", st)
	}
	e.writeFile("/b/new", "x")
	if got := e.handle("/b/new"); bytes.Equal(got, th) {
		t.Errorf("the replaced file's handle %x was handed out again", th)
	}
}

// TestRemoveRetiresHandle: the handle of a removed object reports
// NFS3ERR_STALE, and an object made later under the same name gets a new one.
func TestRemoveRetiresHandle(t *testing.T) {
	e := newCacheEnv(t, ExportOptions{AttrCacheTimeout: time.Hour})
	e.mkdir("/d")
	e.mkdir("/d/sub")
	e.writeFile("/d/f", "x")
	fh, sh, dh := e.handle("/d/f"), e.handle("/d/sub"), e.handle("/d")

	if err := e.nfs.Remove(e.lookup("/d"), "f"); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.getattr(fh); st != NFSERR_STALE {
		t.Errorf("GETATTR of a removed file's handle: status %d, want NFSERR_STALE", st)
	}
	e.writeFile("/d/f", "y")
	if got := e.handle("/d/f"); bytes.Equal(got, fh) {
		t.Errorf("a new /d/f got the removed file's handle %x", got)
	}

	reply, err := e.h.handleRmdir(bytes.NewReader(faDirOp(dh, "sub")), &RPCReply{}, e.auth)
	if err != nil {
		t.Fatal(err)
	}
	if st := readStatusFromReply(reply); st != NFS_OK {
		t.Fatalf("RMDIR status %d", st)
	}
	if st, _ := e.getattr(sh); st != NFSERR_STALE {
		t.Errorf("GETATTR of a removed directory's handle: status %d, want NFSERR_STALE", st)
	}
}

// TestGetAttrRacingChangeLeavesNoStaleEntry: a GETATTR that read the
// attributes just before a WRITE or SETATTR must not cache them after it.
// osfs, because memfs's FileInfo is a live view of the file.
func TestGetAttrRacingChangeLeavesNoStaleEntry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(e *cacheEnv, node *NFSNode)
		check  func(t *testing.T, attrs *NFSAttrs)
	}{
		{"WRITE", func(e *cacheEnv, node *NFSNode) {
			if _, err := e.nfs.Write(node, 5, []byte(" world")); err != nil {
				e.t.Errorf("Write: %v", err)
			}
		}, func(t *testing.T, attrs *NFSAttrs) {
			if attrs.Size != 11 {
				t.Errorf("GETATTR after WRITE reports size %d, want 11", attrs.Size)
			}
		}},
		{"SETATTR", func(e *cacheEnv, node *NFSNode) {
			want := *node.attrs
			want.Mode = 0o444 // read-only: a mode Windows can represent
			if err := e.nfs.SetAttr(node, &want); err != nil {
				e.t.Errorf("SetAttr: %v", err)
			}
		}, func(t *testing.T, attrs *NFSAttrs) {
			if attrs.Mode.Perm() != 0o444 {
				t.Errorf("GETATTR after SETATTR reports mode %o, want 444", attrs.Mode.Perm())
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, base := newOSCacheEnv(t, ExportOptions{AttrCacheTimeout: time.Hour})
			p := path.Join(base, "f")
			e.writeFile(p, "hello")
			// Writable again before the directory is removed (Windows).
			t.Cleanup(func() { e.fs.SymlinkFileSystem.Chmod(p, 0o644) })
			node := e.lookup(p)
			e.nfs.attrCache.Invalidate(p)

			e.fs.afterNextLstat(p, func() { tc.change(e, node) })
			e.nfs.GetAttr(node) // raced: may report the old attributes
			if !e.fs.hookRan() {
				t.Fatal("the interleaving did not happen")
			}
			attrs, err := e.nfs.GetAttr(node)
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, attrs)
		})
	}
}

// TestReadDirRacingChangeLeavesNoStaleListing: a READDIR that read the
// listing just before a CREATE or REMOVE in the directory must not cache it
// after it.
func TestReadDirRacingChangeLeavesNoStaleListing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(e *cacheEnv, dir *NFSNode) error
		want   []string
	}{
		{"CREATE", func(e *cacheEnv, dir *NFSNode) error {
			_, err := e.nfs.Create(dir, "g", &NFSAttrs{Mode: 0o644})
			return err
		}, []string{"f", "g"}},
		{"REMOVE", func(e *cacheEnv, dir *NFSNode) error {
			return e.nfs.Remove(dir, "f")
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, base := newOSCacheEnv(t, ExportOptions{EnableDirCache: true, DirCacheTimeout: time.Hour})
			d := path.Join(base, "d")
			e.mkdir(d)
			e.writeFile(path.Join(d, "f"), "x")
			dir := e.lookup(d)

			e.fs.afterNextReaddir(d, func() {
				if err := tc.change(e, dir); err != nil {
					t.Errorf("%s: %v", tc.name, err)
				}
			})
			e.nfs.ReadDir(dir) // raced: may list the old entries
			if !e.fs.hookRan() {
				t.Fatal("the interleaving did not happen")
			}
			if got := e.names(dir); !slices.Equal(got, tc.want) {
				t.Errorf("READDIR after %s lists %v, want %v", tc.name, got, tc.want)
			}
			// READDIR drops names it cannot look up, which would hide a
			// stale cached listing after REMOVE: check the cache itself.
			cached, _ := e.nfs.dirCache.Get(d)
			var got []string
			for _, c := range cached {
				got = append(got, c.Name())
			}
			slices.Sort(got)
			if !slices.Equal(got, tc.want) {
				t.Errorf("after %s the cached listing is %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestReadDirPlusRacingWriteLeavesNoStaleEntry: READDIRPLUS re-reads every
// entry's attributes; one read just before a WRITE must not be cached
// after it.
func TestReadDirPlusRacingWriteLeavesNoStaleEntry(t *testing.T) {
	e, base := newOSCacheEnv(t, ExportOptions{AttrCacheTimeout: time.Hour})
	d := path.Join(base, "d")
	p := path.Join(d, "f")
	e.mkdir(d)
	e.writeFile(p, "hello")
	dir := e.lookup(d)
	node := e.lookup(p) // cached, so READDIR's own LOOKUP does not stat it

	e.fs.afterNextLstat(p, func() {
		if _, err := e.nfs.Write(node, 5, []byte(" world")); err != nil {
			t.Errorf("Write: %v", err)
		}
	})
	if _, err := e.nfs.ReadDirPlus(dir); err != nil {
		t.Fatal(err)
	}
	if !e.fs.hookRan() {
		t.Fatal("the interleaving did not happen")
	}
	if attrs, err := e.nfs.GetAttr(node); err != nil || attrs.Size != 11 {
		t.Errorf("GETATTR after WRITE: %+v, %v; want size 11", attrs, err)
	}
}

// TestRenameInvalidatesParents: RENAME within a directory and across
// directories updates both parents' listings and attributes.
func TestRenameInvalidatesParents(t *testing.T) {
	e, base := newOSCacheEnv(t, ExportOptions{
		AttrCacheTimeout: time.Hour,
		EnableDirCache:   true,
		DirCacheTimeout:  time.Hour,
	})
	xp, yp := path.Join(base, "x"), path.Join(base, "y")
	e.mkdir(xp)
	e.mkdir(yp)
	e.writeFile(path.Join(xp, "f"), "data")
	x, y := e.lookup(xp), e.lookup(yp)
	checkDir := func(when string, dir *NFSNode, want []string) {
		t.Helper()
		if got := e.names(dir); !slices.Equal(got, want) {
			t.Errorf("%s: %s lists %v, want %v", when, dir.path, got, want)
		}
		attrs, err := e.nfs.GetAttr(dir)
		if err != nil {
			t.Fatal(err)
		}
		info, err := e.fs.SymlinkFileSystem.Lstat(dir.path)
		if err != nil {
			t.Fatal(err)
		}
		if !attrs.Mtime().Equal(info.ModTime()) {
			t.Errorf("%s: %s GETATTR mtime %v, directory has %v", when, dir.path, attrs.Mtime(), info.ModTime())
		}
	}
	checkDir("before", x, []string{"f"})
	checkDir("before", y, nil)

	time.Sleep(20 * time.Millisecond)
	if err := e.nfs.Rename(x, "f", x, "g"); err != nil {
		t.Fatal(err)
	}
	checkDir("after renaming f to g", x, []string{"g"})

	time.Sleep(20 * time.Millisecond)
	if err := e.nfs.Rename(x, "g", y, "h"); err != nil {
		t.Fatal(err)
	}
	checkDir("after moving g to y/h", x, nil)
	checkDir("after moving g to y/h", y, []string{"h"})
}

// TestDisablingNegativeCachingDropsEntries: once negative caching is turned
// off, misses cached before are no longer served.
func TestDisablingNegativeCachingDropsEntries(t *testing.T) {
	e := newCacheEnv(t, ExportOptions{CacheNegativeLookups: true, NegativeCacheTimeout: time.Hour})
	_, err := e.nfs.Lookup("/x")
	wantNotExist(t, "Lookup before creating /x", err)

	e.nfs.attrCache.ConfigureNegativeCaching(false, 0)
	e.writeFile("/x", "")
	if _, err := e.nfs.Lookup("/x"); err != nil {
		t.Errorf("Lookup after disabling negative caching: %v (stale negative entry)", err)
	}
	if n := e.nfs.attrCache.NegativeStats(); n != 0 {
		t.Errorf("%d negative entries remain", n)
	}
}

// TestFailedMkdirAndSymlinkInvalidateName: MKDIR or SYMLINK reporting that
// the name exists also forgets a cached miss for it, as CREATE does.
func TestFailedMkdirAndSymlinkInvalidateName(t *testing.T) {
	e := newCacheEnv(t, ExportOptions{CacheNegativeLookups: true, NegativeCacheTimeout: time.Hour})
	rh := e.handle("/")
	for _, name := range []string{"x", "l"} {
		_, err := e.nfs.Lookup("/" + name)
		wantNotExist(t, "Lookup before it exists", err)
	}
	e.mkdir("/x")
	if err := e.fs.SymlinkFileSystem.Symlink("x", "/l"); err != nil {
		t.Fatal(err)
	}

	reply, err := e.h.handleMkdir(mkdirArgs(rh, "x"), &RPCReply{}, e.auth)
	if err != nil {
		t.Fatal(err)
	}
	if st := readStatusFromReply(reply); st != NFSERR_EXIST {
		t.Fatalf("MKDIR status %d, want NFSERR_EXIST", st)
	}
	if _, err := e.nfs.Lookup("/x"); err != nil {
		t.Errorf("Lookup after MKDIR reported EXIST: %v", err)
	}

	args := faDirOp(rh, "l", faNoSattr, func(b *bytes.Buffer) { xdrEncodeString(b, "x") })
	reply, err = e.h.handleSymlink(bytes.NewReader(args), &RPCReply{}, e.auth)
	if err != nil {
		t.Fatal(err)
	}
	if st := readStatusFromReply(reply); st != NFSERR_EXIST {
		t.Fatalf("SYMLINK status %d, want NFSERR_EXIST", st)
	}
	if _, err := e.nfs.Lookup("/l"); err != nil {
		t.Errorf("Lookup after SYMLINK reported EXIST: %v", err)
	}
}
