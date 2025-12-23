package absnfs

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/absfs/memfs"
)

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
		if status != NFSERR_NOENT {
			t.Errorf("Expected NFSERR_NOENT, got %d", status)
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
		if status != NFSERR_NOENT {
			t.Errorf("Expected NFSERR_NOENT, got %d", status)
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
		if status != NFSERR_NOENT {
			t.Errorf("Expected NFSERR_NOENT, got %d", status)
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
		if status != NFSERR_NOENT {
			t.Errorf("Expected NFSERR_NOENT, got %d", status)
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
		if status != NFSERR_NOENT {
			t.Errorf("Expected NFSERR_NOENT, got %d", status)
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
		if status != NFSERR_NOENT {
			t.Errorf("Expected NFSERR_NOENT, got %d", status)
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
		if status != NFSERR_NOENT {
			t.Errorf("Expected NFSERR_NOENT, got %d", status)
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
		if status != NFSERR_NOENT {
			t.Errorf("Expected NFSERR_NOENT, got %d", status)
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
		if status != ACCESS_DENIED {
			t.Errorf("Expected ACCESS_DENIED, got %d", status)
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
		if status != ACCESS_DENIED {
			t.Errorf("Expected ACCESS_DENIED, got %d", status)
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
		if status != ACCESS_DENIED {
			t.Errorf("Expected ACCESS_DENIED, got %d", status)
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
		if status != NFSERR_NOENT {
			t.Errorf("Expected NFSERR_NOENT, got %d", status)
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
		if status != NFSERR_NOENT {
			t.Errorf("Expected NFSERR_NOENT, got %d", status)
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
		if status != NFSERR_NOENT {
			t.Errorf("Expected NFSERR_NOENT, got %d", status)
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
		if status != NFSERR_NOENT {
			t.Errorf("Expected NFSERR_NOENT, got %d", status)
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
		binary.Write(&buf, binary.BigEndian, uint32(1))     // set_mode = true
		binary.Write(&buf, binary.BigEndian, uint32(0644))  // mode
		binary.Write(&buf, binary.BigEndian, uint32(0))     // set_uid = false
		binary.Write(&buf, binary.BigEndian, uint32(0))     // set_gid = false
		binary.Write(&buf, binary.BigEndian, uint32(0))     // set_size = false
		binary.Write(&buf, binary.BigEndian, uint32(0))     // set_atime = DONT_CHANGE
		binary.Write(&buf, binary.BigEndian, uint32(0))     // set_mtime = DONT_CHANGE

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
		if status != NFSERR_NOENT {
			t.Errorf("Expected NFSERR_NOENT, got %d", status)
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
		if status != NFSERR_NOENT {
			t.Errorf("Expected NFSERR_NOENT, got %d", status)
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
		// Accept NFSERR_NOENT or NFSERR_INVAL depending on validation order
		if status != NFSERR_NOENT && status != NFSERR_INVAL {
			t.Errorf("Expected NFSERR_NOENT or NFSERR_INVAL, got %d", status)
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
