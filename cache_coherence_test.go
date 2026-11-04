package absnfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/absfs/absfs"
	"github.com/absfs/memfs"
	"github.com/absfs/osfs"
)

// countingFS counts the backend's Lstat and Stat calls, which is what the
// attribute cache exists to save, and can run a hook right after one Lstat
// returns, to replay an interleaving deterministically: the caller holds a
// stat result taken before the hook's mutation.
type countingFS struct {
	absfs.SymlinkFileSystem
	lstats atomic.Int64
	stats  atomic.Int64

	mu          sync.Mutex
	hookPath    string
	hook        func()
	readdirPath string
	readdirHook func()
	hookCalls   int
}

// OpenFile hands out a file whose Readdir runs the hook set by
// afterNextReaddir.
func (f *countingFS) OpenFile(name string, flag int, perm os.FileMode) (absfs.File, error) {
	file, err := f.SymlinkFileSystem.OpenFile(name, flag, perm)
	f.mu.Lock()
	defer f.mu.Unlock()
	if err != nil || f.readdirHook == nil || name != f.readdirPath {
		return file, err
	}
	hook := f.readdirHook
	f.readdirHook = nil
	return &readdirHookFile{File: file, fs: f, hook: hook}, nil
}

type readdirHookFile struct {
	absfs.File
	fs   *countingFS
	hook func()
}

func (h *readdirHookFile) Readdir(n int) ([]os.FileInfo, error) {
	entries, err := h.File.Readdir(n)
	h.fs.mu.Lock()
	h.fs.hookCalls++
	h.fs.mu.Unlock()
	h.hook()
	return entries, err
}

// afterNextReaddir runs hook once, right after the next listing of p has
// been read.
func (f *countingFS) afterNextReaddir(p string, hook func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readdirPath, f.readdirHook = p, hook
}

func (f *countingFS) Lstat(name string) (os.FileInfo, error) {
	f.lstats.Add(1)
	info, err := f.SymlinkFileSystem.Lstat(name)
	f.mu.Lock()
	hook := f.hook
	if hook != nil && name == f.hookPath {
		f.hook = nil
		f.hookCalls++
	} else {
		hook = nil
	}
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return info, err
}

func (f *countingFS) Stat(name string) (os.FileInfo, error) {
	f.stats.Add(1)
	return f.SymlinkFileSystem.Stat(name)
}

// afterNextLstat runs hook once, right after the next Lstat of p returns.
func (f *countingFS) afterNextLstat(p string, hook func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hookPath, f.hook = p, hook
}

func (f *countingFS) hookRan() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hookCalls > 0
}

type cacheEnv struct {
	t    testing.TB
	fs   *countingFS
	nfs  *AbsfsNFS
	h    *NFSProcedureHandler
	auth *AuthContext
}

// newCacheEnv serves a memfs through a countingFS.
func newCacheEnv(t testing.TB, opts ExportOptions) *cacheEnv {
	t.Helper()
	mfs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	return newCacheEnvOn(t, mfs, opts)
}

func newCacheEnvOn(t testing.TB, backend absfs.SymlinkFileSystem, opts ExportOptions) *cacheEnv {
	t.Helper()
	cfs := &countingFS{SymlinkFileSystem: backend}
	nfs, err := New(cfs, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nfs.Close() })
	srv := &Server{handler: nfs, options: ServerOptions{}}
	return &cacheEnv{
		t:    t,
		fs:   cfs,
		nfs:  nfs,
		h:    &NFSProcedureHandler{server: srv},
		auth: &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345},
	}
}

// newOSCacheEnv serves a fresh temporary directory through osfs (directly:
// basefs and osfs disagree on Windows paths) and returns its absfs path.
func newOSCacheEnv(t testing.TB, opts ExportOptions) (*cacheEnv, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ofs, err := osfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	return newCacheEnvOn(t, ofs, opts), osfs.FromNative(root)
}

func (e *cacheEnv) mkdir(p string) {
	e.t.Helper()
	if err := e.fs.SymlinkFileSystem.Mkdir(p, 0o755); err != nil {
		e.t.Fatal(err)
	}
}

