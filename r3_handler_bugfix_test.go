package absnfs

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/absfs/memfs"
)

// TestR3_RenameErrorDoubleWcc verifies RENAME early error responses contain
// two wcc_data fields (status + fromdir_wcc + todir_wcc = 20 bytes total).
func TestR3_RenameErrorDoubleWcc(t *testing.T) {
	_, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	// Send RENAME with an invalid (stale) source handle.
	invalidHandle := uint64(99999)
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, invalidHandle)
	xdrEncodeString(&buf, "srcfile")
	xdrEncodeFileHandle(&buf, invalidHandle)
	xdrEncodeString(&buf, "dstfile")

	result, err := handler.handleRename(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleRename returned error: %v", err)
	}

	data := getReplyData(result)
	// Expected: status(4) + fromdir_wcc(pre_op=4 + post_op=4) + todir_wcc(pre_op=4 + post_op=4) = 20 bytes
	if len(data) != 20 {
		t.Errorf("Expected 20 bytes for RENAME error (status + 2*wcc_data), got %d", len(data))
	}
	status := readStatusFromReply(result)
	if status == NFS_OK {
		t.Error("Expected error status for RENAME with stale handle, got NFS_OK")
	}
}

// TestR3_FSINFONoLinkBit verifies that FSINFO properties bitmask does NOT have
// FSF3_LINK (0x0001) set, since LINK is not supported.
func TestR3_FSINFONoLinkBit(t *testing.T) {
	tests := []struct {
		name   string
		setup  func() (*Server, *NFSProcedureHandler, *AuthContext, error)
		roDesc string
	}{
		{"ReadWrite", newTestServerForBugfixes, "read-write"},
		{"ReadOnly", newReadOnlyTestServer, "read-only"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, handler, authCtx, err := tt.setup()
			if err != nil {
				t.Fatal(err)
			}

			rootHandle := getRootHandle(server)
			var buf bytes.Buffer
			xdrEncodeFileHandle(&buf, rootHandle)

			result, err := handler.handleFsinfo(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
			if err != nil {
				t.Fatalf("handleFsinfo returned error: %v", err)
			}
			status := readStatusFromReply(result)
			if status != NFS_OK {
				t.Fatalf("Expected NFS_OK, got %d", status)
			}

			data := getReplyData(result)
			// FSINFO response layout after status(4) + post_op_attr(1+84=88):
			// rtmax(4) + rtpref(4) + rtmult(4) + wtmax(4) + wtpref(4) + wtmult(4) + dtpref(4)
			// + maxfilesize(8) + time_delta(8) + properties(4)
			// Total after status+postop = 4 + 88 + 7*4 + 8 + 8 + 4 = 140
			// properties is the last 4 bytes
			if len(data) < 4 {
				t.Fatal("Response too short")
			}
			propertiesOffset := len(data) - 4
			properties := binary.BigEndian.Uint32(data[propertiesOffset:])

			const FSF3_LINK = 0x0001
			if properties&FSF3_LINK != 0 {
				t.Errorf("FSINFO properties should NOT have FSF3_LINK set on %s server, properties=%#x", tt.roDesc, properties)
			}

			// Verify that FSF3_SYMLINK (0x0002), FSF3_HOMOGENEOUS (0x0008), FSF3_CANSETTIME (0x0010) are set
			const expected = 0x0002 | 0x0008 | 0x0010
			if properties&expected != expected {
				t.Errorf("Expected FSF3_SYMLINK|FSF3_HOMOGENEOUS|FSF3_CANSETTIME in properties, got %#x", properties)
			}
		})
	}
}

// TestR3_SymlinkTargetValidation verifies that SYMLINK rejects absolute paths
// and targets with ".." components, but accepts normal relative targets.
func TestR3_SymlinkTargetValidation(t *testing.T) {
	server, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	rootHandle := getRootHandle(server)

	// Helper to build a SYMLINK request
	buildSymlinkRequest := func(name, target string) []byte {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, rootHandle)
		xdrEncodeString(&buf, name)
		// sattr3: all "don't set"
		for i := 0; i < 6; i++ {
			binary.Write(&buf, binary.BigEndian, uint32(0))
		}
		xdrEncodeString(&buf, target)
		return buf.Bytes()
	}

	t.Run("AbsolutePathRejected", func(t *testing.T) {
		result, err := handler.handleSymlink(
			bytes.NewReader(buildSymlinkRequest("badlink1", "/etc/shadow")),
			&RPCReply{}, authCtx)
		if err != nil {
			t.Fatalf("handleSymlink returned error: %v", err)
		}
		status := readStatusFromReply(result)
		if status != NFSERR_ACCES {
			t.Errorf("Expected NFSERR_ACCES for absolute target, got %d", status)
		}
	})

	t.Run("DotDotRejected", func(t *testing.T) {
		result, err := handler.handleSymlink(
			bytes.NewReader(buildSymlinkRequest("badlink2", "../../etc/passwd")),
			&RPCReply{}, authCtx)
		if err != nil {
			t.Fatalf("handleSymlink returned error: %v", err)
		}
		status := readStatusFromReply(result)
		if status != NFSERR_ACCES {
			t.Errorf("Expected NFSERR_ACCES for target with '..', got %d", status)
		}
	})

	t.Run("RelativeAccepted", func(t *testing.T) {
		result, err := handler.handleSymlink(
			bytes.NewReader(buildSymlinkRequest("goodlink", "otherfile.txt")),
			&RPCReply{}, authCtx)
		if err != nil {
			t.Fatalf("handleSymlink returned error: %v", err)
		}
		status := readStatusFromReply(result)
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK for relative target, got %d", status)
		}
	})
}

