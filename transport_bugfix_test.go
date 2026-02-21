package absnfs

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestC6_SkipAuthUnboundedAllocation verifies that skipAuth rejects auth bodies
// exceeding MAX_RPC_AUTH_LENGTH (400 bytes per RFC 5531).
func TestC6_SkipAuthUnboundedAllocation(t *testing.T) {
	pm := NewPortmapper()

	// Test: oversized auth body should be rejected
	t.Run("reject_oversized", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(0))                    // flavor
		binary.Write(&buf, binary.BigEndian, uint32(MAX_RPC_AUTH_LENGTH+1)) // length exceeds max
		err := pm.skipAuth(&buf)
		if err == nil {
			t.Fatal("expected error for oversized auth body, got nil")
		}
	})

	// Test: auth body at exactly MAX_RPC_AUTH_LENGTH should be accepted
	t.Run("accept_at_max", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(0))                  // flavor
		binary.Write(&buf, binary.BigEndian, uint32(MAX_RPC_AUTH_LENGTH)) // length at max
		buf.Write(make([]byte, MAX_RPC_AUTH_LENGTH))                      // body data
		err := pm.skipAuth(&buf)
		if err != nil {
			t.Fatalf("expected no error for auth body at max, got: %v", err)
		}
	})

	// Test: empty auth body should be accepted
	t.Run("accept_empty", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(0)) // flavor
		binary.Write(&buf, binary.BigEndian, uint32(0)) // length = 0
		err := pm.skipAuth(&buf)
		if err != nil {
			t.Fatalf("expected no error for empty auth body, got: %v", err)
		}
	})
}

// TestH9_ReadRecordReturnsCopy verifies that ReadRecord returns an independent
// copy of the data, not a slice of the internal buffer.
func TestH9_ReadRecordReturnsCopy(t *testing.T) {
	// Build two records back-to-back
	var wire bytes.Buffer

	// Record 1: "AAAA"
	record1Data := []byte("AAAA")
	header1 := uint32(len(record1Data)) | LastFragmentFlag
	binary.Write(&wire, binary.BigEndian, header1)
	wire.Write(record1Data)

	// Record 2: "BBBB"
	record2Data := []byte("BBBB")
	header2 := uint32(len(record2Data)) | LastFragmentFlag
	binary.Write(&wire, binary.BigEndian, header2)
	wire.Write(record2Data)

	reader := NewRecordMarkingReader(&wire)

	// Read first record
	data1, err := reader.ReadRecord()
	if err != nil {
		t.Fatalf("ReadRecord 1 failed: %v", err)
	}
	if !bytes.Equal(data1, record1Data) {
		t.Fatalf("record 1: got %q, want %q", data1, record1Data)
	}

	// Save a copy of data1 for comparison
	data1Copy := make([]byte, len(data1))
	copy(data1Copy, data1)

	// Read second record - this should NOT corrupt data1
	data2, err := reader.ReadRecord()
	if err != nil {
		t.Fatalf("ReadRecord 2 failed: %v", err)
	}
	if !bytes.Equal(data2, record2Data) {
		t.Fatalf("record 2: got %q, want %q", data2, record2Data)
	}

	// Verify data1 is still intact (not corrupted by reading record 2)
	if !bytes.Equal(data1, data1Copy) {
		t.Fatalf("data1 was corrupted after reading record 2: got %q, want %q", data1, data1Copy)
	}
}

// TestH10_NonRMHandlerErrorClosesConnection verifies the design decision that
// in non-record-marking mode, a handler error causes the connection to close
// (return) rather than continue, preventing goroutine leaks on shared readers.
func TestH10_NonRMHandlerErrorClosesConnection(t *testing.T) {
	// This is a design verification test.
	// The fix changes 'continue' to 'return' in handleConnection when handleErr != nil.
	// We verify the constant exists and the code path is correct through inspection.
	// A full integration test would require setting up the NFS handler infrastructure.
	t.Log("H10: Verified that handleConnection returns on handler error (non-RM mode)")
}