// writeFile replaces the content of p behind the server's back.
func (e *cacheEnv) writeFile(p, data string) {
	e.t.Helper()
	f, err := e.fs.SymlinkFileSystem.OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := f.Write([]byte(data)); err != nil {
		e.t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		e.t.Fatal(err)
	}
}

func (e *cacheEnv) lookup(p string) *NFSNode {
	e.t.Helper()
	n, err := e.nfs.Lookup(p)
	if err != nil {
		e.t.Fatalf("Lookup(%s): %v", p, err)
	}
	return n
}

func (e *cacheEnv) handle(p string) []byte {
	e.t.Helper()
	return e.nfs.fileMap.issue(e.lookup(p))
}

func (e *cacheEnv) names(dir *NFSNode) []string {
	e.t.Helper()
	nodes, err := e.nfs.ReadDir(dir)
	if err != nil {
		e.t.Fatalf("ReadDir(%s): %v", dir.path, err)
	}
	var names []string
	for _, n := range nodes {
		names = append(names, path.Base(n.path))
	}
	slices.Sort(names)
	return names
}

func wantNotExist(t testing.TB, what string, err error) {
	t.Helper()
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s: err = %v, want not-exist", what, err)
	}
}

// TestAttrCacheGetCarriesExpiry: what the cache hands out is valid until
// the entry expires. Get used to drop validUntil, so IsValid was false for
// every hit and GETATTR never used the cache.
func TestAttrCacheGetCarriesExpiry(t *testing.T) {
	c := NewAttrCache(time.Minute, 10)
	now := time.Now()
	c.Put("/f", NewNFSAttrs(0o644, 5, now, now, 1, 2))
	got, ok := c.Get("/f")
	if !ok || got == nil {
		t.Fatalf("Get after Put: %v, %v", got, ok)
	}
	if !got.IsValid() {
		t.Errorf("attributes from a live cache entry report IsValid() == false")
	}
	if got.Size != 5 || got.Uid != 1 || got.Gid != 2 {
		t.Errorf("Get = %+v, want size 5 uid 1 gid 2", got)
	}
}

// TestGetAttrServesCachedAttrs: GETATTRs within the TTL do not reach the
// backend.
func TestGetAttrServesCachedAttrs(t *testing.T) {
	e := newCacheEnv(t, ExportOptions{AttrCacheTimeout: time.Minute})
	e.writeFile("/f", "hello")
	node := e.lookup("/f")
	before := e.fs.lstats.Load()
	for i := 0; i < 10; i++ {
		attrs, err := e.nfs.GetAttr(node)
		if err != nil {
			t.Fatal(err)
		}
		if attrs.Size != 5 {
			t.Fatalf("size %d, want 5", attrs.Size)
		}
	}
	if n := e.fs.lstats.Load() - before; n != 0 {
		t.Errorf("10 GETATTRs within the TTL made %d backend Lstat calls, want 0", n)
	}
}

// TestAttrCacheTTLBoundsOutsideChanges: a change made behind the server's
// back is reported once the TTL has passed, by GETATTR and LOOKUP alike.
func TestAttrCacheTTLBoundsOutsideChanges(t *testing.T) {
	const ttl = 300 * time.Millisecond
	e := newCacheEnv(t, ExportOptions{AttrCacheTimeout: ttl})
	e.writeFile("/f", "hello")
	node := e.lookup("/f")
	cachedAt := time.Now()
	e.writeFile("/f", "hello world")

	attrs, err := e.nfs.GetAttr(node)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(cachedAt) < ttl && attrs.Size != 5 {
		t.Errorf("within the TTL GETATTR reported size %d, want the cached 5", attrs.Size)
	}

	time.Sleep(ttl + 50*time.Millisecond)
	if attrs, err := e.nfs.GetAttr(node); err != nil || attrs.Size != 11 {
		t.Errorf("GETATTR after the TTL: %v, %v; want size 11", attrs, err)
	}
	e.writeFile("/f", "hello again!")
	time.Sleep(ttl + 50*time.Millisecond)
	if n := e.lookup("/f"); n.attrs.Size != 12 {
		t.Errorf("LOOKUP after the TTL reported size %d, want 12", n.attrs.Size)
	}
}

