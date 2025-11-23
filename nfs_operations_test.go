package absnfs

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"

	"github.com/absfs/memfs"
)

func newTestServer() (*Server, error) {
	memfs, err := memfs.NewFS()
	if err != nil {
		return nil, err
	}

	fs, err := New(memfs, ExportOptions{})
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

	// Create a test server
	server := &Server{
		handler: fs,
		options: ServerOptions{
			Debug: false,
		},
	}

	return server, nil
}

func TestNFSOperationsErrors(t *testing.T) {
	t.Run("version mismatch", func(t *testing.T) {
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
				Version:    2, // Wrong version
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

		reply := &RPCReply{
			Header: call.Header,
			Status: MSG_ACCEPTED,
		}

		authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err := handler.handleNFSCall(call, bytes.NewReader([]byte{}), reply, authCtx)
		if err != nil {
			t.Fatalf("handleNFSCall failed: %v", err)
		}
		if result.Status != PROG_MISMATCH {
			t.Errorf("Expected PROG_MISMATCH status, got %v", result.Status)
		}
	})

	t.Run("read operation", func(t *testing.T) {
		memfs, err := memfs.NewFS()
		if err != nil {
			t.Fatalf("Failed to create memfs: %v", err)
		}

		fs, err := New(memfs, ExportOptions{})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		// Create test file with content
		f, err := memfs.Create("/test.txt")
		if err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
		content := []byte("test data")
		if _, err := f.Write(content); err != nil {
			t.Fatalf("Failed to write test data: %v", err)
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
				Procedure:  NFSPROC3_READ,
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

		reply := &RPCReply{
			Header: call.Header,
			Status: MSG_ACCEPTED,
		}

		// Test read at offset 0
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, handle)
		binary.Write(&buf, binary.BigEndian, uint64(0)) // offset
		binary.Write(&buf, binary.BigEndian, uint32(4)) // count

		authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err := handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleNFSCall failed: %v", err)
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
		}

		// Test read beyond EOF
		buf.Reset()
		binary.Write(&buf, binary.BigEndian, handle)
		binary.Write(&buf, binary.BigEndian, uint64(100)) // offset beyond EOF
		binary.Write(&buf, binary.BigEndian, uint32(4))   // count

		authCtx = &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err = handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleNFSCall failed: %v", err)
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
		}
	})

	t.Run("write operation", func(t *testing.T) {
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
				Procedure:  NFSPROC3_WRITE,
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

		reply := &RPCReply{
			Header: call.Header,
			Status: MSG_ACCEPTED,
		}

		// Test write operation
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, handle)
		binary.Write(&buf, binary.BigEndian, uint64(0)) // offset
		binary.Write(&buf, binary.BigEndian, uint32(4)) // count
		binary.Write(&buf, binary.BigEndian, uint32(1)) // stable
		binary.Write(&buf, binary.BigEndian, uint32(4)) // data length
		buf.Write([]byte("test"))

		authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err := handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleNFSCall failed: %v", err)
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
		}

		// Test write in read-only mode
		fs, err = New(memfs, ExportOptions{ReadOnly: true})
		if err != nil {
			t.Fatalf("Failed to create NFS: %v", err)
		}

		handler = &NFSProcedureHandler{
			server: &Server{
				handler: fs,
				options: ServerOptions{
					Debug: false,
				},
			},
		}

		authCtx = &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err = handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleNFSCall failed: %v", err)
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
			if status != ACCESS_DENIED {
				t.Errorf("Expected ACCESS_DENIED in reply data, got %v", status)
			}
		}
	})

	t.Run("setattr operation", func(t *testing.T) {
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
				Procedure:  NFSPROC3_SETATTR,
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

		reply := &RPCReply{
			Header: call.Header,
			Status: MSG_ACCEPTED,
		}

		// Test setattr operation
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, handle)
		binary.Write(&buf, binary.BigEndian, uint32(1))    // Set mode
		binary.Write(&buf, binary.BigEndian, uint32(0644)) // New mode
		binary.Write(&buf, binary.BigEndian, uint32(1))    // Set uid
		binary.Write(&buf, binary.BigEndian, uint32(1000)) // New uid
		binary.Write(&buf, binary.BigEndian, uint32(1))    // Set gid
		binary.Write(&buf, binary.BigEndian, uint32(1000)) // New gid

		authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err := handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleNFSCall failed: %v", err)
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
		}

		// Test invalid mode
		buf.Reset()
		binary.Write(&buf, binary.BigEndian, handle)
		binary.Write(&buf, binary.BigEndian, uint32(1))      // Set mode
		binary.Write(&buf, binary.BigEndian, uint32(0x8000)) // Invalid mode

		authCtx = &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err = handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleNFSCall failed: %v", err)
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
			if status != NFSERR_INVAL {
				t.Errorf("Expected NFSERR_INVAL in reply data, got %v", status)
			}
		}
	})

	t.Run("NFSPROC3_CREATE operation", func(t *testing.T) {
		server, err := newTestServer()
		if err != nil {
			t.Fatalf("Failed to create test server: %v", err)
		}

		// Get directory handle
		dirNode, err := server.handler.Lookup("/")
		if err != nil {
			t.Fatalf("Failed to lookup root directory: %v", err)
		}
		dirHandle := server.handler.fileMap.Allocate(dirNode)

		handler := &NFSProcedureHandler{
			server: server,
		}

		call := &RPCCall{
			Header: RPCMsgHeader{
				Xid:        1,
				MsgType:    RPC_CALL,
				RPCVersion: 2,
				Program:    NFS_PROGRAM,
				Version:    NFS_V3,
				Procedure:  NFSPROC3_CREATE,
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

		reply := &RPCReply{
			Header: call.Header,
			Status: MSG_ACCEPTED,
		}

		// Test successful create operation
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, dirHandle)
		
		// Write filename
		filename := "newfile.txt"
		binary.Write(&buf, binary.BigEndian, uint32(len(filename)))
		buf.WriteString(filename)
		// Add XDR padding
		padding := (4 - (len(filename) % 4)) % 4
		if padding > 0 {
			buf.Write(make([]byte, padding))
		}

		// Create mode and attributes
		binary.Write(&buf, binary.BigEndian, uint32(0)) // Create mode
		binary.Write(&buf, binary.BigEndian, uint32(0644)) // Mode

		authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err := handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleNFSCall failed: %v", err)
		}
		if result.Status != MSG_ACCEPTED {
			t.Errorf("Expected MSG_ACCEPTED status, got %v", result.Status)
		}
		
		// Verify status in reply data
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
			
			// Skip file handle
			var handle uint32
			if err := binary.Read(r, binary.BigEndian, &handle); err != nil {
				t.Fatalf("Failed to read handle from reply data: %v", err)
			}
		}
		
		// Verify file was created
		_, err = server.handler.fs.Stat("/newfile.txt")
		if err != nil {
			t.Errorf("File was not created: %v", err)
		}
		
		// Test with invalid mode
		buf.Reset()
		binary.Write(&buf, binary.BigEndian, dirHandle)
		
		// Write filename
		filename = "invalidmode.txt"
		binary.Write(&buf, binary.BigEndian, uint32(len(filename)))
		buf.WriteString(filename)
		// Add XDR padding
		padding = (4 - (len(filename) % 4)) % 4
		if padding > 0 {
			buf.Write(make([]byte, padding))
		}

		// Create mode and invalid attributes
		binary.Write(&buf, binary.BigEndian, uint32(0)) // Create mode
		binary.Write(&buf, binary.BigEndian, uint32(0x8000)) // Invalid mode

		authCtx = &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err = handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleNFSCall failed: %v", err)
		}
		
		// Verify error status in reply data
		if result.Data != nil {
			data := result.Data.([]byte)
			r := bytes.NewReader(data)
			var status uint32
			if err := binary.Read(r, binary.BigEndian, &status); err != nil {
				t.Fatalf("Failed to read status from reply data: %v", err)
			}
			if status != NFSERR_INVAL {
				t.Errorf("Expected NFSERR_INVAL in reply data, got %v", status)
			}
		}
	})

	t.Run("NFSPROC3_MKDIR operation", func(t *testing.T) {
		server, err := newTestServer()
		if err != nil {
			t.Fatalf("Failed to create test server: %v", err)
		}

		// Get directory handle
		dirNode, err := server.handler.Lookup("/")
		if err != nil {
			t.Fatalf("Failed to lookup root directory: %v", err)
		}
		dirHandle := server.handler.fileMap.Allocate(dirNode)

		handler := &NFSProcedureHandler{
			server: server,
		}

		call := &RPCCall{
			Header: RPCMsgHeader{
				Xid:        1,
				MsgType:    RPC_CALL,
				RPCVersion: 2,
				Program:    NFS_PROGRAM,
				Version:    NFS_V3,
				Procedure:  NFSPROC3_MKDIR,
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

		reply := &RPCReply{
			Header: call.Header,
			Status: MSG_ACCEPTED,
		}

		// Test successful mkdir operation
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, dirHandle)
		
		// Write dirname
		dirname := "newdir"
		binary.Write(&buf, binary.BigEndian, uint32(len(dirname)))
		buf.WriteString(dirname)
		// Add XDR padding
		padding := (4 - (len(dirname) % 4)) % 4
		if padding > 0 {
			buf.Write(make([]byte, padding))
		}

		// Mode
		binary.Write(&buf, binary.BigEndian, uint32(0755)) // Directory mode

		authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err := handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleNFSCall failed: %v", err)
		}
		if result.Status != MSG_ACCEPTED {
			t.Errorf("Expected MSG_ACCEPTED status, got %v", result.Status)
		}
		
		// Verify status in reply data
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
			
			// Skip file handle
			var handle uint32
			if err := binary.Read(r, binary.BigEndian, &handle); err != nil {
				t.Fatalf("Failed to read handle from reply data: %v", err)
			}
		}
		
		// Verify directory was created
		info, err := server.handler.fs.Stat("/newdir")
		if err != nil {
			t.Errorf("Directory was not created: %v", err)
		}
		if !info.IsDir() {
			t.Errorf("Created path is not a directory")
		}
		
		// Test with invalid mode
		buf.Reset()
		binary.Write(&buf, binary.BigEndian, dirHandle)
		
		// Write dirname
		dirname = "invalidmode"
		binary.Write(&buf, binary.BigEndian, uint32(len(dirname)))
		buf.WriteString(dirname)
		// Add XDR padding
		padding = (4 - (len(dirname) % 4)) % 4
		if padding > 0 {
			buf.Write(make([]byte, padding))
		}

		// Invalid mode
		binary.Write(&buf, binary.BigEndian, uint32(0x8000)) // Invalid mode

		authCtx = &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err = handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleNFSCall failed: %v", err)
		}
		
		// Verify error status in reply data
		if result.Data != nil {
			data := result.Data.([]byte)
			r := bytes.NewReader(data)
			var status uint32
			if err := binary.Read(r, binary.BigEndian, &status); err != nil {
				t.Fatalf("Failed to read status from reply data: %v", err)
			}
			if status != NFSERR_INVAL {
				t.Errorf("Expected NFSERR_INVAL in reply data, got %v", status)
			}
		}
	})

	t.Run("NFSPROC3_SYMLINK operation", func(t *testing.T) {
		server, err := newTestServer()
		if err != nil {
			t.Fatalf("Failed to create test server: %v", err)
		}

		// Get directory handle
		dirNode, err := server.handler.Lookup("/testdir")
		if err != nil {
			t.Fatalf("Failed to lookup test directory: %v", err)
		}
		dirHandle := server.handler.fileMap.Allocate(dirNode)

		handler := &NFSProcedureHandler{
			server: server,
		}

		call := &RPCCall{
			Header: RPCMsgHeader{
				Xid:        1,
				MsgType:    RPC_CALL,
				RPCVersion: 2,
				Program:    NFS_PROGRAM,
				Version:    NFS_V3,
				Procedure:  NFSPROC3_SYMLINK,
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

		reply := &RPCReply{
			Header: call.Header,
			Status: MSG_ACCEPTED,
		}

		// Test successful symlink operation
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, dirHandle)

		// Write symlink name
		symlinkName := "testlink"
		binary.Write(&buf, binary.BigEndian, uint32(len(symlinkName)))
		buf.WriteString(symlinkName)
		// Add XDR padding
		padding := (4 - (len(symlinkName) % 4)) % 4
		if padding > 0 {
			buf.Write(make([]byte, padding))
		}

		// Mode (for symlinks, typically 0777)
		binary.Write(&buf, binary.BigEndian, uint32(0777))

		// Write target path
		target := "/testfile.txt"
		binary.Write(&buf, binary.BigEndian, uint32(len(target)))
		buf.WriteString(target)
		// Add XDR padding
		padding = (4 - (len(target) % 4)) % 4
		if padding > 0 {
			buf.Write(make([]byte, padding))
		}

		authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err := handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleNFSCall failed: %v", err)
		}
		if result.Status != MSG_ACCEPTED {
			t.Errorf("Expected MSG_ACCEPTED status, got %v", result.Status)
		}

		// Verify status in reply data
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
		}

		// Verify symlink was created
		symlinkFS, ok := server.handler.fs.(SymlinkFileSystem)
		if !ok {
			t.Skip("Filesystem does not support symlinks")
		}
		info, err := symlinkFS.Lstat("/testdir/testlink")
		if err != nil {
			t.Errorf("Symlink was not created: %v", err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("Created path is not a symlink")
		}

		// Test with empty target (should fail)
		buf.Reset()
		binary.Write(&buf, binary.BigEndian, dirHandle)

		symlinkName = "badlink"
		binary.Write(&buf, binary.BigEndian, uint32(len(symlinkName)))
		buf.WriteString(symlinkName)
		// Add XDR padding
		padding = (4 - (len(symlinkName) % 4)) % 4
		if padding > 0 {
			buf.Write(make([]byte, padding))
		}

		binary.Write(&buf, binary.BigEndian, uint32(0777))

		// Empty target
		binary.Write(&buf, binary.BigEndian, uint32(0))

		authCtx = &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err = handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleNFSCall failed: %v", err)
		}

		// Verify error status in reply data
		if result.Data != nil {
			data := result.Data.([]byte)
			r := bytes.NewReader(data)
			var status uint32
			if err := binary.Read(r, binary.BigEndian, &status); err != nil {
				t.Fatalf("Failed to read status from reply data: %v", err)
			}
			if status != NFSERR_INVAL {
				t.Errorf("Expected NFSERR_INVAL for empty target, got %v", status)
			}
		}
	})

	t.Run("NFSPROC3_READLINK operation", func(t *testing.T) {
		server, err := newTestServer()
		if err != nil {
			t.Fatalf("Failed to create test server: %v", err)
		}

		// Create a symlink
		target := "/testfile.txt"
		symlinkFS, ok := server.handler.fs.(SymlinkFileSystem)
		if !ok {
			t.Skip("Filesystem does not support symlinks")
		}
		err = symlinkFS.Symlink(target, "/testdir/linktofile")
		if err != nil {
			t.Fatalf("Failed to create symlink: %v", err)
		}

		// Get symlink handle
		symlinkNode, err := server.handler.Lookup("/testdir/linktofile")
		if err != nil {
			t.Fatalf("Failed to lookup symlink: %v", err)
		}
		symlinkHandle := server.handler.fileMap.Allocate(symlinkNode)

		handler := &NFSProcedureHandler{
			server: server,
		}

		call := &RPCCall{
			Header: RPCMsgHeader{
				Xid:        1,
				MsgType:    RPC_CALL,
				RPCVersion: 2,
				Program:    NFS_PROGRAM,
				Version:    NFS_V3,
				Procedure:  NFSPROC3_READLINK,
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

		reply := &RPCReply{
			Header: call.Header,
			Status: MSG_ACCEPTED,
		}

		// Test successful readlink operation
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, symlinkHandle)

		authCtx := &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err := handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleNFSCall failed: %v", err)
		}
		if result.Status != MSG_ACCEPTED {
			t.Errorf("Expected MSG_ACCEPTED status, got %v", result.Status)
		}

		// Verify status in reply data
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

			// Skip post-op attributes flag and attributes
			var hasAttrs uint32
			if err := binary.Read(r, binary.BigEndian, &hasAttrs); err != nil {
				t.Fatalf("Failed to read hasAttrs: %v", err)
			}

			if hasAttrs == 1 {
				// Skip file attributes: mode, nlink, uid, gid (4 bytes each) + size, mtime, atime (8 bytes each)
				// Total: 4*4 + 8*3 = 40 bytes
				attrBuf := make([]byte, 40)
				if _, err := r.Read(attrBuf); err != nil {
					t.Fatalf("Failed to read attributes: %v", err)
				}
			}

			// Read target path length and value
			var targetLen uint32
			if err := binary.Read(r, binary.BigEndian, &targetLen); err != nil {
				t.Fatalf("Failed to read target length: %v", err)
			}

			targetBytes := make([]byte, targetLen)
			if _, err := r.Read(targetBytes); err != nil {
				t.Fatalf("Failed to read target: %v", err)
			}

			readTarget := string(targetBytes)
			if readTarget != target {
				t.Errorf("Expected target %s, got %s", target, readTarget)
			}
		}

		// Test readlink on non-symlink (should fail)
		fileNode, err := server.handler.Lookup("/testfile.txt")
		if err != nil {
			t.Fatalf("Failed to lookup test file: %v", err)
		}
		fileHandle := server.handler.fileMap.Allocate(fileNode)

		buf.Reset()
		binary.Write(&buf, binary.BigEndian, fileHandle)

		authCtx = &AuthContext{ClientIP: "127.0.0.1", ClientPort: 12345}
		result, err = handler.handleNFSCall(call, bytes.NewReader(buf.Bytes()), reply, authCtx)
		if err != nil {
			t.Fatalf("handleNFSCall failed: %v", err)
		}

		// Verify error status in reply data
		if result.Data != nil {
			data := result.Data.([]byte)
			r := bytes.NewReader(data)
			var status uint32
			if err := binary.Read(r, binary.BigEndian, &status); err != nil {
				t.Fatalf("Failed to read status from reply data: %v", err)
			}
			if status != NFSERR_INVAL {
				t.Errorf("Expected NFSERR_INVAL for non-symlink, got %v", status)
			}
		}
	})
}
