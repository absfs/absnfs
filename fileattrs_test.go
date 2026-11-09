package absnfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/absfs/absfs"
	"github.com/absfs/inode"
	"github.com/absfs/memfs"
)

// memfsOwner returns the owner memfs records for p (memfs keeps chown
// results on the inode, though it is not reported as an OS owner).
func memfsOwner(t *testing.T, fs absfs.SymlinkFileSystem, p string) (uid, gid uint32) {
	t.Helper()
	info, err := fs.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	node, ok := info.Sys().(*inode.Inode)
	if !ok {
		t.Fatalf("%s: Sys() is %T, not *inode.Inode", p, info.Sys())
	}
	return node.Uid, node.Gid
}

// faFattr is a decoded NFSv3 fattr3.
type faFattr struct {
	Type, Mode, Nlink, Uid, Gid uint32
	Size, Used                  uint64
	FileId                      uint64
	MtimeSec                    uint32
}

// faReader decodes the XDR replies the handlers produce.
type faReader struct {
	t *testing.T
	r *bytes.Reader
}

func newFaReader(t *testing.T, reply *RPCReply) *faReader {
	t.Helper()
	data, ok := reply.Data.([]byte)
	if !ok {
		t.Fatalf("reply data is %T", reply.Data)
	}
	return &faReader{t: t, r: bytes.NewReader(data)}
}

func (f *faReader) u32() uint32 {
	f.t.Helper()
	var v uint32
	if err := binary.Read(f.r, binary.BigEndian, &v); err != nil {
		f.t.Fatalf("short reply: %v", err)
	}
	return v
}

func (f *faReader) u64() uint64 {
	f.t.Helper()
	var v uint64
	if err := binary.Read(f.r, binary.BigEndian, &v); err != nil {
		f.t.Fatalf("short reply: %v", err)
	}
	return v
}

func (f *faReader) skip(n int) {
	f.t.Helper()
	if _, err := io.CopyN(io.Discard, f.r, int64(n)); err != nil {
		f.t.Fatalf("short reply: %v", err)
	}
}

func (f *faReader) str() string {
	f.t.Helper()
	n := int(f.u32())
	b := make([]byte, n)
	if _, err := io.ReadFull(f.r, b); err != nil {
		f.t.Fatalf("short reply: %v", err)
	}
	f.skip((4 - n%4) % 4)
	return string(b)
}

func (f *faReader) status() uint32 { return f.u32() }

