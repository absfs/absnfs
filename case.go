// case.go: what PATHCONF reports as case_insensitive.
//
// ExportOptions.CaseInsensitive picks the answer: true or false for every
// handle, or (the default) a read-only probe of the filesystem each handle
// is in, cached per directory.
package absnfs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/absfs/inode"
	"golang.org/x/text/unicode/norm"
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
	// caseProbeLookups is how many of them a probe looks up under another
	// spelling, at most.
	caseProbeLookups = 4
	// caseCacheMax caps the directories whose answer is cached.
	caseCacheMax = 4096
)

// caseCache holds, per directory and until it expires, what a probe found
// (see probeCase) and which fold keys its names share (see sharedKey).
type caseCache struct {
	mu sync.Mutex
	m  map[string]caseAnswer
	// gen counts forget calls, so that an answer worked out while the
	// server changed the directory is not cached (see set).
	gen uint64
}

type caseAnswer struct {
	expires time.Time

	// probed says folds, decided, guess and guessed hold a probe's
	// result: decided when a name looked up under another case decided
	// it (folds), else guessed when one looked up composed or decomposed
	// did (guess, see probeCase).
	probed, decided, folds, guessed, guess bool

	// listed says shared holds the fold keys two or more of the
	// directory's names share (nil when none do).
	listed bool
	shared map[string]bool
}

// get returns the unexpired answer for dir and the generation to pass to
// set when ok is false or the answer lacks what the caller needs.
func (c *caseCache) get(dir string, now time.Time) (a caseAnswer, ok bool, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	a, ok = c.m[dir]
	if !ok || now.After(a.expires) {
		return caseAnswer{}, false, c.gen
	}
	return a, true, c.gen
}

// set applies f to dir's unexpired answer, or to a new one that expires at
// expires, unless the server changed a directory since get returned gen.
func (c *caseCache) set(dir string, gen uint64, now, expires time.Time, f func(*caseAnswer)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.gen {
		return
	}
	a, ok := c.m[dir]
	if !ok || now.After(a.expires) {
		if c.m == nil || len(c.m) >= caseCacheMax {
			c.m = make(map[string]caseAnswer)
		}
		a = caseAnswer{expires: expires}
	}
	f(&a)
	c.m[dir] = a
}

// forget drops the answers for paths, and with tree for everything below
// them too.
func (c *caseCache) forget(tree bool, paths ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
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

// Values of AbsfsNFS.caseVolume.
const (
	caseVolumeUnknown uint32 = iota
	caseVolumeSensitive
	caseVolumeFolds
)

// caseInsensitive is the case_insensitive answer for the object at p (a
// directory when isDir).
//
// With CaseInsensitiveAuto it is the answer for the directory the object
// is (or is in): the first of it and its ancestors that a probe can
// decide. When none can, it is the export's answer (see exportCase).
// Answers are cached for AttrCacheTimeout, and dropped when the server
// changes or moves the directory.
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
	guess, guessed := false, false
	for {
		a := s.probed(dir, now)
		if a.decided {
			return a.folds
		}
		if a.guessed && !guessed {
			guess, guessed = a.guess, true
		}
		if dir == "/" || dir == "." {
			return s.exportCase(guess, guessed)
		}
		dir = path.Dir(dir)
	}
}

// exportCase is the answer for a directory no probe of it or its
// ancestors could decide: the export's, kept for the server's lifetime
// once there is one, so that the fileids it folds (see identityPath) do
// not change when a name is added somewhere. It is the answer of the first
// probe on the export that decided one, else the nearest guess (guessed),
// which then becomes the export's, else false.
func (s *AbsfsNFS) exportCase(guess, guessed bool) bool {
	v := s.caseVolume.Load()
	if v == caseVolumeUnknown && guessed {
		s.caseVolume.CompareAndSwap(caseVolumeUnknown, caseVolumeOf(guess))
		v = s.caseVolume.Load()
	}
	return v == caseVolumeFolds
}

func caseVolumeOf(folds bool) uint32 {
	if folds {
		return caseVolumeFolds
	}
	return caseVolumeSensitive
}

// probed returns dir's probed answer, from the cache or a new probe.
func (s *AbsfsNFS) probed(dir string, now time.Time) caseAnswer {
	a, ok, gen := s.caseCache.get(dir, now)
	if ok && a.probed {
		return a
	}
	r := s.probeCase(dir)
	if r.decided {
		s.caseVolume.CompareAndSwap(caseVolumeUnknown, caseVolumeOf(r.folds))
	}
	s.caseCache.set(dir, gen, now, now.Add(s.tuning.Load().AttrCacheTimeout), func(a *caseAnswer) {
		a.probed, a.decided, a.folds, a.guessed, a.guess = true, r.decided, r.folds, r.guessed, r.guess
		if r.listed && !a.listed {
			a.listed, a.shared = true, r.shared
		}
	})
	return r
}

