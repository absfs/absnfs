package absnfs

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestPortmapperBasicOperations(t *testing.T) {
	pm := NewPortmapper()

	t.Run("register and get port", func(t *testing.T) {
		pm.RegisterService(NFS_PROGRAM, NFS_V3, IPPROTO_TCP, 2049)

		port := pm.GetPort(NFS_PROGRAM, NFS_V3, IPPROTO_TCP)
		if port != 2049 {
			t.Errorf("Expected port 2049, got %d", port)
		}
	})

	t.Run("get non-existent service", func(t *testing.T) {
		port := pm.GetPort(999999, 1, IPPROTO_TCP)
		if port != 0 {
			t.Errorf("Expected port 0 for non-existent service, got %d", port)
		}
	})

	t.Run("unregister service", func(t *testing.T) {
		pm.RegisterService(MOUNT_PROGRAM, MOUNT_V3, IPPROTO_TCP, 635)
		pm.UnregisterService(MOUNT_PROGRAM, MOUNT_V3, IPPROTO_TCP)

		port := pm.GetPort(MOUNT_PROGRAM, MOUNT_V3, IPPROTO_TCP)
		if port != 0 {
			t.Errorf("Expected port 0 after unregister, got %d", port)
		}
	})

	t.Run("update existing service", func(t *testing.T) {
		pm.RegisterService(NFS_PROGRAM, NFS_V3, IPPROTO_TCP, 2049)
		pm.RegisterService(NFS_PROGRAM, NFS_V3, IPPROTO_TCP, 3049)

		port := pm.GetPort(NFS_PROGRAM, NFS_V3, IPPROTO_TCP)
		if port != 3049 {
			t.Errorf("Expected port 3049 after update, got %d", port)
		}
	})

	t.Run("get mappings", func(t *testing.T) {
		pm2 := NewPortmapper()
		pm2.RegisterService(NFS_PROGRAM, NFS_V3, IPPROTO_TCP, 2049)
		pm2.RegisterService(MOUNT_PROGRAM, MOUNT_V3, IPPROTO_TCP, 635)

		mappings := pm2.GetMappings()
		if len(mappings) != 2 {
			t.Errorf("Expected 2 mappings, got %d", len(mappings))
		}
	})
}

func TestPortmapperServerConnection(t *testing.T) {
	pm := NewPortmapper()
	pm.SetDebug(true)

	// Start on a high port (no root needed)
	testPort := 10111
	if err := pm.StartOnPort(testPort); err != nil {
		t.Fatalf("Failed to start portmapper: %v", err)
	}
	defer pm.Stop()

	// Register some services
	pm.RegisterService(NFS_PROGRAM, NFS_V3, IPPROTO_TCP, 2049)
	pm.RegisterService(MOUNT_PROGRAM, MOUNT_V3, IPPROTO_TCP, 2049)

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	t.Run("PMAPPROC_NULL", func(t *testing.T) {
		resp := sendPortmapperCall(t, testPort, PMAPPROC_NULL, 2, nil)
		if resp == nil {
			t.Fatal("No response received")
		}
		// NULL procedure should return empty response (just header)
	})

	t.Run("PMAPPROC_GETPORT for NFS", func(t *testing.T) {
		// Build GETPORT request
		var args bytes.Buffer
		binary.Write(&args, binary.BigEndian, uint32(NFS_PROGRAM))
		binary.Write(&args, binary.BigEndian, uint32(NFS_V3))
		binary.Write(&args, binary.BigEndian, uint32(IPPROTO_TCP))
		binary.Write(&args, binary.BigEndian, uint32(0)) // ignored port

		resp := sendPortmapperCall(t, testPort, PMAPPROC_GETPORT, 2, args.Bytes())
		if resp == nil {
			t.Fatal("No response received")
		}

		// Parse response to get port
		r := bytes.NewReader(resp)
		// Skip header: XID(4) + MsgType(4) + Status(4) + VerFlavor(4) + VerLen(4) + AcceptStatus(4)
		r.Seek(24, 0)
		var port uint32
		if err := binary.Read(r, binary.BigEndian, &port); err != nil {
			t.Fatalf("Failed to read port from response: %v", err)
		}

		if port != 2049 {
			t.Errorf("Expected port 2049, got %d", port)
		}
	})

	t.Run("PMAPPROC_DUMP", func(t *testing.T) {
		resp := sendPortmapperCall(t, testPort, PMAPPROC_DUMP, 2, nil)
		if resp == nil {
			t.Fatal("No response received")
		}
		// Just verify we get a response - detailed parsing is complex
		if len(resp) < 28 {
			t.Errorf("Response too short for DUMP: %d bytes", len(resp))
		}
	})

	t.Run("RPCBPROC_GETADDR for NFS", func(t *testing.T) {
		// Build GETADDR request (rpcbind v3/v4 format)
		var args bytes.Buffer
		binary.Write(&args, binary.BigEndian, uint32(NFS_PROGRAM))
		binary.Write(&args, binary.BigEndian, uint32(NFS_V3))
		xdrEncodeString(&args, "tcp") // netid
		xdrEncodeString(&args, "")    // r_addr (ignored)
		xdrEncodeString(&args, "")    // r_owner

		resp := sendPortmapperCall(t, testPort, 3, 3, args.Bytes()) // proc 3 = GETADDR, version 3
		if resp == nil {
			t.Fatal("No response received")
		}

		// Parse response to get universal address
		r := bytes.NewReader(resp)
		// Skip header
		r.Seek(24, 0)
		uaddr, err := xdrDecodeString(r)
		if err != nil {
			t.Fatalf("Failed to read uaddr from response: %v", err)
		}

		// Should contain port 2049 encoded as high.low (8.1)
		// 2049 = 8*256 + 1
		expected := "0.0.0.0.8.1"
		if uaddr != expected {
			t.Errorf("Expected uaddr %s, got %s", expected, uaddr)
		}
	})
}

