package absnfs

import (
	"bytes"
	"errors"
	"os"
	"path"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRenameIntoOwnSubtreeIsInvalid: moving a directory inside itself is
// EINVAL (POSIX, RFC 1813), whether or not the target exists. memfs would
// otherwise detach and lose the whole subtree.
func TestRenameIntoOwnSubtreeIsInvalid(t *testing.T) {
	for _, target := range []string{"empty", "absent"} {
		t.Run(target, func(t *testing.T) {
			e := newFaEnv(t, ExportOptions{})
			if err := e.fs.Mkdir("/a", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := e.fs.Mkdir("/a/empty", 0o755); err != nil {
				t.Fatal(err)
			}
			f, err := e.fs.Create("/a/keep.txt")
			if err != nil {
				t.Fatal(err)
			}
			f.Close()
			root, err := e.nfs.Lookup("/")
			if err != nil {
				t.Fatal(err)
			}
			a, err := e.nfs.Lookup("/a")
			if err != nil {
				t.Fatal(err)
			}

			err = e.nfs.Rename(root, "a", a, target)
			if !errors.Is(err, syscall.EINVAL) || mapError(err) != NFSERR_INVAL {
				t.Fatalf("Rename(/a, /a/%s) = %v, want EINVAL", target, err)
			}
			for _, p := range []string{"/a", "/a/empty", "/a/keep.txt"} {
				if _, err := e.fs.Lstat(p); err != nil {
					t.Errorf("%s lost: %v", p, err)
				}
			}

			// The same through the RENAME procedure.
			body := buildRenameRequest(e.handle(t, "/"), "a", e.handle(t, "/a"), target)
			if st := faCall(t, e, wrap(e.handler.handleRename), body); st != NFSERR_INVAL {
				t.Errorf("RENAME: status %d, want NFSERR_INVAL", st)
			}
		})
	}
}

func TestIsWithin(t *testing.T) {
	cases := []struct {
		p, dir string
		want   bool
	}{
		{"/a/b", "/a", true},
		{"/a/b/c", "/a", true},
		{"/a", "/a", false},
		{"/ab", "/a", false},
		{"/b", "/a", false},
		{"/a", "/", true},
	}
	for _, c := range cases {
		if got := isWithin(c.p, c.dir); got != c.want {
			t.Errorf("isWithin(%q, %q) = %v, want %v", c.p, c.dir, got, c.want)
		}
	}
}

// TestUncheckedCreateKeepsExistingOwnerAndMode: an UNCHECKED CREATE of an
// existing file keeps its owner and mode and sets only the size its sattr3
// asks for, as Linux knfsd does: without one the file keeps its content,
// with size 0 it is truncated. The request's other attributes apply only to
// a file it creates.
func TestUncheckedCreateKeepsExistingOwnerAndMode(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	if err := e.fs.Chown("/dir/file.txt", 1234, 5678); err != nil {
		t.Fatal(err)
	}
	if err := e.fs.Chmod("/dir/file.txt", 0o600); err != nil {
		t.Fatal(err)
	}
	e.auth.EffectiveUID, e.auth.EffectiveGID = 501, 20
	mode := uint32(0o644)
	body := faDirOp(e.handle(t, "/dir"), "file.txt", faU32(0), func(b *bytes.Buffer) { faSattr(b, &mode) })
	if st := faCall(t, e, wrap(e.handler.handleCreate), body); st != NFS_OK {
		t.Fatalf("status %d", st)
	}
	if uid, gid := memfsOwner(t, e.fs, "/dir/file.txt"); uid != 1234 || gid != 5678 {
		t.Errorf("owner after UNCHECKED CREATE = %d:%d, want 1234:5678", uid, gid)
	}
	info, err := e.fs.Lstat("/dir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode after UNCHECKED CREATE = %o, want 600", info.Mode().Perm())
	}
	if info.Size() == 0 {
		t.Error("UNCHECKED CREATE without a size truncated the existing file")
	}
	sizeZero := func(b *bytes.Buffer) {
		b.Write(encodeSattr3(false, 0, false, 0, false, 0, true, 0, 0, 0, 0, 0, 0, 0))
	}
	body = faDirOp(e.handle(t, "/dir"), "file.txt", faU32(0), sizeZero)
	if st := faCall(t, e, wrap(e.handler.handleCreate), body); st != NFS_OK {
		t.Fatalf("status %d", st)
	}
	if info, err = e.fs.Lstat("/dir/file.txt"); err != nil || info.Size() != 0 {
		t.Errorf("size after UNCHECKED CREATE with size 0 = %d (%v), want 0", info.Size(), err)
	}
	if uid, gid := memfsOwner(t, e.fs, "/dir/file.txt"); uid != 1234 || gid != 5678 {
		t.Errorf("owner after truncating CREATE = %d:%d, want 1234:5678", uid, gid)
	}

	// A new name does get the requested owner and mode.
	body = faDirOp(e.handle(t, "/dir"), "new.txt", faU32(0), func(b *bytes.Buffer) { faSattr(b, &mode) })
	if st := faCall(t, e, wrap(e.handler.handleCreate), body); st != NFS_OK {
		t.Fatalf("status %d", st)
	}
	if uid, gid := memfsOwner(t, e.fs, "/dir/new.txt"); uid != 501 || gid != 20 {
		t.Errorf("owner of new file = %d:%d, want 501:20", uid, gid)
	}
}

// TestUncheckedCreateNeverRemovesExistingFile: when the existing file cannot
// be reopened, CREATE fails without deleting it (only a file the call made
// is cleaned up).
func TestUncheckedCreateNeverRemovesExistingFile(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	dir, err := e.nfs.Lookup("/dir")
	if err != nil {
		t.Fatal(err)
	}
	hook := &faHookFS{SymlinkFileSystem: e.fs, chmodErr: []error{syscall.EIO}}
	e.nfs.fs = hook
	if _, err := e.nfs.Create(dir, "file.txt", &NFSAttrs{Mode: 0o644}); err != nil {
		t.Fatalf("Create of an existing file: %v", err)
	}
	if len(hook.chmodErr) != 1 {
		t.Error("Create chmodded an existing file")
	}
	if _, err := e.fs.Lstat("/dir/file.txt"); err != nil {
		t.Errorf("existing file removed: %v", err)
	}

	// A directory in the way is an error, and the directory survives.
	if _, err := e.nfs.Create(dir, "sub", &NFSAttrs{Mode: 0o644}); err == nil {
		t.Error("Create over a directory succeeded")
	}
	if info, err := e.fs.Lstat("/dir/sub"); err != nil || !info.IsDir() {
		t.Errorf("directory damaged: %v", err)
	}

	// A new file whose mode cannot be set is removed again.
	if _, err := e.nfs.Create(dir, "fresh.txt", &NFSAttrs{Mode: 0o644}); err == nil {
		t.Error("Create succeeded although chmod failed")
	}
	if _, err := e.fs.Lstat("/dir/fresh.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("half-created file left behind: %v", err)
	}
}

// TestPublicSetAttrRefreshesNodeAttrs: SetAttr stores what the filesystem
// records, not the caller's NFSAttrs, so a request without FileId or owner
// does not wipe them from the node.
func TestPublicSetAttrRefreshesNodeAttrs(t *testing.T) {
	e := newFaEnv(t, ExportOptions{DefaultOwner: &FileOwner{UID: 4242, GID: 4343}})
	node, err := e.nfs.Lookup("/dir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.nfs.SetAttr(node, &NFSAttrs{Mode: 0o600}); err != nil {
		t.Fatal(err)
	}
	node.mu.RLock()
	got := *node.attrs
	node.mu.RUnlock()
	if got.FileId != fileID("/dir/file.txt") {
		t.Errorf("FileId = %d, want %d", got.FileId, fileID("/dir/file.txt"))
	}
	if got.Uid != 4242 || got.Gid != 4343 {
		t.Errorf("owner = %d:%d, want the export owner 4242:4343", got.Uid, got.Gid)
	}
	if got.Mode.Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", got.Mode.Perm())
	}
}