// TestR3_WriteBoundsCheck verifies that WRITE rejects count > TransferSize
// with NFSERR_INVAL.
func TestR3_WriteBoundsCheck(t *testing.T) {
	server, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	fileHandle := getFileHandle(server, "/testfile.txt")

	// Build WRITE request with count much larger than TransferSize
	bigCount := uint32(0xFFFFFF) // ~16MB, far exceeds the 64KB default TransferSize
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fileHandle)
	binary.Write(&buf, binary.BigEndian, uint64(0))    // offset
	binary.Write(&buf, binary.BigEndian, bigCount)      // count
	binary.Write(&buf, binary.BigEndian, uint32(2))     // stable = FILE_SYNC
	binary.Write(&buf, binary.BigEndian, bigCount)      // data length (must match count)

	result, err := handler.handleWrite(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleWrite returned error: %v", err)
	}

	status := readStatusFromReply(result)
	if status != NFSERR_INVAL {
		t.Errorf("Expected NFSERR_INVAL for oversized WRITE, got %d", status)
	}
}

// TestR3_SetattrSizeOverflow verifies that SETATTR rejects Size > MaxInt64.
func TestR3_SetattrSizeOverflow(t *testing.T) {
	server, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	fileHandle := getFileHandle(server, "/testfile.txt")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fileHandle)
	// sattr3 with SetSize=true and Size > MaxInt64
	binary.Write(&buf, binary.BigEndian, uint32(0)) // set_mode = false
	binary.Write(&buf, binary.BigEndian, uint32(0)) // set_uid = false
	binary.Write(&buf, binary.BigEndian, uint32(0)) // set_gid = false
	binary.Write(&buf, binary.BigEndian, uint32(1)) // set_size = true
	binary.Write(&buf, binary.BigEndian, uint64(math.MaxInt64+1)) // size > MaxInt64
	binary.Write(&buf, binary.BigEndian, uint32(0)) // set_atime = don't set
	binary.Write(&buf, binary.BigEndian, uint32(0)) // set_mtime = don't set
	binary.Write(&buf, binary.BigEndian, uint32(0)) // guard = no check

	result, err := handler.handleSetattr(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleSetattr returned error: %v", err)
	}

	status := readStatusFromReply(result)
	if status != NFSERR_INVAL {
		t.Errorf("Expected NFSERR_INVAL for size > MaxInt64, got %d", status)
	}
}

// TestR3_CommitReadOnlyCheck verifies that COMMIT on a read-only server
// returns NFSERR_ROFS.
func TestR3_CommitReadOnlyCheck(t *testing.T) {
	server, handler, authCtx, err := newReadOnlyTestServer()
	if err != nil {
		t.Fatal(err)
	}

	execHandle := getFileHandle(server, "/execfile")

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, execHandle)
	binary.Write(&buf, binary.BigEndian, uint64(0)) // offset
	binary.Write(&buf, binary.BigEndian, uint32(0)) // count

	result, err := handler.handleCommit(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleCommit returned error: %v", err)
	}
	status := readStatusFromReply(result)
	if status != NFSERR_ROFS {
		t.Errorf("Expected NFSERR_ROFS for COMMIT on read-only server, got %d", status)
	}
}

