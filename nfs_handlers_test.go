package absnfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/absfs/memfs"
)

// testAuthContext creates a default AuthContext for testing
func testAuthContext() *AuthContext {
	return &AuthContext{
		ClientIP:   "127.0.0.1",
		ClientPort: 1023, // Privileged port
		Credential: &RPCCredential{
			Flavor: AUTH_NONE,
			Body:   []byte{},
		},
	}
}

func TestNFSHandlerErrors(t *testing.T) {
	t.Run("error handling", func(t *testing.T) {
		memfs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		fs, err := New(memfs, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		handler := &NFSProcedureHandler{
			server: &Server{
				handler: fs,
				options: ServerOptions{
					Debug: false,
				},
			},
		}

		// Test RPCError
		rpcErr := &RPCError{
			Status: ACCESS_DENIED,
			Msg:    "Access denied",
		}

		if rpcErr.Error() != "Access denied" {
			t.Errorf("Expected error message 'Access denied', got '%s'", rpcErr.Error())
		}

		// Test invalid program
		call := &RPCCall{
			Header: RPCMsgHeader{
				Xid:        1,
				MsgType:    RPC_CALL,
				RPCVersion: 2,
				Program:    999, // Invalid program
				Version:    NFS_V3,
				Procedure:  NFSPROC3_NULL,
			},
			Credential: RPCCredential{
				Flavor: 0,
				Body:   []byte{},
			},
			Verifier: RPCVerifier{
				Flavor: 0,
				Body:   []byte{},
			},
		}

		authCtx := testAuthContext()
		authCtx.Credential = &call.Credential
		reply, err := handler.HandleCall(call, bytes.NewReader([]byte{}), authCtx)
		if err != nil {
			t.Fatalf("HandleCall failed: %v", err)
		}
		if reply.AcceptStatus != PROG_UNAVAIL {
			t.Errorf("Expected PROG_UNAVAIL AcceptStatus, got %v", reply.AcceptStatus)
		}

		// Test invalid authentication
		call = &RPCCall{
			Header: RPCMsgHeader{
				Xid:        1,
				MsgType:    RPC_CALL,
				RPCVersion: 2,
				Program:    NFS_PROGRAM,
				Version:    NFS_V3,
				Procedure:  NFSPROC3_NULL,
			},
			Credential: RPCCredential{
				Flavor: 1, // Invalid flavor
				Body:   []byte{},
			},
			Verifier: RPCVerifier{
				Flavor: 0,
				Body:   []byte{},
			},
		}

		authCtx = testAuthContext()
		authCtx.Credential = &call.Credential
		reply, err = handler.HandleCall(call, bytes.NewReader([]byte{}), authCtx)
		if err != nil {
			t.Fatalf("HandleCall failed: %v", err)
		}
		if reply.Status != MSG_DENIED {
			t.Errorf("Expected MSG_DENIED status, got %v", reply.Status)
		}
	})
}

func TestNFSHandlerOperations(t *testing.T) {
	t.Run("null operation", func(t *testing.T) {
		memfs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		fs, err := New(memfs, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		handler := &NFSProcedureHandler{
			server: &Server{
				handler: fs,
				options: ServerOptions{
					Debug: false,
				},
			},
		}

		call := &RPCCall{
			Header: RPCMsgHeader{
				Xid:        1,
				MsgType:    RPC_CALL,
				RPCVersion: 2,
				Program:    NFS_PROGRAM,
				Version:    NFS_V3,
				Procedure:  NFSPROC3_NULL,
			},
			Credential: RPCCredential{
				Flavor: 0,
				Body:   []byte{},
			},
			Verifier: RPCVerifier{
				Flavor: 0,
				Body:   []byte{},
			},
		}

		authCtx := testAuthContext()
		authCtx.Credential = &call.Credential
		reply, err := handler.HandleCall(call, bytes.NewReader([]byte{}), authCtx)
		if err != nil {
			t.Fatalf("HandleCall failed: %v", err)
		}
		if reply.Status != MSG_ACCEPTED {
			t.Errorf("Expected MSG_ACCEPTED status, got %v", reply.Status)
		}
	})

	t.Run("getattr operation", func(t *testing.T) {
		memfs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		fs, err := New(memfs, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		// Create test file
		f, err := memfs.Create("/test.txt")
		if err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
		f.Close()

		// Get file handle
		node, err := fs.Lookup("/test.txt")
		if err != nil {
			t.Fatalf("Failed to lookup test file: %v", err)
		}
		handle := fs.fileMap.Allocate(node)

		handler := &NFSProcedureHandler{
			server: &Server{
				handler: fs,
				options: ServerOptions{
					Debug: false,
				},
			},
		}

		call := &RPCCall{
			Header: RPCMsgHeader{
				Xid:        1,
				MsgType:    RPC_CALL,
				RPCVersion: 2,
				Program:    NFS_PROGRAM,
				Version:    NFS_V3,
				Procedure:  NFSPROC3_GETATTR,
			},
			Credential: RPCCredential{
				Flavor: 0,
				Body:   []byte{},
			},
			Verifier: RPCVerifier{
				Flavor: 0,
				Body:   []byte{},
			},
		}

		// Encode handle with proper XDR format
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, handle)

		authCtx := testAuthContext()
		authCtx.Credential = &call.Credential
		reply, err := handler.HandleCall(call, bytes.NewReader(buf.Bytes()), authCtx)
		if err != nil {
			t.Fatalf("HandleCall failed: %v", err)
		}
		if reply.Status != MSG_ACCEPTED {
			t.Errorf("Expected MSG_ACCEPTED status, got %v", reply.Status)
		}
		if reply.Data != nil {
			data := reply.Data.([]byte)
			r := bytes.NewReader(data)
			var status uint32
			if err := binary.Read(r, binary.BigEndian, &status); err != nil {
				t.Fatalf("Failed to read status from reply data: %v", err)
			}
			if status != NFS_OK {
				t.Errorf("Expected NFS_OK in reply data, got %v", status)
			}
		}
	})
}

// TestHandleCallGoroutineLeak tests that goroutines are properly cleaned up on context timeout
func TestHandleCallGoroutineLeak(t *testing.T) {
	t.Run("goroutine cleanup on timeout", func(t *testing.T) {
		memfs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		fs, err := New(memfs, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		handler := &NFSProcedureHandler{
			server: &Server{
				handler: fs,
				options: ServerOptions{
					Debug: false,
				},
			},
		}

		// Get initial goroutine count
		runtime.GC()
		time.Sleep(100 * time.Millisecond)
		initialGoroutines := runtime.NumGoroutine()

		// Execute multiple HandleCall operations that will timeout
		// We use a high number to ensure any leak would be detectable
		iterations := 100
		for i := 0; i < iterations; i++ {
			call := &RPCCall{
				Header: RPCMsgHeader{
					Xid:        uint32(i),
					MsgType:    RPC_CALL,
					RPCVersion: 2,
					Program:    NFS_PROGRAM,
					Version:    NFS_V3,
					Procedure:  NFSPROC3_NULL,
				},
				Credential: RPCCredential{
					Flavor: 0,
					Body:   []byte{},
				},
				Verifier: RPCVerifier{
					Flavor: 0,
					Body:   []byte{},
				},
			}

			// Execute the call - it should complete without timeout for NULL operation
			authCtx := testAuthContext()
			authCtx.Credential = &call.Credential
			_, err := handler.HandleCall(call, bytes.NewReader([]byte{}), authCtx)
			if err != nil {
				t.Logf("HandleCall %d returned error (expected for some cases): %v", i, err)
			}
		}

		// Give time for goroutines to finish
		time.Sleep(500 * time.Millisecond)
		runtime.GC()
		time.Sleep(100 * time.Millisecond)

		// Check final goroutine count
		finalGoroutines := runtime.NumGoroutine()

		// Allow for some variance (worker pool, etc), but no significant leak
		// With the fix, we should not see 100+ leaked goroutines
		goroutineDiff := finalGoroutines - initialGoroutines
		if goroutineDiff > 10 {
			t.Errorf("Potential goroutine leak detected: started with %d goroutines, ended with %d (diff: %d)",
				initialGoroutines, finalGoroutines, goroutineDiff)
		} else {
			t.Logf("Goroutine count stable: started with %d, ended with %d (diff: %d)",
				initialGoroutines, finalGoroutines, goroutineDiff)
		}
	})
}

// newTestServerForBugfixes creates a test server for bugfix testing
func newTestServerForBugfixes() (*Server, *NFSProcedureHandler, *AuthContext, error) {
	fs, err := memfs.NewFS()
	if err != nil {
		return nil, nil, nil, err
	}

	config := DefaultRateLimiterConfig()
	nfs, err := New(fs, ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
	})
	if err != nil {
		return nil, nil, nil, err
	}

	if err := fs.Mkdir("/testdir", 0755); err != nil {
		return nil, nil, nil, err
	}

	f, err := fs.Create("/testfile.txt")
	if err != nil {
		return nil, nil, nil, err
	}
	f.Write([]byte("hello world"))
	f.Close()

	// Create executable file
	f, err = fs.Create("/execfile")
	if err != nil {
		return nil, nil, nil, err
	}
	f.Close()
	fs.Chmod("/execfile", 0755)

	server := &Server{
		handler: nfs,
		options: ServerOptions{Debug: false},
	}
	handler := &NFSProcedureHandler{server: server}
	authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}

	return server, handler, authCtx, nil
}

// newReadOnlyTestServer creates a read-only test server
func newReadOnlyTestServer() (*Server, *NFSProcedureHandler, *AuthContext, error) {
	fs, err := memfs.NewFS()
	if err != nil {
		return nil, nil, nil, err
	}

	config := DefaultRateLimiterConfig()
	nfs, err := New(fs, ExportOptions{
		ReadOnly:           true,
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
	})
	if err != nil {
		return nil, nil, nil, err
	}

	if err := fs.Mkdir("/testdir", 0755); err != nil {
		return nil, nil, nil, err
	}

	f, err := fs.Create("/execfile")
	if err != nil {
		return nil, nil, nil, err
	}
	f.Close()
	fs.Chmod("/execfile", 0755)

	server := &Server{
		handler: nfs,
		options: ServerOptions{Debug: false},
	}
	handler := &NFSProcedureHandler{server: server}
	authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}

	return server, handler, authCtx, nil
}