// TestSetattrOnDanglingSymlink: SETATTR describes the symlink itself, so it
// works on a link whose target does not exist.
func TestSetattrOnDanglingSymlink(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	if err := e.fs.Symlink("nowhere", "/dir/dangling"); err != nil {
		t.Fatal(err)
	}
	node, err := e.nfs.Lookup("/dir/dangling")
	if err != nil {
		t.Fatal(err)
	}
	same := *node.attrs
	if err := e.nfs.SetAttr(node, &same); err != nil {
		t.Fatalf("SetAttr on a dangling symlink: %v", err)
	}

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, e.handle(t, "/dir/dangling"))
	faSattr(&buf, nil)
	buf.Write(make([]byte, 4)) // no guard
	r, err := e.handler.handleSetattr(bytes.NewReader(buf.Bytes()), &RPCReply{}, e.auth)
	if err != nil {
		t.Fatal(err)
	}
	f := newFaReader(t, r)
	if st := f.status(); st != NFS_OK {
		t.Fatalf("SETATTR on a dangling symlink: status %d, want NFS_OK", st)
	}
	a := f.wcc()
	if a.Type != NF3LNK {
		t.Errorf("SETATTR reply type %d, want NF3LNK", a.Type)
	}
	e.checkAttr(t, "SETATTR dangling", "/dir/dangling", a)
}

