package absnfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/absfs/absfs"
	"github.com/absfs/basefs"
	"github.com/absfs/lockfs"
	"github.com/absfs/memfs"
	"github.com/absfs/osfs"
	"github.com/absfs/switchfs"
	"github.com/absfs/unionfs"
)

// fhEnv drives a server through its procedure handlers, as a client does.
type fhEnv struct {
	t    *testing.T
	fs   absfs.SymlinkFileSystem
	nfs  *AbsfsNFS
	h    *NFSProcedureHandler
	auth *AuthContext
}

func newFhEnv(t *testing.T, fs absfs.SymlinkFileSystem, opts ExportOptions) *fhEnv {
	t.Helper()
	nfs, err := New(fs, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nfs.Close() })
	return &fhEnv{
		t: t, fs: fs, nfs: nfs,
		h: &NFSProcedureHandler{server: &Server{handler: nfs, options: ServerOptions{}}},
		auth: &AuthContext{
			ClientIP: "127.0.0.1", ClientPort: 1023,
			Credential:   &RPCCredential{Flavor: AUTH_SYS},
			EffectiveUID: nfs.owner.UID, EffectiveGID: nfs.owner.GID,
		},
	}
}

func (e *fhEnv) mount(p string) []byte {
	e.t.Helper()
	var body bytes.Buffer
	xdrEncodeString(&body, p)
	call := &RPCCall{Header: RPCMsgHeader{Program: MOUNT_PROGRAM, Version: MOUNT_V3, Procedure: 1}}
	r, err := e.h.handleMountCall(call, &body, &RPCReply{}, e.auth)
	if err != nil {
		e.t.Fatal(err)
	}
	f := newFaReader(e.t, r)
	if st := f.status(); st != 0 {
		e.t.Fatalf("MNT %s: status %d", p, st)
	}
	return f.fh()
}

func (e *fhEnv) lookup(dir []byte, name string) ([]byte, uint32) {
	e.t.Helper()
	r, err := e.h.handleLookup(bytes.NewReader(buildLookupRequest(dir, name)), &RPCReply{}, e.auth)
	if err != nil {
		e.t.Fatal(err)
	}
	f := newFaReader(e.t, r)
	if st := f.status(); st != NFS_OK {
		return nil, st
	}
	return f.fh(), NFS_OK
}

// walk looks p up component by component from the root's handle.
func (e *fhEnv) walk(root []byte, p string) []byte {
	e.t.Helper()
	fh := root
	for _, name := range strings.Split(strings.Trim(p, "/"), "/") {
		var st uint32
		if fh, st = e.lookup(fh, name); st != NFS_OK {
			e.t.Fatalf("LOOKUP %s in %s: status %d", name, p, st)
		}
	}
	return fh
}

func (e *fhEnv) getattr(fh []byte) (uint32, faFattr) {
	e.t.Helper()
	var body bytes.Buffer
	xdrEncodeFileHandle(&body, fh)
	r, err := e.h.handleGetattr(&body, &RPCReply{}, e.auth)
	if err != nil {
		e.t.Fatal(err)
	}
	f := newFaReader(e.t, r)
	st := f.status()
	if st != NFS_OK {
		return st, faFattr{}
	}
	return st, f.fattr()
}

func (e *fhEnv) dirOp(fn func(io.Reader, *RPCReply, *AuthContext) (*RPCReply, error), body []byte) uint32 {
	e.t.Helper()
	r, err := fn(bytes.NewReader(body), &RPCReply{}, e.auth)
	if err != nil {
		e.t.Fatal(err)
	}
	return newFaReader(e.t, r).status()
}

func (e *fhEnv) remove(dir []byte, name string) uint32 {
	return e.dirOp(e.h.handleRemove, buildRemoveRequest(dir, name))
}

func (e *fhEnv) rmdir(dir []byte, name string) uint32 {
	return e.dirOp(e.h.handleRmdir, buildRemoveRequest(dir, name))
}

func (e *fhEnv) rename(from []byte, fromName string, to []byte, toName string) uint32 {
	return e.dirOp(e.h.handleRename, buildRenameRequest(from, fromName, to, toName))
}

// size checks that fh resolves to a regular file of the given size (the
// tests give every object a distinct size, so this names the object).
func (e *fhEnv) size(fh []byte, want int, what string) {
	e.t.Helper()
	st, a := e.getattr(fh)
	if st != NFS_OK || a.Size != uint64(want) {
		e.t.Errorf("%s: GETATTR status %d size %d, want NFS_OK and %d", what, st, a.Size, want)
	}
}