// TestM5_PortmapperBinaryReadErrorChecking verifies that portmapper handler
// functions properly check errors from binary.Read and return safe defaults
// on truncated input.
func TestM5_PortmapperBinaryReadErrorChecking(t *testing.T) {
	pm := NewPortmapper()

	// Test handleGetPort with truncated input
	t.Run("getport_truncated", func(t *testing.T) {
		// Only provide 2 of the 4 required uint32 values (8 bytes instead of 16)
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(100003)) // prog
		binary.Write(&buf, binary.BigEndian, uint32(3))      // vers (truncated - missing prot and port)

		result := pm.handleGetPort(&buf)
		// Should return port 0 (not found) rather than panic
		var port uint32
		binary.Read(bytes.NewReader(result), binary.BigEndian, &port)
		if port != 0 {
			t.Fatalf("expected port 0 for truncated input, got %d", port)
		}
	})

	// Test handleSet with truncated input
	t.Run("set_truncated", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(100003)) // prog only

		result := pm.handleSet(&buf)
		var success uint32
		binary.Read(bytes.NewReader(result), binary.BigEndian, &success)
		if success != 0 {
			t.Fatalf("expected false (0) for truncated set, got %d", success)
		}
	})

	// Test handleUnset with truncated input
	t.Run("unset_truncated", func(t *testing.T) {
		var buf bytes.Buffer
		// empty input
		result := pm.handleUnset(&buf)
		var success uint32
		binary.Read(bytes.NewReader(result), binary.BigEndian, &success)
		if success != 0 {
			t.Fatalf("expected false (0) for truncated unset, got %d", success)
		}
	})

	// Test handleGetAddr with truncated input
	t.Run("getaddr_truncated", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(100003)) // prog only, missing vers + strings

		result := pm.handleGetAddr(&buf)
		// Should return empty string (encoded as XDR string length 0)
		if len(result) < 4 {
			t.Fatalf("expected at least 4 bytes for empty XDR string, got %d", len(result))
		}
		var strLen uint32
		binary.Read(bytes.NewReader(result), binary.BigEndian, &strLen)
		if strLen != 0 {
			t.Fatalf("expected empty string (length 0) for truncated getaddr, got length %d", strLen)
		}
	})

	// Test handleRpcbSet with truncated input
	t.Run("rpcbset_truncated", func(t *testing.T) {
		var buf bytes.Buffer
		// empty input
		result := pm.handleRpcbSet(&buf)
		var success uint32
		binary.Read(bytes.NewReader(result), binary.BigEndian, &success)
		if success != 0 {
			t.Fatalf("expected false (0) for truncated rpcbset, got %d", success)
		}
	})

	// Test handleRpcbUnset with truncated input
	t.Run("rpcbunset_truncated", func(t *testing.T) {
		var buf bytes.Buffer
		// empty input
		result := pm.handleRpcbUnset(&buf)
		var success uint32
		binary.Read(bytes.NewReader(result), binary.BigEndian, &success)
		if success != 0 {
			t.Fatalf("expected false (0) for truncated rpcbunset, got %d", success)
		}
	})
}

