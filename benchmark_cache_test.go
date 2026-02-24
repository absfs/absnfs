package absnfs

import (
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// sinkAttrs prevents dead-code elimination for AttrCache benchmarks.
var sinkAttrs *NFSAttrs
var sinkCacheBool bool
var sinkFileInfos []os.FileInfo

// BenchmarkAttrCachePut measures Put latency at different cache fill levels.
func BenchmarkAttrCachePut(b *testing.B) {
	b.Run("empty", func(b *testing.B) {
		b.ReportAllocs()
		cache := benchAttrCache(10*time.Second, b.N+1)
		attrs := benchAttrs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			cache.Put(fmt.Sprintf("/f%d", i), attrs)
		}
	})

	b.Run("half-full", func(b *testing.B) {
		b.ReportAllocs()
		const cap = 10000
		cache := benchAttrCache(10*time.Second, cap)
		attrs := benchAttrs()
		for i := 0; i < cap/2; i++ {
			cache.Put(fmt.Sprintf("/pre%d", i), attrs)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			cache.Put(fmt.Sprintf("/f%d", i), attrs)
		}
	})

	b.Run("full-eviction", func(b *testing.B) {
		b.ReportAllocs()
		const cap = 1000
		cache := benchAttrCache(10*time.Second, cap)
		attrs := benchAttrs()
		for i := 0; i < cap; i++ {
			cache.Put(fmt.Sprintf("/pre%d", i), attrs)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			cache.Put(fmt.Sprintf("/evict%d", i), attrs)
		}
	})
}

// BenchmarkAttrCacheGet measures Get latency for hits, misses, and expired entries.
func BenchmarkAttrCacheGet(b *testing.B) {
	b.Run("hit", func(b *testing.B) {
		b.ReportAllocs()
		cache := benchAttrCache(1*time.Hour, 10000)
		attrs := benchAttrs()
		for i := 0; i < 1000; i++ {
			cache.Put(fmt.Sprintf("/f%d", i), attrs)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			sinkAttrs, sinkCacheBool = cache.Get(fmt.Sprintf("/f%d", i%1000))
		}
	})

	b.Run("miss", func(b *testing.B) {
		b.ReportAllocs()
		cache := benchAttrCache(1*time.Hour, 10000)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			sinkAttrs, sinkCacheBool = cache.Get(fmt.Sprintf("/miss%d", i))
		}
	})

	b.Run("expired", func(b *testing.B) {
		b.ReportAllocs()
		cache := benchAttrCache(1*time.Nanosecond, 10000)
		attrs := benchAttrs()
		for i := 0; i < 1000; i++ {
			cache.Put(fmt.Sprintf("/f%d", i), attrs)
		}
		time.Sleep(10 * time.Millisecond) // ensure all entries expire
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			sinkAttrs, sinkCacheBool = cache.Get(fmt.Sprintf("/f%d", i%1000))
		}
	})
}

// BenchmarkAttrCacheGetParallel measures concurrent Get throughput.
func BenchmarkAttrCacheGetParallel(b *testing.B) {
	b.ReportAllocs()

	cache := benchAttrCache(1*time.Hour, 10000)
	attrs := benchAttrs()
	for i := 0; i < 1000; i++ {
		cache.Put(fmt.Sprintf("/f%d", i), attrs)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var a *NFSAttrs
		var ok bool
		i := 0
		for pb.Next() {
			a, ok = cache.Get(fmt.Sprintf("/f%d", i%1000))
			i++
		}
		_, _ = a, ok
	})
}

// BenchmarkAttrCacheNegative measures negative cache put and get operations.
func BenchmarkAttrCacheNegative(b *testing.B) {
	b.Run("put-negative", func(b *testing.B) {
		b.ReportAllocs()
		cache := benchAttrCache(10*time.Second, b.N+1)
		cache.ConfigureNegativeCaching(true, 5*time.Second)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			cache.PutNegative(fmt.Sprintf("/neg%d", i))
		}
	})

	b.Run("get-negative-hit", func(b *testing.B) {
		b.ReportAllocs()
		cache := benchAttrCache(1*time.Hour, 10000)
		cache.ConfigureNegativeCaching(true, 1*time.Hour)
		for i := 0; i < 1000; i++ {
			cache.PutNegative(fmt.Sprintf("/neg%d", i))
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			sinkAttrs, sinkCacheBool = cache.Get(fmt.Sprintf("/neg%d", i%1000))
		}
	})
}