func (f *faReader) fh() []byte {
	f.t.Helper()
	n := int(f.u32())
	if n == 0 || n > fhMaxSize {
		f.t.Fatalf("handle length %d", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(f.r, b); err != nil {
		f.t.Fatalf("short reply: %v", err)
	}
	f.skip((4 - n%4) % 4)
	return b
}

func (f *faReader) fattr() faFattr {
	var a faFattr
	a.Type, a.Mode, a.Nlink, a.Uid, a.Gid = f.u32(), f.u32(), f.u32(), f.u32(), f.u32()
	a.Size, a.Used = f.u64(), f.u64()
	f.skip(8) // rdev
	f.skip(8) // fsid
	a.FileId = f.u64()
	f.skip(8) // atime
	a.MtimeSec = f.u32()
	f.skip(4 + 8) // mtime nsec, ctime
	return a
}

func (f *faReader) postOpAttr() faFattr {
	f.t.Helper()
	if f.u32() != 1 {
		f.t.Fatal("post_op_attr without attributes")
	}
	return f.fattr()
}

// wcc skips pre_op_attr and returns the post_op_attr of a wcc_data.
func (f *faReader) wcc() faFattr {
	f.t.Helper()
	if f.u32() == 1 {
		f.skip(24)
	}
	return f.postOpAttr()
}

// faEntry is one READDIR or READDIRPLUS entry.
type faEntry struct {
	FileId uint64
	Name   string
	Cookie uint64
	Attr   *faFattr // READDIRPLUS: nil when sent without attributes
	Handle []byte   // READDIRPLUS: nil when sent without a handle
}

func (f *faReader) entries(plus bool) []faEntry {
	var out []faEntry
	for f.u32() == 1 {
		e := faEntry{FileId: f.u64(), Name: f.str(), Cookie: f.u64()}
		if plus {
			if f.u32() == 1 {
				a := f.fattr()
				e.Attr = &a
			}
			if f.u32() == 1 {
				e.Handle = f.fh()
			}
		}
		out = append(out, e)
	}
	return out
}

// faEnv is a memfs export with a handler, seeded with /dir/file.txt,
// /dir/sub, /dir/link -> file.txt and /dir/sublink -> sub.
type faEnv struct {
	fs      *memfs.FileSystem
	nfs     *AbsfsNFS
	handler *NFSProcedureHandler
	auth    *AuthContext
	owner   FileOwner
}

func newFaEnv(t *testing.T, opts ExportOptions) *faEnv {
	t.Helper()
	mfs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	if err := mfs.Mkdir("/dir", 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := mfs.Create("/dir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("hello"))
	f.Close()
	if err := mfs.Mkdir("/dir/sub", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := mfs.Symlink("file.txt", "/dir/link"); err != nil {
		t.Fatal(err)
	}
	if err := mfs.Symlink("sub", "/dir/sublink"); err != nil {
		t.Fatal(err)
	}
	nfs, err := New(mfs, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nfs.Close() })
	owner := nfs.owner
	srv := &Server{handler: nfs, options: ServerOptions{}}
	return &faEnv{
		fs:      mfs,
		nfs:     nfs,
		handler: &NFSProcedureHandler{server: srv},
		auth: &AuthContext{
			ClientIP:     "127.0.0.1",
			ClientPort:   1023,
			Credential:   &RPCCredential{Flavor: AUTH_SYS},
			EffectiveUID: owner.UID,
			EffectiveGID: owner.GID,
		},
		owner: owner,
	}
}

func (e *faEnv) handle(t *testing.T, p string) []byte {
	t.Helper()
	node, err := e.nfs.Lookup(p)
	if err != nil {
		t.Fatal(err)
	}
	return e.nfs.fileMap.issue(node)
}

func faSattr(buf *bytes.Buffer, mode *uint32) {
	if mode != nil {
		binary.Write(buf, binary.BigEndian, uint32(1))
		binary.Write(buf, binary.BigEndian, *mode)
	} else {
		binary.Write(buf, binary.BigEndian, uint32(0))
	}
	for i := 0; i < 5; i++ { // uid, gid, size, atime, mtime: don't set
		binary.Write(buf, binary.BigEndian, uint32(0))
	}
}

// checkAttr asserts that a reply's attributes describe p as the export
// reports it: its path-derived file id and the export owner.
func (e *faEnv) checkAttr(t *testing.T, where, p string, a faFattr) {
	t.Helper()
	if a.FileId == 0 || a.FileId != fileID(p) {
		t.Errorf("%s: fileid of %s = %d, want %d", where, p, a.FileId, fileID(p))
	}
	if a.Uid != e.owner.UID || a.Gid != e.owner.GID {
		t.Errorf("%s: owner of %s = %d:%d, want %d:%d", where, p, a.Uid, a.Gid, e.owner.UID, e.owner.GID)
	}
}

func TestFileIDIsStableAndNeverZero(t *testing.T) {
	if got := nonZeroID(0); got != 1 {
		t.Errorf("nonZeroID(0) = %d, want 1", got)
	}
	if got := nonZeroID(42); got != 42 {
		t.Errorf("nonZeroID(42) = %d, want 42", got)
	}
	for _, p := range []string{"/", "/a", "/a/b", "/dir/file.txt"} {
		if fileID(p) == 0 {
			t.Errorf("fileID(%q) = 0", p)
		}
	}
	if fileID("/a/b") != fileID("/a/b/") || fileID("/a/b") != fileID("/a/./c/../b") {
		t.Error("fileID differs for spellings of the same path")
	}
	if fileID("/a") == fileID("/b") {
		t.Error("fileID collides for /a and /b")
	}
}

func TestProcessOwner(t *testing.T) {
	if idOrZero(-1) != 0 {
		t.Error("idOrZero(-1) should be 0 (os.Getuid on Windows)")
	}
	if idOrZero(501) != 501 {
		t.Error("idOrZero(501) should be 501")
	}
	got := processOwner()
	if uid := os.Getuid(); uid >= 0 && got.UID != uint32(uid) {
		t.Errorf("processOwner().UID = %d, want %d", got.UID, uid)
	}
	if gid := os.Getgid(); gid >= 0 && got.GID != uint32(gid) {
		t.Errorf("processOwner().GID = %d, want %d", got.GID, gid)
	}
}

func TestOwnerOptions(t *testing.T) {
	t.Run("default is the serving user", func(t *testing.T) {
		e := newFaEnv(t, ExportOptions{})
		if e.owner != processOwner() {
			t.Fatalf("owner = %+v, want %+v", e.owner, processOwner())
		}
		attrs, err := e.nfs.GetAttr(e.nfs.root)
		if err != nil {
			t.Fatal(err)
		}
		if attrs.Uid != e.owner.UID || attrs.Gid != e.owner.GID {
			t.Errorf("root owner = %d:%d, want %+v", attrs.Uid, attrs.Gid, e.owner)
		}
		opts := e.nfs.GetExportOptions()
		if opts.DefaultOwner == nil || *opts.DefaultOwner != processOwner() || opts.ForceOwner {
			t.Errorf("GetExportOptions owner = %v force=%v", opts.DefaultOwner, opts.ForceOwner)
		}
	})

	t.Run("configured owner", func(t *testing.T) {
		want := FileOwner{UID: 1234, GID: 5678}
		e := newFaEnv(t, ExportOptions{DefaultOwner: &want, ForceOwner: true})
		node, err := e.nfs.Lookup("/dir/file.txt")
		if err != nil {
			t.Fatal(err)
		}
		if node.attrs.Uid != 1234 || node.attrs.Gid != 5678 {
			t.Errorf("owner = %d:%d, want 1234:5678", node.attrs.Uid, node.attrs.Gid)
		}
		opts := e.nfs.GetExportOptions()
		if opts.DefaultOwner == nil || *opts.DefaultOwner != want || !opts.ForceOwner {
			t.Errorf("GetExportOptions owner = %v force=%v", opts.DefaultOwner, opts.ForceOwner)
		}

		// The owner is fixed at New: an update that keeps it (nil
		// DefaultOwner means unchanged) applies, one that drops ForceOwner
		// is refused.
		if err := e.nfs.UpdateExportOptions(ExportOptions{ForceOwner: true}); err != nil {
			t.Fatal(err)
		}
		if err := e.nfs.UpdateExportOptions(ExportOptions{}); err == nil {
			t.Error("UpdateExportOptions dropped ForceOwner without an error")
		}
		if e.nfs.owner != want || !e.nfs.forceOwner {
			t.Errorf("UpdateExportOptions changed the owner to %+v force=%v", e.nfs.owner, e.nfs.forceOwner)
		}
	})
}

func TestRootAttrsHaveFileIdAndOwner(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	root := e.nfs.root
	if root.attrs.FileId != fileID("/") || root.attrs.FileId == 0 {
		t.Errorf("root fileid = %d, want %d", root.attrs.FileId, fileID("/"))
	}
	if root.attrs.Uid != e.owner.UID || root.attrs.Gid != e.owner.GID {
		t.Errorf("root owner = %d:%d, want %+v", root.attrs.Uid, root.attrs.Gid, e.owner)
	}
	if !root.attrs.Mode.IsDir() || !root.attrs.IsValid() {
		t.Errorf("root attrs = %+v", root.attrs)
	}
}

func TestNewFailsWhenRootCannotBeStatted(t *testing.T) {
	mfs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	fs := &faHookFS{SymlinkFileSystem: mfs, lstatErr: map[string]error{"/": os.ErrPermission}}
	if _, err := New(fs, ExportOptions{}); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("New = %v, want a permission error", err)
	}
}

// TestFileIdConsistentAcrossProcedures checks that every procedure reports
// the same non-zero file id and the export owner for an object.
func TestFileIdConsistentAcrossProcedures(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	h, auth := e.handler, e.auth
	root := e.handle(t, "/")
	dir := e.handle(t, "/dir")

	t.Run("GETATTR", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, root)
		r, _ := h.handleGetattr(&buf, &RPCReply{}, auth)
		f := newFaReader(t, r)
		if st := f.status(); st != NFS_OK {
			t.Fatalf("status %d", st)
		}
		e.checkAttr(t, "GETATTR", "/", f.fattr())
	})

	t.Run("LOOKUP", func(t *testing.T) {
		r, _ := h.handleLookup(bytes.NewReader(buildLookupRequest(dir, "file.txt")), &RPCReply{}, auth)
		f := newFaReader(t, r)
		if st := f.status(); st != NFS_OK {
			t.Fatalf("status %d", st)
		}
		f.fh()
		e.checkAttr(t, "LOOKUP obj", "/dir/file.txt", f.postOpAttr())
		e.checkAttr(t, "LOOKUP dir", "/dir", f.postOpAttr())
	})

	wantEntries := map[string]uint32{"file.txt": NF3REG, "sub": NF3DIR, "link": NF3LNK, "sublink": NF3LNK}

	t.Run("READDIR", func(t *testing.T) {
		r, _ := h.handleReaddir(bytes.NewReader(buildReaddirRequest(dir, 0, 8192)), &RPCReply{}, auth)
		f := newFaReader(t, r)
		if st := f.status(); st != NFS_OK {
			t.Fatalf("status %d", st)
		}
		e.checkAttr(t, "READDIR dir", "/dir", f.postOpAttr())
		f.skip(8) // cookieverf
		entries := f.entries(false)
		if len(entries) != len(wantEntries) {
			t.Fatalf("READDIR returned %d entries, want %d", len(entries), len(wantEntries))
		}
		var last uint64
		for _, en := range entries {
			if en.FileId != fileID("/dir/"+en.Name) {
				t.Errorf("READDIR %s: fileid %d, want %d", en.Name, en.FileId, fileID("/dir/"+en.Name))
			}
			if en.Cookie != dirCookie("/dir/"+en.Name) || en.Cookie <= last {
				t.Errorf("READDIR %s: cookie %d, want %d, increasing", en.Name, en.Cookie, dirCookie("/dir/"+en.Name))
			}
			last = en.Cookie
		}
	})

	t.Run("READDIRPLUS", func(t *testing.T) {
		// Twice: the second pass runs after LOOKUP cached every entry.
		for pass := 0; pass < 2; pass++ {
			r, _ := h.handleReaddirplus(bytes.NewReader(buildReaddirplusRequest(dir, 0, 8192, 65536)), &RPCReply{}, auth)
			f := newFaReader(t, r)
			if st := f.status(); st != NFS_OK {
				t.Fatalf("status %d", st)
			}
			e.checkAttr(t, "READDIRPLUS dir", "/dir", f.postOpAttr())
			f.skip(8)
			entries := f.entries(true)
			if len(entries) != len(wantEntries) {
				t.Fatalf("READDIRPLUS returned %d entries, want %d", len(entries), len(wantEntries))
			}
			var last uint64
			for _, en := range entries {
				p := "/dir/" + en.Name
				if en.FileId != fileID(p) {
					t.Errorf("READDIRPLUS %s: entry fileid %d, want %d", en.Name, en.FileId, fileID(p))
				}
				if en.Cookie != dirCookie(p) || en.Cookie <= last {
					t.Errorf("READDIRPLUS %s: cookie %d, want %d, increasing", en.Name, en.Cookie, dirCookie(p))
				}
				last = en.Cookie
				e.checkAttr(t, "READDIRPLUS attrs", p, *en.Attr)
				if en.Attr.Type != wantEntries[en.Name] {
					t.Errorf("READDIRPLUS %s: type %d, want %d", en.Name, en.Attr.Type, wantEntries[en.Name])
				}
			}
		}
	})

	t.Run("SETATTR", func(t *testing.T) {
		file := e.handle(t, "/dir/file.txt")
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, file)
		mode := uint32(0o600)
		faSattr(&buf, &mode)
		binary.Write(&buf, binary.BigEndian, uint32(0)) // no guard
		r, _ := h.handleSetattr(&buf, &RPCReply{}, auth)
		f := newFaReader(t, r)
		if st := f.status(); st != NFS_OK {
			t.Fatalf("status %d", st)
		}
		post := f.wcc()
		e.checkAttr(t, "SETATTR", "/dir/file.txt", post)
		if post.Mode != 0o600 {
			t.Errorf("SETATTR mode = %o, want 600", post.Mode)
		}
		node, _ := h.lookupNode(file)
		if node.attrs.FileId != fileID("/dir/file.txt") {
			t.Errorf("SETATTR left node fileid %d", node.attrs.FileId)
		}
	})

	t.Run("WRITE", func(t *testing.T) {
		file := e.handle(t, "/dir/file.txt")
		r, _ := h.handleWrite(bytes.NewReader(faWriteRequest(file, 0, []byte("HELLO"))), &RPCReply{}, auth)
		f := newFaReader(t, r)
		if st := f.status(); st != NFS_OK {
			t.Fatalf("status %d", st)
		}
		e.checkAttr(t, "WRITE", "/dir/file.txt", f.wcc())
		node, _ := h.lookupNode(file)
		if node.attrs.FileId != fileID("/dir/file.txt") || node.attrs.Uid != e.owner.UID {
			t.Errorf("WRITE left node attrs %+v", node.attrs)
		}
	})

	create := func(t *testing.T, proc string, name string) {
		t.Helper()
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, dir)
		xdrEncodeString(&buf, name)
		var r *RPCReply
		switch proc {
		case "CREATE":
			binary.Write(&buf, binary.BigEndian, uint32(0)) // UNCHECKED
			faSattr(&buf, nil)
			r, _ = h.handleCreate(&buf, &RPCReply{}, auth)
		case "MKDIR":
			faSattr(&buf, nil)
			r, _ = h.handleMkdir(&buf, &RPCReply{}, auth)
		case "SYMLINK":
			faSattr(&buf, nil)
			xdrEncodeString(&buf, "file.txt")
			r, _ = h.handleSymlink(&buf, &RPCReply{}, auth)
		}
		f := newFaReader(t, r)
		if st := f.status(); st != NFS_OK {
			t.Fatalf("%s status %d", proc, st)
		}
		if f.u32() != 1 {
			t.Fatalf("%s: no handle", proc)
		}
		f.fh()
		obj := f.postOpAttr()
		e.checkAttr(t, proc, "/dir/"+name, obj)
		e.checkAttr(t, proc+" dir", "/dir", f.wcc())
		if proc == "SYMLINK" && obj.Type != NF3LNK {
			t.Errorf("SYMLINK type = %d, want %d", obj.Type, NF3LNK)
		}

		// A later GETATTR reports the same id.
		attrs, err := e.nfs.GetAttr(&NFSNode{SymlinkFileSystem: e.fs, path: "/dir/" + name})
		if err != nil {
			t.Fatal(err)
		}
		if attrs.FileId != obj.FileId {
			t.Errorf("%s fileid %d, later GETATTR %d", proc, obj.FileId, attrs.FileId)
		}
	}
	t.Run("CREATE", func(t *testing.T) { create(t, "CREATE", "created.txt") })
	t.Run("MKDIR", func(t *testing.T) { create(t, "MKDIR", "made") })
	t.Run("SYMLINK", func(t *testing.T) { create(t, "SYMLINK", "made-link") })
}