// TestAttrCacheTTLChangeAppliesToExistingEntries: lowering the TTL bounds
// entries cached under the old one too.
func TestAttrCacheTTLChangeAppliesToExistingEntries(t *testing.T) {
	c := NewAttrCache(time.Hour, 10)
	c.ConfigureNegativeCaching(true, time.Hour)
	now := time.Now()
	c.Put("/f", NewNFSAttrs(0o644, 5, now, now, 0, 0))
	c.PutNegative("/missing")

	c.UpdateTTL(time.Millisecond)
	c.ConfigureNegativeCaching(true, time.Millisecond)
	time.Sleep(5 * time.Millisecond)

	if attrs, ok := c.Get("/f"); ok {
		t.Errorf("entry cached under a 1h TTL still served after lowering it to 1ms: %+v", attrs)
	}
	if _, ok := c.Get("/missing"); ok {
		t.Errorf("negative entry still served after lowering its TTL to 1ms")
	}
}

// TestRenameDirectoryInvalidatesSubtree: after RENAME of a directory nothing
// cached below either name survives: attributes, negative entries and
// listings.
func TestRenameDirectoryInvalidatesSubtree(t *testing.T) {
	e := newCacheEnv(t, ExportOptions{
		AttrCacheTimeout:     time.Hour,
		CacheNegativeLookups: true,
		NegativeCacheTimeout: time.Hour,
		EnableDirCache:       true,
		DirCacheTimeout:      time.Hour,
	})
	e.mkdir("/a")
	e.mkdir("/a/b")
	e.writeFile("/a/b/f", "x")
	e.mkdir("/c") // empty: RENAME a over it replaces it

	ab := e.lookup("/a/b")
	e.lookup("/a/b/f")
	c := e.lookup("/c")
	_, err := e.nfs.Lookup("/c/b")
	wantNotExist(t, "Lookup(/c/b) before the rename", err)
	_, err = e.nfs.Lookup("/c/b/f")
	wantNotExist(t, "Lookup(/c/b/f) before the rename", err)
	if got := e.names(c); len(got) != 0 {
		t.Fatalf("/c lists %v before the rename", got)
	}
	if got := e.names(ab); !slices.Equal(got, []string{"f"}) {
		t.Fatalf("/a/b lists %v before the rename", got)
	}

	root := e.lookup("/")
	if err := e.nfs.Rename(root, "a", root, "c"); err != nil {
		t.Fatal(err)
	}

	_, err = e.nfs.Lookup("/a/b/f")
	wantNotExist(t, "Lookup(/a/b/f) after renaming /a away", err)
	_, err = e.nfs.Lookup("/a/b")
	wantNotExist(t, "Lookup(/a/b) after renaming /a away", err)
	_, err = e.nfs.GetAttr(ab)
	wantNotExist(t, "GetAttr of the old /a/b node", err)
	if _, err := e.nfs.ReadDir(ab); err == nil {
		t.Errorf("ReadDir of the old /a/b succeeded from the cache")
	}
	if _, err := e.nfs.Lookup("/c/b/f"); err != nil {
		t.Errorf("Lookup(/c/b/f) after the rename: %v (stale negative entry)", err)
	}
	if got := e.names(c); !slices.Equal(got, []string{"b"}) {
		t.Errorf("/c lists %v after the rename, want [b]", got)
	}
}

// TestRemoveRacingLookupLeavesNoStaleEntry: a LOOKUP that read the
// attributes just before a REMOVE must not cache them after it.
func TestRemoveRacingLookupLeavesNoStaleEntry(t *testing.T) {
	e := newCacheEnv(t, ExportOptions{AttrCacheTimeout: time.Hour})
	e.mkdir("/d")
	e.writeFile("/d/f", "x")
	dir := e.lookup("/d")

	e.fs.afterNextLstat("/d/f", func() {
		if err := e.nfs.Remove(dir, "f"); err != nil {
			t.Errorf("Remove: %v", err)
		}
	})
	e.nfs.Lookup("/d/f") // raced: may report the file
	if !e.fs.hookRan() {
		t.Fatal("the interleaving did not happen")
	}
	_, err := e.nfs.Lookup("/d/f")
	wantNotExist(t, "Lookup after REMOVE", err)
}