// sendPortmapperCall sends an RPC call to the portmapper and returns the response
func sendPortmapperCall(t *testing.T, port int, procedure uint32, version uint32, args []byte) []byte {
	conn, err := net.DialTimeout("tcp", "localhost:"+itoa(port), 2*time.Second)
	if err != nil {
		t.Fatalf("Failed to connect to portmapper: %v", err)
	}
	defer conn.Close()

	// Build RPC call
	var call bytes.Buffer
	binary.Write(&call, binary.BigEndian, uint32(1))                 // XID
	binary.Write(&call, binary.BigEndian, uint32(RPC_CALL))          // Message type
	binary.Write(&call, binary.BigEndian, uint32(2))                 // RPC version
	binary.Write(&call, binary.BigEndian, uint32(PortmapperProgram)) // Program
	binary.Write(&call, binary.BigEndian, version)                   // Version
	binary.Write(&call, binary.BigEndian, procedure)                 // Procedure
	binary.Write(&call, binary.BigEndian, uint32(0))                 // Auth flavor
	binary.Write(&call, binary.BigEndian, uint32(0))                 // Auth length
	binary.Write(&call, binary.BigEndian, uint32(0))                 // Verifier flavor
	binary.Write(&call, binary.BigEndian, uint32(0))                 // Verifier length
	if args != nil {
		call.Write(args)
	}

	// Send with record marking
	rmConn := NewRecordMarkingConn(conn, conn)
	if err := rmConn.WriteRecord(call.Bytes()); err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}

	// Read response
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp, err := rmConn.ReadRecord()
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	return resp
}

// itoa converts int to string without importing strconv
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	if neg {
		s = "-" + s
	}
	return s
}

// fakeAddr implements net.Addr for testing with a specific address string.
type fakeAddr struct {
	addr string
}

func (a *fakeAddr) Network() string { return "tcp" }
func (a *fakeAddr) String() string  { return a.addr }

