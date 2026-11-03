// Command fhgen generates fh_huffman_v1.go, the frozen Huffman code that
// absnfs uses to spell paths inside version 1 file handles, and measures how
// many paths of a tree fit in a handle.
//
// The code is order-0 over 97 symbols: the 95 printable ASCII bytes (0x20
// to 0x7E, '/' included), EOS, which ends the path, and ESC, which is
// followed by 8 raw bits and spells any other byte, so UTF-8 names survive.
// No code is longer than 15 bits.
//
// The table is part of the handle format. Once released it must never
// change: a client may hold handles spelled with it. A different table needs
// a new handle version. absnfs has a golden test that fails if it changes.
//
// Generating (each file lists one path per line, relative to a tree's root;
// the committed table was built from the corpus described in
// docs/internals/file-handles.md):
//
//	go run ./cmd/fhgen -o fh_huffman_v1.go linux.txt gomod.txt js.txt
//
// Measuring with the committed table (or with a table generated from the
// given corpus files, with -gen):
//
//	go run ./cmd/fhgen -measure k8s.txt
package main

import (
	"bufio"
	"bytes"
	"container/heap"
	"flag"
	"fmt"
	"go/format"
	"io"
	"os"
	"sort"
	"strings"
)

const (
	numSymbols = 97 // 95 printable bytes, EOS, ESC
	symEOS     = 95
	symESC     = 96
	maxBits    = 15

	handleSize = 64 // FHSIZE3
	tagSize    = 8
)

func main() {
	out := flag.String("o", "", "write the generated Go file here (default stdout)")
	measure := flag.Bool("measure", false, "report how many of the listed paths fit inline instead of generating")
	table := flag.String("table", "fh_huffman_v1.go", "with -measure, the generated table to measure")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: fhgen [-o file | -measure] pathlist...")
		os.Exit(2)
	}
	var paths []string
	for _, name := range flag.Args() {
		ps, err := readPaths(name)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		paths = append(paths, ps...)
	}
	if *measure {
		lengths, err := readTable(*table)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Print(report(lengths, paths))
		return
	}
	lengths := codeLengths(frequencies(paths))
	src, err := generate(lengths, len(paths))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *out == "" {
		os.Stdout.Write(src)
		return
	}
	if err := os.WriteFile(*out, src, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, report(lengths, paths))
}

// readPaths reads one path per line, dropping a leading "./" or "/" and
// empty lines.
func readPaths(name string) ([]string, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return scanPaths(f)
}

func scanPaths(r io.Reader) ([]string, error) {
	var paths []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		p := strings.TrimPrefix(strings.TrimPrefix(sc.Text(), "./"), "/")
		if p != "" && p != "." {
			paths = append(paths, p)
		}
	}
	return paths, sc.Err()
}

// symbol returns the symbol that spells b, and whether b needs ESC.
func symbol(b byte) (int, bool) {
	if b >= 0x20 && b <= 0x7E {
		return int(b - 0x20), false
	}
	return symESC, true
}

// frequencies counts the symbols that spell the paths. Every symbol counts
// at least once, so every symbol gets a code.
func frequencies(paths []string) [numSymbols]uint64 {
	var freq [numSymbols]uint64
	for i := range freq {
		freq[i] = 1
	}
	for _, p := range paths {
		for i := 0; i < len(p); i++ {
			s, _ := symbol(p[i])
			freq[s]++
		}
		freq[symEOS]++
	}
	// A floor of 1/floorShare of all symbols keeps bytes the corpus rarely
	// uses (spaces, parentheses, most capitals) from costing 15 bits each
	// in trees that use them more.
	var total uint64
	for _, f := range freq {
		total += f
	}
	for i := range freq {
		freq[i] = max(freq[i], total/floorShare)
	}
	return freq
}

// floorShare sets the least weight a symbol gets; see frequencies.
const floorShare = 2048

type node struct {
	weight uint64
	order  int // tie-break, so the result does not depend on heap internals
	sym    int // leaf symbol, or -1
	kids   [2]*node
}

type nodeHeap []*node

func (h nodeHeap) Len() int { return len(h) }
func (h nodeHeap) Less(i, j int) bool {
	if h[i].weight != h[j].weight {
		return h[i].weight < h[j].weight
	}
	return h[i].order < h[j].order
}
func (h nodeHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *nodeHeap) Push(x any)   { *h = append(*h, x.(*node)) }
func (h *nodeHeap) Pop() any {
	old := *h
	n := old[len(old)-1]
	*h = old[:len(old)-1]
	return n
}