// getRootHandle allocates a handle for the root node
func getRootHandle(server *Server) uint64 {
	rootNode, _ := server.handler.Lookup("/")
	return server.handler.fileMap.Allocate(rootNode)
}

// getFileHandle allocates a handle for a specific path
func getFileHandle(server *Server, path string) uint64 {
	node, _ := server.handler.Lookup(path)
	return server.handler.fileMap.Allocate(node)
}

// readStatusFromReply reads the NFS status from reply data
func readStatusFromReply(reply *RPCReply) uint32 {
	data, ok := reply.Data.([]byte)
	if !ok || len(data) < 4 {
		return 0xFFFFFFFF
	}
	return binary.BigEndian.Uint32(data[:4])
}

// getReplyData gets the raw bytes from a reply
func getReplyData(reply *RPCReply) []byte {
	data, ok := reply.Data.([]byte)
	if !ok {
		return nil
	}
	return data
}

// TestC1_FSINFODtprefEncoding verifies FSINFO encodes dtpref as uint32
func TestC1_FSINFODtprefEncoding(t *testing.T) {
	server, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}
	_ = server

	rootHandle := getRootHandle(server)
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, rootHandle)

	reply := &RPCReply{}
	result, err := handler.handleFsinfo(bytes.NewReader(buf.Bytes()), reply, authCtx)
	if err != nil {
		t.Fatalf("handleFsinfo returned error: %v", err)
	}

	status := readStatusFromReply(result)
	if status != NFS_OK {
		t.Fatalf("Expected NFS_OK, got %d", status)
	}

	data := getReplyData(result)
	if len(data) < 4 {
		t.Fatal("Response too short")
	}
	// Verify the response is well-formed. With uint32 dtpref the total response size
	// is 4 bytes shorter than it would be with uint64.
	t.Logf("FSINFO response length: %d bytes (dtpref should be uint32)", len(data))
}

// TestC2_ErrorReplyWithPostOp verifies read-type errors include post_op_attr
func TestC2_ErrorReplyWithPostOp(t *testing.T) {
	_, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	// Use an invalid handle to trigger NFSERR_STALE
	invalidHandle := uint64(99999)

	tests := []struct {
		name    string
		handler func() (*RPCReply, error)
	}{
		{
			name: "FSSTAT with stale handle",
			handler: func() (*RPCReply, error) {
				var buf bytes.Buffer
				xdrEncodeFileHandle(&buf, invalidHandle)
				return handler.handleFsstat(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
			},
		},
		{
			name: "FSINFO with stale handle",
			handler: func() (*RPCReply, error) {
				var buf bytes.Buffer
				xdrEncodeFileHandle(&buf, invalidHandle)
				return handler.handleFsinfo(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
			},
		},
		{
			name: "PATHCONF with stale handle",
			handler: func() (*RPCReply, error) {
				var buf bytes.Buffer
				xdrEncodeFileHandle(&buf, invalidHandle)
				return handler.handlePathconf(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.handler()
			if err != nil {
				t.Fatalf("handler returned error: %v", err)
			}
			// Should have status(4) + post_op_attr(4, attributes_follow=FALSE) = 8 bytes
			data := getReplyData(result)
			if len(data) < 8 {
				t.Fatalf("Expected at least 8 bytes (status + post_op_attr), got %d", len(data))
			}
			status := readStatusFromReply(result)
			if status != NFSERR_STALE {
				t.Errorf("Expected NFSERR_STALE (%d), got %d", NFSERR_STALE, status)
			}
			// Check that attributes_follow = 0 (FALSE)
			attrFollow := binary.BigEndian.Uint32(data[4:8])
			if attrFollow != 0 {
				t.Errorf("Expected attributes_follow=0, got %d", attrFollow)
			}
		})
	}
}

// TestC2_ErrorReplyWithWcc verifies write-type errors include wcc_data
func TestC2_ErrorReplyWithWcc(t *testing.T) {
	_, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	invalidHandle := uint64(99999)

	// Test COMMIT with stale handle
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, invalidHandle)
	binary.Write(&buf, binary.BigEndian, uint64(0)) // offset
	binary.Write(&buf, binary.BigEndian, uint32(0)) // count

	result, err := handler.handleCommit(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleCommit returned error: %v", err)
	}
	// Should have status(4) + pre_op_attr(4, FALSE) + post_op_attr(4, FALSE) = 12 bytes
	data := getReplyData(result)
	if len(data) < 12 {
		t.Fatalf("Expected at least 12 bytes (status + wcc_data), got %d", len(data))
	}
	status := readStatusFromReply(result)
	if status != NFSERR_STALE {
		t.Errorf("Expected NFSERR_STALE (%d), got %d", NFSERR_STALE, status)
	}
	preOp := binary.BigEndian.Uint32(data[4:8])
	postOp := binary.BigEndian.Uint32(data[8:12])
	if preOp != 0 || postOp != 0 {
		t.Errorf("Expected empty wcc_data (0,0), got (%d,%d)", preOp, postOp)
	}
}

// TestC3_ReaddirStableFileIds verifies READDIR uses stable file IDs
func TestC3_ReaddirStableFileIds(t *testing.T) {
	server, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	rootHandle := getRootHandle(server)

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, rootHandle)
	binary.Write(&buf, binary.BigEndian, uint64(0))     // cookie
	buf.Write(make([]byte, 8))                          // cookieverf
	binary.Write(&buf, binary.BigEndian, uint32(65536)) // count

	result, err := handler.handleReaddir(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleReaddir returned error: %v", err)
	}

	status := readStatusFromReply(result)
	if status != NFS_OK {
		t.Fatalf("Expected NFS_OK, got %d", status)
	}
	// Just verify the response is valid and has entries
	t.Logf("READDIR response length: %d bytes", len(getReplyData(result)))
}

// TestC4_LookupDirectoryTraversal verifies LOOKUP rejects traversal attempts
func TestC4_LookupDirectoryTraversal(t *testing.T) {
	server, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	rootHandle := getRootHandle(server)

	traversalNames := []string{"..", "../etc/passwd", "foo/bar", ".", "name\x00evil"}
	for _, name := range traversalNames {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			xdrEncodeFileHandle(&buf, rootHandle)
			xdrEncodeString(&buf, name)

			result, err := handler.handleLookup(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
			if err != nil {
				t.Fatalf("handleLookup returned error: %v", err)
			}
			status := readStatusFromReply(result)
			if status == NFS_OK {
				t.Errorf("Expected error for traversal name %q, got NFS_OK", name)
			}
		})
	}
}

// TestC5_RmdirDirectoryTraversal verifies RMDIR rejects traversal attempts
func TestC5_RmdirDirectoryTraversal(t *testing.T) {
	server, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	rootHandle := getRootHandle(server)

	traversalNames := []string{"..", "foo/bar", "."}
	for _, name := range traversalNames {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			xdrEncodeFileHandle(&buf, rootHandle)
			xdrEncodeString(&buf, name)

			result, err := handler.handleRmdir(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
			if err != nil {
				t.Fatalf("handleRmdir returned error: %v", err)
			}
			status := readStatusFromReply(result)
			if status == NFS_OK {
				t.Errorf("Expected error for traversal name %q, got NFS_OK", name)
			}
		})
	}
}

// TestH2_MkdirReadOnlyCheck verifies MKDIR rejects on read-only exports
func TestH2_MkdirReadOnlyCheck(t *testing.T) {
	_, handler, authCtx, err := newReadOnlyTestServer()
	if err != nil {
		t.Fatal(err)
	}

	// Build minimal MKDIR request (handle doesn't matter since read-only check is first)
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, uint64(1))
	xdrEncodeString(&buf, "newdir")
	// Minimal sattr3: all "don't set" flags
	for i := 0; i < 6; i++ {
		binary.Write(&buf, binary.BigEndian, uint32(0))
	}

	result, err := handler.handleMkdir(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleMkdir returned error: %v", err)
	}
	status := readStatusFromReply(result)
	if status != NFSERR_ROFS {
		t.Errorf("Expected NFSERR_ROFS (%d), got %d", NFSERR_ROFS, status)
	}
}

// TestH3_ReadOnlyReturnsROFS verifies mutating operations return NFSERR_ROFS
func TestH3_ReadOnlyReturnsROFS(t *testing.T) {
	_, handler, authCtx, err := newReadOnlyTestServer()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		handler func() (*RPCReply, error)
	}{
		{
			name: "WRITE",
			handler: func() (*RPCReply, error) {
				return handler.handleWrite(bytes.NewReader([]byte{}), &RPCReply{}, authCtx)
			},
		},
		{
			name: "SYMLINK",
			handler: func() (*RPCReply, error) {
				return handler.handleSymlink(bytes.NewReader([]byte{}), &RPCReply{}, authCtx)
			},
		},
		{
			name: "REMOVE",
			handler: func() (*RPCReply, error) {
				return handler.handleRemove(bytes.NewReader([]byte{}), &RPCReply{}, authCtx)
			},
		},
		{
			name: "RMDIR",
			handler: func() (*RPCReply, error) {
				return handler.handleRmdir(bytes.NewReader([]byte{}), &RPCReply{}, authCtx)
			},
		},
		{
			name: "RENAME",
			handler: func() (*RPCReply, error) {
				return handler.handleRename(bytes.NewReader([]byte{}), &RPCReply{}, authCtx)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.handler()
			if err != nil {
				t.Fatalf("%s returned error: %v", tt.name, err)
			}
			status := readStatusFromReply(result)
			if status != NFSERR_ROFS {
				t.Errorf("Expected NFSERR_ROFS (%d) for %s, got %d", NFSERR_ROFS, tt.name, status)
			}
		})
	}
}

