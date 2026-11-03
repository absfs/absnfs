package absnfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/absfs/memfs"
)

func faCall(t *testing.T, e *faEnv, fn func(*bytes.Reader, *RPCReply, *AuthContext) (*RPCReply, error), body []byte) uint32 {
	t.Helper()
	r, err := fn(bytes.NewReader(body), &RPCReply{}, e.auth)
	if err != nil {
		t.Fatalf("handler returned error %v", err)
	}
	return readStatusFromReply(r)
}

func faDirOp(dir []byte, name string, rest ...func(*bytes.Buffer)) []byte {
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, dir)
	xdrEncodeString(&buf, name)
	for _, f := range rest {
		f(&buf)
	}
	return buf.Bytes()
}

func faU32(v uint32) func(*bytes.Buffer) {
	return func(b *bytes.Buffer) { binary.Write(b, binary.BigEndian, v) }
}

func faNoSattr(b *bytes.Buffer) { faSattr(b, nil) }

// withDebugLog turns on the server's debug logging into a buffer.
func (e *faEnv) withDebugLog() *bytes.Buffer {
	var logBuf bytes.Buffer
	e.handler.server.options.Debug = true
	e.handler.server.logger = log.New(&logBuf, "", 0)
	return &logBuf
}

// TestTruncatedRequestsAreGarbage: every strict prefix of a valid request is
// rejected with GARBAGE_ARGS rather than half-decoded.
func TestTruncatedRequestsAreGarbage(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	h := e.handler
	dir := e.handle(t, "/dir")
	file := e.handle(t, "/dir/file.txt")
	link := e.handle(t, "/dir/link")

	fh := func(v []byte) []byte {
		var b bytes.Buffer
		xdrEncodeFileHandle(&b, v)
		return b.Bytes()
	}
	setattr := func() []byte {
		var b bytes.Buffer
		xdrEncodeFileHandle(&b, file)
		faSattr(&b, nil)
		binary.Write(&b, binary.BigEndian, uint32(1)) // guard follows
		binary.Write(&b, binary.BigEndian, uint32(1))
		binary.Write(&b, binary.BigEndian, uint32(2))
		return b.Bytes()
	}()
	read := func() []byte {
		var b bytes.Buffer
		xdrEncodeFileHandle(&b, file)
		binary.Write(&b, binary.BigEndian, uint64(0))
		binary.Write(&b, binary.BigEndian, uint32(4))
		return b.Bytes()
	}()
	var verf [8]byte

	cases := []struct {
		name string
		fn   func(*bytes.Reader, *RPCReply, *AuthContext) (*RPCReply, error)
		body []byte
	}{
		{"GETATTR", wrap(h.handleGetattr), fh(file)},
		{"SETATTR", wrap(h.handleSetattr), setattr},
		{"LOOKUP", wrap(h.handleLookup), buildLookupRequest(dir, "file.txt")},
		{"ACCESS", wrap(h.handleAccess), buildAccessRequest(file, 0x3f)},
		{"READLINK", wrap(h.handleReadlink), fh(link)},
		{"READ", wrap(h.handleRead), read},
		{"WRITE", wrap(h.handleWrite), faWriteRequest(file, 0, []byte("abcd"))},
		{"CREATE unchecked", wrap(h.handleCreate), faDirOp(dir, "c", faU32(0), faNoSattr)},
		{"CREATE exclusive", wrap(h.handleCreate), faDirOp(dir, "c", faU32(2), func(b *bytes.Buffer) { b.Write(verf[:]) })},
		{"MKDIR", wrap(h.handleMkdir), faDirOp(dir, "m", faNoSattr)},
		{"SYMLINK", wrap(h.handleSymlink), faDirOp(dir, "s", faNoSattr, func(b *bytes.Buffer) { xdrEncodeString(b, "file.txt") })},
		{"REMOVE", wrap(h.handleRemove), buildRemoveRequest(dir, "file.txt")},
		{"RMDIR", wrap(h.handleRmdir), buildRemoveRequest(dir, "sub")},
		{"RENAME", wrap(h.handleRename), buildRenameRequest(dir, "file.txt", dir, "renamed")},
		{"READDIR", wrap(h.handleReaddir), buildReaddirRequest(dir, 0, 4096)},
		{"READDIRPLUS", wrap(h.handleReaddirplus), buildReaddirplusRequest(dir, 0, 4096, 8192)},
		{"COMMIT", wrap(h.handleCommit), buildCommitRequest(file, 0, 0)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for n := 0; n < len(c.body); n++ {
				if st := faCall(t, e, c.fn, c.body[:n]); st != GARBAGE_ARGS {
					t.Fatalf("%d of %d bytes: status %d, want GARBAGE_ARGS", n, len(c.body), st)
				}
			}
		})
	}
}

func wrap(fn func(body io.Reader, reply *RPCReply, auth *AuthContext) (*RPCReply, error)) func(*bytes.Reader, *RPCReply, *AuthContext) (*RPCReply, error) {
	return func(r *bytes.Reader, reply *RPCReply, auth *AuthContext) (*RPCReply, error) {
		return fn(r, reply, auth)
	}
}