func (e *fhEnv) stale(fh []byte, what string) {
	e.t.Helper()
	if st, a := e.getattr(fh); st != NFSERR_STALE {
		e.t.Errorf("%s: GETATTR status %d (size %d), want NFSERR_STALE", what, st, a.Size)
	}
}

func writeFile(t *testing.T, fs absfs.SymlinkFileSystem, p string, size int) {
	t.Helper()
	f, err := fs.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(bytes.Repeat([]byte{'x'}, size)); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func mustMkdir(t *testing.T, fs absfs.SymlinkFileSystem, p string) {
	t.Helper()
	if err := fs.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func newMemFS(t *testing.T) absfs.SymlinkFileSystem {
	t.Helper()
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func newUnionFS(t *testing.T) absfs.SymlinkFileSystem {
	t.Helper()
	lower, upper := newMemFS(t), newMemFS(t)
	return unionfs.New(unionfs.WithWritableLayer(upper), unionfs.WithReadOnlyLayer(lower)).SymlinkFileSystem()
}

// populate makes dirs*files files, the file i of directory d of size
// 1+d*files+i, with names long enough that some handles hash their tail.
func populate(t *testing.T, fs absfs.SymlinkFileSystem, dirs, files int) []string {
	t.Helper()
	var paths []string
	for d := 0; d < dirs; d++ {
		dir := fmt.Sprintf("/ws/pkg%03d/internal/generated", d)
		mustMkdir(t, fs, dir)
		for i := 0; i < files; i++ {
			name := fmt.Sprintf("%s/f%04d.go", dir, i)
			if i%3 == 0 {
				name = fmt.Sprintf("%s/zz_generated_deepcopy_of_a_rather_long_type_name_%04d_test.go", dir, i)
			}
			writeFile(t, fs, name, 1+d*files+i)
			paths = append(paths, name)
		}
	}
	return paths
}

// TestHandlesResolveAfterEviction is the c4d failure: a client holds more
// handles than the server caches. With a cache of 10, every handle still
// resolves to its object, and a new LOOKUP returns the same bytes, so the
// client's vnode and attribute cache stay valid.
func TestHandlesResolveAfterEviction(t *testing.T) {
	for name, mk := range map[string]func(*testing.T) absfs.SymlinkFileSystem{"memfs": newMemFS, "unionfs": newUnionFS} {
		t.Run(name, func(t *testing.T) {
			fs := mk(t)
			dirs, files := 100, 100
			if testing.Short() {
				dirs = 20
			}
			paths := populate(t, fs, dirs, files)
			e := newFhEnv(t, fs, ExportOptions{MaxHandles: 10})
			root := e.mount("/")
			handles := make([][]byte, len(paths))
			for i, p := range paths {
				handles[i] = e.walk(root, p)
			}
			if n := e.nfs.fileMap.Count(); n > 10 {
				t.Fatalf("%d handles cached, MaxHandles 10", n)
			}
			kinds := map[uint8]int{}
			for i, fh := range handles {
				ph, _ := parseHandle(fh)
				kinds[ph.kind]++
				e.size(fh, 1+i, paths[i])
			}
			if kinds[fhKindInline] == 0 || kinds[fhKindTail] == 0 {
				t.Errorf("handle kinds %v: want inline and tail handles", kinds)
			}
			for i := 0; i < len(paths); i += 97 {
				if again := e.walk(root, paths[i]); !bytes.Equal(again, handles[i]) {
					t.Errorf("%s: LOOKUP after eviction gave %x, first %x", paths[i], again, handles[i])
				}
			}
			e.size(root, 0, "root") // the mount's handle resolves too
			m := e.nfs.GetMetrics()
			if m.HandleCacheMisses == 0 || m.HandleCacheCapacity != 10 || m.HandleCacheSize > 10 || m.HandleTailWalkFailures != 0 {
				t.Errorf("metrics: %d misses, size %d, capacity %d, tail failures %d",
					m.HandleCacheMisses, m.HandleCacheSize, m.HandleCacheCapacity, m.HandleTailWalkFailures)
			}
		})
	}
}

// Remove and recreate, rename over a target, and RMDIR through the server
// make old handles stale, even once the cache has forgotten them.
func TestHandleSemanticsAfterEviction(t *testing.T) {
	fs := newMemFS(t)
	mustMkdir(t, fs, "/d/sub")
	writeFile(t, fs, "/d/f", 1)
	writeFile(t, fs, "/d/target", 2)
	writeFile(t, fs, "/d/src", 3)
	e := newFhEnv(t, fs, ExportOptions{})
	root := e.mount("/")
	dir := e.walk(root, "d")
	f, target, src, sub := e.walk(root, "d/f"), e.walk(root, "d/target"), e.walk(root, "d/src"), e.walk(root, "d/sub")

	if st := e.remove(dir, "f"); st != NFS_OK {
		t.Fatalf("REMOVE: %d", st)
	}
	writeFile(t, fs, "/d/f", 4)
	if st := e.rename(dir, "src", dir, "target"); st != NFS_OK {
		t.Fatalf("RENAME: %d", st)
	}
	if st := e.rmdir(dir, "sub"); st != NFS_OK {
		t.Fatalf("RMDIR: %d", st)
	}
	mustMkdir(t, fs, "/d/sub")

	for _, evict := range []bool{false, true} {
		if evict {
			e.nfs.fileMap.ReleaseAll()
		}
		e.stale(f, fmt.Sprintf("removed and recreated file (evicted %v)", evict))
		e.stale(target, fmt.Sprintf("rename target (evicted %v)", evict))
		e.stale(sub, fmt.Sprintf("removed and recreated directory (evicted %v)", evict))
		e.size(src, 3, fmt.Sprintf("renamed file (evicted %v)", evict))
	}
	if again := e.walk(root, "d/f"); bytes.Equal(again, f) {
		t.Error("the recreated /d/f got the old handle")
	}
	e.size(e.walk(root, "d/f"), 4, "recreated /d/f")
	// LOOKUP of the new name gives the handle the client already holds
	// while it is cached; after eviction the canonical one, which works.
	if moved := e.walk(root, "d/target"); !bytes.Equal(moved, src) {
		e.size(moved, 3, "LOOKUP of the rename's new name")
	}
}

// git's index update: the client renames the open, deleted index to
// .nfsXXXX, then renames index.lock over index.
func TestSillyRenameAfterEviction(t *testing.T) {
	fs := newMemFS(t)
	mustMkdir(t, fs, "/repo/.git")
	writeFile(t, fs, "/repo/.git/index", 10)
	writeFile(t, fs, "/repo/.git/index.lock", 20)
	e := newFhEnv(t, fs, ExportOptions{MaxHandles: 10})
	root := e.mount("/")
	git := e.walk(root, "repo/.git")
	index, lock := e.walk(root, "repo/.git/index"), e.walk(root, "repo/.git/index.lock")

	if st := e.rename(git, "index", git, ".nfs0001"); st != NFS_OK {
		t.Fatal(st)
	}
	if st := e.rename(git, "index.lock", git, "index"); st != NFS_OK {
		t.Fatal(st)
	}
	e.nfs.fileMap.ReleaseAll()
	e.size(index, 10, "the open index's handle")
	e.size(lock, 20, "index.lock's handle")
	newIndex := e.walk(root, "repo/.git/index")
	if bytes.Equal(newIndex, index) {
		t.Error("the new index got the old index's handle")
	}
	e.size(newIndex, 20, "the new index")
	if st := e.remove(git, ".nfs0001"); st != NFS_OK {
		t.Fatal(st)
	}
	e.stale(index, "the closed .nfs file's handle")
}

// Handles below a renamed directory follow it after eviction; a failed
// RMDIR (git removes directories that are not empty) stales nothing.
func TestDirectoryRenameAfterEviction(t *testing.T) {
	fs := newMemFS(t)
	mustMkdir(t, fs, "/a/b/c")
	writeFile(t, fs, "/a/b/c/leaf_with_a_long_name_that_hashes_its_tail_0123456789.txt", 7)
	writeFile(t, fs, "/a/b/short", 8)
	e := newFhEnv(t, fs, ExportOptions{MaxHandles: 10})
	root := e.mount("/")
	long := e.walk(root, "a/b/c/leaf_with_a_long_name_that_hashes_its_tail_0123456789.txt")
	short := e.walk(root, "a/b/short")
	a := e.walk(root, "a")

	if st := e.rmdir(a, "b"); st != NFSERR_NOTEMPTY {
		t.Fatalf("RMDIR of a non-empty directory: %d", st)
	}
	e.nfs.fileMap.ReleaseAll()
	e.size(long, 7, "after a failed RMDIR")

	if st := e.rename(root, "a", root, "z"); st != NFS_OK {
		t.Fatal(st)
	}
	e.nfs.fileMap.ReleaseAll()
	e.size(long, 7, "tail handle below the renamed directory")
	e.size(short, 8, "inline handle below the renamed directory")

	// A child removed and made again before its parent moved is stale.
	z := e.walk(root, "z/b")
	if st := e.remove(z, "short"); st != NFS_OK {
		t.Fatal(st)
	}
	writeFile(t, fs, "/z/b/short", 9)
	if st := e.rename(root, "z", root, "y"); st != NFS_OK {
		t.Fatal(st)
	}
	e.nfs.fileMap.ReleaseAll()
	e.stale(short, "removed and recreated child of a moved directory")
}

// A changed object type at the same path (outside the server) is stale.
func TestTypeMismatchIsStale(t *testing.T) {
	fs := newMemFS(t)
	writeFile(t, fs, "/x", 1)
	e := newFhEnv(t, fs, ExportOptions{AttrCacheTimeout: time.Nanosecond})
	root := e.mount("/")
	x := e.walk(root, "x")
	fs.Remove("/x")
	mustMkdir(t, fs, "/x")
	e.nfs.fileMap.ReleaseAll()
	e.nfs.attrCache.Clear()
	e.stale(x, "a file replaced by a directory")
}

// ioCountingFS counts the filesystem calls a resolution makes.
type ioCountingFS struct {
	absfs.SymlinkFileSystem
	calls atomic.Int64
}

func (c *ioCountingFS) Lstat(name string) (os.FileInfo, error) {
	c.calls.Add(1)
	return c.SymlinkFileSystem.Lstat(name)
}

func (c *ioCountingFS) Stat(name string) (os.FileInfo, error) {
	c.calls.Add(1)
	return c.SymlinkFileSystem.Stat(name)
}

func (c *ioCountingFS) Open(name string) (absfs.File, error) {
	c.calls.Add(1)
	return c.SymlinkFileSystem.Open(name)
}

func (c *ioCountingFS) OpenFile(name string, flag int, perm os.FileMode) (absfs.File, error) {
	c.calls.Add(1)
	return c.SymlinkFileSystem.OpenFile(name, flag, perm)
}

// Forged, garbled, oversized and old-format handles are stale, and cost
// no filesystem call. (A forged tail handle costs a bounded walk first:
// see TestForgedTailHandleCostIsBounded.)
func TestBadHandlesAreStaleWithoutIO(t *testing.T) {
	cfs := &ioCountingFS{SymlinkFileSystem: newMemFS(t)}
	writeFile(t, cfs, "/file", 1)
	e := newFhEnv(t, cfs, ExportOptions{})
	root := e.mount("/")
	good := e.walk(root, "file")
	other := newFileHandleMap(0).issue(fileNode("/file"))
	flipped := append([]byte(nil), good...)
	flipped[3] ^= 0x10
	padded := append(append([]byte(nil), good...), 0)
	bad := map[string][]byte{
		"empty":       {},
		"legacy":      legacyHandle(0x1812345678abcdef),
		"other key":   other,
		"flipped tag": flipped,
		"trailing":    padded,
		"random":      bytes.Repeat([]byte{0x42}, 40),
	}
	for name, fh := range bad {
		before := cfs.calls.Load()
		e.stale(fh, name)
		if n := cfs.calls.Load() - before; n != 0 {
			t.Errorf("%s: %d filesystem calls", name, n)
		}
	}
	var body bytes.Buffer
	xdrEncodeUint32(&body, 65)
	body.Write(make([]byte, 68))
	r, _ := e.h.handleGetattr(&body, &RPCReply{}, e.auth)
	if st := newFaReader(t, r).status(); st != GARBAGE_ARGS {
		t.Errorf("65-byte handle: status %d, want GARBAGE_ARGS", st)
	}
}

// Tail walks resolve through hash collisions, and give up (stale) past
// their caps. A deep path of short names gets 8-bit hashes, which collide.
func TestTailWalkCollisionsAndCaps(t *testing.T) {
	fs := newMemFS(t)
	var comps []string
	for i := 0; i < 30; i++ {
		comps = append(comps, fmt.Sprintf("q%02d", i))
	}
	dir := "/" + strings.Join(comps, "/")
	mustMkdir(t, fs, dir)
	target := "zzz"
	p := strings.TrimPrefix(dir, "/") + "/" + target

	nfs, err := New(fs, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer nfs.Close()
	c := nfs.fileMap.codec
	// Siblings whose 8-bit hash is the target's, listed before it.
	collisions := 0
	for i := 0; collisions < 20 && i < 26*26*26; i++ {
		n := string([]byte{'a' + byte(i/676), 'a' + byte(i/26%26), 'a' + byte(i%26)})
		if c.tailHash(n, false) == c.tailHash(target, false) {
			writeFile(t, fs, dir+"/"+n, 100+collisions)
			collisions++
		}
	}
	writeFile(t, fs, dir+"/"+target, 1)

	fh := c.encode(fhTypeRegular, 0, p)
	ph, ok := parseHandle(fh)
	if !ok || ph.kind != fhKindTail || ph.wide {
		t.Fatalf("handle of %s: kind %d wide %v, want an 8-bit tail handle", p, ph.kind, ph.wide)
	}
	w := tailWalker{s: nfs, ctx: context.Background(), fh: fh, ph: ph}
	if got, ok := w.walk(ph.path, 0); !ok || got != p {
		t.Fatalf("walk: %q, %v", got, ok)
	}
	if w.checks < 2 {
		t.Errorf("%d tag checks: the collisions were not tried", w.checks)
	}
	if node, st := nfs.resolveHandle(fh); st != NFS_OK || node.path != "/"+p {
		t.Errorf("resolveHandle: %v, status %d", node, st)
	}

	// Past the caps the walk gives up: the handle is stale, and counted.
	defer func(a, b int) { tailMaxChecks, tailMaxListings = a, b }(tailMaxChecks, tailMaxListings)
	tailMaxChecks = 1
	if _, ok := nfs.walkTail(context.Background(), fh, ph); ok {
		t.Error("the walk resolved past its check cap")
	}
	tailMaxChecks, tailMaxListings = 256, 3
	if _, ok := nfs.walkTail(context.Background(), fh, ph); ok {
		t.Error("the walk resolved past its listing cap")
	}
	if nfs.fileMap.tailFailures.Load() != 2 {
		t.Errorf("%d tail walk failures counted, want 2", nfs.fileMap.tailFailures.Load())
	}
}

// A tail handle below a directory renamed through the server follows it,
// and so does one whose leaf was renamed.
func TestTailWalkFollowsRenames(t *testing.T) {
	fs := newMemFS(t)
	long := strings.Repeat("long_component_name_", 3)
	mustMkdir(t, fs, "/a/"+long)
	writeFile(t, fs, "/a/"+long+"/"+long+".txt", 5)
	writeFile(t, fs, "/a/"+long+"/"+long+".dat", 6)
	e := newFhEnv(t, fs, ExportOptions{MaxHandles: 10})
	root := e.mount("/")
	txt := e.walk(root, "a/"+long+"/"+long+".txt")
	dat := e.walk(root, "a/"+long+"/"+long+".dat")
	if ph, _ := parseHandle(txt); ph.kind != fhKindTail {
		t.Fatalf("kind %d, want a tail handle", ph.kind)
	}
	sub := e.walk(root, "a/"+long)
	if st := e.rename(sub, long+".dat", sub, "short"); st != NFS_OK {
		t.Fatal(st)
	}
	if st := e.rename(root, "a", root, "b"); st != NFS_OK {
		t.Fatal(st)
	}
	e.nfs.fileMap.ReleaseAll()
	e.size(txt, 5, "tail handle below a renamed directory")
	e.size(dat, 6, "tail handle of a renamed leaf")
}

// switchFS is a switchfs with a memfs default backend and prefix routes
// that strip their prefix, as c4d serves workspaces.
type switchFS struct {
	fs *switchfs.SwitchFS
}

func newSwitchFS(t *testing.T, routes map[string]absfs.FileSystem) *switchFS {
	t.Helper()
	top := newMemFS(t)
	opts := []switchfs.Option{switchfs.WithDefault(top)}
	for route, backend := range routes {
		mustMkdir(t, top, route) // so listings of its parent show it
		opts = append(opts, switchfs.WithRoute(route, backend, switchfs.WithRewriter(switchfs.StripPrefix(route))))
	}
	sw, err := switchfs.New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	return &switchFS{fs: sw}
}

// swap routes route to backend from now on.
func (s *switchFS) swap(t *testing.T, route string, backend absfs.FileSystem) {
	t.Helper()
	if err := s.fs.RemoveRoute(route, switchfs.PatternPrefix); err != nil {
		t.Fatal(err)
	}
	if err := s.fs.AddRoute(route, backend, switchfs.WithRewriter(switchfs.StripPrefix(route))); err != nil {
		t.Fatal(err)
	}
}

// A switchfs prefix route swapped at runtime to another backend with the
// same tree: cached and evicted handles, inline and tail, resolve in the
// new backend. The tail handle hashes the route's own component, so its
// walk lists the default backend's root and then crosses into the route.
func TestRouteSwapResolvesToNewBackend(t *testing.T) {
	const route = "/workspace_route_of_some_length"
	var deep []string
	for i := 0; i < 20; i++ {
		deep = append(deep, fmt.Sprintf("d%02d", i))
	}
	deepFile := "/" + strings.Join(deep, "/") + "/f"
	one, two := newMemFS(t), newMemFS(t)
	for i, fs := range []absfs.SymlinkFileSystem{one, two} {
		writeFile(t, fs, "/a.txt", 10*(i+1))
		mustMkdir(t, fs, path.Dir(deepFile))
		writeFile(t, fs, deepFile, 10*(i+1)+1)
	}
	sw := newSwitchFS(t, map[string]absfs.FileSystem{route: one})
	e := newFhEnv(t, sw.fs, ExportOptions{MaxHandles: 10})
	root := e.mount("/")
	a := e.walk(root, route+"/a.txt")
	f := e.walk(root, route+deepFile)
	if ph, _ := parseHandle(a); ph.kind != fhKindInline {
		t.Fatalf("handle of a.txt is of kind %d, want inline", ph.kind)
	}
	if ph, _ := parseHandle(f); ph.kind != fhKindTail || ph.path != "" {
		t.Fatalf("handle of the deep file: kind %d, prefix %q; want a tail handle hashing the route", ph.kind, ph.path)
	}
	e.size(a, 10, "inline handle before the swap")
	e.size(f, 11, "tail handle before the swap")

	sw.swap(t, route, two)
	e.nfs.attrCache.Clear()
	e.size(a, 20, "cached inline handle after the swap")
	e.size(f, 21, "cached tail handle after the swap")
	e.nfs.fileMap.ReleaseAll()
	e.nfs.fileMap.dirs = newDirHashIndex(dirIndexLimit)
	e.size(a, 20, "evicted inline handle after the swap")
	e.size(f, 21, "evicted tail handle after the swap")
}

// Handles carry only virtual paths, never where an osfs export lives on
// the host.
func TestHandlesDoNotDiscloseHostPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/secret-host-dir/inner", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/secret-host-dir/inner/f", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ofs, err := osfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}
	fs, err := basefs.NewFS(ofs, filepath.ToSlash(dir))
	if err != nil {
		t.Skipf("basefs over %s: %v", dir, err)
	}
	e := newFhEnv(t, fs, ExportOptions{})
	root := e.mount("/")
	for _, fh := range [][]byte{root, e.walk(root, "secret-host-dir/inner/f")} {
		ph, _ := parseHandle(fh)
		if strings.Contains(string(fh), dir) || strings.Contains(ph.path, dir) || strings.HasPrefix(ph.path, "/") {
			t.Errorf("handle %x (path %q) discloses the host directory %s", fh, ph.path, dir)
		}
	}
}

// TestHandleStress runs RENAME, REMOVE, CREATE, LOOKUP and GETATTR from many
// goroutines against a tiny cache. Once quiet, every handle that resolves
// must reach the object it was issued for (each file's content names it).
func TestHandleStress(t *testing.T) {
	// memfs is not safe for concurrent changes; lockfs makes it so.
	fs, err := lockfs.NewSymlinkFS(newMemFS(t))
	if err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, fs, "/s")
	e := newFhEnv(t, fs, ExportOptions{MaxHandles: 8, AttrCacheTimeout: time.Millisecond})
	root := e.mount("/")
	sdir := e.walk(root, "s")
	var nextID atomic.Int64
	// A new file is written in /stage and moved in only if the name is
	// free (memfs refuses to rename over a file), so it never replaces an
	// object behind the server's back and is complete once visible.
	mustMkdir(t, fs, "/stage")
	create := func(name string) {
		id := nextID.Add(1)
		staged := fmt.Sprintf("/stage/%d", id)
		f, err := fs.OpenFile(staged, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			t.Error(err)
			return
		}
		fmt.Fprintf(f, "%d", id)
		f.Close()
		if fs.Rename(staged, "/s/"+name) == nil {
			e.nfs.invalidate("/s", "/s/"+name)
		}
	}
	names := []string{"a", "b", "c", "d", "index", "index.lock"}
	for _, n := range names {
		create(n)
	}

	type issued struct {
		fh []byte
		id string
	}
	var mu sync.Mutex
	var held []issued
	var wg sync.WaitGroup
	steps := 1000
	if testing.Short() {
		steps = 300
	}
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < steps; i++ {
				n := names[r.Intn(len(names))]
				switch op := r.Intn(20); {
				case op == 0:
					e.rename(sdir, n, sdir, names[r.Intn(len(names))])
				case op == 1:
					e.remove(sdir, n)
				case op < 4:
					create(n)
				case op < 12:
					// Kept only if no REMOVE or RENAME ran meanwhile, so
					// the content read is the looked-up object's.
					seq := e.nfs.fileMap.log.seq.Load()
					if fh, st := e.lookup(sdir, n); st == NFS_OK {
						node, st := e.nfs.resolveHandle(fh)
						if st != NFS_OK {
							continue
						}
						b, err := readAll(fs, node.path)
						// Empty: read before the creator wrote the id.
						if err != nil || b == "" || e.nfs.fileMap.log.seq.Load() != seq || e.nfs.fileMap.log.nInflight.Load() != 0 {
							continue
						}
						mu.Lock()
						held = append(held, issued{fh, b})
						mu.Unlock()
					}
				default:
					mu.Lock()
					var fh []byte
					if len(held) > 0 {
						fh = held[r.Intn(len(held))].fh
					}
					mu.Unlock()
					if fh != nil {
						e.getattr(fh)
					}
				}
			}
		}(int64(g))
	}
	wg.Wait()

	wrong, checked := 0, 0
	for _, h := range held {
		node, st := e.nfs.resolveHandle(h.fh)
		if st != NFS_OK {
			continue
		}
		checked++
		if b, err := readAll(fs, node.path); err == nil && b != h.id {
			wrong++
			t.Errorf("handle issued for object %s resolves to %s holding %s", h.id, node.path, b)
		}
		if wrong > 5 {
			break
		}
	}
	if checked == 0 {
		t.Error("no handle resolved at the end")
	}
	if e.nfs.fileMap.log.nInflight.Load() != 0 {
		t.Error("changes left in flight")
	}
}

