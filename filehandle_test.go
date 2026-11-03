package absnfs

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"testing"
)

func fileNode(p string) *NFSNode {
	return &NFSNode{path: p, attrs: &NFSAttrs{Mode: 0o644}}
}

func dirNode(p string) *NFSNode {
	return &NFSNode{path: p, attrs: &NFSAttrs{Mode: os.ModeDir | 0o755}, children: map[string]*NFSNode{}}
}

func TestIssueGivesOneHandlePerObject(t *testing.T) {
	fm := newFileHandleMap(0)
	a := fm.issue(fileNode("/a"))
	if again := fm.issue(fileNode("/a")); !bytes.Equal(a, again) {
		t.Errorf("second issue for /a: %x, want %x", again, a)
	}
	if b := fm.issue(fileNode("/b")); bytes.Equal(a, b) {
		t.Error("/a and /b got the same handle")
	}
	// The same path as another type is another object.
	if d := fm.issue(dirNode("/a")); bytes.Equal(a, d) {
		t.Error("a file and a directory at /a got the same handle")
	}
	if fm.Count() != 3 {
		t.Errorf("Count = %d, want 3", fm.Count())
	}
	// The cache keeps the newest node for the path (its attributes).
	newer := fileNode("/b")
	fm.issue(newer)
	fm.RLock()
	got := fm.handles[fm.paths["b"]].node
	fm.RUnlock()
	if got != newer {
		t.Error("issue did not keep the newer node")
	}
	// Two maps (two servers, or one restarted) issue different handles.
	if other := newFileHandleMap(0).issue(fileNode("/a")); bytes.Equal(a, other) {
		t.Error("two keys gave the same handle")
	}
}

// An unspellable path (a NUL byte, which no NFS name has) still gets a
// handle, resolvable from the cache only.
func TestIssueOpaqueForUnspellablePath(t *testing.T) {
	fm := newFileHandleMap(0)
	fh := fm.issue(fileNode("/a\x00b"))
	ph, ok := parseHandle(fh)
	if !ok || ph.kind != fhKindOpaque {
		t.Fatalf("handle %x: kind %d, ok %v; want opaque", fh, ph.kind, ok)
	}
	if again := fm.issue(fileNode("/a\x00b")); !bytes.Equal(fh, again) {
		t.Error("the opaque handle is not reissued while cached")
	}
}

func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	fm := newFileHandleMap(10)
	busy := fm.issue(fileNode("/busy"))
	for i := 0; i < 100; i++ {
		fm.issue(fileNode(fmt.Sprintf("/f%d", i)))
		fm.cached(busy) // a client keeps using it
		if fm.Count() > 10 {
			t.Fatalf("%d handles cached, limit 10", fm.Count())
		}
	}
	if e, _, _, _ := fm.cached(busy); e == nil {
		t.Error("the handle in use was evicted")
	}
	if e, _, _, _ := fm.cached(fm.codec.encode(fhTypeRegular, 0, "f0")); e != nil {
		t.Error("the oldest unused handle is still cached")
	}
	fm.RLock()
	if len(fm.clock)-fm.hand > 2*len(fm.handles)+64 {
		t.Errorf("clock queue of %d for %d handles", len(fm.clock)-fm.hand, len(fm.handles))
	}
	fm.RUnlock()
}

func TestHandleCacheResize(t *testing.T) {
	fm := newFileHandleMap(100)
	for i := 0; i < 100; i++ {
		fm.issue(fileNode(fmt.Sprintf("/f%d", i)))
	}
	fm.Resize(10)
	if n := fm.Count(); n > 10 {
		t.Errorf("Count after Resize(10) = %d", n)
	}
	fm.Resize(0)
	if fm.limit() != DefaultMaxHandles {
		t.Errorf("limit after Resize(0) = %d", fm.limit())
	}
	fm.RLock()
	for p, key := range fm.paths {
		if e := fm.handles[key]; e == nil || e.path != p {
			t.Errorf("path %s maps to a dropped handle", p)
		}
	}
	fm.RUnlock()
}

