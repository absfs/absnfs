package absnfs

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestAdditionalNFSOperations(t *testing.T) {
	// Skip file/dir creation operations for now to avoid FS access issues
	t.Run("core NFS operations", func(t *testing.T) {
		server, err := newTestServer()
		if err != nil {
			t.Fatalf("Failed to create test server: %v", err)
		}

		handler := &NFSProcedureHandler{server: server}

		// Set up real test handles
		// Get root directory handle
		rootNode, err := server.handler.Lookup("/")
		if err != nil {
			t.Fatalf("Failed to lookup root directory: %v", err)
		}
		rootHandle := server.handler.fileMap.Allocate(rootNode)

		// Get file handle
		fileNode, err := server.handler.Lookup("/testfile.txt")
		if err != nil {
			t.Fatalf("Failed to lookup test file: %v", err)
		}
		fileHandle := server.handler.fileMap.Allocate(fileNode)

		// Get directory handle
		dirNode, err := server.handler.Lookup("/testdir")
		if err != nil {
			t.Fatalf("Failed to lookup test directory: %v", err)
		}
		dirHandle := server.handler.fileMap.Allocate(dirNode)

		t.Run("READDIR operation", func(t *testing.T) {
			call := &RPCCall{
				Header: RPCMsgHeader{
					Version:   NFS_V3,
					Procedure: NFSPROC3_READDIR,
				},
			}

			var buf bytes.Buffer
			binary.Write(&buf, binary.BigEndian, dirHandle)
			binary.Write(&buf, binary.BigEndian, uint64(0)) // cookie
			// cookieverf (8 bytes)
			for i := 0; i < 8; i++ {
				buf.WriteByte(0)
			}
			binary.Write(&buf, binary.BigEndian, uint32(1024)) // count

			reply := &RPCReply{}
			authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
			result, err := handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
			if err != nil {
				t.Fatalf("handleNFSCall failed for READDIR: %v", err)
			}
			if result.Status != MSG_ACCEPTED {
				t.Errorf("Expected MSG_ACCEPTED status, got %v", result.Status)
			}
		})

		t.Run("READDIRPLUS operation", func(t *testing.T) {
			call := &RPCCall{
				Header: RPCMsgHeader{
					Version:   NFS_V3,
					Procedure: NFSPROC3_READDIRPLUS,
				},
			}

			var buf bytes.Buffer
			binary.Write(&buf, binary.BigEndian, dirHandle)
			binary.Write(&buf, binary.BigEndian, uint64(0)) // cookie
			// cookieverf (8 bytes)
			for i := 0; i < 8; i++ {
				buf.WriteByte(0)
			}
			binary.Write(&buf, binary.BigEndian, uint32(1024)) // dircount
			binary.Write(&buf, binary.BigEndian, uint32(4096)) // maxcount

			reply := &RPCReply{}
			authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
			result, err := handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
			if err != nil {
				t.Fatalf("handleNFSCall failed for READDIRPLUS: %v", err)
			}
			if result.Status != MSG_ACCEPTED {
				t.Errorf("Expected MSG_ACCEPTED status, got %v", result.Status)
			}
		})

		t.Run("FSSTAT operation", func(t *testing.T) {
			call := &RPCCall{
				Header: RPCMsgHeader{
					Version:   NFS_V3,
					Procedure: NFSPROC3_FSSTAT,
				},
			}

			var buf bytes.Buffer
			binary.Write(&buf, binary.BigEndian, rootHandle)

			reply := &RPCReply{}
			authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
			result, err := handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
			if err != nil {
				t.Fatalf("handleNFSCall failed for FSSTAT: %v", err)
			}
			if result.Status != MSG_ACCEPTED {
				t.Errorf("Expected MSG_ACCEPTED status, got %v", result.Status)
			}
		})

		t.Run("FSINFO operation", func(t *testing.T) {
			call := &RPCCall{
				Header: RPCMsgHeader{
					Version:   NFS_V3,
					Procedure: NFSPROC3_FSINFO,
				},
			}

			var buf bytes.Buffer
			binary.Write(&buf, binary.BigEndian, rootHandle)

			reply := &RPCReply{}
			authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
			result, err := handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
			if err != nil {
				t.Fatalf("handleNFSCall failed for FSINFO: %v", err)
			}
			if result.Status != MSG_ACCEPTED {
				t.Errorf("Expected MSG_ACCEPTED status, got %v", result.Status)
			}
		})

		t.Run("PATHCONF operation", func(t *testing.T) {
			call := &RPCCall{
				Header: RPCMsgHeader{
					Version:   NFS_V3,
					Procedure: NFSPROC3_PATHCONF,
				},
			}

			var buf bytes.Buffer
			binary.Write(&buf, binary.BigEndian, fileHandle)

			reply := &RPCReply{}
			authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
			result, err := handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
			if err != nil {
				t.Fatalf("handleNFSCall failed for PATHCONF: %v", err)
			}
			if result.Status != MSG_ACCEPTED {
				t.Errorf("Expected MSG_ACCEPTED status, got %v", result.Status)
			}
		})

		t.Run("ACCESS operation", func(t *testing.T) {
			call := &RPCCall{
				Header: RPCMsgHeader{
					Version:   NFS_V3,
					Procedure: NFSPROC3_ACCESS,
				},
			}

			var buf bytes.Buffer
			binary.Write(&buf, binary.BigEndian, fileHandle)
			binary.Write(&buf, binary.BigEndian, uint32(0x1F)) // All access bits

			reply := &RPCReply{}
			authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
			result, err := handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
			if err != nil {
				t.Fatalf("handleNFSCall failed for ACCESS: %v", err)
			}
			if result.Status != MSG_ACCEPTED {
				t.Errorf("Expected MSG_ACCEPTED status, got %v", result.Status)
			}
		})

		t.Run("COMMIT operation", func(t *testing.T) {
			call := &RPCCall{
				Header: RPCMsgHeader{
					Version:   NFS_V3,
					Procedure: NFSPROC3_COMMIT,
				},
			}

			var buf bytes.Buffer
			binary.Write(&buf, binary.BigEndian, fileHandle)
			binary.Write(&buf, binary.BigEndian, uint64(0))    // offset
			binary.Write(&buf, binary.BigEndian, uint32(1024)) // count

			reply := &RPCReply{}
			authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
			result, err := handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
			if err != nil {
				t.Fatalf("handleNFSCall failed for COMMIT: %v", err)
			}
			if result.Status != MSG_ACCEPTED {
				t.Errorf("Expected MSG_ACCEPTED status, got %v", result.Status)
			}
		})

		t.Run("MKDIR operation", func(t *testing.T) {
			call := &RPCCall{
				Header: RPCMsgHeader{
					Version:   NFS_V3,
					Procedure: NFSPROC3_MKDIR,
				},
			}

			var buf bytes.Buffer
			binary.Write(&buf, binary.BigEndian, rootHandle)
			xdrEncodeString(&buf, "newdir")
			// attributes
			binary.Write(&buf, binary.BigEndian, uint32(1))    // set mode
			binary.Write(&buf, binary.BigEndian, uint32(0755)) // mode
			binary.Write(&buf, binary.BigEndian, uint32(0))    // don't set uid
			binary.Write(&buf, binary.BigEndian, uint32(0))    // don't set gid
			binary.Write(&buf, binary.BigEndian, uint32(0))    // don't set size
			binary.Write(&buf, binary.BigEndian, uint32(0))    // don't set atime
			binary.Write(&buf, binary.BigEndian, uint32(0))    // don't set mtime

			reply := &RPCReply{}
			authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
			result, err := handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
			if err != nil {
				t.Fatalf("handleNFSCall failed for MKDIR: %v", err)
			}
			if result.Status != MSG_ACCEPTED {
				t.Errorf("Expected MSG_ACCEPTED status, got %v", result.Status)
			}
		})

		t.Run("CREATE operation", func(t *testing.T) {
			call := &RPCCall{
				Header: RPCMsgHeader{
					Version:   NFS_V3,
					Procedure: NFSPROC3_CREATE,
				},
			}

			var buf bytes.Buffer
			binary.Write(&buf, binary.BigEndian, rootHandle)
			xdrEncodeString(&buf, "newfile.txt")
			binary.Write(&buf, binary.BigEndian, uint32(1)) // GUARDED
			// setattr_attributes
			binary.Write(&buf, binary.BigEndian, uint32(1))    // set mode
			binary.Write(&buf, binary.BigEndian, uint32(0644)) // mode
			binary.Write(&buf, binary.BigEndian, uint32(0))    // don't set uid
			binary.Write(&buf, binary.BigEndian, uint32(0))    // don't set gid
			binary.Write(&buf, binary.BigEndian, uint32(0))    // don't set size
			binary.Write(&buf, binary.BigEndian, uint32(0))    // don't set atime
			binary.Write(&buf, binary.BigEndian, uint32(0))    // don't set mtime

			reply := &RPCReply{}
			authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
			result, err := handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
			if err != nil {
				t.Fatalf("handleNFSCall failed for CREATE: %v", err)
			}
			if result.Status != MSG_ACCEPTED {
				t.Errorf("Expected MSG_ACCEPTED status, got %v", result.Status)
			}
		})

		t.Run("REMOVE operation", func(t *testing.T) {
			call := &RPCCall{
				Header: RPCMsgHeader{
					Version:   NFS_V3,
					Procedure: NFSPROC3_REMOVE,
				},
			}

			var buf bytes.Buffer
			binary.Write(&buf, binary.BigEndian, rootHandle)
			xdrEncodeString(&buf, "dummy.txt")

			reply := &RPCReply{}
			authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
			result, err := handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
			if err != nil {
				t.Fatalf("handleNFSCall failed for REMOVE: %v", err)
			}
			if result.Status != MSG_ACCEPTED {
				t.Errorf("Expected MSG_ACCEPTED status, got %v", result.Status)
			}
		})

		t.Run("RMDIR operation", func(t *testing.T) {
			call := &RPCCall{
				Header: RPCMsgHeader{
					Version:   NFS_V3,
					Procedure: NFSPROC3_RMDIR,
				},
			}

			var buf bytes.Buffer
			binary.Write(&buf, binary.BigEndian, rootHandle)
			xdrEncodeString(&buf, "dummydir")

			reply := &RPCReply{}
			authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
			result, err := handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
			if err != nil {
				t.Fatalf("handleNFSCall failed for RMDIR: %v", err)
			}
			if result.Status != MSG_ACCEPTED {
				t.Errorf("Expected MSG_ACCEPTED status, got %v", result.Status)
			}
		})

		t.Run("RENAME operation", func(t *testing.T) {
			call := &RPCCall{
				Header: RPCMsgHeader{
					Version:   NFS_V3,
					Procedure: NFSPROC3_RENAME,
				},
			}

			var buf bytes.Buffer
			binary.Write(&buf, binary.BigEndian, rootHandle) // from dir
			xdrEncodeString(&buf, "old.txt")                 // from name
			binary.Write(&buf, binary.BigEndian, rootHandle) // to dir
			xdrEncodeString(&buf, "new.txt")                 // to name

			reply := &RPCReply{}
			authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
			result, err := handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
			if err != nil {
				t.Fatalf("handleNFSCall failed for RENAME: %v", err)
			}
			if result.Status != MSG_ACCEPTED {
				t.Errorf("Expected MSG_ACCEPTED status, got %v", result.Status)
			}
		})
	})
}
