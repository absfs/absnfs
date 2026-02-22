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
		if result.AcceptStatus != PROG_MISMATCH {
			t.Errorf("Expected PROG_MISMATCH AcceptStatus, got %v", result.AcceptStatus)
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
		if result.AcceptStatus != PROC_UNAVAIL {
			t.Errorf("Expected PROC_UNAVAIL AcceptStatus, got %v", result.AcceptStatus)
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
		if result.AcceptStatus != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS AcceptStatus, got %v", result.AcceptStatus)
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
		// Add XDR padding
		padding := (4 - (len(path) % 4)) % 4
		if padding > 0 {
			buf.Write(make([]byte, padding))
		}

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
		if result.AcceptStatus != GARBAGE_ARGS {
			t.Errorf("Expected GARBAGE_ARGS AcceptStatus, got %v", result.AcceptStatus)
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
		// Add XDR padding
		padding := (4 - (len(path) % 4)) % 4
		if padding > 0 {
			buf.Write(make([]byte, padding))
		}

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
		// Add XDR padding
		padding := (4 - (len(path) % 4)) % 4
		if padding > 0 {
			buf.Write(make([]byte, padding))
		}

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

// ================================================================
// Coverage boost: handleMountCall – path validation, export, version checks
// ================================================================

func TestCovBoost_HandleMountCall_MNT(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	var buf bytes.Buffer
	xdrEncodeString(&buf, "/")

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   MOUNT_PROGRAM,
			Version:   MOUNT_V3,
			Procedure: 1, // MNT
		},
	}

	reply := &RPCReply{}
	result, err := handler.handleMountCall(call, bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleMountCall: %v", err)
	}
	data := result.Data.([]byte)
	status := binary.BigEndian.Uint32(data[0:4])
	if status != 0 {
		t.Errorf("expected MNT3_OK (0), got %d", status)
	}
}

func TestCovBoost_HandleMountCall_MNTNonexistent(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	var buf bytes.Buffer
	xdrEncodeString(&buf, "/nonexistent/deep/path")

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   MOUNT_PROGRAM,
			Version:   MOUNT_V3,
			Procedure: 1,
		},
	}

	reply := &RPCReply{}
	result, err := handler.handleMountCall(call, bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleMountCall: %v", err)
	}
	data := result.Data.([]byte)
	status := binary.BigEndian.Uint32(data[0:4])
	if status != 2 { // MNT3ERR_NOENT
		t.Errorf("expected MNT3ERR_NOENT (2), got %d", status)
	}
}

func TestCovBoost_HandleMountCall_EXPORT(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   MOUNT_PROGRAM,
			Version:   MOUNT_V3,
			Procedure: 5, // EXPORT
		},
	}

	reply := &RPCReply{}
	result, err := handler.handleMountCall(call, bytes.NewReader([]byte{}), reply, auth)
	if err != nil {
		t.Fatalf("handleMountCall: %v", err)
	}
	if result.Data == nil {
		t.Error("expected data for EXPORT")
	}
}

func TestCovBoost_HandleMountCall_DUMP(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   MOUNT_PROGRAM,
			Version:   MOUNT_V3,
			Procedure: 2, // DUMP
		},
	}

	reply := &RPCReply{}
	result, err := handler.handleMountCall(call, bytes.NewReader([]byte{}), reply, auth)
	if err != nil {
		t.Fatalf("handleMountCall: %v", err)
	}
	if result.Data == nil {
		t.Error("expected data for DUMP")
	}
}

func TestCovBoost_HandleMountCall_UMNTALL(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   MOUNT_PROGRAM,
			Version:   MOUNT_V3,
			Procedure: 4, // UMNTALL
		},
	}

	reply := &RPCReply{}
	_, err := handler.handleMountCall(call, bytes.NewReader([]byte{}), reply, auth)
	if err != nil {
		t.Fatalf("handleMountCall UMNTALL: %v", err)
	}
}

func TestCovBoost_HandleMountCall_VersionMismatch(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   MOUNT_PROGRAM,
			Version:   99, // bad version
			Procedure: 0,
		},
	}

	reply := &RPCReply{}
	result, err := handler.handleMountCall(call, bytes.NewReader([]byte{}), reply, auth)
	if err != nil {
		t.Fatalf("handleMountCall: %v", err)
	}
	if result.AcceptStatus != PROG_MISMATCH {
		t.Errorf("expected PROG_MISMATCH, got %d", result.AcceptStatus)
	}
}

func TestCovBoost_HandleMountCall_UnknownProc(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   MOUNT_PROGRAM,
			Version:   MOUNT_V3,
			Procedure: 99,
		},
	}

	reply := &RPCReply{}
	result, err := handler.handleMountCall(call, bytes.NewReader([]byte{}), reply, auth)
	if err != nil {
		t.Fatalf("handleMountCall: %v", err)
	}
	if result.AcceptStatus != PROC_UNAVAIL {
		t.Errorf("expected PROC_UNAVAIL, got %d", result.AcceptStatus)
	}
}

func TestCovBoost_HandleMountCall_V1(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   MOUNT_PROGRAM,
			Version:   1, // v1
			Procedure: 0,
		},
	}

	reply := &RPCReply{}
	_, err := handler.handleMountCall(call, bytes.NewReader([]byte{}), reply, auth)
	if err != nil {
		t.Fatalf("handleMountCall v1 NULL: %v", err)
	}
}

// ================================================================
// Coverage boost: handleMountCall - UMNT procedure
// ================================================================

func TestCovBoost_HandleMountCall_UMNT(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	var buf bytes.Buffer
	xdrEncodeString(&buf, "/")

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   MOUNT_PROGRAM,
			Version:   MOUNT_V3,
			Procedure: 3, // UMNT
		},
	}

	reply := &RPCReply{}
	_, err := handler.handleMountCall(call, bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleMountCall UMNT: %v", err)
	}
}

// ================================================================
// Coverage boost: handleMountCall - MNT with a subdirectory path
// ================================================================

func TestCovBoost_HandleMountCall_MNTSubdir(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	var buf bytes.Buffer
	xdrEncodeString(&buf, "/dir")

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   MOUNT_PROGRAM,
			Version:   MOUNT_V3,
			Procedure: 1, // MNT
		},
	}

	reply := &RPCReply{}
	result, err := handler.handleMountCall(call, bytes.NewReader(buf.Bytes()), reply, auth)
	if err != nil {
		t.Fatalf("handleMountCall: %v", err)
	}
	data := result.Data.([]byte)
	status := binary.BigEndian.Uint32(data[0:4])
	if status != 0 {
		t.Errorf("expected MNT3_OK (0), got %d", status)
	}
}

// ================================================================
// Coverage boost: handleMountCall - MNT with truncated body (GARBAGE_ARGS)
// ================================================================

func TestCovBoost_HandleMountCall_MNTGarbageArgs(t *testing.T) {
	srv, handler, auth := setupHandlerEnv(t)
	_ = srv

	call := &RPCCall{
		Header: RPCMsgHeader{
			Program:   MOUNT_PROGRAM,
			Version:   MOUNT_V3,
			Procedure: 1, // MNT
		},
	}

	// Empty body - can't decode mount path
	reply := &RPCReply{}
	result, err := handler.handleMountCall(call, bytes.NewReader([]byte{}), reply, auth)
	if err != nil {
		t.Fatalf("handleMountCall: %v", err)
	}
	if result.AcceptStatus != GARBAGE_ARGS {
		t.Errorf("expected GARBAGE_ARGS, got %d", result.AcceptStatus)
	}
}