// TestHandlersReportVanishedDirectory: when the object behind a handle is
// gone, procedures answer with NFS3ERR_NOENT instead of stale success.
func TestHandlersReportVanishedDirectory(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	h := e.handler
	dir := e.handle(t, "/dir")
	file := e.handle(t, "/dir/file.txt")
	link := e.handle(t, "/dir/link")
	e.nfs.fs = &faHookFS{SymlinkFileSystem: e.fs, lstatErr: map[string]error{
		"/dir": os.ErrNotExist, "/dir/file.txt": os.ErrNotExist, "/dir/link": os.ErrNotExist,
	}}
	// The objects vanished behind the server's back, which it sees once the
	// attributes it cached have expired.
	e.nfs.attrCache.Clear()
	read := func() []byte {
		var b bytes.Buffer
		xdrEncodeFileHandle(&b, file)
		binary.Write(&b, binary.BigEndian, uint64(0))
		binary.Write(&b, binary.BigEndian, uint32(4))
		return b.Bytes()
	}()
	var linkFH bytes.Buffer
	xdrEncodeFileHandle(&linkFH, link)

	cases := []struct {
		name string
		fn   func(*bytes.Reader, *RPCReply, *AuthContext) (*RPCReply, error)
		body []byte
	}{
		{"CREATE", wrap(h.handleCreate), faDirOp(dir, "c", faU32(0), faNoSattr)},
		{"MKDIR", wrap(h.handleMkdir), faDirOp(dir, "m", faNoSattr)},
		{"SYMLINK", wrap(h.handleSymlink), faDirOp(dir, "s", faNoSattr, func(b *bytes.Buffer) { xdrEncodeString(b, "x") })},
		{"REMOVE", wrap(h.handleRemove), buildRemoveRequest(dir, "file.txt")},
		{"RMDIR", wrap(h.handleRmdir), buildRemoveRequest(dir, "sub")},
		{"RENAME", wrap(h.handleRename), buildRenameRequest(dir, "file.txt", dir, "renamed")},
		{"READDIR", wrap(h.handleReaddir), buildReaddirRequest(dir, 0, 4096)},
		{"READDIRPLUS", wrap(h.handleReaddirplus), buildReaddirplusRequest(dir, 0, 4096, 8192)},
		{"READ", wrap(h.handleRead), read},
		{"WRITE", wrap(h.handleWrite), faWriteRequest(file, 0, []byte("abcd"))},
		{"READLINK", wrap(h.handleReadlink), linkFH.Bytes()},
	}
	for _, c := range cases {
		if st := faCall(t, e, c.fn, c.body); st != NFSERR_NOENT {
			t.Errorf("%s: status %d, want NFSERR_NOENT", c.name, st)
		}
	}
}