func faWriteRequest(handle []byte, offset uint64, data []byte) []byte {
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, handle)
	binary.Write(&buf, binary.BigEndian, offset)
	binary.Write(&buf, binary.BigEndian, uint32(len(data)))
	binary.Write(&buf, binary.BigEndian, uint32(2)) // FILE_SYNC
	binary.Write(&buf, binary.BigEndian, uint32(len(data)))
	buf.Write(data)
	buf.Write(make([]byte, (4-len(data)%4)%4))
	return buf.Bytes()
}

// TestReaddirplusDescribesSymlinksNotTargets is the macOS failure where
// READDIRPLUS followed symlinks and cached the target's attributes, so a
// later LOOKUP of the link reported a regular file.
func TestReaddirplusDescribesSymlinksNotTargets(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	dirNode, err := e.nfs.Lookup("/dir")
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := e.nfs.ReadDirPlus(dirNode)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if strings.HasSuffix(n.path, "link") && n.attrs.Mode&os.ModeSymlink == 0 {
			t.Errorf("ReadDirPlus: %s mode %v, want a symlink", n.path, n.attrs.Mode)
		}
		if n.attrs.FileId != fileID(n.path) {
			t.Errorf("ReadDirPlus: %s fileid %d, want %d", n.path, n.attrs.FileId, fileID(n.path))
		}
	}
	for _, p := range []string{"/dir/link", "/dir/sublink"} {
		node, err := e.nfs.Lookup(p)
		if err != nil {
			t.Fatal(err)
		}
		if node.attrs.Mode&os.ModeSymlink == 0 {
			t.Errorf("Lookup(%s) after READDIRPLUS: mode %v, want a symlink", p, node.attrs.Mode)
		}
	}
}