// TestR3_PortmapperSetUnsetAuth verifies that handleSet and handleUnset
// reject requests from non-loopback addresses.
func TestR3_PortmapperSetUnsetAuth(t *testing.T) {
	pm := NewPortmapper()
	defer pm.Stop()

	// Build a valid SET/UNSET request body: prog(4) + vers(4) + prot(4) + port(4)
	var requestBody bytes.Buffer
	binary.Write(&requestBody, binary.BigEndian, uint32(100005)) // prog
	binary.Write(&requestBody, binary.BigEndian, uint32(3))      // vers
	binary.Write(&requestBody, binary.BigEndian, uint32(6))      // prot (TCP)
	binary.Write(&requestBody, binary.BigEndian, uint32(2049))   // port

	t.Run("SET from non-loopback rejected", func(t *testing.T) {
		remoteAddr := &fakeAddr{addr: "192.168.1.100:5000"}
		result := pm.handleSet(bytes.NewReader(requestBody.Bytes()), remoteAddr)
		// Should return false (4 bytes: 0x00000000)
		if len(result) != 4 {
			t.Fatalf("Expected 4 bytes, got %d", len(result))
		}
		val := binary.BigEndian.Uint32(result)
		if val != 0 {
			t.Errorf("Expected false (0) for non-loopback SET, got %d", val)
		}
	})

	t.Run("UNSET from non-loopback rejected", func(t *testing.T) {
		remoteAddr := &fakeAddr{addr: "10.0.0.1:5000"}
		result := pm.handleUnset(bytes.NewReader(requestBody.Bytes()), remoteAddr)
		if len(result) != 4 {
			t.Fatalf("Expected 4 bytes, got %d", len(result))
		}
		val := binary.BigEndian.Uint32(result)
		if val != 0 {
			t.Errorf("Expected false (0) for non-loopback UNSET, got %d", val)
		}
	})

	t.Run("SET from loopback accepted", func(t *testing.T) {
		remoteAddr := &fakeAddr{addr: "127.0.0.1:5000"}
		result := pm.handleSet(bytes.NewReader(requestBody.Bytes()), remoteAddr)
		if len(result) != 4 {
			t.Fatalf("Expected 4 bytes, got %d", len(result))
		}
		val := binary.BigEndian.Uint32(result)
		if val != 1 {
			t.Errorf("Expected true (1) for loopback SET, got %d", val)
		}
	})

	t.Run("UNSET from loopback accepted", func(t *testing.T) {
		// First register the service so UNSET has something to remove
		pm.RegisterService(100005, 3, 6, 2049)

		remoteAddr := &fakeAddr{addr: "127.0.0.1:5000"}
		result := pm.handleUnset(bytes.NewReader(requestBody.Bytes()), remoteAddr)
		if len(result) != 4 {
			t.Fatalf("Expected 4 bytes, got %d", len(result))
		}
		val := binary.BigEndian.Uint32(result)
		if val != 1 {
			t.Errorf("Expected true (1) for loopback UNSET, got %d", val)
		}
	})
}

// TestR3_PortmapperConnectionLimit verifies that the portmapper's connSem
// limits concurrent connections correctly.
func TestR3_PortmapperConnectionLimit(t *testing.T) {
	pm := NewPortmapper()

	// Verify default capacity
	if cap(pm.connSem) != DefaultMaxPortmapperConns {
		t.Errorf("Expected connSem capacity %d, got %d", DefaultMaxPortmapperConns, cap(pm.connSem))
	}

	// Create a portmapper with a very small connection limit for testing
	smallPm := &Portmapper{
		connSem: make(chan struct{}, 2),
	}

	// Fill up the connection slots
	smallPm.connSem <- struct{}{}
	smallPm.connSem <- struct{}{}

	// The third connection should be rejected (non-blocking)
	select {
	case smallPm.connSem <- struct{}{}:
		t.Error("Should not have accepted a third connection when at capacity")
		// Drain to clean up
		<-smallPm.connSem
	default:
		// Expected: at capacity, connection rejected
	}

	// Release one slot
	<-smallPm.connSem

	// Now one more should fit
	select {
	case smallPm.connSem <- struct{}{}:
		// Expected: slot available
		<-smallPm.connSem
	default:
		t.Error("Should have accepted connection after releasing a slot")
	}

	// Clean up
	<-smallPm.connSem
}

// --- Tests from r3_coverage_boost_test.go ---

func TestCovBoost_PortmapperHandleSet_Loopback(t *testing.T) {
	pm := NewPortmapper()
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}
	var args bytes.Buffer
	binary.Write(&args, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&args, binary.BigEndian, uint32(NFS_V3))
	binary.Write(&args, binary.BigEndian, uint32(IPPROTO_TCP))
	binary.Write(&args, binary.BigEndian, uint32(2049))
	result := pm.handleSet(bytes.NewReader(args.Bytes()), addr)
	if len(result) != 4 || binary.BigEndian.Uint32(result) != 1 {
		t.Error("expected true from handleSet with loopback addr")
	}
	if port := pm.GetPort(NFS_PROGRAM, NFS_V3, IPPROTO_TCP); port != 2049 {
		t.Errorf("expected port 2049, got %d", port)
	}
}

func TestCovBoost_PortmapperHandleSet_NonLoopback(t *testing.T) {
	pm := NewPortmapper()
	addr := &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1234}
	var args bytes.Buffer
	binary.Write(&args, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&args, binary.BigEndian, uint32(NFS_V3))
	binary.Write(&args, binary.BigEndian, uint32(IPPROTO_TCP))
	binary.Write(&args, binary.BigEndian, uint32(2049))
	result := pm.handleSet(bytes.NewReader(args.Bytes()), addr)
	if len(result) != 4 || binary.BigEndian.Uint32(result) != 0 {
		t.Error("expected false from handleSet with non-loopback addr")
	}
}

func TestCovBoost_PortmapperHandleSet_NilAddr(t *testing.T) {
	pm := NewPortmapper()
	var args bytes.Buffer
	binary.Write(&args, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&args, binary.BigEndian, uint32(NFS_V3))
	binary.Write(&args, binary.BigEndian, uint32(IPPROTO_TCP))
	binary.Write(&args, binary.BigEndian, uint32(2049))
	result := pm.handleSet(bytes.NewReader(args.Bytes()), nil)
	if len(result) != 4 || binary.BigEndian.Uint32(result) != 1 {
		t.Error("expected true from handleSet with nil addr")
	}
}