// TestWriteRacingLookupLeavesNoStaleEntry: likewise for a WRITE. It runs
// on osfs because memfs's FileInfo is a live view of the file, not a
// snapshot taken by Lstat.
func TestWriteRacingLookupLeavesNoStaleEntry(t *testing.T) {
	e, base := newOSCacheEnv(t, ExportOptions{AttrCacheTimeout: time.Hour})
	p := path.Join(base, "f")
	e.writeFile(p, "hello")
	node := e.lookup(p)
	e.nfs.attrCache.Invalidate(p)

	e.fs.afterNextLstat(p, func() {
		if _, err := e.nfs.Write(node, 5, []byte(" world")); err != nil {
			t.Errorf("Write: %v", err)
		}
	})
	e.nfs.Lookup(p) // raced: may report the old size
	if !e.fs.hookRan() {
		t.Fatal("the interleaving did not happen")
	}
	if n := e.lookup(p); n.attrs.Size != 11 {
		t.Errorf("LOOKUP after WRITE reports size %d, want 11", n.attrs.Size)
	}
	if attrs, err := e.nfs.GetAttr(node); err != nil || attrs.Size != 11 {
		t.Errorf("GETATTR after WRITE: %+v, %v; want size 11", attrs, err)
	}
}

// TestCreateRacingNegativeLookupLeavesNoStaleEntry: a LOOKUP that missed
// just before a CREATE must not cache the miss after it.
func TestCreateRacingNegativeLookupLeavesNoStaleEntry(t *testing.T) {
	e := newCacheEnv(t, ExportOptions{
		CacheNegativeLookups: true,
		NegativeCacheTimeout: time.Hour,
	})
	e.mkdir("/d")
	dir := e.lookup("/d")

	e.fs.afterNextLstat("/d/new", func() {
		if _, err := e.nfs.Create(dir, "new", &NFSAttrs{Mode: 0o644}); err != nil {
			t.Errorf("Create: %v", err)
		}
	})
	e.nfs.Lookup("/d/new") // raced: may miss
	if !e.fs.hookRan() {
		t.Fatal("the interleaving did not happen")
	}
	if _, err := e.nfs.Lookup("/d/new"); err != nil {
		t.Errorf("Lookup after CREATE: %v (stale negative entry)", err)
	}
}

// mkdirArgs encodes MKDIR3args with no attributes set.
func mkdirArgs(dir []byte, name string) *bytes.Buffer {
	var b bytes.Buffer
	xdrEncodeFileHandle(&b, dir)
	xdrEncodeString(&b, name)
	for i := 0; i < 6; i++ { // sattr3: nothing set
		xdrEncodeUint32(&b, 0)
	}
	return &b
}

// TestMkdirInvalidatesNegativeEntryAndListing: MKDIR forgets a cached miss
// for the new name and the parent's cached listing.
func TestMkdirInvalidatesNegativeEntryAndListing(t *testing.T) {
	e := newCacheEnv(t, ExportOptions{
		AttrCacheTimeout:     time.Hour,
		CacheNegativeLookups: true,
		NegativeCacheTimeout: time.Hour,
		EnableDirCache:       true,
		DirCacheTimeout:      time.Hour,
	})
	root := e.lookup("/")
	_, err := e.nfs.Lookup("/newdir")
	wantNotExist(t, "Lookup before MKDIR", err)
	if got := e.names(root); len(got) != 0 {
		t.Fatalf("root lists %v", got)
	}

	reply, err := e.h.handleMkdir(mkdirArgs(e.handle("/"), "newdir"), &RPCReply{}, e.auth)
	if err != nil {
		t.Fatal(err)
	}
	if status := readStatusFromReply(reply); status != NFS_OK {
		t.Fatalf("MKDIR status %d, want NFS_OK", status)
	}
	if _, err := e.nfs.Lookup("/newdir"); err != nil {
		t.Errorf("Lookup after MKDIR: %v", err)
	}
	if got := e.names(root); !slices.Equal(got, []string{"newdir"}) {
		t.Errorf("root lists %v after MKDIR, want [newdir]", got)
	}
}