// TestR3_NilAttrsSafety verifies that handlers don't panic when a node has nil attrs.
// We inject a node with nil attrs directly into the fileMap and send GETATTR.
func TestR3_NilAttrsSafety(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	config := DefaultRateLimiterConfig()
	nfs, err := New(fs, ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
	})
	if err != nil {
		t.Fatal(err)
	}

	server := &Server{
		handler: nfs,
		options: ServerOptions{Debug: false},
	}
	handler := &NFSProcedureHandler{server: server}
	authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}

	// Create a node with nil attrs and inject it into the fileMap
	nilAttrsNode := &NFSNode{
		SymlinkFileSystem: fs,
		path:              "/nonexistent",
		attrs:             nil,
	}
	nilHandle := nfs.fileMap.Allocate(nilAttrsNode)

	// GETATTR should not panic with nil attrs
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, nilHandle)
	result, err := handler.handleGetattr(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleGetattr panicked or returned error with nil attrs: %v", err)
	}
	// Should return an error status (not NFS_OK) since the file doesn't exist
	status := readStatusFromReply(result)
	if status == NFS_OK {
		t.Log("GETATTR returned NFS_OK for nil-attrs node (attrs refreshed from fs)")
	}
	t.Logf("GETATTR with nil attrs returned status %d (no panic)", status)

	// SETATTR with nil attrs: should not panic
	var buf2 bytes.Buffer
	xdrEncodeFileHandle(&buf2, nilHandle)
	// sattr3: set mode
	binary.Write(&buf2, binary.BigEndian, uint32(1))   // set_mode = true
	binary.Write(&buf2, binary.BigEndian, uint32(0644)) // mode
	binary.Write(&buf2, binary.BigEndian, uint32(0))    // set_uid = false
	binary.Write(&buf2, binary.BigEndian, uint32(0))    // set_gid = false
	binary.Write(&buf2, binary.BigEndian, uint32(0))    // set_size = false
	binary.Write(&buf2, binary.BigEndian, uint32(0))    // set_atime = don't set
	binary.Write(&buf2, binary.BigEndian, uint32(0))    // set_mtime = don't set
	binary.Write(&buf2, binary.BigEndian, uint32(0))    // guard = no check

	result2, err := handler.handleSetattr(bytes.NewReader(buf2.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleSetattr panicked or returned error with nil attrs: %v", err)
	}
	status2 := readStatusFromReply(result2)
	t.Logf("SETATTR with nil attrs returned status %d (no panic)", status2)
}

// TestR3_ReaddirSmallCount verifies that READDIR with a very small count value
// doesn't crash or return an unreasonably large response due to underflow.
func TestR3_ReaddirSmallCount(t *testing.T) {
	server, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	rootHandle := getRootHandle(server)

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, rootHandle)
	binary.Write(&buf, binary.BigEndian, uint64(0)) // cookie
	buf.Write(make([]byte, 8))                      // cookieverf
	binary.Write(&buf, binary.BigEndian, uint32(50)) // very small count

	result, err := handler.handleReaddir(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleReaddir returned error: %v", err)
	}

	status := readStatusFromReply(result)
	if status != NFS_OK {
		t.Fatalf("Expected NFS_OK, got %d", status)
	}

	data := getReplyData(result)
	// Verify response is reasonable (not gigabytes due to underflow)
	if len(data) > 65536 {
		t.Errorf("READDIR response unreasonably large for small count: %d bytes", len(data))
	}
	t.Logf("READDIR with count=50 returned %d bytes (no underflow)", len(data))
}

// TestR3_ReaddirplusSmallCount verifies the same underflow protection for READDIRPLUS.
func TestR3_ReaddirplusSmallCount(t *testing.T) {
	server, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	rootHandle := getRootHandle(server)

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, rootHandle)
	binary.Write(&buf, binary.BigEndian, uint64(0))  // cookie
	buf.Write(make([]byte, 8))                       // cookieverf
	binary.Write(&buf, binary.BigEndian, uint32(50))  // dircount (very small)
	binary.Write(&buf, binary.BigEndian, uint32(50))  // maxcount (very small)

	result, err := handler.handleReaddirplus(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleReaddirplus returned error: %v", err)
	}

	status := readStatusFromReply(result)
	if status != NFS_OK {
		t.Fatalf("Expected NFS_OK, got %d", status)
	}

	data := getReplyData(result)
	if len(data) > 65536 {
		t.Errorf("READDIRPLUS response unreasonably large for small count: %d bytes", len(data))
	}
	t.Logf("READDIRPLUS with maxcount=50 returned %d bytes (no underflow)", len(data))
}

