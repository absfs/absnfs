package absnfs

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

// BenchmarkTokenBucketAllowN measures AllowN with varying batch sizes.
// Existing BenchmarkTokenBucket covers Allow(); this covers the multi-token path.
func BenchmarkTokenBucketAllowN(b *testing.B) {
	for _, n := range []int{1, 5, 10} {
		b.Run(fmt.Sprintf("n/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			tb := NewTokenBucket(1e6, 1000000)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tb.AllowN(n)
			}
		})
	}
}

// BenchmarkTokenBucketParallel measures contended Allow() under parallel access.
func BenchmarkTokenBucketParallel(b *testing.B) {
	b.ReportAllocs()
	tb := NewTokenBucket(1e6, 1000000)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tb.Allow()
		}
	})
}

// BenchmarkPerIPLimiterMultiIP measures per-IP lookup cost as the number of
// tracked IPs grows.
func BenchmarkPerIPLimiterMultiIP(b *testing.B) {
	for _, numIPs := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("ips/%d", numIPs), func(b *testing.B) {
			b.ReportAllocs()
			limiter := NewPerIPLimiter(1e6, 1000000, time.Minute)

			// Pre-warm with N distinct IPs.
			ips := make([]string, numIPs)
			for i := 0; i < numIPs; i++ {
				ips[i] = fmt.Sprintf("10.0.%d.%d", i/256, i%256)
				limiter.Allow(ips[i])
			}

			rng := rand.New(rand.NewSource(42))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				limiter.Allow(ips[rng.Intn(numIPs)])
			}
		})
	}
}

// BenchmarkPerIPLimiterCleanup measures the cost of the cleanup pass by
// forcing stale entries that will be deleted during the next Allow().
func BenchmarkPerIPLimiterCleanup(b *testing.B) {
	b.ReportAllocs()

	// Use a very short cleanup interval so every Allow() triggers cleanup.
	limiter := NewPerIPLimiter(1e6, 1000000, time.Nanosecond)

	// Pre-populate with stale IPs (all at max tokens, so cleanup deletes them).
	for i := 0; i < 200; i++ {
		ip := fmt.Sprintf("10.1.%d.%d", i/256, i%256)
		limiter.Allow(ip)
	}

	// Use a fixed IP for the benchmark loop; each call triggers cleanup of stale entries.
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		limiter.Allow("192.168.0.1")
	}
}

// BenchmarkPerOperationLimiter measures per-operation-type rate limiting for
// each NFS operation category.
func BenchmarkPerOperationLimiter(b *testing.B) {
	config := DefaultRateLimiterConfig()
	// Use very high limits so tokens never exhaust.
	config.ReadLargeOpsPerSecond = 1000000
	config.WriteLargeOpsPerSecond = 1000000
	config.ReaddirOpsPerSecond = 1000000
	config.MountOpsPerMinute = 60000000

	ops := []struct {
		name string
		op   OperationType
	}{
		{"read_large", OpTypeReadLarge},
		{"write_large", OpTypeWriteLarge},
		{"readdir", OpTypeReaddir},
		{"mount", OpTypeMount},
	}

	for _, tc := range ops {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			pol := NewPerOperationLimiter(config)
			// Pre-warm.
			pol.Allow("10.0.0.1", tc.op)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pol.Allow("10.0.0.1", tc.op)
			}
		})
	}
}

// BenchmarkRateLimiterAllowRequest measures the full AllowRequest path that
// checks global, per-IP, and per-connection limits.
// Existing BenchmarkRateLimiter uses a single client; this adds multi-client.
func BenchmarkRateLimiterAllowRequest(b *testing.B) {
	config := DefaultRateLimiterConfig()
	config.GlobalRequestsPerSecond = 10000000
	config.PerIPRequestsPerSecond = 1000000
	config.PerIPBurstSize = 1000000
	config.PerConnectionRequestsPerSecond = 1000000
	config.PerConnectionBurstSize = 1000000

	b.Run("single-client", func(b *testing.B) {
		b.ReportAllocs()
		rl := NewRateLimiter(config)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rl.AllowRequest("10.0.0.1", "conn-1")
		}
	})

	b.Run("multi-client", func(b *testing.B) {
		b.ReportAllocs()
		rl := NewRateLimiter(config)

		// Pre-build IP and connID slices.
		const numClients = 64
		ips := make([]string, numClients)
		conns := make([]string, numClients)
		for i := 0; i < numClients; i++ {
			ips[i] = fmt.Sprintf("10.0.%d.%d", i/256, i%256)
			conns[i] = fmt.Sprintf("conn-%d", i)
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			idx := i % numClients
			rl.AllowRequest(ips[idx], conns[idx])
		}
	})
}

// BenchmarkSlidingWindow measures the sliding window rate limiter's Allow()
// cost. Uses a large maxCount so the window never fills.
func BenchmarkSlidingWindow(b *testing.B) {
	b.ReportAllocs()
	sw := NewSlidingWindow(time.Hour, 1000000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sw.Allow()
	}
}