func readAll(fs absfs.SymlinkFileSystem, p string) (string, error) {
	f, err := fs.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	return string(b), err
}

// A forged tail handle cannot be found stale before its walk lists the
// directories its prefix names: the tag covers the whole path. The caps
// bound that cost, and an attempt whose listings were all fresh is not
// repeated.
func TestForgedTailHandleCostIsBounded(t *testing.T) {
	cfs := &ioCountingFS{SymlinkFileSystem: newMemFS(t)}
	mustMkdir(t, cfs, "/big")
	for i := 0; i < 300; i++ {
		mustMkdir(t, cfs, fmt.Sprintf("/big/d%d", i))
		writeFile(t, cfs, fmt.Sprintf("/big/d%d/f", i), 1)
	}
	e := newFhEnv(t, cfs, ExportOptions{})
	e.mount("/")
	for _, hb := range []byte{0x00, 0x11, 0x5a, 0xa7, 0xff} {
		fh := []byte{fhVersion1 | fhKindTail<<4 | fhTypeRegular<<2}
		fh = append(fh, 1, 2, 3, 4, 5, 6, 7, 8) // a made-up tag
		fh = append(fh, 2<<2, hb, hb)           // two 8-bit hashes
		fh = appendHuff(fh, "big")
		if ph, ok := parseHandle(fh); !ok || ph.kind != fhKindTail {
			t.Fatal("the forged handle does not parse as a tail handle")
		}
		e.nfs.fileMap.dirs = newDirHashIndex(dirIndexLimit) // nothing cached
		before := cfs.calls.Load()
		e.stale(fh, "forged tail handle")
		calls := cfs.calls.Load() - before
		// Every listing is an Open; a few stats come with them.
		if limit := int64(3 * tailMaxListings); calls > limit {
			t.Errorf("hash %02x: %d filesystem calls, want at most %d", hb, calls, limit)
		}
		// Fresh listings only: the walk is not repeated.
		w := tailWalker{s: e.nfs, ctx: context.Background(), fh: fh}
		w.ph, _ = parseHandle(fh)
		e.nfs.fileMap.dirs = newDirHashIndex(dirIndexLimit)
		w.walk(w.ph.path, 0)
		before = cfs.calls.Load()
		e.nfs.fileMap.dirs = newDirHashIndex(dirIndexLimit)
		e.stale(fh, "forged tail handle")
		if again := cfs.calls.Load() - before; again > int64(w.listings)+1 {
			t.Errorf("hash %02x: %d filesystem calls for %d listings: the walk was repeated", hb, again, w.listings)
		}
	}
}