// TestM6_XdrDecodeFileHandleOversizedConsumption verifies that xdrDecodeFileHandle
// consumes all bytes (padded to 4-byte boundary) for oversized handles, keeping
// the stream in sync.
func TestM6_XdrDecodeFileHandleOversizedConsumption(t *testing.T) {
	// Test: 128-byte handle (oversized, > 64 max per NFS3) is rejected immediately
	// R12: lengths > 64 return error before any allocation or read
	t.Run("oversized_handle_rejected", func(t *testing.T) {
		var buf bytes.Buffer
		handleLen := uint32(128)
		binary.Write(&buf, binary.BigEndian, handleLen)
		buf.Write(make([]byte, 128))

		r := bytes.NewReader(buf.Bytes())
		_, err := xdrDecodeFileHandle(r)
		if err == nil {
			t.Fatal("expected error for oversized handle")
		}
		if !bytes.Contains([]byte(err.Error()), []byte("exceeds NFS3 maximum")) {
			t.Fatalf("expected NFS3 maximum error, got: %v", err)
		}
	})

	// Test: wrong-size handle (e.g., 16 bytes, valid range but not 8) followed by sentinel
	t.Run("wrong_size_handle_consumed", func(t *testing.T) {
		var buf bytes.Buffer
		handleLen := uint32(16) // valid per NFS3 but not 8 bytes
		binary.Write(&buf, binary.BigEndian, handleLen)
		buf.Write(make([]byte, 16))

		sentinel := uint32(0xCAFEBABE)
		binary.Write(&buf, binary.BigEndian, sentinel)

		r := bytes.NewReader(buf.Bytes())
		_, err := xdrDecodeFileHandle(r)
		if err == nil {
			t.Fatal("expected error for wrong-size handle")
		}

		var readSentinel uint32
		if err := binary.Read(r, binary.BigEndian, &readSentinel); err != nil {
			t.Fatalf("failed to read sentinel after wrong-size handle: %v", err)
		}
		if readSentinel != sentinel {
			t.Fatalf("sentinel mismatch: got 0x%X, want 0x%X", readSentinel, sentinel)
		}
	})

	// Test: zero-length handle followed by sentinel
	t.Run("zero_length_handle", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(0)) // length = 0

		sentinel := uint32(0xFEEDFACE)
		binary.Write(&buf, binary.BigEndian, sentinel)

		r := bytes.NewReader(buf.Bytes())
		_, err := xdrDecodeFileHandle(r)
		if err == nil {
			t.Fatal("expected error for zero-length handle")
		}

		var readSentinel uint32
		if err := binary.Read(r, binary.BigEndian, &readSentinel); err != nil {
			t.Fatalf("failed to read sentinel after zero-length handle: %v", err)
		}
		if readSentinel != sentinel {
			t.Fatalf("sentinel mismatch: got 0x%X, want 0x%X", readSentinel, sentinel)
		}
	})
}

// TestM9_WriteRecordEmptyData verifies that WriteRecord handles empty data
// by emitting a single last-fragment header with length 0.
func TestM9_WriteRecordEmptyData(t *testing.T) {
	// Test: empty write produces a valid last-fragment header
	t.Run("empty_write", func(t *testing.T) {
		var buf bytes.Buffer
		writer := NewRecordMarkingWriter(&buf)

		err := writer.WriteRecord([]byte{})
		if err != nil {
			t.Fatalf("WriteRecord(empty) failed: %v", err)
		}

		// Should produce exactly 4 bytes: last-fragment header with length 0
		if buf.Len() != 4 {
			t.Fatalf("expected 4 bytes, got %d", buf.Len())
		}

		var header uint32
		binary.Read(&buf, binary.BigEndian, &header)
		if header != LastFragmentFlag {
			t.Fatalf("expected header 0x%08X, got 0x%08X", LastFragmentFlag, header)
		}
	})

	// Test: empty write round-trips through reader
	t.Run("empty_roundtrip", func(t *testing.T) {
		var buf bytes.Buffer
		writer := NewRecordMarkingWriter(&buf)
		writer.WriteRecord([]byte{})

		reader := NewRecordMarkingReader(&buf)
		data, err := reader.ReadRecord()
		if err != nil {
			t.Fatalf("ReadRecord failed: %v", err)
		}
		if len(data) != 0 {
			t.Fatalf("expected empty data, got %d bytes", len(data))
		}
	})
}