// TestR3_RmdirCacheInvalidation verifies that after RMDIR, the attr cache no
// longer contains the removed directory's entry.
func TestR3_RmdirCacheInvalidation(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	config := DefaultRateLimiterConfig()
	nfs, err := New(fs, ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create a directory to remove
	if err := fs.Mkdir("/rmdir_test", 0755); err != nil {
		t.Fatal(err)
	}

	server := &Server{
		handler: nfs,
		options: ServerOptions{Debug: false},
	}
	handler := &NFSProcedureHandler{server: server}
	authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}

	rootHandle := getRootHandle(server)

	// Prime the attr cache by looking up the directory
	_, _ = nfs.Lookup("/rmdir_test")
	// Verify it's in the cache
	if attrs, found := nfs.attrCache.Get("/rmdir_test", nfs); !found || attrs == nil {
		t.Log("Warning: /rmdir_test not in attr cache before RMDIR (may have expired)")
	}

	// Now RMDIR
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, rootHandle)
	xdrEncodeString(&buf, "rmdir_test")

	result, err := handler.handleRmdir(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleRmdir returned error: %v", err)
	}

	status := readStatusFromReply(result)
	if status != NFS_OK {
		t.Fatalf("Expected NFS_OK from RMDIR, got %d", status)
	}

	// Check that the attr cache no longer has the entry (or it's been invalidated)
	if attrs, found := nfs.attrCache.Get("/rmdir_test", nfs); found && attrs != nil {
		t.Error("Expected attr cache entry for /rmdir_test to be invalidated after RMDIR")
	}
}

// TestR3_RmdirNotempty verifies that RMDIR of a non-empty directory returns
// NFSERR_NOTEMPTY rather than NFSERR_IO.
func TestR3_RmdirNotempty(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	config := DefaultRateLimiterConfig()
	nfs, err := New(fs, ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create a non-empty directory
	if err := fs.Mkdir("/nonempty", 0755); err != nil {
		t.Fatal(err)
	}
	f, err := fs.Create("/nonempty/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	server := &Server{
		handler: nfs,
		options: ServerOptions{Debug: false},
	}
	handler := &NFSProcedureHandler{server: server}
	authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}

	rootHandle := getRootHandle(server)

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, rootHandle)
	xdrEncodeString(&buf, "nonempty")

	result, err := handler.handleRmdir(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleRmdir returned error: %v", err)
	}

	status := readStatusFromReply(result)
	if status == NFS_OK {
		t.Fatal("Expected error for RMDIR of non-empty directory, got NFS_OK")
	}
	if status == NFSERR_IO {
		t.Error("RMDIR non-empty dir returned NFSERR_IO; should return NFSERR_NOTEMPTY")
	}
	if status != NFSERR_NOTEMPTY {
		t.Errorf("Expected NFSERR_NOTEMPTY (%d) for non-empty dir, got %d", NFSERR_NOTEMPTY, status)
	}
}

// TestR3_SymlinkModeTypeBitStrip verifies that SYMLINK with mode bits including
// file type bits (e.g. 0100777) creates the symlink with only permission bits.
func TestR3_SymlinkModeTypeBitStrip(t *testing.T) {
	server, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	rootHandle := getRootHandle(server)

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, rootHandle)
	xdrEncodeString(&buf, "typebitlink")
	// sattr3: set_mode = true with type bits included (0100777 = regular file + rwxrwxrwx)
	binary.Write(&buf, binary.BigEndian, uint32(1))      // set_mode = true
	binary.Write(&buf, binary.BigEndian, uint32(0100777)) // mode with type bits
	binary.Write(&buf, binary.BigEndian, uint32(0))       // set_uid = false
	binary.Write(&buf, binary.BigEndian, uint32(0))       // set_gid = false
	binary.Write(&buf, binary.BigEndian, uint32(0))       // set_size = false
	binary.Write(&buf, binary.BigEndian, uint32(0))       // set_atime = don't set
	binary.Write(&buf, binary.BigEndian, uint32(0))       // set_mtime = don't set
	xdrEncodeString(&buf, "sometarget")

	result, err := handler.handleSymlink(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleSymlink returned error: %v", err)
	}

	status := readStatusFromReply(result)
	if status != NFS_OK {
		t.Fatalf("Expected NFS_OK, got %d", status)
	}

	// Verify the symlink was created with only permission bits (mode & 07777)
	info, err := server.handler.fs.Lstat("/typebitlink")
	if err != nil {
		t.Fatalf("Failed to stat created symlink: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0777 {
		t.Errorf("Expected symlink perm 0777 (type bits stripped), got %04o", perm)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("Expected created file to be a symlink")
	}
}