// TestRmdirRefusesSymlinkToDirectory: RMDIR must describe the entry itself;
// following the link would remove the link as if it were a directory.
func TestRmdirRefusesSymlinkToDirectory(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	dir := e.handle(t, "/dir")
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dir)
	xdrEncodeString(&buf, "sublink")
	r, _ := e.handler.handleRmdir(&buf, &RPCReply{}, e.auth)
	if st := readStatusFromReply(r); st != NFSERR_NOTDIR {
		t.Fatalf("RMDIR on a symlink to a directory = %d, want NFSERR_NOTDIR", st)
	}
	if _, err := e.fs.Lstat("/dir/sublink"); err != nil {
		t.Fatalf("symlink removed: %v", err)
	}
}

// TestSetattrAtimeOnlyKeepsMtime: setting only atime must not move mtime.
func TestSetattrAtimeOnlyKeepsMtime(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	old := time.Unix(1_600_000_000, 0)
	if err := e.fs.Chtimes("/dir/file.txt", old, old); err != nil {
		t.Fatal(err)
	}
	file := e.handle(t, "/dir/file.txt")
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, file)
	for i := 0; i < 4; i++ { // mode, uid, gid, size: don't set
		binary.Write(&buf, binary.BigEndian, uint32(0))
	}
	binary.Write(&buf, binary.BigEndian, uint32(2)) // atime: client time
	binary.Write(&buf, binary.BigEndian, uint32(1_700_000_000))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0)) // mtime: don't set
	binary.Write(&buf, binary.BigEndian, uint32(0)) // no guard
	r, _ := e.handler.handleSetattr(&buf, &RPCReply{}, e.auth)
	if st := readStatusFromReply(r); st != NFS_OK {
		t.Fatalf("status %d", st)
	}
	info, err := e.fs.Lstat("/dir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(old) {
		t.Errorf("mtime = %v, want %v", info.ModTime(), old)
	}
}

