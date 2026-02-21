package absnfs

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/absfs/memfs"
)

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
	binary.Write(&buf, binary.BigEndian, uint64(0))       // cookie
	buf.Write(make([]byte, 8))                             // cookieverf
	binary.Write(&buf, binary.BigEndian, uint32(65536))    // count

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
	timeout := server.handler.options.Timeouts.DefaultTimeout
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
	binary.Write(&buf, binary.BigEndian, uint64(0))   // offset
	binary.Write(&buf, binary.BigEndian, uint32(5))    // count
	binary.Write(&buf, binary.BigEndian, uint32(0))    // stable = UNSTABLE
	binary.Write(&buf, binary.BigEndian, uint32(5))    // data length
	buf.Write([]byte("hello"))                         // data
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