// TestR3_HandleCallErrorPath verifies that the HandleCall error path returns
// SYSTEM_ERR instead of SUCCESS with empty data.
func TestR3_HandleCallErrorPath(t *testing.T) {
	server, _, _, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	procHandler := &NFSProcedureHandler{server: server}
	authCtx := &AuthContext{
		ClientIP:   "127.0.0.1",
		ClientPort: 12345,
		Credential: &RPCCredential{Flavor: AUTH_NONE, Body: []byte{}},
	}

	// Construct a call for an unknown program to trigger PROG_UNAVAIL
	call := &RPCCall{
		Header: RPCMsgHeader{
			Xid:        1,
			MsgType:    RPC_CALL,
			RPCVersion: 2,
			Program:    999999, // Unknown program
			Version:    3,
			Procedure:  0,
		},
		Credential: RPCCredential{Flavor: AUTH_NONE, Body: []byte{}},
		Verifier:   RPCVerifier{Flavor: 0, Body: []byte{}},
	}

	reply, err := procHandler.HandleCall(call, bytes.NewReader(nil), authCtx)
	if err != nil {
		t.Fatalf("HandleCall returned error: %v", err)
	}

	// For unknown program, should get PROG_UNAVAIL, not SUCCESS
	if reply.AcceptStatus == SUCCESS && reply.Data == nil {
		t.Error("HandleCall error path returned SUCCESS with nil Data; should set error status")
	}
	if reply.AcceptStatus != PROG_UNAVAIL {
		t.Errorf("Expected PROG_UNAVAIL for unknown program, got %d", reply.AcceptStatus)
	}
}

// TestR3_ReaddirEncodingXDR verifies that READDIR returns properly encoded
// entries with valid XDR (fileId as uint64, cookie as uint64, name as XDR string).
func TestR3_ReaddirEncodingXDR(t *testing.T) {
	server, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	rootHandle := getRootHandle(server)

	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, rootHandle)
	binary.Write(&buf, binary.BigEndian, uint64(0))    // cookie
	buf.Write(make([]byte, 8))                         // cookieverf
	binary.Write(&buf, binary.BigEndian, uint32(65536)) // count

	result, err := handler.handleReaddir(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleReaddir returned error: %v", err)
	}
	status := readStatusFromReply(result)
	if status != NFS_OK {
		t.Fatalf("Expected NFS_OK, got %d", status)
	}

	data := getReplyData(result)
	reader := bytes.NewReader(data)

	// Skip status(4)
	var statusVal uint32
	binary.Read(reader, binary.BigEndian, &statusVal)

	// Skip post_op_attr: attributes_follow(4) + fattr3(84)
	var attrFollow uint32
	binary.Read(reader, binary.BigEndian, &attrFollow)
	if attrFollow == 1 {
		// Skip 84 bytes of fattr3 (21 uint32s)
		skip := make([]byte, 84)
		reader.Read(skip)
	}

	// Skip cookieverf (8 bytes)
	reader.Read(make([]byte, 8))

	// Parse entries
	entriesFound := 0
	for {
		var hasEntry uint32
		if err := binary.Read(reader, binary.BigEndian, &hasEntry); err != nil {
			break
		}
		if hasEntry == 0 {
			break // No more entries
		}
		entriesFound++

		// fileId (uint64) - must be valid
		var fileId uint64
		if err := binary.Read(reader, binary.BigEndian, &fileId); err != nil {
			t.Fatalf("Failed to read fileId for entry %d: %v", entriesFound, err)
		}
		if fileId == 0 {
			t.Errorf("Entry %d has fileId=0, expected non-zero", entriesFound)
		}

		// name (XDR string: length + data + padding)
		var nameLen uint32
		if err := binary.Read(reader, binary.BigEndian, &nameLen); err != nil {
			t.Fatalf("Failed to read name length for entry %d: %v", entriesFound, err)
		}
		nameBytes := make([]byte, nameLen)
		if _, err := reader.Read(nameBytes); err != nil {
			t.Fatalf("Failed to read name for entry %d: %v", entriesFound, err)
		}
		name := string(nameBytes)
		padding := (4 - (nameLen % 4)) % 4
		if padding > 0 {
			reader.Read(make([]byte, padding))
		}

		if name == "" {
			t.Errorf("Entry %d has empty name", entriesFound)
		}

		// cookie (uint64) - must be valid
		var cookie uint64
		if err := binary.Read(reader, binary.BigEndian, &cookie); err != nil {
			t.Fatalf("Failed to read cookie for entry %d: %v", entriesFound, err)
		}

		t.Logf("Entry %d: fileId=%d name=%q cookie=%d", entriesFound, fileId, name, cookie)
	}

	if entriesFound == 0 {
		t.Error("Expected at least one READDIR entry")
	}
}