func TestSetattrTruncateKeepsFileId(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	file := e.handle(t, "/dir/file.txt")
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, file)
	for i := 0; i < 3; i++ { // mode, uid, gid: don't set
		binary.Write(&buf, binary.BigEndian, uint32(0))
	}
	binary.Write(&buf, binary.BigEndian, uint32(1)) // size
	binary.Write(&buf, binary.BigEndian, uint64(2))
	binary.Write(&buf, binary.BigEndian, uint32(0)) // atime
	binary.Write(&buf, binary.BigEndian, uint32(0)) // mtime
	binary.Write(&buf, binary.BigEndian, uint32(0)) // no guard
	r, _ := e.handler.handleSetattr(&buf, &RPCReply{}, e.auth)
	f := newFaReader(t, r)
	if st := f.status(); st != NFS_OK {
		t.Fatalf("status %d", st)
	}
	post := f.wcc()
	e.checkAttr(t, "SETATTR size", "/dir/file.txt", post)
	if post.Size != 2 {
		t.Errorf("size = %d, want 2", post.Size)
	}
}

func TestSetAttrRefreshFailure(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	node, err := e.nfs.Lookup("/dir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	hook := &faHookFS{SymlinkFileSystem: e.fs, lstatErr: map[string]error{}}
	e.nfs.fs = hook
	attrs := *node.attrs
	attrs.Mode = 0o600
	// The object vanishes between the change and the re-read.
	hook.afterChmod = func() { hook.lstatErr["/dir/file.txt"] = os.ErrNotExist }
	if err := e.nfs.SetAttr(node, &attrs); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("SetAttr = %v, want a not-exist error", err)
	}
}

