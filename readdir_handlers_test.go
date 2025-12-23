package absnfs

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/absfs/memfs"
)

// newTestServerNoRateLimit creates a test server with rate limiting disabled
func newTestServerNoRateLimit() (*Server, error) {
	memfs, err := memfs.NewFS()
	if err != nil {
		return nil, err
	}

	// Explicitly disable rate limiting for tests
	// Need to provide RateLimitConfig to prevent New() from overwriting EnableRateLimiting
	config := DefaultRateLimiterConfig()
	fs, err := New(memfs, ExportOptions{
		EnableRateLimiting: false,
		RateLimitConfig:    &config,
	})
	if err != nil {
		return nil, err
	}

	// Create some test files and directories
	if err := memfs.Mkdir("/testdir", 0755); err != nil {
		return nil, err
	}

	f, err := memfs.Create("/testfile.txt")
	if err != nil {
		return nil, err
	}
	if _, err := f.Write([]byte("test content")); err != nil {
		f.Close()
		return nil, err
	}
	f.Close()

	server := &Server{
		handler: fs,
		options: ServerOptions{
			Debug: false,
		},
	}

	return server, nil
}

// Helper to build READDIR request
func buildReaddirRequest(handle uint64, cookie uint64, count uint32) []byte {
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, handle)
	binary.Write(&buf, binary.BigEndian, cookie)
	buf.Write(make([]byte, 8)) // cookieverf
	binary.Write(&buf, binary.BigEndian, count)
	return buf.Bytes()
}

// Helper to build READDIRPLUS request
func buildReaddirplusRequest(handle uint64, cookie uint64, dirCount, maxCount uint32) []byte {
	var buf bytes.Buffer
	xdrEncodeFileHandle(&buf, handle)
	binary.Write(&buf, binary.BigEndian, cookie)
	buf.Write(make([]byte, 8)) // cookieverf
	binary.Write(&buf, binary.BigEndian, dirCount)
	binary.Write(&buf, binary.BigEndian, maxCount)
	return buf.Bytes()
}

