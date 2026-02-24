package absnfs

import (
	"encoding/binary"
	"testing"
)

// BenchmarkValidateAuthentication measures authentication validation
// across different credential flavors and squash modes.
func BenchmarkValidateAuthentication(b *testing.B) {
	b.ReportAllocs()

	policy := &PolicyOptions{Squash: "none"}

	b.Run("auth-none", func(b *testing.B) {
		b.ReportAllocs()
		ctx := &AuthContext{
			ClientIP:   "192.168.1.10",
			ClientPort: 800,
			Credential: &RPCCredential{Flavor: AUTH_NONE},
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			sink = ValidateAuthentication(ctx, policy)
		}
	})

	// Build a valid AUTH_SYS credential body for reuse.
	authSysBody := buildAuthSysBody(1, "client", 1000, 1000, []uint32{100, 200})

	b.Run("auth-sys-no-squash", func(b *testing.B) {
		b.ReportAllocs()
		p := &PolicyOptions{Squash: "none"}
		ctx := &AuthContext{
			ClientIP:   "192.168.1.10",
			ClientPort: 800,
			Credential: &RPCCredential{Flavor: AUTH_SYS, Body: authSysBody},
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Clear AuthSys so ParseAuthSysCredential runs each iteration.
			ctx.AuthSys = nil
			sink = ValidateAuthentication(ctx, p)
		}
	})

	b.Run("root-squash", func(b *testing.B) {
		b.ReportAllocs()
		rootBody := buildAuthSysBody(1, "client", 0, 0, []uint32{0, 100})
		p := &PolicyOptions{Squash: "root"}
		ctx := &AuthContext{
			ClientIP:   "192.168.1.10",
			ClientPort: 800,
			Credential: &RPCCredential{Flavor: AUTH_SYS, Body: rootBody},
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			ctx.AuthSys = nil
			sink = ValidateAuthentication(ctx, p)
		}
	})

	b.Run("all-squash", func(b *testing.B) {
		b.ReportAllocs()
		p := &PolicyOptions{Squash: "all"}
		ctx := &AuthContext{
			ClientIP:   "192.168.1.10",
			ClientPort: 800,
			Credential: &RPCCredential{Flavor: AUTH_SYS, Body: authSysBody},
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			ctx.AuthSys = nil
			sink = ValidateAuthentication(ctx, p)
		}
	})
}

// BenchmarkIsIPAllowed measures IP allow-list checking with direct IPs,
// CIDR ranges, and worst-case miss scenarios.
func BenchmarkIsIPAllowed(b *testing.B) {
	b.ReportAllocs()

	b.Run("direct-match/5", func(b *testing.B) {
		b.ReportAllocs()
		allowed := []string{"10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4", "192.168.1.10"}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			sinkBool = isIPAllowed("192.168.1.10", allowed)
		}
	})

	b.Run("cidr-match/5", func(b *testing.B) {
		b.ReportAllocs()
		allowed := []string{"10.0.0.0/24", "10.1.0.0/16", "172.16.0.0/12", "192.0.2.0/24", "192.168.1.0/24"}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			sinkBool = isIPAllowed("192.168.1.10", allowed)
		}
	})

	b.Run("no-match/100", func(b *testing.B) {
		b.ReportAllocs()
		allowed := make([]string, 100)
		for i := range allowed {
			allowed[i] = "10.0." + benchItoa(i/256) + "." + benchItoa(i%256)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			sinkBool = isIPAllowed("192.168.1.10", allowed)
		}
	})
}

// BenchmarkParseAuthSysCredential measures parsing of XDR-encoded AUTH_SYS bodies.
func BenchmarkParseAuthSysCredential(b *testing.B) {
	b.ReportAllocs()

	body := buildAuthSysBody(12345, "testhost.example.com", 1000, 1000, []uint32{100, 200, 300, 500})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink, _ = ParseAuthSysCredential(body)
	}
}

// buildAuthSysBody creates a valid XDR-encoded AUTH_SYS credential body.
func buildAuthSysBody(stamp uint32, machine string, uid, gid uint32, auxGIDs []uint32) []byte {
	// stamp(4) + machineLen(4) + machine(padded) + uid(4) + gid(4) + count(4) + gids(4*n)
	machineBytes := []byte(machine)
	padding := (4 - len(machineBytes)%4) % 4
	size := 4 + 4 + len(machineBytes) + padding + 4 + 4 + 4 + 4*len(auxGIDs)
	buf := make([]byte, size)
	off := 0

	binary.BigEndian.PutUint32(buf[off:], stamp)
	off += 4
	binary.BigEndian.PutUint32(buf[off:], uint32(len(machineBytes)))
	off += 4
	copy(buf[off:], machineBytes)
	off += len(machineBytes) + padding
	binary.BigEndian.PutUint32(buf[off:], uid)
	off += 4
	binary.BigEndian.PutUint32(buf[off:], gid)
	off += 4
	binary.BigEndian.PutUint32(buf[off:], uint32(len(auxGIDs)))
	off += 4
	for _, g := range auxGIDs {
		binary.BigEndian.PutUint32(buf[off:], g)
		off += 4
	}
	return buf
}

// benchItoa converts small non-negative ints to strings without importing strconv.
func benchItoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := make([]byte, 0, 4)
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	// reverse
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}

// Sinks to prevent dead-code elimination.
var (
	sink     interface{}
	sinkBool bool
)