// BenchmarkAttrCacheInvalidateNegativeInDir measures the cost of invalidating
// negative cache entries within a directory at different entry counts.
func BenchmarkAttrCacheInvalidateNegativeInDir(b *testing.B) {
	for _, count := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("entries/%d", count), func(b *testing.B) {
			b.ReportAllocs()
			cache := benchAttrCache(1*time.Hour, count*2)
			cache.ConfigureNegativeCaching(true, 1*time.Hour)
			for i := 0; i < count; i++ {
				cache.PutNegative(fmt.Sprintf("/dir/child%d", i))
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				// Re-populate for each iteration since invalidation clears entries
				for j := 0; j < count; j++ {
					cache.PutNegative(fmt.Sprintf("/dir/child%d", j))
				}
				b.StartTimer()
				cache.InvalidateNegativeInDir("/dir")
			}
		})
	}
}

// BenchmarkAttrCacheLRUChurn measures Put performance when the cache is full
// and every insert evicts the LRU entry.
func BenchmarkAttrCacheLRUChurn(b *testing.B) {
	b.ReportAllocs()

	const cap = 1000
	cache := benchAttrCache(1*time.Hour, cap)
	attrs := benchAttrs()
	for i := 0; i < cap; i++ {
		cache.Put(fmt.Sprintf("/churn%d", i), attrs)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Put(fmt.Sprintf("/new%d", i), attrs)
	}
}

// mockBenchFileInfo is a minimal os.FileInfo for DirCache benchmarks.
type mockBenchFileInfo struct {
	name string
	size int64
	mode os.FileMode
}

func (m *mockBenchFileInfo) Name() string      { return m.name }
func (m *mockBenchFileInfo) Size() int64       { return m.size }
func (m *mockBenchFileInfo) Mode() os.FileMode { return m.mode }
func (m *mockBenchFileInfo) ModTime() time.Time { return time.Time{} }
func (m *mockBenchFileInfo) IsDir() bool        { return m.mode.IsDir() }
func (m *mockBenchFileInfo) Sys() interface{}   { return nil }

func makeBenchDirEntries(n int) []os.FileInfo {
	entries := make([]os.FileInfo, n)
	for i := range entries {
		entries[i] = &mockBenchFileInfo{
			name: fmt.Sprintf("file_%d", i),
			size: 1024,
			mode: 0644,
		}
	}
	return entries
}

// BenchmarkDirCachePut measures DirCache Put with different directory sizes.
func BenchmarkDirCachePut(b *testing.B) {
	for _, count := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("entries/%d", count), func(b *testing.B) {
			b.ReportAllocs()
			cache := NewDirCache(10*time.Second, b.N+1, count+1)
			entries := makeBenchDirEntries(count)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				cache.Put(fmt.Sprintf("/dir%d", i), entries)
			}
		})
	}
}

// BenchmarkDirCacheGet measures DirCache Get with different directory sizes.
func BenchmarkDirCacheGet(b *testing.B) {
	for _, count := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("entries/%d", count), func(b *testing.B) {
			b.ReportAllocs()
			cache := NewDirCache(1*time.Hour, 10000, count+1)
			entries := makeBenchDirEntries(count)
			for i := 0; i < 100; i++ {
				cache.Put(fmt.Sprintf("/dir%d", i), entries)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sinkFileInfos, sinkCacheBool = cache.Get(fmt.Sprintf("/dir%d", i%100))
			}
		})
	}
}

// BenchmarkDirCacheGetParallel measures concurrent DirCache Get throughput.
func BenchmarkDirCacheGetParallel(b *testing.B) {
	b.ReportAllocs()

	cache := NewDirCache(1*time.Hour, 10000, 101)
	entries := makeBenchDirEntries(100)
	for i := 0; i < 100; i++ {
		cache.Put(fmt.Sprintf("/dir%d", i), entries)
	}

	b.ResetTimer()
	var counter atomic.Uint64
	b.RunParallel(func(pb *testing.PB) {
		var fi []os.FileInfo
		var ok bool
		for pb.Next() {
			n := counter.Add(1)
			fi, ok = cache.Get(fmt.Sprintf("/dir%d", n%100))
		}
		_, _ = fi, ok
	})
}