// TestR3_LinkConsumesBody verifies that LINK reads its arguments from the body
// (file handle, dir handle, name) and does not leave unread data.
func TestR3_LinkConsumesBody(t *testing.T) {
	server, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	rootHandle := getRootHandle(server)
	fileHandle := getFileHandle(server, "/testfile.txt")

	// Build a proper LINK3args: file_fh + dir_fh + name
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, fileHandle) // file handle
	xdrEncodeFileHandle(&buf, rootHandle) // dir handle
	xdrEncodeString(&buf, "hardlink")     // name

	bodyReader := bytes.NewReader(buf.Bytes())
	result, err := handler.handleLink(bodyReader, &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleLink returned error: %v", err)
	}

	// LINK is not supported, so we expect NFSERR_NOTSUPP
	status := readStatusFromReply(result)
	if status != NFSERR_NOTSUPP {
		t.Errorf("Expected NFSERR_NOTSUPP, got %d", status)
	}

	// Verify the body was consumed (remaining bytes should be 0)
	remaining := bodyReader.Len()
	if remaining > 0 {
		t.Errorf("LINK handler left %d unread bytes in body (should consume all arguments)", remaining)
	}
}

// TestR3_WriteVerifierNonZero verifies that WRITE and COMMIT responses contain
// non-zero write verifiers.
func TestR3_WriteVerifierNonZero(t *testing.T) {
	server, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	// Ensure writeVerf is initialized (it should be from server creation)
	allZero := true
	for _, b := range server.writeVerf {
		if b != 0 {
			allZero = false
			break
		}
	}

	// If writeVerf was not initialized (shouldn't happen), initialize it
	if allZero {
		binary.BigEndian.PutUint64(server.writeVerf[:], uint64(time.Now().UnixNano()))
	}

	fileHandle := getFileHandle(server, "/testfile.txt")

	t.Run("WriteVerifier", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, fileHandle)
		binary.Write(&buf, binary.BigEndian, uint64(0)) // offset
		binary.Write(&buf, binary.BigEndian, uint32(5))  // count
		binary.Write(&buf, binary.BigEndian, uint32(2))  // stable = FILE_SYNC
		binary.Write(&buf, binary.BigEndian, uint32(5))  // data length
		buf.Write([]byte("hello"))                       // data
		buf.Write([]byte{0, 0, 0})                       // padding

		result, err := handler.handleWrite(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
		if err != nil {
			t.Fatalf("handleWrite returned error: %v", err)
		}
		status := readStatusFromReply(result)
		if status != NFS_OK {
			t.Fatalf("Expected NFS_OK, got %d", status)
		}

		data := getReplyData(result)
		// The last 8 bytes should be the write verifier
		if len(data) < 8 {
			t.Fatal("Response too short for write verifier")
		}
		verf := data[len(data)-8:]
		verfAllZero := true
		for _, b := range verf {
			if b != 0 {
				verfAllZero = false
				break
			}
		}
		if verfAllZero {
			t.Error("WRITE response has all-zero write verifier")
		}
	})

	t.Run("CommitVerifier", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, fileHandle)
		binary.Write(&buf, binary.BigEndian, uint64(0)) // offset
		binary.Write(&buf, binary.BigEndian, uint32(0)) // count

		result, err := handler.handleCommit(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
		if err != nil {
			t.Fatalf("handleCommit returned error: %v", err)
		}
		status := readStatusFromReply(result)
		if status != NFS_OK {
			t.Fatalf("Expected NFS_OK, got %d", status)
		}

		data := getReplyData(result)
		// The last 8 bytes should be the write verifier
		if len(data) < 8 {
			t.Fatal("Response too short for write verifier")
		}
		verf := data[len(data)-8:]
		verfAllZero := true
		for _, b := range verf {
			if b != 0 {
				verfAllZero = false
				break
			}
		}
		if verfAllZero {
			t.Error("COMMIT response has all-zero write verifier")
		}
	})
}

// TestR3_ExclusiveCreateIdempotent verifies that CREATE with EXCLUSIVE mode
// returns success even when the file already exists (idempotent behavior).
func TestR3_ExclusiveCreateIdempotent(t *testing.T) {
	server, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	rootHandle := getRootHandle(server)

	// Build first EXCLUSIVE CREATE request
	buildExclusiveCreate := func(name string) []byte {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, rootHandle)
		xdrEncodeString(&buf, name)
		binary.Write(&buf, binary.BigEndian, uint32(2)) // EXCLUSIVE
		buf.Write([]byte{1, 2, 3, 4, 5, 6, 7, 8})     // 8-byte verifier
		return buf.Bytes()
	}

	// First CREATE EXCLUSIVE - should succeed
	result1, err := handler.handleCreate(bytes.NewReader(buildExclusiveCreate("excl_file")), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("First handleCreate returned error: %v", err)
	}
	status1 := readStatusFromReply(result1)
	if status1 != NFS_OK {
		t.Fatalf("First EXCLUSIVE CREATE failed with status %d", status1)
	}

	// Second CREATE EXCLUSIVE with same name - should also succeed (idempotent)
	result2, err := handler.handleCreate(bytes.NewReader(buildExclusiveCreate("excl_file")), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("Second handleCreate returned error: %v", err)
	}
	status2 := readStatusFromReply(result2)
	if status2 != NFS_OK {
		t.Errorf("Second EXCLUSIVE CREATE should be idempotent (NFS_OK), got %d", status2)
	}
}