// slowRenameFS makes Rename take delay.
type slowRenameFS struct {
	absfs.SymlinkFileSystem
	delay time.Duration
}

func (s *slowRenameFS) Rename(a, b string) error {
	time.Sleep(s.delay)
	return s.SymlinkFileSystem.Rename(a, b)
}

// A RENAME in flight longer than inflightWait gives the handles below it
// NFS3ERR_JUKEBOX, so clients retry, never NFS3ERR_STALE: the objects
// still exist. Once the rename commits they follow it.
func TestSlowRenameAnswersJukebox(t *testing.T) {
	defer func(d time.Duration) { inflightWait = d }(inflightWait)
	inflightWait = 100 * time.Millisecond
	sfs := &slowRenameFS{SymlinkFileSystem: newMemFS(t)}
	mustMkdir(t, sfs, "/repo")
	writeFile(t, sfs, "/repo/f", 5)
	writeFile(t, sfs, "/repo/"+strings.Repeat("long_name_", 8), 6)
	e := newFhEnv(t, sfs, ExportOptions{})
	root := e.mount("/")
	cached := e.walk(root, "repo/f")
	evicted := e.walk(root, "repo/"+strings.Repeat("long_name_", 8))
	e.nfs.fileMap.ReleaseAll()
	e.walk(root, "repo/f") // cached again; evicted stays out

	sfs.delay = time.Second
	done := make(chan uint32)
	go func() { done <- e.rename(root, "repo", root, "repo2") }()
	for e.nfs.fileMap.log.nInflight.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	for name, fh := range map[string][]byte{"cached": cached, "evicted": evicted} {
		if st, _ := e.getattr(fh); st != NFSERR_JUKEBOX {
			t.Errorf("%s handle below a rename in flight: status %d, want NFSERR_JUKEBOX", name, st)
		}
	}
	if n := e.nfs.GetMetrics().HandleInflightTimeouts; n != 2 {
		t.Errorf("HandleInflightTimeouts %d, want 2", n)
	}
	if st := <-done; st != NFS_OK {
		t.Fatalf("RENAME: status %d", st)
	}
	e.size(cached, 5, "cached handle after the rename")
	e.size(evicted, 6, "evicted handle after the rename")
}

