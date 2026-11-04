// fh_codec.go: the version 1 file handle format.
//
// A handle locates its object by path, so the server can resolve it without
// any table, and carries a keyed tag that only this server's key can
// produce. Layout (RFC 1813 fhandle3 is opaque<64>):
//
//	byte 0     header: bits 7-6 version (01), 5-4 kind, 3-2 type, 1 G, 0 zero
//	bytes 1-8  tag = HMAC-SHA256(K_tag, byte(0x40|type<<2) || uvarint(g) || path)[:8]
//	[uvarint g, when G is set]
//	body:
//	  kind 00 inline: the path, Huffman coded, EOS, zero bits to a byte
//	  kind 01 tail:   T = m<<2 | w<<1, m hashes of the last m components
//	                  (16 bits when w is set, else 8), then the other
//	                  components coded as for inline
//	  kind 10 opaque: 8 random bytes; resolves only from the handle cache
//
// path is the object's path below the export's root without the leading
// "/" (the root is ""), g its change-log epoch (see fh_changelog.go), and
// K_tag and K_tail are derived from the server's key and handle generation
// (newHandleCodec). The encoding of a (type, g, path) is unique: the
// decoder re-encodes what it read and refuses anything else, so no two
// byte strings name one object and no padding bit can be flipped into an
// alias.
package absnfs

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"os"
	"strings"
	"sync"
)

const (
	fhVersionMask = 0xC0
	fhVersion1    = 0x40
	fhFlagG       = 0x02
	fhReserved    = 0x01

	fhKindInline = 0
	fhKindTail   = 1
	fhKindOpaque = 2

	fhTypeDir     = 0
	fhTypeRegular = 1
	fhTypeSymlink = 2
	fhTypeOther   = 3

	fhTagSize = 8
	fhMaxSize = 64 // FHSIZE3
	fhMinSize = 1 + fhTagSize + 1
	fhMaxTail = 63 // hashed components a tail handle can carry
)

// fhType is the handle type of an object with mode m.
func fhType(m os.FileMode) uint8 {
	switch {
	case m.IsDir():
		return fhTypeDir
	case m.IsRegular():
		return fhTypeRegular
	case m&os.ModeSymlink != 0:
		return fhTypeSymlink
	}
	return fhTypeOther
}

// canonicalPath returns the path handles spell for an NFSNode path ("/" is
// the root, "/a/b" below it), and whether the path can be spelled: every
// component non-empty, not "." or "..", and free of NUL.
func canonicalPath(nodePath string) (string, bool) {
	p := strings.TrimPrefix(nodePath, "/")
	return p, validCanonical(p)
}

// nodePath is the inverse of canonicalPath.
func nodePath(canonical string) string {
	return "/" + canonical
}

func validCanonical(p string) bool {
	if p == "" {
		return true
	}
	for {
		c, rest, more := strings.Cut(p, "/")
		if c == "" || c == "." || c == ".." || strings.IndexByte(c, 0) >= 0 {
			return false
		}
		if !more {
			return true
		}
		p = rest
	}
}

// handleCodec encodes and verifies handles with one server's key and
// handle generation.
type handleCodec struct {
	tagKey  []byte
	tailKey [8]byte
	macs    sync.Pool // of hash.Hash keyed with tagKey
}

// newHandleCodec returns the codec for key and generation (see
// ExportOptions.HandleGeneration). The generation is folded into both
// derived keys, HMAC-SHA256(key, label || uvarint(len(generation)) ||
// generation), so a handle issued under one generation fails its tag under
// any other. The empty generation derives them as before generations
// existed, HMAC-SHA256(key, label), so its handles are unchanged.
func newHandleCodec(key []byte, generation string) *handleCodec {
	derive := func(label string) []byte {
		m := hmac.New(sha256.New, key)
		m.Write([]byte(label))
		if generation != "" {
			m.Write(binary.AppendUvarint(nil, uint64(len(generation))))
			m.Write([]byte(generation))
		}
		return m.Sum(nil)
	}
	c := &handleCodec{tagKey: derive("absnfs fh v1 tag")}
	copy(c.tailKey[:], derive("absnfs fh v1 tail"))
	c.macs.New = func() any { return hmac.New(sha256.New, c.tagKey) }
	return c
}

