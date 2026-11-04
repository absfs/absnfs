// fileid.go: the NFS fileid of an object, one for every spelling of its
// path on an export whose names fold.
package absnfs

import (
	"hash/fnv"
	"path"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// fileID returns the fileid for the path p as spelled: a hash of the
// cleaned path. It is what every object on an export whose names do not
// fold reports, and what READDIR cookies are made from (see dirCookie).
//
// It is never 0, because clients treat fileid 0 as "no entry" (macOS
// silently drops such entries from directory listings).
func fileID(p string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(path.Clean(p)))
	return nonZeroID(h.Sum64())
}

// nonZeroID maps the (astronomically unlikely) zero hash to 1.
func nonZeroID(id uint64) uint64 {
	if id == 0 {
		return 1
	}
	return id
}

// objectID returns the NFS fileid for the object at p.
//
// File handles in absnfs identify paths, so the fileid is derived from the
// path as well: every procedure reports the same id for the same object,
// and it does not change when a union filesystem copies a file up to
// another layer. Where names fold (see caseInsensitive), every spelling of
// a path names one object, so it reports one fileid: that of the path with
// each name that its directory folds replaced by its fold key (see
// foldName). LOOKUP of /Docs and READDIR of / listing docs then agree, and
// a client that compares inode numbers (mv, cp, rsync) sees one file, as
// it does on the host volume itself.
func (s *AbsfsNFS) objectID(p string) uint64 {
	return fileID(s.identityPath(p))
}

// identityPath returns the path whose hash is p's fileid: p (absolute, as
// every node's path is) cleaned, with each name in a directory that folds
// replaced by its fold key. On an export that answers names as
// case-sensitive it is p cleaned.
//
// Only a name its fold key changes needs its directory's answer, so a path
// of lowercase ASCII names costs nothing more than before, and any other
// name one cached lookup of its directory's answer (a probe when that is
// not cached, see caseInsensitive).
func (s *AbsfsNFS) identityPath(p string) string {
	p = path.Clean(p)
	if s.caseMode == CaseInsensitiveFalse || folded(p) {
		return p
	}
	var b strings.Builder
	b.Grow(len(p))
	// p[:start] is the directory holding the name p[start:end], "" for
	// the root.
	for start := 1; start < len(p); {
		end := len(p)
		if i := strings.IndexByte(p[start:], '/'); i >= 0 {
			end = start + i
		}
		name, dir := p[start:end], p[:start-1]
		if dir == "" {
			dir = "/"
		}
		key := foldName(name)
		if key != name && !s.caseInsensitive(dir, true) {
			key = name
		}
		b.WriteByte('/')
		b.WriteString(key)
		start = end + 1
	}
	return b.String()
}

// folded reports whether s is its own fold key, cheaply: it is ASCII with
// no upper-case letters. False says nothing.
func folded(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= utf8.RuneSelf || 'A' <= c && c <= 'Z' {
			return false
		}
	}
	return true
}

// foldName returns the key that every spelling of name that a folding
// volume takes for one name shares: Unicode's canonical caseless match
// (The Unicode Standard, section 3.13, D145), the decomposed (NFD) full
// case folding of the decomposed name. That is what APFS matches names by
// on a case-insensitive volume: README.md and readme.md, é composed and
// decomposed, straße and STRASSE, ﬀ and ff, the Kelvin sign and k, all
// match there and share a key here, and a dotted capital I (İ) matches
// neither i nor ı in either.
//
// Known differences, all rare in file names: Cherokee letters match their
// other case on APFS but not here (x/text folds each case of them to the
// other), so their two spellings keep two fileids, as every spelling did
// before. A volume that folds by other rules can see the opposite: NTFS
// compares upper-cased UTF-16 without normalising, so it keeps straße and
// STRASSE, or é composed and decomposed, apart as two files, which then
// report one fileid.
//
// The key is only hashed into a fileid, never used to find a file.
func foldName(name string) string {
	if folded(name) {
		return name
	}
	if isASCII(name) {
		return strings.ToLower(name)
	}
	// A Caser keeps state, so each call gets its own.
	return norm.NFD.String(cases.Fold().String(norm.NFD.String(name)))
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