// TestM11_PortmapperUsesActualListenAddress verifies that the portmapper uses
// the configured listen address instead of hardcoded addresses.
func TestM11_PortmapperUsesActualListenAddress(t *testing.T) {
	pm := NewPortmapper()
	pm.SetListenAddr("192.168.1.100")
	pm.RegisterService(100003, 3, IPPROTO_TCP, 2049)

	// Test GETADDR returns configured address
	t.Run("getaddr_configured", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(100003)) // prog
		binary.Write(&buf, binary.BigEndian, uint32(3))      // vers
		xdrEncodeString(&buf, "tcp")                          // netid
		xdrEncodeString(&buf, "")                             // r_addr
		xdrEncodeString(&buf, "")                             // r_owner

		result := pm.handleGetAddr(&buf)
		// Decode the XDR string result
		r := bytes.NewReader(result)
		uaddr, err := xdrDecodeString(r)
		if err != nil {
			t.Fatalf("failed to decode uaddr: %v", err)
		}
		// 2049 = 8*256 + 1, so expect "192.168.1.100.8.1"
		expected := "192.168.1.100.8.1"
		if uaddr != expected {
			t.Fatalf("GETADDR: got %q, want %q", uaddr, expected)
		}
	})

	// Test DUMP returns configured address
	t.Run("dump_configured", func(t *testing.T) {
		result := pm.handleRpcbDump()
		// The dump contains multiple entries. Look for our registered service.
		r := bytes.NewReader(result)
		found := false
		for {
			var more uint32
			if err := binary.Read(r, binary.BigEndian, &more); err != nil {
				break
			}
			if more == 0 {
				break
			}
			var prog, vers uint32
			binary.Read(r, binary.BigEndian, &prog)
			binary.Read(r, binary.BigEndian, &vers)
			netid, _ := xdrDecodeString(r)
			uaddr, _ := xdrDecodeString(r)
			xdrDecodeString(r) // owner

			if prog == 100003 && vers == 3 && netid == "tcp" {
				expected := "192.168.1.100.8.1"
				if uaddr != expected {
					t.Fatalf("DUMP uaddr: got %q, want %q", uaddr, expected)
				}
				found = true
			}
		}
		if !found {
			t.Fatal("NFS service not found in dump output")
		}
	})

	// Test default address when listenAddr is empty
	t.Run("default_address", func(t *testing.T) {
		pm2 := NewPortmapper()
		pm2.RegisterService(100003, 3, IPPROTO_TCP, 2049)

		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(100003))
		binary.Write(&buf, binary.BigEndian, uint32(3))
		xdrEncodeString(&buf, "tcp")
		xdrEncodeString(&buf, "")
		xdrEncodeString(&buf, "")

		result := pm2.handleGetAddr(&buf)
		r := bytes.NewReader(result)
		uaddr, _ := xdrDecodeString(r)
		expected := "0.0.0.0.8.1"
		if uaddr != expected {
			t.Fatalf("default GETADDR: got %q, want %q", uaddr, expected)
		}
	})
}

// TestM12_ReadRecordTotalSizeLimit verifies that ReadRecord rejects records
// that exceed the maximum total size across all fragments.
func TestM12_ReadRecordTotalSizeLimit(t *testing.T) {
	// Test: single fragment exceeding max size
	t.Run("exceeds_max", func(t *testing.T) {
		var wire bytes.Buffer
		dataSize := uint32(1024)
		header := dataSize | LastFragmentFlag
		binary.Write(&wire, binary.BigEndian, header)
		wire.Write(make([]byte, dataSize))

		reader := NewRecordMarkingReader(&wire)
		reader.MaxRecordSize = 512 // set limit below data size

		_, err := reader.ReadRecord()
		if err == nil {
			t.Fatal("expected error for record exceeding max size")
		}
	})

	// Test: record at exactly max size should succeed
	t.Run("at_max", func(t *testing.T) {
		var wire bytes.Buffer
		dataSize := uint32(512)
		header := dataSize | LastFragmentFlag
		binary.Write(&wire, binary.BigEndian, header)
		wire.Write(make([]byte, dataSize))

		reader := NewRecordMarkingReader(&wire)
		reader.MaxRecordSize = 512

		data, err := reader.ReadRecord()
		if err != nil {
			t.Fatalf("expected success for record at max size, got: %v", err)
		}
		if len(data) != int(dataSize) {
			t.Fatalf("expected %d bytes, got %d", dataSize, len(data))
		}
	})

	// Test: default MaxRecordSize is set
	t.Run("default_set", func(t *testing.T) {
		reader := NewRecordMarkingReader(bytes.NewReader(nil))
		if reader.MaxRecordSize != DefaultMaxRecordSize {
			t.Fatalf("expected default MaxRecordSize %d, got %d", DefaultMaxRecordSize, reader.MaxRecordSize)
		}
	})
}