func TestCreateModes(t *testing.T) {
	var verfA, verfB [8]byte
	copy(verfA[:], "verifA!!")
	copy(verfB[:], "verifB!!")
	exclusive := func(dir []byte, name string, verf [8]byte) []byte {
		return faDirOp(dir, name, faU32(2), func(b *bytes.Buffer) { b.Write(verf[:]) })
	}
	content := func(e *faEnv, p string) string {
		f, err := e.fs.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		data, _ := io.ReadAll(f)
		return string(data)
	}

	t.Run("guarded refuses an existing file and leaves it intact", func(t *testing.T) {
		e := newFaEnv(t, ExportOptions{})
		dir := e.handle(t, "/dir")
		r, _ := e.handler.handleCreate(bytes.NewReader(faDirOp(dir, "file.txt", faU32(1), faNoSattr)), &RPCReply{}, e.auth)
		f := newFaReader(t, r)
		if st := f.status(); st != NFSERR_EXIST {
			t.Fatalf("status %d, want NFSERR_EXIST", st)
		}
		e.checkAttr(t, "CREATE guarded failure dir", "/dir", f.wcc())
		if got := content(e, "/dir/file.txt"); got != "hello" {
			t.Errorf("content = %q, want hello", got)
		}
		if st := faCall(t, e, wrap(e.handler.handleCreate), faDirOp(dir, "fresh", faU32(1), faNoSattr)); st != NFS_OK {
			t.Errorf("guarded create of a new name: status %d", st)
		}
	})

	t.Run("exclusive refuses an existing file and leaves it intact", func(t *testing.T) {
		e := newFaEnv(t, ExportOptions{})
		dir := e.handle(t, "/dir")
		if st := faCall(t, e, wrap(e.handler.handleCreate), exclusive(dir, "file.txt", verfA)); st != NFSERR_EXIST {
			t.Fatalf("status %d, want NFSERR_EXIST", st)
		}
		if got := content(e, "/dir/file.txt"); got != "hello" {
			t.Errorf("content = %q, want hello", got)
		}
	})

	t.Run("exclusive retransmission succeeds, another verifier does not", func(t *testing.T) {
		e := newFaEnv(t, ExportOptions{})
		dir := e.handle(t, "/dir")
		r, _ := e.handler.handleCreate(bytes.NewReader(exclusive(dir, "lock", verfA)), &RPCReply{}, e.auth)
		f := newFaReader(t, r)
		if st := f.status(); st != NFS_OK {
			t.Fatalf("status %d", st)
		}
		f.u32()
		first := f.fh()
		info, err := e.fs.Lstat("/dir/lock")
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(verifierTime(verfA)) {
			t.Errorf("mtime = %v, want the verifier %v", info.ModTime(), verifierTime(verfA))
		}

		r, _ = e.handler.handleCreate(bytes.NewReader(exclusive(dir, "lock", verfA)), &RPCReply{}, e.auth)
		f = newFaReader(t, r)
		if st := f.status(); st != NFS_OK {
			t.Fatalf("retransmission: status %d, want NFS_OK", st)
		}
		f.u32()
		if again := f.fh(); !bytes.Equal(again, first) {
			t.Errorf("retransmission handle %x, want %x", again, first)
		}
		e.checkAttr(t, "CREATE retransmission", "/dir/lock", f.postOpAttr())

		if st := faCall(t, e, wrap(e.handler.handleCreate), exclusive(dir, "lock", verfB)); st != NFSERR_EXIST {
			t.Errorf("other verifier: status %d, want NFSERR_EXIST", st)
		}
	})

	t.Run("exclusive create survives a filesystem that cannot store times", func(t *testing.T) {
		e := newFaEnv(t, ExportOptions{})
		dir := e.handle(t, "/dir")
		e.nfs.fs = &faHookFS{SymlinkFileSystem: e.fs, chtimesErr: syscall.EPERM}
		if st := faCall(t, e, wrap(e.handler.handleCreate), exclusive(dir, "lock", verfA)); st != NFS_OK {
			t.Fatalf("status %d, want NFS_OK", st)
		}
	})

	t.Run("unchecked failure returns wcc data", func(t *testing.T) {
		e := newFaEnv(t, ExportOptions{})
		dir := e.handle(t, "/dir")
		e.nfs.fs = &faHookFS{SymlinkFileSystem: e.fs, openErr: map[string]error{"/dir/c": syscall.ENOSPC}}
		r, _ := e.handler.handleCreate(bytes.NewReader(faDirOp(dir, "c", faU32(0), faNoSattr)), &RPCReply{}, e.auth)
		f := newFaReader(t, r)
		if st := f.status(); st != NFSERR_NOSPC {
			t.Fatalf("status %d, want NFSERR_NOSPC", st)
		}
		e.checkAttr(t, "CREATE failure dir", "/dir", f.wcc())
	})
}

func TestVerifierTime(t *testing.T) {
	verf := [8]byte{0, 0, 0, 5, 0xff, 0xff, 0xff, 0xff}
	got := verifierTime(verf)
	if got.Unix() != 5 || got.Nanosecond() != int(uint32(0xffffffff)%1e9) {
		t.Errorf("verifierTime = %v", got)
	}
}

func TestMkdirErrors(t *testing.T) {
	t.Run("existing name", func(t *testing.T) {
		e := newFaEnv(t, ExportOptions{})
		dir := e.handle(t, "/dir")
		r, _ := e.handler.handleMkdir(bytes.NewReader(faDirOp(dir, "sub", faNoSattr)), &RPCReply{}, e.auth)
		f := newFaReader(t, r)
		if st := f.status(); st != NFSERR_EXIST {
			t.Fatalf("status %d, want NFSERR_EXIST", st)
		}
		e.checkAttr(t, "MKDIR failure dir", "/dir", f.wcc())
	})

	t.Run("chown failure is logged, not fatal", func(t *testing.T) {
		e := newFaEnv(t, ExportOptions{})
		logs := e.withDebugLog()
		dir := e.handle(t, "/dir")
		e.nfs.fs = &faHookFS{SymlinkFileSystem: e.fs, chownErr: syscall.EPERM}
		if st := faCall(t, e, wrap(e.handler.handleMkdir), faDirOp(dir, "m", faNoSattr)); st != NFS_OK {
			t.Fatalf("status %d, want NFS_OK", st)
		}
		if !strings.Contains(logs.String(), "MKDIR: Chown failed") {
			t.Errorf("log = %q, want the chown failure", logs.String())
		}
	})

	t.Run("new directory vanishes before lookup", func(t *testing.T) {
		e := newFaEnv(t, ExportOptions{})
		dir := e.handle(t, "/dir")
		e.nfs.fs = &faHookFS{SymlinkFileSystem: e.fs, lstatErr: map[string]error{"/dir/m": os.ErrNotExist}}
		if st := faCall(t, e, wrap(e.handler.handleMkdir), faDirOp(dir, "m", faNoSattr)); st != NFSERR_NOENT {
			t.Fatalf("status %d, want NFSERR_NOENT", st)
		}
	})
}