func TestCovBoost_PortmapperHandleUnset_Loopback(t *testing.T) {
	pm := NewPortmapper()
	pm.RegisterService(NFS_PROGRAM, NFS_V3, IPPROTO_TCP, 2049)
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}
	var args bytes.Buffer
	binary.Write(&args, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&args, binary.BigEndian, uint32(NFS_V3))
	binary.Write(&args, binary.BigEndian, uint32(IPPROTO_TCP))
	binary.Write(&args, binary.BigEndian, uint32(2049))
	result := pm.handleUnset(bytes.NewReader(args.Bytes()), addr)
	if len(result) != 4 || binary.BigEndian.Uint32(result) != 1 {
		t.Error("expected true from handleUnset with loopback addr")
	}
}

func TestCovBoost_PortmapperHandleUnset_NonLoopback(t *testing.T) {
	pm := NewPortmapper()
	addr := &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1234}
	var args bytes.Buffer
	binary.Write(&args, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&args, binary.BigEndian, uint32(NFS_V3))
	binary.Write(&args, binary.BigEndian, uint32(IPPROTO_TCP))
	binary.Write(&args, binary.BigEndian, uint32(2049))
	result := pm.handleUnset(bytes.NewReader(args.Bytes()), addr)
	if len(result) != 4 || binary.BigEndian.Uint32(result) != 0 {
		t.Error("expected false from handleUnset with non-loopback addr")
	}
}

func TestCovBoost_PortmapperHandleSet_TruncatedArgs(t *testing.T) {
	pm := NewPortmapper()
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}
	var args bytes.Buffer
	binary.Write(&args, binary.BigEndian, uint32(NFS_PROGRAM))
	result := pm.handleSet(bytes.NewReader(args.Bytes()), addr)
	if binary.BigEndian.Uint32(result) != 0 {
		t.Error("expected false for truncated args")
	}
}

func TestCovBoost_PortmapperHandleUnset_TruncatedArgs(t *testing.T) {
	pm := NewPortmapper()
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}
	var args bytes.Buffer
	binary.Write(&args, binary.BigEndian, uint32(NFS_PROGRAM))
	result := pm.handleUnset(bytes.NewReader(args.Bytes()), addr)
	if binary.BigEndian.Uint32(result) != 0 {
		t.Error("expected false for truncated args")
	}
}

func TestCovBoost_PortmapperSkipAuth_WithBody(t *testing.T) {
	pm := NewPortmapper()
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1))
	binary.Write(&buf, binary.BigEndian, uint32(5))
	buf.Write([]byte{1, 2, 3, 4, 5})
	buf.Write([]byte{0, 0, 0})
	if err := pm.skipAuth(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("skipAuth: %v", err)
	}
}

func TestCovBoost_PortmapperSkipAuth_EmptyBody(t *testing.T) {
	pm := NewPortmapper()
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	if err := pm.skipAuth(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("skipAuth: %v", err)
	}
}

func TestCovBoost_PortmapperSkipAuth_ExcessiveLength(t *testing.T) {
	pm := NewPortmapper()
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0xFFFFFFFF))
	if err := pm.skipAuth(bytes.NewReader(buf.Bytes())); err == nil {
		t.Error("expected error for excessive auth length")
	}
}

func TestCovBoost_PortmapperSkipAuth_4ByteAligned(t *testing.T) {
	pm := NewPortmapper()
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1))
	binary.Write(&buf, binary.BigEndian, uint32(4))
	buf.Write([]byte{1, 2, 3, 4})
	if err := pm.skipAuth(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("skipAuth: %v", err)
	}
}

func TestCovBoost_PortmapperSkipAuth_Truncated(t *testing.T) {
	pm := NewPortmapper()
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1))
	if err := pm.skipAuth(bytes.NewReader(buf.Bytes())); err == nil {
		t.Error("expected error for truncated auth")
	}
}