// TestWriteRefreshesNodeAttrs: after a WRITE the node holds the attributes
// the filesystem now records (size, mtime, file id, owner), even if it held
// none of them before.
func TestWriteRefreshesNodeAttrs(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	node, err := e.nfs.Lookup("/dir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	node.attrs = &NFSAttrs{Mode: 0o644}
	before := time.Now().Add(-time.Second)
	if _, err := e.nfs.Write(node, 100, []byte("grown")); err != nil {
		t.Fatal(err)
	}
	node.mu.RLock()
	got := *node.attrs
	node.mu.RUnlock()
	if got.Size != 105 {
		t.Errorf("Size = %d, want 105", got.Size)
	}
	if got.Mtime().Before(before) {
		t.Errorf("Mtime %v not updated", got.Mtime())
	}
	if got.FileId != fileID("/dir/file.txt") || got.Uid != e.owner.UID || got.Gid != e.owner.GID {
		t.Errorf("attrs = fileid %d owner %d:%d, want %d and %d:%d",
			got.FileId, got.Uid, got.Gid, fileID("/dir/file.txt"), e.owner.UID, e.owner.GID)
	}
}

// TestSetattrUidOnMemfsReportsDefaultOwner pins a documented limitation:
// memfs records a chown on its inode, but exposes no OS owner, so clients
// keep seeing DefaultOwner.
func TestSetattrUidOnMemfsReportsDefaultOwner(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	e.auth.EffectiveUID, e.auth.EffectiveGID = 0, 0
	file := e.handle(t, "/dir/file.txt")
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, file)
	buf.Write(encodeSattr3(false, 0, true, 4321, false, 0, false, 0, 0, 0, 0, 0, 0, 0))
	buf.Write(make([]byte, 4)) // no guard
	r, err := e.handler.handleSetattr(bytes.NewReader(buf.Bytes()), &RPCReply{}, e.auth)
	if err != nil {
		t.Fatal(err)
	}
	f := newFaReader(t, r)
	if st := f.status(); st != NFS_OK {
		t.Fatalf("status %d", st)
	}
	e.checkAttr(t, "SETATTR uid post-op", "/dir/file.txt", f.wcc())
	if uid, _ := memfsOwner(t, e.fs, "/dir/file.txt"); uid != 4321 {
		t.Errorf("memfs inode uid = %d, want 4321", uid)
	}
	node, _ := e.handler.lookupNode(file)
	attrs, err := e.nfs.GetAttr(node)
	if err != nil {
		t.Fatal(err)
	}
	if attrs.Uid != e.owner.UID {
		t.Errorf("later GETATTR uid = %d, want DefaultOwner %d", attrs.Uid, e.owner.UID)
	}
}

// TestUpdateExportOptionsRejectsOwnerChanges: the owner options are fixed at
// New, and changing them is an error rather than silently ignored. The
// result of GetExportOptions, and a nil DefaultOwner, are accepted.
func TestUpdateExportOptionsRejectsOwnerChanges(t *testing.T) {
	e := newFaEnv(t, ExportOptions{DefaultOwner: &FileOwner{UID: 7, GID: 8}})

	if err := e.nfs.UpdateExportOptions(e.nfs.GetExportOptions()); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	opts := e.nfs.GetExportOptions()
	opts.DefaultOwner = nil
	opts.ReadOnly = true
	if err := e.nfs.UpdateExportOptions(opts); err != nil {
		t.Fatalf("nil DefaultOwner: %v", err)
	}
	if !e.nfs.policy.Load().ReadOnly {
		t.Error("update with nil DefaultOwner was not applied")
	}

	opts = e.nfs.GetExportOptions()
	opts.DefaultOwner = &FileOwner{UID: 9, GID: 8}
	opts.TransferSize = 4096
	if err := e.nfs.UpdateExportOptions(opts); err == nil || !strings.Contains(err.Error(), "DefaultOwner") {
		t.Errorf("changing DefaultOwner: %v, want an error", err)
	}
	if e.nfs.tuning.Load().TransferSize == 4096 {
		t.Error("rejected update was partly applied")
	}

	opts = e.nfs.GetExportOptions()
	opts.ForceOwner = true
	if err := e.nfs.UpdateExportOptions(opts); err == nil || !strings.Contains(err.Error(), "ForceOwner") {
		t.Errorf("changing ForceOwner: %v, want an error", err)
	}
	if e.nfs.forceOwner || e.nfs.owner != (FileOwner{UID: 7, GID: 8}) {
		t.Error("owner options changed")
	}
}