func TestSymlinkErrors(t *testing.T) {
	target := func(b *bytes.Buffer) { xdrEncodeString(b, "file.txt") }

	t.Run("existing name", func(t *testing.T) {
		e := newFaEnv(t, ExportOptions{})
		dir := e.handle(t, "/dir")
		r, _ := e.handler.handleSymlink(bytes.NewReader(faDirOp(dir, "link", faNoSattr, target)), &RPCReply{}, e.auth)
		f := newFaReader(t, r)
		if st := f.status(); st != NFSERR_EXIST {
			t.Fatalf("status %d, want NFSERR_EXIST", st)
		}
		e.checkAttr(t, "SYMLINK failure dir", "/dir", f.wcc())
	})

	t.Run("lchown failure is logged, not fatal", func(t *testing.T) {
		e := newFaEnv(t, ExportOptions{})
		logs := e.withDebugLog()
		dir := e.handle(t, "/dir")
		e.nfs.fs = &faHookFS{SymlinkFileSystem: e.fs, lchownErr: syscall.EPERM}
		if st := faCall(t, e, wrap(e.handler.handleSymlink), faDirOp(dir, "s", faNoSattr, target)); st != NFS_OK {
			t.Fatalf("status %d, want NFS_OK", st)
		}
		if !strings.Contains(logs.String(), "SYMLINK: Lchown failed") {
			t.Errorf("log = %q, want the lchown failure", logs.String())
		}
	})
}

func TestRemoveAndRmdirErrors(t *testing.T) {
	e := newFaEnv(t, ExportOptions{EnableDirCache: true})
	logs := e.withDebugLog()
	dir := e.handle(t, "/dir")
	e.fs.Mkdir("/dir/empty", 0o755)
	f, _ := e.fs.Create("/dir/sub/inner")
	f.Close()

	remove := wrap(e.handler.handleRemove)
	rmdir := wrap(e.handler.handleRmdir)
	if st := faCall(t, e, remove, buildRemoveRequest(dir, "missing")); st != NFSERR_NOENT {
		t.Errorf("REMOVE missing: status %d, want NFSERR_NOENT", st)
	}
	if st := faCall(t, e, remove, buildRemoveRequest(dir, "file.txt")); st != NFS_OK {
		t.Errorf("REMOVE file.txt: status %d", st)
	}
	if !strings.Contains(logs.String(), "REMOVE: Successfully removed 'file.txt'") ||
		!strings.Contains(logs.String(), "REMOVE: Failed to remove 'missing'") {
		t.Errorf("REMOVE log = %q", logs.String())
	}
	if st := faCall(t, e, remove, buildRemoveRequest(dir, "..")); st == NFS_OK {
		t.Error("REMOVE '..' succeeded")
	}

	cases := []struct {
		name string
		want uint32
	}{
		{"missing", NFSERR_NOENT},
		{"link", NFSERR_NOTDIR},
		{"sub", NFSERR_NOTEMPTY},
		{"empty", NFS_OK},
	}
	for _, c := range cases {
		if st := faCall(t, e, rmdir, buildRemoveRequest(dir, c.name)); st != c.want {
			t.Errorf("RMDIR %s: status %d, want %d", c.name, st, c.want)
		}
	}

	e.nfs.fs = &faHookFS{SymlinkFileSystem: e.fs, removeErr: os.ErrPermission}
	e.fs.Mkdir("/dir/locked", 0o755)
	if st := faCall(t, e, rmdir, buildRemoveRequest(dir, "locked")); st != NFSERR_ACCES {
		t.Errorf("RMDIR permission denied: status %d, want NFSERR_ACCES", st)
	}
	e.nfs.fs = &faHookFS{SymlinkFileSystem: e.fs, removeErr: os.ErrNotExist}
	if st := faCall(t, e, rmdir, buildRemoveRequest(dir, "locked")); st != NFSERR_NOENT {
		t.Errorf("RMDIR raced removal: status %d, want NFSERR_NOENT", st)
	}
}

func TestRenameErrors(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	dir := e.handle(t, "/dir")
	rename := wrap(e.handler.handleRename)
	if st := faCall(t, e, rename, buildRenameRequest(dir, "missing", dir, "x")); st != NFSERR_NOENT {
		t.Errorf("RENAME missing source: status %d, want NFSERR_NOENT", st)
	}
	if st := faCall(t, e, rename, buildRenameRequest(dir, "..", dir, "x")); st == NFS_OK {
		t.Error("RENAME from '..' succeeded")
	}
	if st := faCall(t, e, rename, buildRenameRequest(dir, "file.txt", dir, "..")); st == NFS_OK {
		t.Error("RENAME to '..' succeeded")
	}
	if st := faCall(t, e, rename, buildRenameRequest(dir, "file.txt", dir, "sub")); st != NFSERR_ISDIR {
		t.Errorf("RENAME file over directory: status %d, want NFSERR_ISDIR", st)
	}
}