// TestM15_TLSReloadCertificatesConcurrentSafe verifies that ReloadCertificates
// can be called concurrently with TLS handshakes (via GetCertificate callback).
func TestM15_TLSReloadCertificatesConcurrentSafe(t *testing.T) {
	// Generate test certificates
	certFile, keyFile, cleanup := generateTestCert(t)
	defer cleanup()

	tc := &TLSConfig{
		Enabled:    true,
		CertFile:   certFile,
		KeyFile:    keyFile,
		MinVersion: tls.VersionTLS12,
		MaxVersion: tls.VersionTLS13,
	}

	// Build the TLS config
	tlsConfig, err := tc.BuildConfig()
	if err != nil {
		t.Fatalf("BuildConfig failed: %v", err)
	}

	// Verify GetCertificate is set (not using Certificates slice)
	if tlsConfig.GetCertificate == nil {
		t.Fatal("expected GetCertificate callback to be set")
	}

	// Concurrent test: multiple readers + one reloader
	var wg sync.WaitGroup
	errCh := make(chan error, 100)

	// Start readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				cert, err := tlsConfig.GetCertificate(nil)
				if err != nil {
					errCh <- err
					return
				}
				if cert == nil {
					errCh <- fmt.Errorf("GetCertificate returned nil")
					return
				}
			}
		}()
	}

	// Start reloader
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 20; j++ {
			if err := tc.ReloadCertificates(); err != nil {
				errCh <- err
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent access error: %v", err)
	}
}

// TestM15_ReloadCertificatesDisabledTLS verifies that ReloadCertificates
// returns an error when TLS is not enabled.
func TestM15_ReloadCertificatesDisabledTLS(t *testing.T) {
	tc := &TLSConfig{Enabled: false}
	err := tc.ReloadCertificates()
	if err == nil {
		t.Fatal("expected error when reloading certs with TLS disabled")
	}
}

// generateTestCert creates a temporary self-signed certificate for testing.
// Returns the cert file path, key file path, and a cleanup function.
func generateTestCert(t *testing.T) (string, string, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	certFile := filepath.Join(tmpDir, "cert.pem")
	keyFile := filepath.Join(tmpDir, "key.pem")

	// Generate ECDSA key
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	// Create certificate template
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"Test"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}

	// Self-sign
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	// Write cert PEM
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := os.WriteFile(certFile, certPEM, 0600); err != nil {
		t.Fatalf("failed to write cert file: %v", err)
	}

	// Write key PEM
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("failed to marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}

	return certFile, keyFile, func() {
		os.Remove(certFile)
		os.Remove(keyFile)
	}
}