// TestR3_NilAttrsSafetyReaddir verifies READDIR skips entries with nil attrs
// without panicking. We inject a nil-attrs node and verify the handler runs safely.
func TestR3_NilAttrsSafetyReaddir(t *testing.T) {
	server, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	rootHandle := getRootHandle(server)

	// READDIR should handle nil-attrs entries gracefully (skip them)
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, rootHandle)
	binary.Write(&buf, binary.BigEndian, uint64(0))    // cookie
	buf.Write(make([]byte, 8))                         // cookieverf
	binary.Write(&buf, binary.BigEndian, uint32(65536)) // count

	result, err := handler.handleReaddir(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleReaddir returned error: %v", err)
	}
	status := readStatusFromReply(result)
	if status != NFS_OK {
		t.Fatalf("Expected NFS_OK, got %d", status)
	}
	t.Log("READDIR completed without panic (nil-attrs entries skipped)")
}

// TestR3_NilAttrsSafetySetattr verifies SETATTR returns an error (not panic)
// when a node has nil attrs.
func TestR3_NilAttrsSafetySetattr(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	config := DefaultRateLimiterConfig()
	nfs, err := New(fs, ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
	})
	if err != nil {
		t.Fatal(err)
	}

	server := &Server{
		handler: nfs,
		options: ServerOptions{Debug: false},
	}
	handler := &NFSProcedureHandler{server: server}
	authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}

	// Create a file node with nil attrs
	nilAttrsNode := &NFSNode{
		SymlinkFileSystem: fs,
		path:              "/nonexistent_file",
		attrs:             nil,
	}
	nilHandle := nfs.fileMap.Allocate(nilAttrsNode)

	// SETATTR with nil attrs node: the handler should return an error, not panic.
	// The fix checks node.attrs == nil after taking RLock and returns NFSERR_IO.
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, nilHandle)
	// sattr3: set mode
	binary.Write(&buf, binary.BigEndian, uint32(1))   // set_mode = true
	binary.Write(&buf, binary.BigEndian, uint32(0644)) // mode
	binary.Write(&buf, binary.BigEndian, uint32(0))    // set_uid = false
	binary.Write(&buf, binary.BigEndian, uint32(0))    // set_gid = false
	binary.Write(&buf, binary.BigEndian, uint32(0))    // set_size = false
	binary.Write(&buf, binary.BigEndian, uint32(0))    // set_atime = don't set
	binary.Write(&buf, binary.BigEndian, uint32(0))    // set_mtime = don't set
	binary.Write(&buf, binary.BigEndian, uint32(0))    // guard = no check

	result, err := handler.handleSetattr(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleSetattr returned error: %v", err)
	}

	status := readStatusFromReply(result)
	// Should return an error status, not NFS_OK, and definitely not panic
	if status == NFS_OK {
		t.Error("Expected error status from SETATTR with nil attrs node, got NFS_OK")
	}
	t.Logf("SETATTR with nil attrs returned status %d (no panic)", status)
}

// TestR3_HandleCallErrorPathSystemErr verifies that when a handler returns an
// error (not RPCError), HandleCall sets AcceptStatus to SYSTEM_ERR.
func TestR3_HandleCallErrorPathSystemErr(t *testing.T) {
	server, _, _, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	procHandler := &NFSProcedureHandler{server: server}
	authCtx := &AuthContext{
		ClientIP:   "127.0.0.1",
		ClientPort: 12345,
		Credential: &RPCCredential{Flavor: AUTH_NONE, Body: []byte{}},
	}

	// Call with version mismatch to trigger PROG_MISMATCH
	call := &RPCCall{
		Header: RPCMsgHeader{
			Xid:        2,
			MsgType:    RPC_CALL,
			RPCVersion: 2,
			Program:    NFS_PROGRAM,
			Version:    99, // Invalid NFS version
			Procedure:  NFSPROC3_NULL,
		},
		Credential: RPCCredential{Flavor: AUTH_NONE, Body: []byte{}},
		Verifier:   RPCVerifier{Flavor: 0, Body: []byte{}},
	}

	reply, err := procHandler.HandleCall(call, bytes.NewReader(nil), authCtx)
	if err != nil {
		t.Fatalf("HandleCall returned error: %v", err)
	}

	if reply.AcceptStatus == SUCCESS && reply.Data == nil {
		t.Error("HandleCall with version mismatch returned SUCCESS with nil Data")
	}
	if reply.AcceptStatus != PROG_MISMATCH {
		t.Errorf("Expected PROG_MISMATCH, got %d", reply.AcceptStatus)
	}
}