func TestReadlinkErrors(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	var stale, file bytes.Buffer
	xdrEncodeFileHandle(&stale, legacyHandle(999999))
	xdrEncodeFileHandle(&file, e.handle(t, "/dir/file.txt"))
	readlink := wrap(e.handler.handleReadlink)
	if st := faCall(t, e, readlink, stale.Bytes()); st != NFSERR_STALE {
		t.Errorf("READLINK stale handle: status %d, want NFSERR_STALE", st)
	}
	if st := faCall(t, e, readlink, file.Bytes()); st != NFSERR_INVAL {
		t.Errorf("READLINK of a regular file: status %d, want NFSERR_INVAL", st)
	}
}

func TestLookupDebugLogging(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	logs := e.withDebugLog()
	dir := e.handle(t, "/dir")
	lookup := wrap(e.handler.handleLookup)
	if st := faCall(t, e, lookup, buildLookupRequest(dir, "file.txt")); st != NFS_OK {
		t.Fatalf("status %d", st)
	}
	if st := faCall(t, e, lookup, buildLookupRequest(dir, "missing")); st != NFSERR_NOENT {
		t.Fatalf("status %d, want NFSERR_NOENT", st)
	}
	for _, want := range []string{"LOOKUP: Looking up '/dir/file.txt'", "LOOKUP: Found '/dir/file.txt'", "LOOKUP: '/dir/missing' not found"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log lacks %q", want)
		}
	}
}

func TestReaddirRateLimited(t *testing.T) {
	cfg := DefaultRateLimiterConfig()
	cfg.ReaddirOpsPerSecond = 1
	e := newFaEnv(t, ExportOptions{EnableRateLimiting: true, RateLimitConfig: &cfg})
	dir := e.handle(t, "/dir")
	for _, c := range []struct {
		name string
		fn   func(*bytes.Reader, *RPCReply, *AuthContext) (*RPCReply, error)
		body []byte
	}{
		{"READDIR", wrap(e.handler.handleReaddir), buildReaddirRequest(dir, 0, 4096)},
		{"READDIRPLUS", wrap(e.handler.handleReaddirplus), buildReaddirplusRequest(dir, 0, 4096, 8192)},
	} {
		limited := false
		for i := 0; i < 20 && !limited; i++ {
			limited = faCall(t, e, c.fn, c.body) == NFSERR_DELAY
		}
		if !limited {
			t.Errorf("%s was never rate limited", c.name)
		}
	}
}

// TestOperationLogging: with operation and file-access logging on, the
// operations layer records what it did.
func TestOperationLogging(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "ops.log")
	e := newFaEnv(t, ExportOptions{Log: &LogConfig{
		Level: "debug", Format: "text", Output: logFile,
		LogOperations: true, LogFileAccess: true,
	}})
	n := e.nfs
	dir, err := n.Lookup("/dir")
	if err != nil {
		t.Fatal(err)
	}
	file, err := n.Lookup("/dir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.Read(file, 0, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := n.Write(file, 0, []byte("HE")); err != nil {
		t.Fatal(err)
	}
	if _, err := n.Create(dir, "new.txt", &NFSAttrs{Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if err := n.Remove(dir, "new.txt"); err != nil {
		t.Fatal(err)
	}
	n.Close()
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"LOOKUP operation", "READ operation", "WRITE operation", "CREATE operation", "REMOVE operation"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("log lacks %q", want)
		}
	}
}

func faReadLog(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestReadOnlyDenialsAreLogged(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "ops.log")
	e := newFaEnv(t, ExportOptions{ReadOnly: true, Log: &LogConfig{
		Level: "debug", Format: "text", Output: logFile, LogOperations: true, LogFileAccess: true,
	}})
	dir, _ := e.nfs.Lookup("/dir")
	file, _ := e.nfs.Lookup("/dir/file.txt")
	if _, err := e.nfs.Write(file, 0, []byte("x")); !errors.Is(err, os.ErrPermission) {
		t.Errorf("Write on a read-only export: %v", err)
	}
	if _, err := e.nfs.Create(dir, "x", &NFSAttrs{Mode: 0o644}); !errors.Is(err, os.ErrPermission) {
		t.Errorf("Create on a read-only export: %v", err)
	}
	if err := e.nfs.Remove(dir, "file.txt"); !errors.Is(err, os.ErrPermission) {
		t.Errorf("Remove on a read-only export: %v", err)
	}
	e.nfs.Close()
	logs := faReadLog(t, logFile)
	for _, want := range []string{"WRITE operation denied", "CREATE operation denied", "REMOVE operation denied"} {
		if !strings.Contains(logs, want) {
			t.Errorf("log lacks %q", want)
		}
	}
}

