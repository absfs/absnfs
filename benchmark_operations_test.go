package absnfs

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func BenchmarkMapError(b *testing.B) {
	b.Run("nil", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			sink = mapError(nil)
		}
	})
	b.Run("ErrNotExist", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			sink = mapError(os.ErrNotExist)
		}
	})
	b.Run("ErrPermission", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			sink = mapError(os.ErrPermission)
		}
	})
	b.Run("InvalidHandle", func(b *testing.B) {
		b.ReportAllocs()
		err := &InvalidFileHandleError{Handle: 42}
		for i := 0; i < b.N; i++ {
			sink = mapError(err)
		}
	})
	b.Run("unknown", func(b *testing.B) {
		b.ReportAllocs()
		err := errors.New("unknown error")
		for i := 0; i < b.N; i++ {
			sink = mapError(err)
		}
	})
}

func BenchmarkSanitizePath(b *testing.B) {
	b.Run("simple", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			sanitizePath("/dir", "file.txt")
		}
	})
	b.Run("long-name", func(b *testing.B) {
		b.ReportAllocs()
		long := strings.Repeat("a", 200)
		for i := 0; i < b.N; i++ {
			sanitizePath("/dir", long)
		}
	})
	b.Run("reject-traversal", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			sanitizePath("/dir", "..")
		}
	})
}

func BenchmarkLookup(b *testing.B) {
	b.Run("cache-hit", func(b *testing.B) {
		b.ReportAllocs()
		nfs := benchNFSWithFiles(b, 100)
		// Prime the cache
		nfs.Lookup("/bench/file_0")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			nfs.Lookup("/bench/file_0")
		}
	})
	b.Run("cache-miss", func(b *testing.B) {
		b.ReportAllocs()
		nfs := benchNFSWithFiles(b, 100)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Invalidate cache before each lookup to force a miss
			nfs.attrCache.Invalidate("/bench/file_0")
			nfs.Lookup("/bench/file_0")
		}
	})
}

func BenchmarkGetAttr(b *testing.B) {
	b.ReportAllocs()
	nfs := benchNFSWithFiles(b, 10)
	node, err := nfs.Lookup("/bench/file_0")
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nfs.GetAttr(node)
	}
}

func BenchmarkRead(b *testing.B) {
	for _, size := range []struct {
		name string
		n    int
	}{
		{"size/4KB", 4096},
		{"size/64KB", 65536},
	} {
		b.Run(size.name, func(b *testing.B) {
			b.ReportAllocs()
			nfs := benchNFS(b)
			// Create a file with enough data
			f, err := nfs.fs.Create("/readfile")
			if err != nil {
				b.Fatal(err)
			}
			data := make([]byte, size.n)
			f.Write(data)
			f.Close()
			node, err := nfs.Lookup("/readfile")
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				nfs.Read(node, 0, int64(size.n))
			}
		})
	}
}

func BenchmarkWrite(b *testing.B) {
	for _, size := range []struct {
		name string
		n    int
	}{
		{"size/4KB", 4096},
		{"size/64KB", 65536},
	} {
		b.Run(size.name, func(b *testing.B) {
			b.ReportAllocs()
			nfs := benchNFS(b)
			// Create the file
			f, err := nfs.fs.Create("/writefile")
			if err != nil {
				b.Fatal(err)
			}
			// Pre-fill so WriteAt doesn't need to extend
			fill := make([]byte, size.n)
			f.Write(fill)
			f.Close()
			node, err := nfs.Lookup("/writefile")
			if err != nil {
				b.Fatal(err)
			}
			data := make([]byte, size.n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				nfs.Write(node, 0, data)
			}
		})
	}
}

func BenchmarkCreateRemove(b *testing.B) {
	b.ReportAllocs()
	nfs := benchNFS(b)
	if err := nfs.fs.Mkdir("/bench", 0755); err != nil {
		b.Fatal(err)
	}
	dirNode, err := nfs.Lookup("/bench")
	if err != nil {
		b.Fatal(err)
	}
	attrs := &NFSAttrs{Mode: 0644}
	attrs.Refresh()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		name := fmt.Sprintf("temp_%d", i)
		nfs.Create(dirNode, name, attrs)
		nfs.Remove(dirNode, name)
	}
}

func BenchmarkReadDir(b *testing.B) {
	for _, count := range []int{10, 100} {
		b.Run(fmt.Sprintf("entries/%d/cold", count), func(b *testing.B) {
			b.ReportAllocs()
			nfs := benchNFSWithFiles(b, count)
			dirNode, err := nfs.Lookup("/bench")
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if nfs.dirCache != nil {
					nfs.dirCache.Clear()
				}
				nfs.attrCache.Clear()
				nfs.ReadDir(dirNode)
			}
		})
		b.Run(fmt.Sprintf("entries/%d/warm", count), func(b *testing.B) {
			b.ReportAllocs()
			nfs := benchNFSWithFiles(b, count)
			dirNode, err := nfs.Lookup("/bench")
			if err != nil {
				b.Fatal(err)
			}
			// Warm the cache
			nfs.ReadDir(dirNode)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				nfs.ReadDir(dirNode)
			}
		})
	}
}
