package absnfs

import (
	"bytes"
	"encoding/binary"
	"os"
	"strings"
	"testing"
	"time"
)

// BenchmarkXDREncodeUint32 measures encoding a single uint32 in XDR format.
func BenchmarkXDREncodeUint32(b *testing.B) {
	b.ReportAllocs()
	var buf bytes.Buffer
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		xdrEncodeUint32(&buf, 0xDEADBEEF)
	}
}

// BenchmarkXDREncodeUint64 measures encoding a single uint64 in XDR format.
func BenchmarkXDREncodeUint64(b *testing.B) {
	b.ReportAllocs()
	var buf bytes.Buffer
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		xdrEncodeUint64(&buf, 0xDEADBEEFCAFEBABE)
	}
}

// BenchmarkXDREncodeString measures XDR string encoding at varying lengths.
func BenchmarkXDREncodeString(b *testing.B) {
	b.ReportAllocs()

	for _, size := range []int{8, 64, 255} {
		s := strings.Repeat("x", size)
		b.Run("len/"+benchItoa(size), func(b *testing.B) {
			b.ReportAllocs()
			var buf bytes.Buffer
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf.Reset()
				xdrEncodeString(&buf, s)
			}
		})
	}
}

// BenchmarkXDRDecodeString measures XDR string decoding at varying lengths.
func BenchmarkXDRDecodeString(b *testing.B) {
	b.ReportAllocs()

	for _, size := range []int{8, 64, 255} {
		s := strings.Repeat("x", size)
		var encoded bytes.Buffer
		xdrEncodeString(&encoded, s)
		data := encoded.Bytes()

		b.Run("len/"+benchItoa(size), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r := bytes.NewReader(data)
				sinkStr, _ = xdrDecodeString(r)
			}
		})
	}
}

// BenchmarkXDREncodeFileHandle measures encoding an NFS3 file handle.
func BenchmarkXDREncodeFileHandle(b *testing.B) {
	b.ReportAllocs()
	var buf bytes.Buffer
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		xdrEncodeFileHandle(&buf, 42)
	}
}

// BenchmarkXDRDecodeFileHandle measures decoding an NFS3 file handle.
func BenchmarkXDRDecodeFileHandle(b *testing.B) {
	b.ReportAllocs()
	var encoded bytes.Buffer
	xdrEncodeFileHandle(&encoded, 42)
	data := encoded.Bytes()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := bytes.NewReader(data)
		sinkUint64, _ = xdrDecodeFileHandle(r)
	}
}

// BenchmarkEncodeFileAttributes measures fattr3 encoding for different file types.
func BenchmarkEncodeFileAttributes(b *testing.B) {
	b.ReportAllocs()

	now := time.Now()

	b.Run("regular-file", func(b *testing.B) {
		b.ReportAllocs()
		attrs := &NFSAttrs{Mode: 0644, Size: 4096, FileId: 1, Uid: 1000, Gid: 1000}
		attrs.SetMtime(now)
		attrs.SetAtime(now)
		var buf bytes.Buffer
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			buf.Reset()
			encodeFileAttributes(&buf, attrs)
		}
	})

	b.Run("directory", func(b *testing.B) {
		b.ReportAllocs()
		attrs := &NFSAttrs{Mode: os.ModeDir | 0755, Size: 0, FileId: 2, Uid: 0, Gid: 0}
		attrs.SetMtime(now)
		attrs.SetAtime(now)
		var buf bytes.Buffer
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			buf.Reset()
			encodeFileAttributes(&buf, attrs)
		}
	})

	b.Run("symlink", func(b *testing.B) {
		b.ReportAllocs()
		attrs := &NFSAttrs{Mode: os.ModeSymlink | 0777, Size: 16, FileId: 3, Uid: 1000, Gid: 1000}
		attrs.SetMtime(now)
		attrs.SetAtime(now)
		var buf bytes.Buffer
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			buf.Reset()
			encodeFileAttributes(&buf, attrs)
		}
	})
}