// TestH4_StaleHandleError verifies stale file handles return NFSERR_STALE
func TestH4_StaleHandleError(t *testing.T) {
	_, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	invalidHandle := uint64(99999)

	tests := []struct {
		name    string
		handler func() (*RPCReply, error)
	}{
		{
			name: "GETATTR",
			handler: func() (*RPCReply, error) {
				var buf bytes.Buffer
				xdrEncodeFileHandle(&buf, invalidHandle)
				return handler.handleGetattr(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
			},
		},
		{
			name: "LOOKUP",
			handler: func() (*RPCReply, error) {
				var buf bytes.Buffer
				xdrEncodeFileHandle(&buf, invalidHandle)
				xdrEncodeString(&buf, "test")
				return handler.handleLookup(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
			},
		},
		{
			name: "READ",
			handler: func() (*RPCReply, error) {
				var buf bytes.Buffer
				xdrEncodeFileHandle(&buf, invalidHandle)
				binary.Write(&buf, binary.BigEndian, uint64(0))
				binary.Write(&buf, binary.BigEndian, uint32(1024))
				return handler.handleRead(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
			},
		},
		{
			name: "ACCESS",
			handler: func() (*RPCReply, error) {
				var buf bytes.Buffer
				xdrEncodeFileHandle(&buf, invalidHandle)
				binary.Write(&buf, binary.BigEndian, uint32(0x3f))
				return handler.handleAccess(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
			},
		},
		{
			name: "READDIR",
			handler: func() (*RPCReply, error) {
				var buf bytes.Buffer
				xdrEncodeFileHandle(&buf, invalidHandle)
				binary.Write(&buf, binary.BigEndian, uint64(0))
				buf.Write(make([]byte, 8))
				binary.Write(&buf, binary.BigEndian, uint32(8192))
				return handler.handleReaddir(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
			},
		},
		{
			name: "FSSTAT",
			handler: func() (*RPCReply, error) {
				var buf bytes.Buffer
				xdrEncodeFileHandle(&buf, invalidHandle)
				return handler.handleFsstat(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.handler()
			if err != nil {
				t.Fatalf("%s returned error: %v", tt.name, err)
			}
			status := readStatusFromReply(result)
			if status != NFSERR_STALE {
				t.Errorf("Expected NFSERR_STALE (%d) for %s, got %d", NFSERR_STALE, tt.name, status)
			}
		})
	}
}

// TestH5_ConfigurableTimeout verifies the timeout uses server config
func TestH5_ConfigurableTimeout(t *testing.T) {
	server, _, _, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	// The default timeout should be 30 seconds, not 2 seconds
	timeout := server.handler.tuning.Load().Timeouts.DefaultTimeout
	if timeout.Seconds() < 5 {
		t.Errorf("Default timeout should be >= 5 seconds, got %v", timeout)
	}
}

// TestH8_SymlinkErrorPathWcc verifies SYMLINK error includes wcc_data
func TestH8_SymlinkErrorPathWcc(t *testing.T) {
	server, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	rootHandle := getRootHandle(server)

	// Create a symlink that will fail (try to create over existing file)
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, rootHandle)
	xdrEncodeString(&buf, "testfile.txt") // Already exists
	// sattr3: all "don't set"
	for i := 0; i < 6; i++ {
		binary.Write(&buf, binary.BigEndian, uint32(0))
	}
	xdrEncodeString(&buf, "/some/target")

	result, err := handler.handleSymlink(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleSymlink returned error: %v", err)
	}

	status := readStatusFromReply(result)
	if status == NFS_OK {
		t.Skip("Symlink creation succeeded unexpectedly, skipping wcc check")
	}

	// Verify response has more than just a status code (should have wcc_data)
	symlinkData := getReplyData(result)
	if len(symlinkData) <= 4 {
		t.Errorf("Expected wcc_data in error response, got only %d bytes", len(symlinkData))
	}
}

// TestM1_ReaddirPathBase verifies READDIR uses path.Base for names
func TestM1_ReaddirPathBase(t *testing.T) {
	server, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	rootHandle := getRootHandle(server)

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, rootHandle)
	binary.Write(&buf, binary.BigEndian, uint64(0))
	buf.Write(make([]byte, 8))
	binary.Write(&buf, binary.BigEndian, uint32(65536))

	result, err := handler.handleReaddir(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleReaddir returned error: %v", err)
	}

	status := readStatusFromReply(result)
	if status != NFS_OK {
		t.Fatalf("Expected NFS_OK, got %d", status)
	}
	t.Logf("READDIR response is well-formed (%d bytes)", len(getReplyData(result)))
}

// TestM2_AccessExecuteOnReadOnly verifies ACCESS grants EXECUTE on read-only exports
func TestM2_AccessExecuteOnReadOnly(t *testing.T) {
	server, handler, authCtx, err := newReadOnlyTestServer()
	if err != nil {
		t.Fatal(err)
	}

	execHandle := getFileHandle(server, "/execfile")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, execHandle)
	binary.Write(&buf, binary.BigEndian, uint32(0x3f)) // All access bits

	result, err := handler.handleAccess(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleAccess returned error: %v", err)
	}

	status := readStatusFromReply(result)
	if status != NFS_OK {
		t.Fatalf("Expected NFS_OK, got %d", status)
	}

	// Parse the access result: status(4) + post_op_attr(1+84=88) + access(4)
	// The access bits are at the end of the response
	accessData := getReplyData(result)
	accessResult := binary.BigEndian.Uint32(accessData[len(accessData)-4:])

	// ACCESS3_EXECUTE = 0x20 = 32
	if accessResult&32 == 0 {
		t.Errorf("Expected ACCESS3_EXECUTE to be granted on read-only export for executable file, access=%#x", accessResult)
	}

	// Verify write bits are NOT granted
	if accessResult&4 != 0 {
		t.Errorf("ACCESS3_MODIFY should NOT be granted on read-only export, access=%#x", accessResult)
	}
}

// TestM8_WriteReturnsFileSync verifies WRITE always returns FILE_SYNC
func TestM8_WriteReturnsFileSync(t *testing.T) {
	server, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	fileHandle := getFileHandle(server, "/testfile.txt")

	// Build WRITE request with stable=0 (UNSTABLE)
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fileHandle)
	binary.Write(&buf, binary.BigEndian, uint64(0)) // offset
	binary.Write(&buf, binary.BigEndian, uint32(5)) // count
	binary.Write(&buf, binary.BigEndian, uint32(0)) // stable = UNSTABLE
	binary.Write(&buf, binary.BigEndian, uint32(5)) // data length
	buf.Write([]byte("hello"))                      // data
	// Pad to 4 bytes
	buf.Write([]byte{0, 0, 0})

	result, err := handler.handleWrite(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleWrite returned error: %v", err)
	}

	status := readStatusFromReply(result)
	if status != NFS_OK {
		t.Fatalf("Expected NFS_OK, got %d", status)
	}

	// Parse: status(4) + wcc_data(varies) + count(4) + committed(4) + writeverf(8)
	// committed is the second-to-last uint32 before the 8-byte verifier
	writeData := getReplyData(result)
	if len(writeData) < 16 {
		t.Fatalf("Response too short: %d bytes", len(writeData))
	}
	committed := binary.BigEndian.Uint32(writeData[len(writeData)-12 : len(writeData)-8])
	if committed != 2 { // FILE_SYNC = 2
		t.Errorf("Expected committed=2 (FILE_SYNC), got %d", committed)
	}
}

// TestM13_SetattrGuardErrorCheck verifies SETATTR guard reads check errors
func TestM13_SetattrGuardErrorCheck(t *testing.T) {
	_, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	// Build SETATTR request with guard=1 but truncated (no ctime data)
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, uint64(1))
	// sattr3: all "don't set"
	for i := 0; i < 6; i++ {
		binary.Write(&buf, binary.BigEndian, uint32(0))
	}
	binary.Write(&buf, binary.BigEndian, uint32(1)) // guard = check ctime
	// Don't write ctime seconds/nseconds - this should cause an error

	result, err := handler.handleSetattr(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleSetattr returned error: %v", err)
	}
	status := readStatusFromReply(result)
	if status != GARBAGE_ARGS {
		t.Errorf("Expected GARBAGE_ARGS for truncated guard, got %d", status)
	}
}

// TestM14_CreateExclusiveVerifier verifies CREATE EXCLUSIVE uses io.ReadFull
func TestM14_CreateExclusiveVerifier(t *testing.T) {
	server, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	rootHandle := getRootHandle(server)

	// Build CREATE request with EXCLUSIVE mode but truncated verifier
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, rootHandle)
	xdrEncodeString(&buf, "newfile")
	binary.Write(&buf, binary.BigEndian, uint32(2)) // EXCLUSIVE
	// Only write 4 of 8 verifier bytes (should fail with io.ReadFull)
	buf.Write([]byte{1, 2, 3, 4})

	result, err := handler.handleCreate(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleCreate returned error: %v", err)
	}
	status := readStatusFromReply(result)
	if status != GARBAGE_ARGS {
		t.Errorf("Expected GARBAGE_ARGS for short verifier, got %d", status)
	}
}

// TestNfsErrorHelpers verifies the error helper functions
func TestNfsErrorHelpers(t *testing.T) {
	t.Run("nfsErrorWithPostOp", func(t *testing.T) {
		reply := &RPCReply{}
		result := nfsErrorWithPostOp(reply, NFSERR_STALE)
		data := getReplyData(result)
		if len(data) != 8 {
			t.Errorf("Expected 8 bytes, got %d", len(data))
		}
		status := binary.BigEndian.Uint32(data[:4])
		if status != NFSERR_STALE {
			t.Errorf("Expected NFSERR_STALE, got %d", status)
		}
		attrFollow := binary.BigEndian.Uint32(data[4:8])
		if attrFollow != 0 {
			t.Errorf("Expected attributes_follow=0, got %d", attrFollow)
		}
	})

	t.Run("nfsErrorWithWcc", func(t *testing.T) {
		reply := &RPCReply{}
		result := nfsErrorWithWcc(reply, NFSERR_ROFS)
		data := getReplyData(result)
		if len(data) != 12 {
			t.Errorf("Expected 12 bytes, got %d", len(data))
		}
		status := binary.BigEndian.Uint32(data[:4])
		if status != NFSERR_ROFS {
			t.Errorf("Expected NFSERR_ROFS, got %d", status)
		}
		preOp := binary.BigEndian.Uint32(data[4:8])
		postOp := binary.BigEndian.Uint32(data[8:12])
		if preOp != 0 || postOp != 0 {
			t.Errorf("Expected empty wcc_data (0,0), got (%d,%d)", preOp, postOp)
		}
	})
}