func TestReadAndWriteFailures(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "ops.log")
	e := newFaEnv(t, ExportOptions{Log: &LogConfig{Level: "debug", Format: "text", Output: logFile}})
	file, _ := e.nfs.Lookup("/dir/file.txt")

	e.nfs.fs = &faHookFS{SymlinkFileSystem: e.fs, openErr: map[string]error{"/dir/file.txt": syscall.EIO}}
	if _, err := e.nfs.Read(file, 0, 4); !errors.Is(err, syscall.EIO) {
		t.Errorf("Read with a failing open: %v, want EIO", err)
	}

	// A failed timestamp update is logged but does not fail the write.
	e.nfs.fs = &faHookFS{SymlinkFileSystem: e.fs, chtimesErr: syscall.EPERM}
	if n, err := e.nfs.Write(file, 0, []byte("HE")); err != nil || n != 2 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	e.nfs.Close()
	if logs := faReadLog(t, logFile); !strings.Contains(logs, "Failed to update times") {
		t.Errorf("log lacks the chtimes failure: %q", logs)
	}

	// WRITE to a directory fails with an error status and is logged.
	e2 := newFaEnv(t, ExportOptions{})
	debug := e2.withDebugLog()
	dir := e2.handle(t, "/dir")
	if st := faCall(t, e2, wrap(e2.handler.handleWrite), faWriteRequest(dir, 0, []byte("abcd"))); st == NFS_OK {
		t.Error("WRITE to a directory succeeded")
	}
	if !strings.Contains(debug.String(), "WRITE: Failed to write") {
		t.Errorf("debug log lacks the write failure: %q", debug.String())
	}
}

func TestDirCacheStaysCoherent(t *testing.T) {
	e := newFaEnv(t, ExportOptions{EnableDirCache: true, Log: &LogConfig{
		Level: "debug", Format: "text", Output: filepath.Join(t.TempDir(), "ops.log"),
	}})
	n := e.nfs
	dir, _ := n.Lookup("/dir")
	sub, _ := n.Lookup("/dir/sub")
	names := func(d *NFSNode) []string {
		t.Helper()
		nodes, err := n.ReadDir(d)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, x := range nodes {
			out = append(out, filepath.Base(x.path))
		}
		return out
	}
	has := func(list []string, name string) bool {
		for _, x := range list {
			if x == name {
				return true
			}
		}
		return false
	}

	names(dir) // miss, populates the cache
	names(dir) // hit
	if _, err := n.Create(dir, "new.txt", &NFSAttrs{Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if !has(names(dir), "new.txt") {
		t.Error("CREATE did not invalidate the cached listing")
	}
	if err := n.Rename(dir, "new.txt", sub, "moved.txt"); err != nil {
		t.Fatal(err)
	}
	if has(names(dir), "new.txt") || !has(names(sub), "moved.txt") {
		t.Error("RENAME did not invalidate the cached listings")
	}
	if _, err := n.Symlink(dir, "ln", "file.txt", &NFSAttrs{Mode: 0o777}); err != nil {
		t.Fatal(err)
	}
	if !has(names(dir), "ln") {
		t.Error("SYMLINK did not invalidate the cached listing")
	}

	// An entry removed behind the server's back drops out of a cached listing.
	if err := e.fs.Remove("/dir/ln"); err != nil {
		t.Fatal(err)
	}
	n.attrCache.Invalidate("/dir/ln")
	if has(names(dir), "ln") {
		t.Error("cached listing still shows a removed entry")
	}

	if err := n.Unexport(); err != nil {
		t.Fatal(err)
	}
	if _, ok := n.dirCache.Get("/dir"); ok {
		t.Error("Unexport left the directory cache populated")
	}
}

func TestExportErrors(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	if err := e.nfs.Export("/", -1); err == nil {
		t.Error("Export with a negative port succeeded")
	}
	busy, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	if err := e.nfs.Export("/", busy.Addr().(*net.TCPAddr).Port); err == nil {
		t.Error("Export on a port in use succeeded")
	}
}

func TestReadlinkOfMissingLink(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	gone := &NFSNode{SymlinkFileSystem: e.fs, path: "/dir/gone"}
	if _, err := e.nfs.Readlink(gone); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Readlink of a missing link: %v, want not-exist", err)
	}
}

func TestMapErrorSizeAndName(t *testing.T) {
	if got := mapError(syscall.EFBIG); got != NFSERR_FBIG {
		t.Errorf("EFBIG -> %d", got)
	}
	if got := mapError(syscall.ENAMETOOLONG); got != NFSERR_NAMETOOLONG {
		t.Errorf("ENAMETOOLONG -> %d", got)
	}
}

func TestMknodNotSupported(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	body := faDirOp(e.handle(t, "/dir"), "dev", faU32(NF3CHR))
	if st := faCall(t, e, wrap(e.handler.handleMknod), body); st != NFSERR_NOTSUPP {
		t.Errorf("MKNOD: status %d, want NFSERR_NOTSUPP", st)
	}
}

func TestLargeTransfersRateLimited(t *testing.T) {
	cfg := DefaultRateLimiterConfig()
	cfg.ReadLargeOpsPerSecond = 1
	cfg.WriteLargeOpsPerSecond = 1
	e := newFaEnv(t, ExportOptions{EnableRateLimiting: true, RateLimitConfig: &cfg})
	file := e.handle(t, "/dir/file.txt")
	read := func() []byte {
		var b bytes.Buffer
		xdrEncodeFileHandle(&b, file)
		binary.Write(&b, binary.BigEndian, uint64(0))
		binary.Write(&b, binary.BigEndian, uint32(1<<20))
		return b.Bytes()
	}()
	write := func() []byte {
		var b bytes.Buffer
		xdrEncodeFileHandle(&b, file)
		binary.Write(&b, binary.BigEndian, uint64(0))
		binary.Write(&b, binary.BigEndian, uint32(1<<20)) // count only; no data needed to be limited
		binary.Write(&b, binary.BigEndian, uint32(2))
		return b.Bytes()
	}()
	for _, c := range []struct {
		name string
		fn   func(*bytes.Reader, *RPCReply, *AuthContext) (*RPCReply, error)
		body []byte
	}{
		{"READ", wrap(e.handler.handleRead), read},
		{"WRITE", wrap(e.handler.handleWrite), write},
	} {
		limited := false
		for i := 0; i < 20 && !limited; i++ {
			limited = faCall(t, e, c.fn, c.body) == NFSERR_DELAY
		}
		if !limited {
			t.Errorf("large %s was never rate limited", c.name)
		}
	}
}

func TestReaddirWhenListingFails(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	dir := e.handle(t, "/dir")
	e.nfs.fs = &faHookFS{SymlinkFileSystem: e.fs, openErr: map[string]error{"/dir": syscall.EIO}}
	if st := faCall(t, e, wrap(e.handler.handleReaddir), buildReaddirRequest(dir, 0, 4096)); st != NFSERR_IO {
		t.Errorf("READDIR: status %d, want NFSERR_IO", st)
	}
	if st := faCall(t, e, wrap(e.handler.handleReaddirplus), buildReaddirplusRequest(dir, 0, 4096, 8192)); st != NFSERR_IO {
		t.Errorf("READDIRPLUS: status %d, want NFSERR_IO", st)
	}
}

func TestGetattrDebugLogging(t *testing.T) {
	e := newFaEnv(t, ExportOptions{})
	logs := e.withDebugLog()
	var stale bytes.Buffer
	xdrEncodeFileHandle(&stale, legacyHandle(987654))
	getattr := wrap(e.handler.handleGetattr)
	if st := faCall(t, e, getattr, []byte{0, 0}); st != GARBAGE_ARGS {
		t.Errorf("GETATTR garbage: status %d", st)
	}
	if st := faCall(t, e, getattr, stale.Bytes()); st != NFSERR_STALE {
		t.Errorf("GETATTR stale: status %d", st)
	}
	for _, want := range []string{"GETATTR: Failed to decode handle", "GETATTR: Looking up handle 00000000000f1206", "GETATTR: Handle 00000000000f1206 is stale"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log lacks %q", want)
		}
	}
}