// readdirPage reads one READDIR (or READDIRPLUS) page after cookie.
func readdirPage(t *testing.T, e *faEnv, dir []byte, cookie uint64, plus bool) ([]faEntry, bool) {
	t.Helper()
	var r *RPCReply
	var err error
	if plus {
		r, err = e.handler.handleReaddirplus(bytes.NewReader(buildReaddirplusRequest(dir, cookie, 600, 1200)), &RPCReply{}, e.auth)
	} else {
		r, err = e.handler.handleReaddir(bytes.NewReader(buildReaddirRequest(dir, cookie, 600)), &RPCReply{}, e.auth)
	}
	if err != nil {
		t.Fatal(err)
	}
	f := newFaReader(t, r)
	if st := f.status(); st != NFS_OK {
		t.Fatalf("status %d", st)
	}
	f.postOpAttr()
	f.skip(8) // cookieverf
	entries := f.entries(plus)
	return entries, f.u32() == 1
}

// TestReaddirCookiesSurviveRemoval: cookies do not depend on position, so a
// client that removes each page before asking for the next (an incremental
// rm -rf) still sees every entry, and paging without changes returns every
// entry exactly once.
func TestReaddirCookiesSurviveRemoval(t *testing.T) {
	for _, plus := range []bool{false, true} {
		name := "READDIR"
		if plus {
			name = "READDIRPLUS"
		}
		t.Run(name, func(t *testing.T) {
			e := newFaEnv(t, ExportOptions{})
			if err := e.fs.Mkdir("/big", 0o755); err != nil {
				t.Fatal(err)
			}
			const n = 60
			for i := 0; i < n; i++ {
				f, err := e.fs.Create(path.Join("/big", "file-"+itoa(i)))
				if err != nil {
					t.Fatal(err)
				}
				f.Close()
			}
			bigNode, err := e.nfs.Lookup("/big")
			if err != nil {
				t.Fatal(err)
			}
			big := e.handle(t, "/big")

			// Unchanged directory: every entry exactly once, cookies increasing.
			seen := map[string]int{}
			var cookie uint64
			pages := 0
			for eof := false; !eof; {
				var entries []faEntry
				entries, eof = readdirPage(t, e, big, cookie, plus)
				for _, en := range entries {
					if en.Cookie <= cookie {
						t.Fatalf("cookie %d after %d", en.Cookie, cookie)
					}
					seen[en.Name]++
					cookie = en.Cookie
				}
				if pages++; pages > n {
					t.Fatal("listing does not terminate")
				}
			}
			if pages < 2 {
				t.Fatalf("listing fit one page; the test needs several")
			}
			if len(seen) != n {
				t.Errorf("paged listing saw %d names, want %d", len(seen), n)
			}
			for k, c := range seen {
				if c != 1 {
					t.Errorf("%s listed %d times", k, c)
				}
			}

			// Remove each page before reading the next.
			removed := 0
			cookie = 0
			for eof := false; !eof; {
				var entries []faEntry
				entries, eof = readdirPage(t, e, big, cookie, plus)
				for _, en := range entries {
					if err := e.nfs.Remove(bigNode, en.Name); err != nil {
						t.Fatal(err)
					}
					removed++
					cookie = en.Cookie
				}
			}
			if removed != n {
				t.Errorf("removed %d entries page by page, want %d", removed, n)
			}
			left, err := e.nfs.ReadDir(bigNode)
			if err != nil {
				t.Fatal(err)
			}
			if len(left) != 0 {
				t.Errorf("%d entries skipped and left behind", len(left))
			}
		})
	}
}

func TestDirCookie(t *testing.T) {
	for _, p := range []string{"/", "/a", "/dir/file.txt"} {
		c := dirCookie(p)
		if c == 0 || c >= 1<<62+1 {
			t.Errorf("dirCookie(%q) = %d, want 0 < c <= 2^62", p, c)
		}
	}
	nodes := []*NFSNode{
		{path: "/d/b", attrs: &NFSAttrs{}},
		{path: "/d/none"},
		{path: "/d/a", attrs: &NFSAttrs{}},
	}
	all := entriesAfter(nodes, 0)
	if len(all) != 2 {
		t.Fatalf("entriesAfter returned %d entries, want 2 (nil attrs skipped)", len(all))
	}
	if all[0].cookie > all[1].cookie {
		t.Error("entries not in cookie order")
	}
	rest := entriesAfter(nodes, all[0].cookie)
	if len(rest) != 1 || rest[0].name != all[1].name {
		t.Errorf("entriesAfter(first cookie) = %v", rest)
	}
}