func TestCreateAppliesOwner(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	dir, err := e.nfs.Lookup("/dir")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.nfs.Create(dir, "owned.txt", &NFSAttrs{Mode: 0o644, Uid: 777, Gid: 888}); err != nil {
		t.Fatal(err)
	}
	if uid, gid := memfsOwner(t, e.fs, "/dir/owned.txt"); uid != 777 || gid != 888 {
		t.Errorf("memfs owner = %d:%d, want 777:888", uid, gid)
	}

	// A failing chown (an unprivileged server) does not fail the create.
	e.nfs.fs = &faHookFS{SymlinkFileSystem: e.fs, chownErr: syscall.EPERM}
	node, err := e.nfs.Create(dir, "unowned.txt", &NFSAttrs{Mode: 0o644, Uid: 777, Gid: 888})
	if err != nil {
		t.Fatalf("Create with a failing chown: %v", err)
	}
	if node.attrs.Uid != e.owner.UID {
		t.Errorf("reported owner = %d, want %d", node.attrs.Uid, e.owner.UID)
	}
}

// faHookFS wraps a filesystem to inject failures.
type faHookFS struct {
	absfs.SymlinkFileSystem
	lstatErr   map[string]error
	chownErr   error
	chmodErr   []error // returned by successive Chmod calls; nil entries pass through
	chtimesErr error
	removeErr  error
	lchownErr  error
	openErr    map[string]error
	afterChmod func()
}

