package absnfs

import (
	"testing"
	"time"
)

func TestTokenBucket(t *testing.T) {
	t.Run("allows requests within rate", func(t *testing.T) {
		tb := NewTokenBucket(10, 10) // 10 tokens per second, burst of 10

		// Should allow 10 immediate requests (burst)
		for i := 0; i < 10; i++ {
			if !tb.Allow() {
				t.Errorf("request %d should be allowed (within burst)", i)
			}
		}

		// 11th request should be denied (bucket empty)
		if tb.Allow() {
			t.Error("request should be denied (bucket empty)")
		}
	})

	t.Run("refills over time", func(t *testing.T) {
		tb := NewTokenBucket(10, 5) // 10 tokens per second, burst of 5

		// Consume all tokens
		for i := 0; i < 5; i++ {
			tb.Allow()
		}

		// Wait for refill (100ms = 1 token at 10 tokens/sec)
		time.Sleep(150 * time.Millisecond)

		// Should allow one more request
		if !tb.Allow() {
			t.Error("request should be allowed after refill")
		}
	})

	t.Run("caps at max tokens", func(t *testing.T) {
		tb := NewTokenBucket(10, 5) // 10 tokens per second, burst of 5

		// Wait for a while
		time.Sleep(1 * time.Second)

		// Should still only allow 5 requests (capped at burst size)
		for i := 0; i < 5; i++ {
			if !tb.Allow() {
				t.Errorf("request %d should be allowed", i)
			}
		}

		// 6th should be denied
		if tb.Allow() {
			t.Error("request should be denied (above burst)")
		}
	})
}

func TestSlidingWindow(t *testing.T) {
	t.Run("allows requests within window", func(t *testing.T) {
		sw := NewSlidingWindow(1*time.Second, 5)

		// Should allow 5 requests
		for i := 0; i < 5; i++ {
			if !sw.Allow() {
				t.Errorf("request %d should be allowed", i)
			}
		}

		// 6th should be denied
		if sw.Allow() {
			t.Error("request should be denied (window full)")
		}
	})

	t.Run("slides window over time", func(t *testing.T) {
		sw := NewSlidingWindow(100*time.Millisecond, 3)

		// Fill the window
		for i := 0; i < 3; i++ {
			sw.Allow()
		}

		// Wait for window to slide
		time.Sleep(150 * time.Millisecond)

		// Should allow new requests
		if !sw.Allow() {
			t.Error("request should be allowed after window slides")
		}
	})

	t.Run("counts correctly", func(t *testing.T) {
		sw := NewSlidingWindow(1*time.Second, 10)

		// Add some requests
		for i := 0; i < 5; i++ {
			sw.Allow()
		}

		count := sw.Count()
		if count != 5 {
			t.Errorf("expected count 5, got %d", count)
		}
	})
}

func TestPerIPLimiter(t *testing.T) {
	t.Run("limits per IP", func(t *testing.T) {
		limiter := NewPerIPLimiter(10, 5, 1*time.Minute)

		// IP1 should get 5 requests (burst)
		for i := 0; i < 5; i++ {
			if !limiter.Allow("192.168.1.1") {
				t.Errorf("IP1 request %d should be allowed", i)
			}
		}

		// IP1's 6th request should be denied
		if limiter.Allow("192.168.1.1") {
			t.Error("IP1 request should be denied")
		}

		// IP2 should still be allowed (separate bucket)
		if !limiter.Allow("192.168.1.2") {
			t.Error("IP2 request should be allowed (separate bucket)")
		}
	})

	t.Run("cleanup removes inactive limiters", func(t *testing.T) {
		limiter := NewPerIPLimiter(10, 5, 100*time.Millisecond)

		// Use a few IPs
		limiter.Allow("192.168.1.1")
		limiter.Allow("192.168.1.2")

		// Wait for cleanup interval
		time.Sleep(150 * time.Millisecond)

		// Trigger cleanup by allowing a new IP
		limiter.Allow("192.168.1.3")

		// Stats should show buckets were cleaned up (they're at max capacity)
		stats := limiter.GetStats()
		if len(stats) > 1 {
			t.Logf("Stats after cleanup: %d entries", len(stats))
		}
	})
}

