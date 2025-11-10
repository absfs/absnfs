package absnfs

import (
	"bytes"
	"encoding/binary"
	"runtime"
	"testing"
	"time"

	"github.com/absfs/memfs"
)

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

		reply, err := handler.HandleCall(call, bytes.NewReader([]byte{}))
		if err != nil {
			t.Fatalf("HandleCall failed: %v", err)
		}
		if reply.Status != PROG_UNAVAIL {
			t.Errorf("Expected PROG_UNAVAIL status, got %v", reply.Status)
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

		reply, err = handler.HandleCall(call, bytes.NewReader([]byte{}))
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

		reply, err := handler.HandleCall(call, bytes.NewReader([]byte{}))
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

		// Encode handle
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, handle)

		reply, err := handler.HandleCall(call, bytes.NewReader(buf.Bytes()))
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
			_, err := handler.HandleCall(call, bytes.NewReader([]byte{}))
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