// tag computes the tag of an object of type typ at path p with epoch g.
func (c *handleCodec) tag(typ uint8, g uint64, p string) [fhTagSize]byte {
	m := c.macs.Get().(hash.Hash)
	m.Reset()
	var pre [1 + binary.MaxVarintLen64]byte
	pre[0] = fhVersion1 | typ<<2
	n := binary.PutUvarint(pre[1:], g)
	m.Write(pre[:1+n])
	m.Write([]byte(p))
	var sum [sha256.Size]byte
	var t [fhTagSize]byte
	copy(t[:], m.Sum(sum[:0]))
	c.macs.Put(m)
	return t
}

// pathHash hashes a path component (or, for the change log, a whole path)
// with the tail key: FNV-1a 64 over K_tail || s.
func (c *handleCodec) pathHash(s string) uint64 {
	const offset, prime = 14695981039346656037, 1099511628211
	h := uint64(offset)
	for _, b := range c.tailKey {
		h = (h ^ uint64(b)) * prime
	}
	for i := 0; i < len(s); i++ {
		h = (h ^ uint64(s[i])) * prime
	}
	return h
}

func fold16(h uint64) uint16 {
	h ^= h >> 32
	h ^= h >> 16
	return uint16(h)
}

func fold8(h uint64) uint8 {
	f := fold16(h)
	return uint8(f ^ f>>8)
}

// tailHash is the hash of component name a tail handle carries, 16 bits
// wide or 8.
func (c *handleCodec) tailHash(name string, wide bool) uint16 {
	h := c.pathHash(name)
	if wide {
		return fold16(h)
	}
	return uint16(fold8(h))
}

// encode returns the handle of an object of type typ at canonical path p
// with epoch g. The result is a function of its arguments and the key,
// except for the opaque kind, used only when neither the inline nor the
// tail kind fits.
func (c *handleCodec) encode(typ uint8, g uint64, p string) []byte {
	return c.encodeTagged(typ, g, p, c.tag(typ, g, p))
}

// encodeTagged is encode with the tag already computed.
func (c *handleCodec) encodeTagged(typ uint8, g uint64, p string, t [fhTagSize]byte) []byte {
	head := make([]byte, 1, fhMaxSize)
	head[0] = fhVersion1 | typ<<2
	head = append(head, t[:]...)
	if g > 0 {
		head[0] |= fhFlagG
		head = binary.AppendUvarint(head, g)
	}
	room := fhMaxSize - len(head)

	if (huffBits(p)+7)/8 <= room {
		return appendHuff(head, p)
	}
	head[0] |= fhKindTail << 4
	if comps := strings.Split(p, "/"); len(comps) > 0 {
		for _, wide := range []bool{true, false} {
			if fh, ok := c.encodeTail(head, comps, wide, room); ok {
				return fh
			}
		}
	}
	return c.encodeOpaque(typ, g, p)
}

// encodeOpaque returns a handle that carries no path, only the tag and 8
// random bytes. Only the cache can resolve it. issue uses it for a path no
// other kind can spell.
func (c *handleCodec) encodeOpaque(typ uint8, g uint64, p string) []byte {
	fh := make([]byte, 1, fhMaxSize)
	fh[0] = fhVersion1 | fhKindOpaque<<4 | typ<<2
	t := c.tag(typ, g, p)
	fh = append(fh, t[:]...)
	if g > 0 {
		fh[0] |= fhFlagG
		fh = binary.AppendUvarint(fh, g)
	}
	var r [8]byte
	rand.Read(r[:])
	return append(fh, r[:]...)
}