// probeCase finds out, reading only, whether names fold case in dir: it
// looks up a few of its names spelled with letters of the other case (see
// caseVariant). A spelling that finds the same file means they fold. One
// that finds nothing, or another file, or is listed as an entry of its
// own, means they do not. Any name that folds decides it, since a
// filesystem made of layers (a union) may fold names in one and not
// another. It is not decided when dir has no name with a letter that has
// another case, or cannot be read.
//
// When it is not decided, a name that composed and decomposed (NFC and
// NFD) is spelled differently, looked up the other way, gives a guess:
// a filesystem that finds the same file (APFS, HFS+) is guessed to fold,
// one that does not (ext4, NTFS) not to. That says nothing certain about
// case (case-sensitive APFS matches them too), so it is used only where
// nothing above decides (see exportCase).
//
// When the probe read all of dir's names it also says which fold keys
// they share (listed, shared).
func (s *AbsfsNFS) probeCase(dir string) (r caseAnswer) {
	f, err := s.fs.Open(dir)
	if err != nil {
		return r
	}
	names, err := f.Readdirnames(caseProbeNames)
	f.Close()
	if len(names) < caseProbeNames && (err == nil || errors.Is(err, io.EOF)) {
		r.listed, r.shared = true, sharedKeys(names)
	}
	listed := make(map[string]bool, len(names))
	for _, n := range names {
		listed[n] = true
	}
	// differs reports whether the spelling other of name finds another
	// file, or nothing, rather than the file name does; ok is false when
	// it cannot tell.
	differs := func(name, other string) (differ, ok bool) {
		if listed[other] {
			return true, true // two entries
		}
		orig, err := s.fs.Lstat(path.Join(dir, name))
		if err != nil {
			return false, false
		}
		info, err := s.fs.Lstat(path.Join(dir, other))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return true, true
		case err != nil:
			return false, false
		}
		same, known := sameFile(orig, info)
		return !same && (known || !sameAttrs(orig, info)), true
	}
	lookups := 0
	for _, name := range names {
		other := caseVariant(name)
		if other == name {
			continue
		}
		if !listed[other] {
			if lookups == caseProbeLookups {
				break
			}
			lookups++
		}
		differ, ok := differs(name, other)
		if !ok {
			continue
		}
		if !differ {
			r.decided, r.folds = true, true
			return r
		}
		r.decided = true
	}
	if r.decided {
		return r
	}
	for _, name := range names {
		other := normVariant(name)
		if other == name {
			continue
		}
		if !listed[other] {
			if lookups == caseProbeLookups {
				break
			}
			lookups++
		}
		if differ, ok := differs(name, other); ok {
			r.guessed, r.guess = true, !differ
			if r.guess {
				break
			}
		}
	}
	return r
}

// sharedKeys returns the fold keys (see foldName) two or more of names
// share, nil when none do.
func sharedKeys(names []string) map[string]bool {
	var shared map[string]bool
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		k := foldName(n)
		if seen[k] {
			if shared == nil {
				shared = make(map[string]bool)
			}
			shared[k] = true
		}
		seen[k] = true
	}
	return shared
}

// caseVariant returns name spelled with letters of the other case, for a
// probe to look up, or name when it has none to swap. A name with an
// ASCII letter has its ASCII letters swapped, and nothing else, as every
// folding filesystem folds those alike. Otherwise each letter that has
// exactly one other case, which maps back to it (Ф and ф, Ü and ü), is
// swapped. Letters whose case is not one to one are left alone, since
// filesystems fold them differently (İ, ı, ß and ς; and APFS folds the
// dotless and dotted i apart from I and i), and so is a letter Unicode's
// folding keeps apart from its other case (Cherokee), so that the variant
// has name's fold key.
func caseVariant(name string) string {
	if s := swapASCIICase(name); s != name || isASCII(name) {
		return s
	}
	var b strings.Builder
	changed := false
	for _, r := range name {
		o := r
		switch {
		case unicode.IsLower(r):
			if u := unicode.ToUpper(r); u != r && unicode.ToLower(u) == r {
				o = u
			}
		case unicode.IsUpper(r):
			if l := unicode.ToLower(r); l != r && unicode.ToUpper(l) == r {
				o = l
			}
		}
		changed = changed || o != r
		b.WriteRune(o)
	}
	if !changed {
		return name
	}
	v := b.String()
	if foldName(v) != foldName(name) {
		return name
	}
	return v
}

// normVariant returns name composed (NFC) when it is not, else decomposed
// (NFD), or name when both are spelled the same.
func normVariant(name string) string {
	if isASCII(name) {
		return name
	}
	if c := norm.NFC.String(name); c != name {
		return c
	}
	return norm.NFD.String(name)
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