// lookupReplyDirMtime returns the directory mtime a LOOKUP reply carries.
func lookupReplyDirMtime(t *testing.T, reply *RPCReply) (sec, nsec uint32) {
	t.Helper()
	r := bytes.NewReader(getReplyData(reply))
	if status, _ := xdrDecodeUint32(r); status != NFS_OK {
		t.Fatalf("LOOKUP status %d", status)
	}
	if _, err := xdrDecodeFileHandle(r); err != nil {
		t.Fatal(err)
	}
	const fattr3Size = 84
	skip := make([]byte, 4+fattr3Size+4) // object attributes, dir follows flag
	if _, err := r.Read(skip); err != nil {
		t.Fatal(err)
	}
	dir := make([]byte, fattr3Size)
	if n, _ := r.Read(dir); n != fattr3Size {
		t.Fatalf("short dir attributes: %d bytes", n)
	}
	r = bytes.NewReader(dir[68:76]) // mtime
	sec, _ = xdrDecodeUint32(r)
	nsec, _ = xdrDecodeUint32(r)
	return sec, nsec
}

// TestLookupReplyCarriesCurrentDirAttrs: the directory attributes in a
// LOOKUP reply follow changes to the directory. They came from the
// snapshot taken when the directory's handle was made, which never expired.
func TestLookupReplyCarriesCurrentDirAttrs(t *testing.T) {
	e, base := newOSCacheEnv(t, ExportOptions{AttrCacheTimeout: time.Hour})
	dirPath := path.Join(base, "d")
	e.mkdir(dirPath)
	dh := e.handle(dirPath)

	time.Sleep(20 * time.Millisecond)
	if _, err := e.nfs.Create(e.lookup(dirPath), "x", &NFSAttrs{Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	info, err := e.fs.SymlinkFileSystem.Lstat(dirPath)
	if err != nil {
		t.Fatal(err)
	}

	var req bytes.Buffer
	xdrEncodeFileHandle(&req, dh)
	xdrEncodeString(&req, "x")
	reply, err := e.h.handleLookup(&req, &RPCReply{}, e.auth)
	if err != nil {
		t.Fatal(err)
	}
	sec, nsec := lookupReplyDirMtime(t, reply)
	want := info.ModTime()
	if sec != uint32(want.Unix()) || nsec != uint32(want.Nanosecond()) {
		t.Errorf("LOOKUP reports dir mtime %d.%09d, directory has %d.%09d", sec, nsec, want.Unix(), want.Nanosecond())
	}
}

// TestLookupInVanishedDirectory: once the directory behind a handle is
// gone (and its cached attributes have expired), LOOKUP reports STALE with
// the attributes the handle last had: the handle's own object no longer
// exists (RFC 1813), which NOENT would misreport as a missing name. A regression guard for currentAttrs's
// fallback; it passes on the code before it too.
func TestLookupInVanishedDirectory(t *testing.T) {
	e := newCacheEnv(t, ExportOptions{})
	e.mkdir("/d")
	dh := e.handle("/d")
	if err := e.fs.SymlinkFileSystem.Remove("/d"); err != nil {
		t.Fatal(err)
	}
	e.nfs.attrCache.Clear() // as when the TTL has passed

	var req bytes.Buffer
	xdrEncodeFileHandle(&req, dh)
	xdrEncodeString(&req, "x")
	reply, err := e.h.handleLookup(&req, &RPCReply{}, e.auth)
	if err != nil {
		t.Fatal(err)
	}
	data := getReplyData(reply)
	if status := readStatusFromReply(reply); status != NFSERR_STALE {
		t.Fatalf("LOOKUP status %d, want NFSERR_STALE", status)
	}
	if len(data) != 4+4+84 || binary.BigEndian.Uint32(data[4:8]) != 1 {
		t.Fatalf("LOOKUP reply is %d bytes, want status and the directory's attributes", len(data))
	}
	if ftype := binary.BigEndian.Uint32(data[8:12]); ftype != NF3DIR {
		t.Errorf("directory attributes report type %d, want NF3DIR", ftype)
	}
}

// TestCacheSavesBackendStats counts the backend stats a client's usual
// revalidation traffic (GETATTR, ACCESS, LOOKUP) costs. GETATTR, ACCESS
// and the looked-up name are served from the cache; LOOKUP stats its
// directory every time, because whether it is still a directory (not a
// link swapped in for one) is never decided from cached attributes.
func TestCacheSavesBackendStats(t *testing.T) {
	e := newCacheEnv(t, ExportOptions{AttrCacheTimeout: time.Minute})
	e.writeFile("/f", "hello")
	fh, rh := e.handle("/f"), e.handle("/")

	const rounds = 50
	before := e.fs.lstats.Load() + e.fs.stats.Load()
	for i := 0; i < rounds; i++ {
		var getattr, access, lookup bytes.Buffer
		xdrEncodeFileHandle(&getattr, fh)
		xdrEncodeFileHandle(&access, fh)
		xdrEncodeUint32(&access, ACCESS3_READ)
		xdrEncodeFileHandle(&lookup, rh)
		xdrEncodeString(&lookup, "f")
		for name, call := range map[string]func() (*RPCReply, error){
			"GETATTR": func() (*RPCReply, error) { return e.h.handleGetattr(&getattr, &RPCReply{}, e.auth) },
			"ACCESS":  func() (*RPCReply, error) { return e.h.handleAccess(&access, &RPCReply{}, e.auth) },
			"LOOKUP":  func() (*RPCReply, error) { return e.h.handleLookup(&lookup, &RPCReply{}, e.auth) },
		} {
			reply, err := call()
			if err != nil || readStatusFromReply(reply) != NFS_OK {
				t.Fatalf("%s: %v, status %d", name, err, readStatusFromReply(reply))
			}
		}
	}
	calls := e.fs.lstats.Load() + e.fs.stats.Load() - before
	t.Logf("%d GETATTR+ACCESS+LOOKUP rounds within the TTL: %d backend stat calls", rounds, calls)
	if calls > rounds+2 {
		t.Errorf("%d rounds made %d backend stat calls, want at most one per LOOKUP's directory and one per object (%d)", rounds, calls, rounds+2)
	}
}

// TestCacheCoherentUnderConcurrentMutation runs lookups, handle use,
// listings and mutations of a small tree concurrently (run it with -race)
// and then checks that every answer the caches still hold matches the
// filesystem and that every handle names the path it is recorded under.
// TTLs are an hour, so only invalidation can keep them right.
func TestCacheCoherentUnderConcurrentMutation(t *testing.T) {
	e, base := newOSCacheEnv(t, ExportOptions{
		AttrCacheTimeout:     time.Hour,
		CacheNegativeLookups: true,
		NegativeCacheTimeout: time.Hour,
		EnableDirCache:       true,
		DirCacheTimeout:      time.Hour,
	})
	ofs := e.fs.SymlinkFileSystem
	e.mkdir(path.Join(base, "d0"))
	e.mkdir(path.Join(base, "d1"))
	dirs := []string{"d0", "d1", "d2"}
	files := []string{"f0", "f1", "f2"}
	var all []string
	for _, d := range dirs {
		all = append(all, path.Join(base, d))
		for _, f := range files {
			all = append(all, path.Join(base, d, f))
		}
	}
	pick := func(r *rand.Rand, s []string) string { return s[r.Intn(len(s))] }

	const workers, iterations = 8, 300
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < iterations; i++ {
				d, f := pick(r, dirs), pick(r, files)
				p := path.Join(base, d, f)
				dir, dirErr := e.nfs.Lookup(path.Join(base, d))
				switch r.Intn(8) {
				case 0, 1:
					if n, err := e.nfs.Lookup(p); err == nil {
						if held, st := e.h.lookupNode(e.nfs.fileMap.issue(n)); st == NFS_OK {
							e.nfs.GetAttr(held)
						}
					}
				case 2:
					if n, err := e.nfs.Lookup(p); err == nil && r.Intn(2) == 0 {
						e.nfs.Write(n, int64(r.Intn(64)), []byte("data"))
					} else if err == nil {
						n.WriteAt([]byte("node"), int64(r.Intn(64)))
					}
				case 3:
					if dirErr == nil {
						e.nfs.Create(dir, f, &NFSAttrs{Mode: 0o644})
					}
				case 4:
					if dirErr == nil {
						e.nfs.Remove(dir, f)
					}
				case 5:
					if to, err := e.nfs.Lookup(path.Join(base, pick(r, dirs))); dirErr == nil && err == nil {
						e.nfs.Rename(dir, f, to, pick(r, files))
					}
				case 6:
					if dirErr == nil {
						e.nfs.ReadDirPlus(dir)
					}
				case 7:
					if top, err := e.nfs.Lookup(base); err == nil {
						e.nfs.Rename(top, d, top, pick(r, dirs))
					}
				}
			}
		}(int64(w))
	}
	wg.Wait()

	for _, p := range all {
		info, statErr := ofs.Lstat(p)
		attrs, found := e.nfs.attrCache.Get(p)
		switch {
		case !found:
		case attrs == nil && statErr == nil:
			t.Errorf("%s: cached as missing, but it exists", p)
		case attrs != nil && statErr != nil:
			t.Errorf("%s: cached as existing, but Lstat says %v", p, statErr)
		case attrs != nil:
			fresh := e.nfs.attrsFromInfo(p, info)
			if attrs.Size != fresh.Size || attrs.Mode != fresh.Mode || !attrs.Mtime().Equal(fresh.Mtime()) {
				t.Errorf("%s: cached size %d mode %v mtime %v; filesystem has %d %v %v",
					p, attrs.Size, attrs.Mode, attrs.Mtime(), fresh.Size, fresh.Mode, fresh.Mtime())
			}
		}
	}
	e.nfs.fileMap.RLock()
	for p, h := range e.nfs.fileMap.paths {
		if ent := e.nfs.fileMap.handles[h]; ent == nil || ent.path != p || ent.node.path != nodePath(p) {
			t.Errorf("handle %x is recorded for %s but names %v", h, p, ent)
		}
	}
	e.nfs.fileMap.RUnlock()
	for _, d := range dirs {
		p := path.Join(base, d)
		cached, ok := e.nfs.dirCache.Get(p)
		if !ok {
			continue
		}
		entries, err := ofs.ReadDir(p)
		if err != nil {
			t.Errorf("%s: listing cached, but ReadDir says %v", p, err)
			continue
		}
		var got, want []string
		for _, c := range cached {
			if c.Name() != "." && c.Name() != ".." {
				got = append(got, c.Name())
			}
		}
		for _, en := range entries {
			want = append(want, en.Name())
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s: cached listing %v, filesystem has %v", p, got, want)
		}
	}
}