// encodeTail spells the path whose components are comps by hashing the
// fewest trailing components that make it fit in room bytes after head.
func (c *handleCodec) encodeTail(head []byte, comps []string, wide bool, room int) ([]byte, bool) {
	width := 1
	if wide {
		width = 2
	}
	// prefixBits[k] is the cost of the first k components plus EOS.
	prefixBits := make([]int, len(comps)+1)
	prefixBits[0] = huffBits("")
	for i, comp := range comps {
		sep := 0
		if i > 0 {
			sep = huffBits("/") - huffBits("")
		}
		prefixBits[i+1] = prefixBits[i] + sep + huffBits(comp) - huffBits("")
	}
	for m := 1; m <= min(len(comps), fhMaxTail); m++ {
		k := len(comps) - m
		if 1+m*width+(prefixBits[k]+7)/8 > room {
			continue
		}
		var w byte
		if wide {
			w = 2
		}
		fh := append(head, byte(m)<<2|w)
		for _, comp := range comps[k:] {
			h := c.tailHash(comp, wide)
			if wide {
				fh = binary.BigEndian.AppendUint16(fh, h)
			} else {
				fh = append(fh, byte(h))
			}
		}
		return appendHuff(fh, strings.Join(comps[:k], "/")), true
	}
	return nil, false
}

// parsedHandle is a handle taken apart, before its tag is checked.
type parsedHandle struct {
	kind   uint8
	typ    uint8
	g      uint64
	tag    [fhTagSize]byte
	path   string   // kind inline: the path; kind tail: the unhashed prefix
	hashes []uint16 // kind tail: hashes of the trailing components, top-down
	wide   bool     // kind tail: 16-bit hashes
}

// parse takes a handle apart. It checks the structure only; verify checks
// that the handle is the one this server would issue.
func parseHandle(fh []byte) (parsedHandle, bool) {
	var ph parsedHandle
	if len(fh) < fhMinSize || len(fh) > fhMaxSize {
		return ph, false
	}
	h := fh[0]
	if h&fhVersionMask != fhVersion1 || h&fhReserved != 0 {
		return ph, false
	}
	ph.kind = h >> 4 & 3
	ph.typ = h >> 2 & 3
	copy(ph.tag[:], fh[1:1+fhTagSize])
	rest := fh[1+fhTagSize:]
	if h&fhFlagG != 0 {
		g, n := binary.Uvarint(rest)
		if n <= 0 || g == 0 || n != uvarintLen(g) {
			return ph, false
		}
		ph.g, rest = g, rest[n:]
	}
	var ok bool
	switch ph.kind {
	case fhKindInline:
		ph.path, ok = huffDecode(rest)
	case fhKindTail:
		ok = ph.parseTail(rest)
	case fhKindOpaque:
		ok = len(rest) == 8
	}
	return ph, ok && validCanonical(ph.path)
}

func (ph *parsedHandle) parseTail(rest []byte) bool {
	if len(rest) < 1 || rest[0]&1 != 0 {
		return false
	}
	m := int(rest[0] >> 2)
	ph.wide = rest[0]&2 != 0
	width := 1
	if ph.wide {
		width = 2
	}
	rest = rest[1:]
	if m == 0 || len(rest) < m*width {
		return false
	}
	ph.hashes = make([]uint16, m)
	for i := range ph.hashes {
		if ph.wide {
			ph.hashes[i] = binary.BigEndian.Uint16(rest[2*i:])
		} else {
			ph.hashes[i] = uint16(rest[i])
		}
	}
	var ok bool
	ph.path, ok = huffDecode(rest[m*width:])
	return ok
}

// verify reports whether fh, parsed as ph, is the handle this server issues
// for the object of type ph.typ at canonical path p with epoch ph.g: the tag
// matches (in constant time) and re-encoding gives the same bytes. An
// opaque handle cannot be re-encoded; only its tag is checked.
func (c *handleCodec) verify(fh []byte, ph parsedHandle, p string) bool {
	t := c.tag(ph.typ, ph.g, p)
	if !hmac.Equal(t[:], ph.tag[:]) {
		return false
	}
	if ph.kind == fhKindOpaque {
		return true
	}
	return bytes.Equal(c.encodeTagged(ph.typ, ph.g, p, t), fh)
}

func uvarintLen(v uint64) int {
	var b [binary.MaxVarintLen64]byte
	return binary.PutUvarint(b[:], v)
}