func TestCovBoost_PortmapperHandleCall_VersionMismatch(t *testing.T) {
	pm := NewPortmapper()
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1))
	binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))
	binary.Write(&buf, binary.BigEndian, uint32(2))
	binary.Write(&buf, binary.BigEndian, uint32(PortmapperProgram))
	binary.Write(&buf, binary.BigEndian, uint32(99))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	result, err := pm.handleCall(buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("handleCall: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCovBoost_PortmapperHandleCall_WrongProgram(t *testing.T) {
	pm := NewPortmapper()
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1))
	binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))
	binary.Write(&buf, binary.BigEndian, uint32(2))
	binary.Write(&buf, binary.BigEndian, uint32(999999))
	binary.Write(&buf, binary.BigEndian, uint32(2))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	result, err := pm.handleCall(buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("handleCall: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCovBoost_PortmapperHandleCall_UnknownProcV2(t *testing.T) {
	pm := NewPortmapper()
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1))
	binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))
	binary.Write(&buf, binary.BigEndian, uint32(2))
	binary.Write(&buf, binary.BigEndian, uint32(PortmapperProgram))
	binary.Write(&buf, binary.BigEndian, uint32(2))
	binary.Write(&buf, binary.BigEndian, uint32(99))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	result, err := pm.handleCall(buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("handleCall: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCovBoost_PortmapperHandleCall_UnknownProcV3(t *testing.T) {
	pm := NewPortmapper()
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1))
	binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))
	binary.Write(&buf, binary.BigEndian, uint32(2))
	binary.Write(&buf, binary.BigEndian, uint32(PortmapperProgram))
	binary.Write(&buf, binary.BigEndian, uint32(3))
	binary.Write(&buf, binary.BigEndian, uint32(99))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	result, err := pm.handleCall(buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("handleCall: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCovBoost_PortmapperHandleCall_NotRPCCall(t *testing.T) {
	pm := NewPortmapper()
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1))
	binary.Write(&buf, binary.BigEndian, uint32(1))
	_, err := pm.handleCall(buf.Bytes(), nil)
	if err == nil {
		t.Error("expected error for non-RPC-call message")
	}
}

func TestCovBoost_PortmapperHandleCall_SetViaWire(t *testing.T) {
	pm := NewPortmapper()
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(42))
	binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))
	binary.Write(&buf, binary.BigEndian, uint32(2))
	binary.Write(&buf, binary.BigEndian, uint32(PortmapperProgram))
	binary.Write(&buf, binary.BigEndian, uint32(2))
	binary.Write(&buf, binary.BigEndian, uint32(PMAPPROC_SET))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&buf, binary.BigEndian, uint32(NFS_V3))
	binary.Write(&buf, binary.BigEndian, uint32(IPPROTO_TCP))
	binary.Write(&buf, binary.BigEndian, uint32(2049))
	result, err := pm.handleCall(buf.Bytes(), addr)
	if err != nil {
		t.Fatalf("handleCall SET: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCovBoost_PortmapperHandleCall_UnsetViaWire(t *testing.T) {
	pm := NewPortmapper()
	pm.RegisterService(NFS_PROGRAM, NFS_V3, IPPROTO_TCP, 2049)
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(43))
	binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))
	binary.Write(&buf, binary.BigEndian, uint32(2))
	binary.Write(&buf, binary.BigEndian, uint32(PortmapperProgram))
	binary.Write(&buf, binary.BigEndian, uint32(2))
	binary.Write(&buf, binary.BigEndian, uint32(PMAPPROC_UNSET))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&buf, binary.BigEndian, uint32(NFS_V3))
	binary.Write(&buf, binary.BigEndian, uint32(IPPROTO_TCP))
	binary.Write(&buf, binary.BigEndian, uint32(2049))
	result, err := pm.handleCall(buf.Bytes(), addr)
	if err != nil {
		t.Fatalf("handleCall UNSET: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCovBoost_PortmapperHandleSet_TruncatedVers(t *testing.T) {
	pm := NewPortmapper()
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}
	var args bytes.Buffer
	binary.Write(&args, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&args, binary.BigEndian, uint32(NFS_V3))
	result := pm.handleSet(bytes.NewReader(args.Bytes()), addr)
	if binary.BigEndian.Uint32(result) != 0 {
		t.Error("expected false for truncated args missing prot")
	}
}

func TestCovBoost_PortmapperHandleUnset_TruncatedVers(t *testing.T) {
	pm := NewPortmapper()
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}
	var args bytes.Buffer
	binary.Write(&args, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&args, binary.BigEndian, uint32(NFS_V3))
	result := pm.handleUnset(bytes.NewReader(args.Bytes()), addr)
	if binary.BigEndian.Uint32(result) != 0 {
		t.Error("expected false for truncated args missing prot")
	}
}

