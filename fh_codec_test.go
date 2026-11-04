package absnfs

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
)

// The Huffman table is part of the version 1 handle format: clients hold
// handles spelled with it. Changing it needs a new handle version.
func TestHuffmanTableGolden(t *testing.T) {
	sum := sha256.Sum256(fhCodeLengths[:])
	const want = "95d8e9ddfd8f1fb8bf2fe1804b913ba732174eb46a67dc7dc4f467baa3a17a2a"
	if got := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("fh_huffman_v1.go changed (sha256 %s, want %s): version 1 handles would no longer decode", got, want)
	}
	// The lengths form a complete prefix code no longer than 15 bits.
	var kraft float64
	for _, l := range fhCodeLengths {
		if l == 0 || l > fhMaxBits {
			t.Fatalf("code length %d out of range", l)
		}
		kraft += 1 / float64(uint(1)<<l)
	}
	if kraft != 1 {
		t.Errorf("Kraft sum %v, want 1 (a complete code)", kraft)
	}
}

// The empty handle generation derives K_tag and K_tail as servers did
// before generations existed, so handles they persisted with
// HandleStateDir stay valid. The vectors were computed by that code.
func TestHandleCodecEmptyGenerationGolden(t *testing.T) {
	c := testCodec()
	long := "repo/" + strings.Repeat("long_name_", 8) + "/x"
	for _, v := range []struct{ what, got, want string }{
		{"K_tag", hex.EncodeToString(c.tagKey), "9993d082b31b755b5bb7c0a4c567a00215560696dac9546a46ba70e19017f6ad"},
		{"K_tail", hex.EncodeToString(c.tailKey[:]), "b55eabbb9988e3f7"},
		{"inline handle", hex.EncodeToString(c.encode(fhTypeRegular, 0, "repo/.git/index")), "4414c962a4df5989f252b208686e2759176e00"},
		{"inline handle with epoch", hex.EncodeToString(c.encode(fhTypeDir, 5, "a/b")), "42cd8e5a5180449e760580e7c0"},
		{"tail handle", hex.EncodeToString(c.encode(fhTypeRegular, 3, long)), "5648c242bbbb417be2030ac5e8282d52b270"},
		{"path hash", fmt.Sprintf("%x", c.pathHash("repo/x")), "e7815052a91dc05c"},
	} {
		if v.got != v.want {
			t.Errorf("%s under the empty generation: %s, want %s", v.what, v.got, v.want)
		}
	}
}

// A handle issued under one generation fails its tag under every other,
// with the same key: inline and tail alike, and the empty generation is
// one of them. The length prefix keeps generations that share a prefix
// apart.
func TestHandleGenerationSeparatesHandles(t *testing.T) {
	key := bytes.Repeat([]byte{7}, handleKeySize)
	gens := []string{"", "a", "b", "ab", "a\x00", "\x01a", "run 2026-11-03T10:00:00Z"}
	codecs := make([]*handleCodec, len(gens))
	tails := make(map[[8]byte]string)
	for i, g := range gens {
		codecs[i] = newHandleCodec(key, g)
		if other, ok := tails[codecs[i].tailKey]; ok {
			t.Errorf("generations %q and %q share K_tail", other, g)
		}
		tails[codecs[i].tailKey] = g
	}
	paths := []string{"", "w1/x", "w1/" + strings.Repeat("long_name_", 8) + "/x"}
	for i, ci := range codecs {
		for _, p := range paths {
			fh := ci.encode(fhTypeRegular, 0, p)
			ph, ok := parseHandle(fh)
			if !ok {
				t.Fatalf("generation %q: handle of %q does not parse", gens[i], p)
			}
			for j, cj := range codecs {
				if got := cj.verify(fh, ph, p); got != (i == j) {
					t.Errorf("handle of %q from generation %q verifies under %q: %v", p, gens[i], gens[j], got)
				}
			}
		}
	}
}

func testCodec() *handleCodec {
	return newHandleCodec(bytes.Repeat([]byte{7}, handleKeySize), "")
}