// codeLengths builds a Huffman code for freq and returns its code lengths,
// none above maxBits: while the longest is too long, the weights are halved
// (keeping at least 1) and the code is built again. The result depends only
// on freq.
func codeLengths(freq [numSymbols]uint64) [numSymbols]uint8 {
	w := freq
	for {
		lengths := huffman(w)
		longest := uint8(0)
		for _, l := range lengths {
			longest = max(longest, l)
		}
		if longest <= maxBits {
			return lengths
		}
		for i := range w {
			w[i] = max(w[i]/2, 1)
		}
	}
}

func huffman(freq [numSymbols]uint64) [numSymbols]uint8 {
	h := make(nodeHeap, 0, numSymbols)
	for s, f := range freq {
		h = append(h, &node{weight: f, order: s, sym: s})
	}
	heap.Init(&h)
	order := numSymbols
	for h.Len() > 1 {
		a := heap.Pop(&h).(*node)
		b := heap.Pop(&h).(*node)
		heap.Push(&h, &node{weight: a.weight + b.weight, order: order, sym: -1, kids: [2]*node{a, b}})
		order++
	}
	var lengths [numSymbols]uint8
	var walk func(n *node, depth uint8)
	walk = func(n *node, depth uint8) {
		if n.sym >= 0 {
			lengths[n.sym] = depth
			return
		}
		walk(n.kids[0], depth+1)
		walk(n.kids[1], depth+1)
	}
	walk(h[0], 0)
	return lengths
}

// pathBits is the number of bits the code spells p with, EOS included.
func pathBits(lengths [numSymbols]uint8, p string) int {
	n := int(lengths[symEOS])
	for i := 0; i < len(p); i++ {
		s, esc := symbol(p[i])
		n += int(lengths[s])
		if esc {
			n += 8
		}
	}
	return n
}

// fitsInline reports whether p fits a handle spelled inline with g = 0.
func fitsInline(lengths [numSymbols]uint8, p string) bool {
	return 1+tagSize+(pathBits(lengths, p)+7)/8 <= handleSize
}

// report summarizes how many paths fit inline and how long they are.
func report(lengths [numSymbols]uint8, paths []string) string {
	if len(paths) == 0 {
		return "no paths\n"
	}
	fit, rawFit := 0, 0
	var total int
	for _, p := range paths {
		if fitsInline(lengths, p) {
			fit++
		}
		if 1+tagSize+len(p) <= handleSize {
			rawFit++
		}
		total += (pathBits(lengths, p) + 7) / 8
	}
	n := float64(len(paths))
	return fmt.Sprintf("%d paths: %.1f%% fit inline (%.1f%% as raw bytes), mean coded path %.1f bytes\n",
		len(paths), 100*float64(fit)/n, 100*float64(rawFit)/n, float64(total)/n)
}

func generate(lengths [numSymbols]uint8, corpusPaths int) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by cmd/fhgen from a corpus of %d paths; DO NOT EDIT.\n\n", corpusPaths)
	b.WriteString("package absnfs\n\n")
	b.WriteString("// fhCodeLengths is the frozen code of version 1 handles: the code length\n")
	b.WriteString("// of each symbol, the 95 printable ASCII bytes 0x20..0x7E in order, then\n")
	b.WriteString("// EOS, then ESC. The codes are canonical: they follow from the lengths.\n")
	b.WriteString("// Changing a length changes the handle format and needs a new version.\n")
	b.WriteString("var fhCodeLengths = [97]uint8{\n")
	syms := make([]int, numSymbols)
	for i := range syms {
		syms[i] = i
	}
	sort.Ints(syms)
	for _, s := range syms {
		fmt.Fprintf(&b, "\t%d, // %s\n", lengths[s], symbolName(s))
	}
	b.WriteString("}\n")
	return format.Source(b.Bytes())
}

// readTable reads the code lengths back from a file generate wrote.
func readTable(name string) ([numSymbols]uint8, error) {
	var lengths [numSymbols]uint8
	src, err := os.ReadFile(name)
	if err != nil {
		return lengths, err
	}
	n := 0
	for _, line := range strings.Split(string(src), "\n") {
		var l uint8
		if _, err := fmt.Sscanf(strings.TrimSpace(line), "%d, //", &l); err != nil {
			continue
		}
		if n == numSymbols {
			return lengths, fmt.Errorf("%s: more than %d code lengths", name, numSymbols)
		}
		lengths[n] = l
		n++
	}
	if n != numSymbols {
		return lengths, fmt.Errorf("%s: %d code lengths, want %d", name, n, numSymbols)
	}
	return lengths, nil
}

func symbolName(s int) string {
	switch s {
	case symEOS:
		return "EOS"
	case symESC:
		return "ESC"
	}
	return fmt.Sprintf("%q", rune(s+0x20))
}