func TestCovBoost_PortmapperHandleCall_RpcbSetV3(t *testing.T) {
	pm := NewPortmapper()
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(50))
	binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))
	binary.Write(&buf, binary.BigEndian, uint32(2))
	binary.Write(&buf, binary.BigEndian, uint32(PortmapperProgram))
	binary.Write(&buf, binary.BigEndian, uint32(3))
	binary.Write(&buf, binary.BigEndian, uint32(1))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&buf, binary.BigEndian, uint32(NFS_V3))
	xdrEncodeString(&buf, "tcp")
	xdrEncodeString(&buf, "0.0.0.0.8.1")
	xdrEncodeString(&buf, "")
	result, err := pm.handleCall(buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("handleCall RPCB_SET: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCovBoost_PortmapperHandleCall_RpcbUnsetV3(t *testing.T) {
	pm := NewPortmapper()
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(51))
	binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))
	binary.Write(&buf, binary.BigEndian, uint32(2))
	binary.Write(&buf, binary.BigEndian, uint32(PortmapperProgram))
	binary.Write(&buf, binary.BigEndian, uint32(3))
	binary.Write(&buf, binary.BigEndian, uint32(2))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&buf, binary.BigEndian, uint32(NFS_V3))
	xdrEncodeString(&buf, "tcp")
	xdrEncodeString(&buf, "")
	xdrEncodeString(&buf, "")
	result, err := pm.handleCall(buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("handleCall RPCB_UNSET: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCovBoost_PortmapperHandleCall_RpcbDumpV3(t *testing.T) {
	pm := NewPortmapper()
	pm.RegisterService(NFS_PROGRAM, NFS_V3, IPPROTO_TCP, 2049)
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(52))
	binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))
	binary.Write(&buf, binary.BigEndian, uint32(2))
	binary.Write(&buf, binary.BigEndian, uint32(PortmapperProgram))
	binary.Write(&buf, binary.BigEndian, uint32(3))
	binary.Write(&buf, binary.BigEndian, uint32(4))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	result, err := pm.handleCall(buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("handleCall RPCB_DUMP: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCovBoost_PortmapperHandleCall_NullV3(t *testing.T) {
	pm := NewPortmapper()
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(53))
	binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))
	binary.Write(&buf, binary.BigEndian, uint32(2))
	binary.Write(&buf, binary.BigEndian, uint32(PortmapperProgram))
	binary.Write(&buf, binary.BigEndian, uint32(3))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	result, err := pm.handleCall(buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("handleCall NULL v3: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCovBoost_PortmapperSkipAuth_OneByteBody(t *testing.T) {
	pm := NewPortmapper()
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1))
	binary.Write(&buf, binary.BigEndian, uint32(1))
	buf.Write([]byte{42})
	buf.Write([]byte{0, 0, 0})
	if err := pm.skipAuth(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("skipAuth: %v", err)
	}
}

func TestCovBoost_PortmapperSkipAuth_TwoByteBody(t *testing.T) {
	pm := NewPortmapper()
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1))
	binary.Write(&buf, binary.BigEndian, uint32(2))
	buf.Write([]byte{1, 2})
	buf.Write([]byte{0, 0})
	if err := pm.skipAuth(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("skipAuth: %v", err)
	}
}

func TestCovBoost_PortmapperSkipAuth_ThreeByteBody(t *testing.T) {
	pm := NewPortmapper()
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1))
	binary.Write(&buf, binary.BigEndian, uint32(3))
	buf.Write([]byte{1, 2, 3})
	buf.Write([]byte{0})
	if err := pm.skipAuth(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("skipAuth: %v", err)
	}
}

func TestCovBoost_PortmapperHandleCall_TruncatedHeader(t *testing.T) {
	pm := NewPortmapper()
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1))
	_, err := pm.handleCall(buf.Bytes(), nil)
	if err == nil {
		t.Error("expected error for truncated header")
	}
}

func TestCovBoost_PortmapperHandleCall_EmptyData(t *testing.T) {
	pm := NewPortmapper()
	_, err := pm.handleCall([]byte{}, nil)
	if err == nil {
		t.Error("expected error for empty data")
	}
}