func TestCacheRenamedAndRemoved(t *testing.T) {
	fm := newFileHandleMap(0)
	src := fm.issue(fileNode("/d/src"))
	dst := fm.issue(fileNode("/d/dst"))
	e := fm.log.commitRename(fm.log.begin("d/src", "d/dst"), true, false, "d/src", "d/dst")
	fm.renamed("d/src", "d/dst", e)

	// LOOKUP of the new name gives the bytes the client holds.
	if got := fm.issue(fileNode("/d/dst")); !bytes.Equal(got, src) {
		t.Errorf("handle of /d/dst after the rename: %x, want the moved %x", got, src)
	}
	if ent, node, p, _ := fm.cached(src); ent == nil || p != "d/dst" || node.path != "/d/dst" {
		t.Errorf("moved entry: %v %v %q", ent, node, p)
	}
	if ent, _, _, _ := fm.cached(dst); ent != nil {
		t.Error("the replaced target's handle is still cached")
	}
	// The new object at the old name gets a new handle.
	if got := fm.issue(fileNode("/d/src")); bytes.Equal(got, src) {
		t.Error("a new /d/src got the moved handle")
	}

	fm.log.commitRemove(fm.log.begin("d/dst"), true, "d/dst")
	fm.removed("d/dst")
	if ent, _, _, _ := fm.cached(src); ent != nil {
		t.Error("the removed object's handle is still cached")
	}
	fm.renamed("none", "other", 1) // nothing cached: nothing happens
}

func TestCacheReleaseAll(t *testing.T) {
	fm := newFileHandleMap(0)
	a := fm.issue(fileNode("/a"))
	fm.ReleaseAll()
	if fm.Count() != 0 {
		t.Errorf("Count after ReleaseAll = %d", fm.Count())
	}
	if again := fm.issue(fileNode("/a")); !bytes.Equal(a, again) {
		t.Error("the handle changed after ReleaseAll")
	}
}

// The uint64 methods are deprecated shims over the byte handles.
func TestDeprecatedUint64Methods(t *testing.T) {
	fm := newFileHandleMap(0)
	node := fileNode("/a")
	id := fm.Allocate(node)
	fh := fm.issue(node)
	if id != binary.BigEndian.Uint64(fh[:8]) {
		t.Errorf("Allocate = %x, want the handle's first 8 bytes %x", id, fh[:8])
	}
	if f, ok := fm.Get(id); !ok || f != node {
		t.Errorf("Get = %v, %v", f, ok)
	}
	if f, err := fm.GetOrError(id); err != nil || f != node {
		t.Errorf("GetOrError = %v, %v", f, err)
	}
	fm.Release(id)
	if _, ok := fm.Get(id); ok {
		t.Error("Get finds a released handle")
	}
	_, err := fm.GetOrError(id)
	if ie, ok := err.(*InvalidFileHandleError); !ok || ie.Handle != id {
		t.Errorf("GetOrError of a released handle: %v", err)
	}
	if fm.Allocate(nil) != 0 || fm.Allocate(&NFSNode{}) != 0 {
		t.Error("Allocate of a non-node is not 0")
	}
}

func BenchmarkIssueCached(b *testing.B) {
	fm := newFileHandleMap(0)
	node := fileNode("/staging/src/k8s.io/client-go/tools/cache/shared_informer.go")
	fm.issue(node)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fm.issue(node)
	}
}

// BenchmarkIssuePastLimit issues handles for new paths in a cache held at
// DefaultMaxHandles, so every issue evicts.
func BenchmarkIssuePastLimit(b *testing.B) {
	fm := newFileHandleMap(0)
	for i := 0; i < DefaultMaxHandles; i++ {
		fm.issue(fileNode(fmt.Sprintf("/pre%d", i)))
	}
	nodes := make([]*NFSNode, b.N)
	for i := range nodes {
		nodes[i] = fileNode(fmt.Sprintf("/n%d", i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fm.issue(nodes[i])
	}
}