// The Huffman code (fh_huffman_v1.go has the lengths): symbols 0..94 are
// the bytes 0x20..0x7E, then EOS and ESC.
const (
	fhSymbols = 97
	fhSymEOS  = 95
	fhSymESC  = 96
	fhMaxBits = 15
)

var (
	fhCodes     [fhSymbols]uint16
	fhFirstCode [fhMaxBits + 2]int // canonical code of the first symbol of each length
	fhFirstIdx  [fhMaxBits + 2]int // its index in fhSorted
	fhCount     [fhMaxBits + 2]int // symbols of each length
	fhSorted    [fhSymbols]uint8   // symbols in canonical order
)

func init() { buildHuffman(fhCodeLengths) }

// buildHuffman derives the canonical code from the code lengths: symbols
// are ordered by length, then value, and numbered consecutively.
func buildHuffman(lengths [fhSymbols]uint8) {
	n := 0
	for l := 1; l <= fhMaxBits; l++ {
		for s := 0; s < fhSymbols; s++ {
			if int(lengths[s]) == l {
				fhSorted[n] = uint8(s)
				n++
			}
		}
	}
	code, prev := 0, 0
	for i, s := range fhSorted {
		l := int(lengths[s])
		code <<= l - prev
		if prev != l {
			fhFirstCode[l], fhFirstIdx[l] = code, i
		}
		fhCodes[s] = uint16(code)
		fhCount[l]++
		code++
		prev = l
	}
}

func fhSymbol(b byte) (int, bool) {
	if b >= 0x20 && b <= 0x7E {
		return int(b - 0x20), false
	}
	return fhSymESC, true
}

// huffBits is the number of bits p takes, EOS included.
func huffBits(p string) int {
	n := int(fhCodeLengths[fhSymEOS])
	for i := 0; i < len(p); i++ {
		s, esc := fhSymbol(p[i])
		n += int(fhCodeLengths[s])
		if esc {
			n += 8
		}
	}
	return n
}

// appendHuff appends p, EOS, and zero bits to the next byte.
func appendHuff(dst []byte, p string) []byte {
	var acc uint64
	var n uint
	put := func(v uint64, bits uint) {
		acc = acc<<bits | v
		n += bits
		for n >= 8 {
			n -= 8
			dst = append(dst, byte(acc>>n))
		}
	}
	for i := 0; i < len(p); i++ {
		s, esc := fhSymbol(p[i])
		put(uint64(fhCodes[s]), uint(fhCodeLengths[s]))
		if esc {
			put(uint64(p[i]), 8)
		}
	}
	put(uint64(fhCodes[fhSymEOS]), uint(fhCodeLengths[fhSymEOS]))
	if n > 0 {
		dst = append(dst, byte(acc<<(8-n)))
	}
	return dst
}

// huffDecode reads a path up to EOS. What follows EOS is not checked here:
// verify re-encodes the handle and compares.
func huffDecode(b []byte) (string, bool) {
	var out []byte
	pos, total := 0, len(b)*8
	bit := func() (int, bool) {
		if pos >= total {
			return 0, false
		}
		v := int(b[pos>>3]>>(7-pos&7)) & 1
		pos++
		return v, true
	}
	for {
		code := 0
		sym := -1
		for l := 1; l <= fhMaxBits; l++ {
			v, ok := bit()
			if !ok {
				return "", false
			}
			code = code<<1 | v
			if d := code - fhFirstCode[l]; fhCount[l] > 0 && d >= 0 && d < fhCount[l] {
				sym = int(fhSorted[fhFirstIdx[l]+d])
				break
			}
		}
		switch {
		case sym < 0:
			return "", false
		case sym == fhSymEOS:
			return string(out), true
		case sym == fhSymESC:
			raw := 0
			for i := 0; i < 8; i++ {
				v, ok := bit()
				if !ok {
					return "", false
				}
				raw = raw<<1 | v
			}
			out = append(out, byte(raw))
		default:
			out = append(out, byte(sym+0x20))
		}
	}
}