func (f *faHookFS) Lstat(name string) (os.FileInfo, error) {
	if err := f.lstatErr[name]; err != nil {
		return nil, &os.PathError{Op: "lstat", Path: name, Err: err}
	}
	return f.SymlinkFileSystem.Lstat(name)
}

func (f *faHookFS) Chown(name string, uid, gid int) error {
	if f.chownErr != nil {
		return f.chownErr
	}
	return f.SymlinkFileSystem.Chown(name, uid, gid)
}

func (f *faHookFS) Chmod(name string, mode os.FileMode) error {
	if f.afterChmod != nil {
		defer f.afterChmod()
	}
	if len(f.chmodErr) > 0 {
		err := f.chmodErr[0]
		f.chmodErr = f.chmodErr[1:]
		if err != nil {
			return err
		}
	}
	return f.SymlinkFileSystem.Chmod(name, mode)
}

// TestReadDirPlusSkipsVanishedEntry: an entry that disappears between the
// listing and its stat keeps the attributes LOOKUP gave it.
func TestReadDirPlusSkipsVanishedEntry(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	dirNode, err := e.nfs.Lookup("/dir")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.nfs.ReadDir(dirNode); err != nil { // caches every entry
		t.Fatal(err)
	}
	e.nfs.fs = &faHookFS{SymlinkFileSystem: e.fs, lstatErr: map[string]error{"/dir/file.txt": os.ErrNotExist}}
	nodes, err := e.nfs.ReadDirPlus(dirNode)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range nodes {
		if n.path == "/dir/file.txt" {
			found = true
			if n.attrs.FileId != fileID(n.path) {
				t.Errorf("fileid = %d, want %d", n.attrs.FileId, fileID(n.path))
			}
		}
	}
	if !found {
		t.Error("cached entry missing from READDIRPLUS")
	}
}

func TestSetAttrReportsFilesystemFailures(t *testing.T) {
	cases := []struct {
		name  string
		hook  faHookFS
		apply func(a *NFSAttrs)
	}{
		{"chmod", faHookFS{chmodErr: []error{syscall.EIO}}, func(a *NFSAttrs) { a.Mode = 0o600 }},
		{"chown", faHookFS{chownErr: syscall.EIO}, func(a *NFSAttrs) { a.Uid++ }},
		{"chtimes", faHookFS{chtimesErr: syscall.EIO}, func(a *NFSAttrs) { a.SetMtime(time.Unix(1, 0)) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newFaEnv(t, ExportOptions{})
			node, err := e.nfs.Lookup("/dir/file.txt")
			if err != nil {
				t.Fatal(err)
			}
			hook := c.hook
			hook.SymlinkFileSystem = e.fs
			e.nfs.fs = &hook
			attrs := *node.attrs
			c.apply(&attrs)
			if err := e.nfs.SetAttr(node, &attrs); !errors.Is(err, syscall.EIO) {
				t.Fatalf("SetAttr = %v, want EIO", err)
			}
		})
	}
}

func (f *faHookFS) Chtimes(name string, atime, mtime time.Time) error {
	if f.chtimesErr != nil {
		return f.chtimesErr
	}
	return f.SymlinkFileSystem.Chtimes(name, atime, mtime)
}

func (f *faHookFS) Remove(name string) error {
	if f.removeErr != nil {
		return &os.PathError{Op: "remove", Path: name, Err: f.removeErr}
	}
	return f.SymlinkFileSystem.Remove(name)
}

func (f *faHookFS) Lchown(name string, uid, gid int) error {
	if f.lchownErr != nil {
		return f.lchownErr
	}
	return f.SymlinkFileSystem.Lchown(name, uid, gid)
}

func (f *faHookFS) OpenFile(name string, flag int, perm os.FileMode) (absfs.File, error) {
	if err := f.openErr[name]; err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	return f.SymlinkFileSystem.OpenFile(name, flag, perm)
}