func TestHandleReaddir(t *testing.T) {
	server, err := newTestServerNoRateLimit()
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	handler := &NFSProcedureHandler{server: server}
	authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}

	// Get root directory handle
	rootNode, err := server.handler.Lookup("/")
	if err != nil {
		t.Fatalf("Failed to lookup root: %v", err)
	}
	rootHandle := server.handler.fileMap.Allocate(rootNode)

	// Get a subdirectory handle
	dirNode, err := server.handler.Lookup("/testdir")
	if err != nil {
		t.Fatalf("Failed to lookup testdir: %v", err)
	}
	dirHandle := server.handler.fileMap.Allocate(dirNode)

	// Get a file handle (not a directory)
	fileNode, err := server.handler.Lookup("/testfile.txt")
	if err != nil {
		t.Fatalf("Failed to lookup testfile.txt: %v", err)
	}
	fileHandle := server.handler.fileMap.Allocate(fileNode)

	t.Run("successful root directory read", func(t *testing.T) {
		body := buildReaddirRequest(rootHandle, 0, 4096)
		reply := &RPCReply{}

		result, err := handler.handleReaddir(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleReaddir failed: %v", err)
		}

		// Check status is NFS_OK
		data := result.Data.([]byte)
		if len(data) < 4 {
			t.Fatalf("Response too short: %d bytes", len(data))
		}
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK (0), got %d", status)
		}
	})

	t.Run("successful subdirectory read", func(t *testing.T) {
		body := buildReaddirRequest(dirHandle, 0, 4096)
		reply := &RPCReply{}

		result, err := handler.handleReaddir(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleReaddir failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("with cookie pagination", func(t *testing.T) {
		// First read with cookie=0
		body := buildReaddirRequest(rootHandle, 0, 4096)
		reply := &RPCReply{}

		result, err := handler.handleReaddir(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleReaddir failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}

		// Second read with cookie=1 (skip first entry)
		body = buildReaddirRequest(rootHandle, 1, 4096)
		reply = &RPCReply{}

		result, err = handler.handleReaddir(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleReaddir with cookie failed: %v", err)
		}

		data = result.Data.([]byte)
		status = binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK with cookie, got %d", status)
		}
	})

	t.Run("small count triggers limit", func(t *testing.T) {
		// Use a very small count to trigger the reachedLimit path
		body := buildReaddirRequest(rootHandle, 0, 200)
		reply := &RPCReply{}

		result, err := handler.handleReaddir(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleReaddir failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("invalid handle - truncated", func(t *testing.T) {
		body := []byte{0x00, 0x00} // Too short
		reply := &RPCReply{}

		result, err := handler.handleReaddir(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleReaddir should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})

	t.Run("non-existent handle", func(t *testing.T) {
		body := buildReaddirRequest(999999, 0, 4096)
		reply := &RPCReply{}

		result, err := handler.handleReaddir(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleReaddir should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_NOENT {
			t.Errorf("Expected NFSERR_NOENT, got %d", status)
		}
	})

	t.Run("not a directory", func(t *testing.T) {
		body := buildReaddirRequest(fileHandle, 0, 4096)
		reply := &RPCReply{}

		result, err := handler.handleReaddir(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleReaddir should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_NOTDIR {
			t.Errorf("Expected NFSERR_NOTDIR, got %d", status)
		}
	})

	t.Run("missing cookie", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, rootHandle)
		// No cookie, cookieverf, or count
		reply := &RPCReply{}

		result, err := handler.handleReaddir(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleReaddir should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS for missing cookie, got %d", status)
		}
	})

	t.Run("missing cookieverf", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, rootHandle)
		binary.Write(&buf, binary.BigEndian, uint64(0)) // cookie only
		reply := &RPCReply{}

		result, err := handler.handleReaddir(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleReaddir should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS for missing cookieverf, got %d", status)
		}
	})

	t.Run("missing count", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, rootHandle)
		binary.Write(&buf, binary.BigEndian, uint64(0))
		buf.Write(make([]byte, 8)) // cookieverf but no count
		reply := &RPCReply{}

		result, err := handler.handleReaddir(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleReaddir should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS for missing count, got %d", status)
		}
	})
}

func TestHandleReaddirplus(t *testing.T) {
	server, err := newTestServerNoRateLimit()
	if err != nil {
		t.Fatalf("Failed to create test server: %v", err)
	}

	handler := &NFSProcedureHandler{server: server}
	authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}

	// Get root directory handle
	rootNode, err := server.handler.Lookup("/")
	if err != nil {
		t.Fatalf("Failed to lookup root: %v", err)
	}
	rootHandle := server.handler.fileMap.Allocate(rootNode)

	// Get a subdirectory handle
	dirNode, err := server.handler.Lookup("/testdir")
	if err != nil {
		t.Fatalf("Failed to lookup testdir: %v", err)
	}
	dirHandle := server.handler.fileMap.Allocate(dirNode)

	// Get a file handle (not a directory)
	fileNode, err := server.handler.Lookup("/testfile.txt")
	if err != nil {
		t.Fatalf("Failed to lookup testfile.txt: %v", err)
	}
	fileHandle := server.handler.fileMap.Allocate(fileNode)

	t.Run("successful root directory read", func(t *testing.T) {
		body := buildReaddirplusRequest(rootHandle, 0, 1024, 8192)
		reply := &RPCReply{}

		result, err := handler.handleReaddirplus(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleReaddirplus failed: %v", err)
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

	t.Run("successful subdirectory read", func(t *testing.T) {
		body := buildReaddirplusRequest(dirHandle, 0, 1024, 8192)
		reply := &RPCReply{}

		result, err := handler.handleReaddirplus(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleReaddirplus failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK, got %d", status)
		}
	})

	t.Run("with cookie pagination", func(t *testing.T) {
		body := buildReaddirplusRequest(rootHandle, 1, 1024, 8192)
		reply := &RPCReply{}

		result, err := handler.handleReaddirplus(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleReaddirplus failed: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFS_OK {
			t.Errorf("Expected NFS_OK with cookie, got %d", status)
		}
	})

	t.Run("small maxCount triggers limit", func(t *testing.T) {
		// Use a very small maxCount to trigger the reachedLimit path
		body := buildReaddirplusRequest(rootHandle, 0, 100, 400)
		reply := &RPCReply{}

		result, err := handler.handleReaddirplus(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleReaddirplus failed: %v", err)
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

		result, err := handler.handleReaddirplus(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleReaddirplus should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})

	t.Run("non-existent handle", func(t *testing.T) {
		body := buildReaddirplusRequest(999999, 0, 1024, 8192)
		reply := &RPCReply{}

		result, err := handler.handleReaddirplus(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleReaddirplus should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_NOENT {
			t.Errorf("Expected NFSERR_NOENT, got %d", status)
		}
	})

	t.Run("not a directory", func(t *testing.T) {
		body := buildReaddirplusRequest(fileHandle, 0, 1024, 8192)
		reply := &RPCReply{}

		result, err := handler.handleReaddirplus(bytes.NewReader(body), reply, authCtx)
		if err != nil {
			t.Fatalf("handleReaddirplus should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != NFSERR_NOTDIR {
			t.Errorf("Expected NFSERR_NOTDIR, got %d", status)
		}
	})

	t.Run("missing cookie", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, rootHandle)
		reply := &RPCReply{}

		result, err := handler.handleReaddirplus(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleReaddirplus should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})

	t.Run("missing cookieverf", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, rootHandle)
		binary.Write(&buf, binary.BigEndian, uint64(0))
		reply := &RPCReply{}

		result, err := handler.handleReaddirplus(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleReaddirplus should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})

	t.Run("missing dirCount", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, rootHandle)
		binary.Write(&buf, binary.BigEndian, uint64(0))
		buf.Write(make([]byte, 8))
		reply := &RPCReply{}

		result, err := handler.handleReaddirplus(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleReaddirplus should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})

	t.Run("missing maxCount", func(t *testing.T) {
		var buf bytes.Buffer
		xdrEncodeFileHandle(&buf, rootHandle)
		binary.Write(&buf, binary.BigEndian, uint64(0))
		buf.Write(make([]byte, 8))
		binary.Write(&buf, binary.BigEndian, uint32(1024))
		reply := &RPCReply{}

		result, err := handler.handleReaddirplus(bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleReaddirplus should not return error: %v", err)
		}

		data := result.Data.([]byte)
		status := binary.BigEndian.Uint32(data[0:4])
		if status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS, got %d", status)
		}
	})
}