// TestAccessGroupPermissions: group bits apply to the file's group and to
// members through auxiliary groups.
func TestAccessGroupPermissions(t *testing.T) {
	e := newFaEnv(t, ExportOptions{DefaultOwner: &FileOwner{UID: 1000, GID: 2000}})
	if err := e.fs.Chmod("/dir/file.txt", 0o060); err != nil { // group rw only
		t.Fatal(err)
	}
	file := e.handle(t, "/dir/file.txt")
	access := func(auth *AuthContext) uint32 {
		t.Helper()
		r, _ := e.handler.handleAccess(bytes.NewReader(buildAccessRequest(file, ACCESS3_READ|ACCESS3_MODIFY)), &RPCReply{}, auth)
		data := getReplyData(r)
		if readStatusFromReply(r) != NFS_OK {
			t.Fatalf("ACCESS status %d", readStatusFromReply(r))
		}
		return binary.BigEndian.Uint32(data[len(data)-4:])
	}
	inGroup := &AuthContext{ClientIP: "127.0.0.1", EffectiveUID: 3000, EffectiveGID: 2000, Credential: &RPCCredential{Flavor: AUTH_SYS}}
	auxMember := &AuthContext{ClientIP: "127.0.0.1", EffectiveUID: 3000, EffectiveGID: 4000,
		Credential: &RPCCredential{Flavor: AUTH_SYS}, AuthSys: &AuthSysCredential{UID: 3000, GID: 4000, AuxGIDs: []uint32{5, 2000}}}
	outsider := &AuthContext{ClientIP: "127.0.0.1", EffectiveUID: 3000, EffectiveGID: 4000,
		Credential: &RPCCredential{Flavor: AUTH_SYS}, AuthSys: &AuthSysCredential{UID: 3000, GID: 4000, AuxGIDs: []uint32{5}}}
	if got := access(inGroup); got != ACCESS3_READ|ACCESS3_MODIFY {
		t.Errorf("primary group: access %#x", got)
	}
	if got := access(auxMember); got != ACCESS3_READ|ACCESS3_MODIFY {
		t.Errorf("auxiliary group: access %#x", got)
	}
	if got := access(outsider); got != 0 {
		t.Errorf("outsider: access %#x, want none", got)
	}
}