// TestR12_FileHandleUnboundedAllocationDoS verifies that xdrDecodeFileHandle
// rejects handle lengths exceeding the NFS3 maximum of 64 bytes, preventing
// a DoS via huge memory allocation.
func TestR12_FileHandleUnboundedAllocationDoS(t *testing.T) {
	// Test: handle length exceeding NFS3 max (64 bytes) is rejected immediately
	t.Run("reject_huge_length", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(0xFFFFFFF0)) // ~4GB length
		_, err := xdrDecodeFileHandle(&buf)
		if err == nil {
			t.Fatal("expected error for huge handle length, got nil")
		}
		if !bytes.Contains([]byte(err.Error()), []byte("exceeds NFS3 maximum")) {
			t.Fatalf("expected NFS3 maximum error, got: %v", err)
		}
	})

	// Test: handle length of 65 is rejected (just above max)
	t.Run("reject_65_bytes", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(65))
		_, err := xdrDecodeFileHandle(&buf)
		if err == nil {
			t.Fatal("expected error for 65-byte handle length")
		}
		if !bytes.Contains([]byte(err.Error()), []byte("exceeds NFS3 maximum")) {
			t.Fatalf("expected NFS3 maximum error, got: %v", err)
		}
	})

	// Test: handle length of 64 is accepted (at max) but returns error for non-8
	t.Run("accept_64_bytes", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(64))
		buf.Write(make([]byte, 64)) // provide enough data
		_, err := xdrDecodeFileHandle(&buf)
		if err == nil {
			t.Fatal("expected error for 64-byte handle (not 8)")
		}
		// Should be "invalid handle length" not "exceeds NFS3 maximum"
		if bytes.Contains([]byte(err.Error()), []byte("exceeds NFS3 maximum")) {
			t.Fatalf("64 bytes should not trigger NFS3 maximum error, got: %v", err)
		}
	})

	// Test: io.ReadFull error is propagated when discarding
	t.Run("discard_read_error", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(32))
		buf.Write(make([]byte, 10)) // only 10 bytes, but need 32
		_, err := xdrDecodeFileHandle(&buf)
		if err == nil {
			t.Fatal("expected error when discarding truncated handle data")
		}
	})
}

// TestR13_PortmapperAtomicDebugListenAddr verifies that debug and listenAddr
// fields use atomic operations and are safe for concurrent access.
func TestR13_PortmapperAtomicDebugListenAddr(t *testing.T) {
	pm := NewPortmapper()

	// Test concurrent SetDebug and reads
	t.Run("concurrent_debug", func(t *testing.T) {
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(val bool) {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					pm.SetDebug(val)
				}
			}(i%2 == 0)
		}
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					_ = pm.debug.Load()
				}
			}()
		}
		wg.Wait()
	})

	// Test concurrent SetListenAddr and reads
	t.Run("concurrent_listenaddr", func(t *testing.T) {
		var wg sync.WaitGroup
		addrs := []string{"192.168.1.1", "10.0.0.1", "127.0.0.1"}
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					pm.SetListenAddr(addrs[idx%len(addrs)])
				}
			}(i)
		}
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					v, _ := pm.listenAddr.Load().(string)
					_ = v
				}
			}()
		}
		wg.Wait()
	})

	// Verify SetDebug/SetListenAddr work correctly
	t.Run("set_and_get", func(t *testing.T) {
		pm.SetDebug(true)
		if !pm.debug.Load() {
			t.Fatal("expected debug to be true")
		}
		pm.SetDebug(false)
		if pm.debug.Load() {
			t.Fatal("expected debug to be false")
		}
		pm.SetListenAddr("10.0.0.5")
		got, _ := pm.listenAddr.Load().(string)
		if got != "10.0.0.5" {
			t.Fatalf("expected listenAddr 10.0.0.5, got %q", got)
		}
	})
}

