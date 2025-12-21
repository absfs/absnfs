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
		xdrEncodeString(&args, "tcp")  // netid
		xdrEncodeString(&args, "")     // r_addr (ignored)
		xdrEncodeString(&args, "")     // r_owner

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
		expected := "127.0.0.1.8.1"
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
	binary.Write(&call, binary.BigEndian, uint32(1))                // XID
	binary.Write(&call, binary.BigEndian, uint32(RPC_CALL))         // Message type
	binary.Write(&call, binary.BigEndian, uint32(2))                // RPC version
	binary.Write(&call, binary.BigEndian, uint32(PortmapperProgram)) // Program
	binary.Write(&call, binary.BigEndian, version)                  // Version
	binary.Write(&call, binary.BigEndian, procedure)                // Procedure
	binary.Write(&call, binary.BigEndian, uint32(0))                // Auth flavor
	binary.Write(&call, binary.BigEndian, uint32(0))                // Auth length
	binary.Write(&call, binary.BigEndian, uint32(0))                // Verifier flavor
	binary.Write(&call, binary.BigEndian, uint32(0))                // Verifier length
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
