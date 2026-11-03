package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateRoundTrip(t *testing.T) {
	paths, err := scanPaths(strings.NewReader("./a/b.go\n/c/d\n\n.\nsrc/é ü/x\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 || paths[0] != "a/b.go" || paths[1] != "c/d" {
		t.Fatalf("scanPaths = %q", paths)
	}
	lengths := codeLengths(frequencies(paths))
	var kraft float64
	for _, l := range lengths {
		if l == 0 || l > maxBits {
			t.Fatalf("code length %d", l)
		}
		kraft += 1 / float64(uint(1)<<l)
	}
	if kraft != 1 {
		t.Errorf("Kraft sum %v, want 1", kraft)
	}
	src, err := generate(lengths, len(paths))
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(t.TempDir(), "table.go")
	if err := os.WriteFile(name, src, 0o644); err != nil {
		t.Fatal(err)
	}
	back, err := readTable(name)
	if err != nil || back != lengths {
		t.Fatalf("readTable = %v, %v; want %v", back, err, lengths)
	}
	if r := report(lengths, paths); !strings.Contains(r, "3 paths: 100.0% fit inline") {
		t.Errorf("report = %q", r)
	}
}

// A skewed corpus forces codes past 15 bits, which must be limited.
func TestCodeLengthsLimited(t *testing.T) {
	var freq [numSymbols]uint64
	w := uint64(1)
	for i := range freq {
		freq[i] = w
		if w < 1<<40 {
			w *= 2
		}
	}
	for _, l := range codeLengths(freq) {
		if l > maxBits {
			t.Fatalf("code length %d past %d", l, maxBits)
		}
	}
}

// The committed table is the one fhgen reads back.
func TestCommittedTableReads(t *testing.T) {
	if _, err := readTable("../../fh_huffman_v1.go"); err != nil {
		t.Fatal(err)
	}
}