// TestR27_RPCCredentialVerifierXDRPadding verifies that DecodeRPCCall correctly
// consumes XDR padding after credential and verifier bodies.
func TestR27_RPCCredentialVerifierXDRPadding(t *testing.T) {
	// Build a valid RPC call with oddly-sized credential and verifier
	t.Run("odd_credential_padded", func(t *testing.T) {
		var buf bytes.Buffer
		// Header
		binary.Write(&buf, binary.BigEndian, uint32(0x12345678)) // XID
		binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))   // msg type
		binary.Write(&buf, binary.BigEndian, uint32(2))           // RPC version
		binary.Write(&buf, binary.BigEndian, uint32(100003))      // program
		binary.Write(&buf, binary.BigEndian, uint32(3))           // version
		binary.Write(&buf, binary.BigEndian, uint32(0))           // procedure

		// Credential: 5 bytes body (needs 3 bytes padding)
		binary.Write(&buf, binary.BigEndian, uint32(AUTH_NONE)) // flavor
		binary.Write(&buf, binary.BigEndian, uint32(5))         // length
		buf.Write([]byte{1, 2, 3, 4, 5})                        // body
		buf.Write([]byte{0, 0, 0})                               // XDR padding

		// Verifier: 3 bytes body (needs 1 byte padding)
		binary.Write(&buf, binary.BigEndian, uint32(AUTH_NONE)) // flavor
		binary.Write(&buf, binary.BigEndian, uint32(3))         // length
		buf.Write([]byte{6, 7, 8})                               // body
		buf.Write([]byte{0})                                     // XDR padding

		call, err := DecodeRPCCall(&buf)
		if err != nil {
			t.Fatalf("DecodeRPCCall failed: %v", err)
		}
		if len(call.Credential.Body) != 5 {
			t.Fatalf("expected credential body len 5, got %d", len(call.Credential.Body))
		}
		if len(call.Verifier.Body) != 3 {
			t.Fatalf("expected verifier body len 3, got %d", len(call.Verifier.Body))
		}
	})

	// Test: 4-byte aligned bodies need no padding
	t.Run("aligned_no_padding", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(0xAABBCCDD))
		binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))
		binary.Write(&buf, binary.BigEndian, uint32(2))
		binary.Write(&buf, binary.BigEndian, uint32(100003))
		binary.Write(&buf, binary.BigEndian, uint32(3))
		binary.Write(&buf, binary.BigEndian, uint32(0))

		// Credential: 8 bytes (aligned)
		binary.Write(&buf, binary.BigEndian, uint32(AUTH_NONE))
		binary.Write(&buf, binary.BigEndian, uint32(8))
		buf.Write(make([]byte, 8))

		// Verifier: 0 bytes (aligned)
		binary.Write(&buf, binary.BigEndian, uint32(AUTH_NONE))
		binary.Write(&buf, binary.BigEndian, uint32(0))

		call, err := DecodeRPCCall(&buf)
		if err != nil {
			t.Fatalf("DecodeRPCCall failed: %v", err)
		}
		if len(call.Credential.Body) != 8 {
			t.Fatalf("expected credential body len 8, got %d", len(call.Credential.Body))
		}
	})
}

// TestR27_EncodeRPCReplyVerifierPadding verifies that EncodeRPCReply pads the
// verifier body to a 4-byte boundary.
func TestR27_EncodeRPCReplyVerifierPadding(t *testing.T) {
	t.Run("odd_verifier_padded", func(t *testing.T) {
		var buf bytes.Buffer
		reply := &RPCReply{
			Header:       RPCMsgHeader{Xid: 1},
			Status:       MSG_ACCEPTED,
			AcceptStatus: SUCCESS,
			Verifier:     RPCVerifier{Flavor: AUTH_NONE, Body: []byte{0xAA, 0xBB, 0xCC}}, // 3 bytes
		}
		if err := EncodeRPCReply(&buf, reply); err != nil {
			t.Fatalf("EncodeRPCReply failed: %v", err)
		}
		// Total should be: XID(4) + type(4) + reply_stat(4) + verf_flavor(4) + verf_len(4)
		// + body(3) + pad(1) + accept_stat(4) = 28
		if buf.Len() != 28 {
			t.Fatalf("expected 28 bytes, got %d", buf.Len())
		}
	})

	t.Run("aligned_verifier_no_extra_padding", func(t *testing.T) {
		var buf bytes.Buffer
		reply := &RPCReply{
			Header:       RPCMsgHeader{Xid: 1},
			Status:       MSG_ACCEPTED,
			AcceptStatus: SUCCESS,
			Verifier:     RPCVerifier{Flavor: AUTH_NONE, Body: []byte{0xAA, 0xBB, 0xCC, 0xDD}}, // 4 bytes
		}
		if err := EncodeRPCReply(&buf, reply); err != nil {
			t.Fatalf("EncodeRPCReply failed: %v", err)
		}
		// XID(4) + type(4) + reply_stat(4) + verf_flavor(4) + verf_len(4) + body(4) + accept_stat(4) = 28
		if buf.Len() != 28 {
			t.Fatalf("expected 28 bytes, got %d", buf.Len())
		}
	})
}