func TestCovBoost_PortmapperHandleCall_V4(t *testing.T) {
	pm := NewPortmapper()
	pm.RegisterService(NFS_PROGRAM, NFS_V3, IPPROTO_TCP, 2049)
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(54))
	binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))
	binary.Write(&buf, binary.BigEndian, uint32(2))
	binary.Write(&buf, binary.BigEndian, uint32(PortmapperProgram))
	binary.Write(&buf, binary.BigEndian, uint32(4))
	binary.Write(&buf, binary.BigEndian, uint32(3))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(0))
	binary.Write(&buf, binary.BigEndian, uint32(NFS_PROGRAM))
	binary.Write(&buf, binary.BigEndian, uint32(NFS_V3))
	xdrEncodeString(&buf, "tcp")
	xdrEncodeString(&buf, "")
	xdrEncodeString(&buf, "")
	result, err := pm.handleCall(buf.Bytes(), nil)
	if err != nil {
		t.Fatalf("handleCall GETADDR v4: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

// Tests for portmapper functions
func TestPortmapperBasics(t *testing.T) {
	t.Run("create and configure portmapper", func(t *testing.T) {
		pm := NewPortmapper()
		pm.SetDebug(true)

		// Register a service (returns void)
		pm.RegisterService(100003, 3, 6, 2049) // NFS v3 TCP

		// Get port
		port := pm.GetPort(100003, 3, 6)
		if port != 2049 {
			t.Errorf("Expected port 2049, got %d", port)
		}

		// Get mappings
		mappings := pm.GetMappings()
		if len(mappings) == 0 {
			t.Error("Expected at least one mapping")
		}

		// Unregister (takes 3 params: prog, vers, prot)
		pm.UnregisterService(100003, 3, 6)
	})
}

// Tests for portmapper internal handlers
func TestPortmapperInternalHandlers(t *testing.T) {
	pm := NewPortmapper()

	t.Run("handleSet", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(100003)) // prog
		binary.Write(&buf, binary.BigEndian, uint32(3))      // vers
		binary.Write(&buf, binary.BigEndian, uint32(6))      // prot (TCP)
		binary.Write(&buf, binary.BigEndian, uint32(2049))   // port

		result := pm.handleSet(&buf, nil)
		if len(result) != 4 {
			t.Errorf("Expected 4 bytes result, got %d", len(result))
		}

		// Verify service was registered
		port := pm.GetPort(100003, 3, 6)
		if port != 2049 {
			t.Errorf("Expected port 2049, got %d", port)
		}
	})

	t.Run("handleUnset", func(t *testing.T) {
		// First register a service
		pm.RegisterService(100005, 1, 6, 2050)

		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(100005)) // prog
		binary.Write(&buf, binary.BigEndian, uint32(1))      // vers
		binary.Write(&buf, binary.BigEndian, uint32(6))      // prot (TCP)
		binary.Write(&buf, binary.BigEndian, uint32(0))      // port (ignored)

		result := pm.handleUnset(&buf, nil)
		if len(result) != 4 {
			t.Errorf("Expected 4 bytes result, got %d", len(result))
		}

		// Verify service was unregistered
		port := pm.GetPort(100005, 1, 6)
		if port != 0 {
			t.Errorf("Expected port 0 after unset, got %d", port)
		}
	})

	t.Run("handleRpcbSet", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(100010)) // prog
		binary.Write(&buf, binary.BigEndian, uint32(2))      // vers
		xdrEncodeString(&buf, "tcp")                         // netid
		xdrEncodeString(&buf, "127.0.0.1.8.5")               // uaddr (port 2053)
		xdrEncodeString(&buf, "superuser")                   // owner

		result := pm.handleRpcbSet(&buf)
		if len(result) != 4 {
			t.Errorf("Expected 4 bytes result, got %d", len(result))
		}

		// Verify service was registered
		port := pm.GetPort(100010, 2, IPPROTO_TCP)
		if port != 2053 {
			t.Errorf("Expected port 2053, got %d", port)
		}
	})

	t.Run("handleRpcbSet with UDP", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(100011)) // prog
		binary.Write(&buf, binary.BigEndian, uint32(1))      // vers
		xdrEncodeString(&buf, "udp")                         // netid
		xdrEncodeString(&buf, "127.0.0.1.8.6")               // uaddr (port 2054)
		xdrEncodeString(&buf, "superuser")                   // owner

		pm.handleRpcbSet(&buf)

		// Verify service was registered with UDP protocol
		port := pm.GetPort(100011, 1, IPPROTO_UDP)
		if port != 2054 {
			t.Errorf("Expected port 2054, got %d", port)
		}
	})

	t.Run("handleRpcbUnset", func(t *testing.T) {
		// First register a service
		pm.RegisterService(100012, 1, IPPROTO_TCP, 2055)

		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(100012)) // prog
		binary.Write(&buf, binary.BigEndian, uint32(1))      // vers
		xdrEncodeString(&buf, "tcp")                         // netid
		xdrEncodeString(&buf, "")                            // r_addr (ignored)
		xdrEncodeString(&buf, "")                            // r_owner (ignored)

		result := pm.handleRpcbUnset(&buf)
		if len(result) != 4 {
			t.Errorf("Expected 4 bytes result, got %d", len(result))
		}

		// Verify service was unregistered
		port := pm.GetPort(100012, 1, IPPROTO_TCP)
		if port != 0 {
			t.Errorf("Expected port 0 after unset, got %d", port)
		}
	})

	t.Run("handleRpcbUnset with UDP", func(t *testing.T) {
		pm.RegisterService(100013, 1, IPPROTO_UDP, 2056)

		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(100013))
		binary.Write(&buf, binary.BigEndian, uint32(1))
		xdrEncodeString(&buf, "udp6") // udp6 uses UDP protocol
		xdrEncodeString(&buf, "")
		xdrEncodeString(&buf, "")

		pm.handleRpcbUnset(&buf)

		port := pm.GetPort(100013, 1, IPPROTO_UDP)
		if port != 0 {
			t.Errorf("Expected port 0, got %d", port)
		}
	})

	t.Run("handleRpcbDump", func(t *testing.T) {
		// Clear and register some services
		pm2 := NewPortmapper()
		pm2.RegisterService(100003, 3, IPPROTO_TCP, 2049)
		pm2.RegisterService(100003, 3, IPPROTO_UDP, 2049)

		result := pm2.handleRpcbDump()
		if len(result) == 0 {
			t.Error("Expected non-empty result from handleRpcbDump")
		}
	})

	t.Run("handleGetAddr with tcp", func(t *testing.T) {
		pm3 := NewPortmapper()
		pm3.RegisterService(100003, 3, IPPROTO_TCP, 2049)

		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(100003)) // prog
		binary.Write(&buf, binary.BigEndian, uint32(3))      // vers
		xdrEncodeString(&buf, "tcp")                         // netid
		xdrEncodeString(&buf, "")                            // r_addr
		xdrEncodeString(&buf, "")                            // r_owner

		result := pm3.handleGetAddr(&buf)
		if len(result) == 0 {
			t.Error("Expected non-empty result from handleGetAddr")
		}
	})

	t.Run("handleGetAddr with tcp6", func(t *testing.T) {
		pm4 := NewPortmapper()
		pm4.RegisterService(100003, 3, IPPROTO_TCP, 2049)

		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(100003))
		binary.Write(&buf, binary.BigEndian, uint32(3))
		xdrEncodeString(&buf, "tcp6") // IPv6
		xdrEncodeString(&buf, "")
		xdrEncodeString(&buf, "")

		result := pm4.handleGetAddr(&buf)
		if len(result) == 0 {
			t.Error("Expected non-empty result from handleGetAddr for tcp6")
		}
	})

	t.Run("handleGetAddr not found", func(t *testing.T) {
		pm5 := NewPortmapper()

		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(999999)) // unknown prog
		binary.Write(&buf, binary.BigEndian, uint32(1))
		xdrEncodeString(&buf, "tcp")
		xdrEncodeString(&buf, "")
		xdrEncodeString(&buf, "")

		result := pm5.handleGetAddr(&buf)
		// Should return empty string (XDR encoded)
		if len(result) < 4 {
			t.Error("Expected result from handleGetAddr")
		}
	})
}