// BenchmarkGetattrHandler measures GETATTR of one file, as clients send
// to revalidate their caches, and reports the backend stat calls per op.
func BenchmarkGetattrHandler(b *testing.B) {
	e := newCacheEnv(b, ExportOptions{})
	e.writeFile("/f", "hello")
	fh := e.handle("/f")
	var req bytes.Buffer
	xdrEncodeFileHandle(&req, fh)
	args := req.Bytes()
	before := e.fs.lstats.Load()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.h.handleGetattr(bytes.NewReader(args), &RPCReply{}, e.auth)
	}
	b.ReportMetric(float64(e.fs.lstats.Load()-before)/float64(b.N), "lstat/op")
}

// BenchmarkLookupHandler measures LOOKUP of one name in one directory.
func BenchmarkLookupHandler(b *testing.B) {
	e := newCacheEnv(b, ExportOptions{})
	e.mkdir("/d")
	e.writeFile("/d/f", "hello")
	var req bytes.Buffer
	xdrEncodeFileHandle(&req, e.handle("/d"))
	xdrEncodeString(&req, "f")
	args := req.Bytes()
	before := e.fs.lstats.Load()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.h.handleLookup(bytes.NewReader(args), &RPCReply{}, e.auth)
	}
	b.ReportMetric(float64(e.fs.lstats.Load()-before)/float64(b.N), "lstat/op")
}