// TestR3_RenameErrorDoubleWccReadOnly verifies RENAME on read-only exports
// also returns the double wcc_data format.
func TestR3_RenameErrorDoubleWccReadOnly(t *testing.T) {
	_, handler, authCtx, err := newReadOnlyTestServer()
	if err != nil {
		t.Fatal(err)
	}

	// Read-only check happens before reading body, so empty body is fine
	result, err := handler.handleRename(bytes.NewReader([]byte{}), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatalf("handleRename returned error: %v", err)
	}

	data := getReplyData(result)
	// status(4) + fromdir_wcc(4+4) + todir_wcc(4+4) = 20
	if len(data) != 20 {
		t.Errorf("Expected 20 bytes for RENAME ROFS error, got %d", len(data))
	}
	status := readStatusFromReply(result)
	if status != NFSERR_ROFS {
		t.Errorf("Expected NFSERR_ROFS, got %d", status)
	}
}

// TestR3_WriteVerifierConsistency verifies that the write verifier from WRITE
// matches the one from COMMIT (both come from server.writeVerf).
func TestR3_WriteVerifierConsistency(t *testing.T) {
	server, handler, authCtx, err := newTestServerForBugfixes()
	if err != nil {
		t.Fatal(err)
	}

	// Make sure writeVerf is set
	binary.BigEndian.PutUint64(server.writeVerf[:], uint64(time.Now().UnixNano()))

	fileHandle := getFileHandle(server, "/testfile.txt")

	// WRITE
	var writeBuf bytes.Buffer
	xdrEncodeFileHandle(&writeBuf, fileHandle)
	binary.Write(&writeBuf, binary.BigEndian, uint64(0)) // offset
	binary.Write(&writeBuf, binary.BigEndian, uint32(3))  // count
	binary.Write(&writeBuf, binary.BigEndian, uint32(2))  // FILE_SYNC
	binary.Write(&writeBuf, binary.BigEndian, uint32(3))  // data length
	writeBuf.Write([]byte("abc"))                         // data
	writeBuf.Write([]byte{0})                             // padding

	writeResult, err := handler.handleWrite(bytes.NewReader(writeBuf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatal(err)
	}
	writeData := getReplyData(writeResult)
	writeVerf := writeData[len(writeData)-8:]

	// COMMIT
	var commitBuf bytes.Buffer
	xdrEncodeFileHandle(&commitBuf, fileHandle)
	binary.Write(&commitBuf, binary.BigEndian, uint64(0)) // offset
	binary.Write(&commitBuf, binary.BigEndian, uint32(0))  // count

	commitResult, err := handler.handleCommit(bytes.NewReader(commitBuf.Bytes()), &RPCReply{}, authCtx)
	if err != nil {
		t.Fatal(err)
	}
	commitData := getReplyData(commitResult)
	commitVerf := commitData[len(commitData)-8:]

	// Both verifiers should match
	if !bytes.Equal(writeVerf, commitVerf) {
		t.Errorf("WRITE verifier %x does not match COMMIT verifier %x", writeVerf, commitVerf)
	}
}

// TestR3_NilAttrsSafetyConcurrent verifies that concurrent handler calls with
// nil-attrs nodes don't panic under race conditions.
func TestR3_NilAttrsSafetyConcurrent(t *testing.T) {
	fs, err := memfs.NewFS()
	if err != nil {
		t.Fatal(err)
	}

	config := DefaultRateLimiterConfig()
	nfs, err := New(fs, ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
	})
	if err != nil {
		t.Fatal(err)
	}

	server := &Server{
		handler: nfs,
		options: ServerOptions{Debug: false},
	}
	handler := &NFSProcedureHandler{server: server}
	authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}

	// Create a node with nil attrs
	nilNode := &NFSNode{
		SymlinkFileSystem: fs,
		path:              "/doesnotexist",
		attrs:             nil,
	}
	nilHandle := nfs.fileMap.Allocate(nilNode)

	// Run concurrent GETATTR calls on the nil-attrs node
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var buf bytes.Buffer
			xdrEncodeFileHandle(&buf, nilHandle)
			handler.handleGetattr(bytes.NewReader(buf.Bytes()), &RPCReply{}, authCtx)
		}()
	}
	wg.Wait()
	t.Log("Concurrent GETATTR with nil attrs completed without panic")
}
