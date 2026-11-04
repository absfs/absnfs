// case.go: what PATHCONF reports as case_insensitive.
//
// ExportOptions.CaseInsensitive picks the answer: true or false for every
// handle, or (the default) a read-only probe of the filesystem each handle
// is in, cached per directory.
package absnfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/absfs/inode"
)

// CaseInsensitivity is what PATHCONF reports in case_insensitive: whether
// a name finds a file spelled with letters of another case. See
// ExportOptions.CaseInsensitive.
type CaseInsensitivity uint8

const (
	// CaseInsensitiveAuto (the zero value) probes the filesystem each
	// handle is in and reports what it finds.
	CaseInsensitiveAuto CaseInsensitivity = iota
	// CaseInsensitiveTrue reports names as case-insensitive for every
	// handle.
	CaseInsensitiveTrue
	// CaseInsensitiveFalse reports names as case-sensitive for every
	// handle, as absnfs did before CaseInsensitive existed.
	CaseInsensitiveFalse
)

// String returns "auto", "true" or "false".
func (c CaseInsensitivity) String() string {
	switch c {
	case CaseInsensitiveAuto:
		return "auto"
	case CaseInsensitiveTrue:
		return "true"
	case CaseInsensitiveFalse:
		return "false"
	}
	return fmt.Sprintf("CaseInsensitivity(%d)", uint8(c))
}

const (
	// caseProbeNames is how many names of a directory a probe reads.
	caseProbeNames = 64
	// caseProbeLookups is how many of them a probe looks up under a
	// case-swapped spelling, at most.
	caseProbeLookups = 4
	// caseCacheMax caps the directories whose answer is cached.
	caseCacheMax = 4096
)

// caseCache holds the probed answer for directories, each until it expires.
type caseCache struct {
	mu sync.Mutex
	m  map[string]caseAnswer
}

type caseAnswer struct {
	folds   bool
	expires time.Time
}

func (c *caseCache) get(dir string, now time.Time) (folds, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	a, ok := c.m[dir]
	if !ok || now.After(a.expires) {
		return false, false
	}
	return a.folds, true
}

func (c *caseCache) put(dir string, folds bool, expires time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= caseCacheMax {
		c.m = make(map[string]caseAnswer)
	}
	c.m[dir] = caseAnswer{folds: folds, expires: expires}
}

// forget drops the answers for paths, and with tree for everything below
// them too.
func (c *caseCache) forget(tree bool, paths ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) == 0 {
		return
	}
	for _, p := range paths {
		delete(c.m, p)
		if !tree {
			continue
		}
		prefix := strings.TrimSuffix(p, "/") + "/"
		for dir := range c.m {
			if strings.HasPrefix(dir, prefix) {
				delete(c.m, dir)
			}
		}
	}
}

// caseInsensitive is the case_insensitive answer for the object at p (a
// directory when isDir).
//
// With CaseInsensitiveAuto it is the answer for the directory the object
// is (or is in): the first of it and its ancestors that a probe can
// decide, else false. Answers are cached for AttrCacheTimeout, and dropped
// when the server changes or moves the directory.
func (s *AbsfsNFS) caseInsensitive(p string, isDir bool) bool {
	switch s.caseMode {
	case CaseInsensitiveTrue:
		return true
	case CaseInsensitiveFalse:
		return false
	}
	dir := p
	if !isDir {
		dir = path.Dir(p)
	}
	now := time.Now()
	for {
		if folds, ok := s.caseCache.get(dir, now); ok {
			return folds
		}
		if folds, ok := s.probeCase(dir); ok {
			s.caseCache.put(dir, folds, now.Add(s.tuning.Load().AttrCacheTimeout))
			return folds
		}
		if dir == "/" || dir == "." {
			return false
		}
		dir = path.Dir(dir)
	}
}

// probeCase decides, reading only, whether names fold case in dir: it looks
// up a few of its names spelled with each letter's case swapped. A swapped
// name that finds the same file means they fold. One that finds nothing, or
// another file, or is listed as an entry of its own, means they do not.
// Any name that folds decides it, since a filesystem made of layers (a
// union) may fold names in one and not another. ok is false when dir has
// no names to try (it is empty, or none has an ASCII letter) or cannot be
// read.
func (s *AbsfsNFS) probeCase(dir string) (folds, ok bool) {
	f, err := s.fs.Open(dir)
	if err != nil {
		return false, false
	}
	names, _ := f.Readdirnames(caseProbeNames)
	f.Close()
	listed := make(map[string]bool, len(names))
	for _, n := range names {
		listed[n] = true
	}
	lookups := 0
	for _, name := range names {
		swapped := swapASCIICase(name)
		if swapped == name {
			continue
		}
		if listed[swapped] {
			ok = true // two entries: these two names do not fold
			continue
		}
		if lookups == caseProbeLookups {
			break
		}
		lookups++
		orig, err := s.fs.Lstat(path.Join(dir, name))
		if err != nil {
			continue
		}
		other, err := s.fs.Lstat(path.Join(dir, swapped))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			ok = true
		case err != nil:
		default:
			same, known := sameFile(orig, other)
			if same || !known && sameAttrs(orig, other) {
				return true, true
			}
			ok = true
		}
	}
	return false, ok
}

// swapASCIICase returns name with the case of each ASCII letter swapped.
func swapASCIICase(name string) string {
	b := []byte(name)
	for i, c := range b {
		switch {
		case 'a' <= c && c <= 'z':
			b[i] = c - 'a' + 'A'
		case 'A' <= c && c <= 'Z':
			b[i] = c - 'A' + 'a'
		}
	}
	return string(b)
}

// sameFile reports whether a and b describe the same file, and whether it
// could tell. It knows the identity os.SameFile knows, the device and inode
// numbers of a host file (also through a filesystem such as basefs that
// wraps its FileInfo, which os.SameFile cannot see through) and a memfs
// inode. It reads them from the innermost info each wraps (see
// unwrapInfo): a union reports a file a metadata marker changes with a
// fresh merged *inode.Inode on every Lstat, so two Lstats of it compare
// as one file only by the inode the marker wraps (absnfs#88).
func sameFile(a, b os.FileInfo) (same, known bool) {
	a, b = unwrapInfo(a), unwrapInfo(b)
	if os.SameFile(a, b) {
		return true, true
	}
	if same, known := sysSameFile(a.Sys(), b.Sys()); known {
		return same, true
	}
	x, xok := a.Sys().(*inode.Inode)
	y, yok := b.Sys().(*inode.Inode)
	if xok && yok && x != nil && y != nil {
		return x == y, true
	}
	return false, false
}

// unwrapInfo returns the innermost info that info wraps, through any
// FileInfo with an Unwrap() fs.FileInfo method (at most 64 deep), as
// unionfs.SameFile does.
// unionfs.SameFile itself is not used because it does not say whether it
// could tell, which probeCase needs.
func unwrapInfo(info os.FileInfo) os.FileInfo {
	for i := 0; i < 64; i++ {
		u, ok := info.(interface{ Unwrap() fs.FileInfo })
		if !ok {
			break
		}
		info = u.Unwrap()
	}
	return info
}

// sameAttrs stands in for identity where sameFile cannot tell: the same
// type, size and modification time.
func sameAttrs(a, b os.FileInfo) bool {
	return a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}
