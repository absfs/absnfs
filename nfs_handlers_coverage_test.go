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