// randomPath makes a path of up to depth components, mostly printable
// ASCII, sometimes UTF-8 and other bytes, never NUL or '/'.
func randomPath(r *rand.Rand, depth, width int) string {
	n := r.Intn(depth + 1)
	comps := make([]string, n)
	for i := range comps {
		l := 1 + r.Intn(width)
		var b strings.Builder
		for b.Len() < l {
			switch r.Intn(20) {
			case 0:
				b.WriteString("é")
			case 1:
				b.WriteByte(byte(1 + r.Intn(31))) // control bytes
			case 2:
				b.WriteByte(byte(0x80 + r.Intn(0x80)))
			default:
				c := byte(0x20 + r.Intn(95))
				if c == '/' {
					c = '_'
				}
				b.WriteByte(c)
			}
		}
		c := b.String()
		if c == "." || c == ".." {
			c += "x"
		}
		comps[i] = c
	}
	return strings.Join(comps, "/")
}

// tailPath rebuilds the path a tail handle names from the true path, as a
// directory walk would: it checks that the hashes match its components.
func tailPath(t *testing.T, c *handleCodec, ph parsedHandle, p string) {
	t.Helper()
	comps := strings.Split(p, "/")
	m := len(ph.hashes)
	if m == 0 || m > len(comps) {
		t.Fatalf("%q: %d hashes for %d components", p, m, len(comps))
	}
	if want := strings.Join(comps[:len(comps)-m], "/"); ph.path != want {
		t.Fatalf("%q: prefix %q, want %q", p, ph.path, want)
	}
	for i, h := range ph.hashes {
		if want := c.tailHash(comps[len(comps)-m+i], ph.wide); h != want {
			t.Fatalf("%q: hash %d is %x, want %x", p, i, h, want)
		}
	}
}

func TestHandleCodecRoundTrip(t *testing.T) {
	c := testCodec()
	r := rand.New(rand.NewSource(1))
	kinds := map[uint8]int{}
	for i := 0; i < 20000; i++ {
		p := randomPath(r, 1+r.Intn(30), 1+r.Intn(30))
		typ := uint8(r.Intn(4))
		g := []uint64{0, 1, uint64(r.Int63()), 59_000_000_000_000}[r.Intn(4)]
		fh := c.encode(typ, g, p)
		if len(fh) < fhMinSize || len(fh) > fhMaxSize {
			t.Fatalf("%q: handle of %d bytes", p, len(fh))
		}
		ph, ok := parseHandle(fh)
		if !ok {
			t.Fatalf("%q: does not parse: %x", p, fh)
		}
		if ph.typ != typ || ph.g != g {
			t.Fatalf("%q: type %d g %d, want %d %d", p, ph.typ, ph.g, typ, g)
		}
		kinds[ph.kind]++
		switch ph.kind {
		case fhKindInline:
			if ph.path != p {
				t.Fatalf("decoded %q, want %q", ph.path, p)
			}
		case fhKindTail:
			tailPath(t, c, ph, p)
		}
		if !c.verify(fh, ph, p) {
			t.Fatalf("%q: does not verify", p)
		}
		if ph.kind != fhKindOpaque && !bytes.Equal(c.encode(typ, g, p), fh) {
			t.Fatalf("%q: encoding is not deterministic", p)
		}
		if c.verify(fh, ph, p+"x") {
			t.Fatalf("%q: verifies for another path", p)
		}
	}
	for _, k := range []uint8{fhKindInline, fhKindTail} { // opaque: TestHandleCodecRealPaths
		if kinds[k] == 0 {
			t.Errorf("no handle of kind %d in the sample: %v", k, kinds)
		}
	}
}