func TestPerOperationLimiter(t *testing.T) {
	config := DefaultRateLimiterConfig()
	limiter := NewPerOperationLimiter(config)

	t.Run("limits per operation type", func(t *testing.T) {
		ip := "192.168.1.1"

		// Large reads should be limited
		for i := 0; i < 10; i++ {
			if !limiter.Allow(ip, OpTypeReadLarge) {
				t.Errorf("read request %d should be allowed", i)
			}
		}

		// 11th large read should be denied (burst is 10)
		if limiter.Allow(ip, OpTypeReadLarge) {
			t.Error("read request should be denied (burst exceeded)")
		}

		// Other operation types should still be allowed
		if !limiter.Allow(ip, OpTypeWriteLarge) {
			t.Error("write request should be allowed (different operation type)")
		}
	})

	t.Run("different IPs have separate limits", func(t *testing.T) {
		// Use up IP1's quota
		for i := 0; i < 10; i++ {
			limiter.Allow("192.168.1.1", OpTypeReadLarge)
		}

		// IP2 should still be allowed
		if !limiter.Allow("192.168.1.2", OpTypeReadLarge) {
			t.Error("IP2 should be allowed (separate quota)")
		}
	})
}

func TestRateLimiter(t *testing.T) {
	config := RateLimiterConfig{
		GlobalRequestsPerSecond:        1000,
		PerIPRequestsPerSecond:         100,
		PerIPBurstSize:                 10,
		PerConnectionRequestsPerSecond: 50,
		PerConnectionBurstSize:         5,
		ReadLargeOpsPerSecond:          10,
		WriteLargeOpsPerSecond:         5,
		ReaddirOpsPerSecond:            2,
		MountOpsPerMinute:              10,
		FileHandlesPerIP:               100,
		FileHandlesGlobal:              1000,
		CleanupInterval:                1 * time.Minute,
	}

	t.Run("enforces global rate limit", func(t *testing.T) {
		rl := NewRateLimiter(config)

		// Should allow requests up to burst
		for i := 0; i < 100; i++ {
			if !rl.AllowRequest("192.168.1.1", "conn1") {
				// Expected - hit the per-IP burst limit
				break
			}
		}
	})

	t.Run("enforces per-IP rate limit", func(t *testing.T) {
		rl := NewRateLimiter(config)

		// Use up IP's quota
		for i := 0; i < 10; i++ {
			rl.AllowRequest("192.168.1.1", "conn1")
		}

		// Should be denied now
		if rl.AllowRequest("192.168.1.1", "conn1") {
			t.Error("request should be denied (IP quota exceeded)")
		}

		// Different IP should still work
		if !rl.AllowRequest("192.168.1.2", "conn2") {
			t.Error("different IP should be allowed")
		}
	})

	t.Run("enforces per-connection rate limit", func(t *testing.T) {
		rl := NewRateLimiter(config)

		// Use up connection's quota (burst size is 5)
		for i := 0; i < 5; i++ {
			if !rl.AllowRequest("192.168.1.1", "conn1") {
				t.Errorf("request %d should be allowed", i)
			}
		}

		// Next request should be denied
		if rl.AllowRequest("192.168.1.1", "conn1") {
			t.Error("request should be denied (connection quota exceeded)")
		}

		// Different connection from same IP should still work (has its own burst)
		if !rl.AllowRequest("192.168.1.1", "conn2") {
			t.Error("different connection should be allowed")
		}
	})

	t.Run("enforces operation-specific limits", func(t *testing.T) {
		rl := NewRateLimiter(config)
		ip := "192.168.1.1"

		// Use up large read quota
		for i := 0; i < 10; i++ {
			rl.AllowOperation(ip, OpTypeReadLarge)
		}

		// Should be denied now
		if rl.AllowOperation(ip, OpTypeReadLarge) {
			t.Error("large read should be denied (quota exceeded)")
		}

		// Other operation types should still work
		if !rl.AllowOperation(ip, OpTypeWriteLarge) {
			t.Error("write should be allowed (different operation type)")
		}
	})

	t.Run("manages file handle allocation", func(t *testing.T) {
		smallConfig := config
		smallConfig.FileHandlesPerIP = 5
		smallConfig.FileHandlesGlobal = 10
		rl := NewRateLimiter(smallConfig)

		ip1 := "192.168.1.1"
		ip2 := "192.168.1.2"

		// Allocate 5 handles for IP1
		for i := 0; i < 5; i++ {
			if !rl.AllocateFileHandle(ip1) {
				t.Errorf("allocation %d should succeed", i)
			}
		}

		// 6th allocation for IP1 should fail (per-IP limit)
		if rl.AllocateFileHandle(ip1) {
			t.Error("allocation should fail (per-IP limit)")
		}

		// IP2 should be able to allocate 5 more
		for i := 0; i < 5; i++ {
			if !rl.AllocateFileHandle(ip2) {
				t.Errorf("IP2 allocation %d should succeed", i)
			}
		}

		// Now we've hit the global limit (10 total)
		if rl.AllocateFileHandle(ip2) {
			t.Error("allocation should fail (global limit)")
		}

		// Release some handles
		rl.ReleaseFileHandle(ip1)
		rl.ReleaseFileHandle(ip1)

		// Now IP1 should be able to allocate again
		if !rl.AllocateFileHandle(ip1) {
			t.Error("allocation should succeed after release")
		}
	})

	t.Run("cleanup connection removes limiter", func(t *testing.T) {
		rl := NewRateLimiter(config)

		// Use the connection
		rl.AllowRequest("192.168.1.1", "conn1")

		// Cleanup should not error
		rl.CleanupConnection("conn1")
	})

	t.Run("get stats returns data", func(t *testing.T) {
		rl := NewRateLimiter(config)

		// Use some quota
		rl.AllowRequest("192.168.1.1", "conn1")
		rl.AllowOperation("192.168.1.1", OpTypeReadLarge)

		stats := rl.GetStats()
		if stats == nil {
			t.Error("stats should not be nil")
		}

		if stats["global_tokens"] == nil {
			t.Error("global_tokens should be present")
		}
	})
}

