package absnfs

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/absfs/osfs"
)

// swapEnv serves a temporary directory through osfs, which follows links
// on the host as every host-backed export does. The export is <root>/export
// and <root>/outside stands for what lies beyond it: a canary file, an empty
// directory and a file, whose state must never change.
type swapEnv struct {
	*cacheEnv
	native  string // <root>, native
	export  string // <root>/export, absfs path
	outside string // <root>/outside, native
	before  string // snapshot of outside
}

func newSwapEnv(t *testing.T) *swapEnv {
	t.Helper()
	e, root := newOSCacheEnv(t, ExportOptions{AttrCacheTimeout: time.Minute})
	native := osfs.ToNative(root)
	s := &swapEnv{cacheEnv: e, native: native, export: root + "/export", outside: filepath.Join(native, "outside")}
	for _, d := range []string{"export", "outside", "outside/sub"} {
		if err := os.Mkdir(filepath.Join(native, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range map[string]string{"outside/canary": "canary", "outside/f": "outside file"} {
		if err := os.WriteFile(filepath.Join(native, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Unix(1e9, 0)
	if err := os.Chtimes(filepath.Join(native, "outside/canary"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(native, "outside"), filepath.Join(native, "probe")); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	os.Remove(filepath.Join(native, "probe"))
	s.before = s.snapshot()
	return s
}

// snapshot describes everything below outside: names, types, modes, sizes,
// times and contents.
func (s *swapEnv) snapshot() string {
	s.t.Helper()
	var lines []string
	err := filepath.Walk(s.outside, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		line := p + " " + info.Mode().String() + " " + info.ModTime().String()
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			line += " " + string(data)
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		s.t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func (s *swapEnv) checkOutside(where string) {
	s.t.Helper()
	if now := s.snapshot(); now != s.before {
		s.t.Errorf("%s changed what lies outside the export:\nbefore:\n%s\nafter:\n%s", where, s.before, now)
	}
}

// swapForLink replaces the object at name (below the export) with a
// symbolic link to target, behind the server's back.
func (s *swapEnv) swapForLink(name, target string) {
	s.t.Helper()
	p := filepath.Join(s.native, "export", name)
	if err := os.RemoveAll(p); err != nil {
		s.t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		s.t.Fatal(err)
	}
}

// call runs procedure proc with the XDR arguments args and returns the
// reply's status.
func (s *swapEnv) call(proc uint32, args []byte) uint32 {
	s.t.Helper()
	reply, err := nfsHandlers[proc](s.h, bytes.NewReader(args), &RPCReply{}, s.auth)
	if err != nil {
		s.t.Fatalf("procedure %d: %v", proc, err)
	}
	return readStatusFromReply(reply)
}

// xdrArgs encodes handles ([]byte), strings and uint32/uint64 values in turn.
func xdrArgs(parts ...interface{}) []byte {
	var buf bytes.Buffer
	for _, p := range parts {
		switch v := p.(type) {
		case []byte:
			xdrEncodeFileHandle(&buf, v)
		case string:
			xdrEncodeString(&buf, v)
		case uint32:
			binary.Write(&buf, binary.BigEndian, v)
		case uint64:
			binary.Write(&buf, binary.BigEndian, v)
		default:
			panic("xdrArgs: unsupported type")
		}
	}
	return buf.Bytes()
}

// noSattr is a sattr3 that sets nothing.
var noSattr = encodeSattr3(false, 0, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0)

// TestDirectorySwappedForLink: a directory replaced by a symbolic link to
// a directory outside the export, after its handle was issued, is no
// longer a directory. Every procedure that takes the handle as a directory
// answers NFS3ERR_NOTDIR (LINK and MKNOD, which absnfs does not support,
// NFS3ERR_NOTSUPP) and nothing outside the export is read or changed.
func TestDirectorySwappedForLink(t *testing.T) {
	s := newSwapEnv(t)
	for _, name := range []string{"d", "d/sub"} {
		s.mkdir(s.export + "/" + name)
	}
	s.writeFile(s.export+"/d/canary", "inside")
	s.writeFile(s.export+"/d/f", "inside")
	s.writeFile(s.export+"/file", "inside")
	root := s.handle(s.export)
	file := s.handle(s.export + "/file")
	d := s.handle(s.export + "/d") // cached, with directory attributes
	s.swapForLink("d", s.outside)

	cases := []struct {
		name string
		proc uint32
		args []byte
		want uint32
	}{
		{"LOOKUP", NFSPROC3_LOOKUP, xdrArgs(d, "canary"), NFSERR_NOTDIR},
		{"READDIR", NFSPROC3_READDIR, xdrArgs(d, uint64(0), uint64(0), uint32(4096)), NFSERR_NOTDIR},
		{"READDIRPLUS", NFSPROC3_READDIRPLUS, xdrArgs(d, uint64(0), uint64(0), uint32(4096), uint32(4096)), NFSERR_NOTDIR},
		{"CREATE", NFSPROC3_CREATE, append(xdrArgs(d, "new", uint32(0)), noSattr...), NFSERR_NOTDIR},
		{"CREATE over the canary", NFSPROC3_CREATE, append(xdrArgs(d, "canary", uint32(0)), noSattr...), NFSERR_NOTDIR},
		{"MKDIR", NFSPROC3_MKDIR, append(xdrArgs(d, "newdir"), noSattr...), NFSERR_NOTDIR},
		{"SYMLINK", NFSPROC3_SYMLINK, append(append(xdrArgs(d, "ln"), noSattr...), xdrArgs("t")...), NFSERR_NOTDIR},
		{"MKNOD", NFSPROC3_MKNOD, xdrArgs(d, "node", uint32(NF3FIFO)), NFSERR_NOTSUPP},
		{"REMOVE", NFSPROC3_REMOVE, xdrArgs(d, "canary"), NFSERR_NOTDIR},
		{"RMDIR", NFSPROC3_RMDIR, xdrArgs(d, "sub"), NFSERR_NOTDIR},
		{"RENAME from it", NFSPROC3_RENAME, xdrArgs(d, "canary", root, "stolen"), NFSERR_NOTDIR},
		{"RENAME into it", NFSPROC3_RENAME, xdrArgs(root, "file", d, "canary"), NFSERR_NOTDIR},
		{"RENAME within it", NFSPROC3_RENAME, xdrArgs(d, "f", d, "canary"), NFSERR_NOTDIR},
		{"LINK", NFSPROC3_LINK, xdrArgs(file, d, "hard"), NFSERR_NOTSUPP},
	}
	for _, c := range cases {
		if got := s.call(c.proc, c.args); got != c.want {
			t.Errorf("%s through a directory swapped for a link: status %d, want %d", c.name, got, c.want)
		}
		s.checkOutside(c.name)
	}
	if _, err := os.Lstat(filepath.Join(s.native, "export", "stolen")); err == nil {
		t.Error("RENAME moved the canary into the export")
	}
	if data, err := os.ReadFile(filepath.Join(s.native, "export", "file")); err != nil || string(data) != "inside" {
		t.Errorf("RENAME into the link moved the export's file: %q, %v", data, err)
	}

	// Through the Go API too.
	dir, err := s.nfs.Lookup(s.export + "/d")
	if err != nil {
		t.Fatal(err)
	}
	dir.attrs = &NFSAttrs{Mode: os.ModeDir | 0o755} // as issued
	if _, err := s.nfs.ReadDirPlus(dir); mapError(err) != NFSERR_NOTDIR {
		t.Errorf("ReadDirPlus: %v, want NFS3ERR_NOTDIR", err)
	}
	if _, err := s.nfs.Symlink(dir, "ln", "t", &NFSAttrs{Mode: os.ModeSymlink | 0o777}); mapError(err) != NFSERR_NOTDIR {
		t.Errorf("Symlink: %v, want NFS3ERR_NOTDIR", err)
	}
	s.checkOutside("the Go API")

	// Once the handle has left the cache it resolves by path, and the
	// object there is no longer of its type: it is stale.
	s.nfs.fileMap.ReleaseAll()
	if got := s.call(NFSPROC3_LOOKUP, xdrArgs(d, "canary")); got != NFSERR_STALE {
		t.Errorf("LOOKUP with an evicted handle of the swapped directory: status %d, want NFS3ERR_STALE", got)
	}
	s.checkOutside("LOOKUP with an evicted handle")
}

// TestFileSwappedForLink: a regular file replaced by a symbolic link after
// its handle was issued is read, written and truncated never through the
// link, and a symbolic link replaced by a file is not read as a link.
func TestFileSwappedForLink(t *testing.T) {
	s := newSwapEnv(t)
	s.writeFile(s.export+"/f", "inside")
	s.writeFile(s.export+"/g", "inside")
	if err := os.Symlink("f", filepath.Join(s.native, "export", "ln")); err != nil {
		t.Fatal(err)
	}
	f := s.handle(s.export + "/f")
	g := s.handle(s.export + "/g")
	ln := s.handle(s.export + "/ln")
	canary := filepath.Join(s.outside, "canary")
	s.swapForLink("f", canary)
	s.swapForLink("g", s.outside)

	data := []byte("pwned")
	write := xdrArgs(f, uint64(0), uint32(len(data)), uint32(2), uint32(len(data)))
	write = append(write, data...)
	write = append(write, 0, 0, 0)
	cases := []struct {
		name string
		proc uint32
		args []byte
		want uint32
	}{
		{"READ", NFSPROC3_READ, xdrArgs(f, uint64(0), uint32(100)), NFSERR_INVAL},
		{"WRITE", NFSPROC3_WRITE, write, NFSERR_INVAL},
		{"SETATTR size", NFSPROC3_SETATTR, append(append(xdrArgs(f), encodeSattr3(false, 0, false, 0, false, 0, true, 0, 0, 0, 0, 0, 0, 0)...), xdrArgs(uint32(0))...), NFSERR_INVAL},
		{"SETATTR mode", NFSPROC3_SETATTR, append(append(xdrArgs(f), encodeSattr3(true, 0o600, false, 0, false, 0, false, 0, 0, 0, 0, 0, 0, 0)...), xdrArgs(uint32(0))...), NFSERR_INVAL},
		{"READ of a file swapped for a directory link", NFSPROC3_READ, xdrArgs(g, uint64(0), uint32(100)), NFSERR_INVAL},
	}
	for _, c := range cases {
		if got := s.call(c.proc, c.args); got != c.want {
			t.Errorf("%s through a file swapped for a link: status %d, want %d", c.name, got, c.want)
		}
		s.checkOutside(c.name)
	}

	// A symbolic link replaced by a regular file is no longer read as one.
	if err := os.Remove(filepath.Join(s.native, "export", "ln")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.native, "export", "ln"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := s.call(NFSPROC3_READLINK, xdrArgs(ln)); got != NFSERR_INVAL {
		t.Errorf("READLINK of a link replaced by a file: status %d, want NFS3ERR_INVAL", got)
	}
	// And one removed behind the server's back is gone.
	if err := os.Remove(filepath.Join(s.native, "export", "ln")); err != nil {
		t.Fatal(err)
	}
	if got := s.call(NFSPROC3_READLINK, xdrArgs(ln)); got != NFSERR_NOENT {
		t.Errorf("READLINK of a link removed behind the server's back: status %d, want NFS3ERR_NOENT", got)
	}
}

// TestDirectoryNotReadOrTruncatedAsFile: READ, WRITE and SETATTR size on
// a directory answer NFS3ERR_ISDIR from its current type.
func TestDirectoryNotReadOrTruncatedAsFile(t *testing.T) {
	s := newSwapEnv(t)
	s.mkdir(s.export + "/d")
	d := s.handle(s.export + "/d")
	sizeZero := encodeSattr3(false, 0, false, 0, false, 0, true, 0, 0, 0, 0, 0, 0, 0)
	data := []byte("data")
	write := append(xdrArgs(d, uint64(0), uint32(len(data)), uint32(2), uint32(len(data))), data...)
	for name, c := range map[string]struct {
		proc uint32
		args []byte
	}{
		"READ":         {NFSPROC3_READ, xdrArgs(d, uint64(0), uint32(100))},
		"WRITE":        {NFSPROC3_WRITE, write},
		"SETATTR size": {NFSPROC3_SETATTR, append(append(xdrArgs(d), sizeZero...), xdrArgs(uint32(0))...)},
	} {
		if got := s.call(c.proc, c.args); got != NFSERR_ISDIR {
			t.Errorf("%s on a directory: status %d, want NFS3ERR_ISDIR", name, got)
		}
	}
}