func TestPortmapperMakeReply(t *testing.T) {
	pm := NewPortmapper()

	t.Run("make success reply", func(t *testing.T) {
		data := []byte{0x00, 0x00, 0x00, 0x01}
		reply := pm.makeReply(12345, 0, data) // SUCCESS
		if len(reply) == 0 {
			t.Error("Expected non-empty reply")
		}
	})

	t.Run("make error reply", func(t *testing.T) {
		reply := pm.makeReply(12345, 1, nil) // PROG_UNAVAIL
		if len(reply) == 0 {
			t.Error("Expected non-empty reply")
		}
	})
}

// Tests for skipAuth
func TestSkipAuth(t *testing.T) {
	pm := NewPortmapper()

	t.Run("skip valid auth", func(t *testing.T) {
		var buf bytes.Buffer
		// Write auth flavor (AUTH_NONE = 0)
		binary.Write(&buf, binary.BigEndian, uint32(0))
		// Write auth length (0)
		binary.Write(&buf, binary.BigEndian, uint32(0))

		err := pm.skipAuth(&buf)
		if err != nil {
			t.Errorf("skipAuth failed: %v", err)
		}
	})

	t.Run("skip auth with body", func(t *testing.T) {
		var buf bytes.Buffer
		// Write auth flavor (AUTH_SYS = 1)
		binary.Write(&buf, binary.BigEndian, uint32(1))
		// Write auth length (8)
		binary.Write(&buf, binary.BigEndian, uint32(8))
		// Write 8 bytes of auth data
		buf.Write(make([]byte, 8))

		err := pm.skipAuth(&buf)
		if err != nil {
			t.Errorf("skipAuth failed: %v", err)
		}
	})

	t.Run("skip auth empty buffer", func(t *testing.T) {
		var buf bytes.Buffer
		err := pm.skipAuth(&buf)
		if err == nil {
			t.Error("Expected error for empty buffer")
		}
	})
}