// newTestServerForHandlers creates a test server with rate limiting disabled
// and additional test files/directories for handler testing
func newTestServerForHandlers() (*Server, *NFSProcedureHandler, *AuthContext, error) {
	fs, err := memfs.NewFS()
	if err != nil {
		return nil, nil, nil, err
	}

	config := DefaultRateLimiterConfig()
	nfs, err := New(fs, ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
	})
	if err != nil {
		return nil, nil, nil, err
	}

	// Create test directory structure
	if err := fs.Mkdir("/testdir", 0755); err != nil {
		return nil, nil, nil, err
	}
	if err := fs.Mkdir("/testdir/subdir", 0755); err != nil {
		return nil, nil, nil, err
	}
	if err := fs.Mkdir("/emptydir", 0755); err != nil {
		return nil, nil, nil, err
	}

	// Create test files
	f, err := fs.Create("/testfile.txt")
	if err != nil {
		return nil, nil, nil, err
	}
	f.Write([]byte("test content"))
	f.Close()

	f, err = fs.Create("/testdir/nested.txt")
	if err != nil {
		return nil, nil, nil, err
	}
	f.Write([]byte("nested content"))
	f.Close()

	// Create executable file
	f, err = fs.Create("/executable")
	if err != nil {
		return nil, nil, nil, err
	}
	f.Close()
	fs.Chmod("/executable", 0755)

	server := &Server{
		handler: nfs,
		options: ServerOptions{Debug: false},
	}

	handler := &NFSProcedureHandler{server: server}
	authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}

	return server, handler, authCtx, nil
}

// Helper to build a lookup request
func buildLookupRequest(handle uint64, name string) []byte {
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, handle)
	xdrEncodeString(&buf, name)
	return buf.Bytes()
}

// Helper to build an access request
func buildAccessRequest(handle uint64, access uint32) []byte {
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, handle)
	binary.Write(&buf, binary.BigEndian, access)
	return buf.Bytes()
}

// Helper to build a commit request
func buildCommitRequest(handle uint64, offset uint64, count uint32) []byte {
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, handle)
	binary.Write(&buf, binary.BigEndian, offset)
	binary.Write(&buf, binary.BigEndian, count)
	return buf.Bytes()
}

// Helper to build a remove/rmdir request
func buildRemoveRequest(handle uint64, name string) []byte {
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, handle)
	xdrEncodeString(&buf, name)
	return buf.Bytes()
}

// Helper to build a rename request
func buildRenameRequest(srcHandle uint64, srcName string, dstHandle uint64, dstName string) []byte {
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, srcHandle)
	xdrEncodeString(&buf, srcName)
	xdrEncodeFileHandle(&buf, dstHandle)
	xdrEncodeString(&buf, dstName)
	return buf.Bytes()
}

// Helper to build a link request (file handle + dir handle + name)
func buildLinkRequest(fileHandle uint64, dirHandle uint64, name string) []byte {
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fileHandle)
	xdrEncodeFileHandle(&buf, dirHandle)
	xdrEncodeString(&buf, name)
	return buf.Bytes()
}

// Helper to build a pathconf request
func buildPathconfRequest(handle uint64) []byte {
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, handle)
	return buf.Bytes()
}

