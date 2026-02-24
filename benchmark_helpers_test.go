package absnfs

import (
	"fmt"
	"testing"
	"time"

	"github.com/absfs/absfs"
	"github.com/absfs/memfs"
)

// benchNFS creates a minimal AbsfsNFS server backed by memfs for benchmarks.
func benchNFS(b *testing.B) *AbsfsNFS {
	b.Helper()
	fs, err := memfs.NewFS()
	if err != nil {
		b.Fatal(err)
	}
	config := DefaultRateLimiterConfig()
	nfs, err := New(fs, ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { nfs.Close() })
	return nfs
}

// benchNFSWithFiles creates an AbsfsNFS with n pre-created files under /bench/.
func benchNFSWithFiles(b *testing.B, n int) *AbsfsNFS {
	b.Helper()
	nfs := benchNFS(b)
	if err := nfs.fs.Mkdir("/bench", 0755); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("/bench/file_%d", i)
		f, err := nfs.fs.Create(name)
		if err != nil {
			b.Fatal(err)
		}
		f.Write([]byte("benchmark content"))
		f.Close()
	}
	return nfs
}

// benchFileHandleMap creates a standalone FileHandleMap for benchmarks.
func benchFileHandleMap(b *testing.B) *FileHandleMap {
	b.Helper()
	return &FileHandleMap{
		handles:     make(map[uint64]absfs.File),
		pathHandles: make(map[string]uint64),
		nextHandle:  1,
		freeHandles: NewUint64MinHeap(),
		maxHandles:  DefaultMaxHandles,
	}
}

// benchAttrCache creates a new AttrCache with the given TTL and max size.
func benchAttrCache(ttl time.Duration, maxSize int) *AttrCache {
	return NewAttrCache(ttl, maxSize)
}

// benchAttrs returns a sample NFSAttrs suitable for cache benchmarks.
func benchAttrs() *NFSAttrs {
	a := &NFSAttrs{
		Mode: 0644,
		Size: 1024,
	}
	a.Refresh()
	return a
}