// READDIRPLUS counts each entry's real size, its handle (10 to 64 bytes)
// and its name, against the client's maxcount: no page is larger, except
// one of a single entry, which is always sent.
func TestReaddirplusPagesFitMaxcount(t *testing.T) {
	fs := newMemFS(t)
	mustMkdir(t, fs, "/d")
	const n = 60
	var names []string
	for i := 0; i < n; i++ {
		long := 14 + i*236/(n-1) // 14 to 250 bytes
		name := fmt.Sprintf("%03d_%s", i, strings.Repeat("qzjx_kv0-9wQ", 25)[i%12:i%12+long-4])
		writeFile(t, fs, "/d/"+name, i)
		names = append(names, name)
	}
	e := newFhEnv(t, fs, ExportOptions{})
	root := e.mount("/")
	d := e.walk(root, "d")
	longest := 0
	for _, name := range names {
		longest = max(longest, len(e.nfs.fileMap.issue(fileNode("/d/"+name))))
	}
	if longest < 56 {
		t.Fatalf("the longest handle is %d bytes; the test wants some near 64", longest)
	}
	for _, maxcount := range []uint32{400, 700, 1024, 2048, 4096} {
		seen := map[string]bool{}
		var cookie uint64
		for pages, eof := 0, false; !eof; pages++ {
			if pages > n {
				t.Fatalf("maxcount %d: the listing does not end", maxcount)
			}
			r, err := e.h.handleReaddirplus(bytes.NewReader(buildReaddirplusRequest(d, cookie, maxcount, maxcount)), &RPCReply{}, e.auth)
			if err != nil {
				t.Fatal(err)
			}
			size := len(r.Data.([]byte))
			f := newFaReader(t, r)
			if st := f.status(); st != NFS_OK {
				t.Fatalf("maxcount %d: status %d", maxcount, st)
			}
			f.postOpAttr()
			f.skip(8) // cookieverf
			entries := f.entries(true)
			eof = f.u32() == 1
			if len(entries) == 0 && !eof {
				t.Fatalf("maxcount %d: an empty page before the end", maxcount)
			}
			if size > int(maxcount) && len(entries) > 1 {
				t.Errorf("maxcount %d: a page of %d entries is %d bytes", maxcount, len(entries), size)
			}
			for _, en := range entries {
				if seen[en.Name] {
					t.Errorf("maxcount %d: %s listed twice", maxcount, en.Name)
				}
				seen[en.Name] = true
				cookie = en.Cookie
			}
		}
		if len(seen) != n {
			t.Errorf("maxcount %d: %d entries listed, want %d", maxcount, len(seen), n)
		}
	}
}