func TestRateLimiterIntegration(t *testing.T) {
	t.Run("realistic DoS scenario", func(t *testing.T) {
		config := DefaultRateLimiterConfig()
		config.PerIPRequestsPerSecond = 10
		config.PerIPBurstSize = 5
		rl := NewRateLimiter(config)

		attackerIP := "10.0.0.100"
		legitimateIP := "192.168.1.50"

		// Attacker floods with requests
		allowedCount := 0
		for i := 0; i < 100; i++ {
			if rl.AllowRequest(attackerIP, "attacker-conn") {
				allowedCount++
			}
		}

		// Attacker should only get burst size requests
		if allowedCount > 10 {
			t.Errorf("attacker got too many requests: %d (expected <= 10)", allowedCount)
		}

		// Legitimate user should still be able to make requests
		legitimateAllowed := 0
		for i := 0; i < 5; i++ {
			if rl.AllowRequest(legitimateIP, "legit-conn") {
				legitimateAllowed++
			}
		}

		if legitimateAllowed == 0 {
			t.Error("legitimate user should be able to make requests")
		}

		t.Logf("Attacker allowed: %d, Legitimate allowed: %d", allowedCount, legitimateAllowed)
	})
}

func BenchmarkTokenBucket(b *testing.B) {
	tb := NewTokenBucket(1000, 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tb.Allow()
	}
}

func BenchmarkPerIPLimiter(b *testing.B) {
	limiter := NewPerIPLimiter(1000, 100, 1*time.Minute)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		limiter.Allow("192.168.1.1")
	}
}

func BenchmarkRateLimiter(b *testing.B) {
	config := DefaultRateLimiterConfig()
	rl := NewRateLimiter(config)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rl.AllowRequest("192.168.1.1", "conn1")
	}
}