// Real trees fit: the measurement in the docs is for such paths.
func TestHandleCodecRealPaths(t *testing.T) {
	c := testCodec()
	paths := []string{
		"",
		"staging/src/k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1/fake/fake_customresourcedefinition.go",
		"vendor/github.com/google/gnostic-models/openapiv3/annotations.pb.go",
		".git/objects/pack/pack-0123456789abcdef0123456789abcdef01234567.idx",
		"drivers/gpu/drm/amd/display/dc/dml2/dml21/src/dml2_core/dml2_core_dcn4_calcs.c",
		"node_modules/@typescript-eslint/eslint-plugin/dist/rules/naming-convention-utils/validator.js",
		"Photos/Summer 2024 (Copy)/IMG_0001 – edited.jpg",
		strings.Repeat("deep/", 60) + "leaf",
	}
	for _, p := range paths {
		for _, g := range []uint64{0, 59_000_000_000_000} {
			fh := c.encode(fhTypeRegular, g, p)
			ph, ok := parseHandle(fh)
			if !ok || !c.verify(fh, ph, p) {
				t.Fatalf("%q: does not round-trip", p)
			}
			if ph.kind == fhKindInline && ph.path != p {
				t.Fatalf("%q decoded as %q", p, ph.path)
			}
			if ph.kind == fhKindTail {
				tailPath(t, c, ph, p)
			}
		}
	}
	if fh := c.encode(fhTypeDir, 0, ""); len(fh) != 10 {
		t.Errorf("root handle is %d bytes, want 10", len(fh))
	}
	if ph, _ := parseHandle(c.encode(fhTypeRegular, 0, paths[1])); ph.kind != fhKindTail {
		t.Errorf("a 130-byte k8s path is kind %d, want a tail handle", ph.kind)
	}
	if ph, _ := parseHandle(c.encode(fhTypeRegular, 0, strings.Repeat("d/", 70)+"f")); ph.kind != fhKindOpaque {
		t.Errorf("a 71-component path is kind %d, want opaque", ph.kind)
	}
}

// Only one byte string is valid for an object: anything else, however
// close, is refused before any filesystem I/O.
func TestHandleCodecRejects(t *testing.T) {
	c := testCodec()
	p := "dir/file.txt"
	fh := c.encode(fhTypeRegular, 0, p)
	gfh := c.encode(fhTypeRegular, 1234567, p)

	mutate := func(src []byte, f func([]byte) []byte) []byte {
		return f(append([]byte(nil), src...))
	}
	cases := map[string][]byte{
		"empty":           {},
		"legacy 8 bytes":  legacyHandle(0x1800000000000001),
		"too short":       fh[:9],
		"too long":        make([]byte, 65),
		"version 00":      mutate(fh, func(b []byte) []byte { b[0] &^= 0xC0; return b }),
		"version 10":      mutate(fh, func(b []byte) []byte { b[0] = b[0]&^0xC0 | 0x80; return b }),
		"version 11":      mutate(fh, func(b []byte) []byte { b[0] |= 0xC0; return b }),
		"reserved bit":    mutate(fh, func(b []byte) []byte { b[0] |= fhReserved; return b }),
		"kind 11":         mutate(fh, func(b []byte) []byte { b[0] |= 3 << 4; return b }),
		"G without g":     mutate(fh, func(b []byte) []byte { b[0] |= fhFlagG; return b }),
		"g of zero":       mutate(fh, func(b []byte) []byte { return append(append(b[:9:9], 0), b[9:]...) }),
		"overlong g":      mutate(gfh, overlongG),
		"tail bit 0":      tailMutation(c, func(b []byte, at int) { b[at] |= 1 }),
		"tail with m = 0": tailMutation(c, func(b []byte, at int) { b[at] &= 3 }),
	}
	for name, b := range cases {
		if ph, ok := parseHandle(b); ok && c.verify(b, ph, ph.path) {
			t.Errorf("%s: accepted", name)
		}
	}

	// Each wrong bit, in the tag, the path or the padding, is refused.
	for i := range fh {
		for bit := 0; bit < 8; bit++ {
			b := append([]byte(nil), fh...)
			b[i] ^= 1 << bit
			if ph, ok := parseHandle(b); ok && ph.kind == fhKindInline && c.verify(b, ph, ph.path) {
				t.Errorf("flipping bit %d of byte %d is accepted", bit, i)
			}
		}
	}
	// A trailing byte, or a printable byte spelled with ESC, is not the
	// canonical spelling.
	if b := append(append([]byte(nil), fh...), 0); verifies(c, b) {
		t.Error("a trailing zero byte is accepted")
	}
	esc := append([]byte(nil), fh[:9]...)
	esc = appendEscaped(esc, p)
	if verifies(c, esc) {
		t.Error("a path spelled with ESC is accepted")
	}
	// Another key's handle is refused.
	other := newHandleCodec(bytes.Repeat([]byte{8}, handleKeySize), "")
	if verifies(other, fh) {
		t.Error("a handle verifies under another key")
	}
	if !verifies(c, fh) || !verifies(c, gfh) {
		t.Fatal("the genuine handles do not verify")
	}
}