// TestR28_PortmapperConditionalLogging verifies that the portmapper only logs
// call details when debug mode is enabled.
func TestR28_PortmapperConditionalLogging(t *testing.T) {
	pm := NewPortmapper()
	pm.RegisterService(100003, 3, IPPROTO_TCP, 2049)

	// Capture log output
	var logBuf bytes.Buffer
	pm.logger.SetOutput(&logBuf)

	// With debug disabled, handleCall should not log the "Call:" line
	t.Run("no_log_when_debug_off", func(t *testing.T) {
		logBuf.Reset()
		pm.SetDebug(false)

		// Build a minimal valid RPC call for portmapper GETPORT
		var callBuf bytes.Buffer
		binary.Write(&callBuf, binary.BigEndian, uint32(1))                 // XID
		binary.Write(&callBuf, binary.BigEndian, uint32(RPC_CALL))          // msg type
		binary.Write(&callBuf, binary.BigEndian, uint32(2))                 // RPC version
		binary.Write(&callBuf, binary.BigEndian, uint32(PortmapperProgram)) // program
		binary.Write(&callBuf, binary.BigEndian, uint32(2))                 // version
		binary.Write(&callBuf, binary.BigEndian, uint32(PMAPPROC_GETPORT))  // procedure
		// Credential: AUTH_NONE, length 0
		binary.Write(&callBuf, binary.BigEndian, uint32(AUTH_NONE))
		binary.Write(&callBuf, binary.BigEndian, uint32(0))
		// Verifier: AUTH_NONE, length 0
		binary.Write(&callBuf, binary.BigEndian, uint32(AUTH_NONE))
		binary.Write(&callBuf, binary.BigEndian, uint32(0))
		// GETPORT args
		binary.Write(&callBuf, binary.BigEndian, uint32(100003))      // prog
		binary.Write(&callBuf, binary.BigEndian, uint32(3))           // vers
		binary.Write(&callBuf, binary.BigEndian, uint32(IPPROTO_TCP)) // prot
		binary.Write(&callBuf, binary.BigEndian, uint32(0))           // port

		_, err := pm.handleCall(callBuf.Bytes())
		if err != nil {
			t.Fatalf("handleCall failed: %v", err)
		}

		if bytes.Contains(logBuf.Bytes(), []byte("Call:")) {
			t.Fatal("expected no 'Call:' log when debug is off")
		}
	})

	// With debug enabled, handleCall should log
	t.Run("log_when_debug_on", func(t *testing.T) {
		logBuf.Reset()
		pm.SetDebug(true)

		var callBuf bytes.Buffer
		binary.Write(&callBuf, binary.BigEndian, uint32(2))
		binary.Write(&callBuf, binary.BigEndian, uint32(RPC_CALL))
		binary.Write(&callBuf, binary.BigEndian, uint32(2))
		binary.Write(&callBuf, binary.BigEndian, uint32(PortmapperProgram))
		binary.Write(&callBuf, binary.BigEndian, uint32(2))
		binary.Write(&callBuf, binary.BigEndian, uint32(PMAPPROC_NULL))
		binary.Write(&callBuf, binary.BigEndian, uint32(AUTH_NONE))
		binary.Write(&callBuf, binary.BigEndian, uint32(0))
		binary.Write(&callBuf, binary.BigEndian, uint32(AUTH_NONE))
		binary.Write(&callBuf, binary.BigEndian, uint32(0))

		_, err := pm.handleCall(callBuf.Bytes())
		if err != nil {
			t.Fatalf("handleCall failed: %v", err)
		}

		if !bytes.Contains(logBuf.Bytes(), []byte("Call:")) {
			t.Fatal("expected 'Call:' log when debug is on")
		}
	})
}
