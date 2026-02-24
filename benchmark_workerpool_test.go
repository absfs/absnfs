package absnfs

import (
	"fmt"
	"io"
	"log"
	"runtime"
	"testing"
)

// BenchmarkWorkerPoolSubmitAsync measures fire-and-forget Submit throughput.
// Existing BenchmarkWorkerPool uses SubmitWait; this tests the async path.
func BenchmarkWorkerPoolSubmitAsync(b *testing.B) {
	b.ReportAllocs()
	mockServer := &AbsfsNFS{
		logger: log.New(io.Discard, "", 0),
	}
	pool := NewWorkerPool(runtime.NumCPU(), mockServer)
	pool.Start()
	defer pool.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pool.Submit(func() interface{} {
			return nil
		})
	}
}

// BenchmarkWorkerPoolQueueSaturation measures SubmitWait with a trivial task,
// exercising the full submit-execute-return path without simulated work.
// Existing BenchmarkWorkerPool uses a 1000-iteration loop as work; this
// isolates pool overhead from payload cost.
func BenchmarkWorkerPoolQueueSaturation(b *testing.B) {
	b.ReportAllocs()
	mockServer := &AbsfsNFS{
		logger: log.New(io.Discard, "", 0),
	}
	pool := NewWorkerPool(runtime.NumCPU(), mockServer)
	pool.Start()
	defer pool.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pool.SubmitWait(func() interface{} {
			return nil
		})
	}
}

// BenchmarkWorkerPoolScaling measures how pool throughput scales with worker
// count. Existing benchmarks use NumCPU only; this sweeps multiple sizes.
func BenchmarkWorkerPoolScaling(b *testing.B) {
	numCPU := runtime.NumCPU()
	workerCounts := []struct {
		name  string
		count int
	}{
		{"workers/1", 1},
		{"workers/4", 4},
		{fmt.Sprintf("workers/%d", numCPU), numCPU},
		{fmt.Sprintf("workers/%d", numCPU*4), numCPU * 4},
	}

	for _, tc := range workerCounts {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			mockServer := &AbsfsNFS{
				logger: log.New(io.Discard, "", 0),
			}
			pool := NewWorkerPool(tc.count, mockServer)
			pool.Start()
			defer pool.Stop()

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					pool.SubmitWait(func() interface{} {
						return nil
					})
				}
			})
		})
	}
}