func verifies(c *handleCodec, fh []byte) bool {
	ph, ok := parseHandle(fh)
	return ok && ph.kind == fhKindInline && c.verify(fh, ph, ph.path)
}

func overlongG(b []byte) []byte {
	// b's g starts at byte 9; add a redundant 0x80 0x00 continuation.
	g, n := binary.Uvarint(b[9:])
	enc := binary.AppendUvarint(nil, g)
	enc[len(enc)-1] |= 0x80
	enc = append(enc, 0)
	return append(append(b[:9:9], enc...), b[9+n:]...)
}

func tailMutation(c *handleCodec, f func(b []byte, at int)) []byte {
	b := c.encode(fhTypeRegular, 0, strings.Repeat("x", 80))
	f(b, 9)
	return b
}

// appendEscaped spells p with ESC for every byte, then EOS.
func appendEscaped(dst []byte, p string) []byte {
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
		put(uint64(fhCodes[fhSymESC]), uint(fhCodeLengths[fhSymESC]))
		put(uint64(p[i]), 8)
	}
	put(uint64(fhCodes[fhSymEOS]), uint(fhCodeLengths[fhSymEOS]))
	if n > 0 {
		dst = append(dst, byte(acc<<(8-n)))
	}
	return dst
}

func TestCanonicalPath(t *testing.T) {
	good := map[string]string{"/": "", "/a": "a", "/a/b c/é": "a/b c/é"}
	for in, want := range good {
		if got, ok := canonicalPath(in); !ok || got != want {
			t.Errorf("canonicalPath(%q) = %q, %v; want %q", in, got, ok, want)
		}
		if nodePath(want) != in {
			t.Errorf("nodePath(%q) = %q, want %q", want, nodePath(want), in)
		}
	}
	for _, bad := range []string{"//", "/a//b", "/a/", "/./a", "/a/..", "/a\x00b"} {
		if _, ok := canonicalPath(bad); ok {
			t.Errorf("canonicalPath(%q) accepted", bad)
		}
	}
	for m, want := range map[uint32]uint8{0: fhTypeRegular, 1 << 31: fhTypeDir, 1 << 27: fhTypeSymlink, 1 << 25: fhTypeOther} {
		if got := fhType(os.FileMode(m)); got != want {
			t.Errorf("fhType(%x) = %d, want %d", m, got, want)
		}
	}
}

func BenchmarkHandleEncode(b *testing.B) {
	c := testCodec()
	p := "staging/src/k8s.io/client-go/tools/cache/shared_informer.go"
	for i := 0; i < b.N; i++ {
		c.encode(fhTypeRegular, 0, p)
	}
}

func BenchmarkHandleDecodeVerify(b *testing.B) {
	c := testCodec()
	fh := c.encode(fhTypeRegular, 0, "staging/src/k8s.io/client-go/tools/cache/shared_informer.go")
	for i := 0; i < b.N; i++ {
		ph, _ := parseHandle(fh)
		if !c.verify(fh, ph, ph.path) {
			b.Fatal("does not verify")
		}
	}
}

func ExampleFileHandleMap() {
	fm := newFileHandleMap(10)
	root := &NFSNode{path: "/", attrs: &NFSAttrs{Mode: 0o755 | 1<<31}}
	fh := fm.issue(root)
	fmt.Println(len(fh), "bytes, version", fh[0]>>6)
	fm.ReleaseAll()
	fmt.Println("same bytes after the cache is emptied:", bytes.Equal(fm.issue(root), fh))
	// Output:
	// 10 bytes, version 1
	// same bytes after the cache is emptied: true
}