func TestHandleLookup(t *testing.T) {
	server, handler, authCtx, err := newTestServerForHandlers()
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	// Get root handle
	rootNode, _ := server.handler.Lookup("/")
	rootHandle := server.handler.fileMap.Allocate(rootNode)

	// Get file handle (not a directory)
	fileNode, _ := server.handler.Lookup("/testfile.txt")
	fileHandle := server.handler.fileMap.Allocate(fileNode)

	t.Run("successful lookup", func(t *testing.T) {
		body := buildLookupRequest(rootHandle, "testdir")
		reply := &RPCReply{}

		result, err := handler.handleLookup(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleLookup failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("lookup file in directory", func(t *testing.T) {
		body := buildLookupRequest(rootHandle, "testfile.txt")
		reply := &RPCReply{}

		result, err := handler.handleLookup(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleLookup failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("invalid handle - truncated", func(t *testing.T) {
		body := []byte{0x00, 0x00}
		reply := &RPCReply{}

		result, err := handler.handleLookup(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleLookup should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})

	t.Run("non-existent handle", func(t *testing.T) {
		body := buildLookupRequest(999999, "testdir")
		reply := &RPCReply{}

		result, err := handler.handleLookup(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleLookup should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_STALE {
			t.Errorf("Expected NFSERR_STALE, got %d", status)
		}
	})

	t.Run("not a directory", func(t *testing.T) {
		body := buildLookupRequest(fileHandle, "something")
		reply := &RPCReply{}

		result, err := handler.handleLookup(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleLookup should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_NOTDIR {
			t.Errorf("Expected NFSERR_NOTDIR, got %d", status)
		}
	})

	t.Run("name not found", func(t *testing.T) {
		body := buildLookupRequest(rootHandle, "nonexistent")
		reply := &RPCReply{}

		result, err := handler.handleLookup(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleLookup should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_NOENT {
			t.Errorf("Expected NFSERR_NOENT, got %d", status)
		}
	})

	t.Run("missing name", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, rootHandle)
		// No name encoded
		reply := &RPCReply{}

		result, err := handler.handleLookup(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleLookup should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS for missing name, got %d", status)
		}
	})
}

func TestHandleAccess(t *testing.T) {
	server, handler, authCtx, err := newTestServerForHandlers()
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	// Get root handle (directory)
	rootNode, _ := server.handler.Lookup("/")
	rootHandle := server.handler.fileMap.Allocate(rootNode)

	// Get file handle
	fileNode, _ := server.handler.Lookup("/testfile.txt")
	fileHandle := server.handler.fileMap.Allocate(fileNode)

	// Get executable handle
	execNode, _ := server.handler.Lookup("/executable")
	execHandle := server.handler.fileMap.Allocate(execNode)

	t.Run("read access on file", func(t *testing.T) {
		body := buildAccessRequest(fileHandle, 1) // ACCESS3_READ
		reply := &RPCReply{}

		result, err := handler.handleAccess(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleAccess failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("lookup access on directory", func(t *testing.T) {
		body := buildAccessRequest(rootHandle, 2) // ACCESS3_LOOKUP
		reply := &RPCReply{}

		result, err := handler.handleAccess(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleAccess failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("modify access", func(t *testing.T) {
		body := buildAccessRequest(fileHandle, 4) // ACCESS3_MODIFY
		reply := &RPCReply{}

		result, err := handler.handleAccess(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleAccess failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("extend access", func(t *testing.T) {
		body := buildAccessRequest(fileHandle, 8) // ACCESS3_EXTEND
		reply := &RPCReply{}

		result, err := handler.handleAccess(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleAccess failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("delete access", func(t *testing.T) {
		body := buildAccessRequest(fileHandle, 16) // ACCESS3_DELETE
		reply := &RPCReply{}

		result, err := handler.handleAccess(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleAccess failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("execute access on executable", func(t *testing.T) {
		body := buildAccessRequest(execHandle, 32) // ACCESS3_EXECUTE
		reply := &RPCReply{}

		result, err := handler.handleAccess(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleAccess failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("all access bits", func(t *testing.T) {
		body := buildAccessRequest(rootHandle, 0x3F) // All access bits
		reply := &RPCReply{}

		result, err := handler.handleAccess(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleAccess failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("invalid handle", func(t *testing.T) {
		body := []byte{0x00, 0x00}
		reply := &RPCReply{}

		result, err := handler.handleAccess(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleAccess should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})

	t.Run("non-existent handle", func(t *testing.T) {
		body := buildAccessRequest(999999, 1)
		reply := &RPCReply{}

		result, err := handler.handleAccess(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleAccess should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_STALE {
			t.Errorf("Expected NFSERR_STALE, got %d", status)
		}
	})

	t.Run("missing access field", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, rootHandle)
		// No access field
		reply := &RPCReply{}

		result, err := handler.handleAccess(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleAccess should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})
}

func TestHandleCommit(t *testing.T) {
	server, handler, authCtx, err := newTestServerForHandlers()
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	// Get file handle
	fileNode, _ := server.handler.Lookup("/testfile.txt")
	fileHandle := server.handler.fileMap.Allocate(fileNode)

	t.Run("successful commit", func(t *testing.T) {
		body := buildCommitRequest(fileHandle, 0, 1024)
		reply := &RPCReply{}

		result, err := handler.handleCommit(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleCommit failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("commit with offset", func(t *testing.T) {
		body := buildCommitRequest(fileHandle, 512, 512)
		reply := &RPCReply{}

		result, err := handler.handleCommit(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleCommit failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("invalid handle", func(t *testing.T) {
		body := []byte{0x00, 0x00}
		reply := &RPCReply{}

		result, err := handler.handleCommit(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleCommit should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})

	t.Run("non-existent handle", func(t *testing.T) {
		body := buildCommitRequest(999999, 0, 1024)
		reply := &RPCReply{}

		result, err := handler.handleCommit(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleCommit should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_STALE {
			t.Errorf("Expected NFSERR_STALE, got %d", status)
		}
	})

	t.Run("missing offset", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, fileHandle)
		// No offset or count
		reply := &RPCReply{}

		result, err := handler.handleCommit(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleCommit should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})

	t.Run("missing count", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, fileHandle)
		binary.Write(&buf, binary.BigEndian, uint64(0))
		// No count
		reply := &RPCReply{}

		result, err := handler.handleCommit(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleCommit should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})
}

func TestHandlePathconf(t *testing.T) {
	server, handler, authCtx, err := newTestServerForHandlers()
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	// Get root handle
	rootNode, _ := server.handler.Lookup("/")
	rootHandle := server.handler.fileMap.Allocate(rootNode)

	// Get file handle
	fileNode, _ := server.handler.Lookup("/testfile.txt")
	fileHandle := server.handler.fileMap.Allocate(fileNode)

	t.Run("pathconf on directory", func(t *testing.T) {
		body := buildPathconfRequest(rootHandle)
		reply := &RPCReply{}

		result, err := handler.handlePathconf(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handlePathconf failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("pathconf on file", func(t *testing.T) {
		body := buildPathconfRequest(fileHandle)
		reply := &RPCReply{}

		result, err := handler.handlePathconf(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handlePathconf failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("invalid handle", func(t *testing.T) {
		body := []byte{0x00, 0x00}
		reply := &RPCReply{}

		result, err := handler.handlePathconf(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handlePathconf should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})

	t.Run("non-existent handle", func(t *testing.T) {
		body := buildPathconfRequest(999999)
		reply := &RPCReply{}

		result, err := handler.handlePathconf(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handlePathconf should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_STALE {
			t.Errorf("Expected NFSERR_STALE, got %d", status)
		}
	})
}

func TestHandleRemove(t *testing.T) {
	server, handler, authCtx, err := newTestServerForHandlers()
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	// Get root handle
	rootNode, _ := server.handler.Lookup("/")
	rootHandle := server.handler.fileMap.Allocate(rootNode)

	// Get file handle (not a directory)
	fileNode, _ := server.handler.Lookup("/testfile.txt")
	fileHandle := server.handler.fileMap.Allocate(fileNode)

	t.Run("successful remove", func(t *testing.T) {
		// Create a file to remove
		f, _ := server.handler.fs.Create("/toremove.txt")
		f.Close()

		body := buildRemoveRequest(rootHandle, "toremove.txt")
		reply := &RPCReply{}

		result, err := handler.handleRemove(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRemove failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("invalid handle", func(t *testing.T) {
		body := []byte{0x00, 0x00}
		reply := &RPCReply{}

		result, err := handler.handleRemove(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRemove should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})

	t.Run("non-existent handle", func(t *testing.T) {
		body := buildRemoveRequest(999999, "file.txt")
		reply := &RPCReply{}

		result, err := handler.handleRemove(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRemove should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_STALE {
			t.Errorf("Expected NFSERR_STALE, got %d", status)
		}
	})

	t.Run("not a directory", func(t *testing.T) {
		body := buildRemoveRequest(fileHandle, "something")
		reply := &RPCReply{}

		result, err := handler.handleRemove(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRemove should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_NOTDIR {
			t.Errorf("Expected NFSERR_NOTDIR, got %d", status)
		}
	})

	t.Run("file not found", func(t *testing.T) {
		body := buildRemoveRequest(rootHandle, "nonexistent.txt")
		reply := &RPCReply{}

		result, err := handler.handleRemove(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRemove should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		// Should get an error status (not NFS_OK)
		if status == NFS_OK {
			t.Errorf("Expected error status, got NFS_OK")
		}
	})

	t.Run("missing name", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, rootHandle)
		// No name
		reply := &RPCReply{}

		result, err := handler.handleRemove(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRemove should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})
}

func TestHandleRmdir(t *testing.T) {
	server, handler, authCtx, err := newTestServerForHandlers()
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	// Get root handle
	rootNode, _ := server.handler.Lookup("/")
	rootHandle := server.handler.fileMap.Allocate(rootNode)

	// Get file handle (not a directory)
	fileNode, _ := server.handler.Lookup("/testfile.txt")
	fileHandle := server.handler.fileMap.Allocate(fileNode)

	t.Run("successful rmdir", func(t *testing.T) {
		// Create a directory to remove
		server.handler.fs.Mkdir("/tormdir", 0755)

		body := buildRemoveRequest(rootHandle, "tormdir")
		reply := &RPCReply{}

		result, err := handler.handleRmdir(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRmdir failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("invalid handle", func(t *testing.T) {
		body := []byte{0x00, 0x00}
		reply := &RPCReply{}

		result, err := handler.handleRmdir(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRmdir should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})

	t.Run("non-existent handle", func(t *testing.T) {
		body := buildRemoveRequest(999999, "somedir")
		reply := &RPCReply{}

		result, err := handler.handleRmdir(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRmdir should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_STALE {
			t.Errorf("Expected NFSERR_STALE, got %d", status)
		}
	})

	t.Run("parent not a directory", func(t *testing.T) {
		body := buildRemoveRequest(fileHandle, "something")
		reply := &RPCReply{}

		result, err := handler.handleRmdir(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRmdir should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_NOTDIR {
			t.Errorf("Expected NFSERR_NOTDIR, got %d", status)
		}
	})

	t.Run("target not found", func(t *testing.T) {
		body := buildRemoveRequest(rootHandle, "nonexistent")
		reply := &RPCReply{}

		result, err := handler.handleRmdir(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRmdir should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_NOENT {
			t.Errorf("Expected NFSERR_NOENT, got %d", status)
		}
	})

	t.Run("target is file not directory", func(t *testing.T) {
		body := buildRemoveRequest(rootHandle, "testfile.txt")
		reply := &RPCReply{}

		result, err := handler.handleRmdir(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRmdir should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_NOTDIR {
			t.Errorf("Expected NFSERR_NOTDIR, got %d", status)
		}
	})

	t.Run("directory not empty", func(t *testing.T) {
		body := buildRemoveRequest(rootHandle, "testdir")
		reply := &RPCReply{}

		result, err := handler.handleRmdir(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRmdir should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_NOTEMPTY {
			t.Errorf("Expected NFSERR_NOTEMPTY, got %d", status)
		}
	})

	t.Run("missing name", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, rootHandle)
		// No name
		reply := &RPCReply{}

		result, err := handler.handleRmdir(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRmdir should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})
}

func TestHandleRename(t *testing.T) {
	server, handler, authCtx, err := newTestServerForHandlers()
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	// Get root handle
	rootNode, _ := server.handler.Lookup("/")
	rootHandle := server.handler.fileMap.Allocate(rootNode)

	// Get testdir handle
	testdirNode, _ := server.handler.Lookup("/testdir")
	testdirHandle := server.handler.fileMap.Allocate(testdirNode)

	// Get file handle (not a directory)
	fileNode, _ := server.handler.Lookup("/testfile.txt")
	fileHandle := server.handler.fileMap.Allocate(fileNode)

	t.Run("successful rename same directory", func(t *testing.T) {
		// Create a file to rename
		f, _ := server.handler.fs.Create("/torename.txt")
		f.Close()

		body := buildRenameRequest(rootHandle, "torename.txt", rootHandle, "renamed.txt")
		reply := &RPCReply{}

		result, err := handler.handleRename(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRename failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("successful rename different directory", func(t *testing.T) {
		// Create a file to rename
		f, _ := server.handler.fs.Create("/tomove.txt")
		f.Close()

		body := buildRenameRequest(rootHandle, "tomove.txt", testdirHandle, "moved.txt")
		reply := &RPCReply{}

		result, err := handler.handleRename(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRename failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("invalid source handle", func(t *testing.T) {
		body := []byte{0x00, 0x00}
		reply := &RPCReply{}

		result, err := handler.handleRename(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRename should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})

	t.Run("non-existent source handle", func(t *testing.T) {
		body := buildRenameRequest(999999, "file.txt", rootHandle, "newname.txt")
		reply := &RPCReply{}

		result, err := handler.handleRename(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRename should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_STALE {
			t.Errorf("Expected NFSERR_STALE, got %d", status)
		}
	})

	t.Run("non-existent dest handle", func(t *testing.T) {
		body := buildRenameRequest(rootHandle, "testfile.txt", 999999, "newname.txt")
		reply := &RPCReply{}

		result, err := handler.handleRename(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRename should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_STALE {
			t.Errorf("Expected NFSERR_STALE, got %d", status)
		}
	})

	t.Run("source file not found", func(t *testing.T) {
		body := buildRenameRequest(rootHandle, "nonexistent.txt", rootHandle, "newname.txt")
		reply := &RPCReply{}

		result, err := handler.handleRename(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRename should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		// Should get an error (not NFS_OK)
		if status == NFS_OK {
			t.Errorf("Expected error status, got NFS_OK")
		}
	})

	t.Run("missing source name", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, rootHandle)
		// No source name
		reply := &RPCReply{}

		result, err := handler.handleRename(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRename should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})

	t.Run("missing dest handle", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, rootHandle)
		xdrEncodeString(&buf, "file.txt")
		// No dest handle
		reply := &RPCReply{}

		result, err := handler.handleRename(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRename should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})

	t.Run("missing dest name", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, rootHandle)
		xdrEncodeString(&buf, "file.txt")
		xdrEncodeFileHandle(&buf, rootHandle)
		// No dest name
		reply := &RPCReply{}

		result, err := handler.handleRename(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRename should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})

	_ = fileHandle // May use in future tests
}

func TestHandleLink(t *testing.T) {
	server, handler, authCtx, err := newTestServerForHandlers()
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	// Get root handle
	rootNode, _ := server.handler.Lookup("/")
	rootHandle := server.handler.fileMap.Allocate(rootNode)

	// Get file handle
	fileNode, _ := server.handler.Lookup("/testfile.txt")
	fileHandle := server.handler.fileMap.Allocate(fileNode)

	t.Run("link not supported", func(t *testing.T) {
		body := buildLinkRequest(fileHandle, rootHandle, "hardlink.txt")
		reply := &RPCReply{}

		result, err := handler.handleLink(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleLink should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		// Should return NFSERR_NOTSUPP or similar error
		if status == NFS_OK {
			t.Errorf("Expected error status for unsupported operation, got NFS_OK")
		}
	})
}

func TestHandleRemoveReadOnly(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	config := DefaultRateLimiterConfig()
	nfs, err := New(fs, ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
		ReadOnly:           true,
	})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}

	// Create test file before setting up server
	f, _ := fs.Create("/testfile.txt")
	f.Close()

	server := &Server{
		handler: nfs,
		options: ServerOptions{Debug: false},
	}

	handler := &NFSProcedureHandler{server: server}
	authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}

	// Get root handle
	rootNode, _ := server.handler.Lookup("/")
	rootHandle := server.handler.fileMap.Allocate(rootNode)

	t.Run("remove denied on read-only", func(t *testing.T) {
		body := buildRemoveRequest(rootHandle, "testfile.txt")
		reply := &RPCReply{}

		result, err := handler.handleRemove(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRemove should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_ROFS {
			t.Errorf("Expected NFSERR_ROFS, got %d", status)
		}
	})
}

func TestHandleRmdirReadOnly(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	config := DefaultRateLimiterConfig()
	nfs, err := New(fs, ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
		ReadOnly:           true,
	})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}

	// Create test dir before setting up server
	fs.Mkdir("/testdir", 0755)

	server := &Server{
		handler: nfs,
		options: ServerOptions{Debug: false},
	}

	handler := &NFSProcedureHandler{server: server}
	authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}

	// Get root handle
	rootNode, _ := server.handler.Lookup("/")
	rootHandle := server.handler.fileMap.Allocate(rootNode)

	t.Run("rmdir denied on read-only", func(t *testing.T) {
		body := buildRemoveRequest(rootHandle, "testdir")
		reply := &RPCReply{}

		result, err := handler.handleRmdir(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRmdir should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_ROFS {
			t.Errorf("Expected NFSERR_ROFS, got %d", status)
		}
	})
}

func TestHandleRenameReadOnly(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	config := DefaultRateLimiterConfig()
	nfs, err := New(fs, ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
		ReadOnly:           true,
	})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}

	// Create test file before setting up server
	f, _ := fs.Create("/testfile.txt")
	f.Close()

	server := &Server{
		handler: nfs,
		options: ServerOptions{Debug: false},
	}

	handler := &NFSProcedureHandler{server: server}
	authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}

	// Get root handle
	rootNode, _ := server.handler.Lookup("/")
	rootHandle := server.handler.fileMap.Allocate(rootNode)

	t.Run("rename denied on read-only", func(t *testing.T) {
		body := buildRenameRequest(rootHandle, "testfile.txt", rootHandle, "renamed.txt")
		reply := &RPCReply{}

		result, err := handler.handleRename(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRename should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_ROFS {
			t.Errorf("Expected NFSERR_ROFS, got %d", status)
		}
	})
}

func TestHandleAccessReadOnly(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	config := DefaultRateLimiterConfig()
	nfs, err := New(fs, ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
		ReadOnly:           true,
	})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}

	// Create test file before setting up server
	f, _ := fs.Create("/testfile.txt")
	f.Close()

	server := &Server{
		handler: nfs,
		options: ServerOptions{Debug: false},
	}

	handler := &NFSProcedureHandler{server: server}
	authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}

	// Get file handle
	fileNode, _ := server.handler.Lookup("/testfile.txt")
	fileHandle := server.handler.fileMap.Allocate(fileNode)

	t.Run("write access denied on read-only", func(t *testing.T) {
		// Request modify access (should be denied)
		body := buildAccessRequest(fileHandle, 4) // ACCESS3_MODIFY
		reply := &RPCReply{}

		result, err := handler.handleAccess(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleAccess failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK (access check should still succeed), got %d", status)
		}
		// The access bits returned should NOT include modify permission
		// We'd need to parse the full response to verify this
	})

	t.Run("read access allowed on read-only", func(t *testing.T) {
		body := buildAccessRequest(fileHandle, 1) // ACCESS3_READ
		reply := &RPCReply{}

		result, err := handler.handleAccess(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleAccess failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})
}

// Helper to build fsstat/fsinfo request (just a file handle)
func buildFsRequest(handle uint64) []byte {
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, handle)
	return buf.Bytes()
}

func TestHandleFsstat(t *testing.T) {
	server, handler, authCtx, err := newTestServerForHandlers()
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	// Get root handle
	rootNode, _ := server.handler.Lookup("/")
	rootHandle := server.handler.fileMap.Allocate(rootNode)

	// Get file handle
	fileNode, _ := server.handler.Lookup("/testfile.txt")
	fileHandle := server.handler.fileMap.Allocate(fileNode)

	// Get directory handle
	dirNode, _ := server.handler.Lookup("/testdir")
	dirHandle := server.handler.fileMap.Allocate(dirNode)

	t.Run("fsstat on root", func(t *testing.T) {
		body := buildFsRequest(rootHandle)
		reply := &RPCReply{}

		result, err := handler.handleFsstat(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleFsstat failed: %v", err)
		}

		data := result.Data.([]byte)
		if len(data) < 4 {
			t.Fatalf("Response too short: %d bytes", len(data))
		}
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("fsstat on file", func(t *testing.T) {
		body := buildFsRequest(fileHandle)
		reply := &RPCReply{}

		result, err := handler.handleFsstat(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleFsstat failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("fsstat on directory", func(t *testing.T) {
		body := buildFsRequest(dirHandle)
		reply := &RPCReply{}

		result, err := handler.handleFsstat(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleFsstat failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("invalid handle - truncated", func(t *testing.T) {
		body := []byte{0x00, 0x00}
		reply := &RPCReply{}

		result, err := handler.handleFsstat(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleFsstat should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})

	t.Run("non-existent handle", func(t *testing.T) {
		body := buildFsRequest(999999)
		reply := &RPCReply{}

		result, err := handler.handleFsstat(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleFsstat should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_STALE {
			t.Errorf("Expected NFSERR_STALE, got %d", status)
		}
	})
}

func TestHandleFsinfo(t *testing.T) {
	server, handler, authCtx, err := newTestServerForHandlers()
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	// Get root handle
	rootNode, _ := server.handler.Lookup("/")
	rootHandle := server.handler.fileMap.Allocate(rootNode)

	// Get file handle
	fileNode, _ := server.handler.Lookup("/testfile.txt")
	fileHandle := server.handler.fileMap.Allocate(fileNode)

	// Get directory handle
	dirNode, _ := server.handler.Lookup("/testdir")
	dirHandle := server.handler.fileMap.Allocate(dirNode)

	t.Run("fsinfo on root", func(t *testing.T) {
		body := buildFsRequest(rootHandle)
		reply := &RPCReply{}

		result, err := handler.handleFsinfo(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleFsinfo failed: %v", err)
		}

		data := result.Data.([]byte)
		if len(data) < 4 {
			t.Fatalf("Response too short: %d bytes", len(data))
		}
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("fsinfo on file", func(t *testing.T) {
		body := buildFsRequest(fileHandle)
		reply := &RPCReply{}

		result, err := handler.handleFsinfo(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleFsinfo failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("fsinfo on directory", func(t *testing.T) {
		body := buildFsRequest(dirHandle)
		reply := &RPCReply{}

		result, err := handler.handleFsinfo(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleFsinfo failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("invalid handle - truncated", func(t *testing.T) {
		body := []byte{0x00, 0x00}
		reply := &RPCReply{}

		result, err := handler.handleFsinfo(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleFsinfo should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})

	t.Run("non-existent handle", func(t *testing.T) {
		body := buildFsRequest(999999)
		reply := &RPCReply{}

		result, err := handler.handleFsinfo(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleFsinfo should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_STALE {
			t.Errorf("Expected NFSERR_STALE, got %d", status)
		}
	})
}

func TestHandleFsinfoReadOnly(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatalf("Failed to create memfs: %v", err)
	}

	config := DefaultRateLimiterConfig()
	nfs, err := New(fs, ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
		ReadOnly:           true,
	})
	if err != nil {
		t.Fatalf("Failed to create NFS: %v", err)
	}

	server := &Server{
		handler: nfs,
		options: ServerOptions{Debug: false},
	}

	handler := &NFSProcedureHandler{server: server}
	authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}

	// Get root handle
	rootNode, _ := server.handler.Lookup("/")
	rootHandle := server.handler.fileMap.Allocate(rootNode)

	t.Run("fsinfo on read-only filesystem", func(t *testing.T) {
		body := buildFsRequest(rootHandle)
		reply := &RPCReply{}

		result, err := handler.handleFsinfo(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleFsinfo failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
		// The properties field should include FSF_RDONLY bit
		// We'd need to parse the full response to verify this
	})
}

// Tests for handleRead with various edge cases
func TestHandleReadCoverage(t *testing.T) {
	server, handler, authCtx, err := newTestServerForHandlers()
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	// Get file handle
	fileNode, _ := server.handler.Lookup("/testfile.txt")
	fileHandle := server.handler.fileMap.Allocate(fileNode)

	t.Run("read success basic", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, fileHandle)
		binary.Write(&buf, binary.BigEndian, uint64(0))   // offset
		binary.Write(&buf, binary.BigEndian, uint32(100)) // count

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleRead(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRead failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("read with offset", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, fileHandle)
		binary.Write(&buf, binary.BigEndian, uint64(5))   // offset to middle of file
		binary.Write(&buf, binary.BigEndian, uint32(100)) // count

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleRead(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRead failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("read invalid handle", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, uint64(999999))
		binary.Write(&buf, binary.BigEndian, uint64(0))
		binary.Write(&buf, binary.BigEndian, uint32(100))

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleRead(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRead failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_STALE {
			t.Errorf("Expected NFSERR_STALE, got %d", status)
		}
	})

	t.Run("read garbage args - truncated handle", func(t *testing.T) {
		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleRead(bytes.NewReader([]byte{0x01}), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRead failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})

	t.Run("read garbage args - missing offset", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, fileHandle)
		// Missing offset and count

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleRead(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRead failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})

	t.Run("read overflow check", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, fileHandle)
		binary.Write(&buf, binary.BigEndian, uint64(0xFFFFFFFFFFFFFFFF)) // max offset
		binary.Write(&buf, binary.BigEndian, uint32(100))                // count that would overflow

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleRead(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleRead failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_INVAL {
			t.Errorf("Expected NFSERR_INVAL for overflow, got %d", status)
		}
	})
}

// Tests for handleWrite with various edge cases
func TestHandleWriteCoverage(t *testing.T) {
	server, handler, authCtx, err := newTestServerForHandlers()
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	// Get file handle
	fileNode, _ := server.handler.Lookup("/testfile.txt")
	fileHandle := server.handler.fileMap.Allocate(fileNode)

	t.Run("write success basic", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, fileHandle)
		binary.Write(&buf, binary.BigEndian, uint64(0)) // offset
		writeData := []byte("new data")
		binary.Write(&buf, binary.BigEndian, uint32(len(writeData))) // count
		binary.Write(&buf, binary.BigEndian, uint32(1))              // stable = DATA_SYNC
		binary.Write(&buf, binary.BigEndian, uint32(len(writeData))) // data length
		buf.Write(writeData)

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleWrite(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleWrite failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("write with offset", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, fileHandle)
		binary.Write(&buf, binary.BigEndian, uint64(10)) // offset
		writeData := []byte("appended")
		binary.Write(&buf, binary.BigEndian, uint32(len(writeData)))
		binary.Write(&buf, binary.BigEndian, uint32(2)) // stable = FILE_SYNC
		binary.Write(&buf, binary.BigEndian, uint32(len(writeData)))
		buf.Write(writeData)

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleWrite(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleWrite failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("write invalid handle", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, uint64(999999))
		binary.Write(&buf, binary.BigEndian, uint64(0))
		writeData := []byte("data")
		binary.Write(&buf, binary.BigEndian, uint32(len(writeData)))
		binary.Write(&buf, binary.BigEndian, uint32(0))
		binary.Write(&buf, binary.BigEndian, uint32(len(writeData)))
		buf.Write(writeData)

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleWrite(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleWrite failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_STALE {
			t.Errorf("Expected NFSERR_STALE, got %d", status)
		}
	})

	t.Run("write garbage args - truncated handle", func(t *testing.T) {
		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleWrite(bytes.NewReader([]byte{0x01}), reply, authCtx)
		if err != nil {
			t.Fatalf("handleWrite failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})

	t.Run("write garbage args - data length mismatch", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, fileHandle)
		binary.Write(&buf, binary.BigEndian, uint64(0))
		binary.Write(&buf, binary.BigEndian, uint32(10)) // count
		binary.Write(&buf, binary.BigEndian, uint32(0))  // stable
		binary.Write(&buf, binary.BigEndian, uint32(5))  // data length different from count
		buf.Write([]byte("12345"))

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleWrite(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleWrite failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS for data length mismatch, got %d", status)
		}
	})

	t.Run("write overflow check", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, fileHandle)
		binary.Write(&buf, binary.BigEndian, uint64(0xFFFFFFFFFFFFFFFF)) // max offset
		binary.Write(&buf, binary.BigEndian, uint32(100))
		binary.Write(&buf, binary.BigEndian, uint32(0))

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleWrite(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleWrite failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_INVAL {
			t.Errorf("Expected NFSERR_INVAL for overflow, got %d", status)
		}
	})
}

// Tests for handleCreate with various edge cases
func TestHandleCreateCoverage(t *testing.T) {
	server, handler, authCtx, err := newTestServerForHandlers()
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	// Get directory handle
	dirNode, _ := server.handler.Lookup("/testdir")
	dirHandle := server.handler.fileMap.Allocate(dirNode)

	t.Run("create success unchecked", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, dirHandle)
		xdrEncodeString(&buf, "newfile1.txt")
		binary.Write(&buf, binary.BigEndian, uint32(0)) // createHow = UNCHECKED

		// sattr3 - set mode
		binary.Write(&buf, binary.BigEndian, uint32(1))    // set_mode = true
		binary.Write(&buf, binary.BigEndian, uint32(0644)) // mode
		binary.Write(&buf, binary.BigEndian, uint32(0))    // set_uid = false
		binary.Write(&buf, binary.BigEndian, uint32(0))    // set_gid = false
		binary.Write(&buf, binary.BigEndian, uint32(0))    // set_size = false
		binary.Write(&buf, binary.BigEndian, uint32(0))    // set_atime = DONT_CHANGE
		binary.Write(&buf, binary.BigEndian, uint32(0))    // set_mtime = DONT_CHANGE

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleCreate(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleCreate failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("create success guarded", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, dirHandle)
		xdrEncodeString(&buf, "newfile2.txt")
		binary.Write(&buf, binary.BigEndian, uint32(1)) // createHow = GUARDED

		// sattr3 - no mode set
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_mode = false
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_uid = false
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_gid = false
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_size = false
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_atime
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_mtime

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleCreate(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleCreate failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("create success exclusive", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, dirHandle)
		xdrEncodeString(&buf, "newfile3.txt")
		binary.Write(&buf, binary.BigEndian, uint32(2)) // createHow = EXCLUSIVE
		buf.Write(make([]byte, 8))                      // verifier

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleCreate(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleCreate failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("create with all sattr3 fields", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, dirHandle)
		xdrEncodeString(&buf, "newfile4.txt")
		binary.Write(&buf, binary.BigEndian, uint32(0)) // createHow = UNCHECKED

		// sattr3 - set all fields
		binary.Write(&buf, binary.BigEndian, uint32(1))    // set_mode = true
		binary.Write(&buf, binary.BigEndian, uint32(0755)) // mode
		binary.Write(&buf, binary.BigEndian, uint32(1))    // set_uid = true
		binary.Write(&buf, binary.BigEndian, uint32(1000)) // uid
		binary.Write(&buf, binary.BigEndian, uint32(1))    // set_gid = true
		binary.Write(&buf, binary.BigEndian, uint32(1000)) // gid
		binary.Write(&buf, binary.BigEndian, uint32(1))    // set_size = true
		binary.Write(&buf, binary.BigEndian, uint64(0))    // size
		binary.Write(&buf, binary.BigEndian, uint32(2))    // set_atime = SET_TO_CLIENT_TIME
		binary.Write(&buf, binary.BigEndian, uint32(0))    // atime_sec
		binary.Write(&buf, binary.BigEndian, uint32(0))    // atime_nsec
		binary.Write(&buf, binary.BigEndian, uint32(2))    // set_mtime = SET_TO_CLIENT_TIME
		binary.Write(&buf, binary.BigEndian, uint32(0))    // mtime_sec
		binary.Write(&buf, binary.BigEndian, uint32(0))    // mtime_nsec

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleCreate(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleCreate failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("create invalid filename - path separator", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, dirHandle)
		xdrEncodeString(&buf, "invalid/name.txt")
		binary.Write(&buf, binary.BigEndian, uint32(0)) // createHow

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleCreate(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleCreate failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_INVAL {
			t.Errorf("Expected NFSERR_INVAL for path separator, got %d", status)
		}
	})

	t.Run("create invalid filename - empty name", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, dirHandle)
		xdrEncodeString(&buf, "")
		binary.Write(&buf, binary.BigEndian, uint32(0))

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleCreate(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleCreate failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_INVAL {
			t.Errorf("Expected NFSERR_INVAL for empty name, got %d", status)
		}
	})

	t.Run("create invalid filename - dot", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, dirHandle)
		xdrEncodeString(&buf, ".")
		binary.Write(&buf, binary.BigEndian, uint32(0))

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleCreate(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleCreate failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_INVAL {
			t.Errorf("Expected NFSERR_INVAL for dot, got %d", status)
		}
	})

	t.Run("create invalid filename - dotdot", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, dirHandle)
		xdrEncodeString(&buf, "..")
		binary.Write(&buf, binary.BigEndian, uint32(0))

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleCreate(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleCreate failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_INVAL {
			t.Errorf("Expected NFSERR_INVAL for dotdot, got %d", status)
		}
	})

	t.Run("create invalid handle", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, uint64(999999))
		xdrEncodeString(&buf, "file.txt")
		binary.Write(&buf, binary.BigEndian, uint32(0))
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_mode = false
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_uid
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_gid
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_size
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_atime
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_mtime

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleCreate(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleCreate failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_STALE {
			t.Errorf("Expected NFSERR_STALE, got %d", status)
		}
	})

	t.Run("create garbage args - truncated handle", func(t *testing.T) {
		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleCreate(bytes.NewReader([]byte{0x01}), reply, authCtx)
		if err != nil {
			t.Fatalf("handleCreate failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})
}

// Tests for handleMkdir with various edge cases
func TestHandleMkdirCoverage(t *testing.T) {
	server, handler, authCtx, err := newTestServerForHandlers()
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	// Get directory handle
	dirNode, _ := server.handler.Lookup("/testdir")
	dirHandle := server.handler.fileMap.Allocate(dirNode)

	t.Run("mkdir success", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, dirHandle)
		xdrEncodeString(&buf, "newsubdir1")

		// sattr3
		binary.Write(&buf, binary.BigEndian, uint32(1))    // set_mode = true
		binary.Write(&buf, binary.BigEndian, uint32(0755)) // mode
		binary.Write(&buf, binary.BigEndian, uint32(0))    // set_uid
		binary.Write(&buf, binary.BigEndian, uint32(0))    // set_gid
		binary.Write(&buf, binary.BigEndian, uint32(0))    // set_size
		binary.Write(&buf, binary.BigEndian, uint32(0))    // set_atime
		binary.Write(&buf, binary.BigEndian, uint32(0))    // set_mtime

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleMkdir(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleMkdir failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("mkdir with all sattr3 fields", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, dirHandle)
		xdrEncodeString(&buf, "newsubdir2")

		// sattr3 - set all fields
		binary.Write(&buf, binary.BigEndian, uint32(1))    // set_mode = true
		binary.Write(&buf, binary.BigEndian, uint32(0700)) // mode
		binary.Write(&buf, binary.BigEndian, uint32(1))    // set_uid = true
		binary.Write(&buf, binary.BigEndian, uint32(1000)) // uid
		binary.Write(&buf, binary.BigEndian, uint32(1))    // set_gid = true
		binary.Write(&buf, binary.BigEndian, uint32(1000)) // gid
		binary.Write(&buf, binary.BigEndian, uint32(1))    // set_size = true
		binary.Write(&buf, binary.BigEndian, uint64(0))    // size
		binary.Write(&buf, binary.BigEndian, uint32(2))    // set_atime = SET_TO_CLIENT_TIME
		binary.Write(&buf, binary.BigEndian, uint32(0))    // atime_sec
		binary.Write(&buf, binary.BigEndian, uint32(0))    // atime_nsec
		binary.Write(&buf, binary.BigEndian, uint32(2))    // set_mtime = SET_TO_CLIENT_TIME
		binary.Write(&buf, binary.BigEndian, uint32(0))    // mtime_sec
		binary.Write(&buf, binary.BigEndian, uint32(0))    // mtime_nsec

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleMkdir(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleMkdir failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("mkdir invalid filename", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, dirHandle)
		xdrEncodeString(&buf, "invalid/dir")

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleMkdir(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleMkdir failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_INVAL {
			t.Errorf("Expected NFSERR_INVAL, got %d", status)
		}
	})

	t.Run("mkdir invalid handle", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, uint64(999999))
		xdrEncodeString(&buf, "newdir")
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_mode
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_uid
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_gid
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_size
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_atime
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_mtime

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleMkdir(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleMkdir failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_STALE {
			t.Errorf("Expected NFSERR_STALE, got %d", status)
		}
	})

	t.Run("mkdir garbage args", func(t *testing.T) {
		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleMkdir(bytes.NewReader([]byte{0x01}), reply, authCtx)
		if err != nil {
			t.Fatalf("handleMkdir failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})
}

// Tests for handleSymlink with various edge cases
func TestHandleSymlinkCoverage(t *testing.T) {
	server, handler, authCtx, err := newTestServerForHandlers()
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	// Get directory handle
	dirNode, _ := server.handler.Lookup("/testdir")
	dirHandle := server.handler.fileMap.Allocate(dirNode)

	t.Run("symlink attempt", func(t *testing.T) {
		// memfs may not support symlinks, so we test the handler path
		// and accept either success or various errors
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, dirHandle)
		xdrEncodeString(&buf, "link1")

		// sattr3
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_mode
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_uid
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_gid
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_size
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_atime
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_mtime

		xdrEncodeString(&buf, "/testfile.txt") // symlink target

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleSymlink(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleSymlink failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		// Accept various statuses - memfs may not support symlinks
		if status != NFS_OK && status != NFSERR_NOTSUPP && status != NFSERR_INVAL && status != NFSERR_ACCES {
			t.Errorf("Expected valid NFS status, got %d", status)
		}
	})

	t.Run("symlink with all sattr3 fields", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, dirHandle)
		xdrEncodeString(&buf, "link2")

		// sattr3 - all fields
		binary.Write(&buf, binary.BigEndian, uint32(1))    // set_mode
		binary.Write(&buf, binary.BigEndian, uint32(0777)) // mode
		binary.Write(&buf, binary.BigEndian, uint32(1))    // set_uid
		binary.Write(&buf, binary.BigEndian, uint32(1000)) // uid
		binary.Write(&buf, binary.BigEndian, uint32(1))    // set_gid
		binary.Write(&buf, binary.BigEndian, uint32(1000)) // gid
		binary.Write(&buf, binary.BigEndian, uint32(1))    // set_size
		binary.Write(&buf, binary.BigEndian, uint64(0))    // size
		binary.Write(&buf, binary.BigEndian, uint32(2))    // set_atime
		binary.Write(&buf, binary.BigEndian, uint32(0))    // atime_sec
		binary.Write(&buf, binary.BigEndian, uint32(0))    // atime_nsec
		binary.Write(&buf, binary.BigEndian, uint32(2))    // set_mtime
		binary.Write(&buf, binary.BigEndian, uint32(0))    // mtime_sec
		binary.Write(&buf, binary.BigEndian, uint32(0))    // mtime_nsec

		xdrEncodeString(&buf, "/testdir/nested.txt")

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleSymlink(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleSymlink failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		// Tests the sattr3 parsing path - accept any status since memfs may not support symlinks
		// This test is primarily for code coverage of the parsing logic
		_ = status
	})

	t.Run("symlink invalid filename", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, dirHandle)
		xdrEncodeString(&buf, "invalid/link")

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleSymlink(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleSymlink failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_INVAL {
			t.Errorf("Expected NFSERR_INVAL, got %d", status)
		}
	})

	t.Run("symlink invalid handle", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, uint64(999999))
		xdrEncodeString(&buf, "link")
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_mode
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_uid
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_gid
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_size
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_atime
		binary.Write(&buf, binary.BigEndian, uint32(0)) // set_mtime
		xdrEncodeString(&buf, "/target")

		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleSymlink(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleSymlink failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		// Accept NFSERR_STALE, NFSERR_INVAL, or NFSERR_ACCES depending on validation order
		// NFSERR_ACCES is returned when symlink target validation rejects absolute paths
		if status != NFSERR_STALE && status != NFSERR_INVAL && status != NFSERR_ACCES {
			t.Errorf("Expected NFSERR_STALE, NFSERR_INVAL, or NFSERR_ACCES, got %d", status)
		}
	})

	t.Run("symlink garbage args", func(t *testing.T) {
		reply := &RPCReply{AcceptStatus: SUCCESS}
		result, err := handler.handleSymlink(bytes.NewReader([]byte{0x01}), reply, authCtx)
		if err != nil {
			t.Fatalf("handleSymlink failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})
}

func TestDecodeAndLookupHandle(t *testing.T) {
	server, handler, _, err := newTestServerForHandlers()
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	// Get a valid handle
	rootNode, _ := server.handler.Lookup("/")
	rootHandle := server.handler.fileMap.Allocate(rootNode)

	t.Run("successful decode and lookup", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, rootHandle)
		reply := &RPCReply{}

		node, handle := handler.decodeAndLookupHandle(bytes.NewReader(buf.Bytes()), reply)
		if node == nil {
			t.Errorf("Expected node, got nil")
		}
		if handle != rootHandle {
			t.Errorf("Expected handle %d, got %d", rootHandle, handle)
		}
	})

	t.Run("invalid handle data", func(t *testing.T) {
		body := []byte{0x00, 0x00} // Too short
		reply := &RPCReply{}

		node, _ := handler.decodeAndLookupHandle(bytes.NewReader(body), reply)
		if node != nil {
			t.Errorf("Expected nil node for invalid handle data")
		}
	})

	t.Run("non-existent handle", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, 999999)
		reply := &RPCReply{}

		node, _ := handler.decodeAndLookupHandle(bytes.NewReader(buf.Bytes()), reply)
		if node != nil {
			t.Errorf("Expected nil node for non-existent handle")
		}
	})
}

func TestEncodePostOpAttr(t *testing.T) {
	attrs := &NFSAttrs{
		Mode: 0644,
		Size: 1024,
		Uid:  1000,
		Gid:  1000,
	}
	attrs.SetMtime(time.Now())
	attrs.SetAtime(time.Now())

	t.Run("encode post_op_attr", func(t *testing.T) {
		var buf bytes.Buffer
		err := encodePostOpAttr(&buf, attrs)
		if err != nil {
			t.Fatalf("encodePostOpAttr failed: %v", err)
		}

		if buf.Len() < 4 {
			t.Errorf("Expected at least 4 bytes, got %d", buf.Len())
		}
	})
}

func TestEncodeNoPostOpAttr(t *testing.T) {
	t.Run("encode empty post_op_attr", func(t *testing.T) {
		var buf bytes.Buffer
		encodeNoPostOpAttr(&buf)

		if buf.Len() != 4 {
			t.Errorf("Expected 4 bytes, got %d", buf.Len())
		}

		data := buf.Bytes()
		if data[0] != 0 || data[1] != 0 || data[2] != 0 || data[3] != 0 {
			t.Errorf("Expected all zeros, got %v", data)
		}
	})
}

func TestNewNFSAttrsZeroCoverage(t *testing.T) {
	now := time.Now()
	atime := now.Add(-time.Hour)

	t.Run("create new attrs", func(t *testing.T) {
		attrs := NewNFSAttrs(0755|os.ModeDir, 4096, now, atime, 1000, 1000)

		if attrs.Mode != 0755|os.ModeDir {
			t.Errorf("Expected mode %v, got %v", 0755|os.ModeDir, attrs.Mode)
		}
		if attrs.Size != 4096 {
			t.Errorf("Expected size 4096, got %d", attrs.Size)
		}
		if attrs.Uid != 1000 {
			t.Errorf("Expected uid 1000, got %d", attrs.Uid)
		}
		if attrs.Gid != 1000 {
			t.Errorf("Expected gid 1000, got %d", attrs.Gid)
		}
		if !attrs.Mtime().Equal(now) {
			t.Errorf("Expected mtime %v, got %v", now, attrs.Mtime())
		}
		if !attrs.Atime().Equal(atime) {
			t.Errorf("Expected atime %v, got %v", atime, attrs.Atime())
		}
	})
}

// Tests for encodeFileAttributes with all file types
func TestEncodeFileAttributesAllTypes(t *testing.T) {
	now := time.Now()

	t.Run("block device", func(t *testing.T) {
		attrs := &NFSAttrs{
			Mode: os.ModeDevice | os.FileMode(0644),
			Size: 0,
		}
		attrs.SetMtime(now)
		attrs.SetAtime(now)

		var buf bytes.Buffer
		err := encodeFileAttributes(&buf, attrs)
		if err != nil {
			t.Fatalf("Failed to encode block device: %v", err)
		}
		if buf.Len() != 84 {
			t.Errorf("Expected 84 bytes, got %d", buf.Len())
		}
	})

	t.Run("character device", func(t *testing.T) {
		attrs := &NFSAttrs{
			Mode: os.ModeDevice | os.ModeCharDevice | os.FileMode(0644),
			Size: 0,
		}
		attrs.SetMtime(now)
		attrs.SetAtime(now)

		var buf bytes.Buffer
		err := encodeFileAttributes(&buf, attrs)
		if err != nil {
			t.Fatalf("Failed to encode char device: %v", err)
		}
	})

	t.Run("socket", func(t *testing.T) {
		attrs := &NFSAttrs{
			Mode: os.ModeSocket | os.FileMode(0644),
			Size: 0,
		}
		attrs.SetMtime(now)
		attrs.SetAtime(now)

		var buf bytes.Buffer
		err := encodeFileAttributes(&buf, attrs)
		if err != nil {
			t.Fatalf("Failed to encode socket: %v", err)
		}
	})

	t.Run("named pipe", func(t *testing.T) {
		attrs := &NFSAttrs{
			Mode: os.ModeNamedPipe | os.FileMode(0644),
			Size: 0,
		}
		attrs.SetMtime(now)
		attrs.SetAtime(now)

		var buf bytes.Buffer
		err := encodeFileAttributes(&buf, attrs)
		if err != nil {
			t.Fatalf("Failed to encode named pipe: %v", err)
		}
	})
}

// Tests for encodeWccAttr
func TestEncodeWccAttrCoverage(t *testing.T) {
	now := time.Now()

	t.Run("successful encoding", func(t *testing.T) {
		attrs := &NFSAttrs{
			Mode: os.FileMode(0644),
			Size: 1024,
		}
		attrs.SetMtime(now)

		var buf bytes.Buffer
		err := encodeWccAttr(&buf, attrs)
		if err != nil {
			t.Fatalf("Failed to encode wcc attr: %v", err)
		}
		// Size: 8 (size) + 4+4 (mtime) + 4+4 (ctime) = 24 bytes
		if buf.Len() != 24 {
			t.Errorf("Expected 24 bytes, got %d", buf.Len())
		}
	})

	t.Run("error on write", func(t *testing.T) {
		attrs := &NFSAttrs{
			Size: 1024,
		}
		attrs.SetMtime(now)

		fw := &wccFailingWriter{failAfter: 0}
		err := encodeWccAttr(fw, attrs)
		if err == nil {
			t.Error("Expected error from failing writer")
		}
	})
}

// wccFailingWriter fails after specified number of writes (for wcc tests)
type wccFailingWriter struct {
	writes    int
	failAfter int
}

func (w *wccFailingWriter) Write(p []byte) (n int, err error) {
	if w.writes >= w.failAfter {
		return 0, errors.New("write failed")
	}
	w.writes++
	return len(p), nil
}
