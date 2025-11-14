package absnfs

import (
	"bytes"
	"encoding/binary"
	"log"
	"os"
	"testing"

	"github.com/absfs/memfs"
)

func TestHandleMountCall(t *testing.T) {
	t.Run("version mismatch", func(t *testing.T) {
		memfs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		fs, err := New(memfs, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		call := &RPCCall{
			Header: RPCMsgHeader{
				Xid:        1,
				MsgType:    RPC_CALL,
				RPCVersion: 2,
				Program:    MOUNT_PROGRAM,
				Version:    2, // Wrong version
				Procedure:  0,
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

		handler := &NFSProcedureHandler{
			server: &Server{
				handler: fs,
				options: ServerOptions{
					Debug: false,
				},
			},
		}

		reply := &RPCReply{
			Header: call.Header,
			Status: MSG_ACCEPTED,
		}

		authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err := handler.handleMountCall(call, bytes.NewReader([]byte{}), reply, authCtx)
		if err != nil {
			t.Fatalf("handleMountCall failed: %v", err)
		}
		if result.Status != PROG_MISMATCH {
			t.Errorf("Expected PROG_MISMATCH status, got %v", result.Status)
		}
	})

	t.Run("invalid procedure", func(t *testing.T) {
		memfs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		fs, err := New(memfs, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		call := &RPCCall{
			Header: RPCMsgHeader{
				Xid:        1,
				MsgType:    RPC_CALL,
				RPCVersion: 2,
				Program:    MOUNT_PROGRAM,
				Version:    MOUNT_V3,
				Procedure:  99, // Invalid procedure
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

		handler := &NFSProcedureHandler{
			server: &Server{
				handler: fs,
				options: ServerOptions{
					Debug: false,
				},
			},
		}

		reply := &RPCReply{
			Header: call.Header,
			Status: MSG_ACCEPTED,
		}

		authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err := handler.handleMountCall(call, bytes.NewReader([]byte{}), reply, authCtx)
		if err != nil {
			t.Fatalf("handleMountCall failed: %v", err)
		}
		if result.Status != PROC_UNAVAIL {
			t.Errorf("Expected PROC_UNAVAIL status, got %v", result.Status)
		}
	})

	t.Run("mount with invalid path", func(t *testing.T) {
		memfs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		fs, err := New(memfs, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		call := &RPCCall{
			Header: RPCMsgHeader{
				Xid:        1,
				MsgType:    RPC_CALL,
				RPCVersion: 2,
				Program:    MOUNT_PROGRAM,
				Version:    MOUNT_V3,
				Procedure:  1, // MNT
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

		handler := &NFSProcedureHandler{
			server: &Server{
				handler: fs,
				options: ServerOptions{
					Debug: false,
				},
			},
		}

		reply := &RPCReply{
			Header: call.Header,
			Status: MSG_ACCEPTED,
		}

		// Test with invalid path encoding
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(1)) // Invalid length
		authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err := handler.handleMountCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleMountCall failed: %v", err)
		}
		if result.Status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS status, got %v", result.Status)
		}
	})

	t.Run("mount with non-existent path", func(t *testing.T) {
		memfs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		fs, err := New(memfs, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		call := &RPCCall{
			Header: RPCMsgHeader{
				Xid:        1,
				MsgType:    RPC_CALL,
				RPCVersion: 2,
				Program:    MOUNT_PROGRAM,
				Version:    MOUNT_V3,
				Procedure:  1, // MNT
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

		handler := &NFSProcedureHandler{
			server: &Server{
				handler: fs,
				options: ServerOptions{
					Debug: false,
				},
			},
		}

		reply := &RPCReply{
			Header: call.Header,
			Status: MSG_ACCEPTED,
		}

		// Test with non-existent path
		var buf bytes.Buffer
		path := "/nonexistent"
		binary.Write(&buf, binary.BigEndian, uint32(len(path)))
		buf.WriteString(path)

		authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err := handler.handleMountCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleMountCall failed: %v", err)
		}
		if result.Status != MSG_ACCEPTED {
			t.Errorf("Expected MSG_ACCEPTED status, got %v", result.Status)
		}
		if result.Data != nil {
			data := result.Data.([]byte)
			r := bytes.NewReader(data)
			var status uint32
			if err := binary.Read(r, binary.BigEndian, &status); err != nil {
				t.Fatalf("Failed to read status from reply data: %v", err)
			}
			if status != NFSERR_NOENT {
				t.Errorf("Expected NFSERR_NOENT in reply data, got %v", status)
			}
		}
	})

	t.Run("unmount with invalid path", func(t *testing.T) {
		memfs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		fs, err := New(memfs, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		call := &RPCCall{
			Header: RPCMsgHeader{
				Xid:        1,
				MsgType:    RPC_CALL,
				RPCVersion: 2,
				Program:    MOUNT_PROGRAM,
				Version:    MOUNT_V3,
				Procedure:  3, // UMNT
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

		handler := &NFSProcedureHandler{
			server: &Server{
				handler: fs,
				options: ServerOptions{
					Debug: true,
				},
			},
		}

		reply := &RPCReply{
			Header: call.Header,
			Status: MSG_ACCEPTED,
		}

		// Test with invalid path encoding
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(1)) // Invalid length
		authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err := handler.handleMountCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleMountCall failed: %v", err)
		}
		if result.Status != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS status, got %v", result.Status)
		}
	})

	t.Run("mount with valid path", func(t *testing.T) {
		memfs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		fs, err := New(memfs, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		call := &RPCCall{
			Header: RPCMsgHeader{
				Xid:        1,
				MsgType:    RPC_CALL,
				RPCVersion: 2,
				Program:    MOUNT_PROGRAM,
				Version:    MOUNT_V3,
				Procedure:  1, // MNT
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

		handler := &NFSProcedureHandler{
			server: &Server{
				handler: fs,
				options: ServerOptions{
					Debug: false,
				},
			},
		}

		reply := &RPCReply{
			Header: call.Header,
			Status: MSG_ACCEPTED,
		}

		// Test with valid path (root directory)
		var buf bytes.Buffer
		path := "/"
		binary.Write(&buf, binary.BigEndian, uint32(len(path)))
		buf.WriteString(path)

		authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err := handler.handleMountCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleMountCall failed: %v", err)
		}
		if result.Status != MSG_ACCEPTED {
			t.Errorf("Expected MSG_ACCEPTED status, got %v", result.Status)
		}
		if result.Data != nil {
			data := result.Data.([]byte)
			r := bytes.NewReader(data)
			var status uint32
			if err := binary.Read(r, binary.BigEndian, &status); err != nil {
				t.Fatalf("Failed to read status from reply data: %v", err)
			}
			if status != NFS_OK {
				t.Errorf("Expected NFS_OK in reply data, got %v", status)
			}
			
			// Skip validation of the actual handle value since it might be 
			// implementation-dependent. Just check we can read it.
			var handle uint32
			if err := binary.Read(r, binary.BigEndian, &handle); err != nil {
				t.Fatalf("Failed to read handle from reply data: %v", err)
			}
		}
	})

	t.Run("dump mounts", func(t *testing.T) {
		memfs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		fs, err := New(memfs, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		call := &RPCCall{
			Header: RPCMsgHeader{
				Xid:        1,
				MsgType:    RPC_CALL,
				RPCVersion: 2,
				Program:    MOUNT_PROGRAM,
				Version:    MOUNT_V3,
				Procedure:  2, // DUMP
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

		handler := &NFSProcedureHandler{
			server: &Server{
				handler: fs,
				options: ServerOptions{
					Debug: false,
				},
			},
		}

		reply := &RPCReply{
			Header: call.Header,
			Status: MSG_ACCEPTED,
		}

		authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err := handler.handleMountCall(call, bytes.NewReader([]byte{}), reply, authCtx)
		if err != nil {
			t.Fatalf("handleMountCall failed: %v", err)
		}
		if result.Status != MSG_ACCEPTED {
			t.Errorf("Expected MSG_ACCEPTED status, got %v", result.Status)
		}
		if result.Data != nil {
			data := result.Data.([]byte)
			r := bytes.NewReader(data)
			var count uint32
			if err := binary.Read(r, binary.BigEndian, &count); err != nil {
				t.Fatalf("Failed to read entry count from reply data: %v", err)
			}
			if count != 0 {
				t.Errorf("Expected empty list (0 entries), got %d entries", count)
			}
		}
	})

	t.Run("successful unmount", func(t *testing.T) {
		memfs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		fs, err := New(memfs, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		call := &RPCCall{
			Header: RPCMsgHeader{
				Xid:        1,
				MsgType:    RPC_CALL,
				RPCVersion: 2,
				Program:    MOUNT_PROGRAM,
				Version:    MOUNT_V3,
				Procedure:  3, // UMNT
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

		// Initialize with a logger to avoid nil pointer dereference
		logger := log.New(os.Stderr, "", log.LstdFlags)
		handler := &NFSProcedureHandler{
			server: &Server{
				handler: fs,
				options: ServerOptions{
					Debug: true,
				},
				logger: logger,
			},
		}

		reply := &RPCReply{
			Header: call.Header,
			Status: MSG_ACCEPTED,
		}

		// Test with valid path
		var buf bytes.Buffer
		path := "/"
		binary.Write(&buf, binary.BigEndian, uint32(len(path)))
		buf.WriteString(path)

		authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err := handler.handleMountCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleMountCall failed: %v", err)
		}
		if result.Status != MSG_ACCEPTED {
			t.Errorf("Expected MSG_ACCEPTED status, got %v", result.Status)
		}
		
		// UMNT returns an empty response, so no data to check
	})
}