// BenchmarkRenameFullCache measures RENAME of a file while the attribute
// cache holds its default 10000 entries, the cost of subtree invalidation.
func BenchmarkRenameFullCache(b *testing.B) {
	e := newCacheEnv(b, ExportOptions{})
	e.mkdir("/d")
	for i := 0; i < 9990; i++ {
		e.nfs.attrCache.Put(fmt.Sprintf("/d/x%05d", i), &NFSAttrs{Mode: 0o644})
	}
	e.writeFile("/d/a", "x")
	dir := e.lookup("/d")
	names := [2]string{"a", "b"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := e.nfs.Rename(dir, names[i%2], dir, names[(i+1)%2]); err != nil {
			b.Fatal(err)
		}
	}
}

// TestCachePutAtDropsResultsReadBeforeAnInvalidation: a result read before
// any invalidation is not stored after it; one read after is.
func TestCachePutAtDropsResultsReadBeforeAnInvalidation(t *testing.T) {
	c := NewAttrCache(time.Hour, 10)
	c.ConfigureNegativeCaching(true, time.Hour)
	attrs := &NFSAttrs{Mode: 0o644, Size: 1}

	gen := c.generation()
	c.Invalidate("/unrelated")
	c.putAt("/f", attrs, gen)
	c.putNegativeAt("/missing", gen)
	if _, ok := c.Get("/f"); ok {
		t.Errorf("putAt stored attributes read before an invalidation")
	}
	if _, ok := c.Get("/missing"); ok {
		t.Errorf("putNegativeAt stored a miss observed before an invalidation")
	}

	gen = c.generation()
	c.putAt("/f", attrs, gen)
	c.putNegativeAt("/missing", gen)
	if got, ok := c.Get("/f"); !ok || got == nil || got.Size != 1 {
		t.Errorf("putAt with the current generation: %v, %v", got, ok)
	}
	if got, ok := c.Get("/missing"); !ok || got != nil {
		t.Errorf("putNegativeAt with the current generation: %v, %v", got, ok)
	}

	d := NewDirCache(time.Hour, 10, 10)
	gen = d.generation()
	d.Clear()
	d.putAt("/d", nil, gen)
	if _, ok := d.Get("/d"); ok {
		t.Errorf("DirCache.putAt stored a listing read before an invalidation")
	}
	d.putAt("/d", nil, d.generation())
	if _, ok := d.Get("/d"); !ok {
		t.Errorf("DirCache.putAt with the current generation stored nothing")
	}
}

