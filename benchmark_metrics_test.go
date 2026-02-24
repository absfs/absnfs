package absnfs

import (
	"testing"
	"time"
)

func BenchmarkMetricsIncrementOp(b *testing.B) {
	for _, op := range []string{"READ", "WRITE", "LOOKUP", "GETATTR"} {
		b.Run(op, func(b *testing.B) {
			b.ReportAllocs()
			nfs := benchNFS(b)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				nfs.metrics.IncrementOperationCount(op)
			}
		})
	}
}

func BenchmarkMetricsIncrementOpParallel(b *testing.B) {
	b.ReportAllocs()
	nfs := benchNFS(b)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			nfs.metrics.IncrementOperationCount("READ")
		}
	})
}

func BenchmarkMetricsRecordLatency(b *testing.B) {
	b.ReportAllocs()
	nfs := benchNFS(b)
	d := time.Millisecond
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nfs.metrics.RecordLatency("READ", d)
	}
}

func BenchmarkMetricsRecordLatencyParallel(b *testing.B) {
	b.ReportAllocs()
	nfs := benchNFS(b)
	d := time.Millisecond
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			nfs.metrics.RecordLatency("READ", d)
		}
	})
}

func BenchmarkMetricsGetMetrics(b *testing.B) {
	b.ReportAllocs()
	nfs := benchNFS(b)
	// Pre-populate some metrics
	for i := 0; i < 100; i++ {
		nfs.metrics.IncrementOperationCount("READ")
		nfs.metrics.RecordLatency("READ", time.Duration(i)*time.Microsecond)
		nfs.metrics.RecordOperationResult(false)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nfs.metrics.GetMetrics()
	}
}

func BenchmarkMetricsRecordCacheHit(b *testing.B) {
	b.Run("attr-hit", func(b *testing.B) {
		b.ReportAllocs()
		nfs := benchNFS(b)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			nfs.metrics.RecordAttrCacheHit()
		}
	})
	b.Run("attr-miss", func(b *testing.B) {
		b.ReportAllocs()
		nfs := benchNFS(b)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			nfs.metrics.RecordAttrCacheMiss()
		}
	})
	b.Run("dir-hit", func(b *testing.B) {
		b.ReportAllocs()
		nfs := benchNFS(b)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			nfs.metrics.RecordDirCacheHit()
		}
	})
	b.Run("negative-hit", func(b *testing.B) {
		b.ReportAllocs()
		nfs := benchNFS(b)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			nfs.metrics.RecordNegativeCacheHit()
		}
	})
}

func BenchmarkMetricsRecordOperationResult(b *testing.B) {
	b.ReportAllocs()
	nfs := benchNFS(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nfs.metrics.RecordOperationResult(false)
	}
}

func BenchmarkMetricsIsHealthy(b *testing.B) {
	b.ReportAllocs()
	nfs := benchNFS(b)
	// Pre-populate some results so IsHealthy has data to check
	for i := 0; i < 100; i++ {
		nfs.metrics.RecordOperationResult(false)
		nfs.metrics.RecordLatency("READ", time.Millisecond)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nfs.metrics.IsHealthy()
	}
}