func TestMountRateLimitAndDebugLog(t *testing.T) {
	cfg := DefaultRateLimiterConfig()
	cfg.MountOpsPerMinute = 1
	e := newFaEnv(t, ExportOptions{EnableRateLimiting: true, RateLimitConfig: &cfg})
	logs := e.withDebugLog()
	var body bytes.Buffer
	xdrEncodeString(&body, "/")
	call := &RPCCall{Header: RPCMsgHeader{Program: MOUNT_PROGRAM, Version: MOUNT_V3, Procedure: 1}}
	busy := false
	for i := 0; i < 10 && !busy; i++ {
		r, err := e.handler.handleMountCall(call, bytes.NewReader(body.Bytes()), &RPCReply{}, e.auth)
		if err != nil {
			t.Fatal(err)
		}
		busy = readStatusFromReply(r) == 10006 // MNT3ERR_SERVERFAULT
	}
	if !busy {
		t.Error("MNT was never rate limited")
	}
	if !strings.Contains(logs.String(), "MOUNT: handle ") {
		t.Errorf("log lacks the mount: %q", logs.String())
	}
}

func TestHandleCallDebugLogging(t *testing.T) {
	e := newFaEnv(t, ExportOptions{Secure: true})
	logs := e.withDebugLog()
	call := &RPCCall{Header: RPCMsgHeader{Program: NFS_PROGRAM, Version: NFS_V3, Procedure: 0}}
	// From an unprivileged port, a Secure export denies the call.
	insecure := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 40000, Credential: &RPCCredential{Flavor: AUTH_SYS}}
	r, err := e.handler.HandleCall(call, bytes.NewReader(nil), insecure)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != MSG_DENIED {
		t.Errorf("status %d, want MSG_DENIED", r.Status)
	}
	secure := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 800, Credential: &RPCCredential{Flavor: AUTH_NONE}}
	if r, err = e.handler.HandleCall(call, bytes.NewReader(nil), secure); err != nil || r.Status != MSG_ACCEPTED {
		t.Fatalf("privileged NULL call: %v, %+v", err, r)
	}
	for _, want := range []string{"Authentication denied", "HandleCall: prog=100003 vers=3 proc=0"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log lacks %q", want)
		}
	}
}

func TestNewRejectsBadOptions(t *testing.T) {
	mfs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(mfs, ExportOptions{Squash: "some"}); err == nil {
		t.Error("New accepted an invalid squash mode")
	}
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	badLog := &LogConfig{Output: filepath.Join(notADir, "log")}
	if _, err := New(mfs, ExportOptions{Log: badLog}); err == nil {
		t.Error("New accepted an unwritable log path")
	}
}

func TestRuntimeOptionUpdates(t *testing.T) {
	tlsCfg := &TLSConfig{Enabled: false}
	e := newFaEnv(t, ExportOptions{EnableDirCache: true, TLS: tlsCfg})
	n := e.nfs
	if got := n.GetExportOptions(); got.TLS == nil || got.TLS == tlsCfg {
		t.Errorf("GetExportOptions TLS = %p, want a copy of %p", got.TLS, tlsCfg)
	}

	n.UpdateTuningOptions(func(o *TuningOptions) {
		o.DirCacheMaxEntries = 7
		o.DirCacheTimeout = 3 * time.Second
	})
	n.dirCache.mu.RLock()
	maxEntries, timeout := n.dirCache.maxEntries, n.dirCache.timeout
	n.dirCache.mu.RUnlock()
	if maxEntries != 7 || timeout != 3*time.Second {
		t.Errorf("dir cache = %d entries, %v; want 7, 3s", maxEntries, timeout)
	}

	policy := *n.policy.Load()
	policy.Squash = "all"
	if err := n.UpdatePolicyOptions(policy); err == nil {
		t.Error("UpdatePolicyOptions changed the squash mode")
	}
	cfg := DefaultRateLimiterConfig()
	policy = *n.policy.Load()
	policy.EnableRateLimiting = true
	policy.RateLimitConfig = &cfg
	policy.TLS = tlsCfg
	if err := n.UpdatePolicyOptions(policy); err != nil {
		t.Fatal(err)
	}
	if n.rateLimiter == nil || n.policy.Load().TLS == tlsCfg {
		t.Error("UpdatePolicyOptions did not install a rate limiter and a TLS copy")
	}
	opts := n.GetExportOptions()
	opts.TLS = tlsCfg
	if err := n.UpdateExportOptions(opts); err != nil {
		t.Fatal(err)
	}
	if n.policy.Load().TLS == tlsCfg {
		t.Error("UpdateExportOptions stored the caller's TLS config instead of a copy")
	}
}