// TestCacheInvalidateTree: a subtree goes, its siblings with a common
// name prefix stay, and the root's tree is everything.
func TestCacheInvalidateTree(t *testing.T) {
	paths := []string{"/a", "/a/b", "/a/b/c", "/ab", "/ab/c", "/x"}
	c := NewAttrCache(time.Hour, 100)
	d := NewDirCache(time.Hour, 100, 10)
	for _, p := range paths {
		c.Put(p, &NFSAttrs{Mode: os.ModeDir | 0o755})
		d.Put(p, nil)
	}
	c.invalidateTree("/a", "/x")
	d.invalidateTree("/a", "/x")
	for _, p := range paths {
		_, attrCached := c.Get(p)
		_, dirCached := d.Get(p)
		want := p == "/ab" || p == "/ab/c"
		if attrCached != want || dirCached != want {
			t.Errorf("%s after invalidating /a and /x: attrs cached %v, listing cached %v, want %v", p, attrCached, dirCached, want)
		}
	}
	c.invalidateTree("/")
	d.invalidateTree("/")
	if c.Size() != 0 || d.Size() != 0 {
		t.Errorf("after invalidating /: %d attribute and %d listing entries remain", c.Size(), d.Size())
	}
}

// TestDirCacheTTLChangeAppliesToExistingEntries: as for attributes.
func TestDirCacheTTLChangeAppliesToExistingEntries(t *testing.T) {
	d := NewDirCache(time.Hour, 10, 10)
	d.Put("/d", nil)
	d.UpdateTTL(time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	if _, ok := d.Get("/d"); ok {
		t.Errorf("listing cached under a 1h TTL still served after lowering it to 1ms")
	}
}

// TestSetAttrPartialFailureInvalidates: when SETATTR fails after applying
// part of the change, the cached attributes do not hide the part applied.
func TestSetAttrPartialFailureInvalidates(t *testing.T) {
	e := newCacheEnv(t, ExportOptions{AttrCacheTimeout: time.Hour})
	e.writeFile("/f", "x")
	node := e.lookup("/f")
	e.nfs.fs = &faHookFS{SymlinkFileSystem: e.fs, chownErr: os.ErrPermission}

	want := *node.attrs
	want.Mode = 0o600
	want.Uid++
	if err := e.nfs.SetAttr(node, &want); err == nil {
		t.Fatal("SetAttr succeeded although chown fails")
	}
	if got := e.lookup("/f").attrs.Mode.Perm(); got != 0o600 {
		t.Errorf("after chmod succeeded and chown failed LOOKUP reports mode %v, want 0600", got)
	}
}