// BenchmarkEncodeWccAttr measures wcc_attr encoding (pre/post operation attributes).
func BenchmarkEncodeWccAttr(b *testing.B) {
	b.ReportAllocs()
	now := time.Now()
	attrs := &NFSAttrs{Size: 8192}
	attrs.SetMtime(now)
	var buf bytes.Buffer
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		encodeWccAttr(&buf, attrs)
	}
}

// BenchmarkDecodeRPCCall measures decoding a full RPC call header with AUTH_SYS credential.
func BenchmarkDecodeRPCCall(b *testing.B) {
	b.ReportAllocs()

	// Build a complete RPC call message with AUTH_SYS credential.
	var msg bytes.Buffer

	// Header: xid, msgType=CALL, rpcVersion=2, program=NFS, version=3, procedure=GETATTR
	binary.Write(&msg, binary.BigEndian, uint32(0x12345678)) // xid
	binary.Write(&msg, binary.BigEndian, uint32(RPC_CALL))   // msgType
	binary.Write(&msg, binary.BigEndian, uint32(2))          // rpcVersion
	binary.Write(&msg, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&msg, binary.BigEndian, uint32(NFS_V3))
	binary.Write(&msg, binary.BigEndian, uint32(NFSPROC3_GETATTR))

	// Credential: AUTH_SYS
	authBody := buildAuthSysBody(1, "bench", 1000, 1000, []uint32{100, 200})
	binary.Write(&msg, binary.BigEndian, uint32(AUTH_SYS))
	binary.Write(&msg, binary.BigEndian, uint32(len(authBody)))
	msg.Write(authBody)
	// Pad credential to 4-byte boundary
	if pad := (4 - len(authBody)%4) % 4; pad > 0 {
		msg.Write(make([]byte, pad))
	}

	// Verifier: AUTH_NONE, empty body
	binary.Write(&msg, binary.BigEndian, uint32(AUTH_NONE))
	binary.Write(&msg, binary.BigEndian, uint32(0))

	data := msg.Bytes()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		r := bytes.NewReader(data)
		sink, _ = DecodeRPCCall(r)
	}
}

// BenchmarkEncodeRPCReply measures RPC reply encoding for different response types.
func BenchmarkEncodeRPCReply(b *testing.B) {
	b.ReportAllocs()

	b.Run("success-bytes", func(b *testing.B) {
		b.ReportAllocs()
		reply := &RPCReply{
			Header:       RPCMsgHeader{Xid: 1},
			Status:       MSG_ACCEPTED,
			AcceptStatus: SUCCESS,
			Verifier:     RPCVerifier{Flavor: AUTH_NONE, Body: []byte{}},
			Data:         []byte{0, 0, 0, 0}, // NFS_OK
		}
		var buf bytes.Buffer
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			buf.Reset()
			EncodeRPCReply(&buf, reply)
		}
	})

	b.Run("success-attrs", func(b *testing.B) {
		b.ReportAllocs()
		now := time.Now()
		attrs := &NFSAttrs{Mode: 0644, Size: 4096, FileId: 1, Uid: 1000, Gid: 1000}
		attrs.SetMtime(now)
		attrs.SetAtime(now)
		reply := &RPCReply{
			Header:       RPCMsgHeader{Xid: 2},
			Status:       MSG_ACCEPTED,
			AcceptStatus: SUCCESS,
			Verifier:     RPCVerifier{Flavor: AUTH_NONE, Body: []byte{}},
			Data:         attrs,
		}
		var buf bytes.Buffer
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			buf.Reset()
			EncodeRPCReply(&buf, reply)
		}
	})

	b.Run("error", func(b *testing.B) {
		b.ReportAllocs()
		reply := &RPCReply{
			Header:       RPCMsgHeader{Xid: 3},
			Status:       MSG_DENIED,
			AcceptStatus: 0,
			Verifier:     RPCVerifier{Flavor: AUTH_NONE, Body: []byte{}},
		}
		var buf bytes.Buffer
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			buf.Reset()
			EncodeRPCReply(&buf, reply)
		}
	})
}

// Sinks for XDR benchmarks to prevent dead-code elimination.
var (
	sinkStr    string
	sinkUint64 uint64
)
